package media

import "sync"

// VideoFrameBuffer is a pre-allocated buffer for zero-allocation frame delivery.
type VideoFrameBuffer struct {
	// Pre-allocated plane buffers
	Y []byte // Y plane buffer
	U []byte // U plane buffer
	V []byte // V plane buffer

	// Metadata (filled by source)
	Width       int
	Height      int
	StrideY     int
	StrideU     int
	StrideV     int
	Format      PixelFormat
	TimestampNs int64
	DurationNs  int64

	// For interleaved formats (RGB, etc.)
	Data []byte
}

// NewVideoFrameBuffer creates a new pre-allocated frame buffer.
func NewVideoFrameBuffer(width, height int, format PixelFormat) *VideoFrameBuffer {
	buf := &VideoFrameBuffer{
		Width:  width,
		Height: height,
		Format: format,
	}

	switch format {
	case PixelFormatI420:
		ySize := width * height
		uvSize := (width / 2) * (height / 2)
		buf.Y = make([]byte, ySize)
		buf.U = make([]byte, uvSize)
		buf.V = make([]byte, uvSize)
		buf.StrideY = width
		buf.StrideU = width / 2
		buf.StrideV = width / 2
	case PixelFormatNV12:
		ySize := width * height
		uvSize := (width / 2) * (height / 2) * 2 // Interleaved UV
		buf.Y = make([]byte, ySize)
		buf.U = make([]byte, uvSize) // UV interleaved in U buffer
		buf.StrideY = width
		buf.StrideU = width
	case PixelFormatRGB24:
		buf.Data = make([]byte, width*height*3)
		buf.StrideY = width * 3
	case PixelFormatRGBA32, PixelFormatBGRA32:
		buf.Data = make([]byte, width*height*4)
		buf.StrideY = width * 4
	}

	return buf
}

// Reset clears the buffer metadata for reuse.
func (b *VideoFrameBuffer) Reset() {
	b.TimestampNs = 0
	b.DurationNs = 0
}

// ToVideoFrame creates a VideoFrame pointing to this buffer's data.
// The returned frame is only valid while the buffer is not modified.
func (b *VideoFrameBuffer) ToVideoFrame() VideoFrame {
	frame := VideoFrame{
		Width:     b.Width,
		Height:    b.Height,
		Format:    b.Format,
		Timestamp: b.TimestampNs,
		Duration:  b.DurationNs,
	}

	switch b.Format {
	case PixelFormatI420:
		frame.Data = [][]byte{b.Y, b.U, b.V}
		frame.Stride = []int{b.StrideY, b.StrideU, b.StrideV}
	case PixelFormatNV12:
		frame.Data = [][]byte{b.Y, b.U}
		frame.Stride = []int{b.StrideY, b.StrideU}
	default:
		frame.Data = [][]byte{b.Data}
		frame.Stride = []int{b.StrideY}
	}

	return frame
}

// AudioSampleBuffer is a pre-allocated buffer for zero-allocation audio delivery.
type AudioSampleBuffer struct {
	// Pre-allocated sample buffer
	Data []byte

	// Metadata (filled by source)
	SampleRate  int
	Channels    int
	SampleCount int
	Format      AudioFormat
	TimestampNs int64
}

// NewAudioSampleBuffer creates a new pre-allocated audio buffer.
func NewAudioSampleBuffer(maxSamples, channels int, format AudioFormat) *AudioSampleBuffer {
	bytesPerSample := format.BytesPerSample()
	return &AudioSampleBuffer{
		Data:     make([]byte, maxSamples*channels*bytesPerSample),
		Channels: channels,
		Format:   format,
	}
}

// Reset clears the buffer metadata for reuse.
func (b *AudioSampleBuffer) Reset() {
	b.SampleCount = 0
	b.TimestampNs = 0
}

// ToAudioSamples creates an AudioSamples pointing to this buffer's data.
func (b *AudioSampleBuffer) ToAudioSamples() AudioSamples {
	return AudioSamples{
		Data:        b.Data[:b.SampleCount*b.Channels*b.Format.BytesPerSample()],
		SampleRate:  b.SampleRate,
		Channels:    b.Channels,
		SampleCount: b.SampleCount,
		Format:      b.Format,
		Timestamp:   b.TimestampNs,
	}
}

// EncodedFrameBuffer is a pre-allocated buffer for encoded frame data.
type EncodedFrameBuffer struct {
	Data            []byte
	Size            int // Actual data size
	FrameType       FrameType
	Timestamp       uint32
	Duration        uint32
	TemporalLayerID uint8
	SpatialLayerID  uint8
}

// NewEncodedFrameBuffer creates a pre-allocated encoded frame buffer.
func NewEncodedFrameBuffer(maxSize int) *EncodedFrameBuffer {
	return &EncodedFrameBuffer{
		Data: make([]byte, maxSize),
	}
}

// Reset clears the buffer for reuse.
func (b *EncodedFrameBuffer) Reset() {
	b.Size = 0
	b.FrameType = FrameTypeUnknown
	b.Timestamp = 0
	b.Duration = 0
	b.TemporalLayerID = 0
	b.SpatialLayerID = 0
}

// ToEncodedFrame creates an EncodedFrame pointing to this buffer's data.
func (b *EncodedFrameBuffer) ToEncodedFrame() EncodedFrame {
	return EncodedFrame{
		Data:            b.Data[:b.Size],
		FrameType:       b.FrameType,
		Timestamp:       b.Timestamp,
		Duration:        b.Duration,
		TemporalLayerID: b.TemporalLayerID,
		SpatialLayerID:  b.SpatialLayerID,
	}
}

// BufferPool provides pooled allocation for frame buffers.
type BufferPool struct {
	videoPool sync.Pool
	audioPool sync.Pool

	width, height int
	format        PixelFormat

	audioSamples  int
	audioChannels int
	audioFormat   AudioFormat
}

// NewBufferPool creates a new buffer pool for the given video configuration.
func NewBufferPool(width, height int, format PixelFormat) *BufferPool {
	return &BufferPool{
		width:  width,
		height: height,
		format: format,
		videoPool: sync.Pool{
			New: func() interface{} {
				return NewVideoFrameBuffer(width, height, format)
			},
		},
	}
}

// NewAudioBufferPool creates a new audio buffer pool.
func NewAudioBufferPool(maxSamples, channels int, format AudioFormat) *BufferPool {
	return &BufferPool{
		audioSamples:  maxSamples,
		audioChannels: channels,
		audioFormat:   format,
		audioPool: sync.Pool{
			New: func() interface{} {
				return NewAudioSampleBuffer(maxSamples, channels, format)
			},
		},
	}
}

// GetVideoBuffer gets a video buffer from the pool.
func (p *BufferPool) GetVideoBuffer() *VideoFrameBuffer {
	buf := p.videoPool.Get().(*VideoFrameBuffer)
	buf.Reset()
	return buf
}

// PutVideoBuffer returns a video buffer to the pool.
func (p *BufferPool) PutVideoBuffer(buf *VideoFrameBuffer) {
	p.videoPool.Put(buf)
}

// GetAudioBuffer gets an audio buffer from the pool.
func (p *BufferPool) GetAudioBuffer() *AudioSampleBuffer {
	buf := p.audioPool.Get().(*AudioSampleBuffer)
	buf.Reset()
	return buf
}

// PutAudioBuffer returns an audio buffer to the pool.
func (p *BufferPool) PutAudioBuffer(buf *AudioSampleBuffer) {
	p.audioPool.Put(buf)
}
