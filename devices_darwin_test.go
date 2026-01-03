//go:build darwin && !nodevices

package media

import (
	"context"
	"testing"
)

func TestAVFoundationProvider_ListVideoDevices(t *testing.T) {
	if !IsAVFoundationAvailable() {
		t.Skip("AVFoundation not available")
	}

	provider := NewAVFoundationProvider()
	devices, err := provider.ListVideoDevices(context.Background())
	if err != nil {
		t.Fatalf("ListVideoDevices failed: %v", err)
	}

	t.Logf("Found %d video devices:", len(devices))
	for _, d := range devices {
		t.Logf("  - %s (%s)", d.Label, d.DeviceID)
	}
}

func TestAVFoundationProvider_ListAudioInputDevices(t *testing.T) {
	if !IsAVFoundationAvailable() {
		t.Skip("AVFoundation not available")
	}

	provider := NewAVFoundationProvider()
	devices, err := provider.ListAudioInputDevices(context.Background())
	if err != nil {
		t.Fatalf("ListAudioInputDevices failed: %v", err)
	}

	t.Logf("Found %d audio input devices:", len(devices))
	for _, d := range devices {
		t.Logf("  - %s (%s)", d.Label, d.DeviceID)
	}
}

func TestCameraPermissionStatus(t *testing.T) {
	if !IsAVFoundationAvailable() {
		t.Skip("AVFoundation not available")
	}

	status := CameraPermissionStatus()
	statusStr := ""
	switch status {
	case AVAuthorizationStatusNotDetermined:
		statusStr = "not determined"
	case AVAuthorizationStatusRestricted:
		statusStr = "restricted"
	case AVAuthorizationStatusDenied:
		statusStr = "denied"
	case AVAuthorizationStatusAuthorized:
		statusStr = "authorized"
	}
	t.Logf("Camera permission status: %s (%d)", statusStr, status)
}

func TestMicrophonePermissionStatus(t *testing.T) {
	if !IsAVFoundationAvailable() {
		t.Skip("AVFoundation not available")
	}

	status := MicrophonePermissionStatus()
	statusStr := ""
	switch status {
	case AVAuthorizationStatusNotDetermined:
		statusStr = "not determined"
	case AVAuthorizationStatusRestricted:
		statusStr = "restricted"
	case AVAuthorizationStatusDenied:
		statusStr = "denied"
	case AVAuthorizationStatusAuthorized:
		statusStr = "authorized"
	}
	t.Logf("Microphone permission status: %s (%d)", statusStr, status)
}

func TestGetMediaDevices(t *testing.T) {
	if !IsAVFoundationAvailable() {
		t.Skip("AVFoundation not available")
	}

	md := GetMediaDevices()
	if md == nil {
		t.Fatal("GetMediaDevices returned nil")
	}

	devices, err := md.EnumerateDevices(context.Background())
	if err != nil {
		t.Fatalf("EnumerateDevices failed: %v", err)
	}

	t.Logf("Total devices via MediaDevices: %d", len(devices))
	for _, d := range devices {
		t.Logf("  - [%s] %s (%s)", d.Kind, d.Label, d.DeviceID)
	}
}
