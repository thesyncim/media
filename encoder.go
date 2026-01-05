package media

import (
	"errors"
	"fmt"
	"io"
	"sync"
)

// Common errors
var (
	ErrBufferTooSmall    = errors.New("buffer too small")
	ErrProviderNotFound  = errors.New("provider not available")
	ErrCodecNotSupported = errors.New("codec not supported by provider")
)

// VideoEncoderConfig configures a video encoder.
type VideoEncoderConfig struct {
	Codec    VideoCodec // Codec type (VP8, VP9, H264, AV1)
	Provider Provider   // Provider to use (ProviderAuto = library chooses)

	Width      int // Frame width
	Height     int // Frame height
	FPS        int // Target framerate
	BitrateBps int // Target bitrate in bits per second

	MaxBitrateBps    int             // Maximum bitrate (0 = no limit)
	MinBitrateBps    int             // Minimum bitrate (0 = no limit)
	KeyframeInterval int             // Deprecated: use RequestKeyframe() for PLI
	RateControlMode  RateControlMode // Rate control mode
	Threads          int             // Encoder threads (0 = auto)
	Quality          int             // Quality level (codec-specific, 0-63 for VP8/VP9)
	PayloadType      uint8           // RTP payload type

	// Codec-specific options
	H264Profile H264Profile // H.264 profile
	VP9Profile  VP9Profile  // VP9 profile
	AV1Profile  AV1Profile  // AV1 profile

	// SVC (Scalable Video Coding) settings
	TemporalLayers int // Number of temporal layers (1-4)
	SpatialLayers  int // Number of spatial layers (1-3)
}

// DefaultVideoEncoderConfig returns a default encoder configuration.
func DefaultVideoEncoderConfig(codec VideoCodec, width, height int) VideoEncoderConfig {
	return VideoEncoderConfig{
		Codec:            codec,
		Provider:         ProviderAuto,
		Width:            width,
		Height:           height,
		FPS:              30,
		BitrateBps:       1500000, // 1.5 Mbps
		KeyframeInterval: 0,       // Disabled; use RequestKeyframe()
		RateControlMode:  RateControlVBR,
		Threads:          0, // Auto
		Quality:          32,
		PayloadType:      codec.DefaultPayloadType(),
		TemporalLayers:   1,
		SpatialLayers:    1,
	}
}

// EncoderStats provides encoding metrics.
type EncoderStats struct {
	FramesEncoded     uint64  // Total frames encoded
	KeyframesEncoded  uint64  // Total keyframes encoded
	BytesEncoded      uint64  // Total bytes of encoded data
	AverageBitrateBps int     // Average bitrate in bps
	AverageQP         float64 // Average quantization parameter
	AverageFPS        float64 // Average frames per second
	EncodingTimeUs    uint64  // Total encoding time in microseconds
	DroppedFrames     uint64  // Frames dropped due to rate control
}

// EncodeResult contains the result of an encode operation.
type EncodeResult struct {
	N         int       // Bytes written
	FrameType FrameType // Key or Delta
	PTS       int64     // Presentation timestamp
	DTS       int64     // Decode timestamp
}

// VideoEncoder encodes raw video frames to compressed bitstream.
type VideoEncoder interface {
	io.Closer

	// Encode encodes a video frame.
	// Returns nil if the encoder is buffering and no output is ready.
	// The returned EncodedFrame data is valid until the next Encode() call.
	Encode(frame *VideoFrame) (*EncodedFrame, error)

	// EncodeInto encodes directly into the provided buffer.
	// Returns ErrBufferTooSmall if buf is insufficient (use MaxEncodedSize).
	// This is the zero-allocation path for performance-critical code.
	EncodeInto(frame *VideoFrame, buf []byte) (EncodeResult, error)

	// MaxEncodedSize returns the maximum possible encoded size.
	MaxEncodedSize() int

	// RequestKeyframe forces the next frame to be a keyframe.
	RequestKeyframe()

	// SetBitrate updates the target bitrate dynamically.
	SetBitrate(bitrateBps int) error

	// SetResolution updates the encoding resolution dynamically.
	// Returns ErrNotSupported if the provider doesn't support dynamic resolution.
	// Check Provider().Features().Has(FeatureDynamicResolution) before calling.
	SetResolution(width, height int) error

	// Provider returns which provider created this encoder.
	Provider() Provider

	// Config returns the encoder configuration.
	Config() VideoEncoderConfig

	// Codec returns the codec type.
	Codec() VideoCodec

	// Stats returns encoding statistics.
	Stats() EncoderStats

	// Flush flushes any buffered frames.
	Flush() ([]*EncodedFrame, error)
}

// AudioEncoderConfig configures an audio encoder.
type AudioEncoderConfig struct {
	Codec    AudioCodec // Codec type (Opus, etc.)
	Provider Provider   // Provider to use (ProviderAuto = library chooses)

	SampleRate  int   // Sample rate (e.g., 48000)
	Channels    int   // Number of channels (1 or 2)
	BitrateBps  int   // Target bitrate in bps
	FrameSizeMs int   // Frame size in milliseconds
	PayloadType uint8 // RTP payload type

	// Opus-specific options
	DTX         bool // Enable discontinuous transmission
	FEC         bool // Enable forward error correction
	Application int  // Opus application (0=VOIP, 1=Audio, 2=LowDelay)
	Complexity  int  // Opus complexity (0-10)
}

// DefaultAudioEncoderConfig returns a default audio encoder configuration.
func DefaultAudioEncoderConfig(codec AudioCodec) AudioEncoderConfig {
	return AudioEncoderConfig{
		Codec:       codec,
		Provider:    ProviderAuto,
		SampleRate:  48000,
		Channels:    2,
		BitrateBps:  64000,
		FrameSizeMs: 20,
		PayloadType: codec.DefaultPayloadType(),
		DTX:         true,
		FEC:         true,
		Application: 0,
		Complexity:  10,
	}
}

// AudioEncoderStats provides audio encoding metrics.
type AudioEncoderStats struct {
	FramesEncoded  uint64
	BytesEncoded   uint64
	SamplesEncoded uint64
	EncodingTimeUs uint64
	SilentFrames   uint64
}

// AudioEncoder encodes raw audio samples to compressed bitstream.
type AudioEncoder interface {
	io.Closer
	Encode(samples *AudioSamples) (*EncodedAudio, error)
	Provider() Provider
	Config() AudioEncoderConfig
	Codec() AudioCodec
	Stats() AudioEncoderStats
}

// --- Registry ---

type videoEncoderFactory func(VideoEncoderConfig) (VideoEncoder, error)
type audioEncoderFactory func(AudioEncoderConfig) (AudioEncoder, error)

type encoderRegistry struct {
	mu sync.RWMutex

	// Provider-aware registry: codec -> provider -> factory
	videoProviders map[VideoCodec]map[Provider]videoEncoderFactory
	audioProviders map[AudioCodec]map[Provider]audioEncoderFactory

	// Default provider per codec
	videoDefaults map[VideoCodec]Provider
	audioDefaults map[AudioCodec]Provider
}

var globalEncoderRegistry = &encoderRegistry{
	videoProviders: make(map[VideoCodec]map[Provider]videoEncoderFactory),
	audioProviders: make(map[AudioCodec]map[Provider]audioEncoderFactory),
	videoDefaults:  make(map[VideoCodec]Provider),
	audioDefaults:  make(map[AudioCodec]Provider),
}

// registerVideoEncoder registers a video encoder factory for a codec+provider.
func registerVideoEncoder(codec VideoCodec, provider Provider, factory videoEncoderFactory) {
	globalEncoderRegistry.mu.Lock()
	defer globalEncoderRegistry.mu.Unlock()

	if globalEncoderRegistry.videoProviders[codec] == nil {
		globalEncoderRegistry.videoProviders[codec] = make(map[Provider]videoEncoderFactory)
	}
	globalEncoderRegistry.videoProviders[codec][provider] = factory

	// Set default: prefer BSD (permissive) license providers
	current, exists := globalEncoderRegistry.videoDefaults[codec]
	if !exists || (provider.License().Permissive() && !current.License().Permissive()) {
		globalEncoderRegistry.videoDefaults[codec] = provider
	}
}

// registerAudioEncoder registers an audio encoder factory for a codec+provider.
func registerAudioEncoder(codec AudioCodec, provider Provider, factory audioEncoderFactory) {
	globalEncoderRegistry.mu.Lock()
	defer globalEncoderRegistry.mu.Unlock()

	if globalEncoderRegistry.audioProviders[codec] == nil {
		globalEncoderRegistry.audioProviders[codec] = make(map[Provider]audioEncoderFactory)
	}
	globalEncoderRegistry.audioProviders[codec][provider] = factory

	// Set default: prefer BSD license
	current, exists := globalEncoderRegistry.audioDefaults[codec]
	if !exists || (provider.License().Permissive() && !current.License().Permissive()) {
		globalEncoderRegistry.audioDefaults[codec] = provider
	}
}

// SetDefaultVideoEncoderProvider sets the default provider for a video codec.
func SetDefaultVideoEncoderProvider(codec VideoCodec, provider Provider) {
	globalEncoderRegistry.mu.Lock()
	defer globalEncoderRegistry.mu.Unlock()
	globalEncoderRegistry.videoDefaults[codec] = provider
}

// SetDefaultAudioEncoderProvider sets the default provider for an audio codec.
func SetDefaultAudioEncoderProvider(codec AudioCodec, provider Provider) {
	globalEncoderRegistry.mu.Lock()
	defer globalEncoderRegistry.mu.Unlock()
	globalEncoderRegistry.audioDefaults[codec] = provider
}

// NewVideoEncoder creates a video encoder.
func NewVideoEncoder(config VideoEncoderConfig) (VideoEncoder, error) {
	globalEncoderRegistry.mu.RLock()
	defer globalEncoderRegistry.mu.RUnlock()

	providers := globalEncoderRegistry.videoProviders[config.Codec]
	if providers == nil {
		return nil, fmt.Errorf("%w: no providers for %s", ErrCodecNotSupported, config.Codec)
	}

	// Resolve provider
	p := config.Provider
	if p == ProviderAuto {
		p = globalEncoderRegistry.videoDefaults[config.Codec]
	}

	factory, ok := providers[p]
	if !ok || !p.Available() {
		return nil, fmt.Errorf("%w: %s for %s", ErrProviderNotFound, p, config.Codec)
	}

	return factory(config)
}

// NewAudioEncoder creates an audio encoder.
func NewAudioEncoder(config AudioEncoderConfig) (AudioEncoder, error) {
	globalEncoderRegistry.mu.RLock()
	defer globalEncoderRegistry.mu.RUnlock()

	providers := globalEncoderRegistry.audioProviders[config.Codec]
	if providers == nil {
		return nil, fmt.Errorf("%w: no providers for %s", ErrCodecNotSupported, config.Codec)
	}

	p := config.Provider
	if p == ProviderAuto {
		p = globalEncoderRegistry.audioDefaults[config.Codec]
	}

	factory, ok := providers[p]
	if !ok || !p.Available() {
		return nil, fmt.Errorf("%w: %s for %s", ErrProviderNotFound, p, config.Codec)
	}

	return factory(config)
}

// VideoEncoderProviders returns available providers for a video codec.
func VideoEncoderProviders(codec VideoCodec) []Provider {
	globalEncoderRegistry.mu.RLock()
	defer globalEncoderRegistry.mu.RUnlock()

	providers := globalEncoderRegistry.videoProviders[codec]
	result := make([]Provider, 0, len(providers))
	for p := range providers {
		if p.Available() {
			result = append(result, p)
		}
	}
	return result
}

// AudioEncoderProviders returns available providers for an audio codec.
func AudioEncoderProviders(codec AudioCodec) []Provider {
	globalEncoderRegistry.mu.RLock()
	defer globalEncoderRegistry.mu.RUnlock()

	providers := globalEncoderRegistry.audioProviders[codec]
	result := make([]Provider, 0, len(providers))
	for p := range providers {
		if p.Available() {
			result = append(result, p)
		}
	}
	return result
}


