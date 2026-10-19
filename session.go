package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/danielpaulus/go-ios/ios"
)

const (
	broadcastPort uint16 = 12100
	runnerPort    uint16 = 12101

	broadcastRetry = 10 * time.Second
)

type session struct {
	options options
	device  ios.DeviceEntry
	info    deviceInfo
	apps    deviceApps

	mixer     *mixer
	web       *webServer
	broadcast *streamClient
	runner    *streamClient

	mu                sync.Mutex
	settings          Settings
	broadcastPending  bool
	broadcastAttempts int
	broadcastRotation *int
	runnerCancel      context.CancelFunc
	runnerRunning     bool
}

func newSession(o options, device ios.DeviceEntry, apps deviceApps) *session {
	s := &session{
		options:  o,
		device:   device,
		info:     readDeviceInfo(device),
		apps:     apps,
		mixer:    &mixer{mode: o.settings.Mode},
		web:      newWebServer(),
		settings: o.settings,
	}
	s.broadcast = newStreamClient("broadcast", broadcastPort, s.broadcastEvent)
	s.runner = newStreamClient("runner", runnerPort, s.runnerEvent)
	return s
}

func (s *session) run(ctx context.Context) error {
	s.mixer.onAccessUnit = func(data []byte, flags byte) {
		s.web.sendVideo(data, flags)
		if s.options.stdout {
			_, _ = os.Stdout.Write(data)
		}
	}
	s.mixer.onStatus = func(status mixerStatus) { s.web.sendText(s.statusJSON(&status)) }
	s.web.greeting = func() []byte { return s.statusJSON(nil) }
	s.web.onMessage = s.handleMessage

	addr := fmt.Sprintf("localhost:%d", s.options.port)
	errs := make(chan error, 1)
	go func() { errs <- s.web.listen(addr) }()

	for _, listener := range forwardPorts(s.device, broadcastPort, runnerPort) {
		defer listener.Close()
	}
	s.broadcast.start()
	s.runner.start()

	url := "http://" + addr
	logf("%s (%s, iOS %s), mode %s", s.info.Name, s.info.Model, s.info.IOSVersion, s.settings.Mode)
	go func() {
		if generateFrames() {
			s.mu.Lock()
			s.info = readDeviceInfo(s.device)
			s.mu.Unlock()
			s.web.sendText(s.statusJSON(nil))
		}
	}()
	logf("viewer on %s  (Ctrl+C to stop)", url)

	s.startRunner(ctx)
	go func() {
		ticker := time.NewTicker(broadcastRetry)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.startBroadcastIfNeeded()
			}
		}
	}()

	select {
	case <-ctx.Done():
		s.stopRunner()
		return nil
	case err := <-errs:
		return fmt.Errorf("viewer: %w", err)
	}
}

func (s *session) startRunner(parent context.Context) {
	s.mu.Lock()
	if s.runnerRunning {
		s.mu.Unlock()
		s.publishLog("runner already running")
		return
	}
	ctx, cancel := context.WithCancel(parent)
	s.runnerCancel = cancel
	s.runnerRunning = true
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			s.runnerRunning = false
			s.mu.Unlock()
		}()

		s.publishLog("connecting to the device")
		device, err := connectTunnel(ctx, s.device)
		if err != nil {
			s.publishLog(err.Error())
			return
		}
		if err := mountDeveloperImage(device); err != nil {
			s.publishLog(err.Error())
			return
		}
		s.publishLog("starting runner (unlock the phone if this hangs)")
		err = runRunner(ctx, device, s.apps)
		if ctx.Err() == nil {
			if err != nil {
				s.publishLog(fmt.Sprintf("runner stopped: %v", err))
			} else {
				s.publishLog("runner stopped")
			}
		}
	}()
}

func (s *session) stopRunner() {
	s.mu.Lock()
	cancel := s.runnerCancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *session) broadcastEvent(e streamEvent) {
	if e.rotation != nil {
		s.mu.Lock()
		s.broadcastRotation = e.rotation
		s.mu.Unlock()
		s.runner.send(encodePacket(packetRotation, []byte{byte(*e.rotation)}))
	}
	switch {
	case e.connected:
		s.mu.Lock()
		s.broadcastAttempts = 0
		s.mu.Unlock()
		defer s.sendSettings()
	case e.disconnected:
		defer s.sendSettings()
		s.mu.Lock()
		s.broadcastAttempts = 0
		s.mu.Unlock()
		time.AfterFunc(2*time.Second, s.startBroadcastIfNeeded)
	}
	s.mixer.broadcastEvent(e)
}

func (s *session) runnerEvent(e streamEvent) {
	if e.notice != "" {
		s.publishLog(e.notice)
		return
	}
	if e.broadcastResult != nil {
		s.onBroadcastResult(*e.broadcastResult)
		return
	}
	if e.connected {
		s.sendSettings()
		s.mu.Lock()
		rotation := s.broadcastRotation
		s.mu.Unlock()
		if rotation != nil {
			s.runner.send(encodePacket(packetRotation, []byte{byte(*rotation)}))
		}
		time.AfterFunc(2*time.Second, s.startBroadcastIfNeeded)
	}
	if e.disconnected {
		s.mu.Lock()
		s.broadcastPending = false
		s.mu.Unlock()
	}
	s.mixer.runnerEvent(e)
}

func (s *session) onBroadcastResult(result broadcastResult) {
	// The broadcast stream is the ground truth; the runner may not have noticed in time.
	if !result.started && s.mixer.currentStatus().Broadcast {
		result = broadcastResult{started: true, reason: "Broadcast started."}
	}
	s.mu.Lock()
	s.broadcastPending = false
	retry := !result.started && s.broadcastAttempts < s.options.broadcastAttempts
	if result.started {
		s.broadcastAttempts = 0
	}
	s.mu.Unlock()

	if result.started {
		s.publishLog(result.reason)
		return
	}
	if retry {
		s.publishLog(result.reason + " Trying again.")
		time.AfterFunc(3*time.Second, s.startBroadcastIfNeeded)
		return
	}
	s.publishLog(result.reason + " Press Start broadcast to try again.")
	data, _ := json.Marshal(map[string]any{"type": "broadcastFailed", "message": result.reason})
	s.web.sendText(data)
}

func (s *session) handleMessage(message map[string]any) {
	switch message["type"] {
	case "start":
		s.mu.Lock()
		s.broadcastAttempts = 0
		s.mu.Unlock()
		s.startRunner(context.Background())
	case "broadcast":
		s.mu.Lock()
		s.broadcastAttempts = 0
		s.broadcastPending = false
		s.mu.Unlock()
		s.startBroadcastIfNeeded()
	case "stop":
		s.stopRunner()
		s.publishLog("stopped runner")
	case "settings":
		s.mu.Lock()
		next := s.settings
		if mode, ok := message["mode"].(string); ok {
			next.Mode = Mode(mode)
		}
		if scale, ok := message["scale"].(float64); ok {
			next.Scale = scale
		}
		if fps, ok := message["fps"].(float64); ok {
			next.FPS = int(fps)
		}
		if bitrate, ok := message["bitrate"].(float64); ok {
			next.Bitrate = int(bitrate)
		}
		s.settings = next
		s.mu.Unlock()

		s.mixer.setMode(next.Mode)
		s.publishLog(fmt.Sprintf("settings: %s, scale %.2f, %d fps, %d Mbps", next.Mode, next.Scale, next.FPS, next.Bitrate/1_000_000))
		s.sendSettings()
		s.startBroadcastIfNeeded()
	}
	s.web.sendText(s.statusJSON(nil))
}

func (s *session) sendSettings() {
	s.mu.Lock()
	settings := s.settings
	s.mu.Unlock()
	s.broadcast.send(settingsPacket(settings))
	if settings.Mode == ModeMix && !s.mixer.currentStatus().Broadcast {
		settings.Mode = ModeRunner
	}
	s.runner.send(settingsPacket(settings))
}

func (s *session) startBroadcastIfNeeded() {
	status := s.mixer.currentStatus()
	s.mu.Lock()
	if s.settings.Mode == ModeRunner || !status.Runner || status.Broadcast ||
		s.broadcastPending || s.broadcastAttempts >= s.options.broadcastAttempts {
		s.mu.Unlock()
		return
	}
	s.broadcastAttempts++
	s.broadcastPending = true
	s.mu.Unlock()

	s.publishLog("asking runner to start the broadcast")
	command := "startBroadcast"
	if s.options.stay {
		command = "startBroadcastStay"
	}
	s.runner.send(commandPacket(command))

	time.AfterFunc(30*time.Second, func() {
		s.mu.Lock()
		s.broadcastPending = false
		s.mu.Unlock()
	})
}

func (s *session) statusJSON(status *mixerStatus) []byte {
	if status == nil {
		current := s.mixer.currentStatus()
		status = &current
	}
	s.mu.Lock()
	settings := s.settings
	info := s.info
	s.mu.Unlock()

	data, _ := json.Marshal(map[string]any{
		"type":      "status",
		"broadcast": status.Broadcast,
		"runner":    status.Runner,
		"alert":     status.Alert,
		"source":    status.Source,
		"rotation":  status.Rotation,
		"settings":  settings,
		"device":    info,
	})
	return data
}

func (s *session) publishLog(message string) {
	logf("%s", message)
	data, _ := json.Marshal(map[string]any{"type": "log", "message": message})
	s.web.sendText(data)
}
