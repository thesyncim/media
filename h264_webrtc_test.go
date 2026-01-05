package media

import (
	"testing"
)

// TestH264DecoderBuffering tests that the decoder correctly handles buffering
// and doesn't return errors when OpenH264 needs more frames
func TestH264DecoderBuffering(t *testing.T) {
	if !IsH264EncoderAvailable() {
		t.Skip("H264 encoder not available")
	}

	// Create encoder with settings similar to WebRTC
	enc, err := NewH264Encoder(VideoEncoderConfig{
		Width:      1280,
		Height:     720,
		BitrateBps: 2_000_000,
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
		Data:   [][]byte{make([]byte, 1280*720), make([]byte, 640*360), make([]byte, 640*360)},
		Stride: []int{1280, 640, 640},
		Width:  1280,
		Height: 720,
		Format: PixelFormatI420,
	}

	// Fill with gradient pattern
	for y := 0; y < 720; y++ {
		for x := 0; x < 1280; x++ {
			rawFrame.Data[0][y*1280+x] = byte((x + y) % 256)
		}
	}
	for i := range rawFrame.Data[1] {
		rawFrame.Data[1][i] = 128
	}
	for i := range rawFrame.Data[2] {
		rawFrame.Data[2][i] = 128
	}

	// Encode frames and try to decode them one by one
	// This simulates WebRTC where frames arrive one at a time
	var errors []string
	var decodedCount, bufferingCount int

	for i := 0; i < 60; i++ {
		// Request keyframe periodically
		if i%30 == 0 {
			enc.RequestKeyframe()
		}

		encoded, err := enc.Encode(rawFrame)
		if err != nil {
			t.Fatalf("Encode failed at frame %d: %v", i, err)
		}

		if encoded == nil || len(encoded.Data) == 0 {
			continue
		}

		// Try to decode
		decoded, err := dec.Decode(encoded)
		if err != nil {
			// This should NOT happen - decoder should return nil,nil when buffering
			errors = append(errors, err.Error())
		} else if decoded == nil {
			bufferingCount++
		} else {
			decodedCount++
			if i < 5 {
				t.Logf("Frame %d: decoded %dx%d", i, decoded.Width, decoded.Height)
			}
		}
	}

	t.Logf("Decoded: %d, Buffering: %d, Errors: %d", decodedCount, bufferingCount, len(errors))

	if len(errors) > 0 {
		t.Errorf("Got %d decode errors (expected 0). First error: %s", len(errors), errors[0])
	}

	if decodedCount == 0 {
		t.Error("No frames were decoded successfully")
	}
}

// TestH264DecoderSmallKeyframe tests decoding of small keyframes that might
// have incomplete parameter sets
func TestH264DecoderSmallKeyframe(t *testing.T) {
	if !IsH264DecoderAvailable() {
		t.Skip("H264 decoder not available")
	}

	dec, err := NewH264Decoder(VideoDecoderConfig{})
	if err != nil {
		t.Fatalf("Failed to create H264 decoder: %v", err)
	}
	defer dec.Close()

	// Create a minimal H264 keyframe with just SPS NAL unit (incomplete)
	// Real WebRTC might send frames before full parameter sets are available
	spsNAL := []byte{
		0x00, 0x00, 0x00, 0x01, // Start code
		0x67, // NAL type 7 (SPS)
		0x42, 0xc0, 0x1f, 0xda, 0x01, 0x10, 0x00, 0x00, 0x03, 0x00, 0x10,
	}

	// This should NOT panic or return stride=0/0 error
	// It should return nil,nil (buffering) or a proper error
	decoded, err := dec.Decode(&EncodedFrame{
		Data:      spsNAL,
		FrameType: FrameTypeKey,
	})

	if err != nil {
		// Error is acceptable for incomplete data, but should be meaningful
		t.Logf("Incomplete SPS decode result: error=%v", err)
		// Make sure it's not the "stride=0/0" error
		if err.Error() == "invalid decoder output: stride=0/0, size=0x0" {
			t.Error("Got stride=0/0 error instead of proper buffering behavior")
		}
	} else if decoded != nil {
		t.Log("Unexpectedly got decoded output from incomplete SPS")
	} else {
		t.Log("Correctly returned nil for incomplete data")
	}
}
