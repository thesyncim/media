package media

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestNewAudioTestPatternSource_Defaults(t *testing.T) {
	config := AudioTestPatternConfig{}
	source := NewAudioTestPatternSource(config)

	if source.SampleRate() != 48000 {
		t.Errorf("Default sample rate = %d, want 48000", source.SampleRate())
	}
	if source.Channels() != 2 {
		t.Errorf("Default channels = %d, want 2", source.Channels())
	}
}

func TestNewAudioTestPatternSource_CustomConfig(t *testing.T) {
	config := AudioTestPatternConfig{
		SampleRate: 44100,
		Channels:   1,
		FrameSize:  480,
		Frequency:  880.0,
		Amplitude:  0.8,
	}
	source := NewAudioTestPatternSource(config)

	if source.SampleRate() != 44100 {
		t.Errorf("Sample rate = %d, want 44100", source.SampleRate())
	}
	if source.Channels() != 1 {
		t.Errorf("Channels = %d, want 1", source.Channels())
	}
}

func TestAudioTestPatternSource_StartStop(t *testing.T) {
	source := NewAudioTestPatternSource(AudioTestPatternConfig{
		SampleRate: 48000,
		Channels:   2,
		FrameSize:  960,
	})

	ctx := context.Background()

	// Start should succeed
	if err := source.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Double start should fail
	if err := source.Start(ctx); err == nil {
		t.Error("Double start should fail")
	}

	// Stop should succeed
	if err := source.Stop(); err != nil {
		t.Errorf("Stop failed: %v", err)
	}

	// Double stop should be safe
	if err := source.Stop(); err != nil {
		t.Errorf("Double stop should not fail: %v", err)
	}
}

func TestAudioTestPatternSource_ReadSamples(t *testing.T) {
	source := NewAudioTestPatternSource(AudioTestPatternConfig{
		SampleRate: 48000,
		Channels:   2,
		FrameSize:  960,
		Pattern:    AudioPatternSineWave,
		Frequency:  440.0,
		Amplitude:  0.5,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := source.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer source.Close()

	// Read samples
	samples, err := source.ReadSamples(ctx)
	if err != nil {
		t.Fatalf("ReadSamples failed: %v", err)
	}

	// Verify sample properties
	if samples.SampleRate != 48000 {
		t.Errorf("Sample rate = %d, want 48000", samples.SampleRate)
	}
	if samples.Channels != 2 {
		t.Errorf("Channels = %d, want 2", samples.Channels)
	}
	if samples.SampleCount != 960 {
		t.Errorf("Sample count = %d, want 960", samples.SampleCount)
	}
	if samples.Format != AudioFormatS16 {
		t.Errorf("Format = %v, want S16", samples.Format)
	}

	// Verify data size (S16 = 2 bytes per sample per channel)
	expectedSize := 960 * 2 * 2
	if len(samples.Data) != expectedSize {
		t.Errorf("Data size = %d, want %d", len(samples.Data), expectedSize)
	}

	if samples.Timestamp <= 0 {
		t.Error("Timestamp should be positive")
	}
}

func TestAudioTestPatternSource_Callback(t *testing.T) {
	source := NewAudioTestPatternSource(AudioTestPatternConfig{
		SampleRate: 48000,
		Channels:   2,
		FrameSize:  960,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	samplesReceived := make(chan *AudioSamples, 1)
	source.SetCallback(func(samples *AudioSamples) {
		select {
		case samplesReceived <- samples:
		default:
		}
	})

	if err := source.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer source.Close()

	select {
	case samples := <-samplesReceived:
		if samples.SampleRate != 48000 {
			t.Errorf("Callback sample rate: %d", samples.SampleRate)
		}
	case <-ctx.Done():
		t.Fatal("Timeout waiting for callback samples")
	}
}

func TestAudioTestPatternSource_AllPatterns(t *testing.T) {
	patterns := []AudioPatternType{
		AudioPatternSilence,
		AudioPatternSineWave,
		AudioPatternSquareWave,
		AudioPatternWhiteNoise,
		AudioPatternSweep,
	}

	for _, pattern := range patterns {
		t.Run(pattern.String(), func(t *testing.T) {
			config := AudioTestPatternConfig{
				SampleRate:    48000,
				Channels:      2,
				FrameSize:     960,
				Pattern:       pattern,
				Frequency:     440.0,
				Amplitude:     0.5,
				SweepStartHz:  200,
				SweepEndHz:    2000,
				SweepDuration: time.Second,
			}
			source := NewAudioTestPatternSource(config)

			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()

			if err := source.Start(ctx); err != nil {
				t.Fatalf("Start failed: %v", err)
			}
			defer source.Close()

			// Read a few frames
			for i := 0; i < 3; i++ {
				samples, err := source.ReadSamples(ctx)
				if err != nil {
					t.Fatalf("ReadSamples failed on frame %d: %v", i, err)
				}
				if samples == nil {
					t.Fatalf("ReadSamples returned nil on frame %d", i)
				}
			}
		})
	}
}

func TestAudioTestPatternSource_Silence(t *testing.T) {
	source := NewAudioTestPatternSource(AudioTestPatternConfig{
		SampleRate: 48000,
		Channels:   2,
		FrameSize:  960,
		Pattern:    AudioPatternSilence,
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := source.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer source.Close()

	samples, err := source.ReadSamples(ctx)
	if err != nil {
		t.Fatalf("ReadSamples failed: %v", err)
	}

	// All bytes should be zero for silence
	for i, b := range samples.Data {
		if b != 0 {
			t.Errorf("Silence sample byte %d is %d, want 0", i, b)
			break
		}
	}
}

func TestAudioTestPatternSource_SineWave(t *testing.T) {
	source := NewAudioTestPatternSource(AudioTestPatternConfig{
		SampleRate: 48000,
		Channels:   1, // Mono for easier analysis
		FrameSize:  960,
		Pattern:    AudioPatternSineWave,
		Frequency:  440.0,
		Amplitude:  1.0,
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := source.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer source.Close()

	samples, err := source.ReadSamples(ctx)
	if err != nil {
		t.Fatalf("ReadSamples failed: %v", err)
	}

	// Convert to int16 and check for non-zero values
	var hasPositive, hasNegative bool
	var maxVal, minVal int16

	for i := 0; i < len(samples.Data); i += 2 {
		val := int16(samples.Data[i]) | (int16(samples.Data[i+1]) << 8)
		if val > maxVal {
			maxVal = val
		}
		if val < minVal {
			minVal = val
		}
		if val > 0 {
			hasPositive = true
		}
		if val < 0 {
			hasNegative = true
		}
	}

	if !hasPositive || !hasNegative {
		t.Error("Sine wave should have both positive and negative values")
	}

	// Check amplitude (should be close to max for amplitude=1.0)
	if maxVal < 30000 || minVal > -30000 {
		t.Errorf("Sine wave amplitude too low: max=%d, min=%d", maxVal, minVal)
	}
}

func TestAudioTestPatternSource_WhiteNoise(t *testing.T) {
	source := NewAudioTestPatternSource(AudioTestPatternConfig{
		SampleRate: 48000,
		Channels:   1,
		FrameSize:  9600, // Larger sample for better statistics
		Pattern:    AudioPatternWhiteNoise,
		Amplitude:  1.0,
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := source.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer source.Close()

	samples, err := source.ReadSamples(ctx)
	if err != nil {
		t.Fatalf("ReadSamples failed: %v", err)
	}

	// Calculate statistics
	var sum float64
	var sumSquares float64
	count := len(samples.Data) / 2

	for i := 0; i < len(samples.Data); i += 2 {
		val := float64(int16(samples.Data[i]) | (int16(samples.Data[i+1]) << 8))
		sum += val
		sumSquares += val * val
	}

	mean := sum / float64(count)
	variance := (sumSquares / float64(count)) - (mean * mean)
	stdDev := math.Sqrt(variance)

	// White noise should have mean close to 0 and significant variance
	if math.Abs(mean) > 5000 {
		t.Logf("White noise mean: %f (expected near 0)", mean)
	}
	if stdDev < 5000 {
		t.Logf("White noise stddev: %f (expected > 5000)", stdDev)
	}
}

func TestAudioTestPatternSource_Registry(t *testing.T) {
	// Test that audio test pattern source is registered
	if !IsAudioSourceAvailable(SourceTypeTestPattern) {
		t.Error("Audio TestPattern source should be registered")
	}

	// Create via registry
	source, err := CreateAudioSource(SourceTypeTestPattern, nil)
	if err != nil {
		t.Fatalf("CreateAudioSource failed: %v", err)
	}
	defer source.Close()

	if source.SampleRate() != 48000 {
		t.Errorf("Default sample rate = %d, want 48000", source.SampleRate())
	}
}

func TestAudioPatternType_String(t *testing.T) {
	tests := []struct {
		pattern AudioPatternType
		want    string
	}{
		{AudioPatternSilence, "Silence"},
		{AudioPatternSineWave, "SineWave"},
		{AudioPatternSquareWave, "SquareWave"},
		{AudioPatternWhiteNoise, "WhiteNoise"},
		{AudioPatternSweep, "Sweep"},
		{AudioPatternType(99), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.pattern.String(); got != tt.want {
				t.Errorf("AudioPatternType.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func BenchmarkAudioTestPatternSource_SineWave(b *testing.B) {
	source := NewAudioTestPatternSource(AudioTestPatternConfig{
		SampleRate: 48000,
		Channels:   2,
		FrameSize:  960,
		Pattern:    AudioPatternSineWave,
		Frequency:  440.0,
		Amplitude:  0.5,
	})

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		source.generateSamples()
	}
}

func BenchmarkAudioTestPatternSource_WhiteNoise(b *testing.B) {
	source := NewAudioTestPatternSource(AudioTestPatternConfig{
		SampleRate: 48000,
		Channels:   2,
		FrameSize:  960,
		Pattern:    AudioPatternWhiteNoise,
		Amplitude:  0.5,
	})

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		source.generateSamples()
	}
}
