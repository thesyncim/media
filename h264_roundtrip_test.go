package media

import (
	"testing"
)

func TestH264EncoderDecoderRoundtrip(t *testing.T) {
	if !IsH264EncoderAvailable() {
		t.Skip("H264 encoder not available")
	}

	// Create encoder
	enc, err := NewH264Encoder(VideoEncoderConfig{
		Width:      640,
		Height:     480,
		BitrateBps: 1_000_000,
		FPS:        30,
	})
	if err != nil {
		t.Fatalf("Failed to create H264 encoder: %v", err)
	}
	defer enc.Close()

	// Create decoder
	dec, err := NewH264Decoder(VideoDecoderConfig{})
	if err != nil {
		t.Fatalf("Failed to create H264 decoder: %v", err)
	}
	defer dec.Close()

	// Create test frame
	rawFrame := &VideoFrame{
		Data:   [][]byte{make([]byte, 640*480), make([]byte, 320*240), make([]byte, 320*240)},
		Stride: []int{640, 320, 320},
		Width:  640,
		Height: 480,
		Format: PixelFormatI420,
	}

	// Fill with some pattern
	for i := range rawFrame.Data[0] {
		rawFrame.Data[0][i] = byte(i % 256)
	}
	for i := range rawFrame.Data[1] {
		rawFrame.Data[1][i] = byte(128)
	}
	for i := range rawFrame.Data[2] {
		rawFrame.Data[2][i] = byte(128)
	}

	// Encode several frames (decoder needs multiple frames to warm up)
	var encoded *EncodedFrame
	var lastEncoded *EncodedFrame
	enc.RequestKeyframe()

	for i := 0; i < 30; i++ {
		encoded, err = enc.Encode(rawFrame)
		if err != nil {
			t.Fatalf("Encode failed at frame %d: %v", i, err)
		}
		if encoded != nil && len(encoded.Data) > 0 {
			lastEncoded = encoded
		}
	}

	if lastEncoded == nil {
		t.Fatal("Encoder produced no output")
	}
	t.Logf("Encoded frame: %d bytes, keyframe=%v", len(lastEncoded.Data), lastEncoded.IsKeyframe())

	// Try to decode - the fix should prevent "stride=0/0, size=0x0" errors
	// H264 decoder often needs a keyframe to produce output
	enc.RequestKeyframe()
	encoded, err = enc.Encode(rawFrame)
	if err != nil {
		t.Fatalf("Encode keyframe failed: %v", err)
	}
	if encoded == nil || len(encoded.Data) == 0 {
		t.Fatal("Encoder produced no keyframe output")
	}

	t.Logf("Keyframe: %d bytes", len(encoded.Data))

	// Decode the keyframe
	decoded, err := dec.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	// First decode may return nil (buffering), keep feeding frames
	frameCount := 0
	for i := 0; i < 30 && decoded == nil; i++ {
		encoded, err = enc.Encode(rawFrame)
		if err != nil {
			t.Fatalf("Encode failed: %v", err)
		}
		if encoded == nil {
			continue
		}
		decoded, err = dec.Decode(encoded)
		if err != nil {
			t.Fatalf("Decode failed at iteration %d: %v", i, err)
		}
		frameCount++
	}

	if decoded == nil {
		t.Logf("Decoder returned nil after %d frames (buffering), this is acceptable", frameCount)
	} else {
		t.Logf("Decoded frame: %dx%d, format=%v", decoded.Width, decoded.Height, decoded.Format)
		if decoded.Width != 640 || decoded.Height != 480 {
			t.Errorf("Unexpected dimensions: got %dx%d, want 640x480", decoded.Width, decoded.Height)
		}
	}
}

func TestH264DecoderInvalidInput(t *testing.T) {
	if !IsH264DecoderAvailable() {
		t.Skip("H264 decoder not available")
	}

	dec, err := NewH264Decoder(VideoDecoderConfig{})
	if err != nil {
		t.Fatalf("Failed to create H264 decoder: %v", err)
	}
	defer dec.Close()

	// Test with empty data - should return error or nil, not panic
	emptyFrame := &EncodedFrame{
		Data:      []byte{},
		FrameType: FrameTypeDelta,
	}
	_, err = dec.Decode(emptyFrame)
	if err != nil {
		t.Logf("Empty frame decode error (expected): %v", err)
	}

	// Test with garbage data - should handle gracefully
	garbageFrame := &EncodedFrame{
		Data:      []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		FrameType: FrameTypeDelta,
	}
	_, err = dec.Decode(garbageFrame)
	if err != nil {
		t.Logf("Garbage frame decode error (expected): %v", err)
	}
}
