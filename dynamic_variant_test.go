package media

import (
	"testing"
	"time"
)

// TestDynamicVariantAddition tests adding a variant while transcoding is in progress
func TestDynamicVariantAddition(t *testing.T) {
	// Create source encoder (VP8)
	srcEncoder, err := NewVP8Encoder(VideoEncoderConfig{
		Width:      640,
		Height:     480,
		BitrateBps: 1_000_000,
		FPS:        30,
	})
	if err != nil {
		t.Fatalf("Failed to create source encoder: %v", err)
	}
	defer srcEncoder.Close()

	// Create transcoder with 1 initial output (passthrough + VP8)
	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec: VideoCodecVP8,
		Outputs: []OutputConfig{
			{ID: "source", Codec: VideoCodecVP8, Passthrough: true},
			{ID: "vp8-720p", Codec: VideoCodecVP8, Width: 1280, Height: 720, BitrateBps: 1_500_000, FPS: 30},
		},
	})
	if err != nil {
		t.Fatalf("Failed to create transcoder: %v", err)
	}
	defer mt.Close()

	// Generate test frame
	rawFrame := &VideoFrame{
		Data:   [][]byte{make([]byte, 640*480), make([]byte, 320*240), make([]byte, 320*240)},
		Stride: []int{640, 320, 320},
		Width:  640,
		Height: 480,
		Format: PixelFormatI420,
	}

	// Encode source frame
	srcEncoder.RequestKeyframe()
	srcEncoded, err := srcEncoder.Encode(rawFrame)
	if err != nil {
		t.Fatalf("Failed to encode source: %v", err)
	}

	// Transcode 30 frames with initial 2 variants
	t.Log("Phase 1: Transcoding with 2 variants...")
	var phase1Variants int
	for i := 0; i < 30; i++ {
		result, err := mt.Transcode(srcEncoded)
		if err != nil {
			t.Fatalf("Transcode failed at frame %d: %v", i, err)
		}
		if result != nil {
			phase1Variants += len(result.Variants)
		}
		srcEncoded, _ = srcEncoder.Encode(rawFrame)
	}
	t.Logf("Phase 1: Got %d variant outputs (expected ~60)", phase1Variants)
	if phase1Variants < 50 {
		t.Errorf("Phase 1: Expected ~60 variants, got %d", phase1Variants)
	}

	// Dynamically add a new variant (VP9)
	t.Log("Adding dynamic VP9 variant...")
	err = mt.AddOutput(OutputConfig{
		ID:         "dyn-vp9",
		Codec:      VideoCodecVP9,
		Width:      640,
		Height:     480,
		BitrateBps: 1_000_000,
		FPS:        30,
	})
	if err != nil {
		t.Fatalf("AddOutput failed: %v", err)
	}

	// Transcode 30 more frames with 3 variants
	t.Log("Phase 2: Transcoding with 3 variants (including dynamic)...")
	var phase2Variants int
	var seenDynVP9 bool
	for i := 0; i < 30; i++ {
		result, err := mt.Transcode(srcEncoded)
		if err != nil {
			t.Fatalf("Transcode failed at frame %d: %v", i, err)
		}
		if result != nil {
			for _, v := range result.Variants {
				phase2Variants++
				if v.VariantID == "dyn-vp9" {
					seenDynVP9 = true
				}
			}
		}
		srcEncoded, _ = srcEncoder.Encode(rawFrame)
	}
	t.Logf("Phase 2: Got %d variant outputs (expected ~90)", phase2Variants)
	t.Logf("Phase 2: Saw dyn-vp9 output: %v", seenDynVP9)

	if !seenDynVP9 {
		t.Error("CRITICAL: Dynamic VP9 variant never produced output!")
	}
	if phase2Variants < 80 {
		t.Errorf("Phase 2: Expected ~90 variants, got %d", phase2Variants)
	}

	// Add another variant (H264) to test multiple dynamic additions
	if IsH264EncoderAvailable() {
		t.Log("Adding dynamic H264 variant...")
		err = mt.AddOutput(OutputConfig{
			ID:         "dyn-h264",
			Codec:      VideoCodecH264,
			Width:      640,
			Height:     480,
			BitrateBps: 1_000_000,
			FPS:        30,
		})
		if err != nil {
			t.Fatalf("AddOutput H264 failed: %v", err)
		}

		// Transcode 30 more frames with 4 variants
		t.Log("Phase 3: Transcoding with 4 variants...")
		var phase3Variants int
		var seenDynH264 bool
		for i := 0; i < 30; i++ {
			result, err := mt.Transcode(srcEncoded)
			if err != nil {
				t.Fatalf("Transcode failed at frame %d: %v", i, err)
			}
			if result != nil {
				for _, v := range result.Variants {
					phase3Variants++
					if v.VariantID == "dyn-h264" {
						seenDynH264 = true
					}
				}
			}
			srcEncoded, _ = srcEncoder.Encode(rawFrame)
		}
		t.Logf("Phase 3: Got %d variant outputs (expected ~120)", phase3Variants)
		t.Logf("Phase 3: Saw dyn-h264 output: %v", seenDynH264)

		if !seenDynH264 {
			t.Error("CRITICAL: Dynamic H264 variant never produced output!")
		}
	}
}

// TestDynamicVariantTiming tests timing of dynamic variant addition
func TestDynamicVariantTiming(t *testing.T) {
	srcEncoder, err := NewVP8Encoder(VideoEncoderConfig{
		Width:      640,
		Height:     480,
		BitrateBps: 1_000_000,
		FPS:        30,
	})
	if err != nil {
		t.Fatalf("Failed to create source encoder: %v", err)
	}
	defer srcEncoder.Close()

	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec: VideoCodecVP8,
		Outputs: []OutputConfig{
			{ID: "vp8", Codec: VideoCodecVP8, Width: 640, Height: 480, BitrateBps: 1_000_000, FPS: 30},
		},
	})
	if err != nil {
		t.Fatalf("Failed to create transcoder: %v", err)
	}
	defer mt.Close()

	rawFrame := &VideoFrame{
		Data:   [][]byte{make([]byte, 640*480), make([]byte, 320*240), make([]byte, 320*240)},
		Stride: []int{640, 320, 320},
		Width:  640,
		Height: 480,
		Format: PixelFormatI420,
	}

	srcEncoder.RequestKeyframe()
	srcEncoded, _ := srcEncoder.Encode(rawFrame)

	// Warm up
	for i := 0; i < 10; i++ {
		mt.Transcode(srcEncoded)
		srcEncoded, _ = srcEncoder.Encode(rawFrame)
	}

	// Measure time with 1 variant
	start := time.Now()
	for i := 0; i < 30; i++ {
		mt.Transcode(srcEncoded)
		srcEncoded, _ = srcEncoder.Encode(rawFrame)
	}
	time1 := time.Since(start)
	t.Logf("1 variant: %v (%.2fms/frame)", time1, float64(time1.Milliseconds())/30)

	// Add 2nd variant
	mt.AddOutput(OutputConfig{
		ID:         "vp9",
		Codec:      VideoCodecVP9,
		Width:      640,
		Height:     480,
		BitrateBps: 1_000_000,
		FPS:        30,
	})

	// Measure time with 2 variants
	start = time.Now()
	for i := 0; i < 30; i++ {
		mt.Transcode(srcEncoded)
		srcEncoded, _ = srcEncoder.Encode(rawFrame)
	}
	time2 := time.Since(start)
	t.Logf("2 variants: %v (%.2fms/frame)", time2, float64(time2.Milliseconds())/30)

	// Add 3rd variant
	if IsH264EncoderAvailable() {
		mt.AddOutput(OutputConfig{
			ID:         "h264",
			Codec:      VideoCodecH264,
			Width:      640,
			Height:     480,
			BitrateBps: 1_000_000,
			FPS:        30,
		})

		// Measure time with 3 variants
		start = time.Now()
		for i := 0; i < 30; i++ {
			mt.Transcode(srcEncoded)
			srcEncoded, _ = srcEncoder.Encode(rawFrame)
		}
		time3 := time.Since(start)
		t.Logf("3 variants: %v (%.2fms/frame)", time3, float64(time3.Milliseconds())/30)
	}
}
