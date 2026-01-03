package media

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// AudioPatternType defines the type of audio test pattern.
type AudioPatternType int

const (
	AudioPatternSilence    AudioPatternType = iota // Silence
	AudioPatternSineWave                           // Sine wave tone
	AudioPatternSquareWave                         // Square wave tone
	AudioPatternWhiteNoise                         // White noise
	AudioPatternSweep                              // Frequency sweep
)

func (p AudioPatternType) String() string {
	switch p {
	case AudioPatternSilence:
		return "Silence"
	case AudioPatternSineWave:
		return "SineWave"
	case AudioPatternSquareWave:
		return "SquareWave"
	case AudioPatternWhiteNoise:
		return "WhiteNoise"
	case AudioPatternSweep:
		return "Sweep"
	default:
		return "Unknown"
	}
}

// AudioTestPatternConfig configures an audio test pattern source.
type AudioTestPatternConfig struct {
	SampleRate int              // Sample rate (default: 48000)
	Channels   int              // Number of channels (default: 2)
	FrameSize  int              // Samples per frame (default: 960 = 20ms at 48kHz)
	Pattern    AudioPatternType // Pattern type
	Frequency  float64          // Tone frequency in Hz (default: 440)
	Amplitude  float64          // Amplitude 0.0-1.0 (default: 0.5)

	// For sweep pattern
	SweepStartHz  float64       // Sweep start frequency
	SweepEndHz    float64       // Sweep end frequency
	SweepDuration time.Duration // Sweep duration
}

// DefaultAudioTestPatternConfig returns a default audio test configuration.
func DefaultAudioTestPatternConfig() AudioTestPatternConfig {
	return AudioTestPatternConfig{
		SampleRate:    48000,
		Channels:      2,
		FrameSize:     960, // 20ms at 48kHz
		Pattern:       AudioPatternSineWave,
		Frequency:     440.0, // A4
		Amplitude:     0.5,
		SweepStartHz:  200,
		SweepEndHz:    2000,
		SweepDuration: 2 * time.Second,
	}
}

// AudioTestPatternSource generates synthetic audio samples.
type AudioTestPatternSource struct {
	config AudioTestPatternConfig

	// Sample buffer
	sampleData []byte

	// Phase for continuous waveforms
	phase       float64
	sweepPhase  float64
	sampleCount uint64

	// State
	running   atomic.Bool
	ctx       context.Context
	cancel    context.CancelFunc
	samplesCh chan *AudioSamples
	callback  AudioSamplesCallback

	// Random state for noise
	rngState uint64

	mu sync.RWMutex
}

// NewAudioTestPatternSource creates a new audio test pattern source.
func NewAudioTestPatternSource(config AudioTestPatternConfig) *AudioTestPatternSource {
	// Apply defaults
	if config.SampleRate <= 0 {
		config.SampleRate = 48000
	}
	if config.Channels <= 0 {
		config.Channels = 2
	}
	if config.FrameSize <= 0 {
		config.FrameSize = 960
	}
	if config.Frequency <= 0 {
		config.Frequency = 440.0
	}
	if config.Amplitude <= 0 {
		config.Amplitude = 0.5
	}
	if config.Amplitude > 1.0 {
		config.Amplitude = 1.0
	}

	// Allocate sample buffer (S16 format: 2 bytes per sample per channel)
	bufferSize := config.FrameSize * config.Channels * 2
	sampleData := make([]byte, bufferSize)

	return &AudioTestPatternSource{
		config:     config,
		sampleData: sampleData,
		samplesCh:  make(chan *AudioSamples, 2),
		rngState:   uint64(time.Now().UnixNano()),
	}
}

// Start begins generating audio samples.
func (s *AudioTestPatternSource) Start(ctx context.Context) error {
	if s.running.Load() {
		return fmt.Errorf("source already running")
	}

	s.ctx, s.cancel = context.WithCancel(ctx)
	s.running.Store(true)
	s.sampleCount = 0
	s.phase = 0
	s.sweepPhase = 0

	go s.generateLoop()

	return nil
}

// Stop stops generating audio samples.
func (s *AudioTestPatternSource) Stop() error {
	if !s.running.Load() {
		return nil
	}

	s.running.Store(false)
	if s.cancel != nil {
		s.cancel()
	}

	return nil
}

// Close closes the source.
func (s *AudioTestPatternSource) Close() error {
	s.Stop()
	close(s.samplesCh)
	return nil
}

// ReadSamples reads the next audio samples (blocking).
func (s *AudioTestPatternSource) ReadSamples(ctx context.Context) (*AudioSamples, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case samples, ok := <-s.samplesCh:
		if !ok {
			return nil, fmt.Errorf("source closed")
		}
		return samples, nil
	}
}

// SetCallback sets the push-mode callback.
func (s *AudioTestPatternSource) SetCallback(cb AudioSamplesCallback) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callback = cb
}

// SampleRate returns the audio sample rate.
func (s *AudioTestPatternSource) SampleRate() int {
	return s.config.SampleRate
}

// Channels returns the number of audio channels.
func (s *AudioTestPatternSource) Channels() int {
	return s.config.Channels
}

// ReadSamplesInto reads samples into a pre-allocated buffer.
func (s *AudioTestPatternSource) ReadSamplesInto(ctx context.Context, buf *AudioSampleBuffer) error {
	samples, err := s.ReadSamples(ctx)
	if err != nil {
		return err
	}

	// Copy data into the provided buffer
	copy(buf.Data, samples.Data)
	buf.SampleRate = samples.SampleRate
	buf.Channels = samples.Channels
	buf.SampleCount = samples.SampleCount
	buf.Format = samples.Format
	buf.TimestampNs = samples.Timestamp

	return nil
}

func (s *AudioTestPatternSource) generateLoop() {
	// Calculate frame duration
	frameDuration := time.Duration(float64(s.config.FrameSize) / float64(s.config.SampleRate) * float64(time.Second))
	ticker := time.NewTicker(frameDuration)
	defer ticker.Stop()

	startTime := time.Now()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			// Generate samples
			s.generateSamples()

			// Create sample struct
			timestamp := time.Since(startTime).Nanoseconds()
			samples := &AudioSamples{
				Data:        s.sampleData,
				SampleRate:  s.config.SampleRate,
				Channels:    s.config.Channels,
				SampleCount: s.config.FrameSize,
				Format:      AudioFormatS16,
				Timestamp:   timestamp,
			}

			// Deliver via callback or channel
			s.mu.RLock()
			cb := s.callback
			s.mu.RUnlock()

			if cb != nil {
				cb(samples)
			} else {
				select {
				case s.samplesCh <- samples:
				default:
					// Drop if channel full
				}
			}

			s.sampleCount += uint64(s.config.FrameSize)
		}
	}
}

func (s *AudioTestPatternSource) generateSamples() {
	switch s.config.Pattern {
	case AudioPatternSilence:
		s.generateSilence()
	case AudioPatternSineWave:
		s.generateSineWave()
	case AudioPatternSquareWave:
		s.generateSquareWave()
	case AudioPatternWhiteNoise:
		s.generateWhiteNoise()
	case AudioPatternSweep:
		s.generateSweep()
	default:
		s.generateSilence()
	}
}

func (s *AudioTestPatternSource) generateSilence() {
	for i := range s.sampleData {
		s.sampleData[i] = 0
	}
}

func (s *AudioTestPatternSource) generateSineWave() {
	phaseIncrement := 2.0 * math.Pi * s.config.Frequency / float64(s.config.SampleRate)
	amplitude := s.config.Amplitude * 32767.0

	idx := 0
	for i := 0; i < s.config.FrameSize; i++ {
		sample := int16(amplitude * math.Sin(s.phase))
		s.phase += phaseIncrement

		// Keep phase in reasonable range
		if s.phase > 2*math.Pi {
			s.phase -= 2 * math.Pi
		}

		// Write sample for each channel (little-endian S16)
		for c := 0; c < s.config.Channels; c++ {
			s.sampleData[idx] = byte(sample & 0xFF)
			s.sampleData[idx+1] = byte((sample >> 8) & 0xFF)
			idx += 2
		}
	}
}

func (s *AudioTestPatternSource) generateSquareWave() {
	phaseIncrement := 2.0 * math.Pi * s.config.Frequency / float64(s.config.SampleRate)
	amplitude := int16(s.config.Amplitude * 32767.0)

	idx := 0
	for i := 0; i < s.config.FrameSize; i++ {
		var sample int16
		if math.Sin(s.phase) >= 0 {
			sample = amplitude
		} else {
			sample = -amplitude
		}
		s.phase += phaseIncrement

		if s.phase > 2*math.Pi {
			s.phase -= 2 * math.Pi
		}

		for c := 0; c < s.config.Channels; c++ {
			s.sampleData[idx] = byte(sample & 0xFF)
			s.sampleData[idx+1] = byte((sample >> 8) & 0xFF)
			idx += 2
		}
	}
}

func (s *AudioTestPatternSource) generateWhiteNoise() {
	amplitude := s.config.Amplitude * 32767.0

	idx := 0
	for i := 0; i < s.config.FrameSize; i++ {
		// xorshift64
		s.rngState ^= s.rngState << 13
		s.rngState ^= s.rngState >> 7
		s.rngState ^= s.rngState << 17

		// Convert to -1.0 to 1.0 range
		normalized := (float64(s.rngState)/float64(^uint64(0)))*2.0 - 1.0
		sample := int16(amplitude * normalized)

		for c := 0; c < s.config.Channels; c++ {
			s.sampleData[idx] = byte(sample & 0xFF)
			s.sampleData[idx+1] = byte((sample >> 8) & 0xFF)
			idx += 2
		}
	}
}

func (s *AudioTestPatternSource) generateSweep() {
	// Calculate current frequency in sweep
	sweepSamples := float64(s.config.SampleRate) * s.config.SweepDuration.Seconds()
	progress := math.Mod(float64(s.sampleCount), sweepSamples) / sweepSamples

	// Logarithmic frequency sweep
	logStart := math.Log(s.config.SweepStartHz)
	logEnd := math.Log(s.config.SweepEndHz)
	currentFreq := math.Exp(logStart + progress*(logEnd-logStart))

	phaseIncrement := 2.0 * math.Pi * currentFreq / float64(s.config.SampleRate)
	amplitude := s.config.Amplitude * 32767.0

	idx := 0
	for i := 0; i < s.config.FrameSize; i++ {
		sample := int16(amplitude * math.Sin(s.sweepPhase))
		s.sweepPhase += phaseIncrement

		if s.sweepPhase > 2*math.Pi {
			s.sweepPhase -= 2 * math.Pi
		}

		for c := 0; c < s.config.Channels; c++ {
			s.sampleData[idx] = byte(sample & 0xFF)
			s.sampleData[idx+1] = byte((sample >> 8) & 0xFF)
			idx += 2
		}
	}
}

// Register audio test pattern source factory
func init() {
	RegisterAudioSource(SourceTypeTestPattern, func(config interface{}) (AudioSource, error) {
		cfg, ok := config.(*AudioTestPatternConfig)
		if !ok {
			defaultCfg := DefaultAudioTestPatternConfig()
			cfg = &defaultCfg
		}
		return NewAudioTestPatternSource(*cfg), nil
	})
}
