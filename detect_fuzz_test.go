package media

import (
	"testing"
)

// FuzzDetectVideoCodec tests codec detection with random inputs.
// Run with: go test -fuzz=FuzzDetectVideoCodec -fuzztime=30s
func FuzzDetectVideoCodec(f *testing.F) {
	// Add seed corpus with known codec patterns
	seeds := [][]byte{
		// H264 Annex-B patterns
		{0x00, 0x00, 0x00, 0x01, 0x67}, // SPS
		{0x00, 0x00, 0x00, 0x01, 0x68}, // PPS
		{0x00, 0x00, 0x00, 0x01, 0x65}, // IDR
		{0x00, 0x00, 0x01, 0x61, 0x00}, // 3-byte start code + slice

		// H264 AVCC
		{0x00, 0x00, 0x00, 0x05, 0x67, 0x42, 0x00, 0x0A, 0x00},

		// VP8 keyframe
		{0x00, 0x00, 0x00, 0x9D, 0x01, 0x2A, 0x00, 0x00, 0x00, 0x00},
		{0x10, 0x00, 0x00, 0x9D, 0x01, 0x2A, 0x00, 0x00, 0x00, 0x00},

		// VP9 frames (need at least 3 bytes)
		{0x82, 0x49, 0x83},       // frame_marker = 0b10
		{0x80, 0x00, 0x00},       // frame_marker = 0b10
		{0xA0, 0x00, 0x00, 0x00}, // frame_marker = 0b10

		// AV1 OBUs (need at least 2 bytes)
		{0x0A, 0x00},             // Sequence header (type 1)
		{0x12, 0x00},             // Temporal delimiter (type 2)
		{0x32, 0x00, 0x00, 0x00}, // Frame header (type 6)

		// IVF headers
		{'D', 'K', 'I', 'F', 0, 0, 32, 0, 'V', 'P', '8', '0', 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		{'D', 'K', 'I', 'F', 0, 0, 32, 0, 'V', 'P', '9', '0', 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		{'D', 'K', 'I', 'F', 0, 0, 32, 0, 'A', 'V', '0', '1', 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},

		// Edge cases
		{},
		{0x00},
		{0x00, 0x00},
		{0x00, 0x00, 0x00},
		{0xFF, 0xFF, 0xFF, 0xFF},
		{0xC0, 0xC1, 0xC2, 0xC3},
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		// The function should never panic
		result := DetectVideoCodec(data)

		// Result must be a valid VideoCodec value
		if result < VideoCodecUnknown || result > VideoCodecAV1 {
			t.Errorf("DetectVideoCodec returned invalid codec: %d", result)
		}

		// Verify deterministic behavior
		result2 := DetectVideoCodec(data)
		if result != result2 {
			t.Errorf("DetectVideoCodec not deterministic: %v != %v", result, result2)
		}
	})
}

// FuzzIsAnnexBStartCode tests Annex-B start code detection
func FuzzIsAnnexBStartCode(f *testing.F) {
	seeds := [][]byte{
		{0x00, 0x00, 0x00, 0x01},       // 4-byte
		{0x00, 0x00, 0x01, 0x00},       // 3-byte (needs 4 bytes min)
		{0x00, 0x00, 0x00, 0x01, 0x67}, // with NAL
		{0x00, 0x00, 0x01, 0x67},       // 3-byte with NAL
		{},
		{0x00},
		{0x00, 0x00},
		{0x00, 0x00, 0x00},
		{0x00, 0x00, 0x02, 0x00},
		{0xFF, 0xFF, 0xFF, 0xFF},
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		// Should never panic
		result := isAnnexBStartCode(data)

		// The function requires len >= 4
		if len(data) < 4 && result {
			t.Error("isAnnexBStartCode should return false for data < 4 bytes")
		}

		// Verify result matches manual check for data >= 4 bytes
		if len(data) >= 4 {
			has4Byte := data[0] == 0 && data[1] == 0 && data[2] == 0 && data[3] == 1
			has3Byte := data[0] == 0 && data[1] == 0 && data[2] == 1
			expected := has4Byte || has3Byte
			if result != expected {
				t.Errorf("isAnnexBStartCode(%v) = %v, expected %v", data[:4], result, expected)
			}
		}
	})
}

// FuzzIsVP9Frame tests VP9 frame detection
func FuzzIsVP9Frame(f *testing.F) {
	seeds := [][]byte{
		{0x80, 0x00, 0x00}, // frame_marker = 0b10 (need >= 3 bytes)
		{0x82, 0x00, 0x00},
		{0xA0, 0x00, 0x00},
		{0xBF, 0x00, 0x00},
		{0x00, 0x00, 0x00}, // frame_marker = 0b00
		{0x40, 0x00, 0x00}, // frame_marker = 0b01
		{0xC0, 0x00, 0x00}, // frame_marker = 0b11
		{},
		{0x80},       // too short
		{0x80, 0x00}, // too short
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		result := isVP9Frame(data)

		// The function requires len >= 3
		if len(data) < 3 && result {
			t.Error("isVP9Frame should return false for data < 3 bytes")
		}

		// Verify against manual check for data >= 3 bytes
		if len(data) >= 3 {
			frameMarker := (data[0] >> 6) & 0x03
			expected := frameMarker == 2
			if result != expected {
				t.Errorf("isVP9Frame([0x%02X...]) = %v, expected %v (marker=%d)",
					data[0], result, expected, frameMarker)
			}
		}
	})
}

// FuzzIsAV1OBU tests AV1 OBU detection
func FuzzIsAV1OBU(f *testing.F) {
	seeds := [][]byte{
		{0x0A, 0x00},             // Sequence header, forbidden=0 (need >= 2 bytes)
		{0x12, 0x00},             // Temporal delimiter
		{0x22, 0x00},             // Tile list
		{0x32, 0x00},             // Frame header
		{0x8A, 0x00},             // forbidden=1 (invalid)
		{0x00, 0x00},             // type=0 (invalid)
		{0x00, 0x00, 0x00, 0x00}, // longer data
		{},
		{0x0A}, // too short
		{0xFF, 0xFF},
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		result := isAV1OBU(data)

		// The function requires len >= 2
		if len(data) < 2 && result {
			t.Error("isAV1OBU should return false for data < 2 bytes")
		}

		// Verify against manual check for data >= 2 bytes
		if len(data) >= 2 {
			forbidden := (data[0] >> 7) & 0x01
			obuType := (data[0] >> 3) & 0x0F
			validType := (obuType >= 1 && obuType <= 8) || obuType == 15
			expected := forbidden == 0 && validType
			if result != expected {
				t.Errorf("isAV1OBU([0x%02X...]) = %v, expected %v (forbidden=%d, type=%d)",
					data[0], result, expected, forbidden, obuType)
			}
		}
	})
}

// FuzzGetNALType tests NAL type extraction (should never panic)
func FuzzGetNALType(f *testing.F) {
	seeds := [][]byte{
		{0x00, 0x00, 0x00, 0x01, 0x67}, // SPS
		{0x00, 0x00, 0x00, 0x01, 0x68}, // PPS
		{0x00, 0x00, 0x00, 0x01, 0x65}, // IDR
		{0x00, 0x00, 0x01, 0x67, 0x00}, // 3-byte
		{0x00, 0x00, 0x01, 0x41, 0x00}, // slice
		{},
		{0x00},
		{0x00, 0x00},
		{0x00, 0x00, 0x00},
		{0xFF, 0xFF, 0xFF, 0xFF},
		{0x00, 0x00, 0x00, 0x01}, // start code only
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		// The function should never panic (this was a bug we fixed!)
		result := getNALType(data)

		// For short data, should return 0
		if len(data) < 4 && result != 0 {
			t.Error("getNALType should return 0 for data < 4 bytes")
		}
	})
}

// FuzzIsVP8Keyframe tests VP8 keyframe detection
func FuzzIsVP8Keyframe(f *testing.F) {
	seeds := [][]byte{
		{0x00, 0x00, 0x00, 0x9D, 0x01, 0x2A, 0x00, 0x00, 0x00, 0x00}, // Valid keyframe
		{0x10, 0x00, 0x00, 0x9D, 0x01, 0x2A, 0x00, 0x00, 0x00, 0x00}, // Also valid (version bits)
		{0x01, 0x00, 0x00, 0x9D, 0x01, 0x2A, 0x00, 0x00, 0x00, 0x00}, // Not keyframe (bit 0 set)
		{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, // Wrong start code
		{},
		{0x00},
		{0x00, 0x00, 0x00, 0x9D, 0x01}, // Too short after start code check
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		// Should never panic
		result := isVP8Keyframe(data)

		// The function requires len >= 10
		if len(data) < 10 && result {
			t.Error("isVP8Keyframe should return false for data < 10 bytes")
		}
	})
}
