package media

import (
	"fmt"
	"io"
	"sync"
)

// VideoEncoderConfig configures a video encoder.
type VideoEncoderConfig struct {
	Codec            VideoCodec      // Codec type (VP8, VP9, H264, AV1)
	Width            int             // Frame width
	Height           int             // Frame height
	FPS              int             // Target framerate
	BitrateBps       int             // Target bitrate in bits per second
	MaxBitrateBps    int             // Maximum bitrate (0 = no limit)
	MinBitrateBps    int             // Minimum bitrate (0 = no limit)
	KeyframeInterval int             // Deprecated: automatic keyframes are disabled; use RequestKeyframe() for PLI
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
		Width:            width,
		Height:           height,
		FPS:              30,
		BitrateBps:       1500000, // 1.5 Mbps
		KeyframeInterval: 0,       // Automatic keyframes disabled; use RequestKeyframe() for PLI
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

// VideoEncoder encodes raw video frames to compressed bitstream.
type VideoEncoder interface {
	io.Closer

	// Encode encodes a video frame.
	// Returns nil if the encoder is buffering (e.g., B-frames) and no output is ready.
	// The returned EncodedFrame data is valid until the next Encode() call.
	Encode(frame *VideoFrame) (*EncodedFrame, error)

	// RequestKeyframe forces the next frame to be a keyframe.
	RequestKeyframe()

	// SetBitrate updates the target bitrate dynamically.
	SetBitrate(bitrateBps int) error

	// Config returns the encoder configuration.
	Config() VideoEncoderConfig

	// Codec returns the codec type.
	Codec() VideoCodec

	// Stats returns encoding statistics.
	Stats() EncoderStats

	// Flush flushes any buffered frames.
	// Call this when no more input frames will be provided.
	Flush() ([]*EncodedFrame, error)
}

// AudioEncoderConfig configures an audio encoder.
type AudioEncoderConfig struct {
	Codec       AudioCodec // Codec type (Opus, etc.)
	SampleRate  int        // Sample rate (e.g., 48000)
	Channels    int        // Number of channels (1 or 2)
	BitrateBps  int        // Target bitrate in bps (e.g., 64000 for 64kbps)
	FrameSizeMs int        // Frame size in milliseconds (Opus: 2.5, 5, 10, 20, 40, 60)
	PayloadType uint8      // RTP payload type

	// Opus-specific options
	DTX         bool // Enable discontinuous transmission
	FEC         bool // Enable forward error correction
	Application int  // Opus application (0=VOIP, 1=Audio, 2=LowDelay)
	Complexity  int  // Opus complexity (0-10, higher = better quality)
}

// DefaultAudioEncoderConfig returns a default audio encoder configuration.
func DefaultAudioEncoderConfig(codec AudioCodec) AudioEncoderConfig {
	return AudioEncoderConfig{
		Codec:       codec,
		SampleRate:  48000,
		Channels:    2,
		BitrateBps:  64000, // 64 kbps
		FrameSizeMs: 20,    // 20ms frames
		PayloadType: codec.DefaultPayloadType(),
		DTX:         true,
		FEC:         true,
		Application: 0, // VOIP
		Complexity:  10,
	}
}

// AudioEncoderStats provides audio encoding metrics.
type AudioEncoderStats struct {
	FramesEncoded  uint64 // Total frames encoded
	BytesEncoded   uint64 // Total bytes of encoded data
	SamplesEncoded uint64 // Total samples encoded
	EncodingTimeUs uint64 // Total encoding time in microseconds
	SilentFrames   uint64 // Frames encoded as silence (DTX)
}

// AudioEncoder encodes raw audio samples to compressed bitstream.
type AudioEncoder interface {
	io.Closer

	// Encode encodes audio samples.
	// The input samples should match the configured sample rate and channels.
	Encode(samples *AudioSamples) (*EncodedAudio, error)

	// Config returns the encoder configuration.
	Config() AudioEncoderConfig

	// Codec returns the codec type.
	Codec() AudioCodec

	// Stats returns encoding statistics.
	Stats() AudioEncoderStats
}

// VideoEncoderFactory creates a video encoder.
type VideoEncoderFactory func(config VideoEncoderConfig) (VideoEncoder, error)

// AudioEncoderFactory creates an audio encoder.
type AudioEncoderFactory func(config AudioEncoderConfig) (AudioEncoder, error)

// encoderRegistry holds registered encoder factories.
type encoderRegistry struct {
	videoFactories map[VideoCodec]VideoEncoderFactory
	audioFactories map[AudioCodec]AudioEncoderFactory
	mu             sync.RWMutex
}

var globalEncoderRegistry = &encoderRegistry{
	videoFactories: make(map[VideoCodec]VideoEncoderFactory),
	audioFactories: make(map[AudioCodec]AudioEncoderFactory),
}

// RegisterVideoEncoder registers a video encoder factory for a codec.
func RegisterVideoEncoder(codec VideoCodec, factory VideoEncoderFactory) {
	globalEncoderRegistry.mu.Lock()
	defer globalEncoderRegistry.mu.Unlock()
	globalEncoderRegistry.videoFactories[codec] = factory
}

// RegisterAudioEncoder registers an audio encoder factory for a codec.
func RegisterAudioEncoder(codec AudioCodec, factory AudioEncoderFactory) {
	globalEncoderRegistry.mu.Lock()
	defer globalEncoderRegistry.mu.Unlock()
	globalEncoderRegistry.audioFactories[codec] = factory
}

// CreateVideoEncoder creates a video encoder for the specified codec.
func CreateVideoEncoder(config VideoEncoderConfig) (VideoEncoder, error) {
	globalEncoderRegistry.mu.RLock()
	factory, ok := globalEncoderRegistry.videoFactories[config.Codec]
	globalEncoderRegistry.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("video encoder not available: %v", config.Codec)
	}

	return factory(config)
}

// CreateAudioEncoder creates an audio encoder for the specified codec.
func CreateAudioEncoder(config AudioEncoderConfig) (AudioEncoder, error) {
	globalEncoderRegistry.mu.RLock()
	factory, ok := globalEncoderRegistry.audioFactories[config.Codec]
	globalEncoderRegistry.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("audio encoder not available: %v", config.Codec)
	}

	return factory(config)
}

// IsVideoEncoderAvailable checks if a video encoder is available.
func IsVideoEncoderAvailable(codec VideoCodec) bool {
	globalEncoderRegistry.mu.RLock()
	defer globalEncoderRegistry.mu.RUnlock()
	_, ok := globalEncoderRegistry.videoFactories[codec]
	return ok
}

// IsAudioEncoderAvailable checks if an audio encoder is available.
func IsAudioEncoderAvailable(codec AudioCodec) bool {
	globalEncoderRegistry.mu.RLock()
	defer globalEncoderRegistry.mu.RUnlock()
	_, ok := globalEncoderRegistry.audioFactories[codec]
	return ok
}

// AvailableVideoEncoders returns a list of available video encoders.
func AvailableVideoEncoders() []VideoCodec {
	globalEncoderRegistry.mu.RLock()
	defer globalEncoderRegistry.mu.RUnlock()

	codecs := make([]VideoCodec, 0, len(globalEncoderRegistry.videoFactories))
	for c := range globalEncoderRegistry.videoFactories {
		codecs = append(codecs, c)
	}
	return codecs
}

// AvailableAudioEncoders returns a list of available audio encoders.
func AvailableAudioEncoders() []AudioCodec {
	globalEncoderRegistry.mu.RLock()
	defer globalEncoderRegistry.mu.RUnlock()

	codecs := make([]AudioCodec, 0, len(globalEncoderRegistry.audioFactories))
	for c := range globalEncoderRegistry.audioFactories {
		codecs = append(codecs, c)
	}
	return codecs
}
