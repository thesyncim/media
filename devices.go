package media

import (
	"context"
	"fmt"
	"sync"
)

// DeviceKind represents the type of media device.
type DeviceKind int

const (
	DeviceKindVideoInput  DeviceKind = iota // Camera
	DeviceKindAudioInput                    // Microphone
	DeviceKindAudioOutput                   // Speaker/headphones
)

func (k DeviceKind) String() string {
	switch k {
	case DeviceKindVideoInput:
		return "videoinput"
	case DeviceKindAudioInput:
		return "audioinput"
	case DeviceKindAudioOutput:
		return "audiooutput"
	default:
		return "unknown"
	}
}

// DeviceInfo describes a media device (like browser's MediaDeviceInfo).
type DeviceInfo struct {
	DeviceID string     // Unique identifier for the device
	GroupID  string     // Group identifier (devices with same groupID belong together)
	Kind     DeviceKind // Device type
	Label    string     // Human-readable device name
}

// DisplayMediaOptions configures getDisplayMedia (screen capture).
type DisplayMediaOptions struct {
	Video DisplayVideoOptions
	Audio bool // Request audio from display
}

// DisplayVideoOptions configures display capture video.
type DisplayVideoOptions struct {
	DisplaySurface string // "monitor", "window", "browser"
	Cursor         string // "always", "motion", "never"
	Width          int    // Requested width
	Height         int    // Requested height
	FrameRate      int    // Requested framerate
}

// UserMediaOptions configures getUserMedia.
type UserMediaOptions struct {
	Video *VideoConstraints // nil = no video
	Audio *AudioConstraints // nil = no audio
}

// VideoConstraints for getUserMedia video.
type VideoConstraints struct {
	DeviceID   string // Specific device ID
	Width      int    // Requested width
	Height     int    // Requested height
	FrameRate  int    // Requested framerate
	FacingMode string // "user" or "environment"
}

// AudioConstraints for getUserMedia audio.
type AudioConstraints struct {
	DeviceID         string // Specific device ID
	SampleRate       int    // Requested sample rate
	ChannelCount     int    // Requested channels
	EchoCancellation bool   // Enable echo cancellation
	NoiseSuppression bool   // Enable noise suppression
	AutoGainControl  bool   // Enable automatic gain control
}

// MediaDevices provides access to media input devices (like navigator.mediaDevices).
// Use GetMediaDevices() to get the singleton instance.
type MediaDevices interface {
	// EnumerateDevices returns a list of available media devices.
	EnumerateDevices(ctx context.Context) ([]DeviceInfo, error)

	// GetUserMedia prompts for permission and returns a MediaStream with
	// requested audio and/or video tracks (camera/microphone).
	GetUserMedia(ctx context.Context, options UserMediaOptions) (MediaStream, error)

	// GetDisplayMedia prompts for permission and returns a MediaStream with
	// screen/window capture.
	GetDisplayMedia(ctx context.Context, options DisplayMediaOptions) (MediaStream, error)

	// OnDeviceChange sets a callback for device connection/disconnection events.
	OnDeviceChange(callback func())
}

// DeviceProvider is implemented by platform-specific device implementations.
type DeviceProvider interface {
	// ListVideoDevices returns available video input devices.
	ListVideoDevices(ctx context.Context) ([]DeviceInfo, error)

	// ListAudioInputDevices returns available audio input devices.
	ListAudioInputDevices(ctx context.Context) ([]DeviceInfo, error)

	// ListAudioOutputDevices returns available audio output devices.
	ListAudioOutputDevices(ctx context.Context) ([]DeviceInfo, error)

	// OpenVideoDevice opens a video input device.
	OpenVideoDevice(ctx context.Context, deviceID string, constraints *VideoConstraints) (VideoTrack, error)

	// OpenAudioDevice opens an audio input device.
	OpenAudioDevice(ctx context.Context, deviceID string, constraints *AudioConstraints) (AudioTrack, error)

	// CaptureDisplay captures the screen/window.
	CaptureDisplay(ctx context.Context, options DisplayVideoOptions) (VideoTrack, error)

	// CaptureDisplayAudio captures display audio (if available).
	CaptureDisplayAudio(ctx context.Context) (AudioTrack, error)
}

// deviceRegistry holds registered device providers.
type deviceRegistry struct {
	provider DeviceProvider
	mu       sync.RWMutex
}

var globalDeviceRegistry = &deviceRegistry{}

// RegisterDeviceProvider registers a platform-specific device provider.
func RegisterDeviceProvider(provider DeviceProvider) {
	globalDeviceRegistry.mu.Lock()
	defer globalDeviceRegistry.mu.Unlock()
	globalDeviceRegistry.provider = provider
}

// GetDeviceProvider returns the registered device provider.
func GetDeviceProvider() DeviceProvider {
	globalDeviceRegistry.mu.RLock()
	defer globalDeviceRegistry.mu.RUnlock()
	return globalDeviceRegistry.provider
}

// DefaultMediaDevices is the default MediaDevices implementation.
type DefaultMediaDevices struct {
	deviceChangeCb func()
	mu             sync.RWMutex
}

var globalMediaDevices = &DefaultMediaDevices{}

// GetMediaDevices returns the MediaDevices singleton (like navigator.mediaDevices).
func GetMediaDevices() MediaDevices {
	return globalMediaDevices
}

// EnumerateDevices implements MediaDevices.
func (d *DefaultMediaDevices) EnumerateDevices(ctx context.Context) ([]DeviceInfo, error) {
	provider := GetDeviceProvider()
	if provider == nil {
		return nil, fmt.Errorf("no device provider registered")
	}

	var devices []DeviceInfo

	// Get video input devices
	videoDevices, err := provider.ListVideoDevices(ctx)
	if err == nil {
		devices = append(devices, videoDevices...)
	}

	// Get audio input devices
	audioInputDevices, err := provider.ListAudioInputDevices(ctx)
	if err == nil {
		devices = append(devices, audioInputDevices...)
	}

	// Get audio output devices
	audioOutputDevices, err := provider.ListAudioOutputDevices(ctx)
	if err == nil {
		devices = append(devices, audioOutputDevices...)
	}

	return devices, nil
}

// GetUserMedia implements MediaDevices.
func (d *DefaultMediaDevices) GetUserMedia(ctx context.Context, options UserMediaOptions) (MediaStream, error) {
	provider := GetDeviceProvider()
	if provider == nil {
		return nil, fmt.Errorf("no device provider registered")
	}

	stream := NewMediaStream(generateStreamID())

	// Open video device if requested
	if options.Video != nil {
		deviceID := options.Video.DeviceID
		if deviceID == "" {
			// Use default device
			devices, err := provider.ListVideoDevices(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to list video devices: %w", err)
			}
			if len(devices) == 0 {
				return nil, fmt.Errorf("no video devices available")
			}
			deviceID = devices[0].DeviceID
		}

		videoTrack, err := provider.OpenVideoDevice(ctx, deviceID, options.Video)
		if err != nil {
			return nil, fmt.Errorf("failed to open video device: %w", err)
		}
		stream.AddTrack(videoTrack)
	}

	// Open audio device if requested
	if options.Audio != nil {
		deviceID := options.Audio.DeviceID
		if deviceID == "" {
			// Use default device
			devices, err := provider.ListAudioInputDevices(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to list audio devices: %w", err)
			}
			if len(devices) == 0 {
				return nil, fmt.Errorf("no audio devices available")
			}
			deviceID = devices[0].DeviceID
		}

		audioTrack, err := provider.OpenAudioDevice(ctx, deviceID, options.Audio)
		if err != nil {
			// Close video track if we already opened it
			stream.Close()
			return nil, fmt.Errorf("failed to open audio device: %w", err)
		}
		stream.AddTrack(audioTrack)
	}

	return stream, nil
}

// GetDisplayMedia implements MediaDevices.
func (d *DefaultMediaDevices) GetDisplayMedia(ctx context.Context, options DisplayMediaOptions) (MediaStream, error) {
	provider := GetDeviceProvider()
	if provider == nil {
		return nil, fmt.Errorf("no device provider registered")
	}

	stream := NewMediaStream(generateStreamID())

	// Capture display video
	videoTrack, err := provider.CaptureDisplay(ctx, options.Video)
	if err != nil {
		return nil, fmt.Errorf("failed to capture display: %w", err)
	}
	stream.AddTrack(videoTrack)

	// Capture display audio if requested
	if options.Audio {
		audioTrack, err := provider.CaptureDisplayAudio(ctx)
		if err != nil {
			// Audio might not be available, log but don't fail
			// (similar to browser behavior)
		} else {
			stream.AddTrack(audioTrack)
		}
	}

	return stream, nil
}

// OnDeviceChange implements MediaDevices.
func (d *DefaultMediaDevices) OnDeviceChange(callback func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deviceChangeCb = callback
}

// NotifyDeviceChange should be called by DeviceProvider when devices change.
func (d *DefaultMediaDevices) NotifyDeviceChange() {
	d.mu.RLock()
	cb := d.deviceChangeCb
	d.mu.RUnlock()

	if cb != nil {
		go cb()
	}
}

var streamCounter uint64
var streamCounterMu sync.Mutex

func generateStreamID() string {
	streamCounterMu.Lock()
	streamCounter++
	id := streamCounter
	streamCounterMu.Unlock()
	return fmt.Sprintf("stream-%d", id)
}
