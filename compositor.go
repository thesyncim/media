package media

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// BlendMode defines how layers are blended together.
type BlendMode int32

const (
	BlendModeCopy     BlendMode = 0 // Direct copy (ignore alpha)
	BlendModeOver     BlendMode = 1 // Porter-Duff over (standard alpha blend)
	BlendModeAdd      BlendMode = 2 // Additive blending
	BlendModeMultiply BlendMode = 3 // Multiply blending
)

// CompositorLayer represents a video layer in the compositor.
type CompositorLayer struct {
	ID        int         // Unique layer ID
	Source    VideoSource // Video source for this layer
	X, Y      int         // Position on canvas
	Width     int         // Scaled width (0 = use source width)
	Height    int         // Scaled height (0 = use source height)
	ZOrder    int         // Layer order (higher = on top)
	Alpha     float32     // Layer opacity 0.0-1.0
	Visible   bool        // Layer visibility
	BlendMode BlendMode   // How to blend this layer
}

// CompositorConfig configures the video compositor.
type CompositorConfig struct {
	Width      int     // Canvas width
	Height     int     // Canvas height
	FPS        int     // Output frame rate
	Background [3]byte // Background color (Y, U, V)
}

// DefaultCompositorConfig returns a default compositor configuration.
func DefaultCompositorConfig() CompositorConfig {
	return CompositorConfig{
		Width:      1920,
		Height:     1080,
		FPS:        30,
		Background: [3]byte{16, 128, 128}, // Black in YUV
	}
}

// VideoCompositor combines multiple video sources into a single output.
// It implements the VideoSource interface, so it can be used anywhere
// a VideoSource is expected (e.g., as input to an encoder or another compositor).
type VideoCompositor struct {
	config CompositorConfig

	// Layer management
	layers   []*CompositorLayer
	layersMu sync.RWMutex
	nextID   int

	// Native compositor handle
	handle uintptr

	// Output
	running   atomic.Bool
	ctx       context.Context
	cancel    context.CancelFunc
	frameCh   chan *VideoFrame
	startTime time.Time

	mu sync.RWMutex
}

// NewVideoCompositor creates a new video compositor.
func NewVideoCompositor(config CompositorConfig) (*VideoCompositor, error) {
	if config.Width <= 0 {
		config.Width = 1920
	}
	if config.Height <= 0 {
		config.Height = 1080
	}
	if config.FPS <= 0 {
		config.FPS = 30
	}

	// Ensure even dimensions
	config.Width = (config.Width + 1) &^ 1
	config.Height = (config.Height + 1) &^ 1

	handle, err := compositorCreate(config.Width, config.Height)
	if err != nil {
		return nil, err
	}

	c := &VideoCompositor{
		config:  config,
		handle:  handle,
		layers:  make([]*CompositorLayer, 0),
		frameCh: make(chan *VideoFrame, 3),
	}

	return c, nil
}

// AddLayer adds a video source as a layer to the compositor.
// Returns the layer ID for future reference.
func (c *VideoCompositor) AddLayer(source VideoSource, x, y int) int {
	c.layersMu.Lock()
	defer c.layersMu.Unlock()

	c.nextID++
	layer := &CompositorLayer{
		ID:        c.nextID,
		Source:    source,
		X:         x,
		Y:         y,
		Width:     0, // Use source dimensions
		Height:    0,
		ZOrder:    len(c.layers),
		Alpha:     1.0,
		Visible:   true,
		BlendMode: BlendModeOver,
	}

	c.layers = append(c.layers, layer)
	return layer.ID
}

// RemoveLayer removes a layer by ID.
func (c *VideoCompositor) RemoveLayer(id int) bool {
	c.layersMu.Lock()
	defer c.layersMu.Unlock()

	for i, layer := range c.layers {
		if layer.ID == id {
			c.layers = append(c.layers[:i], c.layers[i+1:]...)
			return true
		}
	}
	return false
}

// GetLayer returns a layer by ID.
func (c *VideoCompositor) GetLayer(id int) *CompositorLayer {
	c.layersMu.RLock()
	defer c.layersMu.RUnlock()

	for _, layer := range c.layers {
		if layer.ID == id {
			return layer
		}
	}
	return nil
}

// SetLayerPosition updates the position of a layer.
func (c *VideoCompositor) SetLayerPosition(id, x, y int) {
	c.layersMu.Lock()
	defer c.layersMu.Unlock()

	for _, layer := range c.layers {
		if layer.ID == id {
			layer.X = x
			layer.Y = y
			return
		}
	}
}

// SetLayerSize updates the size of a layer.
func (c *VideoCompositor) SetLayerSize(id, width, height int) {
	c.layersMu.Lock()
	defer c.layersMu.Unlock()

	for _, layer := range c.layers {
		if layer.ID == id {
			layer.Width = width
			layer.Height = height
			return
		}
	}
}

// SetLayerAlpha updates the opacity of a layer.
func (c *VideoCompositor) SetLayerAlpha(id int, alpha float32) {
	c.layersMu.Lock()
	defer c.layersMu.Unlock()

	for _, layer := range c.layers {
		if layer.ID == id {
			layer.Alpha = alpha
			return
		}
	}
}

// SetLayerZOrder updates the z-order of a layer.
func (c *VideoCompositor) SetLayerZOrder(id, zorder int) {
	c.layersMu.Lock()
	defer c.layersMu.Unlock()

	for _, layer := range c.layers {
		if layer.ID == id {
			layer.ZOrder = zorder
			return
		}
	}

	// Re-sort layers by z-order
	c.sortLayers()
}

// SetLayerVisible updates the visibility of a layer.
func (c *VideoCompositor) SetLayerVisible(id int, visible bool) {
	c.layersMu.Lock()
	defer c.layersMu.Unlock()

	for _, layer := range c.layers {
		if layer.ID == id {
			layer.Visible = visible
			return
		}
	}
}

// SetLayerBlendMode updates the blend mode of a layer.
func (c *VideoCompositor) SetLayerBlendMode(id int, mode BlendMode) {
	c.layersMu.Lock()
	defer c.layersMu.Unlock()

	for _, layer := range c.layers {
		if layer.ID == id {
			layer.BlendMode = mode
			return
		}
	}
}

func (c *VideoCompositor) sortLayers() {
	// Simple bubble sort (layers list is typically small)
	for i := 0; i < len(c.layers)-1; i++ {
		for j := 0; j < len(c.layers)-i-1; j++ {
			if c.layers[j].ZOrder > c.layers[j+1].ZOrder {
				c.layers[j], c.layers[j+1] = c.layers[j+1], c.layers[j]
			}
		}
	}
}

// Start begins compositing frames from all layers.
func (c *VideoCompositor) Start(ctx context.Context) error {
	if c.running.Load() {
		return nil
	}

	c.ctx, c.cancel = context.WithCancel(ctx)
	c.running.Store(true)
	c.startTime = time.Now()

	// Start all layer sources
	c.layersMu.RLock()
	for _, layer := range c.layers {
		if layer.Source != nil {
			layer.Source.Start(c.ctx)
		}
	}
	c.layersMu.RUnlock()

	go c.compositeLoop()

	return nil
}

// Stop stops compositing.
func (c *VideoCompositor) Stop() error {
	if !c.running.Load() {
		return nil
	}

	c.running.Store(false)
	if c.cancel != nil {
		c.cancel()
	}

	// Stop all layer sources
	c.layersMu.RLock()
	for _, layer := range c.layers {
		if layer.Source != nil {
			layer.Source.Stop()
		}
	}
	c.layersMu.RUnlock()

	return nil
}

// Close releases all resources.
func (c *VideoCompositor) Close() error {
	c.Stop()

	c.mu.Lock()
	if c.frameCh != nil {
		close(c.frameCh)
		c.frameCh = nil
	}
	c.mu.Unlock()

	if c.handle != 0 {
		compositorDestroy(c.handle)
		c.handle = 0
	}

	return nil
}

// ReadFrame reads the next composited frame.
func (c *VideoCompositor) ReadFrame(ctx context.Context) (*VideoFrame, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case frame, ok := <-c.frameCh:
		if !ok {
			return nil, context.Canceled
		}
		return frame, nil
	}
}

// ReadFrameInto reads the next frame into a pre-allocated buffer.
func (c *VideoCompositor) ReadFrameInto(ctx context.Context, buf *VideoFrameBuffer) error {
	frame, err := c.ReadFrame(ctx)
	if err != nil {
		return err
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
func (c *VideoCompositor) SetCallback(cb VideoFrameCallback) {
	// Not implemented for compositor
}

// Config returns the compositor configuration as a SourceConfig.
func (c *VideoCompositor) Config() SourceConfig {
	return SourceConfig{
		Width:      c.config.Width,
		Height:     c.config.Height,
		FPS:        c.config.FPS,
		Format:     PixelFormatI420,
		SourceType: SourceTypeCustom,
	}
}

func (c *VideoCompositor) compositeLoop() {
	frameDuration := time.Second / time.Duration(c.config.FPS)
	ticker := time.NewTicker(frameDuration)
	defer ticker.Stop()

	// Pre-allocate frame storage for each layer
	type layerFrame struct {
		frame *VideoFrame
		layer *CompositorLayer
	}
	layerFrames := make([]layerFrame, 0, 8)

	for c.running.Load() {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			// Clear canvas
			compositorClear(c.handle, c.config.Background[0], c.config.Background[1], c.config.Background[2])

			// Collect frames from all layers
			layerFrames = layerFrames[:0]
			c.layersMu.RLock()

			for _, layer := range c.layers {
				if !layer.Visible || layer.Source == nil {
					continue
				}

				// Try to get a frame (non-blocking with short timeout)
				frameCtx, cancel := context.WithTimeout(c.ctx, frameDuration/2)
				frame, err := layer.Source.ReadFrame(frameCtx)
				cancel()

				if err == nil && frame != nil {
					layerFrames = append(layerFrames, layerFrame{frame: frame, layer: layer})
				}
			}
			c.layersMu.RUnlock()

			// Blend layers onto canvas (they're already sorted by z-order)
			for _, lf := range layerFrames {
				width := lf.layer.Width
				height := lf.layer.Height
				if width <= 0 {
					width = lf.frame.Width
				}
				if height <= 0 {
					height = lf.frame.Height
				}

				compositorBlendLayer(
					c.handle,
					lf.frame.Data[0], lf.frame.Data[1], lf.frame.Data[2],
					nil, // No alpha plane
					lf.frame.Width, lf.frame.Height,
					lf.frame.Stride[0], lf.frame.Stride[1],
					lf.layer.X, lf.layer.Y,
					width, height,
					lf.layer.ZOrder,
					lf.layer.Alpha,
					lf.layer.Visible,
					lf.layer.BlendMode,
				)
			}

			// Get the composited result
			y, u, v, strideY, strideUV := compositorGetResult(c.handle)
			if y == nil {
				continue
			}

			// Create output frame
			frame := &VideoFrame{
				Width:     c.config.Width,
				Height:    c.config.Height,
				Format:    PixelFormatI420,
				Timestamp: time.Since(c.startTime).Nanoseconds(),
				Duration:  frameDuration.Nanoseconds(),
				Data:      [][]byte{y, u, v},
				Stride:    []int{strideY, strideUV, strideUV},
			}

			// Send to channel
			select {
			case c.frameCh <- frame:
			default:
				// Drop frame if channel full
			}
		}
	}
}

// Preset layouts for common use cases

// LayoutPiP creates a picture-in-picture layout.
// main: The main (background) source
// pip: The picture-in-picture (overlay) source
// pipWidth, pipHeight: Size of the PiP window
// position: "top-left", "top-right", "bottom-left", "bottom-right"
// margin: Margin from the edge in pixels
func (c *VideoCompositor) LayoutPiP(main, pip VideoSource, pipWidth, pipHeight int, position string, margin int) (mainID, pipID int) {
	// Add main layer (full screen)
	mainID = c.AddLayer(main, 0, 0)
	c.SetLayerSize(mainID, c.config.Width, c.config.Height)

	// Calculate PiP position
	var x, y int
	switch position {
	case "top-left":
		x, y = margin, margin
	case "top-right":
		x = c.config.Width - pipWidth - margin
		y = margin
	case "bottom-left":
		x = margin
		y = c.config.Height - pipHeight - margin
	case "bottom-right":
		x = c.config.Width - pipWidth - margin
		y = c.config.Height - pipHeight - margin
	default: // default to bottom-right
		x = c.config.Width - pipWidth - margin
		y = c.config.Height - pipHeight - margin
	}

	// Add PiP layer
	pipID = c.AddLayer(pip, x, y)
	c.SetLayerSize(pipID, pipWidth, pipHeight)
	c.SetLayerZOrder(pipID, 1)

	return mainID, pipID
}

// LayoutSideBySide creates a side-by-side layout.
// left, right: The two sources
// gap: Gap between the two sources in pixels
func (c *VideoCompositor) LayoutSideBySide(left, right VideoSource, gap int) (leftID, rightID int) {
	halfWidth := (c.config.Width - gap) / 2

	leftID = c.AddLayer(left, 0, 0)
	c.SetLayerSize(leftID, halfWidth, c.config.Height)

	rightID = c.AddLayer(right, halfWidth+gap, 0)
	c.SetLayerSize(rightID, halfWidth, c.config.Height)

	return leftID, rightID
}

// LayoutGrid creates a grid layout.
// sources: Array of video sources
// cols: Number of columns (rows calculated automatically)
// gap: Gap between cells in pixels
func (c *VideoCompositor) LayoutGrid(sources []VideoSource, cols, gap int) []int {
	if cols <= 0 {
		cols = 2
	}

	rows := (len(sources) + cols - 1) / cols
	cellWidth := (c.config.Width - gap*(cols-1)) / cols
	cellHeight := (c.config.Height - gap*(rows-1)) / rows

	ids := make([]int, len(sources))
	for i, source := range sources {
		col := i % cols
		row := i / cols
		x := col * (cellWidth + gap)
		y := row * (cellHeight + gap)

		ids[i] = c.AddLayer(source, x, y)
		c.SetLayerSize(ids[i], cellWidth, cellHeight)
		c.SetLayerZOrder(ids[i], i)
	}

	return ids
}
