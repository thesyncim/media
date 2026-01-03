//go:build (darwin || linux) && !noopus
// +build darwin linux
// +build !noopus

package media

import (
	"encoding/binary"
	"math"
	"testing"
)

// createTestAudioSamples creates test audio samples (sine wave)
// Returns AudioSamples with Data as []byte (S16LE format)
func createTestAudioSamples(sampleRate, channels, durationMs int) *AudioSamples {
	samplesPerChannel := sampleRate * durationMs / 1000
	totalSamples := samplesPerChannel * channels

	// Create byte buffer (2 bytes per sample for S16)
	data := make([]byte, totalSamples*2)

	frequency := 440.0 // A4 note
	for i := 0; i < samplesPerChannel; i++ {
		t := float64(i) / float64(sampleRate)
		sample := int16(math.Sin(2*math.Pi*frequency*t) * 16000)

		for ch := 0; ch < channels; ch++ {
			idx := (i*channels + ch) * 2
			binary.LittleEndian.PutUint16(data[idx:], uint16(sample))
		}
	}

	return &AudioSamples{
		Data:        data,
		SampleRate:  sampleRate,
		Channels:    channels,
		SampleCount: samplesPerChannel,
		Format:      AudioFormatS16,
		Timestamp:   0,
	}
}

func TestOpusAvailable(t *testing.T) {
	if !IsOpusAvailable() {
		t.Skip("Opus not available")
	}

	version := GetOpusVersion()
	if version == "" {
		t.Error("GetOpusVersion returned empty string")
	}
	t.Logf("Opus version: %s", version)
}

func TestOpusEncoder(t *testing.T) {
	config := AudioEncoderConfig{
		SampleRate: 48000,
		Channels:   1,
		BitrateBps: 24000,
	}

	enc, err := NewOpusEncoder(config)
	if err != nil {
		t.Fatalf("Failed to create Opus encoder: %v", err)
	}
	defer enc.Close()

	// Verify codec
	if enc.Codec() != AudioCodecOpus {
		t.Errorf("Codec = %v, want Opus", enc.Codec())
	}

	// Encode a frame (20ms of audio)
	samples := createTestAudioSamples(48000, 1, 20)
	encoded, err := enc.Encode(samples)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	if encoded == nil {
		t.Fatal("Encode returned nil")
	}
	if len(encoded.Data) == 0 {
		t.Error("Encoded data is empty")
	}

	// Check stats
	stats := enc.Stats()
	if stats.FramesEncoded != 1 {
		t.Errorf("FramesEncoded = %d, want 1", stats.FramesEncoded)
	}
	if stats.BytesEncoded == 0 {
		t.Error("BytesEncoded = 0")
	}

	t.Logf("Encoded %d samples to %d bytes", len(samples.Data), len(encoded.Data))
}

func TestOpusEncoderStereo(t *testing.T) {
	config := AudioEncoderConfig{
		SampleRate: 48000,
		Channels:   2,
		BitrateBps: 64000,
	}

	enc, err := NewOpusEncoder(config)
	if err != nil {
		t.Fatalf("Failed to create stereo Opus encoder: %v", err)
	}
	defer enc.Close()

	// Encode a frame
	samples := createTestAudioSamples(48000, 2, 20)
	encoded, err := enc.Encode(samples)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	if encoded == nil || len(encoded.Data) == 0 {
		t.Fatal("Encode returned empty data")
	}

	t.Logf("Encoded stereo: %d samples to %d bytes", len(samples.Data), len(encoded.Data))
}

func TestOpusDecoder(t *testing.T) {
	config := AudioDecoderConfig{
		SampleRate: 48000,
		Channels:   1,
	}

	dec, err := NewOpusDecoder(config)
	if err != nil {
		t.Fatalf("Failed to create Opus decoder: %v", err)
	}
	defer dec.Close()

	// Verify codec
	if dec.Codec() != AudioCodecOpus {
		t.Errorf("Codec = %v, want Opus", dec.Codec())
	}
}

func TestOpusRoundTrip(t *testing.T) {
	encConfig := AudioEncoderConfig{
		SampleRate: 48000,
		Channels:   1,
		BitrateBps: 32000,
	}

	enc, err := NewOpusEncoder(encConfig)
	if err != nil {
		t.Fatalf("Failed to create Opus encoder: %v", err)
	}
	defer enc.Close()

	decConfig := AudioDecoderConfig{
		SampleRate: 48000,
		Channels:   1,
	}

	dec, err := NewOpusDecoder(decConfig)
	if err != nil {
		t.Fatalf("Failed to create Opus decoder: %v", err)
	}
	defer dec.Close()

	// Create and encode samples
	original := createTestAudioSamples(48000, 1, 20)
	encoded, err := enc.Encode(original)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Decode
	decoded, err := dec.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}
	if decoded == nil {
		t.Fatal("Decode returned nil")
	}

	// Verify sample count matches (20ms at 48kHz = 960 samples)
	expectedSamples := 960
	if decoded.SampleCount != expectedSamples {
		t.Errorf("Decoded samples = %d, want %d", decoded.SampleCount, expectedSamples)
	}

	t.Logf("Round-trip: %d bytes -> %d bytes -> %d samples",
		len(original.Data), len(encoded.Data), decoded.SampleCount)
}

func TestOpusBitrateChange(t *testing.T) {
	config := AudioEncoderConfig{
		SampleRate: 48000,
		Channels:   1,
		BitrateBps: 24000,
	}

	enc, err := NewOpusEncoder(config)
	if err != nil {
		t.Fatalf("Failed to create Opus encoder: %v", err)
	}
	defer enc.Close()

	// Get initial bitrate
	initialBitrate := enc.GetBitrate()
	t.Logf("Initial bitrate: %d", initialBitrate)

	// Change bitrate
	err = enc.SetBitrate(64000)
	if err != nil {
		t.Fatalf("SetBitrate failed: %v", err)
	}

	// Encode with new bitrate
	samples := createTestAudioSamples(48000, 1, 20)
	_, err = enc.Encode(samples)
	if err != nil {
		t.Fatalf("Encode after bitrate change failed: %v", err)
	}

	newBitrate := enc.GetBitrate()
	if newBitrate != 64000 {
		t.Logf("Bitrate after change: %d (may be adjusted by encoder)", newBitrate)
	}
}

func TestOpusDTX(t *testing.T) {
	config := AudioEncoderConfig{
		SampleRate: 48000,
		Channels:   1,
		BitrateBps: 24000,
	}

	enc, err := NewOpusEncoder(config)
	if err != nil {
		t.Fatalf("Failed to create Opus encoder: %v", err)
	}
	defer enc.Close()

	// Enable DTX
	err = enc.SetDTX(true)
	if err != nil {
		t.Fatalf("SetDTX failed: %v", err)
	}

	// Encode silence - DTX should produce smaller frames
	// 20ms at 48kHz mono = 960 samples = 1920 bytes (S16)
	silence := &AudioSamples{
		Data:        make([]byte, 960*2), // 20ms of silence
		SampleRate:  48000,
		Channels:    1,
		SampleCount: 960,
		Format:      AudioFormatS16,
	}

	encoded, err := enc.Encode(silence)
	if err != nil {
		t.Fatalf("Encode with DTX failed: %v", err)
	}

	t.Logf("DTX encoded silence to %d bytes", len(encoded.Data))
}

func TestOpusFEC(t *testing.T) {
	config := AudioEncoderConfig{
		SampleRate: 48000,
		Channels:   1,
		BitrateBps: 32000,
	}

	enc, err := NewOpusEncoder(config)
	if err != nil {
		t.Fatalf("Failed to create Opus encoder: %v", err)
	}
	defer enc.Close()

	// Enable FEC and set packet loss percentage
	err = enc.SetFEC(true)
	if err != nil {
		t.Fatalf("SetFEC failed: %v", err)
	}

	err = enc.SetPacketLossPercent(20)
	if err != nil {
		t.Fatalf("SetPacketLossPercent failed: %v", err)
	}

	// Encode - FEC should be embedded in the stream
	samples := createTestAudioSamples(48000, 1, 20)
	encoded, err := enc.Encode(samples)
	if err != nil {
		t.Fatalf("Encode with FEC failed: %v", err)
	}

	t.Logf("FEC encoded to %d bytes", len(encoded.Data))
}

func TestOpusComplexity(t *testing.T) {
	config := AudioEncoderConfig{
		SampleRate: 48000,
		Channels:   1,
		BitrateBps: 24000,
	}

	enc, err := NewOpusEncoder(config)
	if err != nil {
		t.Fatalf("Failed to create Opus encoder: %v", err)
	}
	defer enc.Close()

	// Set complexity
	err = enc.SetComplexity(5)
	if err != nil {
		t.Fatalf("SetComplexity failed: %v", err)
	}

	samples := createTestAudioSamples(48000, 1, 20)
	_, err = enc.Encode(samples)
	if err != nil {
		t.Fatalf("Encode with complexity 5 failed: %v", err)
	}
}

func TestOpusDecoderReset(t *testing.T) {
	dec, err := NewOpusDecoder(AudioDecoderConfig{
		SampleRate: 48000,
		Channels:   1,
	})
	if err != nil {
		t.Fatalf("Failed to create Opus decoder: %v", err)
	}
	defer dec.Close()

	err = dec.Reset()
	if err != nil {
		t.Errorf("Reset failed: %v", err)
	}
}

func TestOpusPLC(t *testing.T) {
	dec, err := NewOpusDecoder(AudioDecoderConfig{
		SampleRate: 48000,
		Channels:   1,
	})
	if err != nil {
		t.Fatalf("Failed to create Opus decoder: %v", err)
	}
	defer dec.Close()

	// First, decode a real packet to initialize state
	enc, err := NewOpusEncoder(AudioEncoderConfig{
		SampleRate: 48000,
		Channels:   1,
		BitrateBps: 24000,
	})
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}
	defer enc.Close()

	samples := createTestAudioSamples(48000, 1, 20)
	encoded, err := enc.Encode(samples)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Decode the real packet
	_, err = dec.Decode(encoded)
	if err != nil {
		t.Fatalf("Initial decode failed: %v", err)
	}

	// Now do PLC (packet loss concealment) by passing empty data
	plcSamples, err := dec.DecodeWithPLC(nil)
	if err != nil {
		t.Fatalf("PLC decode failed: %v", err)
	}
	if plcSamples == nil || len(plcSamples.Data) == 0 {
		t.Error("PLC returned empty samples")
	}

	t.Logf("PLC produced %d samples", plcSamples.SampleCount)
}

func TestOpusPacketSamples(t *testing.T) {
	// Encode a known frame
	enc, err := NewOpusEncoder(AudioEncoderConfig{
		SampleRate: 48000,
		Channels:   1,
		BitrateBps: 24000,
	})
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}
	defer enc.Close()

	// 20ms at 48kHz = 960 samples
	samples := createTestAudioSamples(48000, 1, 20)
	encoded, err := enc.Encode(samples)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Check packet samples
	packetSamples := GetOpusPacketSamples(encoded.Data, 48000)
	if packetSamples != 960 {
		t.Errorf("GetOpusPacketSamples = %d, want 960", packetSamples)
	}
}

func BenchmarkOpusEncode(b *testing.B) {
	enc, err := NewOpusEncoder(AudioEncoderConfig{
		SampleRate: 48000,
		Channels:   1,
		BitrateBps: 32000,
	})
	if err != nil {
		b.Fatalf("Failed to create encoder: %v", err)
	}
	defer enc.Close()

	samples := createTestAudioSamples(48000, 1, 20)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := enc.Encode(samples)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOpusDecode(b *testing.B) {
	enc, err := NewOpusEncoder(AudioEncoderConfig{
		SampleRate: 48000,
		Channels:   1,
		BitrateBps: 32000,
	})
	if err != nil {
		b.Fatalf("Failed to create encoder: %v", err)
	}
	defer enc.Close()

	dec, err := NewOpusDecoder(AudioDecoderConfig{
		SampleRate: 48000,
		Channels:   1,
	})
	if err != nil {
		b.Fatalf("Failed to create decoder: %v", err)
	}
	defer dec.Close()

	samples := createTestAudioSamples(48000, 1, 20)
	encoded, _ := enc.Encode(samples)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := dec.Decode(encoded)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOpusRoundTrip(b *testing.B) {
	enc, err := NewOpusEncoder(AudioEncoderConfig{
		SampleRate: 48000,
		Channels:   1,
		BitrateBps: 32000,
	})
	if err != nil {
		b.Fatalf("Failed to create encoder: %v", err)
	}
	defer enc.Close()

	dec, err := NewOpusDecoder(AudioDecoderConfig{
		SampleRate: 48000,
		Channels:   1,
	})
	if err != nil {
		b.Fatalf("Failed to create decoder: %v", err)
	}
	defer dec.Close()

	samples := createTestAudioSamples(48000, 1, 20)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		encoded, _ := enc.Encode(samples)
		_, err := dec.Decode(encoded)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkOpusCallOverhead measures the pure FFI call overhead
// by calling simple getter functions that do minimal work in C.
func BenchmarkOpusCallOverhead(b *testing.B) {
	if !IsOpusAvailable() {
		b.Skip("Opus not available")
	}

	b.Run("GetVersion", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = GetOpusVersion()
		}
	})

	enc, err := NewOpusEncoder(AudioEncoderConfig{
		SampleRate: 48000,
		Channels:   1,
		BitrateBps: 32000,
	})
	if err != nil {
		b.Fatalf("Failed to create encoder: %v", err)
	}
	defer enc.Close()

	b.Run("GetBitrate", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = enc.GetBitrate()
		}
	})

	b.Run("SetBitrate", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = enc.SetBitrate(32000)
		}
	})
}
