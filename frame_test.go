package media

import (
	"testing"
)

func TestPixelFormat_String(t *testing.T) {
	tests := []struct {
		format PixelFormat
		want   string
	}{
		{PixelFormatI420, "I420"},
		{PixelFormatNV12, "NV12"},
		{PixelFormatRGB24, "RGB24"},
		{PixelFormatRGBA32, "RGBA32"},
		{PixelFormatBGRA32, "BGRA32"},
		{PixelFormat(99), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.format.String(); got != tt.want {
				t.Errorf("PixelFormat.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPixelFormat_PlaneCount(t *testing.T) {
	tests := []struct {
		format PixelFormat
		want   int
	}{
		{PixelFormatI420, 3},
		{PixelFormatNV12, 2},
		{PixelFormatRGB24, 1},
		{PixelFormatRGBA32, 1},
		{PixelFormatBGRA32, 1},
		{PixelFormat(99), 0},
	}

	for _, tt := range tests {
		t.Run(tt.format.String(), func(t *testing.T) {
			if got := tt.format.PlaneCount(); got != tt.want {
				t.Errorf("PixelFormat.PlaneCount() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAudioFormat_BytesPerSample(t *testing.T) {
	tests := []struct {
		format AudioFormat
		want   int
	}{
		{AudioFormatS16, 2},
		{AudioFormatF32, 4},
		{AudioFormat(99), 0},
	}

	for _, tt := range tests {
		t.Run(tt.format.String(), func(t *testing.T) {
			if got := tt.format.BytesPerSample(); got != tt.want {
				t.Errorf("AudioFormat.BytesPerSample() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestI420Size(t *testing.T) {
	tests := []struct {
		width, height int
		want          int
	}{
		{1920, 1080, 1920*1080 + 2*(960*540)},
		{1280, 720, 1280*720 + 2*(640*360)},
		{640, 480, 640*480 + 2*(320*240)},
		{320, 240, 320*240 + 2*(160*120)},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			if got := I420Size(tt.width, tt.height); got != tt.want {
				t.Errorf("I420Size(%d, %d) = %v, want %v", tt.width, tt.height, got, tt.want)
			}
		})
	}
}

func TestVideoFrame_Clone(t *testing.T) {
	original := &VideoFrame{
		Data: [][]byte{
			{1, 2, 3, 4},
			{5, 6},
			{7, 8},
		},
		Stride:    []int{4, 2, 2},
		Width:     2,
		Height:    2,
		Format:    PixelFormatI420,
		Timestamp: 12345,
		Duration:  33333,
	}

	clone := original.Clone()

	// Verify values match
	if clone.Width != original.Width || clone.Height != original.Height {
		t.Error("Clone dimensions mismatch")
	}
	if clone.Format != original.Format {
		t.Error("Clone format mismatch")
	}
	if clone.Timestamp != original.Timestamp || clone.Duration != original.Duration {
		t.Error("Clone timing mismatch")
	}

	// Verify data is copied
	for i := range original.Data {
		for j := range original.Data[i] {
			if clone.Data[i][j] != original.Data[i][j] {
				t.Errorf("Clone data mismatch at plane %d, index %d", i, j)
			}
		}
	}

	// Verify independence (modify clone, original unchanged)
	clone.Data[0][0] = 99
	if original.Data[0][0] == 99 {
		t.Error("Clone is not independent from original")
	}
}

func TestEncodedFrame_Clone(t *testing.T) {
	original := &EncodedFrame{
		Data:            []byte{0x00, 0x01, 0x02, 0x03},
		FrameType:       FrameTypeKey,
		Timestamp:       90000,
		Duration:        3000,
		TemporalLayerID: 1,
		SpatialLayerID:  0,
	}

	clone := original.Clone()

	if clone.FrameType != original.FrameType {
		t.Error("Clone frame type mismatch")
	}
	if clone.Timestamp != original.Timestamp {
		t.Error("Clone timestamp mismatch")
	}
	if len(clone.Data) != len(original.Data) {
		t.Error("Clone data length mismatch")
	}

	// Verify independence
	clone.Data[0] = 0xFF
	if original.Data[0] == 0xFF {
		t.Error("Clone is not independent from original")
	}
}

func TestEncodedFrame_IsKeyframe(t *testing.T) {
	tests := []struct {
		frameType FrameType
		want      bool
	}{
		{FrameTypeKey, true},
		{FrameTypeDelta, false},
		{FrameTypeUnknown, false},
	}

	for _, tt := range tests {
		t.Run(tt.frameType.String(), func(t *testing.T) {
			f := &EncodedFrame{FrameType: tt.frameType}
			if got := f.IsKeyframe(); got != tt.want {
				t.Errorf("IsKeyframe() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAudioSamples_Clone(t *testing.T) {
	original := &AudioSamples{
		Data:        []byte{0x00, 0x01, 0x02, 0x03},
		SampleRate:  48000,
		Channels:    2,
		SampleCount: 960,
		Format:      AudioFormatS16,
		Timestamp:   12345,
	}

	clone := original.Clone()

	if clone.SampleRate != original.SampleRate {
		t.Error("Clone sample rate mismatch")
	}
	if clone.Channels != original.Channels {
		t.Error("Clone channels mismatch")
	}
	if len(clone.Data) != len(original.Data) {
		t.Error("Clone data length mismatch")
	}

	// Verify independence
	clone.Data[0] = 0xFF
	if original.Data[0] == 0xFF {
		t.Error("Clone is not independent from original")
	}
}

func BenchmarkVideoFrame_Clone(b *testing.B) {
	// Simulate a 720p I420 frame
	ySize := 1280 * 720
	uvSize := 640 * 360

	frame := &VideoFrame{
		Data: [][]byte{
			make([]byte, ySize),
			make([]byte, uvSize),
			make([]byte, uvSize),
		},
		Stride: []int{1280, 640, 640},
		Width:  1280,
		Height: 720,
		Format: PixelFormatI420,
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = frame.Clone()
	}
}
