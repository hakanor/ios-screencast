package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/forward"
	"github.com/danielpaulus/go-ios/ios/imagemounter"
	"github.com/danielpaulus/go-ios/ios/installationproxy"
	"github.com/danielpaulus/go-ios/ios/testmanagerd"
	"github.com/danielpaulus/go-ios/ios/tunnel"
)

const (
	hostExecutable   = "ScreencastHost"
	runnerExecutable = "ScreencastRunner-Runner"
	xctestConfig     = "ScreencastRunner.xctest"
	tunnelBasePort   = 28150
)

type deviceApps struct {
	host   string
	runner string
}

func cacheDir(name string) string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "ios-screencast", name)
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func findDevice(udid string) (ios.DeviceEntry, error) {
	if udid != "" {
		return ios.GetDevice(udid)
	}
	list, err := ios.ListDevices()
	if err != nil {
		return ios.DeviceEntry{}, fmt.Errorf("cannot list devices (is usbmuxd running?): %w", err)
	}
	if len(list.DeviceList) == 0 {
		return ios.DeviceEntry{}, errors.New("no device connected. Connect an iPhone with USB and trust this Mac")
	}
	return list.DeviceList[0], nil
}

// Matched by executable name, since signing tools may change bundle IDs.
func findApps(device ios.DeviceEntry) (deviceApps, error) {
	conn, err := installationproxy.New(device)
	if err != nil {
		return deviceApps{}, fmt.Errorf("cannot list apps: %w", err)
	}
	defer conn.Close()

	apps, err := conn.BrowseUserApps()
	if err != nil {
		return deviceApps{}, fmt.Errorf("cannot list apps: %w", err)
	}

	var found deviceApps
	for _, app := range apps {
		executable, _ := app[installationproxy.CFBundleExecutable].(string)
		switch executable {
		case hostExecutable:
			found.host = app.CFBundleIdentifier()
		case runnerExecutable:
			found.runner = app.CFBundleIdentifier()
		}
	}

	switch {
	case found.host == "" && found.runner == "":
		return found, errors.New("ios-screencast is not installed. Sign and install ios-screencast.ipa and ios-screencast-runner.ipa")
	case found.host == "":
		return found, errors.New("ios-screencast.ipa (host app) is not installed")
	case found.runner == "":
		return found, errors.New("ios-screencast-runner.ipa is not installed")
	}
	return found, nil
}

func connectTunnel(ctx context.Context, device ios.DeviceEntry) (ios.DeviceEntry, error) {
	version, err := ios.GetProductVersion(device)
	if err != nil {
		return device, fmt.Errorf("cannot read iOS version (is the phone unlocked and trusted?): %w", err)
	}
	if version.LessThan(ios.IOS17()) {
		return device, nil
	}

	pairRecords, err := tunnel.NewPairRecordManager(cacheDir("tunnel"))
	if err != nil {
		return device, err
	}
	udid := device.Properties.SerialNumber
	manager := tunnel.NewTunnelManagerForDevice(pairRecords, true, udid, tunnelBasePort)

	var info tunnel.Tunnel
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := manager.UpdateTunnels(ctx); err != nil {
			logf("tunnel: %v", err)
		}
		info, _ = manager.FindTunnel(udid)
		if info.Udid != "" {
			break
		}
		if time.Now().After(deadline) {
			return device, errors.New("could not start a tunnel to the device. iOS 17.0-17.3 needs `sudo ios tunnel start`")
		}
		time.Sleep(time.Second)
	}

	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = manager.Close()
				return
			case <-ticker.C:
				_ = manager.UpdateTunnels(ctx)
			}
		}
	}()

	device.UserspaceTUN = info.UserspaceTUN
	device.UserspaceTUNHost = ios.HttpApiHost()
	device.UserspaceTUNPort = info.UserspaceTUNPort

	rsd, err := ios.NewWithAddrPortDevice(info.Address, info.RsdPort, device)
	if err != nil {
		return device, fmt.Errorf("cannot reach the device through the tunnel: %w", err)
	}
	defer rsd.Close()
	provider, err := rsd.Handshake()
	if err != nil {
		return device, fmt.Errorf("tunnel handshake failed: %w", err)
	}
	tunneled, err := ios.GetDeviceWithAddress(udid, info.Address, provider)
	if err != nil {
		return device, err
	}
	tunneled.UserspaceTUN = device.UserspaceTUN
	tunneled.UserspaceTUNHost = device.UserspaceTUNHost
	tunneled.UserspaceTUNPort = device.UserspaceTUNPort
	return tunneled, nil
}

func mountDeveloperImage(device ios.DeviceEntry) error {
	path, err := imagemounter.DownloadImageFor(device, cacheDir("devimages"))
	if err != nil {
		return fmt.Errorf("cannot download the developer disk image: %w", err)
	}
	if err := imagemounter.MountImage(device, path); err != nil {
		logf("developer disk image: %v", err)
	}
	return nil
}

func forwardPorts(device ios.DeviceEntry, ports ...uint16) []*forward.ConnListener {
	var listeners []*forward.ConnListener
	for _, port := range ports {
		listener, err := forward.Forward(device, port, port)
		if err != nil {
			logf("cannot forward port %d (in use by an old forward?): %v", port, err)
			continue
		}
		listeners = append(listeners, listener)
	}
	return listeners
}

func runRunner(ctx context.Context, device ios.DeviceEntry, apps deviceApps) error {
	var testLog io.Writer = io.Discard
	if os.Getenv("SCREENCAST_DEBUG") != "" {
		testLog = os.Stderr
	}
	_, err := testmanagerd.RunTestWithConfig(ctx, testmanagerd.TestConfig{
		BundleId:           apps.host,
		TestRunnerBundleId: apps.runner,
		XctestConfigName:   xctestConfig,
		Env:                map[string]any{"SCREENCAST_HOST_BUNDLE_ID": apps.host},
		Args:               []string{},
		Device:             device,
		Listener:           testmanagerd.NewTestListener(testLog, testLog, os.TempDir()),
	})
	return err
}
