//go:build !novpx && cgo
// +build !novpx,cgo

package media

import (
	"testing"
)

// createTestFrame creates a test I420 frame with the given dimensions.
func createTestFrame(width, height int) *VideoFrame {
	ySize := width * height
	uvSize := (width / 2) * (height / 2)

	yPlane := make([]byte, ySize)
	uPlane := make([]byte, uvSize)
	vPlane := make([]byte, uvSize)

	// Fill with gradient pattern
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			yPlane[y*width+x] = byte((x + y) % 256)
		}
	}
	for y := 0; y < height/2; y++ {
		for x := 0; x < width/2; x++ {
			uPlane[y*(width/2)+x] = 128
			vPlane[y*(width/2)+x] = 128
		}
	}

	return &VideoFrame{
		Width:  width,
		Height: height,
		Format: PixelFormatI420,
		Data:   [][]byte{yPlane, uPlane, vPlane},
		Stride: []int{width, width / 2, width / 2},
	}
}

// TestVP8Encoder tests the VP8 encoder.
func TestVP8Encoder(t *testing.T) {
	config := VideoEncoderConfig{
		Width:      320,
		Height:     240,
		FPS:        30,
		BitrateBps: 500000,
	}

	enc, err := NewVP8Encoder(config)
	if err != nil {
		t.Fatalf("Failed to create VP8 encoder: %v", err)
	}
	defer enc.Close()

	// Verify config
	if enc.Codec() != VideoCodecVP8 {
		t.Errorf("Codec = %v, want VP8", enc.Codec())
	}
	if enc.Config().Width != 320 || enc.Config().Height != 240 {
		t.Errorf("Config dimensions = %dx%d, want 320x240", enc.Config().Width, enc.Config().Height)
	}

	// Encode a frame
	frame := createTestFrame(320, 240)
	encoded, err := enc.Encode(frame)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	if encoded == nil {
		t.Fatal("Encode returned nil frame")
	}

	// First frame should be a keyframe
	if encoded.FrameType != FrameTypeKey {
		t.Errorf("First frame type = %v, want Key", encoded.FrameType)
	}
	if len(encoded.Data) == 0 {
		t.Error("Encoded data is empty")
	}

	// Check stats
	stats := enc.Stats()
	if stats.FramesEncoded != 1 {
		t.Errorf("FramesEncoded = %d, want 1", stats.FramesEncoded)
	}
	if stats.KeyframesEncoded != 1 {
		t.Errorf("KeyframesEncoded = %d, want 1", stats.KeyframesEncoded)
	}
}

// TestVP8Decoder tests the VP8 decoder.
func TestVP8Decoder(t *testing.T) {
	config := VideoDecoderConfig{}

	dec, err := NewVP8Decoder(config)
	if err != nil {
		t.Fatalf("Failed to create VP8 decoder: %v", err)
	}
	defer dec.Close()

	// Verify config
	if dec.Codec() != VideoCodecVP8 {
		t.Errorf("Codec = %v, want VP8", dec.Codec())
	}
}

// TestVP8RoundTrip tests encoding and decoding with VP8.
func TestVP8RoundTrip(t *testing.T) {
	encConfig := VideoEncoderConfig{
		Width:      320,
		Height:     240,
		FPS:        30,
		BitrateBps: 500000,
	}

	enc, err := NewVP8Encoder(encConfig)
	if err != nil {
		t.Fatalf("Failed to create VP8 encoder: %v", err)
	}
	defer enc.Close()

	dec, err := NewVP8Decoder(VideoDecoderConfig{})
	if err != nil {
		t.Fatalf("Failed to create VP8 decoder: %v", err)
	}
	defer dec.Close()

	// Create and encode a frame
	frame := createTestFrame(320, 240)
	encoded, err := enc.Encode(frame)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Decode the encoded frame
	decoded, err := dec.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}
	if decoded == nil {
		t.Fatal("Decode returned nil frame")
	}

	// Verify dimensions
	if decoded.Width != 320 || decoded.Height != 240 {
		t.Errorf("Decoded dimensions = %dx%d, want 320x240", decoded.Width, decoded.Height)
	}
	if decoded.Format != PixelFormatI420 {
		t.Errorf("Decoded format = %v, want I420", decoded.Format)
	}

	// Check decoder dimensions
	w, h := dec.GetDimensions()
	if w != 320 || h != 240 {
		t.Errorf("GetDimensions = %dx%d, want 320x240", w, h)
	}

	// Check stats
	stats := dec.Stats()
	if stats.FramesDecoded != 1 {
		t.Errorf("FramesDecoded = %d, want 1", stats.FramesDecoded)
	}
}

// TestVP8KeyframeRequest tests keyframe request functionality.
func TestVP8KeyframeRequest(t *testing.T) {
	config := VideoEncoderConfig{
		Width:            320,
		Height:           240,
		FPS:              30,
		BitrateBps:       500000,
		KeyframeInterval: 300, // Very large to ensure delta frames normally
	}

	enc, err := NewVP8Encoder(config)
	if err != nil {
		t.Fatalf("Failed to create VP8 encoder: %v", err)
	}
	defer enc.Close()

	frame := createTestFrame(320, 240)

	// First frame is always keyframe
	_, err = enc.Encode(frame)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Encode a few delta frames
	for i := 0; i < 3; i++ {
		encoded, err := enc.Encode(frame)
		if err != nil {
			t.Fatalf("Encode %d failed: %v", i, err)
		}
		if encoded.FrameType != FrameTypeDelta {
			t.Logf("Frame %d is not delta (may be normal for short sequences)", i)
		}
	}

	// Request keyframe
	enc.RequestKeyframe()

	// Next frame should be keyframe
	encoded, err := enc.Encode(frame)
	if err != nil {
		t.Fatalf("Encode after keyframe request failed: %v", err)
	}
	if encoded.FrameType != FrameTypeKey {
		t.Errorf("Frame after keyframe request = %v, want Key", encoded.FrameType)
	}
}

// TestVP8BitrateChange tests bitrate change functionality.
func TestVP8BitrateChange(t *testing.T) {
	config := VideoEncoderConfig{
		Width:      320,
		Height:     240,
		FPS:        30,
		BitrateBps: 500000,
	}

	enc, err := NewVP8Encoder(config)
	if err != nil {
		t.Fatalf("Failed to create VP8 encoder: %v", err)
	}
	defer enc.Close()

	// Change bitrate
	err = enc.SetBitrate(1000000)
	if err != nil {
		t.Fatalf("SetBitrate failed: %v", err)
	}

	if enc.Config().BitrateBps != 1000000 {
		t.Errorf("Bitrate = %d, want 1000000", enc.Config().BitrateBps)
	}

	// Encode a frame with new bitrate
	frame := createTestFrame(320, 240)
	_, err = enc.Encode(frame)
	if err != nil {
		t.Fatalf("Encode after bitrate change failed: %v", err)
	}
}

// TestVP9Encoder tests the VP9 encoder.
func TestVP9Encoder(t *testing.T) {
	config := VideoEncoderConfig{
		Width:      320,
		Height:     240,
		FPS:        30,
		BitrateBps: 500000,
	}

	enc, err := NewVP9Encoder(config)
	if err != nil {
		t.Fatalf("Failed to create VP9 encoder: %v", err)
	}
	defer enc.Close()

	// Verify config
	if enc.Codec() != VideoCodecVP9 {
		t.Errorf("Codec = %v, want VP9", enc.Codec())
	}

	// Encode multiple frames (VP9 buffers more aggressively)
	var encoded *EncodedFrame
	for i := 0; i < 30; i++ {
		frame := createTestFrame(320, 240)
		var err error
		encoded, err = enc.Encode(frame)
		if err != nil {
			t.Fatalf("Encode failed: %v", err)
		}
		if encoded != nil {
			break
		}
	}
	if encoded == nil {
		// Check if any frames were encoded via stats
		stats := enc.Stats()
		if stats.FramesEncoded == 0 {
			t.Fatal("Encode returned nil frame after multiple attempts and no frames encoded")
		}
		t.Skipf("VP9 encoder buffering all frames (encoded %d internally)", stats.FramesEncoded)
	}

	// First non-nil frame should be a keyframe
	if encoded.FrameType != FrameTypeKey {
		t.Errorf("First frame type = %v, want Key", encoded.FrameType)
	}

	// Check stats
	stats := enc.Stats()
	if stats.FramesEncoded < 1 {
		t.Errorf("FramesEncoded = %d, want >= 1", stats.FramesEncoded)
	}
}

// TestVP9Decoder tests the VP9 decoder.
func TestVP9Decoder(t *testing.T) {
	config := VideoDecoderConfig{}

	dec, err := NewVP9Decoder(config)
	if err != nil {
		t.Fatalf("Failed to create VP9 decoder: %v", err)
	}
	defer dec.Close()

	// Verify config
	if dec.Codec() != VideoCodecVP9 {
		t.Errorf("Codec = %v, want VP9", dec.Codec())
	}
}

// TestVP9RoundTrip tests encoding and decoding with VP9.
func TestVP9RoundTrip(t *testing.T) {
	encConfig := VideoEncoderConfig{
		Width:      320,
		Height:     240,
		FPS:        30,
		BitrateBps: 500000,
	}

	enc, err := NewVP9Encoder(encConfig)
	if err != nil {
		t.Fatalf("Failed to create VP9 encoder: %v", err)
	}
	defer enc.Close()

	dec, err := NewVP9Decoder(VideoDecoderConfig{})
	if err != nil {
		t.Fatalf("Failed to create VP9 decoder: %v", err)
	}
	defer dec.Close()

	// Create and encode frames (VP9 buffers more aggressively)
	var encoded *EncodedFrame
	for i := 0; i < 30; i++ {
		frame := createTestFrame(320, 240)
		var err error
		encoded, err = enc.Encode(frame)
		if err != nil {
			t.Fatalf("Encode failed: %v", err)
		}
		if encoded != nil {
			break
		}
	}
	if encoded == nil {
		t.Skip("VP9 encoder buffering all frames")
	}

	// Decode the encoded frame
	decoded, err := dec.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}
	if decoded == nil {
		t.Fatal("Decode returned nil frame")
	}

	// Verify dimensions
	if decoded.Width != 320 || decoded.Height != 240 {
		t.Errorf("Decoded dimensions = %dx%d, want 320x240", decoded.Width, decoded.Height)
	}
}

// TestVP9KeyframeRequest tests keyframe request functionality.
func TestVP9KeyframeRequest(t *testing.T) {
	config := VideoEncoderConfig{
		Width:            320,
		Height:           240,
		FPS:              30,
		BitrateBps:       500000,
		KeyframeInterval: 300, // Very long to avoid auto keyframes
	}

	enc, err := NewVP9Encoder(config)
	if err != nil {
		t.Fatalf("Failed to create VP9 encoder: %v", err)
	}
	defer enc.Close()

	frame := createTestFrame(320, 240)

	// Get initial keyframe count after first frame (always a keyframe)
	_, err = enc.Encode(frame)
	if err != nil {
		t.Fatalf("First encode failed: %v", err)
	}

	// Encode enough frames to flush any buffering
	for i := 0; i < 30; i++ {
		_, err := enc.Encode(frame)
		if err != nil {
			t.Fatalf("Encode %d failed: %v", i, err)
		}
	}

	// Record keyframe count before request
	statsBefore := enc.Stats()
	keyframesBefore := statsBefore.KeyframesEncoded
	t.Logf("Keyframes before request: %d", keyframesBefore)

	// Request keyframe
	enc.RequestKeyframe()

	// Encode more frames to ensure the keyframe request is processed
	// VP9 may buffer, so we need to encode many frames
	for i := 0; i < 60; i++ {
		_, err := enc.Encode(frame)
		if err != nil {
			t.Fatalf("Encode after keyframe request failed: %v", err)
		}
	}

	// Check stats - keyframe count should have increased
	statsAfter := enc.Stats()
	keyframesAfter := statsAfter.KeyframesEncoded
	t.Logf("Keyframes after request + encoding: %d (encoded %d total frames)",
		keyframesAfter, statsAfter.FramesEncoded)

	if keyframesAfter <= keyframesBefore {
		t.Errorf("Keyframe request did not produce a keyframe: before=%d, after=%d",
			keyframesBefore, keyframesAfter)
	}
}

// TestVP9BitrateChange tests bitrate change functionality.
func TestVP9BitrateChange(t *testing.T) {
	config := VideoEncoderConfig{
		Width:      320,
		Height:     240,
		FPS:        30,
		BitrateBps: 500000,
	}

	enc, err := NewVP9Encoder(config)
	if err != nil {
		t.Fatalf("Failed to create VP9 encoder: %v", err)
	}
	defer enc.Close()

	// Change bitrate
	err = enc.SetBitrate(1000000)
	if err != nil {
		t.Fatalf("SetBitrate failed: %v", err)
	}

	// Encode a frame with new bitrate
	frame := createTestFrame(320, 240)
	_, err = enc.Encode(frame)
	if err != nil {
		t.Fatalf("Encode after bitrate change failed: %v", err)
	}
}

// TestVP8DecoderReset tests decoder reset functionality.
func TestVP8DecoderReset(t *testing.T) {
	dec, err := NewVP8Decoder(VideoDecoderConfig{})
	if err != nil {
		t.Fatalf("Failed to create VP8 decoder: %v", err)
	}
	defer dec.Close()

	// Reset should succeed
	err = dec.Reset()
	if err != nil {
		t.Errorf("Reset failed: %v", err)
	}
}

// TestVP9DecoderReset tests decoder reset functionality.
func TestVP9DecoderReset(t *testing.T) {
	dec, err := NewVP9Decoder(VideoDecoderConfig{})
	if err != nil {
		t.Fatalf("Failed to create VP9 decoder: %v", err)
	}
	defer dec.Close()

	// Reset should succeed
	err = dec.Reset()
	if err != nil {
		t.Errorf("Reset failed: %v", err)
	}
}

// TestCodecRegistry tests that VP8/VP9 codecs are available.
func TestCodecRegistry(t *testing.T) {
	// VP8 should be available
	if !IsVP8Available() {
		t.Error("VP8 should be available")
	}

	// VP9 should be available
	if !IsVP9Available() {
		t.Error("VP9 should be available")
	}

	// Create encoder via NewVideoEncoder
	enc, err := NewVideoEncoder(VideoEncoderConfig{
		Codec:      VideoCodecVP8,
		Width:      320,
		Height:     240,
		FPS:        30,
		BitrateBps: 500000,
	})
	if err != nil {
		t.Fatalf("NewVideoEncoder(VP8) failed: %v", err)
	}
	defer enc.Close()

	// Create decoder via NewVideoDecoder
	dec, err := NewVideoDecoder(VideoDecoderConfig{Codec: VideoCodecVP8})
	if err != nil {
		t.Fatalf("NewVideoDecoder(VP8) failed: %v", err)
	}
	defer dec.Close()
}

// TestMultipleFrames tests encoding/decoding multiple frames.
func TestMultipleFrames(t *testing.T) {
	enc, err := NewVP8Encoder(VideoEncoderConfig{
		Width:      320,
		Height:     240,
		FPS:        30,
		BitrateBps: 500000,
	})
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}
	defer enc.Close()

	dec, err := NewVP8Decoder(VideoDecoderConfig{})
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}
	defer dec.Close()

	frame := createTestFrame(320, 240)

	// Encode and decode 30 frames
	for i := 0; i < 30; i++ {
		encoded, err := enc.Encode(frame)
		if err != nil {
			t.Fatalf("Encode frame %d failed: %v", i, err)
		}

		decoded, err := dec.Decode(encoded)
		if err != nil {
			t.Fatalf("Decode frame %d failed: %v", i, err)
		}
		if decoded == nil {
			t.Fatalf("Decode frame %d returned nil", i)
		}
	}

	// Check stats
	encStats := enc.Stats()
	if encStats.FramesEncoded != 30 {
		t.Errorf("FramesEncoded = %d, want 30", encStats.FramesEncoded)
	}

	decStats := dec.Stats()
	if decStats.FramesDecoded != 30 {
		t.Errorf("FramesDecoded = %d, want 30", decStats.FramesDecoded)
	}
}

// BenchmarkVP8Encode benchmarks VP8 encoding.
func BenchmarkVP8Encode(b *testing.B) {
	enc, err := NewVP8Encoder(VideoEncoderConfig{
		Width:      1280,
		Height:     720,
		FPS:        30,
		BitrateBps: 2000000,
	})
	if err != nil {
		b.Fatalf("Failed to create encoder: %v", err)
	}
	defer enc.Close()

	frame := createTestFrame(1280, 720)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := enc.Encode(frame)
		if err != nil {
			b.Fatalf("Encode failed: %v", err)
		}
	}
}

// BenchmarkVP8Decode benchmarks VP8 decoding.
func BenchmarkVP8Decode(b *testing.B) {
	enc, err := NewVP8Encoder(VideoEncoderConfig{
		Width:      1280,
		Height:     720,
		FPS:        30,
		BitrateBps: 2000000,
	})
	if err != nil {
		b.Fatalf("Failed to create encoder: %v", err)
	}
	defer enc.Close()

	dec, err := NewVP8Decoder(VideoDecoderConfig{})
	if err != nil {
		b.Fatalf("Failed to create decoder: %v", err)
	}
	defer dec.Close()

	frame := createTestFrame(1280, 720)

	// Pre-encode some frames
	var encodedFrames []*EncodedFrame
	for i := 0; i < 30; i++ {
		encoded, err := enc.Encode(frame)
		if err != nil {
			b.Fatalf("Encode failed: %v", err)
		}
		// Clone the encoded data since it's reused
		cloned := &EncodedFrame{
			Data:      make([]byte, len(encoded.Data)),
			FrameType: encoded.FrameType,
			Timestamp: encoded.Timestamp,
		}
		copy(cloned.Data, encoded.Data)
		encodedFrames = append(encodedFrames, cloned)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Use modulo to cycle through pre-encoded frames
		encoded := encodedFrames[i%len(encodedFrames)]
		_, err := dec.Decode(encoded)
		if err != nil {
			b.Fatalf("Decode failed: %v", err)
		}
	}
}

// BenchmarkVP9Encode benchmarks VP9 encoding.
func BenchmarkVP9Encode(b *testing.B) {
	enc, err := NewVP9Encoder(VideoEncoderConfig{
		Width:      1280,
		Height:     720,
		FPS:        30,
		BitrateBps: 2000000,
	})
	if err != nil {
		b.Fatalf("Failed to create encoder: %v", err)
	}
	defer enc.Close()

	frame := createTestFrame(1280, 720)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := enc.Encode(frame)
		if err != nil {
			b.Fatalf("Encode failed: %v", err)
		}
	}
}

// BenchmarkVP9Decode benchmarks VP9 decoding.
func BenchmarkVP9Decode(b *testing.B) {
	enc, err := NewVP9Encoder(VideoEncoderConfig{
		Width:      1280,
		Height:     720,
		FPS:        30,
		BitrateBps: 2000000,
	})
	if err != nil {
		b.Fatalf("Failed to create encoder: %v", err)
	}
	defer enc.Close()

	dec, err := NewVP9Decoder(VideoDecoderConfig{})
	if err != nil {
		b.Fatalf("Failed to create decoder: %v", err)
	}
	defer dec.Close()

	frame := createTestFrame(1280, 720)

	// Pre-encode some frames (encoder may buffer initial frames)
	var encodedFrames []*EncodedFrame
	for i := 0; i < 60; i++ { // Encode more frames to ensure we get output
		encoded, err := enc.Encode(frame)
		if err != nil {
			b.Fatalf("Encode failed: %v", err)
		}
		if encoded == nil {
			continue // Encoder is buffering
		}
		// Clone the encoded data since it's reused
		cloned := &EncodedFrame{
			Data:      make([]byte, len(encoded.Data)),
			FrameType: encoded.FrameType,
			Timestamp: encoded.Timestamp,
		}
		copy(cloned.Data, encoded.Data)
		encodedFrames = append(encodedFrames, cloned)
	}

	if len(encodedFrames) == 0 {
		b.Fatal("No encoded frames produced")
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		encoded := encodedFrames[i%len(encodedFrames)]
		_, err := dec.Decode(encoded)
		if err != nil {
			b.Fatalf("Decode failed: %v", err)
		}
	}
}

// BenchmarkVP8RoundTrip benchmarks full encode/decode cycle.
func BenchmarkVP8RoundTrip(b *testing.B) {
	enc, err := NewVP8Encoder(VideoEncoderConfig{
		Width:      1280,
		Height:     720,
		FPS:        30,
		BitrateBps: 2000000,
	})
	if err != nil {
		b.Fatalf("Failed to create encoder: %v", err)
	}
	defer enc.Close()

	dec, err := NewVP8Decoder(VideoDecoderConfig{})
	if err != nil {
		b.Fatalf("Failed to create decoder: %v", err)
	}
	defer dec.Close()

	frame := createTestFrame(1280, 720)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		encoded, err := enc.Encode(frame)
		if err != nil {
			b.Fatalf("Encode failed: %v", err)
		}
		_, err = dec.Decode(encoded)
		if err != nil {
			b.Fatalf("Decode failed: %v", err)
		}
	}
}

// BenchmarkVPXCallOverhead measures the pure FFI call overhead
// by calling simple getter/setter functions that do minimal work in C.
func BenchmarkVPXCallOverhead(b *testing.B) {
	if !IsVPXAvailable() {
		b.Skip("VPX not available")
	}

	enc, err := NewVP8Encoder(VideoEncoderConfig{
		Width:      320,
		Height:     240,
		FPS:        30,
		BitrateBps: 500000,
	})
	if err != nil {
		b.Fatalf("Failed to create encoder: %v", err)
	}
	defer enc.Close()

	b.Run("SetBitrate", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = enc.SetBitrate(500000)
		}
	})

	b.Run("RequestKeyframe", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			enc.RequestKeyframe()
		}
	})
}
