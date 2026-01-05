package media

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestMultiTranscoderAllInputCodecs tests the MultiTranscoder with each input codec
// This simulates a WebRTC publisher sending different codec formats
func TestMultiTranscoderAllInputCodecs(t *testing.T) {
	inputCodecs := []struct {
		name         string
		codec        VideoCodec
		available    func() bool
		createEnc    func(VideoEncoderConfig) (VideoEncoder, error)
	}{
		{"VP8", VideoCodecVP8, IsVP8Available, func(c VideoEncoderConfig) (VideoEncoder, error) { return NewVP8Encoder(c) }},
		{"VP9", VideoCodecVP9, IsVP9Available, func(c VideoEncoderConfig) (VideoEncoder, error) { return NewVP9Encoder(c) }},
		{"H264", VideoCodecH264, IsH264EncoderAvailable, func(c VideoEncoderConfig) (VideoEncoder, error) { return NewH264Encoder(c) }},
		{"AV1", VideoCodecAV1, IsAV1Available, func(c VideoEncoderConfig) (VideoEncoder, error) { return NewAV1Encoder(c) }},
	}

	for _, input := range inputCodecs {
		t.Run("Input_"+input.name, func(t *testing.T) {
			if !input.available() {
				t.Skipf("%s not available", input.name)
			}
			testMultiTranscoderWithInputCodec(t, input.name, input.codec, input.createEnc)
		})
	}
}

func testMultiTranscoderWithInputCodec(t *testing.T, codecName string, inputCodec VideoCodec, createEnc func(VideoEncoderConfig) (VideoEncoder, error)) {
	// Create source encoder
	srcEnc, err := createEnc(VideoEncoderConfig{
		Width: 640, Height: 480, BitrateBps: 1_000_000, FPS: 30,
	})
	if err != nil {
		t.Fatalf("Failed to create %s encoder: %v", codecName, err)
	}
	defer srcEnc.Close()
	srcEncodeBuf := make([]byte, srcEnc.MaxEncodedSize())

	// Create MultiTranscoder with multiple output variants
	outputs := []OutputConfig{
		{ID: "passthrough", Codec: inputCodec, Passthrough: true},
	}

	// Add VP8 output if not same as input
	if inputCodec != VideoCodecVP8 && IsVP8Available() {
		outputs = append(outputs, OutputConfig{
			ID: "vp8-480p", Codec: VideoCodecVP8, Width: 640, Height: 480, BitrateBps: 800_000, FPS: 30,
		})
	}

	// Add VP9 output if not same as input
	if inputCodec != VideoCodecVP9 && IsVP9Available() {
		outputs = append(outputs, OutputConfig{
			ID: "vp9-480p", Codec: VideoCodecVP9, Width: 640, Height: 480, BitrateBps: 600_000, FPS: 30,
		})
	}

	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec: inputCodec,
		Outputs:    outputs,
	})
	if err != nil {
		t.Fatalf("Failed to create MultiTranscoder: %v", err)
	}
	defer mt.Close()

	// Create test frame
	rawFrame := &VideoFrame{
		Data:   [][]byte{make([]byte, 640*480), make([]byte, 320*240), make([]byte, 320*240)},
		Stride: []int{640, 320, 320},
		Width:  640,
		Height: 480,
		Format: PixelFormatI420,
	}

	// Fill with pattern
	for y := 0; y < 480; y++ {
		for x := 0; x < 640; x++ {
			rawFrame.Data[0][y*640+x] = byte((x + y) % 256)
		}
	}
	for i := range rawFrame.Data[1] {
		rawFrame.Data[1][i] = 128
		rawFrame.Data[2][i] = 128
	}

	// Encode and transcode frames
	srcEnc.RequestKeyframe()

	var totalVariants int
	var errors []string
	passthroughCount := 0
	transcodedCount := make(map[string]int)

	for i := 0; i < 60; i++ {
		// Request keyframe periodically
		if i%30 == 0 && i > 0 {
			srcEnc.RequestKeyframe()
		}

		encResult, err := srcEnc.Encode(rawFrame, srcEncodeBuf)
		if err != nil {
			t.Fatalf("Source encode failed at frame %d: %v", i, err)
		}
		if encResult.N == 0 {
			continue
		}
		encoded := &EncodedFrame{
			Data:      srcEncodeBuf[:encResult.N],
			FrameType: encResult.FrameType,
		}

		result, err := mt.Transcode(encoded)
		if err != nil {
			errors = append(errors, fmt.Sprintf("frame %d: %v", i, err))
			continue
		}

		if result != nil {
			for _, v := range result.Variants {
				totalVariants++
				if v.VariantID == "passthrough" {
					passthroughCount++
				} else {
					transcodedCount[v.VariantID]++
				}
			}
		}
	}

	t.Logf("%s input: passthrough=%d, transcoded=%v, errors=%d",
		codecName, passthroughCount, transcodedCount, len(errors))

	// Check results
	if passthroughCount < 50 {
		t.Errorf("%s: passthrough expected ~60 frames, got %d", codecName, passthroughCount)
	}

	for id, count := range transcodedCount {
		if count < 50 {
			t.Errorf("%s: %s expected ~60 frames, got %d", codecName, id, count)
		}
	}

	if len(errors) > 5 {
		t.Errorf("%s: too many errors (%d). First: %s", codecName, len(errors), errors[0])
	}
}

// TestMultiTranscoderDynamicAddAllCodecs tests adding outputs dynamically for each codec
func TestMultiTranscoderDynamicAddAllCodecs(t *testing.T) {
	if !IsVP8Available() {
		t.Skip("VP8 not available")
	}

	// Start with VP8 source
	srcEnc, err := NewVP8Encoder(VideoEncoderConfig{
		Width: 640, Height: 480, BitrateBps: 1_000_000, FPS: 30,
	})
	if err != nil {
		t.Fatalf("Failed to create VP8 encoder: %v", err)
	}
	defer srcEnc.Close()
	srcEncodeBuf := make([]byte, srcEnc.MaxEncodedSize())

	// Create transcoder with passthrough only
	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec: VideoCodecVP8,
		Outputs: []OutputConfig{
			{ID: "passthrough", Codec: VideoCodecVP8, Passthrough: true},
		},
	})
	if err != nil {
		t.Fatalf("Failed to create MultiTranscoder: %v", err)
	}
	defer mt.Close()

	rawFrame := &VideoFrame{
		Data:   [][]byte{make([]byte, 640*480), make([]byte, 320*240), make([]byte, 320*240)},
		Stride: []int{640, 320, 320},
		Width:  640,
		Height: 480,
		Format: PixelFormatI420,
	}

	srcEnc.RequestKeyframe()

	// Phase 1: Just passthrough
	t.Log("Phase 1: Passthrough only")
	phase1Count := transcodeFrames(t, mt, srcEnc, srcEncodeBuf, rawFrame, 30)
	t.Logf("Phase 1 results: %v", phase1Count)

	// Dynamically add VP8 (if different from input, skip)
	// Add VP9
	if IsVP9Available() {
		t.Log("Adding VP9 output dynamically...")
		err = mt.AddOutput(OutputConfig{
			ID: "dyn-vp9", Codec: VideoCodecVP9, Width: 640, Height: 480, BitrateBps: 800_000, FPS: 30,
		})
		if err != nil {
			t.Fatalf("Failed to add VP9: %v", err)
		}
	}

	// Phase 2: With VP9
	t.Log("Phase 2: With VP9")
	phase2Count := transcodeFrames(t, mt, srcEnc, srcEncodeBuf, rawFrame, 30)
	t.Logf("Phase 2 results: %v", phase2Count)

	// Add H264
	if IsH264EncoderAvailable() {
		t.Log("Adding H264 output dynamically...")
		err = mt.AddOutput(OutputConfig{
			ID: "dyn-h264", Codec: VideoCodecH264, Width: 640, Height: 480, BitrateBps: 800_000, FPS: 30,
		})
		if err != nil {
			t.Fatalf("Failed to add H264: %v", err)
		}
	}

	// Phase 3: With H264
	t.Log("Phase 3: With H264")
	phase3Count := transcodeFrames(t, mt, srcEnc, srcEncodeBuf, rawFrame, 30)
	t.Logf("Phase 3 results: %v", phase3Count)

	// Add AV1
	if IsAV1Available() {
		t.Log("Adding AV1 output dynamically...")
		err = mt.AddOutput(OutputConfig{
			ID: "dyn-av1", Codec: VideoCodecAV1, Width: 640, Height: 480, BitrateBps: 500_000, FPS: 30,
		})
		if err != nil {
			t.Fatalf("Failed to add AV1: %v", err)
		}
	}

	// Phase 4: With all codecs
	t.Log("Phase 4: All codecs")
	phase4Count := transcodeFrames(t, mt, srcEnc, srcEncodeBuf, rawFrame, 30)
	t.Logf("Phase 4 results: %v", phase4Count)

	// Verify each phase has increasing output
	if len(phase2Count) <= len(phase1Count) && IsVP9Available() {
		t.Error("Phase 2 should have more variants than Phase 1")
	}
	if len(phase3Count) <= len(phase2Count) && IsH264EncoderAvailable() {
		t.Error("Phase 3 should have more variants than Phase 2")
	}
}

func transcodeFrames(t *testing.T, mt *MultiTranscoder, enc VideoEncoder, encodeBuf []byte, frame *VideoFrame, count int) map[string]int {
	results := make(map[string]int)

	for i := 0; i < count; i++ {
		if i%30 == 0 {
			enc.RequestKeyframe()
		}

		encResult, err := enc.Encode(frame, encodeBuf)
		if err != nil || encResult.N == 0 {
			continue
		}
		encoded := &EncodedFrame{
			Data:      encodeBuf[:encResult.N],
			FrameType: encResult.FrameType,
		}

		result, err := mt.Transcode(encoded)
		if err != nil {
			t.Logf("Transcode error at frame %d: %v", i, err)
			continue
		}

		if result != nil {
			for _, v := range result.Variants {
				results[v.VariantID]++
			}
		}
	}

	return results
}

// TestH264InputSpecifically tests H264 as input codec with detailed logging
func TestH264InputSpecifically(t *testing.T) {
	if !IsH264EncoderAvailable() {
		t.Skip("H264 encoder not available")
	}
	if !IsH264DecoderAvailable() {
		t.Skip("H264 decoder not available")
	}

	// Create H264 encoder (simulates browser/camera H264)
	srcEnc, err := NewH264Encoder(VideoEncoderConfig{
		Width: 1280, Height: 720, BitrateBps: 2_000_000, FPS: 30,
	})
	if err != nil {
		t.Fatalf("Failed to create H264 encoder: %v", err)
	}
	defer srcEnc.Close()
	srcEncodeBuf := make([]byte, srcEnc.MaxEncodedSize())

	// Create transcoder with H264 input, output to VP8
	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec: VideoCodecH264,
		Outputs: []OutputConfig{
			{ID: "passthrough", Codec: VideoCodecH264, Passthrough: true},
			{ID: "vp8-720p", Codec: VideoCodecVP8, Width: 1280, Height: 720, BitrateBps: 1_500_000, FPS: 30},
		},
	})
	if err != nil {
		t.Fatalf("Failed to create MultiTranscoder: %v", err)
	}
	defer mt.Close()

	rawFrame := &VideoFrame{
		Data:   [][]byte{make([]byte, 1280*720), make([]byte, 640*360), make([]byte, 640*360)},
		Stride: []int{1280, 640, 640},
		Width:  1280,
		Height: 720,
		Format: PixelFormatI420,
	}

	// Fill with gradient
	for y := 0; y < 720; y++ {
		for x := 0; x < 1280; x++ {
			rawFrame.Data[0][y*1280+x] = byte((x + y) % 256)
		}
	}
	for i := range rawFrame.Data[1] {
		rawFrame.Data[1][i] = 128
		rawFrame.Data[2][i] = 128
	}

	srcEnc.RequestKeyframe()

	var passthroughCount, vp8Count, errorCount int
	var firstError string

	for i := 0; i < 90; i++ {
		if i%30 == 0 && i > 0 {
			srcEnc.RequestKeyframe()
		}

		encResult, err := srcEnc.Encode(rawFrame, srcEncodeBuf)
		if err != nil {
			t.Fatalf("H264 encode failed: %v", err)
		}
		if encResult.N == 0 {
			continue
		}
		encoded := &EncodedFrame{
			Data:      srcEncodeBuf[:encResult.N],
			FrameType: encResult.FrameType,
		}

		t.Logf("Frame %d: encoded %d bytes, keyframe=%v", i, len(encoded.Data), encoded.IsKeyframe())

		result, err := mt.Transcode(encoded)
		if err != nil {
			errorCount++
			if firstError == "" {
				firstError = err.Error()
			}
			t.Logf("Frame %d: transcode error: %v", i, err)
			continue
		}

		if result != nil {
			for _, v := range result.Variants {
				if v.VariantID == "passthrough" {
					passthroughCount++
				} else if v.VariantID == "vp8-720p" {
					vp8Count++
				}
			}
		}
	}

	t.Logf("H264 Input Results: passthrough=%d, vp8=%d, errors=%d", passthroughCount, vp8Count, errorCount)

	if passthroughCount < 80 {
		t.Errorf("Passthrough: expected ~90, got %d", passthroughCount)
	}

	if vp8Count < 80 {
		t.Errorf("VP8 transcoded: expected ~90, got %d (first error: %s)", vp8Count, firstError)
	}

	if errorCount > 5 {
		t.Errorf("Too many errors: %d (first: %s)", errorCount, firstError)
	}
}

// TestConcurrentTranscoding tests multiple transcoders running in parallel
func TestConcurrentTranscoding(t *testing.T) {
	if !IsVP8Available() || !IsVP9Available() {
		t.Skip("VP8 or VP9 not available")
	}

	const numTranscoders = 3
	const framesPerTranscoder = 60

	var wg sync.WaitGroup
	var totalFrames int64
	var totalErrors int64

	for i := 0; i < numTranscoders; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			enc, err := NewVP8Encoder(VideoEncoderConfig{
				Width: 640, Height: 480, BitrateBps: 1_000_000, FPS: 30,
			})
			if err != nil {
				t.Errorf("Transcoder %d: failed to create encoder: %v", id, err)
				return
			}
			defer enc.Close()
			encodeBuf := make([]byte, enc.MaxEncodedSize())

			mt, err := NewMultiTranscoder(MultiTranscoderConfig{
				InputCodec: VideoCodecVP8,
				Outputs: []OutputConfig{
					{ID: "vp9", Codec: VideoCodecVP9, Width: 640, Height: 480, BitrateBps: 800_000, FPS: 30},
				},
			})
			if err != nil {
				t.Errorf("Transcoder %d: failed to create transcoder: %v", id, err)
				return
			}
			defer mt.Close()

			frame := &VideoFrame{
				Data:   [][]byte{make([]byte, 640*480), make([]byte, 320*240), make([]byte, 320*240)},
				Stride: []int{640, 320, 320},
				Width:  640,
				Height: 480,
				Format: PixelFormatI420,
			}

			enc.RequestKeyframe()

			for j := 0; j < framesPerTranscoder; j++ {
				encResult, _ := enc.Encode(frame, encodeBuf)
				if encResult.N == 0 {
					continue
				}
				encoded := &EncodedFrame{
					Data:      encodeBuf[:encResult.N],
					FrameType: encResult.FrameType,
				}

				result, err := mt.Transcode(encoded)
				if err != nil {
					atomic.AddInt64(&totalErrors, 1)
					continue
				}

				if result != nil && len(result.Variants) > 0 {
					atomic.AddInt64(&totalFrames, int64(len(result.Variants)))
				}
			}
		}(i)
	}

	wg.Wait()

	frames := atomic.LoadInt64(&totalFrames)
	errors := atomic.LoadInt64(&totalErrors)

	t.Logf("Concurrent test: %d transcoders, %d total frames, %d errors", numTranscoders, frames, errors)

	expectedMin := int64(numTranscoders * framesPerTranscoder * 80 / 100) // 80% success rate
	if frames < expectedMin {
		t.Errorf("Expected at least %d frames, got %d", expectedMin, frames)
	}
}
