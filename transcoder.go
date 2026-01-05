package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// Single-Output Transcoder
// =============================================================================

// Transcoder transcodes video from one codec to another.
type Transcoder struct {
	decoder VideoDecoder
	encoder VideoEncoder
	scaler  *VideoScaler // for dimension mismatch

	inputCodec  VideoCodec
	outputCodec VideoCodec

	config TranscoderConfig
	mu     sync.Mutex
}

// TranscoderConfig configures a transcoder.
type TranscoderConfig struct {
	// Input settings (auto-detected if zero)
	InputCodec VideoCodec

	// Output settings
	OutputCodec VideoCodec
	Width       int
	Height      int
	BitrateBps  int
	FPS         int

	// Optional: specific encoder/decoder configs
	DecoderThreads int
	EncoderThreads int
}

// NewTranscoder creates a new video transcoder.
// If InputCodec is VideoCodecUnknown, it will auto-detect from first frame.
func NewTranscoder(config TranscoderConfig) (*Transcoder, error) {
	if config.OutputCodec == VideoCodecUnknown {
		return nil, errors.New("output codec must be specified")
	}

	// Set defaults
	if config.Width == 0 {
		config.Width = 1920
	}
	if config.Height == 0 {
		config.Height = 1080
	}
	if config.BitrateBps == 0 {
		config.BitrateBps = 2_000_000
	}
	if config.FPS == 0 {
		config.FPS = 30
	}

	t := &Transcoder{
		inputCodec:  config.InputCodec,
		outputCodec: config.OutputCodec,
		config:      config,
	}

	// Create encoder
	encoderConfig := VideoEncoderConfig{
		Width:      config.Width,
		Height:     config.Height,
		BitrateBps: config.BitrateBps,
		FPS:        config.FPS,
		Threads:    config.EncoderThreads,
	}

	var err error
	switch config.OutputCodec {
	case VideoCodecVP8:
		t.encoder, err = NewVP8Encoder(encoderConfig)
	case VideoCodecVP9:
		t.encoder, err = NewVP9Encoder(encoderConfig)
	case VideoCodecH264:
		t.encoder, err = NewH264Encoder(encoderConfig)
	case VideoCodecAV1:
		t.encoder, err = NewAV1Encoder(encoderConfig)
	default:
		return nil, errors.New("unsupported output codec")
	}
	if err != nil {
		return nil, err
	}

	// Create decoder if input codec is known
	if config.InputCodec != VideoCodecUnknown {
		t.decoder, err = t.createDecoder(config.InputCodec)
		if err != nil {
			t.encoder.Close()
			return nil, err
		}
	}

	return t, nil
}

func (t *Transcoder) createDecoder(codec VideoCodec) (VideoDecoder, error) {
	decoderConfig := VideoDecoderConfig{
		Codec:   codec,
		Threads: t.config.DecoderThreads,
	}
	return NewVideoDecoder(decoderConfig)
}

// Transcode transcodes an encoded frame from input codec to output codec.
// On first call, auto-detects input codec if not specified.
func (t *Transcoder) Transcode(input *EncodedFrame) (*EncodedFrame, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Auto-detect input codec if needed
	if t.decoder == nil {
		codec := t.inputCodec
		if codec == VideoCodecUnknown {
			codec = DetectVideoCodec(input.Data)
			if codec == VideoCodecUnknown {
				return nil, errors.New("cannot detect input codec")
			}
			t.inputCodec = codec
		}

		var err error
		t.decoder, err = t.createDecoder(codec)
		if err != nil {
			return nil, err
		}
	}

	// Decode
	rawFrame, err := t.decoder.Decode(input)
	if err != nil {
		return nil, err
	}
	if rawFrame == nil {
		return nil, nil // Decoder buffering
	}

	// Validate decoded frame has data
	if len(rawFrame.Data) < 3 || len(rawFrame.Data[0]) == 0 || len(rawFrame.Data[1]) == 0 || len(rawFrame.Data[2]) == 0 {
		return nil, nil // Invalid frame data, skip
	}

	// Scale if dimensions don't match encoder's expected dimensions
	frameToEncode := rawFrame
	if rawFrame.Width != t.config.Width || rawFrame.Height != t.config.Height {
		// Create or update scaler
		if t.scaler == nil ||
			t.scaler.srcWidth != rawFrame.Width ||
			t.scaler.srcHeight != rawFrame.Height ||
			t.scaler.dstWidth != t.config.Width ||
			t.scaler.dstHeight != t.config.Height {
			t.scaler = NewVideoScaler(
				rawFrame.Width, rawFrame.Height,
				t.config.Width, t.config.Height,
				ScaleModeStretch,
			)
		}
		frameToEncode = t.scaler.Scale(rawFrame)
	}

	// Encode
	encoded, err := t.encoder.Encode(frameToEncode)
	if err != nil {
		return nil, err
	}
	if encoded != nil {
		// Propagate source timestamp to encoded output
		encoded.Timestamp = input.Timestamp
	}
	return encoded, nil
}

// TranscodeRaw encodes a raw frame (skips decode step).
// Note: For raw frames, the timestamp comes from the encoder's internal PTS
// since there's no source EncodedFrame to propagate from.
func (t *Transcoder) TranscodeRaw(frame *VideoFrame) (*EncodedFrame, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.encoder.Encode(frame)
}

// RequestKeyframe requests the encoder to produce a keyframe.
func (t *Transcoder) RequestKeyframe() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.encoder != nil {
		t.encoder.RequestKeyframe()
	}
}

// InputCodec returns the input codec (auto-detected or configured).
func (t *Transcoder) InputCodec() VideoCodec {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.inputCodec
}

// OutputCodec returns the output codec.
func (t *Transcoder) OutputCodec() VideoCodec {
	return t.outputCodec
}

// Close releases all resources.
func (t *Transcoder) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	var errs []error
	if t.decoder != nil {
		if err := t.decoder.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if t.encoder != nil {
		if err := t.encoder.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// =============================================================================
// Passthrough (No Transcoding)
// =============================================================================

// Passthrough wraps an encoded stream without transcoding.
// Useful when input and output codecs match.
type Passthrough struct {
	codec VideoCodec
}

// NewPassthrough creates a passthrough for the given codec.
func NewPassthrough(codec VideoCodec) *Passthrough {
	return &Passthrough{codec: codec}
}

// Process returns the frame unchanged.
func (p *Passthrough) Process(frame *EncodedFrame) (*EncodedFrame, error) {
	return frame, nil
}

// Codec returns the codec.
func (p *Passthrough) Codec() VideoCodec {
	return p.codec
}

// OutputCodec returns the output codec (same as input for passthrough).
func (p *Passthrough) OutputCodec() VideoCodec {
	return p.codec
}

// Close is a no-op for passthrough.
func (p *Passthrough) Close() error {
	return nil
}

// FrameProcessor is the common interface for Transcoder and Passthrough.
type FrameProcessor interface {
	io.Closer
	OutputCodec() VideoCodec
}

// =============================================================================
// Multi-Output Transcoding
// =============================================================================

// OutputConfig describes a single output stream configuration.
// Use this to configure simulcast, SVC, or multi-codec output.
type OutputConfig struct {
	// ID uniquely identifies this variant (e.g., "720p-vp8", "1080p-h264-svc")
	ID string

	// Codec for this output variant
	Codec VideoCodec

	// Resolution (0 = same as input)
	Width  int
	Height int

	// Bitrate in bits per second
	BitrateBps int

	// Frames per second (0 = same as input)
	FPS int

	// SVC settings (optional, codec must support it)
	// VP8/VP9/AV1 support temporal layers; VP9/AV1 support spatial layers
	TemporalLayers int // 1-4 temporal layers (1 = no temporal SVC)
	SpatialLayers  int // 1-3 spatial layers (1 = no spatial SVC)

	// Quality hint (codec-specific, 0-63 for VP8/VP9)
	Quality int

	// Passthrough skips decode+encode when input codec and resolution match.
	// Use this when you want to forward the original stream without re-encoding.
	// Note: Bitrate settings are ignored when passthrough is active.
	Passthrough bool
}

// SimulcastPreset creates a common simulcast configuration.
// Returns variants for common resolutions: 1080p, 720p, 360p.
func SimulcastPreset(codec VideoCodec, baseBitrate int) []OutputConfig {
	return []OutputConfig{
		{ID: "high", Codec: codec, Width: 1920, Height: 1080, BitrateBps: baseBitrate},
		{ID: "medium", Codec: codec, Width: 1280, Height: 720, BitrateBps: baseBitrate / 2},
		{ID: "low", Codec: codec, Width: 640, Height: 360, BitrateBps: baseBitrate / 4},
	}
}

// SVCPreset creates an SVC configuration with temporal layers.
func SVCPreset(codec VideoCodec, width, height, bitrate, temporalLayers int) []OutputConfig {
	return []OutputConfig{
		{
			ID:             "svc",
			Codec:          codec,
			Width:          width,
			Height:         height,
			BitrateBps:     bitrate,
			TemporalLayers: temporalLayers,
		},
	}
}

// MultiCodecPreset creates variants for multiple codecs at the same resolution.
func MultiCodecPreset(width, height, bitrate int, codecs ...VideoCodec) []OutputConfig {
	variants := make([]OutputConfig, len(codecs))
	for i, codec := range codecs {
		variants[i] = OutputConfig{
			ID:         codec.String(),
			Codec:      codec,
			Width:      width,
			Height:     height,
			BitrateBps: bitrate,
		}
	}
	return variants
}

// VariantFrame holds a single variant's encoded output.
type VariantFrame struct {
	VariantID string
	Frame     *EncodedFrame
	Codec     VideoCodec
}

// TranscodeResult contains all output variants from a single transcode operation.
type TranscodeResult struct {
	Variants []VariantFrame
}

// Get returns the output for a specific variant ID, or nil if not found.
func (r *TranscodeResult) Get(variantID string) *EncodedFrame {
	for _, out := range r.Variants {
		if out.VariantID == variantID {
			return out.Frame
		}
	}
	return nil
}

// outputPipeline holds encoder and optional scaler for a variant.
type outputPipeline struct {
	variant     OutputConfig
	encoder     VideoEncoder // nil if passthrough
	scaler      *VideoScaler
	passthrough bool  // if true, forward input without encode
	removed     int32 // atomic: 1 if marked for removal (encoder may still be in use)
}

// KeyframeRequestFunc is called when the transcoder needs a keyframe from the source.
// This is typically used to send PLI (Picture Loss Indication) to the video source.
type KeyframeRequestFunc func() error

// MultiTranscoder transcodes video to multiple output variants simultaneously.
// Supports simulcast (multiple resolutions), SVC (layered output), and multi-codec.
// Also implements VideoSource interface for pipeline integration.
type MultiTranscoder struct {
	decoder    VideoDecoder
	pipelines  map[string]*outputPipeline
	inputCodec VideoCodec

	inputWidth  int
	inputHeight int
	inputFPS    int

	// Source timestamp propagation: encoded outputs use the source timestamp
	// rather than each encoder's internal PTS counter
	sourceTimestamp uint32

	// VideoSource implementation
	source        VideoSource
	sourceConfig  SourceConfig
	running       int32 // atomic: 0=stopped, 1=running
	cancel        context.CancelFunc
	frameCh       chan *VideoFrame
	frameCallback VideoFrameCallback

	// Encoding timeout to prevent hangs
	encodeTimeout time.Duration

	// Concurrency control
	inFlightEncodes int32 // atomic: tracks goroutines currently encoding

	// Auto-recovery: callback to request keyframe from source
	onKeyframeNeeded    KeyframeRequestFunc
	needsKeyframe       int32 // atomic: 1 if waiting for keyframe
	consecutiveErrors   int32 // atomic: count of consecutive decode errors
	lastKeyframeRequest int64 // atomic: unix nano of last keyframe request
	minKeyframeInterval int64 // minimum interval between keyframe requests in nanoseconds

	mu sync.RWMutex // RWMutex allows concurrent reads (RequestKeyframe, etc)

	frameCount int // debug counter
}

// MultiTranscoderConfig configures a multi-output transcoder.
type MultiTranscoderConfig struct {
	// Input settings (auto-detected if InputCodec is unknown)
	InputCodec  VideoCodec
	InputWidth  int // Hint for scaler setup (0 = detect from first frame)
	InputHeight int
	InputFPS    int

	// Output variants - each produces an independent encoded stream
	Outputs []OutputConfig

	// Optional: decoder thread count
	DecoderThreads int

	// EncodeTimeout is the maximum time to wait for all encoders.
	// Default is 500ms. Set to 0 to wait forever (not recommended).
	EncodeTimeout time.Duration

	// OnKeyframeNeeded is called when the decoder needs a keyframe from the source.
	// Set this to send PLI/FIR to your video source (e.g., WebRTC peer connection).
	// If not set, the transcoder will wait for a keyframe but cannot request one.
	OnKeyframeNeeded KeyframeRequestFunc

	// MinKeyframeInterval is the minimum time between keyframe requests.
	// This prevents flooding the source with PLI requests during recovery.
	// Default is 1 second. Set to 0 to use the default.
	MinKeyframeInterval time.Duration
}

// NewMultiTranscoder creates a transcoder that outputs multiple variants.
// The decoder is shared across all output variants for efficiency.
func NewMultiTranscoder(config MultiTranscoderConfig) (*MultiTranscoder, error) {
	if len(config.Outputs) == 0 {
		return nil, errors.New("at least one output variant required")
	}

	// Validate unique IDs
	seen := make(map[string]bool)
	for _, out := range config.Outputs {
		if out.ID == "" {
			return nil, errors.New("output variant ID cannot be empty")
		}
		if seen[out.ID] {
			return nil, errors.New("duplicate output variant ID: " + out.ID)
		}
		seen[out.ID] = true
		if out.Codec == VideoCodecUnknown {
			return nil, errors.New("output codec must be specified for variant: " + out.ID)
		}
	}

	encodeTimeout := config.EncodeTimeout
	if encodeTimeout == 0 {
		encodeTimeout = 500 * time.Millisecond // Default timeout
	}

	minKeyframeInterval := config.MinKeyframeInterval
	if minKeyframeInterval == 0 {
		minKeyframeInterval = 1 * time.Second // Default: max 1 PLI per second
	}

	mt := &MultiTranscoder{
		inputCodec:          config.InputCodec,
		inputWidth:          config.InputWidth,
		inputHeight:         config.InputHeight,
		inputFPS:            config.InputFPS,
		pipelines:           make(map[string]*outputPipeline),
		encodeTimeout:       encodeTimeout,
		onKeyframeNeeded:    config.OnKeyframeNeeded,
		minKeyframeInterval: minKeyframeInterval.Nanoseconds(),
	}

	// Create encoders for each variant (skip for passthrough)
	for _, variant := range config.Outputs {
		pipeline := &outputPipeline{variant: variant, passthrough: variant.Passthrough}

		// For passthrough variants, no encoder needed
		if variant.Passthrough {
			mt.pipelines[variant.ID] = pipeline
			continue
		}

		// Determine output dimensions
		w, h := variant.Width, variant.Height
		if w == 0 {
			w = config.InputWidth
		}
		if h == 0 {
			h = config.InputHeight
		}
		if w == 0 {
			w = 1920 // Default
		}
		if h == 0 {
			h = 1080
		}

		fps := variant.FPS
		if fps == 0 {
			fps = config.InputFPS
		}
		if fps == 0 {
			fps = 30
		}

		bitrate := variant.BitrateBps
		if bitrate == 0 {
			bitrate = 2_000_000
		}

		encoderConfig := VideoEncoderConfig{
			Codec:          variant.Codec,
			Width:          w,
			Height:         h,
			FPS:            fps,
			BitrateBps:     bitrate,
			Quality:        variant.Quality,
			TemporalLayers: variant.TemporalLayers,
			SpatialLayers:  variant.SpatialLayers,
		}

		encoder, err := NewVideoEncoder(encoderConfig)
		if err != nil {
			// Clean up already created encoders
			for _, p := range mt.pipelines {
				if p.encoder != nil {
					p.encoder.Close()
				}
			}
			return nil, fmt.Errorf("failed to create encoder for %s: %w", variant.ID, err)
		}
		pipeline.encoder = encoder
		mt.pipelines[variant.ID] = pipeline
	}

	// Create decoder if input codec is known
	if config.InputCodec != VideoCodecUnknown {
		decoder, err := NewVideoDecoder(VideoDecoderConfig{
			Codec:   config.InputCodec,
			Threads: config.DecoderThreads,
		})
		if err != nil {
			mt.Close()
			return nil, fmt.Errorf("failed to create decoder: %w", err)
		}
		mt.decoder = decoder
	}

	return mt, nil
}

// Transcode decodes the input and encodes to all configured output variants.
// Auto-detects input codec on first call if not specified.
// Returns nil results for variants where encoding produced no output (buffering).
// Passthrough variants skip decode+encode when input codec matches.
func (mt *MultiTranscoder) Transcode(input *EncodedFrame) (*TranscodeResult, error) {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	// Store source timestamp for propagation to encoded outputs
	// This ensures transcoded variants use the same timestamp as the source
	// rather than each encoder's internal PTS counter
	mt.sourceTimestamp = input.Timestamp

	// Auto-detect input codec if needed
	if mt.inputCodec == VideoCodecUnknown {
		codec := DetectVideoCodec(input.Data)
		if codec == VideoCodecUnknown {
			return nil, errors.New("cannot detect input codec")
		}
		mt.inputCodec = codec
	}

	result := &TranscodeResult{
		Variants: make([]VariantFrame, 0, len(mt.pipelines)),
	}

	// Check if we have any passthrough variants that match
	hasPassthrough := false
	needsDecode := false
	for _, pipeline := range mt.pipelines {
		if pipeline.passthrough && pipeline.variant.Codec == mt.inputCodec {
			hasPassthrough = true
		} else if !pipeline.passthrough {
			needsDecode = true
		}
	}

	// Handle passthrough variants (no decode/encode needed)
	if hasPassthrough {
		for id, pipeline := range mt.pipelines {
			if pipeline.passthrough && pipeline.variant.Codec == mt.inputCodec {
				// Forward input directly - zero CPU cost
				result.Variants = append(result.Variants, VariantFrame{
					VariantID: id,
					Frame:     input,
					Codec:     pipeline.variant.Codec,
				})
			}
		}
	}

	// If no variants need decoding, we're done
	if !needsDecode {
		return result, nil
	}

	// Create decoder on first use
	if mt.decoder == nil {
		decoder, err := NewVideoDecoder(VideoDecoderConfig{Codec: mt.inputCodec})
		if err != nil {
			return nil, fmt.Errorf("failed to create decoder: %w", err)
		}
		mt.decoder = decoder
	}

	// Auto-recovery: if waiting for keyframe, skip non-keyframes
	if atomic.LoadInt32(&mt.needsKeyframe) == 1 {
		if input.FrameType != FrameTypeKey {
			// Still waiting for keyframe - return passthrough only
			if len(result.Variants) > 0 {
				return result, nil
			}
			return nil, nil
		}
		// Got keyframe! Reset recovery state
		atomic.StoreInt32(&mt.needsKeyframe, 0)
		atomic.StoreInt32(&mt.consecutiveErrors, 0)
	}

	// Decode
	rawFrame, err := mt.decoder.Decode(input)
	if err != nil {
		// Track consecutive errors for auto-recovery
		errCount := atomic.AddInt32(&mt.consecutiveErrors, 1)

		// After 3 consecutive errors, request keyframe from source (rate-limited)
		if errCount >= 3 {
			atomic.StoreInt32(&mt.needsKeyframe, 1)
			mt.requestKeyframeFromSource()
		}

		return nil, fmt.Errorf("decode failed: %w", err)
	}
	if rawFrame == nil {
		// Decoder buffering - return any passthrough results we have
		if len(result.Variants) > 0 {
			return result, nil
		}
		return nil, nil
	}

	// Validate decoded frame has data (all 3 planes for I420)
	if len(rawFrame.Data) < 3 || len(rawFrame.Data[0]) == 0 || len(rawFrame.Data[1]) == 0 || len(rawFrame.Data[2]) == 0 {
		// Decoder returned invalid frame - return passthrough only
		if len(result.Variants) > 0 {
			return result, nil
		}
		return nil, nil
	}

	// Success! Reset error counter
	atomic.StoreInt32(&mt.consecutiveErrors, 0)

	// Update input dimensions from decoded frame
	if mt.inputWidth == 0 {
		mt.inputWidth = rawFrame.Width
		mt.inputHeight = rawFrame.Height
	}

	// Encode non-passthrough variants (runs in parallel for multiple outputs)
	encodeResult, err := mt.transcodeRawNonPassthrough(rawFrame)
	if err != nil {
		return nil, err
	}

	// Debug: log variant count periodically
	mt.frameCount++
	if mt.frameCount%100 == 0 {
		fmt.Printf("[DEBUG] Transcode frame %d: passthrough=%d, encoded=%d, total pipelines=%d\n",
			mt.frameCount, len(result.Variants), len(encodeResult.Variants), len(mt.pipelines))
	}

	// Merge encode results with passthrough results
	result.Variants = append(result.Variants, encodeResult.Variants...)
	return result, nil
}

// TranscodeRaw encodes a raw frame to all output variants (skips decode step).
// Useful when input is already decoded or from a capture source.
func (mt *MultiTranscoder) TranscodeRaw(frame *VideoFrame) (*TranscodeResult, error) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	return mt.transcodeRaw(frame)
}

func (mt *MultiTranscoder) transcodeRaw(frame *VideoFrame) (*TranscodeResult, error) {
	return mt.transcodeRawNonPassthrough(frame)
}

// transcodeRawNonPassthrough encodes raw frame to non-passthrough variants only.
func (mt *MultiTranscoder) transcodeRawNonPassthrough(frame *VideoFrame) (*TranscodeResult, error) {
	// Count non-passthrough pipelines (excluding removed ones)
	var nonPassthrough []*outputPipeline
	var ids []string
	for id, pipeline := range mt.pipelines {
		// Skip removed or passthrough pipelines
		if atomic.LoadInt32(&pipeline.removed) == 1 {
			continue
		}
		if !pipeline.passthrough && pipeline.encoder != nil {
			nonPassthrough = append(nonPassthrough, pipeline)
			ids = append(ids, id)
		}
	}

	if len(nonPassthrough) == 0 {
		return &TranscodeResult{}, nil
	}

	// For single output, encode directly without goroutine overhead
	if len(nonPassthrough) == 1 {
		return mt.transcodeRawSinglePipeline(frame, ids[0], nonPassthrough[0])
	}

	// For multiple outputs, encode in parallel for better performance
	return mt.transcodeRawParallelPipelines(frame, ids, nonPassthrough)
}

// transcodeRawSinglePipeline handles single pipeline without goroutine overhead.
func (mt *MultiTranscoder) transcodeRawSinglePipeline(frame *VideoFrame, id string, pipeline *outputPipeline) (*TranscodeResult, error) {
	result := &TranscodeResult{
		Variants: make([]VariantFrame, 0, 1),
	}

	inputFrame := mt.prepareInputFrame(frame, pipeline)
	encoded, err := pipeline.encoder.Encode(inputFrame)
	if err != nil {
		return nil, fmt.Errorf("encode error for %s: %w", id, err)
	}

	if encoded != nil {
		// Propagate source timestamp to encoded output
		// This ensures all variants have consistent timestamps from the source
		encoded.Timestamp = mt.sourceTimestamp
		result.Variants = append(result.Variants, VariantFrame{
			VariantID: id,
			Frame:     encoded,
			Codec:     pipeline.variant.Codec,
		})
	}

	return result, nil
}

// transcodeRawParallelPipelines encodes to multiple pipelines concurrently.
func (mt *MultiTranscoder) transcodeRawParallelPipelines(frame *VideoFrame, ids []string, pipelines []*outputPipeline) (*TranscodeResult, error) {
	numPipelines := len(pipelines)

	// Capture source timestamp before launching goroutines
	// This ensures all encoded frames get the same timestamp
	sourceTs := mt.sourceTimestamp

	// Check if too many encodes are already in flight (prevents goroutine accumulation)
	const maxInFlight = 100 // Max concurrent encoder goroutines
	inFlight := atomic.LoadInt32(&mt.inFlightEncodes)
	if inFlight > maxInFlight {
		return nil, errors.New("too many in-flight encodes, dropping frame")
	}

	type encodeResult struct {
		id    string
		frame *EncodedFrame
		codec VideoCodec
		err   error
	}

	// Pre-scale all frames BEFORE launching goroutines to avoid race conditions
	scaledFrames := make([]*VideoFrame, numPipelines)
	for i, p := range pipelines {
		scaledFrames[i] = mt.prepareInputFrame(frame, p)
	}

	resultCh := make(chan encodeResult, numPipelines)

	// Launch parallel encoders with pre-scaled frames
	for i, pipeline := range pipelines {
		atomic.AddInt32(&mt.inFlightEncodes, 1)
		go func(variantID string, p *outputPipeline, inputFrame *VideoFrame) {
			defer atomic.AddInt32(&mt.inFlightEncodes, -1)

			// Check if pipeline was removed before we start encoding
			if atomic.LoadInt32(&p.removed) == 1 {
				resultCh <- encodeResult{id: variantID, err: errors.New("pipeline removed")}
				return
			}

			encoded, err := p.encoder.Encode(inputFrame)
			resultCh <- encodeResult{
				id:    variantID,
				frame: encoded,
				codec: p.variant.Codec,
				err:   err,
			}
		}(ids[i], pipeline, scaledFrames[i])
	}

	// Collect results with timeout
	result := &TranscodeResult{
		Variants: make([]VariantFrame, 0, numPipelines),
	}

	var firstErr error
	timeout := time.After(mt.encodeTimeout)
	received := 0

	for received < numPipelines {
		select {
		case res := <-resultCh:
			received++
			if res.err != nil && firstErr == nil {
				firstErr = fmt.Errorf("encode error for %s: %w", res.id, res.err)
				continue
			}
			if res.frame != nil {
				// Propagate source timestamp to encoded output
				// This ensures all variants have consistent timestamps from the source
				res.frame.Timestamp = sourceTs
				result.Variants = append(result.Variants, VariantFrame{
					VariantID: res.id,
					Frame:     res.frame,
					Codec:     res.codec,
				})
			}
		case <-timeout:
			// Some encoders timed out - return what we have
			if firstErr == nil {
				firstErr = fmt.Errorf("encode timeout: got %d/%d results", received, numPipelines)
			}
			// Return partial results so we don't block
			if len(result.Variants) > 0 {
				return result, nil // Return partial success
			}
			return nil, firstErr
		}
	}

	if firstErr != nil {
		return nil, firstErr
	}

	return result, nil
}

// prepareInputFrame scales the frame if needed for the target pipeline.
func (mt *MultiTranscoder) prepareInputFrame(frame *VideoFrame, pipeline *outputPipeline) *VideoFrame {
	targetW := pipeline.variant.Width
	targetH := pipeline.variant.Height
	if targetW == 0 {
		targetW = frame.Width
	}
	if targetH == 0 {
		targetH = frame.Height
	}

	// No scaling needed
	if frame.Width == targetW && frame.Height == targetH {
		return frame
	}

	// Create or update scaler (thread-safe: each pipeline has its own scaler)
	if pipeline.scaler == nil ||
		pipeline.scaler.srcWidth != frame.Width ||
		pipeline.scaler.srcHeight != frame.Height ||
		pipeline.scaler.dstWidth != targetW ||
		pipeline.scaler.dstHeight != targetH {
		pipeline.scaler = NewVideoScaler(
			frame.Width, frame.Height,
			targetW, targetH,
			ScaleModeStretch,
		)
	}
	return pipeline.scaler.Scale(frame)
}

// SetKeyframeCallback sets the callback for requesting keyframes from the source.
// This should be called to enable automatic recovery when the decoder needs a keyframe.
func (mt *MultiTranscoder) SetKeyframeCallback(fn KeyframeRequestFunc) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	mt.onKeyframeNeeded = fn
}

// requestKeyframeFromSource sends a rate-limited keyframe request to the source.
// Returns true if a request was sent, false if rate-limited or no callback set.
// This is the central point for all keyframe requests to prevent flooding.
func (mt *MultiTranscoder) requestKeyframeFromSource() bool {
	// Get the callback (read under lock)
	mt.mu.RLock()
	callback := mt.onKeyframeNeeded
	mt.mu.RUnlock()

	if callback == nil {
		return false
	}

	// Check rate limit atomically
	now := time.Now().UnixNano()
	lastRequest := atomic.LoadInt64(&mt.lastKeyframeRequest)

	if now-lastRequest < mt.minKeyframeInterval {
		// Rate limited - don't send
		return false
	}

	// Try to update last request time (compare-and-swap to avoid races)
	if !atomic.CompareAndSwapInt64(&mt.lastKeyframeRequest, lastRequest, now) {
		// Another goroutine beat us - they will send the request
		return false
	}

	// Send the request in a goroutine to avoid blocking
	go callback()
	return true
}

// NeedsKeyframe returns true if the transcoder is waiting for a keyframe to recover.
func (mt *MultiTranscoder) NeedsKeyframe() bool {
	return atomic.LoadInt32(&mt.needsKeyframe) == 1
}

// RequestSourceKeyframe manually triggers a keyframe request from the source.
// This is automatically called on decode errors, but can be called manually if needed.
// Note: This is rate-limited by MinKeyframeInterval to prevent flooding.
func (mt *MultiTranscoder) RequestSourceKeyframe() {
	atomic.StoreInt32(&mt.needsKeyframe, 1)
	mt.requestKeyframeFromSource()
}

// RequestKeyframe requests a keyframe from a specific output variant.
// For passthrough variants (no encoder), this requests a keyframe from the source.
// Note: Source requests are rate-limited by MinKeyframeInterval.
func (mt *MultiTranscoder) RequestKeyframe(variantID string) {
	mt.mu.RLock()
	pipeline, ok := mt.pipelines[variantID]
	mt.mu.RUnlock()

	if !ok {
		return
	}

	if pipeline.passthrough {
		// Passthrough variant - request from source since there's no encoder (rate-limited)
		mt.requestKeyframeFromSource()
	} else if pipeline.encoder != nil {
		// Regular variant - request from encoder
		pipeline.encoder.RequestKeyframe()
	}
}

// RequestKeyframeAll requests keyframes from all variants.
// For passthrough variants, requests a keyframe from the source.
// Note: Source requests are rate-limited by MinKeyframeInterval.
func (mt *MultiTranscoder) RequestKeyframeAll() {
	mt.mu.RLock()
	hasPassthrough := false
	for _, pipeline := range mt.pipelines {
		if pipeline.passthrough {
			hasPassthrough = true
		} else if pipeline.encoder != nil {
			pipeline.encoder.RequestKeyframe()
		}
	}
	mt.mu.RUnlock()

	// Request from source once if any passthrough variants exist (rate-limited)
	if hasPassthrough {
		mt.requestKeyframeFromSource()
	}
}

// SetBitrate updates the bitrate for a specific variant.
func (mt *MultiTranscoder) SetBitrate(variantID string, bitrateBps int) error {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	pipeline, ok := mt.pipelines[variantID]
	if !ok {
		return errors.New("variant not found: " + variantID)
	}
	if pipeline.encoder == nil {
		return errors.New("cannot set bitrate on passthrough variant: " + variantID)
	}
	return pipeline.encoder.SetBitrate(bitrateBps)
}

// Variants returns the list of output variant IDs.
func (mt *MultiTranscoder) Variants() []string {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	ids := make([]string, 0, len(mt.pipelines))
	for id := range mt.pipelines {
		ids = append(ids, id)
	}
	return ids
}

// VariantConfig returns the configuration for a specific variant.
func (mt *MultiTranscoder) VariantConfig(variantID string) (OutputConfig, bool) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	if pipeline, ok := mt.pipelines[variantID]; ok {
		return pipeline.variant, true
	}
	return OutputConfig{}, false
}

// AddOutput adds a new output variant dynamically.
// The new variant will be available for the next Transcode call.
// Thread-safe: can be called while transcoding is in progress.
// Note: Encoder creation is done OUTSIDE the lock to avoid blocking Transcode calls.
func (mt *MultiTranscoder) AddOutput(config OutputConfig) error {
	// Validate config first (no lock needed)
	if config.ID == "" {
		return errors.New("output variant ID cannot be empty")
	}
	if config.Codec == VideoCodecUnknown {
		return errors.New("output codec must be specified")
	}

	// Quick check if variant exists (brief lock)
	mt.mu.Lock()
	if _, exists := mt.pipelines[config.ID]; exists {
		mt.mu.Unlock()
		return errors.New("output variant already exists: " + config.ID)
	}
	// Get dimensions from input if needed
	inputW, inputH, inputFPS := mt.inputWidth, mt.inputHeight, mt.inputFPS
	mt.mu.Unlock()

	pipeline := &outputPipeline{variant: config, passthrough: config.Passthrough}

	// For passthrough variants, no encoder needed - quick add
	if config.Passthrough {
		mt.mu.Lock()
		// Double-check it wasn't added while we released the lock
		if _, exists := mt.pipelines[config.ID]; exists {
			mt.mu.Unlock()
			return errors.New("output variant already exists: " + config.ID)
		}
		mt.pipelines[config.ID] = pipeline
		mt.mu.Unlock()
		return nil
	}

	// Determine output dimensions (no lock needed)
	w, h := config.Width, config.Height
	if w == 0 {
		w = inputW
	}
	if h == 0 {
		h = inputH
	}
	if w == 0 {
		w = 1920
	}
	if h == 0 {
		h = 1080
	}

	fps := config.FPS
	if fps == 0 {
		fps = inputFPS
	}
	if fps == 0 {
		fps = 30
	}

	bitrate := config.BitrateBps
	if bitrate == 0 {
		bitrate = 2_000_000
	}

	encoderConfig := VideoEncoderConfig{
		Codec:          config.Codec,
		Width:          w,
		Height:         h,
		FPS:            fps,
		BitrateBps:     bitrate,
		Quality:        config.Quality,
		TemporalLayers: config.TemporalLayers,
		SpatialLayers:  config.SpatialLayers,
	}

	// Create encoder OUTSIDE the lock - this can be slow!
	encoder, err := NewVideoEncoder(encoderConfig)
	if err != nil {
		return fmt.Errorf("failed to create encoder for %s: %w", config.ID, err)
	}

	// Request keyframe before adding to pipeline
	encoder.RequestKeyframe()

	// Now add to pipeline with brief lock
	mt.mu.Lock()
	// Double-check it wasn't added while we created the encoder
	if _, exists := mt.pipelines[config.ID]; exists {
		mt.mu.Unlock()
		encoder.Close() // Clean up the encoder we just created
		return errors.New("output variant already exists: " + config.ID)
	}
	pipeline.encoder = encoder
	mt.pipelines[config.ID] = pipeline
	callback := mt.onKeyframeNeeded
	pipelineCount := len(mt.pipelines)
	mt.mu.Unlock()

	// Request keyframe from SOURCE so the decoder can decode frames for the new encoder
	// Fire async to avoid any blocking
	if callback != nil {
		go callback()
	}

	fmt.Printf("[DEBUG] AddOutput: added %s (codec=%d, %dx%d), total pipelines=%d\n",
		config.ID, config.Codec, w, h, pipelineCount)
	return nil
}

// RemoveOutput removes an output variant dynamically.
// Thread-safe: can be called while transcoding is in progress.
// The variant will no longer appear in TranscodeResult after removal.
// Note: If encoding is in progress, the encoder is marked for removal and
// will be cleaned up on the next Transcode call.
func (mt *MultiTranscoder) RemoveOutput(variantID string) error {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	pipeline, exists := mt.pipelines[variantID]
	if !exists {
		return errors.New("variant not found: " + variantID)
	}

	// Mark as removed - encoding goroutines will skip this pipeline
	atomic.StoreInt32(&pipeline.removed, 1)

	// Try to close encoder, but don't block if it's in use
	// The encoder will be cleaned up on the next Transcode call
	go func(p *outputPipeline) {
		// Give in-flight encoding a moment to complete
		time.Sleep(100 * time.Millisecond)
		if p.encoder != nil {
			p.encoder.Close()
		}
	}(pipeline)

	delete(mt.pipelines, variantID)
	return nil
}

// OutputCount returns the number of active output variants.
func (mt *MultiTranscoder) OutputCount() int {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	return len(mt.pipelines)
}

// InputCodec returns the detected or configured input codec.
func (mt *MultiTranscoder) InputCodec() VideoCodec {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	return mt.inputCodec
}

// Close releases all resources.
func (mt *MultiTranscoder) Close() error {
	mt.Stop() // Stop if running

	mt.mu.Lock()
	defer mt.mu.Unlock()

	var errs []error
	if mt.decoder != nil {
		if err := mt.decoder.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	for _, pipeline := range mt.pipelines {
		if pipeline.encoder != nil {
			if err := pipeline.encoder.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// =============================================================================
// VideoSource Interface Implementation
// =============================================================================

// SetSource sets a VideoSource for pull-mode input.
// When Start() is called, frames will be read from this source.
func (mt *MultiTranscoder) SetSource(source VideoSource) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	mt.source = source
	if source != nil {
		cfg := source.Config()
		mt.sourceConfig = SourceConfig{
			Width:      cfg.Width,
			Height:     cfg.Height,
			FPS:        cfg.FPS,
			Format:     PixelFormatI420,
			SourceType: SourceTypeCustom,
		}
		if mt.inputWidth == 0 {
			mt.inputWidth = cfg.Width
		}
		if mt.inputHeight == 0 {
			mt.inputHeight = cfg.Height
		}
		if mt.inputFPS == 0 {
			mt.inputFPS = cfg.FPS
		}
	}
}

// Start begins reading from the source and transcoding.
// Decoded frames are available via ReadFrame() or SetCallback().
func (mt *MultiTranscoder) Start(ctx context.Context) error {
	if !atomic.CompareAndSwapInt32(&mt.running, 0, 1) {
		return errors.New("already running")
	}

	mt.mu.Lock()
	source := mt.source
	mt.frameCh = make(chan *VideoFrame, 5)
	ctx, mt.cancel = context.WithCancel(ctx)
	mt.mu.Unlock()

	if source == nil {
		atomic.StoreInt32(&mt.running, 0)
		return errors.New("no source set")
	}

	if err := source.Start(ctx); err != nil {
		atomic.StoreInt32(&mt.running, 0)
		return err
	}

	go mt.processLoop(ctx, source)
	return nil
}

// processLoop reads frames from source and delivers decoded frames.
func (mt *MultiTranscoder) processLoop(ctx context.Context, source VideoSource) {
	defer func() {
		atomic.StoreInt32(&mt.running, 0)
		mt.mu.Lock()
		if mt.frameCh != nil {
			close(mt.frameCh)
		}
		mt.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		frame, err := source.ReadFrame(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		if frame == nil {
			continue
		}

		// Clone frame to ensure it outlives the source buffer
		clonedFrame := frame.Clone()

		// Deliver via callback if set
		mt.mu.Lock()
		cb := mt.frameCallback
		mt.mu.Unlock()

		if cb != nil {
			cb(clonedFrame)
		}

		// Also deliver via channel
		select {
		case mt.frameCh <- clonedFrame:
		default:
			// Drop frame if buffer full
		}
	}
}

// Stop halts the processing loop.
func (mt *MultiTranscoder) Stop() error {
	if !atomic.CompareAndSwapInt32(&mt.running, 1, 0) {
		return nil // Already stopped
	}

	mt.mu.Lock()
	cancel := mt.cancel
	source := mt.source
	mt.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if source != nil {
		source.Stop()
	}
	return nil
}

// ReadFrame reads the next decoded frame from the transcoder.
// This is useful when using the transcoder as a VideoSource in a pipeline.
func (mt *MultiTranscoder) ReadFrame(ctx context.Context) (*VideoFrame, error) {
	mt.mu.Lock()
	ch := mt.frameCh
	mt.mu.Unlock()

	if ch == nil {
		return nil, errors.New("not running")
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case frame, ok := <-ch:
		if !ok {
			return nil, io.EOF
		}
		return frame, nil
	}
}

// ReadFrameInto reads the next frame into a pre-allocated buffer.
// Optional: returns ErrNotSupported as this implementation uses Clone().
func (mt *MultiTranscoder) ReadFrameInto(ctx context.Context, buf *VideoFrameBuffer) error {
	return ErrNotSupported
}

// SetCallback sets push-mode callback for decoded frame delivery.
func (mt *MultiTranscoder) SetCallback(cb VideoFrameCallback) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	mt.frameCallback = cb
}

// Config returns the source configuration for this transcoder.
// Implements VideoSource interface.
func (mt *MultiTranscoder) Config() SourceConfig {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	return mt.sourceConfig
}

// =============================================================================
// Adaptive Bitrate Helper
// =============================================================================

// ABRController provides adaptive bitrate control for multi-output transcoding.
// It monitors network conditions and adjusts variant selection.
type ABRController struct {
	transcoder *MultiTranscoder
	variants   []string // Ordered by quality (highest first)
	current    int      // Current variant index
	mu         sync.Mutex
}

// NewABRController creates an ABR controller for the given multi-transcoder.
// Variants should be ordered by quality (highest bitrate first).
func NewABRController(mt *MultiTranscoder, variantOrder []string) *ABRController {
	return &ABRController{
		transcoder: mt,
		variants:   variantOrder,
		current:    0,
	}
}

// CurrentVariant returns the currently selected variant ID.
func (c *ABRController) CurrentVariant() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current < len(c.variants) {
		return c.variants[c.current]
	}
	return ""
}

// StepDown moves to a lower quality variant.
// Returns false if already at lowest quality.
func (c *ABRController) StepDown() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current < len(c.variants)-1 {
		c.current++
		return true
	}
	return false
}

// StepUp moves to a higher quality variant.
// Returns false if already at highest quality.
func (c *ABRController) StepUp() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current > 0 {
		c.current--
		return true
	}
	return false
}

// SetVariant sets the current variant by ID.
func (c *ABRController) SetVariant(variantID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, v := range c.variants {
		if v == variantID {
			c.current = i
			return true
		}
	}
	return false
}

// SelectOutput picks the appropriate output from a TranscodeResult.
func (c *ABRController) SelectOutput(result *TranscodeResult) *VariantFrame {
	c.mu.Lock()
	variantID := ""
	if c.current < len(c.variants) {
		variantID = c.variants[c.current]
	}
	c.mu.Unlock()

	for i := range result.Variants {
		if result.Variants[i].VariantID == variantID {
			return &result.Variants[i]
		}
	}

	// Fallback to first available
	if len(result.Variants) > 0 {
		return &result.Variants[0]
	}
	return nil
}
