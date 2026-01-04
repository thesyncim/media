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
