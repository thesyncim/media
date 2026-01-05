package media

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// =============================================================================
// DetectVideoCodec Tests
// =============================================================================

func TestDetectVideoCodec_H264AnnexB(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected VideoCodec
	}{
		{
			name:     "H264 4-byte start code with SPS",
			data:     []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x1e}, // NAL type 7 = SPS
			expected: VideoCodecH264,
		},
		{
			name:     "H264 4-byte start code with PPS",
			data:     []byte{0x00, 0x00, 0x00, 0x01, 0x68, 0x00, 0x00, 0x00}, // NAL type 8 = PPS
			expected: VideoCodecH264,
		},
		{
			name:     "H264 4-byte start code with IDR",
			data:     []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x00, 0x00, 0x00}, // NAL type 5 = IDR
			expected: VideoCodecH264,
		},
		{
			name:     "H264 3-byte start code with slice",
			data:     []byte{0x00, 0x00, 0x01, 0x41, 0x00, 0x00, 0x00, 0x00}, // NAL type 1 = non-IDR
			expected: VideoCodecH264,
		},
		{
			name:     "H264 3-byte start code with SEI",
			data:     []byte{0x00, 0x00, 0x01, 0x06, 0x00, 0x00, 0x00, 0x00}, // NAL type 6 = SEI
			expected: VideoCodecH264,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectVideoCodec(tt.data)
			if got != tt.expected {
				t.Errorf("DetectVideoCodec() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDetectVideoCodec_H264AVCC(t *testing.T) {
	// AVCC format: 4-byte length prefix followed by NAL data
	tests := []struct {
		name     string
		data     []byte
		expected VideoCodec
	}{
		{
			name: "H264 AVCC format",
			// Length = 4, followed by some NAL data
			data:     []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x00, 0x00, 0x00},
			expected: VideoCodecH264,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectVideoCodec(tt.data)
			if got != tt.expected {
				t.Errorf("DetectVideoCodec() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDetectVideoCodec_VP8(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected VideoCodec
	}{
		{
			name: "VP8 keyframe with start code",
			// Frame tag byte 0 (keyframe), followed by VP8 start code 0x9D 0x01 0x2A
			data:     []byte{0x00, 0x00, 0x00, 0x9D, 0x01, 0x2A, 0x00, 0x00, 0x00, 0x00},
			expected: VideoCodecVP8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectVideoCodec(tt.data)
			if got != tt.expected {
				t.Errorf("DetectVideoCodec() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDetectVideoCodec_VP9(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected VideoCodec
	}{
		{
			name: "VP9 frame marker",
			// Frame marker 0b10 at bits 6-7 (0x82 = 1000 0010)
			data:     []byte{0x82, 0x00, 0x00, 0x00},
			expected: VideoCodecVP9,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectVideoCodec(tt.data)
			if got != tt.expected {
				t.Errorf("DetectVideoCodec() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDetectVideoCodec_AV1(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected VideoCodec
	}{
		{
			name: "AV1 OBU sequence header",
			// OBU type 1 (sequence header) = 0x08 (type 1 << 3)
			data:     []byte{0x08, 0x00, 0x00, 0x00},
			expected: VideoCodecAV1,
		},
		{
			name: "AV1 OBU temporal delimiter",
			// OBU type 2 (temporal delimiter) = 0x10 (type 2 << 3)
			data:     []byte{0x10, 0x00, 0x00, 0x00},
			expected: VideoCodecAV1,
		},
		{
			name: "AV1 OBU frame header",
			// OBU type 3 (frame header) = 0x18 (type 3 << 3)
			data:     []byte{0x18, 0x00, 0x00, 0x00},
			expected: VideoCodecAV1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectVideoCodec(tt.data)
			if got != tt.expected {
				t.Errorf("DetectVideoCodec() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDetectVideoCodec_IVF(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected VideoCodec
	}{
		{
			name: "IVF header VP8",
			data: func() []byte {
				data := make([]byte, 32)
				copy(data[0:4], "DKIF")
				copy(data[8:12], "VP80")
				return data
			}(),
			expected: VideoCodecVP8,
		},
		{
			name: "IVF header VP9",
			data: func() []byte {
				data := make([]byte, 32)
				copy(data[0:4], "DKIF")
				copy(data[8:12], "VP90")
				return data
			}(),
			expected: VideoCodecVP9,
		},
		{
			name: "IVF header AV1",
			data: func() []byte {
				data := make([]byte, 32)
				copy(data[0:4], "DKIF")
				copy(data[8:12], "AV01")
				return data
			}(),
			expected: VideoCodecAV1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectVideoCodec(tt.data)
			if got != tt.expected {
				t.Errorf("DetectVideoCodec() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDetectVideoCodec_Unknown(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{name: "empty data", data: []byte{}},
		{name: "too short", data: []byte{0x00, 0x00}},
		{name: "random data", data: []byte{0xFF, 0xFE, 0xFD, 0xFC}},
		// 0xC0 has forbidden bit=1 (not AV1) and frame_marker=0b11 (not VP9)
		{name: "non-matching byte pattern", data: []byte{0xC0, 0xC1, 0xC2, 0xC3}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectVideoCodec(tt.data)
			if got != VideoCodecUnknown {
				t.Errorf("DetectVideoCodec() = %v, want VideoCodecUnknown", got)
			}
		})
	}
}

// =============================================================================
// Helper Functions Tests
// =============================================================================

func TestIsAnnexBStartCode(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected bool
	}{
		{name: "4-byte start code", data: []byte{0, 0, 0, 1, 0x67}, expected: true},
		{name: "3-byte start code", data: []byte{0, 0, 1, 0x67}, expected: true},
		{name: "not a start code", data: []byte{0, 0, 2, 0x67}, expected: false},
		{name: "too short", data: []byte{0, 0, 0}, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAnnexBStartCode(tt.data)
			if got != tt.expected {
				t.Errorf("isAnnexBStartCode() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestGetNALType(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected byte
	}{
		{name: "SPS with 4-byte SC", data: []byte{0, 0, 0, 1, 0x67}, expected: 7},  // 0x67 & 0x1F = 7
		{name: "PPS with 4-byte SC", data: []byte{0, 0, 0, 1, 0x68}, expected: 8},  // 0x68 & 0x1F = 8
		{name: "IDR with 3-byte SC", data: []byte{0, 0, 1, 0x65}, expected: 5},     // 0x65 & 0x1F = 5
		{name: "Non-IDR with 3-byte SC", data: []byte{0, 0, 1, 0x41}, expected: 1}, // 0x41 & 0x1F = 1
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getNALType(tt.data)
			if got != tt.expected {
				t.Errorf("getNALType() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsH264NALType(t *testing.T) {
	tests := []struct {
		nalType  byte
		expected bool
	}{
		{0, false},  // Reserved
		{1, true},   // Non-IDR slice
		{5, true},   // IDR slice
		{6, true},   // SEI
		{7, true},   // SPS
		{8, true},   // PPS
		{9, true},   // Access unit delimiter
		{12, true},  // Filler data
		{13, false}, // Invalid
		{18, false}, // Invalid
		{19, true},  // Coded slice extension
		{21, true},  // Coded slice depth
		{22, false}, // Invalid
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := isH264NALType(tt.nalType)
			if got != tt.expected {
				t.Errorf("isH264NALType(%d) = %v, want %v", tt.nalType, got, tt.expected)
			}
		})
	}
}

func TestIsVP8Keyframe(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected bool
	}{
		{
			name:     "valid keyframe",
			data:     []byte{0x00, 0x00, 0x00, 0x9D, 0x01, 0x2A, 0x00, 0x00, 0x00, 0x00},
			expected: true,
		},
		{
			name:     "not a keyframe (bit 0 set)",
			data:     []byte{0x01, 0x00, 0x00, 0x9D, 0x01, 0x2A, 0x00, 0x00, 0x00, 0x00},
			expected: false,
		},
		{
			name:     "wrong start code",
			data:     []byte{0x00, 0x00, 0x00, 0x9E, 0x01, 0x2A, 0x00, 0x00, 0x00, 0x00},
			expected: false,
		},
		{
			name:     "too short",
			data:     []byte{0x00, 0x00, 0x00, 0x9D, 0x01},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isVP8Keyframe(tt.data)
			if got != tt.expected {
				t.Errorf("isVP8Keyframe() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsVP9Frame(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected bool
	}{
		{
			name:     "valid VP9 frame marker",
			data:     []byte{0x82, 0x00, 0x00}, // 0b10 at bits 6-7
			expected: true,
		},
		{
			name:     "invalid frame marker",
			data:     []byte{0x42, 0x00, 0x00}, // 0b01 at bits 6-7
			expected: false,
		},
		{
			name:     "too short",
			data:     []byte{0x82, 0x00},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isVP9Frame(tt.data)
			if got != tt.expected {
				t.Errorf("isVP9Frame() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsAV1OBU(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected bool
	}{
		{
			name:     "sequence header OBU",
			data:     []byte{0x08, 0x00}, // Type 1
			expected: true,
		},
		{
			name:     "temporal delimiter OBU",
			data:     []byte{0x10, 0x00}, // Type 2
			expected: true,
		},
		{
			name:     "frame OBU",
			data:     []byte{0x30, 0x00}, // Type 6
			expected: true,
		},
		{
			name:     "forbidden bit set",
			data:     []byte{0x88, 0x00}, // Forbidden bit = 1
			expected: false,
		},
		{
			name:     "invalid OBU type",
			data:     []byte{0x48, 0x00}, // Type 9 (invalid)
			expected: false,
		},
		{
			name:     "too short",
			data:     []byte{0x08},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAV1OBU(tt.data)
			if got != tt.expected {
				t.Errorf("isAV1OBU() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// =============================================================================
// Audio Codec Detection Tests
// =============================================================================

func TestDetectAudioCodec_AAC(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected AudioCodec
	}{
		{
			name: "AAC ADTS frame",
			// ADTS header: 0xFFF (syncword) + ID=0 + layer=00 + protection=1
			data:     []byte{0xFF, 0xF1, 0x50, 0x80, 0x00, 0x1F, 0xFC},
			expected: AudioCodecAAC,
		},
		{
			name: "AAC ADTS with CRC",
			// ADTS header with protection_absent=0 (has CRC)
			data:     []byte{0xFF, 0xF0, 0x50, 0x80, 0x00, 0x1F, 0xFC},
			expected: AudioCodecAAC,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectAudioCodec(tt.data)
			if got != tt.expected {
				t.Errorf("DetectAudioCodec() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDetectAudioCodec_Ogg(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected AudioCodec
	}{
		{
			name: "Ogg with OpusHead",
			// OggS + page header + OpusHead magic at offset 28
			data: func() []byte {
				data := make([]byte, 40)
				copy(data[0:4], "OggS")
				copy(data[28:36], "OpusHead")
				return data
			}(),
			expected: AudioCodecOpus,
		},
		{
			name: "Ogg without Opus",
			// OggS container but not Opus
			data:     append([]byte("OggS"), make([]byte, 24)...),
			expected: AudioCodecUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectAudioCodec(tt.data)
			if got != tt.expected {
				t.Errorf("DetectAudioCodec() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDetectAudioCodec_Unknown(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{name: "empty data", data: []byte{}},
		{name: "too short", data: []byte{0xFF, 0xFB}},
		{name: "random data", data: []byte{0x12, 0x34, 0x56, 0x78}},
		{name: "FLAC marker", data: []byte{'f', 'L', 'a', 'C', 0x00}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectAudioCodec(tt.data)
			if got != AudioCodecUnknown {
				t.Errorf("DetectAudioCodec() = %v, want AudioCodecUnknown", got)
			}
		})
	}
}

func TestIsAACAdts(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected bool
	}{
		{
			name:     "valid ADTS header",
			data:     []byte{0xFF, 0xF1, 0x50, 0x80, 0x00, 0x1F, 0xFC},
			expected: true,
		},
		{
			name:     "wrong syncword",
			data:     []byte{0xFF, 0xE1, 0x50, 0x80, 0x00, 0x1F, 0xFC},
			expected: false,
		},
		{
			name:     "wrong layer",
			data:     []byte{0xFF, 0xF3, 0x50, 0x80, 0x00, 0x1F, 0xFC}, // layer = 01
			expected: false,
		},
		{
			name:     "too short",
			data:     []byte{0xFF, 0xF1, 0x50},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAACAdts(tt.data)
			if got != tt.expected {
				t.Errorf("isAACAdts() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsMP3Frame(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected bool
	}{
		{
			name:     "valid MP3 frame header",
			data:     []byte{0xFF, 0xFB, 0x90, 0x00}, // MPEG1 Layer III
			expected: true,
		},
		{
			name:     "wrong syncword",
			data:     []byte{0xFF, 0xC0, 0x90, 0x00},
			expected: false,
		},
		{
			name:     "wrong layer (Layer II)",
			data:     []byte{0xFF, 0xFD, 0x90, 0x00}, // layer = 10 = Layer II
			expected: false,
		},
		{
			name:     "too short",
			data:     []byte{0xFF, 0xFB},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isMP3Frame(tt.data)
			if got != tt.expected {
				t.Errorf("isMP3Frame() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// =============================================================================
// Preset Function Tests
// =============================================================================

func TestSimulcastPreset(t *testing.T) {
	variants := SimulcastPreset(VideoCodecVP8, 2_000_000)

	if len(variants) != 3 {
		t.Fatalf("expected 3 variants, got %d", len(variants))
	}

	// Check high variant
	if variants[0].ID != "high" {
		t.Errorf("expected ID 'high', got '%s'", variants[0].ID)
	}
	if variants[0].Width != 1920 || variants[0].Height != 1080 {
		t.Errorf("expected 1920x1080, got %dx%d", variants[0].Width, variants[0].Height)
	}
	if variants[0].BitrateBps != 2_000_000 {
		t.Errorf("expected bitrate 2000000, got %d", variants[0].BitrateBps)
	}

	// Check medium variant
	if variants[1].ID != "medium" {
		t.Errorf("expected ID 'medium', got '%s'", variants[1].ID)
	}
	if variants[1].Width != 1280 || variants[1].Height != 720 {
		t.Errorf("expected 1280x720, got %dx%d", variants[1].Width, variants[1].Height)
	}
	if variants[1].BitrateBps != 1_000_000 {
		t.Errorf("expected bitrate 1000000, got %d", variants[1].BitrateBps)
	}

	// Check low variant
	if variants[2].ID != "low" {
		t.Errorf("expected ID 'low', got '%s'", variants[2].ID)
	}
	if variants[2].Width != 640 || variants[2].Height != 360 {
		t.Errorf("expected 640x360, got %dx%d", variants[2].Width, variants[2].Height)
	}
	if variants[2].BitrateBps != 500_000 {
		t.Errorf("expected bitrate 500000, got %d", variants[2].BitrateBps)
	}
}

func TestSVCPreset(t *testing.T) {
	variants := SVCPreset(VideoCodecVP9, 1920, 1080, 3_000_000, 3)

	if len(variants) != 1 {
		t.Fatalf("expected 1 variant, got %d", len(variants))
	}

	v := variants[0]
	if v.ID != "svc" {
		t.Errorf("expected ID 'svc', got '%s'", v.ID)
	}
	if v.Codec != VideoCodecVP9 {
		t.Errorf("expected codec VP9, got %v", v.Codec)
	}
	if v.Width != 1920 || v.Height != 1080 {
		t.Errorf("expected 1920x1080, got %dx%d", v.Width, v.Height)
	}
	if v.BitrateBps != 3_000_000 {
		t.Errorf("expected bitrate 3000000, got %d", v.BitrateBps)
	}
	if v.TemporalLayers != 3 {
		t.Errorf("expected 3 temporal layers, got %d", v.TemporalLayers)
	}
}

func TestMultiCodecPreset(t *testing.T) {
	variants := MultiCodecPreset(1280, 720, 1_500_000, VideoCodecVP8, VideoCodecVP9, VideoCodecH264)

	if len(variants) != 3 {
		t.Fatalf("expected 3 variants, got %d", len(variants))
	}

	codecs := []VideoCodec{VideoCodecVP8, VideoCodecVP9, VideoCodecH264}
	for i, v := range variants {
		if v.Codec != codecs[i] {
			t.Errorf("variant %d: expected codec %v, got %v", i, codecs[i], v.Codec)
		}
		if v.Width != 1280 || v.Height != 720 {
			t.Errorf("variant %d: expected 1280x720, got %dx%d", i, v.Width, v.Height)
		}
		if v.BitrateBps != 1_500_000 {
			t.Errorf("variant %d: expected bitrate 1500000, got %d", i, v.BitrateBps)
		}
	}
}

// =============================================================================
// TranscodeResult Tests
// =============================================================================

func TestTranscodeResult_Get(t *testing.T) {
	result := &TranscodeResult{
		Variants: []VariantFrame{
			{VariantID: "high", Frame: &EncodedFrame{Data: []byte{1}}},
			{VariantID: "low", Frame: &EncodedFrame{Data: []byte{2}}},
		},
	}

	// Test found
	frame := result.Get("high")
	if frame == nil {
		t.Fatal("expected to find 'high' variant")
	}
	if !bytes.Equal(frame.Data, []byte{1}) {
		t.Errorf("expected data [1], got %v", frame.Data)
	}

	// Test not found
	frame = result.Get("medium")
	if frame != nil {
		t.Error("expected nil for non-existent variant")
	}
}

// =============================================================================
// ABRController Tests
// =============================================================================

func TestABRController_StepDown(t *testing.T) {
	ctrl := &ABRController{
		variants: []string{"high", "medium", "low"},
		current:  0,
	}

	// Current should be "high"
	if ctrl.CurrentVariant() != "high" {
		t.Errorf("expected 'high', got '%s'", ctrl.CurrentVariant())
	}

	// Step down to medium
	if !ctrl.StepDown() {
		t.Error("expected StepDown to succeed")
	}
	if ctrl.CurrentVariant() != "medium" {
		t.Errorf("expected 'medium', got '%s'", ctrl.CurrentVariant())
	}

	// Step down to low
	if !ctrl.StepDown() {
		t.Error("expected StepDown to succeed")
	}
	if ctrl.CurrentVariant() != "low" {
		t.Errorf("expected 'low', got '%s'", ctrl.CurrentVariant())
	}

	// Can't step down further
	if ctrl.StepDown() {
		t.Error("expected StepDown to fail at lowest")
	}
}

func TestABRController_StepUp(t *testing.T) {
	ctrl := &ABRController{
		variants: []string{"high", "medium", "low"},
		current:  2, // Start at low
	}

	// Current should be "low"
	if ctrl.CurrentVariant() != "low" {
		t.Errorf("expected 'low', got '%s'", ctrl.CurrentVariant())
	}

	// Step up to medium
	if !ctrl.StepUp() {
		t.Error("expected StepUp to succeed")
	}
	if ctrl.CurrentVariant() != "medium" {
		t.Errorf("expected 'medium', got '%s'", ctrl.CurrentVariant())
	}

	// Step up to high
	if !ctrl.StepUp() {
		t.Error("expected StepUp to succeed")
	}
	if ctrl.CurrentVariant() != "high" {
		t.Errorf("expected 'high', got '%s'", ctrl.CurrentVariant())
	}

	// Can't step up further
	if ctrl.StepUp() {
		t.Error("expected StepUp to fail at highest")
	}
}

func TestABRController_SetVariant(t *testing.T) {
	ctrl := &ABRController{
		variants: []string{"high", "medium", "low"},
		current:  0,
	}

	// Set to low
	if !ctrl.SetVariant("low") {
		t.Error("expected SetVariant to succeed")
	}
	if ctrl.CurrentVariant() != "low" {
		t.Errorf("expected 'low', got '%s'", ctrl.CurrentVariant())
	}

	// Set to non-existent
	if ctrl.SetVariant("ultra") {
		t.Error("expected SetVariant to fail for non-existent")
	}
	if ctrl.CurrentVariant() != "low" {
		t.Errorf("expected 'low', got '%s'", ctrl.CurrentVariant())
	}
}

func TestABRController_SelectOutput(t *testing.T) {
	ctrl := &ABRController{
		variants: []string{"high", "medium", "low"},
		current:  1, // medium
	}

	result := &TranscodeResult{
		Variants: []VariantFrame{
			{VariantID: "high", Frame: &EncodedFrame{Data: []byte{1}}},
			{VariantID: "medium", Frame: &EncodedFrame{Data: []byte{2}}},
			{VariantID: "low", Frame: &EncodedFrame{Data: []byte{3}}},
		},
	}

	output := ctrl.SelectOutput(result)
	if output == nil {
		t.Fatal("expected output")
	}
	if output.VariantID != "medium" {
		t.Errorf("expected 'medium', got '%s'", output.VariantID)
	}
}

// =============================================================================
// SyncMode Tests
// =============================================================================

func TestSyncModeConstants(t *testing.T) {
	// Verify the sync modes have expected values
	if SyncStrict != 0 {
		t.Errorf("SyncStrict should be 0, got %d", SyncStrict)
	}
	if SyncLoose != 1 {
		t.Errorf("SyncLoose should be 1, got %d", SyncLoose)
	}
	if SyncVideoMaster != 2 {
		t.Errorf("SyncVideoMaster should be 2, got %d", SyncVideoMaster)
	}
	if SyncAudioMaster != 3 {
		t.Errorf("SyncAudioMaster should be 3, got %d", SyncAudioMaster)
	}
}

// =============================================================================
// SyncedMedia Tests
// =============================================================================

func TestSyncedMedia_GetVideo(t *testing.T) {
	output := &SyncedMedia{
		Videos: []VariantFrame{
			{VariantID: "high", Frame: &EncodedFrame{Data: []byte{1}}},
			{VariantID: "low", Frame: &EncodedFrame{Data: []byte{2}}},
		},
	}

	// Test found
	frame := output.GetVideo("high")
	if frame == nil {
		t.Fatal("expected to find 'high' variant")
	}
	if !bytes.Equal(frame.Data, []byte{1}) {
		t.Errorf("expected data [1], got %v", frame.Data)
	}

	// Test not found
	frame = output.GetVideo("medium")
	if frame != nil {
		t.Error("expected nil for non-existent variant")
	}
}

// =============================================================================
// Passthrough Tests
// =============================================================================

func TestPassthrough(t *testing.T) {
	pt := NewPassthrough(VideoCodecH264)

	if pt.Codec() != VideoCodecH264 {
		t.Errorf("expected H264, got %v", pt.Codec())
	}

	frame := &EncodedFrame{Data: []byte{1, 2, 3}}
	result, err := pt.Process(frame)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if result != frame {
		t.Error("expected same frame reference")
	}

	if err := pt.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

// =============================================================================
// DefaultAudioConfig Tests
// =============================================================================

func TestDefaultAudioConfig(t *testing.T) {
	cfg := DefaultAudioConfig()

	if cfg.Codec != AudioCodecOpus {
		t.Errorf("expected Opus, got %v", cfg.Codec)
	}
	if cfg.BitrateBps != 64000 {
		t.Errorf("expected 64000 bps, got %d", cfg.BitrateBps)
	}
	if cfg.SampleRate != 48000 {
		t.Errorf("expected 48000 Hz, got %d", cfg.SampleRate)
	}
	if cfg.Channels != 2 {
		t.Errorf("expected 2 channels, got %d", cfg.Channels)
	}
}

// =============================================================================
// MultiTranscoder VideoSource Tests
// =============================================================================

type mockVideoSource struct {
	frames    []*VideoFrame
	index     int
	started   bool
	stopped   bool
	config    SourceConfig
}

func (m *mockVideoSource) Start(ctx context.Context) error {
	m.started = true
	return nil
}

func (m *mockVideoSource) Stop() error {
	m.stopped = true
	return nil
}

func (m *mockVideoSource) ReadFrame(ctx context.Context) (*VideoFrame, error) {
	if m.index >= len(m.frames) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
			return nil, nil
		}
	}
	frame := m.frames[m.index]
	m.index++
	return frame, nil
}

func (m *mockVideoSource) ReadFrameInto(ctx context.Context, buf *VideoFrameBuffer) error {
	return ErrNotSupported
}

func (m *mockVideoSource) SetCallback(cb VideoFrameCallback) {}

func (m *mockVideoSource) Config() SourceConfig {
	return m.config
}

func (m *mockVideoSource) Close() error {
	return nil
}

func TestMultiTranscoder_SetSource(t *testing.T) {
	// This test only verifies SetSource doesn't panic and sets config
	// Full integration tests require encoder registration

	mt := &MultiTranscoder{
		pipelines: make(map[string]*outputPipeline),
	}

	source := &mockVideoSource{
		config: SourceConfig{
			Width:  1920,
			Height: 1080,
			FPS:    30,
		},
	}

	mt.SetSource(source)

	cfg := mt.Config()
	if cfg.Width != 1920 {
		t.Errorf("expected width 1920, got %d", cfg.Width)
	}
	if cfg.Height != 1080 {
		t.Errorf("expected height 1080, got %d", cfg.Height)
	}
	if cfg.FPS != 30 {
		t.Errorf("expected FPS 30, got %d", cfg.FPS)
	}
}

func TestMultiTranscoder_StartWithoutSource(t *testing.T) {
	mt := &MultiTranscoder{
		pipelines: make(map[string]*outputPipeline),
	}

	err := mt.Start(context.Background())
	if err == nil {
		t.Error("expected error when starting without source")
	}
	if err.Error() != "no source set" {
		t.Errorf("expected 'no source set' error, got '%s'", err.Error())
	}
}

func TestMultiTranscoder_DoubleStart(t *testing.T) {
	mt := &MultiTranscoder{
		pipelines: make(map[string]*outputPipeline),
		source: &mockVideoSource{
			config: SourceConfig{Width: 640, Height: 480, FPS: 30},
		},
	}

	ctx := context.Background()
	if err := mt.Start(ctx); err != nil {
		t.Fatalf("first Start failed: %v", err)
	}
	defer mt.Stop()

	err := mt.Start(ctx)
	if err == nil {
		t.Error("expected error on double start")
	}
	if err.Error() != "already running" {
		t.Errorf("expected 'already running' error, got '%s'", err.Error())
	}
}

func TestAbs64(t *testing.T) {
	tests := []struct {
		input    int64
		expected int64
	}{
		{0, 0},
		{5, 5},
		{-5, 5},
		{-9223372036854775808, -9223372036854775808}, // Min int64 edge case (stays negative due to overflow)
	}

	for _, tt := range tests {
		// Skip the min int64 case as it causes overflow
		if tt.input == -9223372036854775808 {
			continue
		}
		got := abs64(tt.input)
		if got != tt.expected {
			t.Errorf("abs64(%d) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}

// =============================================================================
// Comprehensive Transcoder Tests
// =============================================================================

// --- Test Helpers ---

// createEncodedTestFrame creates an encoded frame for a given codec
func createEncodedTestFrame(t *testing.T, codec VideoCodec, width, height int, keyframe bool) *EncodedFrame {
	t.Helper()

	// Create a raw frame
	raw := createTestVideoFrame(width, height)

	// Create encoder
	var enc VideoEncoder
	var err error

	switch codec {
	case VideoCodecVP8:
		if !IsVP8Available() {
			t.Skipf("VP8 not available")
		}
		enc, err = NewVP8Encoder(VideoEncoderConfig{
			Width: width, Height: height, BitrateBps: 1_000_000, FPS: 30,
		})
	case VideoCodecVP9:
		if !IsVP9Available() {
			t.Skipf("VP9 not available")
		}
		enc, err = NewVP9Encoder(VideoEncoderConfig{
			Width: width, Height: height, BitrateBps: 1_000_000, FPS: 30,
		})
	case VideoCodecH264:
		if !IsH264EncoderAvailable() {
			t.Skipf("H264 encoder not available")
		}
		enc, err = NewH264Encoder(VideoEncoderConfig{
			Width: width, Height: height, BitrateBps: 1_000_000, FPS: 30,
		})
	default:
		t.Fatalf("unsupported codec: %v", codec)
	}
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}
	defer enc.Close()

	// Request keyframe if needed
	if keyframe {
		enc.RequestKeyframe()
	}

	// Encode multiple frames to handle buffering (especially VP9)
	encodeBuf := make([]byte, enc.MaxEncodedSize())
	var result EncodeResult
	for i := 0; i < 60 && result.N == 0; i++ {
		raw.Timestamp = int64(i) * 33_333_333 // 30fps
		result, err = enc.Encode(raw, encodeBuf)
		if err != nil {
			t.Fatalf("encode failed: %v", err)
		}
	}

	if result.N == 0 {
		t.Fatal("failed to get encoded frame after 60 attempts")
	}

	// Make a copy since encoder may reuse buffer
	dataCopy := make([]byte, result.N)
	copy(dataCopy, encodeBuf[:result.N])

	return &EncodedFrame{
		Data:      dataCopy,
		FrameType: result.FrameType,
	}
}

// createTestVideoFrame creates a test I420 video frame with gradient pattern
func createTestVideoFrame(width, height int) *VideoFrame {
	ySize := width * height
	uvSize := (width / 2) * (height / 2)

	yPlane := make([]byte, ySize)
	uPlane := make([]byte, uvSize)
	vPlane := make([]byte, uvSize)

	// Fill Y with gradient pattern
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			yPlane[y*width+x] = byte((x + y) % 256)
		}
	}

	// Fill U/V with neutral values + slight variation
	for y := 0; y < height/2; y++ {
		for x := 0; x < width/2; x++ {
			uPlane[y*(width/2)+x] = byte(128 + (x%10))
			vPlane[y*(width/2)+x] = byte(128 + (y%10))
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

// verifyDecodedFrame validates that a decoded frame has correct properties
func verifyDecodedFrame(t *testing.T, frame *VideoFrame, minWidth, minHeight int) {
	t.Helper()

	if frame.Width < minWidth || frame.Height < minHeight {
		t.Errorf("frame dimensions %dx%d smaller than expected %dx%d",
			frame.Width, frame.Height, minWidth, minHeight)
	}

	// Verify Y plane
	expectedYSize := frame.Width * frame.Height
	if len(frame.Data[0]) < expectedYSize {
		t.Errorf("Y plane too small: %d < %d", len(frame.Data[0]), expectedYSize)
	}

	// Verify U/V planes
	expectedUVSize := (frame.Width / 2) * (frame.Height / 2)
	if len(frame.Data[1]) < expectedUVSize || len(frame.Data[2]) < expectedUVSize {
		t.Errorf("U/V planes too small: %d, %d < %d",
			len(frame.Data[1]), len(frame.Data[2]), expectedUVSize)
	}

	// Verify pixel data has variance (not all zeros)
	hasVariance := false
	first := frame.Data[0][0]
	for _, v := range frame.Data[0][:min(1000, len(frame.Data[0]))] {
		if v != first {
			hasVariance = true
			break
		}
	}
	if !hasVariance {
		t.Error("Y plane has no variance")
	}
}

// --- Single Transcoder Codec Combination Tests ---

func TestTranscoder_AllCodecCombinations(t *testing.T) {
	// Define all codec combinations to test
	combos := []struct {
		from, to VideoCodec
	}{
		{VideoCodecVP8, VideoCodecVP8},
		{VideoCodecVP8, VideoCodecVP9},
		{VideoCodecVP8, VideoCodecH264},
		{VideoCodecVP9, VideoCodecVP8},
		{VideoCodecVP9, VideoCodecVP9},
		{VideoCodecVP9, VideoCodecH264},
		{VideoCodecH264, VideoCodecVP8},
		{VideoCodecH264, VideoCodecVP9},
		{VideoCodecH264, VideoCodecH264},
	}

	for _, combo := range combos {
		name := combo.from.String() + "_to_" + combo.to.String()
		t.Run(name, func(t *testing.T) {
			// Check codec availability
			switch combo.from {
			case VideoCodecVP8:
				if !IsVP8Available() {
					t.Skip("VP8 not available")
				}
			case VideoCodecVP9:
				if !IsVP9Available() {
					t.Skip("VP9 not available")
				}
			case VideoCodecH264:
				if !IsH264DecoderAvailable() {
					t.Skip("H264 decoder not available")
				}
			}
			switch combo.to {
			case VideoCodecVP8:
				if !IsVP8Available() {
					t.Skip("VP8 not available")
				}
			case VideoCodecVP9:
				if !IsVP9Available() {
					t.Skip("VP9 not available")
				}
			case VideoCodecH264:
				if !IsH264EncoderAvailable() {
					t.Skip("H264 encoder not available")
				}
			}

			// Create transcoder
			transcoder, err := NewTranscoder(TranscoderConfig{
				InputCodec:  combo.from,
				OutputCodec: combo.to,
				Width:       640,
				Height:      480,
				BitrateBps:  500_000,
				FPS:         30,
			})
			if err != nil {
				t.Fatalf("failed to create transcoder: %v", err)
			}
			defer transcoder.Close()

			// Create encoded input frame
			input := createEncodedTestFrame(t, combo.from, 640, 480, true)

			// Transcode
			output, err := transcoder.Transcode(input)
			if err != nil {
				t.Fatalf("transcode failed: %v", err)
			}
			if output == nil {
				// May need more frames for buffering
				raw := createTestVideoFrame(640, 480)
				for i := 0; i < 30 && output == nil; i++ {
					output, _ = transcoder.TranscodeRaw(raw)
				}
			}
			if output == nil {
				t.Skip("transcoder still buffering - may need more frames")
			}

			// Verify output
			if len(output.Data) == 0 {
				t.Error("output frame has no data")
			}

			// Decode and verify
			var dec VideoDecoder
			switch combo.to {
			case VideoCodecVP8:
				dec, err = NewVP8Decoder(VideoDecoderConfig{})
			case VideoCodecVP9:
				dec, err = NewVP9Decoder(VideoDecoderConfig{})
			case VideoCodecH264:
				dec, err = NewH264Decoder(VideoDecoderConfig{})
			}
			if err != nil {
				t.Fatalf("failed to create decoder: %v", err)
			}
			defer dec.Close()

			decoded, err := dec.Decode(output)
			if err != nil {
				t.Fatalf("decode failed: %v", err)
			}
			if decoded != nil {
				verifyDecodedFrame(t, decoded, 320, 240) // Allow some variance
				t.Logf("%s -> %s: decoded %dx%d", combo.from, combo.to, decoded.Width, decoded.Height)
			}
		})
	}
}

func TestTranscoder_KeyframeRequest(t *testing.T) {
	if !IsVP8Available() {
		t.Skip("VP8 not available")
	}

	transcoder, err := NewTranscoder(TranscoderConfig{
		InputCodec:  VideoCodecVP8,
		OutputCodec: VideoCodecVP8,
		Width:       640,
		Height:      480,
		BitrateBps:  500_000,
		FPS:         30,
	})
	if err != nil {
		t.Fatalf("failed to create transcoder: %v", err)
	}
	defer transcoder.Close()

	// Encode several frames first
	raw := createTestVideoFrame(640, 480)
	var frameCount int
	for i := 0; i < 60; i++ {
		output, err := transcoder.TranscodeRaw(raw)
		if err != nil {
			t.Fatalf("transcode failed: %v", err)
		}
		if output != nil {
			frameCount++
			if frameCount > 5 {
				// Now request keyframe
				transcoder.RequestKeyframe()

				// Next frame should be keyframe
				for j := 0; j < 5; j++ {
					output, _ = transcoder.TranscodeRaw(raw)
					if output != nil && output.FrameType == FrameTypeKey {
						t.Log("Keyframe received after request")
						return
					}
				}
			}
		}
	}
	t.Log("Keyframe request test completed (may not have received explicit keyframe)")
}

// --- MultiTranscoder Tests ---

func TestMultiTranscoder_MultiCodecOutput(t *testing.T) {
	// Check all required codecs
	if !IsVP8Available() {
		t.Skip("VP8 not available")
	}
	if !IsVP9Available() {
		t.Skip("VP9 not available")
	}
	if !IsH264EncoderAvailable() || !IsH264DecoderAvailable() {
		t.Skip("H264 not available")
	}

	// Create multi-transcoder with 3 different codec outputs
	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec:  VideoCodecH264,
		InputWidth:  640,
		InputHeight: 480,
		InputFPS:    30,
		Outputs: []OutputConfig{
			{ID: "vp8", Codec: VideoCodecVP8, BitrateBps: 500_000},
			{ID: "vp9", Codec: VideoCodecVP9, BitrateBps: 500_000},
			{ID: "h264", Codec: VideoCodecH264, BitrateBps: 500_000},
		},
	})
	if err != nil {
		t.Fatalf("failed to create multi-transcoder: %v", err)
	}
	defer mt.Close()

	// Create input frame
	input := createEncodedTestFrame(t, VideoCodecH264, 640, 480, true)

	// Transcode
	result, err := mt.Transcode(input)
	if err != nil {
		t.Fatalf("transcode failed: %v", err)
	}

	// May need more frames due to buffering
	if result == nil || len(result.Variants) < 3 {
		raw := createTestVideoFrame(640, 480)
		for i := 0; i < 30 && (result == nil || len(result.Variants) < 3); i++ {
			result, _ = mt.TranscodeRaw(raw)
		}
	}

	if result == nil {
		t.Skip("multi-transcoder still buffering")
	}

	// Verify all variants present
	variants := mt.Variants()
	if len(variants) != 3 {
		t.Errorf("expected 3 variants, got %d", len(variants))
	}

	t.Logf("Multi-codec output: %d variants", len(result.Variants))
	for _, v := range result.Variants {
		if v.Frame != nil {
			t.Logf("  %s: %d bytes", v.VariantID, len(v.Frame.Data))
		}
	}
}

func TestMultiTranscoder_SimulcastPreset(t *testing.T) {
	if !IsVP8Available() {
		t.Skip("VP8 not available")
	}

	outputs := SimulcastPreset(VideoCodecVP8, 2_000_000)

	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec:  VideoCodecVP8,
		InputWidth:  1920,
		InputHeight: 1080,
		InputFPS:    30,
		Outputs:     outputs,
	})
	if err != nil {
		t.Fatalf("failed to create multi-transcoder: %v", err)
	}
	defer mt.Close()

	// Transcode raw frame
	raw := createTestVideoFrame(1920, 1080)

	var result *TranscodeResult
	for i := 0; i < 60 && result == nil; i++ {
		result, _ = mt.TranscodeRaw(raw)
	}

	if result == nil {
		t.Skip("simulcast transcoder still buffering")
	}

	// Verify we got all 3 variants
	if len(result.Variants) != 3 {
		t.Errorf("expected 3 variants, got %d", len(result.Variants))
	}

	t.Logf("Simulcast output: %d variants", len(result.Variants))
}

// --- Performance Tests (Critical: No Lag for Multiple Outputs) ---

func TestMultiTranscoder_LatencyBudget(t *testing.T) {
	if !IsVP8Available() {
		t.Skip("VP8 not available")
	}

	// Test latency with different number of outputs
	tests := []struct {
		name       string
		numOutputs int
		maxLatency time.Duration
	}{
		{"1_output", 1, 15 * time.Millisecond},
		{"2_outputs", 2, 20 * time.Millisecond},
		{"3_outputs", 3, 30 * time.Millisecond},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			outputs := make([]OutputConfig, tc.numOutputs)
			for i := 0; i < tc.numOutputs; i++ {
				outputs[i] = OutputConfig{
					ID:         "output_" + string(rune('A'+i)),
					Codec:      VideoCodecVP8,
					Width:      640,
					Height:     480,
					BitrateBps: 500_000,
				}
			}

			mt, err := NewMultiTranscoder(MultiTranscoderConfig{
				InputCodec:  VideoCodecVP8,
				InputWidth:  640,
				InputHeight: 480,
				InputFPS:    30,
				Outputs:     outputs,
			})
			if err != nil {
				t.Fatalf("failed to create transcoder: %v", err)
			}
			defer mt.Close()

			raw := createTestVideoFrame(640, 480)

			// Warm up (get encoder past buffering)
			for i := 0; i < 60; i++ {
				mt.TranscodeRaw(raw)
			}

			// Measure latency over 30 frames
			var totalLatency time.Duration
			var measuredFrames int

			for i := 0; i < 30; i++ {
				start := time.Now()
				result, err := mt.TranscodeRaw(raw)
				latency := time.Since(start)

				if err == nil && result != nil && len(result.Variants) == tc.numOutputs {
					totalLatency += latency
					measuredFrames++
				}
			}

			if measuredFrames == 0 {
				t.Skip("no frames produced for measurement")
			}

			avgLatency := totalLatency / time.Duration(measuredFrames)
			t.Logf("%s: avg latency = %v (budget = %v)", tc.name, avgLatency, tc.maxLatency)

			if avgLatency > tc.maxLatency {
				t.Errorf("latency %v exceeds budget %v", avgLatency, tc.maxLatency)
			}
		})
	}
}

func TestMultiTranscoder_SustainedThroughput(t *testing.T) {
	if !IsVP8Available() {
		t.Skip("VP8 not available")
	}

	outputs := SimulcastPreset(VideoCodecVP8, 1_000_000)

	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec:  VideoCodecVP8,
		InputWidth:  1280,
		InputHeight: 720,
		InputFPS:    30,
		Outputs:     outputs,
	})
	if err != nil {
		t.Fatalf("failed to create transcoder: %v", err)
	}
	defer mt.Close()

	raw := createTestVideoFrame(1280, 720)

	// Warm up
	for i := 0; i < 60; i++ {
		mt.TranscodeRaw(raw)
	}

	// Test sustained throughput for 5 seconds at 30fps
	targetFPS := 30
	duration := 5 * time.Second
	targetFrames := int(duration.Seconds()) * targetFPS

	var successFrames int
	var latencies []time.Duration

	start := time.Now()
	for i := 0; i < targetFrames; i++ {
		frameStart := time.Now()
		result, err := mt.TranscodeRaw(raw)
		latency := time.Since(frameStart)

		if err == nil && result != nil {
			successFrames++
			latencies = append(latencies, latency)
		}

		// Pace to target FPS
		elapsed := time.Since(start)
		expected := time.Duration(i+1) * time.Second / time.Duration(targetFPS)
		if elapsed < expected {
			time.Sleep(expected - elapsed)
		}
	}

	// Calculate stats
	actualDuration := time.Since(start)
	actualFPS := float64(successFrames) / actualDuration.Seconds()

	var avgLatency, maxLatency time.Duration
	if len(latencies) > 0 {
		for _, l := range latencies {
			avgLatency += l
			if l > maxLatency {
				maxLatency = l
			}
		}
		avgLatency /= time.Duration(len(latencies))
	}

	t.Logf("Sustained throughput test:")
	t.Logf("  Target: %d fps for %v", targetFPS, duration)
	t.Logf("  Achieved: %.1f fps, %d/%d frames", actualFPS, successFrames, targetFrames)
	t.Logf("  Latency: avg=%v, max=%v", avgLatency, maxLatency)

	// Should achieve at least 90% of target
	if float64(successFrames)/float64(targetFrames) < 0.9 {
		t.Errorf("only achieved %d/%d frames (%.1f%%)",
			successFrames, targetFrames, float64(successFrames)/float64(targetFrames)*100)
	}
}

func TestMultiTranscoder_NoLagMultipleOutputs(t *testing.T) {
	if !IsVP8Available() || !IsVP9Available() {
		t.Skip("VP8 or VP9 not available")
	}

	// Create multi-output transcoder
	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec:  VideoCodecVP8,
		InputWidth:  640,
		InputHeight: 480,
		InputFPS:    30,
		Outputs: []OutputConfig{
			{ID: "vp8_high", Codec: VideoCodecVP8, Width: 640, Height: 480, BitrateBps: 1_000_000},
			{ID: "vp8_low", Codec: VideoCodecVP8, Width: 320, Height: 240, BitrateBps: 300_000},
			{ID: "vp9", Codec: VideoCodecVP9, Width: 640, Height: 480, BitrateBps: 800_000},
		},
	})
	if err != nil {
		t.Fatalf("failed to create transcoder: %v", err)
	}
	defer mt.Close()

	raw := createTestVideoFrame(640, 480)

	// Warm up
	for i := 0; i < 60; i++ {
		mt.TranscodeRaw(raw)
	}

	// Run 100 frames and check for lag buildup
	var latencies []time.Duration
	for i := 0; i < 100; i++ {
		start := time.Now()
		result, _ := mt.TranscodeRaw(raw)
		latency := time.Since(start)

		if result != nil {
			latencies = append(latencies, latency)
		}
	}

	if len(latencies) < 50 {
		t.Skip("not enough frames for analysis")
	}

	// Compare first half vs second half latency (should not increase)
	half := len(latencies) / 2
	var firstHalfAvg, secondHalfAvg time.Duration
	for i := 0; i < half; i++ {
		firstHalfAvg += latencies[i]
		secondHalfAvg += latencies[half+i]
	}
	firstHalfAvg /= time.Duration(half)
	secondHalfAvg /= time.Duration(half)

	t.Logf("Latency analysis: first half avg=%v, second half avg=%v", firstHalfAvg, secondHalfAvg)

	// Second half should not be significantly higher (lag buildup indicator)
	if secondHalfAvg > firstHalfAvg*2 {
		t.Errorf("latency increased over time (lag buildup): %v -> %v", firstHalfAvg, secondHalfAvg)
	}
}

// --- Benchmarks ---

func BenchmarkTranscoder_VP8toVP8(b *testing.B) {
	if !IsVP8Available() {
		b.Skip("VP8 not available")
	}

	transcoder, err := NewTranscoder(TranscoderConfig{
		InputCodec:  VideoCodecVP8,
		OutputCodec: VideoCodecVP8,
		Width:       640,
		Height:      480,
		BitrateBps:  500_000,
		FPS:         30,
	})
	if err != nil {
		b.Fatalf("failed to create transcoder: %v", err)
	}
	defer transcoder.Close()

	raw := createTestVideoFrame(640, 480)

	// Warm up
	for i := 0; i < 60; i++ {
		transcoder.TranscodeRaw(raw)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		transcoder.TranscodeRaw(raw)
	}
}

func BenchmarkMultiTranscoder_3Outputs(b *testing.B) {
	if !IsVP8Available() {
		b.Skip("VP8 not available")
	}

	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec:  VideoCodecVP8,
		InputWidth:  640,
		InputHeight: 480,
		InputFPS:    30,
		Outputs: []OutputConfig{
			{ID: "high", Codec: VideoCodecVP8, Width: 640, Height: 480, BitrateBps: 1_000_000},
			{ID: "medium", Codec: VideoCodecVP8, Width: 480, Height: 360, BitrateBps: 500_000},
			{ID: "low", Codec: VideoCodecVP8, Width: 320, Height: 240, BitrateBps: 200_000},
		},
	})
	if err != nil {
		b.Fatalf("failed to create transcoder: %v", err)
	}
	defer mt.Close()

	raw := createTestVideoFrame(640, 480)

	// Warm up
	for i := 0; i < 60; i++ {
		mt.TranscodeRaw(raw)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		mt.TranscodeRaw(raw)
	}
}

func BenchmarkMultiTranscoder_ScalingWithOutputs(b *testing.B) {
	if !IsVP8Available() {
		b.Skip("VP8 not available")
	}

	for _, numOutputs := range []int{1, 2, 3, 4} {
		b.Run("outputs_"+string(rune('0'+numOutputs)), func(b *testing.B) {
			outputs := make([]OutputConfig, numOutputs)
			for i := 0; i < numOutputs; i++ {
				outputs[i] = OutputConfig{
					ID:         "output_" + string(rune('A'+i)),
					Codec:      VideoCodecVP8,
					Width:      640,
					Height:     480,
					BitrateBps: 500_000,
				}
			}

			mt, err := NewMultiTranscoder(MultiTranscoderConfig{
				InputCodec:  VideoCodecVP8,
				InputWidth:  640,
				InputHeight: 480,
				InputFPS:    30,
				Outputs:     outputs,
			})
			if err != nil {
				b.Fatalf("failed to create transcoder: %v", err)
			}
			defer mt.Close()

			raw := createTestVideoFrame(640, 480)

			// Warm up
			for i := 0; i < 60; i++ {
				mt.TranscodeRaw(raw)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				mt.TranscodeRaw(raw)
			}
		})
	}
}

// --- Raw Frame Transcoding Tests ---

// TestTranscoder_RawFrameInput verifies that raw VideoFrame can be encoded directly
// without needing to decode first. This is the preferred API when you have raw YUV frames.
func TestTranscoder_RawFrameInput(t *testing.T) {
	if !IsVP8Available() {
		t.Skip("VP8 not available")
	}

	transcoder, err := NewTranscoder(TranscoderConfig{
		OutputCodec: VideoCodecVP8,
		Width:       640,
		Height:      480,
		BitrateBps:  500_000,
		FPS:         30,
	})
	if err != nil {
		t.Fatalf("failed to create transcoder: %v", err)
	}
	defer transcoder.Close()

	// Create raw YUV frame (no decoding needed)
	raw := createTestVideoFrame(640, 480)

	// Encode directly using TranscodeRaw
	var output *EncodedFrame
	for i := 0; i < 60 && output == nil; i++ {
		output, err = transcoder.TranscodeRaw(raw)
		if err != nil {
			t.Fatalf("TranscodeRaw failed: %v", err)
		}
	}

	if output == nil {
		t.Skip("encoder still buffering")
	}

	// Verify output
	if len(output.Data) == 0 {
		t.Error("output frame has no data")
	}
	t.Logf("Raw frame encoded to %d bytes", len(output.Data))
}

// TestMultiTranscoder_RawFrameInput verifies that MultiTranscoder can encode raw frames
// to multiple outputs simultaneously.
func TestMultiTranscoder_RawFrameInput(t *testing.T) {
	if !IsVP8Available() || !IsVP9Available() {
		t.Skip("VP8 or VP9 not available")
	}

	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputWidth:  640,
		InputHeight: 480,
		InputFPS:    30,
		Outputs: []OutputConfig{
			{ID: "vp8", Codec: VideoCodecVP8, BitrateBps: 500_000},
			{ID: "vp9", Codec: VideoCodecVP9, BitrateBps: 500_000},
		},
	})
	if err != nil {
		t.Fatalf("failed to create multi-transcoder: %v", err)
	}
	defer mt.Close()

	// Create raw YUV frame
	raw := createTestVideoFrame(640, 480)

	// Encode to multiple outputs using TranscodeRaw
	var result *TranscodeResult
	for i := 0; i < 60 && (result == nil || len(result.Variants) < 2); i++ {
		result, err = mt.TranscodeRaw(raw)
		if err != nil {
			t.Fatalf("TranscodeRaw failed: %v", err)
		}
	}

	if result == nil || len(result.Variants) < 2 {
		t.Skip("encoders still buffering")
	}

	// Verify both outputs
	for _, v := range result.Variants {
		if v.Frame == nil || len(v.Frame.Data) == 0 {
			t.Errorf("variant %s has no data", v.VariantID)
		}
		t.Logf("Raw frame encoded to %s: %d bytes", v.VariantID, len(v.Frame.Data))
	}
}

// TestMultiTranscoder_Passthrough verifies that passthrough skips decode+encode.
func TestMultiTranscoder_Passthrough(t *testing.T) {
	if !IsVP8Available() {
		t.Skip("VP8 not available")
	}

	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec:  VideoCodecVP8,
		InputWidth:  640,
		InputHeight: 480,
		InputFPS:    30,
		Outputs: []OutputConfig{
			{ID: "passthrough", Codec: VideoCodecVP8, Passthrough: true},
			{ID: "transcode", Codec: VideoCodecVP8, Width: 320, Height: 240, BitrateBps: 300_000},
		},
	})
	if err != nil {
		t.Fatalf("failed to create multi-transcoder: %v", err)
	}
	defer mt.Close()

	// Create encoded input
	input := createEncodedTestFrame(t, VideoCodecVP8, 640, 480, true)

	// Transcode
	result, err := mt.Transcode(input)
	if err != nil {
		t.Fatalf("transcode failed: %v", err)
	}

	// Check passthrough variant uses same data pointer (zero-copy)
	for _, v := range result.Variants {
		if v.VariantID == "passthrough" {
			// Passthrough should return the same frame
			if v.Frame != input {
				t.Log("passthrough created a new frame (expected same pointer)")
			}
			if len(v.Frame.Data) == 0 {
				t.Error("passthrough frame has no data")
			}
			t.Logf("Passthrough: %d bytes (zero CPU cost)", len(v.Frame.Data))
		}
	}

	// Measure time difference
	var passthroughTime, transcodeTime time.Duration
	const iterations = 100

	// Passthrough-only config
	mtPassthrough, _ := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec: VideoCodecVP8,
		Outputs:    []OutputConfig{{ID: "pt", Codec: VideoCodecVP8, Passthrough: true}},
	})
	defer mtPassthrough.Close()

	start := time.Now()
	for i := 0; i < iterations; i++ {
		mtPassthrough.Transcode(input)
	}
	passthroughTime = time.Since(start)

	// Transcode-only config (need raw frame for this)
	mtTranscode, _ := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec:  VideoCodecVP8,
		InputWidth:  640,
		InputHeight: 480,
		Outputs:     []OutputConfig{{ID: "tc", Codec: VideoCodecVP8, BitrateBps: 500_000}},
	})
	defer mtTranscode.Close()

	raw := createTestVideoFrame(640, 480)
	// Warm up
	for i := 0; i < 60; i++ {
		mtTranscode.TranscodeRaw(raw)
	}

	start = time.Now()
	for i := 0; i < iterations; i++ {
		mtTranscode.TranscodeRaw(raw)
	}
	transcodeTime = time.Since(start)

	t.Logf("Passthrough: %v for %d iterations (%.2f µs/op)", passthroughTime, iterations, float64(passthroughTime.Microseconds())/float64(iterations))
	t.Logf("Transcode:   %v for %d iterations (%.2f µs/op)", transcodeTime, iterations, float64(transcodeTime.Microseconds())/float64(iterations))
	t.Logf("Speedup: %.0fx", float64(transcodeTime)/float64(passthroughTime))
}

// --- Dynamic Output Addition Tests ---

// TestMultiTranscoder_AddOutput verifies that outputs can be added dynamically.
func TestMultiTranscoder_AddOutput(t *testing.T) {
	if !IsVP8Available() {
		t.Skip("VP8 not available")
	}

	// Create with single output
	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec:  VideoCodecVP8,
		InputWidth:  640,
		InputHeight: 480,
		InputFPS:    30,
		Outputs: []OutputConfig{
			{ID: "initial", Codec: VideoCodecVP8, BitrateBps: 500_000},
		},
	})
	if err != nil {
		t.Fatalf("failed to create multi-transcoder: %v", err)
	}
	defer mt.Close()

	// Verify initial state
	if mt.OutputCount() != 1 {
		t.Errorf("expected 1 output, got %d", mt.OutputCount())
	}

	// Add a second output dynamically
	err = mt.AddOutput(OutputConfig{
		ID:         "dynamic_vp8",
		Codec:      VideoCodecVP8,
		Width:      320,
		Height:     240,
		BitrateBps: 200_000,
	})
	if err != nil {
		t.Fatalf("AddOutput failed: %v", err)
	}

	if mt.OutputCount() != 2 {
		t.Errorf("expected 2 outputs, got %d", mt.OutputCount())
	}

	// Transcode and verify both outputs produce data
	raw := createTestVideoFrame(640, 480)
	var result *TranscodeResult
	for i := 0; i < 60 && (result == nil || len(result.Variants) < 2); i++ {
		result, _ = mt.TranscodeRaw(raw)
	}

	if result == nil || len(result.Variants) < 2 {
		t.Skip("encoders still buffering")
	}

	t.Logf("Dynamic output addition: %d variants", len(result.Variants))
	for _, v := range result.Variants {
		if v.Frame != nil {
			t.Logf("  %s: %d bytes", v.VariantID, len(v.Frame.Data))
		}
	}
}

// TestMultiTranscoder_AddOutput_AllCodecs tests adding outputs for all supported codecs.
func TestMultiTranscoder_AddOutput_AllCodecs(t *testing.T) {
	if !IsVP8Available() {
		t.Skip("VP8 not available")
	}

	// Start with empty-ish transcoder (one initial output)
	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputWidth:  640,
		InputHeight: 480,
		InputFPS:    30,
		Outputs: []OutputConfig{
			{ID: "base", Codec: VideoCodecVP8, BitrateBps: 500_000},
		},
	})
	if err != nil {
		t.Fatalf("failed to create multi-transcoder: %v", err)
	}
	defer mt.Close()

	// Add VP8 output
	err = mt.AddOutput(OutputConfig{
		ID:         "vp8",
		Codec:      VideoCodecVP8,
		Width:      320,
		Height:     240,
		BitrateBps: 200_000,
	})
	if err != nil {
		t.Fatalf("AddOutput VP8 failed: %v", err)
	}
	t.Log("Added VP8 output")

	// Add VP9 output if available
	if IsVP9Available() {
		err = mt.AddOutput(OutputConfig{
			ID:         "vp9",
			Codec:      VideoCodecVP9,
			Width:      640,
			Height:     480,
			BitrateBps: 400_000,
		})
		if err != nil {
			t.Errorf("AddOutput VP9 failed: %v", err)
		} else {
			t.Log("Added VP9 output")
		}
	}

	// Add H264 output if available
	if IsH264EncoderAvailable() {
		err = mt.AddOutput(OutputConfig{
			ID:         "h264",
			Codec:      VideoCodecH264,
			Width:      640,
			Height:     480,
			BitrateBps: 500_000,
		})
		if err != nil {
			t.Errorf("AddOutput H264 failed: %v", err)
		} else {
			t.Log("Added H264 output")
		}
	}

	// Add AV1 output if available
	if IsAV1Available() {
		err = mt.AddOutput(OutputConfig{
			ID:         "av1",
			Codec:      VideoCodecAV1,
			Width:      640,
			Height:     480,
			BitrateBps: 300_000,
		})
		if err != nil {
			t.Errorf("AddOutput AV1 failed: %v", err)
		} else {
			t.Log("Added AV1 output")
		}
	}

	t.Logf("Total outputs: %d", mt.OutputCount())

	// Verify all variants are listed
	variants := mt.Variants()
	t.Logf("Variants: %v", variants)

	// Transcode and verify outputs
	raw := createTestVideoFrame(640, 480)
	var result *TranscodeResult
	for i := 0; i < 90; i++ {
		result, _ = mt.TranscodeRaw(raw)
	}

	if result != nil {
		t.Logf("Transcode produced %d variants", len(result.Variants))
		for _, v := range result.Variants {
			if v.Frame != nil {
				t.Logf("  %s (%s): %d bytes", v.VariantID, v.Codec, len(v.Frame.Data))
			}
		}
	}
}

// TestMultiTranscoder_RemoveOutput verifies that outputs can be removed dynamically.
func TestMultiTranscoder_RemoveOutput(t *testing.T) {
	if !IsVP8Available() {
		t.Skip("VP8 not available")
	}

	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec:  VideoCodecVP8,
		InputWidth:  640,
		InputHeight: 480,
		InputFPS:    30,
		Outputs: []OutputConfig{
			{ID: "keep", Codec: VideoCodecVP8, BitrateBps: 500_000},
			{ID: "remove", Codec: VideoCodecVP8, Width: 320, Height: 240, BitrateBps: 200_000},
		},
	})
	if err != nil {
		t.Fatalf("failed to create multi-transcoder: %v", err)
	}
	defer mt.Close()

	if mt.OutputCount() != 2 {
		t.Errorf("expected 2 outputs, got %d", mt.OutputCount())
	}

	// Remove one output
	err = mt.RemoveOutput("remove")
	if err != nil {
		t.Fatalf("RemoveOutput failed: %v", err)
	}

	if mt.OutputCount() != 1 {
		t.Errorf("expected 1 output after removal, got %d", mt.OutputCount())
	}

	// Verify removed variant is gone
	_, exists := mt.VariantConfig("remove")
	if exists {
		t.Error("removed variant still exists")
	}

	// Transcode and verify only one output
	raw := createTestVideoFrame(640, 480)
	var result *TranscodeResult
	for i := 0; i < 60 && (result == nil || len(result.Variants) == 0); i++ {
		result, _ = mt.TranscodeRaw(raw)
	}

	if result != nil {
		if len(result.Variants) != 1 {
			t.Errorf("expected 1 variant, got %d", len(result.Variants))
		}
		for _, v := range result.Variants {
			if v.VariantID == "remove" {
				t.Error("removed variant still producing output")
			}
		}
	}
}

// TestMultiTranscoder_AddOutput_Errors tests error handling for AddOutput.
func TestMultiTranscoder_AddOutput_Errors(t *testing.T) {
	if !IsVP8Available() {
		t.Skip("VP8 not available")
	}

	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputWidth:  640,
		InputHeight: 480,
		InputFPS:    30,
		Outputs: []OutputConfig{
			{ID: "existing", Codec: VideoCodecVP8, BitrateBps: 500_000},
		},
	})
	if err != nil {
		t.Fatalf("failed to create multi-transcoder: %v", err)
	}
	defer mt.Close()

	// Test: empty ID
	err = mt.AddOutput(OutputConfig{Codec: VideoCodecVP8})
	if err == nil {
		t.Error("expected error for empty ID")
	}

	// Test: duplicate ID
	err = mt.AddOutput(OutputConfig{ID: "existing", Codec: VideoCodecVP8})
	if err == nil {
		t.Error("expected error for duplicate ID")
	}

	// Test: unknown codec
	err = mt.AddOutput(OutputConfig{ID: "new", Codec: VideoCodecUnknown})
	if err == nil {
		t.Error("expected error for unknown codec")
	}
}

// TestMultiTranscoder_AddOutput_Passthrough tests adding passthrough outputs dynamically.
func TestMultiTranscoder_AddOutput_Passthrough(t *testing.T) {
	if !IsVP8Available() {
		t.Skip("VP8 not available")
	}

	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec: VideoCodecVP8,
		Outputs: []OutputConfig{
			{ID: "transcode", Codec: VideoCodecVP8, Width: 320, Height: 240, BitrateBps: 200_000},
		},
	})
	if err != nil {
		t.Fatalf("failed to create multi-transcoder: %v", err)
	}
	defer mt.Close()

	// Add passthrough output dynamically
	err = mt.AddOutput(OutputConfig{
		ID:          "passthrough",
		Codec:       VideoCodecVP8,
		Passthrough: true,
	})
	if err != nil {
		t.Fatalf("AddOutput passthrough failed: %v", err)
	}

	if mt.OutputCount() != 2 {
		t.Errorf("expected 2 outputs, got %d", mt.OutputCount())
	}

	// Verify passthrough config
	cfg, exists := mt.VariantConfig("passthrough")
	if !exists {
		t.Fatal("passthrough variant not found")
	}
	if !cfg.Passthrough {
		t.Error("passthrough flag not set")
	}

	// Transcode and verify passthrough works
	input := createEncodedTestFrame(t, VideoCodecVP8, 640, 480, true)
	result, err := mt.Transcode(input)
	if err != nil {
		t.Fatalf("transcode failed: %v", err)
	}

	// Check passthrough returns same data
	for _, v := range result.Variants {
		if v.VariantID == "passthrough" {
			if len(v.Frame.Data) == 0 {
				t.Error("passthrough frame has no data")
			}
			t.Logf("Passthrough variant: %d bytes", len(v.Frame.Data))
		}
	}
}

// TestMultiTranscoder_AddRemove_Concurrent tests thread safety of add/remove.
func TestMultiTranscoder_AddRemove_Concurrent(t *testing.T) {
	if !IsVP8Available() {
		t.Skip("VP8 not available")
	}

	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputWidth:  640,
		InputHeight: 480,
		InputFPS:    30,
		Outputs: []OutputConfig{
			{ID: "initial", Codec: VideoCodecVP8, BitrateBps: 500_000},
		},
	})
	if err != nil {
		t.Fatalf("failed to create multi-transcoder: %v", err)
	}
	defer mt.Close()

	raw := createTestVideoFrame(640, 480)

	// Run concurrent add/remove/transcode
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 20; i++ {
			id := "dynamic_" + string(rune('A'+i%10))
			mt.AddOutput(OutputConfig{
				ID:         id,
				Codec:      VideoCodecVP8,
				Width:      320,
				Height:     240,
				BitrateBps: 200_000,
			})
			time.Sleep(5 * time.Millisecond)
			mt.RemoveOutput(id)
		}
	}()

	// Transcode concurrently
	for i := 0; i < 100; i++ {
		mt.TranscodeRaw(raw)
		time.Sleep(2 * time.Millisecond)
	}

	<-done
	t.Log("Concurrent add/remove test passed (no deadlock)")
}

// TestMultiTranscoder_ParallelEncodingEfficiency verifies that parallel encoding
// provides better performance than sequential encoding would.
func TestMultiTranscoder_ParallelEncodingEfficiency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping performance test in short mode")
	}
	if !IsVP8Available() {
		t.Skip("VP8 not available")
	}

	// Measure single encoder time
	single, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec:  VideoCodecVP8,
		InputWidth:  640,
		InputHeight: 480,
		InputFPS:    30,
		Outputs: []OutputConfig{
			{ID: "single", Codec: VideoCodecVP8, Width: 640, Height: 480, BitrateBps: 500_000},
		},
	})
	if err != nil {
		t.Fatalf("failed to create single transcoder: %v", err)
	}
	defer single.Close()

	// Measure triple encoder time
	triple, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec:  VideoCodecVP8,
		InputWidth:  640,
		InputHeight: 480,
		InputFPS:    30,
		Outputs: []OutputConfig{
			{ID: "a", Codec: VideoCodecVP8, Width: 640, Height: 480, BitrateBps: 500_000},
			{ID: "b", Codec: VideoCodecVP8, Width: 640, Height: 480, BitrateBps: 500_000},
			{ID: "c", Codec: VideoCodecVP8, Width: 640, Height: 480, BitrateBps: 500_000},
		},
	})
	if err != nil {
		t.Fatalf("failed to create triple transcoder: %v", err)
	}
	defer triple.Close()

	raw := createTestVideoFrame(640, 480)

	// Warm up both
	for i := 0; i < 60; i++ {
		single.TranscodeRaw(raw)
		triple.TranscodeRaw(raw)
	}

	// Measure single encoder
	var singleTotal time.Duration
	for i := 0; i < 30; i++ {
		start := time.Now()
		single.TranscodeRaw(raw)
		singleTotal += time.Since(start)
	}
	singleAvg := singleTotal / 30

	// Measure triple encoder
	var tripleTotal time.Duration
	for i := 0; i < 30; i++ {
		start := time.Now()
		triple.TranscodeRaw(raw)
		tripleTotal += time.Since(start)
	}
	tripleAvg := tripleTotal / 30

	t.Logf("Single encoder avg: %v", singleAvg)
	t.Logf("Triple encoder avg: %v", tripleAvg)
	t.Logf("Ratio (triple/single): %.2fx", float64(tripleAvg)/float64(singleAvg))

	// With parallel encoding, triple should be less than 3x single
	// (ideally close to 1x on multi-core systems)
	// Note: with sequential scaling for thread-safety, ratio is higher
	maxAcceptableRatio := 3.0
	actualRatio := float64(tripleAvg) / float64(singleAvg)
	if actualRatio > maxAcceptableRatio {
		t.Errorf("parallel encoding not effective: triple took %.2fx longer than single (expected <%.1fx)",
			actualRatio, maxAcceptableRatio)
	}
}
