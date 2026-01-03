package media

import (
	"testing"
)

func TestVideoScaler_NoScaling(t *testing.T) {
	frame := &VideoFrame{
		Data: [][]byte{
			make([]byte, 640*480),
			make([]byte, 320*240),
			make([]byte, 320*240),
		},
		Stride:    []int{640, 320, 320},
		Width:     640,
		Height:    480,
		Format:    PixelFormatI420,
		Timestamp: 12345,
	}

	scaler := NewVideoScaler(640, 480, 640, 480, ScaleModeStretch)
	out := scaler.Scale(frame)

	// Should return same frame when no scaling needed
	if out != frame {
		t.Error("Expected same frame when no scaling needed")
	}
}

func TestVideoScaler_Downscale(t *testing.T) {
	// Create test frame with gradient
	srcW, srcH := 1280, 720
	dstW, dstH := 640, 360

	frame := createGradientFrame(srcW, srcH)

	scaler := NewVideoScaler(srcW, srcH, dstW, dstH, ScaleModeStretch)
	out := scaler.Scale(frame)

	if out.Width != dstW || out.Height != dstH {
		t.Errorf("Expected %dx%d, got %dx%d", dstW, dstH, out.Width, out.Height)
	}

	if len(out.Data[0]) != dstW*dstH {
		t.Errorf("Y plane size mismatch: expected %d, got %d", dstW*dstH, len(out.Data[0]))
	}

	if len(out.Data[1]) != (dstW/2)*(dstH/2) {
		t.Errorf("U plane size mismatch")
	}
}

func TestVideoScaler_Upscale(t *testing.T) {
	srcW, srcH := 320, 240
	dstW, dstH := 640, 480

	frame := createGradientFrame(srcW, srcH)

	scaler := NewVideoScaler(srcW, srcH, dstW, dstH, ScaleModeStretch)
	out := scaler.Scale(frame)

	if out.Width != dstW || out.Height != dstH {
		t.Errorf("Expected %dx%d, got %dx%d", dstW, dstH, out.Width, out.Height)
	}
}

func TestVideoScaler_Fill(t *testing.T) {
	// 16:9 source to 4:3 destination (should crop sides)
	srcW, srcH := 1920, 1080
	dstW, dstH := 640, 480

	frame := createGradientFrame(srcW, srcH)

	scaler := NewVideoScaler(srcW, srcH, dstW, dstH, ScaleModeFill)
	out := scaler.Scale(frame)

	if out.Width != dstW || out.Height != dstH {
		t.Errorf("Expected %dx%d, got %dx%d", dstW, dstH, out.Width, out.Height)
	}
}

func TestCalculateScaledSize(t *testing.T) {
	tests := []struct {
		name             string
		srcW, srcH       int
		maxW, maxH       int
		mode             ScaleMode
		expectW, expectH int
	}{
		{"16:9 to 4:3 fit", 1920, 1080, 640, 480, ScaleModeFit, 640, 360},
		{"4:3 to 16:9 fit", 640, 480, 1280, 720, ScaleModeFit, 960, 720},
		{"same aspect", 1280, 720, 640, 360, ScaleModeFit, 640, 360},
		{"fill mode", 1920, 1080, 640, 480, ScaleModeFill, 640, 480},
		{"stretch mode", 1920, 1080, 640, 480, ScaleModeStretch, 640, 480},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, h := CalculateScaledSize(tt.srcW, tt.srcH, tt.maxW, tt.maxH, tt.mode)
			if w != tt.expectW || h != tt.expectH {
				t.Errorf("Expected %dx%d, got %dx%d", tt.expectW, tt.expectH, w, h)
			}
		})
	}
}

func createGradientFrame(width, height int) *VideoFrame {
	ySize := width * height
	uvSize := (width / 2) * (height / 2)

	yData := make([]byte, ySize)
	uData := make([]byte, uvSize)
	vData := make([]byte, uvSize)

	// Fill Y with horizontal gradient
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			yData[y*width+x] = byte(x * 255 / width)
		}
	}

	// Fill U/V with neutral values
	for i := range uData {
		uData[i] = 128
		vData[i] = 128
	}

	return &VideoFrame{
		Data:   [][]byte{yData, uData, vData},
		Stride: []int{width, width / 2, width / 2},
		Width:  width,
		Height: height,
		Format: PixelFormatI420,
	}
}

func BenchmarkVideoScaler_720pTo480p(b *testing.B) {
	frame := createGradientFrame(1280, 720)
	scaler := NewVideoScaler(1280, 720, 640, 480, ScaleModeFill)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scaler.Scale(frame)
	}
}

func BenchmarkVideoScaler_1080pTo720p(b *testing.B) {
	frame := createGradientFrame(1920, 1080)
	scaler := NewVideoScaler(1920, 1080, 1280, 720, ScaleModeFill)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scaler.Scale(frame)
	}
}
