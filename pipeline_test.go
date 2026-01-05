//go:build darwin || linux
// +build darwin linux

package media

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockRTPWriter implements RTPWriter for testing
type mockRTPWriter struct {
	packets [][]byte
	mu      sync.Mutex
}

func (w *mockRTPWriter) WriteRTP(packet *RTPPacket) error {
	return nil
}

func (w *mockRTPWriter) WriteRTPBytes(data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	pkt := make([]byte, len(data))
	copy(pkt, data)
	w.packets = append(w.packets, pkt)
	return nil
}

func (w *mockRTPWriter) PacketCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.packets)
}

// mockRTPReader implements RTPReader for testing
type mockRTPReader struct {
	packets []*RTPPacket
	index   int
	mu      sync.Mutex
}

func (r *mockRTPReader) ReadRTP() (*RTPPacket, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.index >= len(r.packets) {
		return nil, nil
	}
	pkt := r.packets[r.index]
	r.index++
	return pkt, nil
}

func (r *mockRTPReader) ReadRTPBytes(buf []byte) (int, error) {
	return 0, nil
}

func TestVideoEncodePipeline(t *testing.T) {
	if !IsVPXAvailable() {
		t.Skip("VPX not available")
	}

	// Create test pattern source
	source := NewTestPatternSource(TestPatternConfig{
		Width:   320,
		Height:  240,
		FPS:     30,
		Pattern: PatternColorBars,
	})

	// Create encoder
	encoder, err := NewVP8Encoder(VideoEncoderConfig{
		Width:      320,
		Height:     240,
		FPS:        30,
		BitrateBps: 500000,
	})
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	// Create packetizer
	packetizer, err := NewVP8Packetizer(12345, 96, 1200)
	if err != nil {
		t.Fatalf("Failed to create packetizer: %v", err)
	}

	// Create mock writer
	writer := &mockRTPWriter{}

	// Create pipeline
	pipeline, err := NewVideoEncodePipeline(VideoPipelineConfig{
		Source:     source,
		Encoder:    encoder,
		Packetizer: packetizer,
		Writer:     writer,
		OnError: func(err error) {
			t.Logf("Pipeline error: %v", err)
		},
	})
	if err != nil {
		t.Fatalf("Failed to create pipeline: %v", err)
	}

	// Start pipeline
	if err := pipeline.Start(); err != nil {
		t.Fatalf("Failed to start pipeline: %v", err)
	}

	if pipeline.State() != PipelineStateRunning {
		t.Errorf("State = %v, want running", pipeline.State())
	}

	// Let it run for a bit
	time.Sleep(200 * time.Millisecond)

	// Request a keyframe
	pipeline.RequestKeyframe()
	time.Sleep(100 * time.Millisecond)

	// Stop pipeline
	if err := pipeline.Stop(); err != nil {
		t.Fatalf("Failed to stop pipeline: %v", err)
	}

	// Check stats
	stats := pipeline.Stats()
	t.Logf("Pipeline stats: frames captured=%d, encoded=%d, packets sent=%d",
		stats.FramesCaptured, stats.FramesEncoded, stats.PacketsSent)

	if stats.FramesCaptured == 0 {
		t.Error("No frames captured")
	}
	if stats.FramesEncoded == 0 {
		t.Error("No frames encoded")
	}
	if stats.PacketsSent == 0 {
		t.Error("No packets sent")
	}

	// Verify packets were written
	if writer.PacketCount() == 0 {
		t.Error("No packets written to writer")
	}

	// Close pipeline
	if err := pipeline.Close(); err != nil {
		t.Fatalf("Failed to close pipeline: %v", err)
	}
}

func TestVideoEncodePipelineKeyframeRequest(t *testing.T) {
	if !IsVPXAvailable() {
		t.Skip("VPX not available")
	}

	source := NewTestPatternSource(TestPatternConfig{
		Width:   320,
		Height:  240,
		FPS:     30,
		Pattern: PatternColorBars,
	})
	encoder, _ := NewVP8Encoder(VideoEncoderConfig{
		Width:      320,
		Height:     240,
		FPS:        30,
		BitrateBps: 500000,
	})
	packetizer, _ := NewVP8Packetizer(12345, 96, 1200)
	writer := &mockRTPWriter{}

	pipeline, _ := NewVideoEncodePipeline(VideoPipelineConfig{
		Source:     source,
		Encoder:    encoder,
		Packetizer: packetizer,
		Writer:     writer,
	})

	pipeline.Start()
	time.Sleep(100 * time.Millisecond)

	// Request keyframe
	pipeline.RequestKeyframe()
	time.Sleep(100 * time.Millisecond)

	stats := pipeline.Stats()
	// The first frame is always a keyframe, plus our request
	if stats.KeyframesSent < 1 {
		t.Errorf("KeyframesSent = %d, want at least 1", stats.KeyframesSent)
	}

	pipeline.Close()
}

func TestVideoEncodePipelineStats(t *testing.T) {
	if !IsVPXAvailable() {
		t.Skip("VPX not available")
	}

	source := NewTestPatternSource(TestPatternConfig{
		Width:   320,
		Height:  240,
		FPS:     30,
		Pattern: PatternColorBars,
	})
	encoder, _ := NewVP8Encoder(VideoEncoderConfig{
		Width:      320,
		Height:     240,
		FPS:        30,
		BitrateBps: 500000,
	})
	packetizer, _ := NewVP8Packetizer(12345, 96, 1200)
	writer := &mockRTPWriter{}

	pipeline, _ := NewVideoEncodePipeline(VideoPipelineConfig{
		Source:     source,
		Encoder:    encoder,
		Packetizer: packetizer,
		Writer:     writer,
	})

	pipeline.Start()
	time.Sleep(200 * time.Millisecond)
	pipeline.Stop()

	stats := pipeline.Stats()

	// Verify timing stats are reasonable
	if stats.EncodeTimeUs == 0 {
		t.Error("EncodeTimeUs should be > 0")
	}
	if stats.PacketizeTimeUs == 0 {
		t.Error("PacketizeTimeUs should be > 0")
	}

	avgEncodeUs := stats.EncodeTimeUs / max(stats.FramesEncoded, 1)
	avgPacketizeUs := stats.PacketizeTimeUs / max(stats.FramesEncoded, 1)

	t.Logf("Average encode time: %d µs, packetize time: %d µs",
		avgEncodeUs, avgPacketizeUs)

	pipeline.Close()
}

func TestVideoEncodePipelineValidation(t *testing.T) {
	// Test missing source
	_, err := NewVideoEncodePipeline(VideoPipelineConfig{
		Encoder:    &mockEncoder{},
		Packetizer: &mockPacketizer{},
		Writer:     &mockRTPWriter{},
	})
	if err == nil {
		t.Error("Expected error for missing source")
	}

	// Test missing encoder
	_, err = NewVideoEncodePipeline(VideoPipelineConfig{
		Source:     &mockSource{},
		Packetizer: &mockPacketizer{},
		Writer:     &mockRTPWriter{},
	})
	if err == nil {
		t.Error("Expected error for missing encoder")
	}

	// Test missing packetizer
	_, err = NewVideoEncodePipeline(VideoPipelineConfig{
		Source:  &mockSource{},
		Encoder: &mockEncoder{},
		Writer:  &mockRTPWriter{},
	})
	if err == nil {
		t.Error("Expected error for missing packetizer")
	}

	// Test missing writer
	_, err = NewVideoEncodePipeline(VideoPipelineConfig{
		Source:     &mockSource{},
		Encoder:    &mockEncoder{},
		Packetizer: &mockPacketizer{},
	})
	if err == nil {
		t.Error("Expected error for missing writer")
	}
}

// Mock implementations for validation tests
type mockSource struct{}

func (s *mockSource) Start(ctx context.Context) error { return nil }
func (s *mockSource) Stop() error                     { return nil }
func (s *mockSource) Close() error                    { return nil }
func (s *mockSource) ReadFrame(ctx context.Context) (*VideoFrame, error) {
	return nil, nil
}
func (s *mockSource) ReadFrameInto(ctx context.Context, buf *VideoFrameBuffer) error {
	return ErrNotSupported
}
func (s *mockSource) SetCallback(cb VideoFrameCallback) {}
func (s *mockSource) Config() SourceConfig              { return SourceConfig{} }

type mockEncoder struct{}

func (e *mockEncoder) Encode(frame *VideoFrame, buf []byte) (EncodeResult, error) { return EncodeResult{}, nil }
func (e *mockEncoder) MaxEncodedSize() int                                        { return 1024 }
func (e *mockEncoder) RequestKeyframe()                                {}
func (e *mockEncoder) SetBitrate(bps int) error                        { return nil }
func (e *mockEncoder) SetResolution(width, height int) error           { return ErrNotSupported }
func (e *mockEncoder) Provider() Provider                              { return ProviderAuto }
func (e *mockEncoder) Config() VideoEncoderConfig                      { return VideoEncoderConfig{} }
func (e *mockEncoder) Codec() VideoCodec                               { return VideoCodecVP8 }
func (e *mockEncoder) Stats() EncoderStats                             { return EncoderStats{} }
func (e *mockEncoder) Flush() ([]*EncodedFrame, error)                 { return nil, nil }
func (e *mockEncoder) Close() error                                    { return nil }

type mockPacketizer struct{}

func (p *mockPacketizer) Packetize(frame *EncodedFrame) ([]*RTPPacket, error) { return nil, nil }
func (p *mockPacketizer) PacketizeToBytes(frame *EncodedFrame) ([][]byte, error) {
	return nil, nil
}
func (p *mockPacketizer) SetSSRC(ssrc uint32)     {}
func (p *mockPacketizer) SSRC() uint32            { return 0 }
func (p *mockPacketizer) PayloadType() uint8      { return 0 }
func (p *mockPacketizer) SetPayloadType(pt uint8) {}
func (p *mockPacketizer) MTU() int                { return 1200 }
func (p *mockPacketizer) SetMTU(mtu int)          {}

func TestPipelineState(t *testing.T) {
	states := []struct {
		state PipelineState
		want  string
	}{
		{PipelineStateIdle, "idle"},
		{PipelineStateRunning, "running"},
		{PipelineStatePaused, "paused"},
		{PipelineStateStopped, "stopped"},
		{PipelineState(99), "unknown"},
	}

	for _, tc := range states {
		if got := tc.state.String(); got != tc.want {
			t.Errorf("%v.String() = %q, want %q", tc.state, got, tc.want)
		}
	}
}

func BenchmarkVideoEncodePipeline(b *testing.B) {
	if !IsVPXAvailable() {
		b.Skip("VPX not available")
	}

	source := NewTestPatternSource(TestPatternConfig{
		Width:   640,
		Height:  480,
		FPS:     30,
		Pattern: PatternColorBars,
	})
	encoder, _ := NewVP8Encoder(VideoEncoderConfig{
		Width:      640,
		Height:     480,
		FPS:        30,
		BitrateBps: 1000000,
	})
	packetizer, _ := NewVP8Packetizer(12345, 96, 1200)

	var packetCount atomic.Uint64
	writer := &benchWriter{count: &packetCount}

	pipeline, _ := NewVideoEncodePipeline(VideoPipelineConfig{
		Source:     source,
		Encoder:    encoder,
		Packetizer: packetizer,
		Writer:     writer,
	})

	b.ResetTimer()

	pipeline.Start()

	// Run for the duration of the benchmark
	for i := 0; i < b.N; i++ {
		time.Sleep(10 * time.Millisecond)
	}

	pipeline.Close()

	b.ReportMetric(float64(packetCount.Load())/float64(b.N), "packets/op")
}

type benchWriter struct {
	count *atomic.Uint64
}

func (w *benchWriter) WriteRTP(packet *RTPPacket) error { return nil }
func (w *benchWriter) WriteRTPBytes(data []byte) error {
	w.count.Add(1)
	return nil
}

func max(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}
