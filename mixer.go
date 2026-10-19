package main

import "sync"

type source string

const (
	sourceBroadcast source = "broadcast"
	sourceRunner    source = "runner"
)

type mixerStatus struct {
	Broadcast bool    `json:"broadcast"`
	Runner    bool    `json:"runner"`
	Alert     bool    `json:"alert"`
	Source    *source `json:"source"`
	Rotation  int     `json:"rotation"`
}

type mixer struct {
	mu        sync.Mutex
	mode      Mode
	active    *source
	status    mixerStatus
	rotations map[source]int

	// flags: bit 0 = keyframe, bit 1 = first access unit after a source switch
	onAccessUnit func(data []byte, flags byte)
	onStatus     func(mixerStatus)
}

func (m *mixer) setMode(mode Mode) {
	m.mu.Lock()
	m.mode = mode
	m.mu.Unlock()
}

func (m *mixer) currentStatus() mixerStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

func (m *mixer) broadcastEvent(e streamEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case e.accessUnit != nil:
		m.handle(e.accessUnit, e.keyframe, sourceBroadcast)
	case e.rotation != nil:
		m.setRotation(sourceBroadcast, *e.rotation)
	case e.connected:
		m.status.Broadcast = true
		m.publish()
	case e.disconnected:
		m.status.Broadcast = false
		if m.active != nil && *m.active == sourceBroadcast {
			m.active = nil
		}
		m.publish()
	}
}

func (m *mixer) runnerEvent(e streamEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case e.accessUnit != nil:
		m.handle(e.accessUnit, e.keyframe, sourceRunner)
	case e.rotation != nil:
		m.setRotation(sourceRunner, *e.rotation)
	case e.alertState != nil:
		if *e.alertState != m.status.Alert {
			if *e.alertState {
				logf("alert visible, switching to runner")
			} else {
				logf("alert gone, switching back to broadcast")
			}
		}
		m.status.Alert = *e.alertState
		m.publish()
	case e.connected:
		m.status.Runner = true
		m.publish()
	case e.disconnected:
		m.status.Runner = false
		m.status.Alert = false
		if m.active != nil && *m.active == sourceRunner {
			m.active = nil
		}
		m.publish()
	}
}

func (m *mixer) handle(data []byte, keyframe bool, from source) {
	var wanted source
	switch m.mode {
	case ModeRunner:
		wanted = sourceRunner
	case ModeBroadcast:
		wanted = sourceBroadcast
	default:
		wanted = sourceBroadcast
		if m.status.Alert {
			wanted = sourceRunner
		}
	}

	var flags byte
	if keyframe {
		flags = 1
	}

	if m.active == nil || *m.active != from {
		if !keyframe || !(from == wanted || (m.active == nil && m.mode == ModeMix)) {
			return
		}
		logf("output -> %s", from)
		active := from
		m.active = &active
		m.status.Source = &active
		m.status.Rotation = m.rotations[from]
		flags |= 2
		m.publish()
	}

	if m.onAccessUnit != nil {
		m.onAccessUnit(data, flags)
	}
}

func (m *mixer) setRotation(from source, quarterTurns int) {
	if m.rotations == nil {
		m.rotations = map[source]int{}
	}
	m.rotations[from] = quarterTurns
	if m.active != nil && *m.active == from && m.status.Rotation != quarterTurns {
		m.status.Rotation = quarterTurns
		m.publish()
	}
}

func (m *mixer) publish() {
	snapshot := m.status
	if m.onStatus != nil {
		go m.onStatus(snapshot)
	}
}
