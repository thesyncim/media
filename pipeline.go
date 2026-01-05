package media

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// PipelineState represents the state of a media pipeline.
type PipelineState int

const (
	PipelineStateIdle    PipelineState = iota // Not started
	PipelineStateRunning                      // Processing media
	PipelineStatePaused                       // Paused
	PipelineStateStopped                      // Stopped
)

func (s PipelineState) String() string {
	switch s {
	case PipelineStateIdle:
		return "idle"
	case PipelineStateRunning:
		return "running"
	case PipelineStatePaused:
		return "paused"
	case PipelineStateStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// VideoEncodePipeline handles: VideoSource/VideoTrack -> Encoder -> Packetizer -> RTPWriter
type VideoEncodePipeline struct {
	source     VideoSource   // Raw frame source (can be nil if using track)
	track      VideoTrack    // Or a track as source
	encoder    VideoEncoder  // Encoder
	encodeBuf  []byte        // Buffer for encoder output
	packetizer RTPPacketizer // RTP packetizer
	writer     RTPWriter     // Output writer

	state  atomic.Int32
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	stats   VideoPipelineStats
	statsMu sync.Mutex

	keyframeRequested atomic.Bool
	onError           func(error)
	mu                sync.Mutex
}

// VideoPipelineStats provides pipeline statistics.
type VideoPipelineStats struct {
	FramesCaptured  uint64
	FramesEncoded   uint64
	FramesDropped   uint64
	PacketsSent     uint64
	BytesSent       uint64
	KeyframesSent   uint64
	EncodeTimeUs    uint64
	PacketizeTimeUs uint64
	Errors          uint64
}

// VideoPipelineConfig configures a video encode pipeline.
type VideoPipelineConfig struct {
	Source     VideoSource   // Raw frame source
	Track      VideoTrack    // Alternative: use track as source
	Encoder    VideoEncoder  // Encoder
	Packetizer RTPPacketizer // RTP packetizer
	Writer     RTPWriter     // Output writer
	OnError    func(error)   // Error callback
}

// NewVideoEncodePipeline creates a new video encoding pipeline.
func NewVideoEncodePipeline(config VideoPipelineConfig) (*VideoEncodePipeline, error) {
	if config.Source == nil && config.Track == nil {
		return nil, fmt.Errorf("either source or track must be provided")
	}
	if config.Encoder == nil {
		return nil, fmt.Errorf("encoder is required")
	}
	if config.Packetizer == nil {
		return nil, fmt.Errorf("packetizer is required")
	}
	if config.Writer == nil {
		return nil, fmt.Errorf("writer is required")
	}

	p := &VideoEncodePipeline{
		source:     config.Source,
		track:      config.Track,
		encoder:    config.Encoder,
		encodeBuf:  make([]byte, config.Encoder.MaxEncodedSize()),
		packetizer: config.Packetizer,
		writer:     config.Writer,
		onError:    config.OnError,
	}
	p.state.Store(int32(PipelineStateIdle))

	return p, nil
}

// Start starts the pipeline.
func (p *VideoEncodePipeline) Start() error {
	if PipelineState(p.state.Load()) == PipelineStateRunning {
		return fmt.Errorf("pipeline already running")
	}

	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.state.Store(int32(PipelineStateRunning))

	// Start source if we have one
	if p.source != nil {
		if err := p.source.Start(p.ctx); err != nil {
			p.state.Store(int32(PipelineStateIdle))
			return fmt.Errorf("failed to start source: %w", err)
		}
	}

	p.wg.Add(1)
	go p.processLoop()

	return nil
}

// Stop stops the pipeline.
func (p *VideoEncodePipeline) Stop() error {
	if PipelineState(p.state.Load()) != PipelineStateRunning {
		return nil
	}

	p.state.Store(int32(PipelineStateStopped))
	p.cancel()
	p.wg.Wait()

	if p.source != nil {
		p.source.Stop()
	}

	return nil
}

// Close closes the pipeline and releases resources.
func (p *VideoEncodePipeline) Close() error {
	p.Stop()

	var errs []error
	if p.source != nil {
		if err := p.source.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if p.encoder != nil {
		if err := p.encoder.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// RequestKeyframe requests a keyframe from the encoder.
func (p *VideoEncodePipeline) RequestKeyframe() {
	p.keyframeRequested.Store(true)
}

// State returns the current pipeline state.
func (p *VideoEncodePipeline) State() PipelineState {
	return PipelineState(p.state.Load())
}

// Stats returns pipeline statistics.
func (p *VideoEncodePipeline) Stats() VideoPipelineStats {
	p.statsMu.Lock()
	defer p.statsMu.Unlock()
	return p.stats
}

func (p *VideoEncodePipeline) processLoop() {
	defer p.wg.Done()

	for {
		select {
		case <-p.ctx.Done():
			return
		default:
		}

		// Get next frame
		var frame *VideoFrame
		var err error

		if p.source != nil {
			frame, err = p.source.ReadFrame(p.ctx)
		} else if p.track != nil {
			frame, err = p.track.ReadFrame(p.ctx)
		}

		if err != nil {
			if p.ctx.Err() != nil {
				return // Context cancelled
			}
			p.handleError(err)
			continue
		}

		if frame == nil {
			continue
		}

		p.statsMu.Lock()
		p.stats.FramesCaptured++
		p.statsMu.Unlock()

		// Check for keyframe request
		if p.keyframeRequested.Swap(false) {
			p.encoder.RequestKeyframe()
		}

		// Encode frame
		encodeStart := time.Now()
		result, err := p.encoder.Encode(frame, p.encodeBuf)
		encodeTime := time.Since(encodeStart)

		if err != nil {
			p.handleError(err)
			p.statsMu.Lock()
			p.stats.FramesDropped++
			p.statsMu.Unlock()
			continue
		}

		if result.N == 0 {
			continue // Encoder buffering
		}

		encoded := &EncodedFrame{
			Data:      make([]byte, result.N),
			FrameType: result.FrameType,
		}
		copy(encoded.Data, p.encodeBuf[:result.N])

		p.statsMu.Lock()
		p.stats.FramesEncoded++
		p.stats.EncodeTimeUs += uint64(encodeTime.Microseconds())
		if encoded.IsKeyframe() {
			p.stats.KeyframesSent++
		}
		p.statsMu.Unlock()

		// Packetize
		packetizeStart := time.Now()
		packets, err := p.packetizer.PacketizeToBytes(encoded)
		packetizeTime := time.Since(packetizeStart)

		if err != nil {
			p.handleError(err)
			continue
		}

		p.statsMu.Lock()
		p.stats.PacketizeTimeUs += uint64(packetizeTime.Microseconds())
		p.statsMu.Unlock()

		// Send packets
		for _, pkt := range packets {
			if err := p.writer.WriteRTPBytes(pkt); err != nil {
				p.handleError(err)
				continue
			}

			p.statsMu.Lock()
			p.stats.PacketsSent++
			p.stats.BytesSent += uint64(len(pkt))
			p.statsMu.Unlock()
		}
	}
}

func (p *VideoEncodePipeline) handleError(err error) {
	p.statsMu.Lock()
	p.stats.Errors++
	p.statsMu.Unlock()

	p.mu.Lock()
	cb := p.onError
	p.mu.Unlock()

	if cb != nil {
		go cb(err)
	}
}

// VideoDecodePipeline handles: RTPReader -> Depacketizer -> Decoder -> VideoFrameCallback
type VideoDecodePipeline struct {
	reader       RTPReader       // RTP packet source
	depacketizer RTPDepacketizer // RTP depacketizer
	decoder      VideoDecoder    // Decoder

	state  atomic.Int32
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	stats   VideoDecodeStats
	statsMu sync.Mutex

	onFrame VideoFrameCallback
	onError func(error)
	mu      sync.Mutex
}

// VideoDecodeStats provides decode pipeline statistics.
type VideoDecodeStats struct {
	PacketsReceived   uint64
	BytesReceived     uint64
	FramesAssembled   uint64
	FramesDecoded     uint64
	FramesDropped     uint64
	KeyframesDecoded  uint64
	DepacketizeTimeUs uint64
	DecodeTimeUs      uint64
	Errors            uint64
}

// VideoDecodePipelineConfig configures a video decode pipeline.
type VideoDecodePipelineConfig struct {
	Reader       RTPReader          // RTP packet source
	Depacketizer RTPDepacketizer    // RTP depacketizer
	Decoder      VideoDecoder       // Decoder
	OnFrame      VideoFrameCallback // Frame callback
	OnError      func(error)        // Error callback
}

// NewVideoDecodePipeline creates a new video decoding pipeline.
func NewVideoDecodePipeline(config VideoDecodePipelineConfig) (*VideoDecodePipeline, error) {
	if config.Reader == nil {
		return nil, fmt.Errorf("reader is required")
	}
	if config.Depacketizer == nil {
		return nil, fmt.Errorf("depacketizer is required")
	}
	if config.Decoder == nil {
		return nil, fmt.Errorf("decoder is required")
	}

	p := &VideoDecodePipeline{
		reader:       config.Reader,
		depacketizer: config.Depacketizer,
		decoder:      config.Decoder,
		onFrame:      config.OnFrame,
		onError:      config.OnError,
	}
	p.state.Store(int32(PipelineStateIdle))

	return p, nil
}

// Start starts the decode pipeline.
func (p *VideoDecodePipeline) Start() error {
	if PipelineState(p.state.Load()) == PipelineStateRunning {
		return fmt.Errorf("pipeline already running")
	}

	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.state.Store(int32(PipelineStateRunning))

	p.wg.Add(1)
	go p.processLoop()

	return nil
}

// Stop stops the decode pipeline.
func (p *VideoDecodePipeline) Stop() error {
	if PipelineState(p.state.Load()) != PipelineStateRunning {
		return nil
	}

	p.state.Store(int32(PipelineStateStopped))
	p.cancel()
	p.wg.Wait()

	return nil
}

// Close closes the pipeline and releases resources.
func (p *VideoDecodePipeline) Close() error {
	p.Stop()

	if p.decoder != nil {
		return p.decoder.Close()
	}
	return nil
}

// OnFrame sets the frame callback.
func (p *VideoDecodePipeline) OnFrame(callback VideoFrameCallback) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onFrame = callback
}

// State returns the current pipeline state.
func (p *VideoDecodePipeline) State() PipelineState {
	return PipelineState(p.state.Load())
}

// Stats returns decode pipeline statistics.
func (p *VideoDecodePipeline) Stats() VideoDecodeStats {
	p.statsMu.Lock()
	defer p.statsMu.Unlock()
	return p.stats
}

func (p *VideoDecodePipeline) processLoop() {
	defer p.wg.Done()

	buf := make([]byte, 2000) // MTU + some headroom

	for {
		select {
		case <-p.ctx.Done():
			return
		default:
		}

		// Read RTP packet
		n, err := p.reader.ReadRTPBytes(buf)
		if err != nil {
			if p.ctx.Err() != nil {
				return
			}
			if err != io.EOF {
				p.handleError(err)
			}
			continue
		}

		if n == 0 {
			continue
		}

		p.statsMu.Lock()
		p.stats.PacketsReceived++
		p.stats.BytesReceived += uint64(n)
		p.statsMu.Unlock()

		// Depacketize
		depacketizeStart := time.Now()
		encoded, err := p.depacketizer.DepacketizeBytes(buf[:n])
		depacketizeTime := time.Since(depacketizeStart)

		if err != nil {
			p.handleError(err)
			continue
		}

		p.statsMu.Lock()
		p.stats.DepacketizeTimeUs += uint64(depacketizeTime.Microseconds())
		p.statsMu.Unlock()

		if encoded == nil {
			continue // Frame not complete yet
		}

		p.statsMu.Lock()
		p.stats.FramesAssembled++
		p.statsMu.Unlock()

		// Decode
		decodeStart := time.Now()
		frame, err := p.decoder.Decode(encoded)
		decodeTime := time.Since(decodeStart)

		if err != nil {
			p.handleError(err)
			p.statsMu.Lock()
			p.stats.FramesDropped++
			p.statsMu.Unlock()
			continue
		}

		if frame == nil {
			continue // Decoder buffering
		}

		p.statsMu.Lock()
		p.stats.FramesDecoded++
		p.stats.DecodeTimeUs += uint64(decodeTime.Microseconds())
		if encoded.IsKeyframe() {
			p.stats.KeyframesDecoded++
		}
		p.statsMu.Unlock()

		// Deliver frame
		p.mu.Lock()
		cb := p.onFrame
		p.mu.Unlock()

		if cb != nil {
			cb(frame)
		}
	}
}

func (p *VideoDecodePipeline) handleError(err error) {
	p.statsMu.Lock()
	p.stats.Errors++
	p.statsMu.Unlock()

	p.mu.Lock()
	cb := p.onError
	p.mu.Unlock()

	if cb != nil {
		go cb(err)
	}
}

// AudioEncodePipeline handles: AudioSource/AudioTrack -> Encoder -> Packetizer -> RTPWriter
type AudioEncodePipeline struct {
	source     AudioSource   // Raw sample source
	track      AudioTrack    // Or a track as source
	encoder    AudioEncoder  // Encoder
	packetizer RTPPacketizer // RTP packetizer
	writer     RTPWriter     // Output writer

	state  atomic.Int32
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	stats   AudioPipelineStats
	statsMu sync.Mutex

	onError func(error)
	mu      sync.Mutex
}

// AudioPipelineStats provides audio pipeline statistics.
type AudioPipelineStats struct {
	SamplesCaptured uint64
	FramesEncoded   uint64
	PacketsSent     uint64
	BytesSent       uint64
	EncodeTimeUs    uint64
	Errors          uint64
}

// AudioDecodePipeline handles: RTPReader -> Depacketizer -> Decoder -> AudioSamplesCallback
type AudioDecodePipeline struct {
	reader       RTPReader
	depacketizer RTPDepacketizer
	decoder      AudioDecoder

	state  atomic.Int32
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	stats   AudioDecodeStats
	statsMu sync.Mutex

	onSamples AudioSamplesCallback
	onError   func(error)
	mu        sync.Mutex
}

// AudioDecodeStats provides audio decode pipeline statistics.
type AudioDecodeStats struct {
	PacketsReceived uint64
	BytesReceived   uint64
	FramesDecoded   uint64
	SamplesDecoded  uint64
	PLCFrames       uint64
	DecodeTimeUs    uint64
	Errors          uint64
}
