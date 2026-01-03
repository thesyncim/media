package media

import (
	"testing"
)

func TestVideoFrameBuffer_I420(t *testing.T) {
	buf := NewVideoFrameBuffer(1280, 720, PixelFormatI420)

	// Check allocations
	ySize := 1280 * 720
	uvSize := 640 * 360

	if len(buf.Y) != ySize {
		t.Errorf("Y plane size = %d, want %d", len(buf.Y), ySize)
	}
	if len(buf.U) != uvSize {
		t.Errorf("U plane size = %d, want %d", len(buf.U), uvSize)
	}
	if len(buf.V) != uvSize {
		t.Errorf("V plane size = %d, want %d", len(buf.V), uvSize)
	}

	if buf.StrideY != 1280 {
		t.Errorf("StrideY = %d, want 1280", buf.StrideY)
	}
	if buf.StrideU != 640 || buf.StrideV != 640 {
		t.Errorf("StrideU/V = %d/%d, want 640", buf.StrideU, buf.StrideV)
	}
}

func TestVideoFrameBuffer_NV12(t *testing.T) {
	buf := NewVideoFrameBuffer(1920, 1080, PixelFormatNV12)

	ySize := 1920 * 1080
	uvSize := (1920 / 2) * (1080 / 2) * 2 // Interleaved UV

	if len(buf.Y) != ySize {
		t.Errorf("Y plane size = %d, want %d", len(buf.Y), ySize)
	}
	if len(buf.U) != uvSize {
		t.Errorf("UV plane size = %d, want %d", len(buf.U), uvSize)
	}
}

func TestVideoFrameBuffer_RGB(t *testing.T) {
	tests := []struct {
		format PixelFormat
		bpp    int
	}{
		{PixelFormatRGB24, 3},
		{PixelFormatRGBA32, 4},
		{PixelFormatBGRA32, 4},
	}

	for _, tt := range tests {
		t.Run(tt.format.String(), func(t *testing.T) {
			buf := NewVideoFrameBuffer(640, 480, tt.format)

			expectedSize := 640 * 480 * tt.bpp
			if len(buf.Data) != expectedSize {
				t.Errorf("Data size = %d, want %d", len(buf.Data), expectedSize)
			}
		})
	}
}

func TestVideoFrameBuffer_Reset(t *testing.T) {
	buf := NewVideoFrameBuffer(320, 240, PixelFormatI420)
	buf.TimestampNs = 12345
	buf.DurationNs = 33333

	buf.Reset()

	if buf.TimestampNs != 0 || buf.DurationNs != 0 {
		t.Error("Reset should clear timestamp and duration")
	}

	// Buffer data should still be allocated
	if len(buf.Y) == 0 {
		t.Error("Reset should not deallocate buffers")
	}
}

func TestVideoFrameBuffer_ToVideoFrame(t *testing.T) {
	buf := NewVideoFrameBuffer(320, 240, PixelFormatI420)
	buf.TimestampNs = 12345
	buf.DurationNs = 33333

	// Fill with test data
	buf.Y[0] = 100
	buf.U[0] = 128
	buf.V[0] = 200

	frame := buf.ToVideoFrame()

	if frame.Width != 320 || frame.Height != 240 {
		t.Errorf("Frame dimensions = %dx%d, want 320x240", frame.Width, frame.Height)
	}
	if frame.Format != PixelFormatI420 {
		t.Errorf("Frame format = %v, want I420", frame.Format)
	}
	if len(frame.Data) != 3 {
		t.Errorf("Frame planes = %d, want 3", len(frame.Data))
	}

	// Verify data points to same memory (zero-copy)
	if &frame.Data[0][0] != &buf.Y[0] {
		t.Error("ToVideoFrame should return zero-copy view")
	}
	if frame.Data[0][0] != 100 {
		t.Error("Data not preserved")
	}
}

func TestAudioSampleBuffer(t *testing.T) {
	buf := NewAudioSampleBuffer(960, 2, AudioFormatS16)

	expectedSize := 960 * 2 * 2 // samples * channels * bytes per sample
	if len(buf.Data) != expectedSize {
		t.Errorf("Data size = %d, want %d", len(buf.Data), expectedSize)
	}
}

func TestAudioSampleBuffer_Reset(t *testing.T) {
	buf := NewAudioSampleBuffer(960, 2, AudioFormatS16)
	buf.SampleCount = 960
	buf.TimestampNs = 12345

	buf.Reset()

	if buf.SampleCount != 0 || buf.TimestampNs != 0 {
		t.Error("Reset should clear sample count and timestamp")
	}
}

func TestAudioSampleBuffer_ToAudioSamples(t *testing.T) {
	buf := NewAudioSampleBuffer(960, 2, AudioFormatS16)
	buf.SampleRate = 48000
	buf.SampleCount = 480
	buf.TimestampNs = 12345

	samples := buf.ToAudioSamples()

	if samples.SampleRate != 48000 {
		t.Errorf("SampleRate = %d, want 48000", samples.SampleRate)
	}
	if samples.SampleCount != 480 {
		t.Errorf("SampleCount = %d, want 480", samples.SampleCount)
	}

	// Data should be truncated to actual sample count
	expectedLen := 480 * 2 * 2
	if len(samples.Data) != expectedLen {
		t.Errorf("Data len = %d, want %d", len(samples.Data), expectedLen)
	}
}

func TestEncodedFrameBuffer(t *testing.T) {
	buf := NewEncodedFrameBuffer(1024 * 1024) // 1MB max

	if len(buf.Data) != 1024*1024 {
		t.Errorf("Buffer size = %d, want 1MB", len(buf.Data))
	}

	buf.Size = 1000
	buf.FrameType = FrameTypeKey
	buf.Timestamp = 90000

	buf.Reset()

	if buf.Size != 0 || buf.FrameType != FrameTypeUnknown || buf.Timestamp != 0 {
		t.Error("Reset should clear all metadata")
	}
}

func TestEncodedFrameBuffer_ToEncodedFrame(t *testing.T) {
	buf := NewEncodedFrameBuffer(1024)
	buf.Size = 100
	buf.FrameType = FrameTypeKey
	buf.Timestamp = 90000
	buf.Duration = 3000

	// Fill with test data
	for i := 0; i < 100; i++ {
		buf.Data[i] = byte(i)
	}

	frame := buf.ToEncodedFrame()

	if len(frame.Data) != 100 {
		t.Errorf("Frame data len = %d, want 100", len(frame.Data))
	}
	if frame.FrameType != FrameTypeKey {
		t.Errorf("FrameType = %v, want Key", frame.FrameType)
	}

	// Verify zero-copy
	if &frame.Data[0] != &buf.Data[0] {
		t.Error("ToEncodedFrame should return zero-copy view")
	}
}

func TestBufferPool_Video(t *testing.T) {
	pool := NewBufferPool(1280, 720, PixelFormatI420)

	// Get buffer
	buf1 := pool.GetVideoBuffer()
	if buf1 == nil {
		t.Fatal("GetVideoBuffer returned nil")
	}
	if buf1.Width != 1280 || buf1.Height != 720 {
		t.Errorf("Buffer dimensions = %dx%d, want 1280x720", buf1.Width, buf1.Height)
	}

	// Modify and return
	buf1.TimestampNs = 12345
	pool.PutVideoBuffer(buf1)

	// Get again - should get same buffer (or a clean one)
	buf2 := pool.GetVideoBuffer()
	if buf2.TimestampNs != 0 {
		t.Error("Pooled buffer should be reset")
	}

	pool.PutVideoBuffer(buf2)
}

func TestBufferPool_Audio(t *testing.T) {
	pool := NewAudioBufferPool(960, 2, AudioFormatS16)

	buf1 := pool.GetAudioBuffer()
	if buf1 == nil {
		t.Fatal("GetAudioBuffer returned nil")
	}

	buf1.SampleCount = 480
	pool.PutAudioBuffer(buf1)

	buf2 := pool.GetAudioBuffer()
	if buf2.SampleCount != 0 {
		t.Error("Pooled buffer should be reset")
	}

	pool.PutAudioBuffer(buf2)
}

func BenchmarkVideoFrameBuffer_Alloc(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		buf := NewVideoFrameBuffer(1280, 720, PixelFormatI420)
		_ = buf
	}
}

func BenchmarkVideoFrameBuffer_Pool(b *testing.B) {
	pool := NewBufferPool(1280, 720, PixelFormatI420)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		buf := pool.GetVideoBuffer()
		pool.PutVideoBuffer(buf)
	}
}

func BenchmarkVideoFrameBuffer_ToVideoFrame(b *testing.B) {
	buf := NewVideoFrameBuffer(1280, 720, PixelFormatI420)
	buf.TimestampNs = 12345

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		frame := buf.ToVideoFrame()
		_ = frame
	}
}

func BenchmarkEncodedFrameBuffer_Pool(b *testing.B) {
	// Custom pool for encoded frames
	pool := &encodedBufferPool{
		maxSize: 256 * 1024, // 256KB
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		buf := pool.Get()
		pool.Put(buf)
	}
}

// Helper pool for benchmarking
type encodedBufferPool struct {
	pool    []*EncodedFrameBuffer
	maxSize int
}

func (p *encodedBufferPool) Get() *EncodedFrameBuffer {
	if len(p.pool) > 0 {
		buf := p.pool[len(p.pool)-1]
		p.pool = p.pool[:len(p.pool)-1]
		buf.Reset()
		return buf
	}
	return NewEncodedFrameBuffer(p.maxSize)
}

func (p *encodedBufferPool) Put(buf *EncodedFrameBuffer) {
	p.pool = append(p.pool, buf)
}
