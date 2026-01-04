package media

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// CameraConfig configures a camera video source.
type CameraConfig struct {
	DeviceID  string    // Device ID (empty for default camera)
	Width     int       // Target frame width (default: 1280)
	Height    int       // Target frame height (default: 720)
	FPS       int       // Frames per second (default: 30)
	ScaleMode ScaleMode // How to handle aspect ratio mismatch (default: ScaleModeFit)
}

// DefaultCameraConfig returns a default camera configuration.
func DefaultCameraConfig() CameraConfig {
	return CameraConfig{
		Width:     1280,
		Height:    720,
		FPS:       30,
		ScaleMode: ScaleModeFit,
	}
}

// CameraSource captures video from a camera device.
type CameraSource struct {
	config CameraConfig

	// Camera track from device provider
	track VideoTrack

	// Frame timing
	startTime  time.Time
	frameCount uint64

	// Scaler for resolution adjustment
	scaler   *VideoScaler
	scalerMu sync.Mutex

	// State
	running  atomic.Bool
	ctx      context.Context
	cancel   context.CancelFunc
	frameCh  chan *VideoFrame
	doneCh   chan struct{}
	callback VideoFrameCallback

	mu sync.RWMutex
}

// DefaultMaxCameraFPS is the fallback maximum frame rate when camera capabilities can't be queried.
const DefaultMaxCameraFPS = 30

// NewCameraSource creates a new camera video source.
func NewCameraSource(config CameraConfig) (*CameraSource, error) {
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
	// Ensure even dimensions for YUV
	config.Width = (config.Width + 1) &^ 1
	config.Height = (config.Height + 1) &^ 1

	provider := GetDeviceProvider()
	if provider == nil {
		return nil, fmt.Errorf("no device provider registered")
	}

	// Get device ID
	deviceID := config.DeviceID
	if deviceID == "" {
		// Use default camera
		devices, err := provider.ListVideoDevices(context.Background())
		if err != nil {
			return nil, fmt.Errorf("failed to list video devices: %w", err)
		}
		if len(devices) == 0 {
			return nil, fmt.Errorf("no video devices available")
		}
		deviceID = devices[0].DeviceID
	}

	// The C library now automatically clamps FPS to the device's supported range,
	// so we can pass the requested FPS directly. The library will use the max
	// supported FPS if the requested value is too high.
	track, err := provider.OpenVideoDevice(context.Background(), deviceID, &VideoConstraints{
		DeviceID:  deviceID,
		Width:     config.Width,
		Height:    config.Height,
		FrameRate: config.FPS,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open video device: %w", err)
	}

	s := &CameraSource{
		config:  config,
		track:   track,
		frameCh: make(chan *VideoFrame, 3),
	}

	return s, nil
}

// Start begins capturing frames from the camera.
func (s *CameraSource) Start(ctx context.Context) error {
	if s.running.Load() {
		return fmt.Errorf("source already running")
	}

	s.ctx, s.cancel = context.WithCancel(ctx)
	s.doneCh = make(chan struct{})
	s.running.Store(true)
	s.startTime = time.Now()
	s.frameCount = 0

	go s.captureLoop()

	return nil
}

// Stop stops capturing frames.
func (s *CameraSource) Stop() error {
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

// Close closes the camera source and releases resources.
func (s *CameraSource) Close() error {
	s.Stop()

	s.mu.Lock()
	if s.frameCh != nil {
		close(s.frameCh)
		s.frameCh = nil
	}
	s.mu.Unlock()

	if s.track != nil {
		return s.track.Close()
	}
	return nil
}

// ReadFrame reads the next frame (blocking).
func (s *CameraSource) ReadFrame(ctx context.Context) (*VideoFrame, error) {
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

// ReadFrameInto reads the next frame into a pre-allocated buffer.
func (s *CameraSource) ReadFrameInto(ctx context.Context, buf *VideoFrameBuffer) error {
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

// SetCallback sets the push-mode callback for frame delivery.
func (s *CameraSource) SetCallback(cb VideoFrameCallback) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callback = cb
}

// Config returns the source configuration.
func (s *CameraSource) Config() SourceConfig {
	return SourceConfig{
		Width:      s.config.Width,
		Height:     s.config.Height,
		FPS:        s.config.FPS,
		Format:     PixelFormatI420,
		SourceType: SourceTypeCamera,
	}
}

func (s *CameraSource) captureLoop() {
	defer close(s.doneCh)

	frameDuration := time.Second / time.Duration(s.config.FPS)
	ticker := time.NewTicker(frameDuration)
	defer ticker.Stop()

	// For FPS upscaling: keep track of the last frame to duplicate when needed
	var lastFrame *VideoFrame
	var lastFrameMu sync.Mutex
	newFrameCh := make(chan *VideoFrame, 1)

	// Start a goroutine to read frames from the camera at its native rate
	go func() {
		for s.running.Load() {
			select {
			case <-s.ctx.Done():
				return
			default:
			}

			frame, err := s.track.ReadFrame(s.ctx)
			if err != nil {
				if s.ctx.Err() != nil {
					return
				}
				continue
			}

			// Scale frame if needed
			if frame.Width != s.config.Width || frame.Height != s.config.Height {
				frame = s.scaleFrame(frame)
			}

			// Update last frame
			lastFrameMu.Lock()
			lastFrame = frame
			lastFrameMu.Unlock()

			// Try to send to channel (non-blocking)
			select {
			case newFrameCh <- frame:
			default:
				// Previous frame not consumed yet, that's OK
			}
		}
	}()

	for s.running.Load() {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			// Get the latest frame or reuse the last one
			var frame *VideoFrame
			select {
			case frame = <-newFrameCh:
				// Got a new frame from camera
			default:
				// No new frame, use the last one (FPS upscaling)
				lastFrameMu.Lock()
				if lastFrame != nil {
					// Copy the frame to avoid data races
					frame = &VideoFrame{
						Data:      lastFrame.Data,
						Stride:    lastFrame.Stride,
						Width:     lastFrame.Width,
						Height:    lastFrame.Height,
						Format:    lastFrame.Format,
						Timestamp: lastFrame.Timestamp,
						Duration:  lastFrame.Duration,
					}
				}
				lastFrameMu.Unlock()
			}

			if frame == nil {
				continue // No frame available yet
			}

			s.frameCount++

			// Update timestamp for this output frame
			frame.Timestamp = time.Since(s.startTime).Nanoseconds()
			frame.Duration = frameDuration.Nanoseconds()

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

func (s *CameraSource) scaleFrame(frame *VideoFrame) *VideoFrame {
	s.scalerMu.Lock()
	defer s.scalerMu.Unlock()

	// Create or update scaler if source dimensions changed
	if s.scaler == nil ||
		s.scaler.srcWidth != frame.Width ||
		s.scaler.srcHeight != frame.Height {
		s.scaler = NewVideoScaler(
			frame.Width, frame.Height,
			s.config.Width, s.config.Height,
			s.config.ScaleMode,
		)
	}

	return s.scaler.Scale(frame)
}

// ListCameras returns a list of available camera devices.
func ListCameras(ctx context.Context) ([]DeviceInfo, error) {
	provider := GetDeviceProvider()
	if provider == nil {
		return nil, fmt.Errorf("no device provider registered")
	}
	return provider.ListVideoDevices(ctx)
}

// Register camera source factory
func init() {
	RegisterVideoSource(SourceTypeCamera, func(config interface{}) (VideoSource, error) {
		cfg, ok := config.(*CameraConfig)
		if !ok {
			// Use default config
			defaultCfg := DefaultCameraConfig()
			cfg = &defaultCfg
		}
		return NewCameraSource(*cfg)
	})
}
