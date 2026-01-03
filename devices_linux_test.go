//go:build linux && !nodevices

package media

import (
	"context"
	"testing"
)

func TestLinuxDeviceProviderAvailability(t *testing.T) {
	// Check if V4L2 is available
	v4l2Available := IsV4L2Available()
	t.Logf("V4L2 available: %v", v4l2Available)

	// Check if ALSA is available
	alsaAvailable := IsALSAAvailable()
	t.Logf("ALSA available: %v", alsaAvailable)
}

func TestLinuxVideoDeviceEnumeration(t *testing.T) {
	if !IsV4L2Available() {
		t.Skip("V4L2 library not available")
	}

	provider := NewLinuxDeviceProvider()
	ctx := context.Background()

	devices, err := provider.ListVideoDevices(ctx)
	if err != nil {
		t.Fatalf("ListVideoDevices failed: %v", err)
	}

	t.Logf("Found %d video devices", len(devices))
	for i, device := range devices {
		t.Logf("  Device %d: ID=%s, Label=%s, Kind=%s",
			i, device.DeviceID, device.Label, device.Kind)
	}
}

func TestLinuxAudioInputDeviceEnumeration(t *testing.T) {
	if !IsALSAAvailable() {
		t.Skip("ALSA library not available")
	}

	provider := NewLinuxDeviceProvider()
	ctx := context.Background()

	devices, err := provider.ListAudioInputDevices(ctx)
	if err != nil {
		t.Fatalf("ListAudioInputDevices failed: %v", err)
	}

	t.Logf("Found %d audio input devices", len(devices))
	for i, device := range devices {
		t.Logf("  Device %d: ID=%s, Label=%s, Kind=%s",
			i, device.DeviceID, device.Label, device.Kind)
	}
}

func TestLinuxDeviceProviderRegistration(t *testing.T) {
	provider := GetDeviceProvider()
	if provider == nil {
		t.Log("No device provider registered (V4L2/ALSA libraries may not be available)")
		return
	}

	_, ok := provider.(*LinuxDeviceProvider)
	if !ok {
		t.Logf("Device provider is not LinuxDeviceProvider, got %T", provider)
	}

	// Test MediaDevices interface
	mediaDevices := GetMediaDevices()
	if mediaDevices == nil {
		t.Fatal("GetMediaDevices returned nil")
	}

	ctx := context.Background()
	devices, err := mediaDevices.EnumerateDevices(ctx)
	if err != nil {
		t.Logf("EnumerateDevices error (expected if no libs available): %v", err)
		return
	}

	t.Logf("MediaDevices.EnumerateDevices found %d devices", len(devices))
	for _, d := range devices {
		t.Logf("  %s: %s (%s)", d.Kind, d.Label, d.DeviceID)
	}
}
