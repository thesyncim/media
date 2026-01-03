package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ErrNotSupported is returned when an optional operation is not supported.
var ErrNotSupported = errors.New("operation not supported")

// SourceType identifies the type of media source.
type SourceType int

const (
	SourceTypeUnknown     SourceType = iota
	SourceTypeCamera                 // Camera capture (platform-specific)
	SourceTypeScreen                 // Screen capture (platform-specific)
	SourceTypeFile                   // File reader (via FFmpeg or native)
	SourceTypeTestPattern            // Synthetic test pattern generator
	SourceTypeCustom                 // User-provided callback source
)

func (s SourceType) String() string {
	switch s {
	case SourceTypeCamera:
		return "Camera"
	case SourceTypeScreen:
		return "Screen"
	case SourceTypeFile:
		return "File"
	case SourceTypeTestPattern:
		return "TestPattern"
	case SourceTypeCustom:
		return "Custom"
	default:
		return "Unknown"
	}
}

// SourceConfig describes a media source's capabilities and configuration.
type SourceConfig struct {
	Width      int         // Frame width in pixels
	Height     int         // Frame height in pixels
	FPS        int         // Frames per second
	Format     PixelFormat // Pixel format
	SourceType SourceType  // Type of source
}

// VideoFrameCallback is called when a frame is available (push mode).
type VideoFrameCallback func(frame *VideoFrame)

// VideoSource produces raw video frames.
type VideoSource interface {
	io.Closer

	// Start begins capture/generation.
	Start(ctx context.Context) error

	// Stop halts capture/generation.
	Stop() error

	// ReadFrame reads the next frame (blocking).
	// The returned frame is valid until the next ReadFrame call or Close.
	ReadFrame(ctx context.Context) (*VideoFrame, error)

	// ReadFrameInto reads the next frame into a pre-allocated buffer (zero-alloc path).
	// Optional: implementations may return ErrNotSupported.
	ReadFrameInto(ctx context.Context, buf *VideoFrameBuffer) error

	// SetCallback sets push-mode callback for frame delivery.
	// When set, frames are pushed to the callback instead of being buffered.
	SetCallback(cb VideoFrameCallback)

	// Config returns the source configuration.
	Config() SourceConfig
}

// AudioSamplesCallback is called when audio samples are available (push mode).
type AudioSamplesCallback func(samples *AudioSamples)

// AudioSource produces raw audio samples.
type AudioSource interface {
	io.Closer

	// Start begins capture/generation.
	Start(ctx context.Context) error

	// Stop halts capture/generation.
	Stop() error

	// ReadSamples reads the next audio samples (blocking).
	ReadSamples(ctx context.Context) (*AudioSamples, error)

	// ReadSamplesInto reads samples into a pre-allocated buffer (zero-alloc path).
	// Optional: implementations may return ErrNotSupported.
	ReadSamplesInto(ctx context.Context, buf *AudioSampleBuffer) error

	// SetCallback sets push-mode callback for sample delivery.
	SetCallback(cb AudioSamplesCallback)

	// SampleRate returns the audio sample rate.
	SampleRate() int

	// Channels returns the number of audio channels.
	Channels() int
}

// VideoSourceFactory creates a video source with the given configuration.
type VideoSourceFactory func(config interface{}) (VideoSource, error)

// AudioSourceFactory creates an audio source with the given configuration.
type AudioSourceFactory func(config interface{}) (AudioSource, error)

// sourceRegistry holds registered source factories.
type sourceRegistry struct {
	videoFactories map[SourceType]VideoSourceFactory
	audioFactories map[SourceType]AudioSourceFactory
	mu             sync.RWMutex
}

var globalSourceRegistry = &sourceRegistry{
	videoFactories: make(map[SourceType]VideoSourceFactory),
	audioFactories: make(map[SourceType]AudioSourceFactory),
}

// RegisterVideoSource registers a video source factory for a source type.
func RegisterVideoSource(stype SourceType, factory VideoSourceFactory) {
	globalSourceRegistry.mu.Lock()
	defer globalSourceRegistry.mu.Unlock()
	globalSourceRegistry.videoFactories[stype] = factory
}

// RegisterAudioSource registers an audio source factory for a source type.
func RegisterAudioSource(stype SourceType, factory AudioSourceFactory) {
	globalSourceRegistry.mu.Lock()
	defer globalSourceRegistry.mu.Unlock()
	globalSourceRegistry.audioFactories[stype] = factory
}

// CreateVideoSource creates a video source of the specified type.
func CreateVideoSource(stype SourceType, config interface{}) (VideoSource, error) {
	globalSourceRegistry.mu.RLock()
	factory, ok := globalSourceRegistry.videoFactories[stype]
	globalSourceRegistry.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("video source type not available: %v", stype)
	}

	return factory(config)
}

// CreateAudioSource creates an audio source of the specified type.
func CreateAudioSource(stype SourceType, config interface{}) (AudioSource, error) {
	globalSourceRegistry.mu.RLock()
	factory, ok := globalSourceRegistry.audioFactories[stype]
	globalSourceRegistry.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("audio source type not available: %v", stype)
	}

	return factory(config)
}

// IsVideoSourceAvailable checks if a video source type is available.
func IsVideoSourceAvailable(stype SourceType) bool {
	globalSourceRegistry.mu.RLock()
	defer globalSourceRegistry.mu.RUnlock()
	_, ok := globalSourceRegistry.videoFactories[stype]
	return ok
}

// IsAudioSourceAvailable checks if an audio source type is available.
func IsAudioSourceAvailable(stype SourceType) bool {
	globalSourceRegistry.mu.RLock()
	defer globalSourceRegistry.mu.RUnlock()
	_, ok := globalSourceRegistry.audioFactories[stype]
	return ok
}

// AvailableVideoSources returns a list of available video source types.
func AvailableVideoSources() []SourceType {
	globalSourceRegistry.mu.RLock()
	defer globalSourceRegistry.mu.RUnlock()

	types := make([]SourceType, 0, len(globalSourceRegistry.videoFactories))
	for t := range globalSourceRegistry.videoFactories {
		types = append(types, t)
	}
	return types
}

// AvailableAudioSources returns a list of available audio source types.
func AvailableAudioSources() []SourceType {
	globalSourceRegistry.mu.RLock()
	defer globalSourceRegistry.mu.RUnlock()

	types := make([]SourceType, 0, len(globalSourceRegistry.audioFactories))
	for t := range globalSourceRegistry.audioFactories {
		types = append(types, t)
	}
	return types
}
