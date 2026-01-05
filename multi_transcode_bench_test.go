package media

import (
	"testing"
	"time"
)

// BenchmarkMultiTranscode4Codecs720p benchmarks transcoding to 4 different codecs at 720p
func BenchmarkMultiTranscode4Codecs720p(b *testing.B) {
	// Check codec availability
	codecs := []struct {
		codec     VideoCodec
		available func() bool
		name      string
	}{
		{VideoCodecVP8, IsVP8Available, "VP8"},
		{VideoCodecVP9, IsVP9Available, "VP9"},
		{VideoCodecH264, IsH264EncoderAvailable, "H264"},
		{VideoCodecAV1, IsAV1Available, "AV1"},
	}

	var outputs []OutputConfig
	for _, c := range codecs {
		if c.available() {
			outputs = append(outputs, OutputConfig{
				ID:         c.name + "-720p",
				Codec:      c.codec,
				Width:      1280,
				Height:     720,
				BitrateBps: 1_500_000,
				FPS:        30,
			})
		}
	}

	if len(outputs) == 0 {
		b.Skip("No codecs available")
	}

	b.Logf("Testing with %d codecs: ", len(outputs))
	for _, o := range outputs {
		b.Logf("  - %s", o.ID)
	}

	// Create source encoder (VP8 at 720p to simulate WebRTC input)
	srcEncoder, err := NewVP8Encoder(VideoEncoderConfig{
		Width:      1280,
		Height:     720,
		BitrateBps: 2_000_000,
		FPS:        30,
	})
	if err != nil {
		b.Fatalf("Failed to create source encoder: %v", err)
	}
	defer srcEncoder.Close()
	srcEncodeBuf := make([]byte, srcEncoder.MaxEncodedSize())

	// Create multi-transcoder
	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec: VideoCodecVP8,
		Outputs:    outputs,
	})
	if err != nil {
		b.Fatalf("Failed to create multi-transcoder: %v", err)
	}
	defer mt.Close()

	// Generate test frames
	rawFrame := generateTestFrame(1280, 720)

	// Helper to encode a frame
	encodeFrame := func() *EncodedFrame {
		result, err := srcEncoder.Encode(rawFrame, srcEncodeBuf)
		if err != nil || result.N == 0 {
			return nil
		}
		return &EncodedFrame{
			Data:      srcEncodeBuf[:result.N],
			FrameType: result.FrameType,
		}
	}

	// Encode first frame as keyframe
	srcEncoder.RequestKeyframe()
	srcEncoded := encodeFrame()
	if srcEncoded == nil {
		b.Fatalf("Failed to encode source")
	}

	// Warm up transcoder
	for i := 0; i < 10; i++ {
		mt.Transcode(srcEncoded)
		srcEncoded = encodeFrame()
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := mt.Transcode(srcEncoded)
		if err != nil {
			b.Fatalf("Transcode failed: %v", err)
		}
		// Re-encode for next iteration
		srcEncoded = encodeFrame()
	}
}

// TestMultiTranscode4Codecs720pTiming measures detailed timing for 4 codecs at 720p
func TestMultiTranscode4Codecs720pTiming(t *testing.T) {
	// Check codec availability
	codecs := []struct {
		codec     VideoCodec
		available func() bool
		name      string
	}{
		{VideoCodecVP8, IsVP8Available, "VP8"},
		{VideoCodecVP9, IsVP9Available, "VP9"},
		{VideoCodecH264, IsH264EncoderAvailable, "H264"},
		{VideoCodecAV1, IsAV1Available, "AV1"},
	}

	var outputs []OutputConfig
	var codecNames []string
	for _, c := range codecs {
		if c.available() {
			outputs = append(outputs, OutputConfig{
				ID:         c.name + "-720p",
				Codec:      c.codec,
				Width:      1280,
				Height:     720,
				BitrateBps: 1_500_000,
				FPS:        30,
			})
			codecNames = append(codecNames, c.name)
		}
	}

	t.Logf("Testing with %d codecs: %v", len(outputs), codecNames)

	if len(outputs) == 0 {
		t.Skip("No codecs available")
	}

	// Create source encoder
	srcEncoder, err := NewVP8Encoder(VideoEncoderConfig{
		Width:      1280,
		Height:     720,
		BitrateBps: 2_000_000,
		FPS:        30,
	})
	if err != nil {
		t.Fatalf("Failed to create source encoder: %v", err)
	}
	defer srcEncoder.Close()
	srcEncodeBuf := make([]byte, srcEncoder.MaxEncodedSize())

	// Create multi-transcoder
	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec: VideoCodecVP8,
		Outputs:    outputs,
	})
	if err != nil {
		t.Fatalf("Failed to create multi-transcoder: %v", err)
	}
	defer mt.Close()

	// Generate test frame
	rawFrame := generateTestFrame(1280, 720)

	// Helper to encode a frame
	encodeFrame := func() *EncodedFrame {
		result, err := srcEncoder.Encode(rawFrame, srcEncodeBuf)
		if err != nil || result.N == 0 {
			return nil
		}
		return &EncodedFrame{
			Data:      srcEncodeBuf[:result.N],
			FrameType: result.FrameType,
		}
	}

	// Encode first frame as keyframe
	srcEncoder.RequestKeyframe()
	srcEncoded := encodeFrame()
	if srcEncoded == nil {
		t.Fatalf("Failed to encode source")
	}

	// Warm up
	for i := 0; i < 5; i++ {
		mt.Transcode(srcEncoded)
		srcEncoded = encodeFrame()
	}

	// Measure 100 frames
	const numFrames = 100
	times := make([]time.Duration, numFrames)
	var totalVariants int

	for i := 0; i < numFrames; i++ {
		start := time.Now()
		result, err := mt.Transcode(srcEncoded)
		times[i] = time.Since(start)

		if err != nil {
			t.Fatalf("Frame %d failed: %v", i, err)
		}
		if result != nil {
			totalVariants += len(result.Variants)
		}

		// Re-encode for next frame
		srcEncoded = encodeFrame()
	}

	// Calculate statistics
	var total time.Duration
	var min, max time.Duration = times[0], times[0]
	for _, d := range times {
		total += d
		if d < min {
			min = d
		}
		if d > max {
			max = d
		}
	}
	avg := total / numFrames

	// Count frames that would miss 30fps deadline (33.3ms)
	deadline := 33 * time.Millisecond
	var missedFrames int
	for _, d := range times {
		if d > deadline {
			missedFrames++
		}
	}

	t.Logf("\n=== Multi-Transcode Benchmark: %d codecs @ 720p ===", len(outputs))
	t.Logf("Codecs: %v", codecNames)
	t.Logf("Frames: %d", numFrames)
	t.Logf("Variants produced: %d (%.1f per frame)", totalVariants, float64(totalVariants)/float64(numFrames))
	t.Logf("")
	t.Logf("Timing:")
	t.Logf("  Min:     %v", min)
	t.Logf("  Max:     %v", max)
	t.Logf("  Avg:     %v", avg)
	t.Logf("  Total:   %v", total)
	t.Logf("")
	t.Logf("30 FPS Analysis (deadline: 33ms):")
	t.Logf("  Missed frames: %d/%d (%.1f%%)", missedFrames, numFrames, float64(missedFrames)*100/float64(numFrames))
	t.Logf("  Max sustainable FPS: %.1f", 1000/float64(avg.Milliseconds()))
	t.Logf("")

	if avg > deadline {
		t.Logf("WARNING: Average time (%.1fms) exceeds 30fps deadline (33ms)", float64(avg.Microseconds())/1000)
	} else {
		t.Logf("OK: Average time (%.1fms) is within 30fps deadline (33ms)", float64(avg.Microseconds())/1000)
	}
}

func generateTestFrame(width, height int) *VideoFrame {
	ySize := width * height
	uvSize := (width / 2) * (height / 2)

	y := make([]byte, ySize)
	u := make([]byte, uvSize)
	v := make([]byte, uvSize)

	// Generate gradient pattern
	for i := 0; i < ySize; i++ {
		y[i] = byte((i * 255) / ySize)
	}
	for i := 0; i < uvSize; i++ {
		u[i] = 128
		v[i] = 128
	}

	return &VideoFrame{
		Data:   [][]byte{y, u, v},
		Stride: []int{width, width / 2, width / 2},
		Width:  width,
		Height: height,
		Format: PixelFormatI420,
	}
}
