//go:build (darwin || linux) && !noopus

package media

import "testing"

// BenchmarkPuregoCallOverhead measures the purego call overhead for comparison with CGO.
func BenchmarkPuregoCallOverhead(b *testing.B) {
	if !IsOpusAvailable() {
		b.Skip("Opus not available")
	}

	b.Run("GetVersion", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = GetOpusVersion()
		}
	})

	b.Run("CreateDestroy", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			enc, err := NewOpusEncoder(AudioEncoderConfig{
				SampleRate: 48000,
				Channels:   1,
			})
			if err != nil {
				b.Fatal(err)
			}
			enc.Close()
		}
	})

	// Benchmark encoding - more realistic workload
	b.Run("EncodeFrame", func(b *testing.B) {
		enc, err := NewOpusEncoder(AudioEncoderConfig{
			SampleRate: 48000,
			Channels:   1,
			BitrateBps: 24000,
		})
		if err != nil {
			b.Fatal(err)
		}
		defer enc.Close()

		// 20ms of audio at 48kHz mono = 960 samples = 1920 bytes (16-bit)
		samples := &AudioSamples{
			Data:        make([]byte, 1920),
			SampleRate:  48000,
			Channels:    1,
			SampleCount: 960,
			Format:      AudioFormatS16,
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := enc.Encode(samples)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkVPXPurego benchmarks VP8/VP9 encoding via purego
func BenchmarkVPXPurego(b *testing.B) {
	if !IsVP8Available() {
		b.Skip("VP8 not available")
	}

	b.Run("VP8_CreateDestroy", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			enc, err := NewVP8Encoder(VideoEncoderConfig{
				Width:      640,
				Height:     480,
				FPS:        30,
				BitrateBps: 1000000,
			})
			if err != nil {
				b.Fatal(err)
			}
			enc.Close()
		}
	})

	b.Run("VP8_EncodeFrame", func(b *testing.B) {
		enc, err := NewVP8Encoder(VideoEncoderConfig{
			Width:      640,
			Height:     480,
			FPS:        30,
			BitrateBps: 1000000,
		})
		if err != nil {
			b.Fatal(err)
		}
		defer enc.Close()

		frame := &VideoFrame{
			Width:  640,
			Height: 480,
			Format: PixelFormatI420,
			Data: [][]byte{
				make([]byte, 640*480), // Y
				make([]byte, 320*240), // U
				make([]byte, 320*240), // V
			},
			Stride: []int{640, 320, 320},
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := enc.Encode(frame)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
