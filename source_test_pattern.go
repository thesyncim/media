package media

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// PatternType defines the type of test pattern to generate.
type PatternType int

const (
	PatternColorBars    PatternType = iota // SMPTE color bars
	PatternGradient                        // Horizontal gradient
	PatternCheckerboard                    // Checkerboard pattern
	PatternSolidColor                      // Solid color
	PatternNoise                           // Random noise
	PatternMovingBox                       // Moving box (animated)
)

func (p PatternType) String() string {
	switch p {
	case PatternColorBars:
		return "ColorBars"
	case PatternGradient:
		return "Gradient"
	case PatternCheckerboard:
		return "Checkerboard"
	case PatternSolidColor:
		return "SolidColor"
	case PatternNoise:
		return "Noise"
	case PatternMovingBox:
		return "MovingBox"
	default:
		return "Unknown"
	}
}

// TestPatternConfig configures a test pattern source.
type TestPatternConfig struct {
	Width    int         // Frame width (default: 1280)
	Height   int         // Frame height (default: 720)
	FPS      int         // Frames per second (default: 30)
	Pattern  PatternType // Pattern type (default: ColorBars)
	Animated bool        // Enable animation for static patterns (MovingBox/Noise always animate)

	// For SolidColor pattern
	SolidR, SolidG, SolidB uint8

	// For Checkerboard pattern
	CheckerSize int // Size of each checker square (default: 32)
}

// DefaultTestPatternConfig returns a default test pattern configuration.
func DefaultTestPatternConfig() TestPatternConfig {
	return TestPatternConfig{
		Width:       1280,
		Height:      720,
		FPS:         30,
		Pattern:     PatternColorBars,
		Animated:    false,
		CheckerSize: 32,
	}
}

// TestPatternSource generates synthetic video frames with test patterns.
type TestPatternSource struct {
	config TestPatternConfig

	// Pre-allocated frame buffer (I420 format)
	frameData []byte
	yPlane    []byte
	uPlane    []byte
	vPlane    []byte

	// Frame timing
	frameDuration time.Duration
	frameCount    uint64
	startTime     time.Time

	// State
	running  atomic.Bool
	ctx      context.Context
	cancel   context.CancelFunc
	frameCh  chan *VideoFrame
	doneCh   chan struct{}
	callback VideoFrameCallback

	// Random state for noise pattern
	rngState uint64

	mu sync.RWMutex
}

// NewTestPatternSource creates a new test pattern video source.
func NewTestPatternSource(config TestPatternConfig) *TestPatternSource {
	// Apply defaults
	if config.Width <= 0 {
		config.Width = 1280
	}
	if config.Height <= 0 {
		config.Height = 720
	}
	if config.FPS <= 0 {
		config.FPS = 30
	}
	if config.CheckerSize <= 0 {
		config.CheckerSize = 32
	}

	// Calculate plane sizes for I420
	ySize := config.Width * config.Height
	uvWidth := config.Width / 2
	uvHeight := config.Height / 2
	uvSize := uvWidth * uvHeight
	totalSize := ySize + uvSize*2

	// Allocate frame buffer
	frameData := make([]byte, totalSize)

	s := &TestPatternSource{
		config:        config,
		frameData:     frameData,
		yPlane:        frameData[:ySize],
		uPlane:        frameData[ySize : ySize+uvSize],
		vPlane:        frameData[ySize+uvSize:],
		frameDuration: time.Second / time.Duration(config.FPS),
		frameCh:       make(chan *VideoFrame, 2),
		rngState:      uint64(time.Now().UnixNano()),
	}

	// Generate initial pattern
	s.generatePattern(0)

	return s
}

// Start begins generating frames.
func (s *TestPatternSource) Start(ctx context.Context) error {
	if s.running.Load() {
		return fmt.Errorf("source already running")
	}

	s.ctx, s.cancel = context.WithCancel(ctx)
	s.doneCh = make(chan struct{})
	s.running.Store(true)
	s.startTime = time.Now()
	s.frameCount = 0

	go s.generateLoop()

	return nil
}

// Stop stops generating frames and waits for the goroutine to exit.
func (s *TestPatternSource) Stop() error {
	if !s.running.Load() {
		return nil
	}

	s.running.Store(false)
	if s.cancel != nil {
		s.cancel()
	}

	// Wait for goroutine to finish
	if s.doneCh != nil {
		<-s.doneCh
	}

	return nil
}

// Close closes the source.
func (s *TestPatternSource) Close() error {
	s.Stop()
	s.mu.Lock()
	if s.frameCh != nil {
		close(s.frameCh)
		s.frameCh = nil
	}
	s.mu.Unlock()
	return nil
}

// ReadFrame reads the next frame (blocking).
func (s *TestPatternSource) ReadFrame(ctx context.Context) (*VideoFrame, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case frame, ok := <-s.frameCh:
		if !ok {
			return nil, fmt.Errorf("source closed")
		}
		return frame, nil
	}
}

// SetCallback sets the push-mode callback.
func (s *TestPatternSource) SetCallback(cb VideoFrameCallback) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callback = cb
}

// Config returns the source configuration.
func (s *TestPatternSource) Config() SourceConfig {
	return SourceConfig{
		Width:      s.config.Width,
		Height:     s.config.Height,
		FPS:        s.config.FPS,
		Format:     PixelFormatI420,
		SourceType: SourceTypeTestPattern,
	}
}

// ReadFrameInto reads the next frame into a pre-allocated buffer.
func (s *TestPatternSource) ReadFrameInto(ctx context.Context, buf *VideoFrameBuffer) error {
	frame, err := s.ReadFrame(ctx)
	if err != nil {
		return err
	}

	// Copy data into the provided buffer
	if buf.Format != PixelFormatI420 {
		return fmt.Errorf("buffer format mismatch: expected I420, got %v", buf.Format)
	}

	copy(buf.Y, frame.Data[0])
	copy(buf.U, frame.Data[1])
	copy(buf.V, frame.Data[2])

	buf.Width = frame.Width
	buf.Height = frame.Height
	buf.StrideY = frame.Stride[0]
	buf.StrideU = frame.Stride[1]
	buf.StrideV = frame.Stride[2]
	buf.TimestampNs = frame.Timestamp
	buf.DurationNs = frame.Duration

	return nil
}

func (s *TestPatternSource) generateLoop() {
	defer close(s.doneCh)

	ticker := time.NewTicker(s.frameDuration)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.frameCount++

			// Generate pattern (with animation if enabled)
			if s.config.Animated || s.config.Pattern == PatternMovingBox || s.config.Pattern == PatternNoise {
				s.generatePattern(s.frameCount)
			}

			// Create frame
			timestamp := time.Since(s.startTime).Nanoseconds()
			frame := &VideoFrame{
				Data: [][]byte{s.yPlane, s.uPlane, s.vPlane},
				Stride: []int{
					s.config.Width,
					s.config.Width / 2,
					s.config.Width / 2,
				},
				Width:     s.config.Width,
				Height:    s.config.Height,
				Format:    PixelFormatI420,
				Timestamp: timestamp,
				Duration:  s.frameDuration.Nanoseconds(),
			}

			// Deliver via callback or channel
			s.mu.RLock()
			cb := s.callback
			s.mu.RUnlock()

			if cb != nil {
				cb(frame)
			} else {
				select {
				case <-s.ctx.Done():
					return
				case s.frameCh <- frame:
				default:
					// Drop frame if channel full
				}
			}
		}
	}
}

func (s *TestPatternSource) generatePattern(frameNum uint64) {
	switch s.config.Pattern {
	case PatternColorBars:
		s.generateColorBars()
	case PatternGradient:
		s.generateGradient()
	case PatternCheckerboard:
		s.generateCheckerboard()
	case PatternSolidColor:
		s.generateSolidColor(s.config.SolidR, s.config.SolidG, s.config.SolidB)
	case PatternNoise:
		s.generateNoise()
	case PatternMovingBox:
		s.generateMovingBox(frameNum)
	default:
		s.generateColorBars()
	}
}

// SMPTE color bars (simplified 8-bar pattern)
var colorBarsRGB = [][3]uint8{
	{192, 192, 192}, // White (75%)
	{192, 192, 0},   // Yellow
	{0, 192, 192},   // Cyan
	{0, 192, 0},     // Green
	{192, 0, 192},   // Magenta
	{192, 0, 0},     // Red
	{0, 0, 192},     // Blue
	{16, 16, 16},    // Black
}

func (s *TestPatternSource) generateColorBars() {
	w, h := s.config.Width, s.config.Height
	barWidth := w / 8

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			barIdx := x / barWidth
			if barIdx >= 8 {
				barIdx = 7
			}

			rgb := colorBarsRGB[barIdx]
			yVal, u, v := rgbToYUV(rgb[0], rgb[1], rgb[2])

			// Y plane
			s.yPlane[y*w+x] = yVal

			// UV planes (subsampled 2x2)
			if x%2 == 0 && y%2 == 0 {
				uvIdx := (y/2)*(w/2) + (x / 2)
				s.uPlane[uvIdx] = u
				s.vPlane[uvIdx] = v
			}
		}
	}
}

func (s *TestPatternSource) generateGradient() {
	w, h := s.config.Width, s.config.Height

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// Horizontal gradient from black to white
			yVal := uint8((x * 255) / w)

			s.yPlane[y*w+x] = yVal

			if x%2 == 0 && y%2 == 0 {
				uvIdx := (y/2)*(w/2) + (x / 2)
				s.uPlane[uvIdx] = 128 // Neutral
				s.vPlane[uvIdx] = 128
			}
		}
	}
}

func (s *TestPatternSource) generateCheckerboard() {
	w, h := s.config.Width, s.config.Height
	size := s.config.CheckerSize

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// Alternate black/white based on position
			isWhite := ((x/size)+(y/size))%2 == 0
			var yVal uint8
			if isWhite {
				yVal = 235 // White
			} else {
				yVal = 16 // Black
			}

			s.yPlane[y*w+x] = yVal

			if x%2 == 0 && y%2 == 0 {
				uvIdx := (y/2)*(w/2) + (x / 2)
				s.uPlane[uvIdx] = 128
				s.vPlane[uvIdx] = 128
			}
		}
	}
}

func (s *TestPatternSource) generateSolidColor(r, g, b uint8) {
	w, h := s.config.Width, s.config.Height
	yVal, u, v := rgbToYUV(r, g, b)

	// Fill Y plane
	for i := range s.yPlane {
		s.yPlane[i] = yVal
	}

	// Fill UV planes
	for i := range s.uPlane {
		s.uPlane[i] = u
		s.vPlane[i] = v
	}

	_ = w
	_ = h
}

func (s *TestPatternSource) generateNoise() {
	// Simple xorshift64 PRNG for fast noise
	for i := range s.yPlane {
		s.rngState ^= s.rngState << 13
		s.rngState ^= s.rngState >> 7
		s.rngState ^= s.rngState << 17
		s.yPlane[i] = uint8(s.rngState)
	}

	// Neutral chroma for grayscale noise
	for i := range s.uPlane {
		s.uPlane[i] = 128
		s.vPlane[i] = 128
	}
}

func (s *TestPatternSource) generateMovingBox(frameNum uint64) {
	w, h := s.config.Width, s.config.Height

	// Clear to black
	for i := range s.yPlane {
		s.yPlane[i] = 16
	}
	for i := range s.uPlane {
		s.uPlane[i] = 128
		s.vPlane[i] = 128
	}

	// Calculate box position (moves in a circle)
	boxSize := 100
	centerX := w / 2
	centerY := h / 2
	radius := float64(min(w, h)) / 4

	angle := float64(frameNum) * 0.05 // Radians per frame
	boxX := centerX + int(radius*math.Cos(angle)) - boxSize/2
	boxY := centerY + int(radius*math.Sin(angle)) - boxSize/2

	// Draw white box
	for y := boxY; y < boxY+boxSize && y < h; y++ {
		if y < 0 {
			continue
		}
		for x := boxX; x < boxX+boxSize && x < w; x++ {
			if x < 0 {
				continue
			}
			s.yPlane[y*w+x] = 235

			if x%2 == 0 && y%2 == 0 {
				uvIdx := (y/2)*(w/2) + (x / 2)
				if uvIdx < len(s.uPlane) {
					s.uPlane[uvIdx] = 128
					s.vPlane[uvIdx] = 128
				}
			}
		}
	}
}

// rgbToYUV converts RGB to YUV (BT.601)
func rgbToYUV(r, g, b uint8) (y, u, v uint8) {
	// BT.601 conversion
	yf := 16.0 + 65.481*float64(r)/255.0 + 128.553*float64(g)/255.0 + 24.966*float64(b)/255.0
	uf := 128.0 - 37.797*float64(r)/255.0 - 74.203*float64(g)/255.0 + 112.0*float64(b)/255.0
	vf := 128.0 + 112.0*float64(r)/255.0 - 93.786*float64(g)/255.0 - 18.214*float64(b)/255.0

	y = uint8(clamp(yf, 16, 235))
	u = uint8(clamp(uf, 16, 240))
	v = uint8(clamp(vf, 16, 240))
	return
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Register test pattern source factory
func init() {
	RegisterVideoSource(SourceTypeTestPattern, func(config interface{}) (VideoSource, error) {
		cfg, ok := config.(*TestPatternConfig)
		if !ok {
			// Use default config
			defaultCfg := DefaultTestPatternConfig()
			cfg = &defaultCfg
		}
		return NewTestPatternSource(*cfg), nil
	})
}
