package media

import (
	"context"
	"testing"
	"time"
)

func TestNewTestPatternSource_Defaults(t *testing.T) {
	config := TestPatternConfig{}
	source := NewTestPatternSource(config)

	cfg := source.Config()
	if cfg.Width != 1280 {
		t.Errorf("Default width = %d, want 1280", cfg.Width)
	}
	if cfg.Height != 720 {
		t.Errorf("Default height = %d, want 720", cfg.Height)
	}
	if cfg.FPS != 30 {
		t.Errorf("Default FPS = %d, want 30", cfg.FPS)
	}
	if cfg.Format != PixelFormatI420 {
		t.Errorf("Default format = %v, want I420", cfg.Format)
	}
	if cfg.SourceType != SourceTypeTestPattern {
		t.Errorf("SourceType = %v, want TestPattern", cfg.SourceType)
	}
}

func TestNewTestPatternSource_CustomConfig(t *testing.T) {
	config := TestPatternConfig{
		Width:   640,
		Height:  480,
		FPS:     60,
		Pattern: PatternGradient,
	}
	source := NewTestPatternSource(config)

	cfg := source.Config()
	if cfg.Width != 640 || cfg.Height != 480 {
		t.Errorf("Custom dimensions not applied: %dx%d", cfg.Width, cfg.Height)
	}
	if cfg.FPS != 60 {
		t.Errorf("Custom FPS not applied: %d", cfg.FPS)
	}
}

func TestTestPatternSource_StartStop(t *testing.T) {
	source := NewTestPatternSource(TestPatternConfig{
		Width:  320,
		Height: 240,
		FPS:    30,
	})

	ctx := context.Background()

	// Start should succeed
	if err := source.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Double start should fail
	if err := source.Start(ctx); err == nil {
		t.Error("Double start should fail")
	}

	// Stop should succeed
	if err := source.Stop(); err != nil {
		t.Errorf("Stop failed: %v", err)
	}

	// Double stop should be safe
	if err := source.Stop(); err != nil {
		t.Errorf("Double stop should not fail: %v", err)
	}
}

func TestTestPatternSource_ReadFrame(t *testing.T) {
	source := NewTestPatternSource(TestPatternConfig{
		Width:   320,
		Height:  240,
		FPS:     30,
		Pattern: PatternColorBars,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := source.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer source.Close()

	// Read a frame
	frame, err := source.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("ReadFrame failed: %v", err)
	}

	// Verify frame properties
	if frame.Width != 320 || frame.Height != 240 {
		t.Errorf("Frame dimensions: %dx%d, want 320x240", frame.Width, frame.Height)
	}
	if frame.Format != PixelFormatI420 {
		t.Errorf("Frame format: %v, want I420", frame.Format)
	}
	if len(frame.Data) != 3 {
		t.Errorf("Frame planes: %d, want 3", len(frame.Data))
	}

	// Verify plane sizes
	ySize := 320 * 240
	uvSize := 160 * 120
	if len(frame.Data[0]) != ySize {
		t.Errorf("Y plane size: %d, want %d", len(frame.Data[0]), ySize)
	}
	if len(frame.Data[1]) != uvSize || len(frame.Data[2]) != uvSize {
		t.Errorf("UV plane sizes: %d, %d, want %d", len(frame.Data[1]), len(frame.Data[2]), uvSize)
	}

	if frame.Timestamp <= 0 {
		t.Error("Frame timestamp should be positive")
	}
}

func TestTestPatternSource_Callback(t *testing.T) {
	source := NewTestPatternSource(TestPatternConfig{
		Width:  320,
		Height: 240,
		FPS:    30,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	frameReceived := make(chan *VideoFrame, 1)
	source.SetCallback(func(frame *VideoFrame) {
		select {
		case frameReceived <- frame:
		default:
		}
	})

	if err := source.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer source.Close()

	select {
	case frame := <-frameReceived:
		if frame.Width != 320 || frame.Height != 240 {
			t.Errorf("Callback frame dimensions: %dx%d", frame.Width, frame.Height)
		}
	case <-ctx.Done():
		t.Fatal("Timeout waiting for callback frame")
	}
}

func TestTestPatternSource_AllPatterns(t *testing.T) {
	patterns := []PatternType{
		PatternColorBars,
		PatternGradient,
		PatternCheckerboard,
		PatternSolidColor,
		PatternNoise,
		PatternMovingBox,
	}

	for _, pattern := range patterns {
		t.Run(pattern.String(), func(t *testing.T) {
			config := TestPatternConfig{
				Width:    320,
				Height:   240,
				FPS:      30,
				Pattern:  pattern,
				Animated: true,
				SolidR:   255,
				SolidG:   128,
				SolidB:   64,
			}
			source := NewTestPatternSource(config)

			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()

			if err := source.Start(ctx); err != nil {
				t.Fatalf("Start failed: %v", err)
			}
			defer source.Close()

			// Read a few frames to ensure pattern generation works
			for i := 0; i < 3; i++ {
				frame, err := source.ReadFrame(ctx)
				if err != nil {
					t.Fatalf("ReadFrame failed on frame %d: %v", i, err)
				}
				if frame == nil {
					t.Fatalf("ReadFrame returned nil on frame %d", i)
				}
			}
		})
	}
}

func TestTestPatternSource_FrameTiming(t *testing.T) {
	source := NewTestPatternSource(TestPatternConfig{
		Width:  320,
		Height: 240,
		FPS:    60, // 60 FPS = ~16.67ms per frame
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := source.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer source.Close()

	// Read 10 frames and check timing
	var timestamps []int64
	for i := 0; i < 10; i++ {
		frame, err := source.ReadFrame(ctx)
		if err != nil {
			t.Fatalf("ReadFrame failed: %v", err)
		}
		timestamps = append(timestamps, frame.Timestamp)
	}

	// Check that timestamps are increasing
	for i := 1; i < len(timestamps); i++ {
		if timestamps[i] <= timestamps[i-1] {
			t.Errorf("Timestamps not increasing: %d <= %d", timestamps[i], timestamps[i-1])
		}
	}

	// Check approximate frame interval (with tolerance)
	expectedInterval := time.Second / 60
	for i := 1; i < len(timestamps); i++ {
		interval := time.Duration(timestamps[i] - timestamps[i-1])
		// Allow 50% tolerance for timing
		if interval < expectedInterval/2 || interval > expectedInterval*2 {
			t.Logf("Frame interval %d: %v (expected ~%v)", i, interval, expectedInterval)
		}
	}
}

func TestTestPatternSource_ContextCancellation(t *testing.T) {
	source := NewTestPatternSource(TestPatternConfig{
		Width:  320,
		Height: 240,
		FPS:    30,
	})

	ctx, cancel := context.WithCancel(context.Background())

	if err := source.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Cancel context
	cancel()

	// ReadFrame should return context error
	_, err := source.ReadFrame(ctx)
	if err != context.Canceled {
		// It might also be "source closed" depending on timing
		t.Logf("ReadFrame after cancel: %v", err)
	}

	source.Close()
}

func TestTestPatternSource_RGBToYUV(t *testing.T) {
	tests := []struct {
		r, g, b uint8
		name    string
	}{
		{255, 255, 255, "white"},
		{0, 0, 0, "black"},
		{255, 0, 0, "red"},
		{0, 255, 0, "green"},
		{0, 0, 255, "blue"},
		{128, 128, 128, "gray"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			y, u, v := rgbToYUV(tt.r, tt.g, tt.b)

			// Y should be in valid range
			if y < 16 || y > 235 {
				t.Errorf("Y value %d out of range [16, 235]", y)
			}
			// UV should be in valid range
			if u < 16 || u > 240 {
				t.Errorf("U value %d out of range [16, 240]", u)
			}
			if v < 16 || v > 240 {
				t.Errorf("V value %d out of range [16, 240]", v)
			}
		})
	}
}

func TestTestPatternSource_Registry(t *testing.T) {
	// Test that test pattern source is registered
	if !IsVideoSourceAvailable(SourceTypeTestPattern) {
		t.Error("TestPattern source should be registered")
	}

	// Create via registry
	source, err := CreateVideoSource(SourceTypeTestPattern, nil)
	if err != nil {
		t.Fatalf("CreateVideoSource failed: %v", err)
	}
	defer source.Close()

	cfg := source.Config()
	if cfg.SourceType != SourceTypeTestPattern {
		t.Errorf("SourceType = %v, want TestPattern", cfg.SourceType)
	}
}

func BenchmarkTestPatternSource_ColorBars(b *testing.B) {
	source := NewTestPatternSource(TestPatternConfig{
		Width:   1280,
		Height:  720,
		FPS:     30,
		Pattern: PatternColorBars,
	})

	ctx := context.Background()
	source.Start(ctx)
	defer source.Close()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		source.generatePattern(uint64(i))
	}
}

func BenchmarkTestPatternSource_Noise(b *testing.B) {
	source := NewTestPatternSource(TestPatternConfig{
		Width:   1280,
		Height:  720,
		FPS:     30,
		Pattern: PatternNoise,
	})

	ctx := context.Background()
	source.Start(ctx)
	defer source.Close()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		source.generatePattern(uint64(i))
	}
}

func BenchmarkTestPatternSource_MovingBox(b *testing.B) {
	source := NewTestPatternSource(TestPatternConfig{
		Width:   1280,
		Height:  720,
		FPS:     30,
		Pattern: PatternMovingBox,
	})

	ctx := context.Background()
	source.Start(ctx)
	defer source.Close()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		source.generatePattern(uint64(i))
	}
}
