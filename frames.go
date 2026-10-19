package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/danielpaulus/go-ios/ios"
)

func framesDir() string {
	if dir := os.Getenv("IOS_DEVICE_FRAMES"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "ios-device-frames")
}

type frameEntry struct {
	Name             string   `json:"name"`
	Path             string   `json:"path"`
	ModelIdentifiers []string `json:"modelIdentifiers"`
}

func findFrame(productType string) *frameEntry {
	data, err := os.ReadFile(filepath.Join(framesDir(), "index.json"))
	if err != nil {
		return nil
	}
	var entries []frameEntry
	if json.Unmarshal(data, &entries) != nil {
		return nil
	}
	for _, entry := range entries {
		for _, identifier := range entry.ModelIdentifiers {
			if identifier == productType {
				return &entry
			}
		}
	}
	return nil
}

type deviceInfo struct {
	UDID        string `json:"udid"`
	Name        string `json:"name"`
	ProductType string `json:"productType"`
	Model       string `json:"model"`
	IOSVersion  string `json:"iosVersion"`
	Frame       string `json:"frame,omitempty"`
}

func readDeviceInfo(device ios.DeviceEntry) deviceInfo {
	info := deviceInfo{UDID: device.Properties.SerialNumber}
	if values, err := ios.GetValues(device); err == nil {
		info.Name = values.Value.DeviceName
		info.ProductType = values.Value.ProductType
		info.IOSVersion = values.Value.ProductVersion
	}
	info.Model = info.ProductType
	if frame := findFrame(info.ProductType); frame != nil {
		info.Model = frame.Name
		info.Frame = "frames/" + frame.Path + "/"
	}
	return info
}

const xcodeDeviceTypes = "/Library/Developer/CoreSimulator/Profiles/DeviceTypes"

func generateFrames() bool {
	if _, err := os.Stat(filepath.Join(framesDir(), "index.json")); err == nil {
		return false
	}
	if _, err := os.Stat(xcodeDeviceTypes); err != nil {
		return false
	}
	tool, err := exec.LookPath("ios-device-frames")
	if err != nil {
		executable, _ := os.Executable()
		tool = filepath.Join(filepath.Dir(executable), "ios-device-frames")
		if _, err := os.Stat(tool); err != nil {
			return false
		}
	}
	logf("generating device frames from Xcode (once)")
	if err := exec.Command(tool, "generate", "--output", framesDir()).Run(); err != nil {
		logf("could not generate device frames: %v", err)
		return false
	}
	return true
}
