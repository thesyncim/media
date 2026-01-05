// Core frame and sample types used across the media package.
package media

// PixelFormat represents video pixel formats.
type PixelFormat int

const (
	PixelFormatI420   PixelFormat = iota // YUV 4:2:0 planar (Y + U + V)
	PixelFormatNV12                      // YUV 4:2:0 semi-planar (Y + interleaved UV)
	PixelFormatRGB24                     // Packed RGB, 3 bytes per pixel
	PixelFormatRGBA32                    // Packed RGBA, 4 bytes per pixel
	PixelFormatBGRA32                    // Packed BGRA, 4 bytes per pixel
)

func (p PixelFormat) String() string {
	switch p {
	case PixelFormatI420:
		return "I420"
	case PixelFormatNV12:
		return "NV12"
	case PixelFormatRGB24:
		return "RGB24"
	case PixelFormatRGBA32:
		return "RGBA32"
	case PixelFormatBGRA32:
		return "BGRA32"
	default:
		return "Unknown"
	}
}

// PlaneCount returns the number of planes for this pixel format.
func (p PixelFormat) PlaneCount() int {
	switch p {
	case PixelFormatI420:
		return 3 // Y, U, V
	case PixelFormatNV12:
		return 2 // Y, UV
	case PixelFormatRGB24, PixelFormatRGBA32, PixelFormatBGRA32:
		return 1 // Packed
	default:
		return 0
	}
}

// AudioFormat represents audio sample formats.
type AudioFormat int

const (
	AudioFormatS16 AudioFormat = iota // Signed 16-bit PCM
	AudioFormatF32                    // 32-bit float
)

func (a AudioFormat) String() string {
	switch a {
	case AudioFormatS16:
		return "S16"
	case AudioFormatF32:
		return "F32"
	default:
		return "Unknown"
	}
}

// BytesPerSample returns the number of bytes per sample for this format.
func (a AudioFormat) BytesPerSample() int {
	switch a {
	case AudioFormatS16:
		return 2
	case AudioFormatF32:
		return 4
	default:
		return 0
	}
}

// VideoFrame represents a raw video frame.
// The Data slices may point to external memory (e.g., C memory via FFI).
// Callers must ensure the data remains valid for the lifetime of the frame.
type VideoFrame struct {
	Data      [][]byte    // Plane data (1-4 planes depending on format)
	Stride    []int       // Stride for each plane in bytes
	Width     int         // Frame width in pixels
	Height    int         // Frame height in pixels
	Format    PixelFormat // Pixel format
	Timestamp int64       // Capture timestamp in nanoseconds
	Duration  int64       // Frame duration in nanoseconds (optional)
}

// Clone creates a deep copy of the video frame.
// Use this when you need to keep the frame data beyond its original lifetime.
func (f *VideoFrame) Clone() *VideoFrame {
	clone := &VideoFrame{
		Data:      make([][]byte, len(f.Data)),
		Stride:    make([]int, len(f.Stride)),
		Width:     f.Width,
		Height:    f.Height,
		Format:    f.Format,
		Timestamp: f.Timestamp,
		Duration:  f.Duration,
	}
	copy(clone.Stride, f.Stride)
	for i, plane := range f.Data {
		if plane != nil {
			clone.Data[i] = make([]byte, len(plane))
			copy(clone.Data[i], plane)
		}
	}
	return clone
}

// I420Size returns the total buffer size needed for an I420 frame.
func I420Size(width, height int) int {
	// Y plane: width * height
	// U plane: (width/2) * (height/2)
	// V plane: (width/2) * (height/2)
	ySize := width * height
	uvSize := (width / 2) * (height / 2)
	return ySize + uvSize*2
}

// AudioSamples represents raw audio samples.
type AudioSamples struct {
	Data        []byte      // Sample data
	SampleRate  int         // Sample rate (e.g., 48000)
	Channels    int         // Number of channels (1 = mono, 2 = stereo)
	SampleCount int         // Number of samples (per channel)
	Format      AudioFormat // Sample format
	Timestamp   int64       // Capture timestamp in nanoseconds
}

// Clone creates a deep copy of the audio samples.
func (s *AudioSamples) Clone() *AudioSamples {
	clone := &AudioSamples{
		SampleRate:  s.SampleRate,
		Channels:    s.Channels,
		SampleCount: s.SampleCount,
		Format:      s.Format,
		Timestamp:   s.Timestamp,
	}
	if s.Data != nil {
		clone.Data = make([]byte, len(s.Data))
		copy(clone.Data, s.Data)
	}
	return clone
}

// FrameType indicates whether a frame is a keyframe or delta frame.
type FrameType int

const (
	FrameTypeUnknown FrameType = iota
	FrameTypeKey               // I-frame, can be decoded independently
	FrameTypeDelta             // P/B-frame, requires previous frames
)

func (f FrameType) String() string {
	switch f {
	case FrameTypeKey:
		return "Key"
	case FrameTypeDelta:
		return "Delta"
	default:
		return "Unknown"
	}
}

// EncodedFrame holds encoded video data.
// The Data slice is owned by the encoder and valid until the next Encode() call.
type EncodedFrame struct {
	Data            []byte    // Encoded bitstream data
	FrameType       FrameType // Key or delta frame
	Timestamp       uint32    // RTP timestamp (90kHz clock for video)
	Duration        uint32    // Duration in RTP timestamp units
	TemporalLayerID uint8     // SVC temporal layer (0 = base)
	SpatialLayerID  uint8     // SVC spatial layer (0 = base)
}

// IsKeyframe returns true if this is a keyframe.
func (f *EncodedFrame) IsKeyframe() bool {
	return f.FrameType == FrameTypeKey
}

// Clone creates a deep copy of the encoded frame.
func (f *EncodedFrame) Clone() *EncodedFrame {
	clone := &EncodedFrame{
		FrameType:       f.FrameType,
		Timestamp:       f.Timestamp,
		Duration:        f.Duration,
		TemporalLayerID: f.TemporalLayerID,
		SpatialLayerID:  f.SpatialLayerID,
	}
	if f.Data != nil {
		clone.Data = make([]byte, len(f.Data))
		copy(clone.Data, f.Data)
	}
	return clone
}

// EncodedAudio holds encoded audio data.
type EncodedAudio struct {
	Data      []byte // Encoded data (e.g., Opus packets)
	Timestamp uint32 // RTP timestamp (48kHz clock for Opus)
	Duration  uint32 // Duration in samples
}

// Clone creates a deep copy of the encoded audio.
func (a *EncodedAudio) Clone() *EncodedAudio {
	clone := &EncodedAudio{
		Timestamp: a.Timestamp,
		Duration:  a.Duration,
	}
	if a.Data != nil {
		clone.Data = make([]byte, len(a.Data))
		copy(clone.Data, a.Data)
	}
	return clone
}
