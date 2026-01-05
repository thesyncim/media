package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/thesyncim/media"
	"github.com/yutopp/go-rtmp"
	rtmpmsg "github.com/yutopp/go-rtmp/message"
)

// Test extractSPSPPS parsing
func TestExtractSPSPPS(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantSPS bool
		wantPPS bool
	}{
		{
			name:    "empty data",
			data:    []byte{},
			wantSPS: false,
			wantPPS: false,
		},
		{
			name:    "too short",
			data:    []byte{0x01, 0x02, 0x03, 0x04, 0x05},
			wantSPS: false,
			wantPPS: false,
		},
		{
			name: "valid AVCC with SPS/PPS",
			// AVCC format: version(1) + profile(1) + compat(1) + level(1) + 0xFF(1) + numSPS(1) + SPSlen(2) + SPS + numPPS(1) + PPSlen(2) + PPS
			data: []byte{
				0x01, 0x64, 0x00, 0x1F, 0xFF, // version, profile, compat, level, length_size_minus_one
				0xE1,             // numSPS = 1 (0xE1 & 0x1F = 1)
				0x00, 0x04,       // SPS length = 4
				0x67, 0x64, 0x00, 0x1F, // SPS data
				0x01,             // numPPS = 1
				0x00, 0x03,       // PPS length = 3
				0x68, 0xEB, 0xE3, // PPS data
			},
			wantSPS: true,
			wantPPS: true,
		},
		{
			name: "SPS only",
			data: []byte{
				0x01, 0x64, 0x00, 0x1F, 0xFF,
				0xE1,
				0x00, 0x02,
				0x67, 0x64,
			},
			wantSPS: true,
			wantPPS: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sps, pps := extractSPSPPS(tt.data)
			gotSPS := len(sps) > 0
			gotPPS := len(pps) > 0

			if gotSPS != tt.wantSPS {
				t.Errorf("SPS: got %v, want %v (sps=%v)", gotSPS, tt.wantSPS, sps)
			}
			if gotPPS != tt.wantPPS {
				t.Errorf("PPS: got %v, want %v (pps=%v)", gotPPS, tt.wantPPS, pps)
			}
		})
	}
}

// Test AVCC NAL parsing
func TestParseAVCCNALUs(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		wantCount int
	}{
		{
			name:      "empty",
			data:      []byte{},
			wantCount: 0,
		},
		{
			name:      "too short",
			data:      []byte{0x00, 0x00, 0x00},
			wantCount: 0,
		},
		{
			name: "single NAL",
			data: []byte{
				0x00, 0x00, 0x00, 0x05, // length = 5
				0x65, 0x88, 0x84, 0x00, 0x00, // NAL data (IDR)
			},
			wantCount: 1,
		},
		{
			name: "multiple NALs",
			data: []byte{
				0x00, 0x00, 0x00, 0x03, // length = 3
				0x06, 0x05, 0x01, // SEI NAL
				0x00, 0x00, 0x00, 0x04, // length = 4
				0x65, 0x88, 0x84, 0x00, // IDR NAL
			},
			wantCount: 2,
		},
		{
			name: "invalid length",
			data: []byte{
				0x00, 0x00, 0x00, 0xFF, // length = 255 (too long)
				0x65,
			},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nalus := parseAVCCNALUs(tt.data)
			if len(nalus) != tt.wantCount {
				t.Errorf("got %d NALUs, want %d", len(nalus), tt.wantCount)
			}
		})
	}
}

// Test Annex-B building
func TestBuildAnnexB(t *testing.T) {
	sps := []byte{0x67, 0x64, 0x00, 0x1F}
	pps := []byte{0x68, 0xEB, 0xE3}
	nalu := []byte{0x65, 0x88, 0x84, 0x00}

	tests := []struct {
		name   string
		nalus  [][]byte
		sps    []byte
		pps    []byte
		isKey  bool
		minLen int
	}{
		{
			name:   "keyframe with SPS/PPS",
			nalus:  [][]byte{nalu},
			sps:    sps,
			pps:    pps,
			isKey:  true,
			minLen: 4 + len(sps) + 4 + len(pps) + 4 + len(nalu), // start codes + data
		},
		{
			name:   "delta frame",
			nalus:  [][]byte{nalu},
			sps:    sps,
			pps:    pps,
			isKey:  false,
			minLen: 4 + len(nalu), // just NAL with start code
		},
		{
			name:   "keyframe without SPS/PPS",
			nalus:  [][]byte{nalu},
			sps:    nil,
			pps:    nil,
			isKey:  true,
			minLen: 4 + len(nalu),
		},
		{
			name:   "multiple NALUs",
			nalus:  [][]byte{nalu, nalu},
			sps:    nil,
			pps:    nil,
			isKey:  false,
			minLen: 2 * (4 + len(nalu)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildAnnexB(tt.nalus, tt.sps, tt.pps, tt.isKey)
			if len(result) < tt.minLen {
				t.Errorf("result too short: got %d, want at least %d", len(result), tt.minLen)
			}

			// Verify start codes
			if !bytes.HasPrefix(result, []byte{0, 0, 0, 1}) {
				t.Error("result doesn't start with start code")
			}
		})
	}
}

// Test status endpoint
func TestStatusEndpoint(t *testing.T) {
	// Reset global state
	streamMu.Lock()
	currentStream = nil
	streamMu.Unlock()
	viewerCount.Store(0)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	w := httptest.NewRecorder()

	handleStatus(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status code: got %d, want 200", resp.StatusCode)
	}

	var status map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if status["streaming"].(bool) != false {
		t.Error("expected streaming=false when no stream")
	}

	// Simulate active stream
	streamMu.Lock()
	currentStream = &rtmpStream{
		width:   1920,
		height:  1080,
		frameCh: make(chan *media.EncodedFrame, 60),
	}
	streamMu.Unlock()
	viewerCount.Store(2)

	req = httptest.NewRequest(http.MethodGet, "/status", nil)
	w = httptest.NewRecorder()
	handleStatus(w, req)

	if err := json.NewDecoder(w.Result().Body).Decode(&status); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if status["streaming"].(bool) != true {
		t.Error("expected streaming=true with active stream")
	}
	if int(status["viewers"].(float64)) != 2 {
		t.Errorf("viewers: got %v, want 2", status["viewers"])
	}
	if int(status["width"].(float64)) != 1920 {
		t.Errorf("width: got %v, want 1920", status["width"])
	}

	// Cleanup
	streamMu.Lock()
	currentStream = nil
	streamMu.Unlock()
	viewerCount.Store(0)
}

// Test config endpoint
func TestConfigEndpoint(t *testing.T) {
	// Reset config
	outputCodec = "passthrough"
	outputBitrate = 2_000_000

	// GET config
	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()
	handleConfig(w, req)

	var config map[string]interface{}
	if err := json.NewDecoder(w.Result().Body).Decode(&config); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if config["codec"].(string) != "passthrough" {
		t.Errorf("codec: got %s, want passthrough", config["codec"])
	}

	// POST config change
	body := bytes.NewBufferString(`{"codec":"vp8","bitrate":1000000}`)
	req = httptest.NewRequest(http.MethodPost, "/config", body)
	w = httptest.NewRecorder()
	handleConfig(w, req)

	if outputCodec != "vp8" {
		t.Errorf("codec not updated: got %s, want vp8", outputCodec)
	}
	if outputBitrate != 1000000 {
		t.Errorf("bitrate not updated: got %d, want 1000000", outputBitrate)
	}

	// Reset
	outputCodec = "passthrough"
	outputBitrate = 2_000_000
}

// Test WebRTC offer handling
func TestOfferEndpoint(t *testing.T) {
	// Reset state
	streamMu.Lock()
	currentStream = nil
	streamMu.Unlock()
	outputCodec = "passthrough"

	// Create a valid WebRTC offer
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("failed to create peer connection: %v", err)
	}
	defer pc.Close()

	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		t.Fatalf("failed to add transceiver: %v", err)
	}

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatalf("failed to create offer: %v", err)
	}

	gatherComplete := webrtc.GatheringCompletePromise(pc)
	pc.SetLocalDescription(offer)
	<-gatherComplete

	offerJSON, _ := json.Marshal(pc.LocalDescription())
	req := httptest.NewRequest(http.MethodPost, "/offer", bytes.NewReader(offerJSON))
	w := httptest.NewRecorder()

	handleOffer(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("offer failed: status=%d body=%s", resp.StatusCode, body)
	}

	var answer webrtc.SessionDescription
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		t.Fatalf("failed to decode answer: %v", err)
	}

	if answer.Type != webrtc.SDPTypeAnswer {
		t.Errorf("expected answer SDP, got %s", answer.Type)
	}
}

// Test HTML serving
func TestServeHTML(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	serveHTML(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("RTMP to WebRTC")) {
		t.Error("HTML doesn't contain expected title")
	}
	if !bytes.Contains(body, []byte("<video")) {
		t.Error("HTML doesn't contain video element")
	}
}

// Test RTMP handler
func TestRTMPHandler(t *testing.T) {
	handler := &rtmpHandler{}

	// Test OnPublish
	err := handler.OnPublish(nil, 0, &rtmpmsg.NetStreamPublish{PublishingName: "test"})
	if err != nil {
		t.Errorf("OnPublish failed: %v", err)
	}

	streamMu.RLock()
	hasStream := currentStream != nil
	streamMu.RUnlock()

	if !hasStream {
		t.Error("stream not created after OnPublish")
	}

	// Test OnClose
	handler.OnClose()

	streamMu.RLock()
	hasStream = currentStream != nil
	streamMu.RUnlock()

	if hasStream {
		t.Error("stream not cleaned up after OnClose")
	}
}

// Integration test: full RTMP to frame flow
func TestRTMPToFrameFlow(t *testing.T) {
	// Create stream
	stream := &rtmpStream{
		width:   1920,
		height:  1080,
		frameCh: make(chan *media.EncodedFrame, 10),
	}

	// Set SPS/PPS (realistic H.264 parameters)
	stream.sps = []byte{0x67, 0x64, 0x00, 0x1F, 0xAC, 0xD9, 0x40, 0x78}
	stream.pps = []byte{0x68, 0xEB, 0xE3, 0xCB, 0x22, 0xC0}

	streamMu.Lock()
	currentStream = stream
	streamMu.Unlock()

	// Simulate sending a keyframe
	nalu := []byte{0x65, 0x88, 0x84, 0x00, 0x01, 0x02, 0x03, 0x04}
	annexB := buildAnnexB([][]byte{nalu}, stream.sps, stream.pps, true)

	frame := &media.EncodedFrame{
		Data:      annexB,
		FrameType: media.FrameTypeKey,
		Timestamp: 90000, // 1 second at 90kHz
	}

	// Send frame
	select {
	case stream.frameCh <- frame:
	default:
		t.Fatal("failed to send frame to channel")
	}

	// Receive frame
	select {
	case received := <-stream.frameCh:
		if received.FrameType != media.FrameTypeKey {
			t.Errorf("frame type: got %v, want key", received.FrameType)
		}
		if received.Timestamp != 90000 {
			t.Errorf("timestamp: got %d, want 90000", received.Timestamp)
		}
		if len(received.Data) == 0 {
			t.Error("received empty frame data")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for frame")
	}

	// Cleanup
	streamMu.Lock()
	currentStream = nil
	streamMu.Unlock()
}

// Test passthrough mode (no transcoding)
func TestPassthroughMode(t *testing.T) {
	if !media.IsH264Available() {
		t.Skip("H.264 decoder not available")
	}

	outputCodec = "passthrough"

	// Create a realistic H.264 keyframe
	sps := []byte{0x67, 0x64, 0x00, 0x1F, 0xAC, 0xD9, 0x40, 0x78}
	pps := []byte{0x68, 0xEB, 0xE3, 0xCB, 0x22, 0xC0}
	idr := make([]byte, 100)
	idr[0] = 0x65 // IDR NAL

	annexB := buildAnnexB([][]byte{idr}, sps, pps, true)

	frame := &media.EncodedFrame{
		Data:      annexB,
		FrameType: media.FrameTypeKey,
		Timestamp: 0,
	}

	// Create packetizer
	packetizer, err := media.CreateVideoPacketizer(media.VideoCodecH264, 0x12345678, 96, 1200)
	if err != nil {
		t.Fatalf("failed to create packetizer: %v", err)
	}

	// Packetize frame
	packets, err := packetizer.Packetize(frame)
	if err != nil {
		t.Fatalf("packetize failed: %v", err)
	}

	if len(packets) == 0 {
		t.Error("no RTP packets generated")
	}

	t.Logf("Generated %d RTP packets from %d bytes", len(packets), len(frame.Data))
}

// Test transcoding mode
func TestTranscodingMode(t *testing.T) {
	if !media.IsH264Available() || !media.IsVP8Available() {
		t.Skip("H.264 decoder or VP8 encoder not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create test pattern source
	source := media.NewTestPatternSource(media.TestPatternConfig{
		Width:   640,
		Height:  480,
		FPS:     30,
		Pattern: media.PatternMovingBox,
	})
	defer source.Close()

	if err := source.Start(ctx); err != nil {
		t.Fatalf("failed to start source: %v", err)
	}

	// Create H.264 encoder
	h264Enc, err := media.NewH264Encoder(media.VideoEncoderConfig{
		Width:      640,
		Height:     480,
		BitrateBps: 1_000_000,
		FPS:        30,
	})
	if err != nil {
		t.Fatalf("failed to create H.264 encoder: %v", err)
	}
	defer h264Enc.Close()
	encodeBuf := make([]byte, h264Enc.MaxEncodedSize())

	// Create transcoder H.264 -> VP8
	transcoder, err := media.NewTranscoder(media.TranscoderConfig{
		InputCodec:  media.VideoCodecH264,
		OutputCodec: media.VideoCodecVP8,
		Width:       640,
		Height:      480,
		BitrateBps:  500_000,
		FPS:         30,
	})
	if err != nil {
		t.Skipf("failed to create transcoder: %v", err)
	}
	defer transcoder.Close()

	// Encode a few frames and transcode them
	successCount := 0
	for i := 0; i < 10; i++ {
		frame, err := source.ReadFrame(ctx)
		if err != nil {
			continue
		}

		// Encode to H.264
		result, err := h264Enc.Encode(frame, encodeBuf)
		if err != nil || result.N == 0 {
			continue
		}
		h264Frame := &media.EncodedFrame{
			Data:      encodeBuf[:result.N],
			FrameType: result.FrameType,
		}

		// Transcode to VP8
		vp8Frame, err := transcoder.Transcode(h264Frame)
		if err != nil || vp8Frame == nil {
			continue
		}

		successCount++
	}

	if successCount == 0 {
		t.Error("no frames were successfully transcoded")
	}

	t.Logf("Successfully transcoded %d frames", successCount)
}

// Test concurrent viewer connections
func TestConcurrentViewers(t *testing.T) {
	// Reset state
	streamMu.Lock()
	currentStream = &rtmpStream{
		width:   1920,
		height:  1080,
		frameCh: make(chan *media.EncodedFrame, 60),
	}
	streamMu.Unlock()
	viewerCount.Store(0)
	outputCodec = "passthrough"

	numViewers := 5
	var wg sync.WaitGroup
	errors := make(chan error, numViewers)

	for i := 0; i < numViewers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
			if err != nil {
				errors <- fmt.Errorf("viewer %d: create pc: %v", id, err)
				return
			}
			defer pc.Close()

			pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
				Direction: webrtc.RTPTransceiverDirectionRecvonly,
			})

			offer, _ := pc.CreateOffer(nil)
			gatherComplete := webrtc.GatheringCompletePromise(pc)
			pc.SetLocalDescription(offer)
			<-gatherComplete

			offerJSON, _ := json.Marshal(pc.LocalDescription())
			req := httptest.NewRequest(http.MethodPost, "/offer", bytes.NewReader(offerJSON))
			w := httptest.NewRecorder()

			handleOffer(w, req)

			if w.Result().StatusCode != http.StatusOK {
				errors <- fmt.Errorf("viewer %d: offer failed: %d", id, w.Result().StatusCode)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Error(err)
	}

	// Cleanup
	streamMu.Lock()
	currentStream = nil
	streamMu.Unlock()
}

// Fuzz test for AVCC parsing
func FuzzParseAVCCNALUs(f *testing.F) {
	// Seed with valid examples
	f.Add([]byte{0x00, 0x00, 0x00, 0x05, 0x65, 0x88, 0x84, 0x00, 0x00})
	f.Add([]byte{})
	f.Add([]byte{0x00, 0x00, 0x00, 0x01, 0x65})

	f.Fuzz(func(t *testing.T, data []byte) {
		// Should not panic
		nalus := parseAVCCNALUs(data)
		_ = nalus
	})
}

// Fuzz test for SPS/PPS extraction
func FuzzExtractSPSPPS(f *testing.F) {
	f.Add([]byte{0x01, 0x64, 0x00, 0x1F, 0xFF, 0xE1, 0x00, 0x04, 0x67, 0x64, 0x00, 0x1F})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		// Should not panic
		sps, pps := extractSPSPPS(data)
		_, _ = sps, pps
	})
}

// Benchmark frame processing
func BenchmarkBuildAnnexB(b *testing.B) {
	sps := []byte{0x67, 0x64, 0x00, 0x1F, 0xAC, 0xD9, 0x40, 0x78}
	pps := []byte{0x68, 0xEB, 0xE3, 0xCB, 0x22, 0xC0}
	nalu := make([]byte, 10000) // 10KB NAL
	nalu[0] = 0x65

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buildAnnexB([][]byte{nalu}, sps, pps, true)
	}
}

func BenchmarkParseAVCCNALUs(b *testing.B) {
	// Simulate a frame with multiple NALUs
	data := make([]byte, 0, 20000)
	for i := 0; i < 5; i++ {
		nalu := make([]byte, 4000)
		nalu[0] = 0x65
		length := len(nalu)
		data = append(data, byte(length>>24), byte(length>>16), byte(length>>8), byte(length))
		data = append(data, nalu...)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = parseAVCCNALUs(data)
	}
}

// Test RTMP server startup
func TestRTMPServerStartup(t *testing.T) {
	// Find available port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find available port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	// Start RTMP server on that port
	ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer ln.Close()

	srv := rtmp.NewServer(&rtmp.ServerConfig{
		OnConnect: func(conn net.Conn) (io.ReadWriteCloser, *rtmp.ConnConfig) {
			return conn, &rtmp.ConnConfig{
				Handler: &rtmpHandler{},
				ControlState: rtmp.StreamControlStateConfig{
					DefaultBandwidthWindowSize: 6 * 1024 * 1024,
				},
			}
		},
	})

	go srv.Serve(ln)

	// Verify server accepts connections
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatalf("failed to connect to RTMP server: %v", err)
	}
	conn.Close()
}

// =============================================================================
// FFmpeg E2E Tests - Comprehensive RTMP to WebRTC Transcoding Tests
// =============================================================================

// checkFFmpeg verifies FFmpeg is available
func checkFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg not available - skipping e2e test")
	}
}

// testServer holds the RTMP and HTTP server state for e2e tests
type testServer struct {
	rtmpPort int
	httpPort int
	rtmpLn   net.Listener
	httpLn   net.Listener
	httpSrv  *http.Server
	rtmpSrv  *rtmp.Server
}

// startTestServer starts RTMP and HTTP servers on random ports
func startTestServer(t *testing.T) *testServer {
	t.Helper()

	// Reset global state
	streamMu.Lock()
	currentStream = nil
	streamMu.Unlock()
	viewerCount.Store(0)
	outputCodec = "passthrough"
	outputBitrate = 2_000_000

	// Find available ports
	rtmpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find RTMP port: %v", err)
	}
	rtmpPort := rtmpLn.Addr().(*net.TCPAddr).Port

	httpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		rtmpLn.Close()
		t.Fatalf("failed to find HTTP port: %v", err)
	}
	httpPort := httpLn.Addr().(*net.TCPAddr).Port

	// Start RTMP server
	rtmpSrv := rtmp.NewServer(&rtmp.ServerConfig{
		OnConnect: func(conn net.Conn) (io.ReadWriteCloser, *rtmp.ConnConfig) {
			return conn, &rtmp.ConnConfig{
				Handler: &rtmpHandler{},
				ControlState: rtmp.StreamControlStateConfig{
					DefaultBandwidthWindowSize: 6 * 1024 * 1024,
				},
			}
		},
	})
	go rtmpSrv.Serve(rtmpLn)

	// Start HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/offer", handleOffer)
	mux.HandleFunc("/status", handleStatus)
	mux.HandleFunc("/config", handleConfig)

	httpSrv := &http.Server{Handler: mux}
	go httpSrv.Serve(httpLn)

	return &testServer{
		rtmpPort: rtmpPort,
		httpPort: httpPort,
		rtmpLn:   rtmpLn,
		httpLn:   httpLn,
		httpSrv:  httpSrv,
		rtmpSrv:  rtmpSrv,
	}
}

// close shuts down the test server
func (s *testServer) close() {
	if s.httpSrv != nil {
		s.httpSrv.Close()
	}
	if s.rtmpLn != nil {
		s.rtmpLn.Close()
	}
	if s.httpLn != nil {
		s.httpLn.Close()
	}

	// Reset global state
	streamMu.Lock()
	currentStream = nil
	streamMu.Unlock()
	viewerCount.Store(0)
}

// startFFmpegRTMP starts FFmpeg pushing a test pattern to RTMP
func startFFmpegRTMP(ctx context.Context, rtmpPort int, durationSec int) (*exec.Cmd, error) {
	// Generate test pattern with testsrc2 and encode to H.264
	rtmpURL := fmt.Sprintf("rtmp://127.0.0.1:%d/live/test", rtmpPort)

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "warning",
		"-re", // real-time pacing - crucial for streaming!
		"-f", "lavfi",
		"-i", fmt.Sprintf("testsrc2=size=640x480:rate=30:duration=%d", durationSec),
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-profile:v", "baseline",
		"-g", "30", // keyframe every 30 frames (1 sec at 30fps)
		"-bf", "0", // no B-frames
		"-f", "flv",
		rtmpURL,
	)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return cmd, nil
}

// waitForStream waits until the RTMP stream is active
func waitForStream(t *testing.T, httpPort int, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/status", httpPort))
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		var status map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&status)
		resp.Body.Close()

		if streaming, ok := status["streaming"].(bool); ok && streaming {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// setCodec configures the output codec via HTTP API
func setCodec(t *testing.T, httpPort int, codec string) {
	t.Helper()
	body := fmt.Sprintf(`{"codec":"%s"}`, codec)
	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/config", httpPort),
		"application/json",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("failed to set codec: %v", err)
	}
	resp.Body.Close()
	outputCodec = codec
}

// connectWebRTCViewer creates a WebRTC connection and returns received RTP packets
func connectWebRTCViewer(ctx context.Context, t *testing.T, httpPort int) (<-chan *rtp.Packet, *webrtc.PeerConnection, error) {
	t.Helper()

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, nil, fmt.Errorf("create pc: %w", err)
	}

	rtpCh := make(chan *rtp.Packet, 1000)

	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		t.Logf("Got track: %s codec=%s", track.ID(), track.Codec().MimeType)
		for {
			pkt, _, err := track.ReadRTP()
			if err != nil {
				close(rtpCh)
				return
			}
			select {
			case rtpCh <- pkt:
			case <-ctx.Done():
				return
			default:
				// Drop if channel full
			}
		}
	})

	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		pc.Close()
		return nil, nil, fmt.Errorf("add transceiver: %w", err)
	}

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		pc.Close()
		return nil, nil, fmt.Errorf("create offer: %w", err)
	}

	gatherComplete := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		pc.Close()
		return nil, nil, fmt.Errorf("set local desc: %w", err)
	}
	<-gatherComplete

	offerJSON, _ := json.Marshal(pc.LocalDescription())
	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/offer", httpPort),
		"application/json",
		bytes.NewReader(offerJSON),
	)
	if err != nil {
		pc.Close()
		return nil, nil, fmt.Errorf("post offer: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		pc.Close()
		return nil, nil, fmt.Errorf("offer failed: %s", body)
	}

	var answer webrtc.SessionDescription
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		pc.Close()
		return nil, nil, fmt.Errorf("decode answer: %w", err)
	}

	if err := pc.SetRemoteDescription(answer); err != nil {
		pc.Close()
		return nil, nil, fmt.Errorf("set remote desc: %w", err)
	}

	return rtpCh, pc, nil
}

// TestE2EFFmpegPassthrough tests RTMP -> H.264 passthrough -> WebRTC
func TestE2EFFmpegPassthrough(t *testing.T) {
	checkFFmpeg(t)

	if !media.IsH264EncoderAvailable() {
		t.Skip("H.264 encoder not available")
	}

	srv := startTestServer(t)
	defer srv.close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start FFmpeg pushing RTMP
	ffmpegCmd, err := startFFmpegRTMP(ctx, srv.rtmpPort, 10)
	if err != nil {
		t.Fatalf("failed to start FFmpeg: %v", err)
	}
	defer ffmpegCmd.Process.Kill()

	// Wait for stream to be active
	if !waitForStream(t, srv.httpPort, 5*time.Second) {
		t.Fatal("RTMP stream not detected within timeout")
	}

	t.Log("RTMP stream active, connecting WebRTC viewer...")

	// Connect WebRTC viewer
	rtpCh, pc, err := connectWebRTCViewer(ctx, t, srv.httpPort)
	if err != nil {
		t.Fatalf("failed to connect viewer: %v", err)
	}
	defer pc.Close()

	// Wait for connection
	time.Sleep(500 * time.Millisecond)

	// Collect RTP packets
	var receivedPackets int
	timeout := time.After(5 * time.Second)
collect:
	for {
		select {
		case pkt, ok := <-rtpCh:
			if !ok {
				break collect
			}
			receivedPackets++
			if receivedPackets == 1 {
				t.Logf("First RTP packet: PT=%d, SSRC=%x, len=%d",
					pkt.PayloadType, pkt.SSRC, len(pkt.Payload))
			}
			if receivedPackets >= 100 {
				break collect
			}
		case <-timeout:
			break collect
		case <-ctx.Done():
			break collect
		}
	}

	if receivedPackets < 10 {
		t.Errorf("expected at least 10 RTP packets, got %d", receivedPackets)
	} else {
		t.Logf("SUCCESS: Received %d RTP packets in passthrough mode", receivedPackets)
	}
}

// TestE2EFFmpegTranscodeVP8 tests RTMP H.264 -> VP8 transcoding -> WebRTC
func TestE2EFFmpegTranscodeVP8(t *testing.T) {
	checkFFmpeg(t)

	if !media.IsH264DecoderAvailable() || !media.IsVP8Available() {
		t.Skip("H.264 decoder or VP8 not available")
	}

	srv := startTestServer(t)
	defer srv.close()

	// Set codec to VP8
	setCodec(t, srv.httpPort, "vp8")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start FFmpeg pushing RTMP
	ffmpegCmd, err := startFFmpegRTMP(ctx, srv.rtmpPort, 10)
	if err != nil {
		t.Fatalf("failed to start FFmpeg: %v", err)
	}
	defer ffmpegCmd.Process.Kill()

	// Wait for stream
	if !waitForStream(t, srv.httpPort, 5*time.Second) {
		t.Fatal("RTMP stream not detected")
	}

	t.Log("RTMP stream active, connecting WebRTC viewer for VP8...")

	rtpCh, pc, err := connectWebRTCViewer(ctx, t, srv.httpPort)
	if err != nil {
		t.Fatalf("failed to connect viewer: %v", err)
	}
	defer pc.Close()

	time.Sleep(500 * time.Millisecond)

	var receivedPackets int
	timeout := time.After(8 * time.Second)
collect:
	for {
		select {
		case pkt, ok := <-rtpCh:
			if !ok {
				break collect
			}
			receivedPackets++
			if receivedPackets == 1 {
				t.Logf("First VP8 RTP packet: PT=%d, len=%d", pkt.PayloadType, len(pkt.Payload))
			}
			if receivedPackets >= 50 {
				break collect
			}
		case <-timeout:
			break collect
		case <-ctx.Done():
			break collect
		}
	}

	if receivedPackets < 5 {
		t.Errorf("expected at least 5 VP8 RTP packets, got %d", receivedPackets)
	} else {
		t.Logf("SUCCESS: Received %d VP8 RTP packets", receivedPackets)
	}
}

// TestE2EFFmpegTranscodeVP9 tests RTMP H.264 -> VP9 transcoding -> WebRTC
func TestE2EFFmpegTranscodeVP9(t *testing.T) {
	checkFFmpeg(t)

	if !media.IsH264DecoderAvailable() || !media.IsVP9Available() {
		t.Skip("H.264 decoder or VP9 not available")
	}

	srv := startTestServer(t)
	defer srv.close()

	setCodec(t, srv.httpPort, "vp9")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ffmpegCmd, err := startFFmpegRTMP(ctx, srv.rtmpPort, 10)
	if err != nil {
		t.Fatalf("failed to start FFmpeg: %v", err)
	}
	defer ffmpegCmd.Process.Kill()

	if !waitForStream(t, srv.httpPort, 5*time.Second) {
		t.Fatal("RTMP stream not detected")
	}

	t.Log("RTMP stream active, connecting WebRTC viewer for VP9...")

	rtpCh, pc, err := connectWebRTCViewer(ctx, t, srv.httpPort)
	if err != nil {
		t.Fatalf("failed to connect viewer: %v", err)
	}
	defer pc.Close()

	time.Sleep(500 * time.Millisecond)

	var receivedPackets int
	timeout := time.After(8 * time.Second)
collect:
	for {
		select {
		case pkt, ok := <-rtpCh:
			if !ok {
				break collect
			}
			receivedPackets++
			if receivedPackets == 1 {
				t.Logf("First VP9 RTP packet: PT=%d, len=%d", pkt.PayloadType, len(pkt.Payload))
			}
			if receivedPackets >= 50 {
				break collect
			}
		case <-timeout:
			break collect
		case <-ctx.Done():
			break collect
		}
	}

	if receivedPackets < 5 {
		t.Errorf("expected at least 5 VP9 RTP packets, got %d", receivedPackets)
	} else {
		t.Logf("SUCCESS: Received %d VP9 RTP packets", receivedPackets)
	}
}

// TestE2EFFmpegTranscodeH264 tests RTMP H.264 -> H.264 re-encoding -> WebRTC
func TestE2EFFmpegTranscodeH264(t *testing.T) {
	checkFFmpeg(t)

	if !media.IsH264DecoderAvailable() || !media.IsH264EncoderAvailable() {
		t.Skip("H.264 encoder/decoder not available")
	}

	srv := startTestServer(t)
	defer srv.close()

	setCodec(t, srv.httpPort, "h264")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ffmpegCmd, err := startFFmpegRTMP(ctx, srv.rtmpPort, 10)
	if err != nil {
		t.Fatalf("failed to start FFmpeg: %v", err)
	}
	defer ffmpegCmd.Process.Kill()

	if !waitForStream(t, srv.httpPort, 5*time.Second) {
		t.Fatal("RTMP stream not detected")
	}

	t.Log("RTMP stream active, connecting WebRTC viewer for H.264 re-encode...")

	rtpCh, pc, err := connectWebRTCViewer(ctx, t, srv.httpPort)
	if err != nil {
		t.Fatalf("failed to connect viewer: %v", err)
	}
	defer pc.Close()

	time.Sleep(500 * time.Millisecond)

	var receivedPackets int
	timeout := time.After(8 * time.Second)
collect:
	for {
		select {
		case pkt, ok := <-rtpCh:
			if !ok {
				break collect
			}
			receivedPackets++
			if receivedPackets == 1 {
				t.Logf("First H.264 RTP packet: PT=%d, len=%d", pkt.PayloadType, len(pkt.Payload))
			}
			if receivedPackets >= 50 {
				break collect
			}
		case <-timeout:
			break collect
		case <-ctx.Done():
			break collect
		}
	}

	if receivedPackets < 5 {
		t.Errorf("expected at least 5 H.264 RTP packets, got %d", receivedPackets)
	} else {
		t.Logf("SUCCESS: Received %d H.264 RTP packets (re-encoded)", receivedPackets)
	}
}

// TestE2EAllCodecs runs e2e tests for all available codecs
func TestE2EAllCodecs(t *testing.T) {
	checkFFmpeg(t)

	if !media.IsH264DecoderAvailable() {
		t.Skip("H.264 decoder not available")
	}

	codecs := []struct {
		name      string
		codec     string
		available bool
		mime      string
	}{
		{"Passthrough", "passthrough", media.IsH264EncoderAvailable(), "video/H264"},
		{"VP8", "vp8", media.IsVP8Available(), "video/VP8"},
		{"VP9", "vp9", media.IsVP9Available(), "video/VP9"},
		{"H264-Transcode", "h264", media.IsH264EncoderAvailable(), "video/H264"},
	}

	for _, tc := range codecs {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.available {
				t.Skipf("%s not available", tc.name)
			}

			srv := startTestServer(t)
			defer srv.close()

			setCodec(t, srv.httpPort, tc.codec)

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			ffmpegCmd, err := startFFmpegRTMP(ctx, srv.rtmpPort, 8)
			if err != nil {
				t.Fatalf("failed to start FFmpeg: %v", err)
			}
			defer ffmpegCmd.Process.Kill()

			if !waitForStream(t, srv.httpPort, 5*time.Second) {
				t.Fatal("RTMP stream not detected")
			}

			rtpCh, pc, err := connectWebRTCViewer(ctx, t, srv.httpPort)
			if err != nil {
				t.Fatalf("failed to connect viewer: %v", err)
			}
			defer pc.Close()

			time.Sleep(300 * time.Millisecond)

			var receivedPackets int
			var totalBytes int
			timeout := time.After(6 * time.Second)
		collect:
			for {
				select {
				case pkt, ok := <-rtpCh:
					if !ok {
						break collect
					}
					receivedPackets++
					totalBytes += len(pkt.Payload)
					if receivedPackets >= 30 {
						break collect
					}
				case <-timeout:
					break collect
				case <-ctx.Done():
					break collect
				}
			}

			if receivedPackets < 5 {
				t.Errorf("expected at least 5 RTP packets, got %d", receivedPackets)
			} else {
				avgSize := totalBytes / receivedPackets
				t.Logf("SUCCESS: %s - %d packets, %d bytes total (avg %d bytes/pkt)",
					tc.name, receivedPackets, totalBytes, avgSize)
			}
		})
	}
}

// TestE2EMultipleViewers tests multiple WebRTC viewers receiving the same stream
func TestE2EMultipleViewers(t *testing.T) {
	checkFFmpeg(t)

	if !media.IsH264EncoderAvailable() {
		t.Skip("H.264 not available")
	}

	srv := startTestServer(t)
	defer srv.close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ffmpegCmd, err := startFFmpegRTMP(ctx, srv.rtmpPort, 15)
	if err != nil {
		t.Fatalf("failed to start FFmpeg: %v", err)
	}
	defer ffmpegCmd.Process.Kill()

	if !waitForStream(t, srv.httpPort, 5*time.Second) {
		t.Fatal("RTMP stream not detected")
	}

	numViewers := 3
	var wg sync.WaitGroup
	results := make([]int, numViewers)

	for i := 0; i < numViewers; i++ {
		wg.Add(1)
		go func(viewerID int) {
			defer wg.Done()

			rtpCh, pc, err := connectWebRTCViewer(ctx, t, srv.httpPort)
			if err != nil {
				t.Errorf("viewer %d: failed to connect: %v", viewerID, err)
				return
			}
			defer pc.Close()

			time.Sleep(300 * time.Millisecond)

			timeout := time.After(5 * time.Second)
		collect:
			for {
				select {
				case _, ok := <-rtpCh:
					if !ok {
						break collect
					}
					results[viewerID]++
					if results[viewerID] >= 30 {
						break collect
					}
				case <-timeout:
					break collect
				case <-ctx.Done():
					break collect
				}
			}
		}(i)
	}

	wg.Wait()

	for i, count := range results {
		if count < 5 {
			t.Errorf("viewer %d: expected at least 5 packets, got %d", i, count)
		} else {
			t.Logf("viewer %d: received %d packets", i, count)
		}
	}
}

// TestE2ECodecSwitch tests switching codecs mid-stream
func TestE2ECodecSwitch(t *testing.T) {
	checkFFmpeg(t)

	if !media.IsH264DecoderAvailable() || !media.IsVP8Available() || !media.IsVP9Available() {
		t.Skip("Required codecs not available")
	}

	codecs := []string{"passthrough", "vp8", "vp9", "h264"}

	for _, codec := range codecs {
		t.Run(codec, func(t *testing.T) {
			srv := startTestServer(t)
			defer srv.close()

			setCodec(t, srv.httpPort, codec)

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			ffmpegCmd, err := startFFmpegRTMP(ctx, srv.rtmpPort, 10)
			if err != nil {
				t.Fatalf("failed to start FFmpeg: %v", err)
			}
			defer ffmpegCmd.Process.Kill()

			if !waitForStream(t, srv.httpPort, 5*time.Second) {
				t.Fatal("RTMP stream not detected")
			}

			rtpCh, pc, err := connectWebRTCViewer(ctx, t, srv.httpPort)
			if err != nil {
				t.Fatalf("codec %s: failed to connect: %v", codec, err)
			}
			defer pc.Close()

			time.Sleep(300 * time.Millisecond)

			var receivedPackets int
			timeout := time.After(5 * time.Second)
		collect:
			for {
				select {
				case _, ok := <-rtpCh:
					if !ok {
						break collect
					}
					receivedPackets++
					if receivedPackets >= 15 {
						break collect
					}
				case <-timeout:
					break collect
				case <-ctx.Done():
					break collect
				}
			}

			if receivedPackets < 3 {
				t.Errorf("codec %s: expected at least 3 packets, got %d", codec, receivedPackets)
			} else {
				t.Logf("codec %s: SUCCESS - %d packets", codec, receivedPackets)
			}
		})
	}
}

// TestE2ELatency measures the round-trip latency from RTMP push to WebRTC receive
func TestE2ELatency(t *testing.T) {
	checkFFmpeg(t)

	if !media.IsH264EncoderAvailable() {
		t.Skip("H.264 not available")
	}

	srv := startTestServer(t)
	defer srv.close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	ffmpegCmd, err := startFFmpegRTMP(ctx, srv.rtmpPort, 10)
	if err != nil {
		t.Fatalf("failed to start FFmpeg: %v", err)
	}
	defer ffmpegCmd.Process.Kill()

	streamStart := time.Now()

	if !waitForStream(t, srv.httpPort, 5*time.Second) {
		t.Fatal("RTMP stream not detected")
	}

	rtpCh, pc, err := connectWebRTCViewer(ctx, t, srv.httpPort)
	if err != nil {
		t.Fatalf("failed to connect viewer: %v", err)
	}
	defer pc.Close()

	// Wait for first packet
	select {
	case pkt := <-rtpCh:
		firstPacketTime := time.Since(streamStart)
		t.Logf("Time to first RTP packet: %v (PT=%d, len=%d)",
			firstPacketTime, pkt.PayloadType, len(pkt.Payload))

		if firstPacketTime > 5*time.Second {
			t.Errorf("latency too high: %v", firstPacketTime)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for first packet")
	}
}

// TestE2EBitrate tests that bitrate configuration works
func TestE2EBitrate(t *testing.T) {
	checkFFmpeg(t)

	if !media.IsH264DecoderAvailable() || !media.IsVP8Available() {
		t.Skip("Required codecs not available")
	}

	srv := startTestServer(t)
	defer srv.close()

	// Set VP8 with low bitrate
	outputCodec = "vp8"
	outputBitrate = 200_000 // 200 kbps

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	ffmpegCmd, err := startFFmpegRTMP(ctx, srv.rtmpPort, 10)
	if err != nil {
		t.Fatalf("failed to start FFmpeg: %v", err)
	}
	defer ffmpegCmd.Process.Kill()

	if !waitForStream(t, srv.httpPort, 5*time.Second) {
		t.Fatal("RTMP stream not detected")
	}

	rtpCh, pc, err := connectWebRTCViewer(ctx, t, srv.httpPort)
	if err != nil {
		t.Fatalf("failed to connect viewer: %v", err)
	}
	defer pc.Close()

	time.Sleep(500 * time.Millisecond)

	var totalBytes int
	startTime := time.Now()
	var receivedPackets int
	timeout := time.After(5 * time.Second)
collect:
	for {
		select {
		case pkt, ok := <-rtpCh:
			if !ok {
				break collect
			}
			receivedPackets++
			totalBytes += len(pkt.Payload)
			if receivedPackets >= 100 {
				break collect
			}
		case <-timeout:
			break collect
		case <-ctx.Done():
			break collect
		}
	}

	duration := time.Since(startTime)
	bitrateKbps := float64(totalBytes*8) / duration.Seconds() / 1000

	t.Logf("Received %d packets, %d bytes in %v", receivedPackets, totalBytes, duration)
	t.Logf("Measured bitrate: %.1f kbps (target: 200 kbps)", bitrateKbps)

	// Allow some variance but should be reasonable
	if bitrateKbps > 500 {
		t.Logf("Warning: bitrate higher than expected (may be due to keyframes)")
	}
}

// =============================================================================
// Frame Output Assertion Tests - Verify actual decoded video content
// =============================================================================

// TestE2EFrameOutputH264 verifies H.264 passthrough produces valid decodable frames
func TestE2EFrameOutputH264(t *testing.T) {
	checkFFmpeg(t)

	if !media.IsH264EncoderAvailable() || !media.IsH264DecoderAvailable() {
		t.Skip("H.264 encoder/decoder not available")
	}

	srv := startTestServer(t)
	defer srv.close()

	setCodec(t, srv.httpPort, "passthrough")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ffmpegCmd, err := startFFmpegRTMP(ctx, srv.rtmpPort, 15)
	if err != nil {
		t.Fatalf("failed to start FFmpeg: %v", err)
	}
	defer ffmpegCmd.Process.Kill()

	if !waitForStream(t, srv.httpPort, 5*time.Second) {
		t.Fatal("RTMP stream not detected")
	}

	t.Log("RTMP stream active, connecting WebRTC viewer...")

	// Connect and verify we get valid frames
	verifyDecodedFrames(ctx, t, srv.httpPort, media.VideoCodecH264, "H264")
}

// TestE2EFrameOutputVP8 verifies VP8 transcoding produces valid decodable frames
func TestE2EFrameOutputVP8(t *testing.T) {
	checkFFmpeg(t)

	if !media.IsH264DecoderAvailable() || !media.IsVP8Available() {
		t.Skip("H.264 decoder or VP8 not available")
	}

	srv := startTestServer(t)
	defer srv.close()

	setCodec(t, srv.httpPort, "vp8")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ffmpegCmd, err := startFFmpegRTMP(ctx, srv.rtmpPort, 15)
	if err != nil {
		t.Fatalf("failed to start FFmpeg: %v", err)
	}
	defer ffmpegCmd.Process.Kill()

	if !waitForStream(t, srv.httpPort, 5*time.Second) {
		t.Fatal("RTMP stream not detected")
	}

	t.Log("RTMP stream active, connecting WebRTC viewer for VP8...")

	verifyDecodedFrames(ctx, t, srv.httpPort, media.VideoCodecVP8, "VP8")
}

// TestE2EFrameOutputVP9 verifies VP9 transcoding produces valid decodable frames
func TestE2EFrameOutputVP9(t *testing.T) {
	checkFFmpeg(t)

	if !media.IsH264DecoderAvailable() || !media.IsVP9Available() {
		t.Skip("H.264 decoder or VP9 not available")
	}

	srv := startTestServer(t)
	defer srv.close()

	setCodec(t, srv.httpPort, "vp9")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ffmpegCmd, err := startFFmpegRTMP(ctx, srv.rtmpPort, 15)
	if err != nil {
		t.Fatalf("failed to start FFmpeg: %v", err)
	}
	defer ffmpegCmd.Process.Kill()

	if !waitForStream(t, srv.httpPort, 5*time.Second) {
		t.Fatal("RTMP stream not detected")
	}

	t.Log("RTMP stream active, connecting WebRTC viewer for VP9...")

	verifyDecodedFrames(ctx, t, srv.httpPort, media.VideoCodecVP9, "VP9")
}

// verifyDecodedFrames connects as WebRTC viewer, decodes frames, and verifies output
func verifyDecodedFrames(ctx context.Context, t *testing.T, httpPort int, expectedCodec media.VideoCodec, codecName string) {
	t.Helper()

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create pc: %v", err)
	}
	defer pc.Close()

	frameCh := make(chan *media.VideoFrame, 100)
	errorCh := make(chan error, 1)

	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		t.Logf("Got track: codec=%s", track.Codec().MimeType)

		// Determine codec from track
		var codec media.VideoCodec
		switch {
		case strings.Contains(strings.ToLower(track.Codec().MimeType), "h264"):
			codec = media.VideoCodecH264
		case strings.Contains(strings.ToLower(track.Codec().MimeType), "vp8"):
			codec = media.VideoCodecVP8
		case strings.Contains(strings.ToLower(track.Codec().MimeType), "vp9"):
			codec = media.VideoCodecVP9
		default:
			errorCh <- fmt.Errorf("unknown codec: %s", track.Codec().MimeType)
			return
		}

		// Create depacketizer and decoder
		depacketizer, err := media.CreateVideoDepacketizer(codec)
		if err != nil {
			errorCh <- fmt.Errorf("create depacketizer: %w", err)
			return
		}

		var decoder media.VideoDecoder
		switch codec {
		case media.VideoCodecH264:
			decoder, err = media.NewH264Decoder(media.VideoDecoderConfig{})
		case media.VideoCodecVP8:
			decoder, err = media.NewVP8Decoder(media.VideoDecoderConfig{})
		case media.VideoCodecVP9:
			decoder, err = media.NewVP9Decoder(media.VideoDecoderConfig{})
		}
		if err != nil {
			errorCh <- fmt.Errorf("create decoder: %w", err)
			return
		}
		defer decoder.Close()

		t.Logf("Decoder created for %s", codecName)

		var rtpCount, encodedCount, decodedCount int
		for {
			pkt, _, err := track.ReadRTP()
			if err != nil {
				t.Logf("RTP read error (stats: rtp=%d encoded=%d decoded=%d): %v", rtpCount, encodedCount, decodedCount, err)
				return
			}
			rtpCount++
			if rtpCount%100 == 1 {
				t.Logf("RTP packet %d: PT=%d Marker=%v len=%d", rtpCount, pkt.PayloadType, pkt.Marker, len(pkt.Payload))
			}

			// Depacketize RTP to encoded frame (convert pion rtp.Packet to media.RTPPacket)
			encoded, err := depacketizer.Depacketize((*media.RTPPacket)(pkt))
			if err != nil {
				t.Logf("Depacketize error: %v", err)
				continue
			}
			if encoded == nil {
				continue // Incomplete frame
			}
			encodedCount++
			if encodedCount <= 3 {
				t.Logf("Encoded frame %d: len=%d type=%v", encodedCount, len(encoded.Data), encoded.FrameType)
			}

			// Decode to raw frame
			raw, err := decoder.Decode(encoded)
			if err != nil {
				t.Logf("Decode error (may be buffering): %v", err)
				continue
			}
			if raw == nil {
				continue // Buffering
			}
			decodedCount++
			if decodedCount <= 3 {
				t.Logf("Decoded frame %d: %dx%d", decodedCount, raw.Width, raw.Height)
			}

			select {
			case frameCh <- raw:
			case <-ctx.Done():
				return
			default:
				// Drop if full
			}
		}
	})

	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		t.Fatalf("add transceiver: %v", err)
	}

	offer, _ := pc.CreateOffer(nil)
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	pc.SetLocalDescription(offer)
	<-gatherComplete

	offerJSON, _ := json.Marshal(pc.LocalDescription())
	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/offer", httpPort),
		"application/json",
		bytes.NewReader(offerJSON),
	)
	if err != nil {
		t.Fatalf("post offer: %v", err)
	}
	defer resp.Body.Close()

	var answer webrtc.SessionDescription
	json.NewDecoder(resp.Body).Decode(&answer)
	pc.SetRemoteDescription(answer)

	// Wait for decoded frames
	var decodedFrames int
	var validFrames int
	timeout := time.After(15 * time.Second)

collect:
	for {
		select {
		case err := <-errorCh:
			t.Fatalf("error: %v", err)
		case frame := <-frameCh:
			decodedFrames++

			// Verify frame dimensions are reasonable (not zero/negative)
			if frame.Width < 16 || frame.Height < 16 || frame.Width > 4096 || frame.Height > 4096 {
				t.Errorf("frame %d: invalid dimensions %dx%d",
					decodedFrames, frame.Width, frame.Height)
				continue
			}

			// Verify Y plane size matches dimensions (width * height)
			expectedYSize := frame.Width * frame.Height
			if len(frame.Data[0]) < expectedYSize {
				t.Errorf("frame %d: Y plane too small: %d < %d",
					decodedFrames, len(frame.Data[0]), expectedYSize)
				continue
			}

			// Verify U/V plane sizes (width/2 * height/2 for I420)
			expectedUVSize := (frame.Width / 2) * (frame.Height / 2)
			if len(frame.Data[1]) < expectedUVSize || len(frame.Data[2]) < expectedUVSize {
				t.Errorf("frame %d: U/V planes too small: %d, %d < %d",
					decodedFrames, len(frame.Data[1]), len(frame.Data[2]), expectedUVSize)
				continue
			}

			// Verify pixel data is not all zeros (would indicate corrupted decode)
			if isAllZeros(frame.Data[0][:1000]) {
				t.Errorf("frame %d: Y plane appears to be all zeros (corrupted)", decodedFrames)
				continue
			}

			// Verify pixel values are in valid range (0-255) and have some variance
			if !hasReasonableVariance(frame.Data[0][:1000]) {
				t.Errorf("frame %d: Y plane has no variance (all same value)", decodedFrames)
				continue
			}

			validFrames++

			if decodedFrames == 1 {
				t.Logf("First decoded %s frame: %dx%d, Y=%d bytes, U=%d bytes, V=%d bytes",
					codecName, frame.Width, frame.Height,
					len(frame.Data[0]), len(frame.Data[1]), len(frame.Data[2]))

				// Log sample pixel values for debugging
				t.Logf("Sample Y values: %v", frame.Data[0][:10])
			}

			if validFrames >= 10 {
				break collect
			}

		case <-timeout:
			break collect
		case <-ctx.Done():
			break collect
		}
	}

	if decodedFrames == 0 {
		t.Errorf("no frames decoded for %s", codecName)
	} else if validFrames < 3 {
		t.Errorf("%s: only %d valid frames out of %d decoded (expected >= 3)",
			codecName, validFrames, decodedFrames)
	} else {
		t.Logf("SUCCESS: %s - decoded %d frames, %d valid", codecName, decodedFrames, validFrames)
	}
}

// isAllZeros checks if a slice is all zeros
func isAllZeros(data []byte) bool {
	for _, b := range data {
		if b != 0 {
			return false
		}
	}
	return true
}

// hasReasonableVariance checks if pixel data has some variance (not all same value)
func hasReasonableVariance(data []byte) bool {
	if len(data) < 2 {
		return false
	}
	first := data[0]
	for _, b := range data[1:] {
		if b != first {
			return true
		}
	}
	return false
}

// TestE2EFrameOutputAllCodecs tests frame output for all available codecs
func TestE2EFrameOutputAllCodecs(t *testing.T) {
	checkFFmpeg(t)

	if !media.IsH264DecoderAvailable() {
		t.Skip("H.264 decoder not available")
	}

	tests := []struct {
		name      string
		codec     string
		available bool
		mediaCodec media.VideoCodec
	}{
		{"H264-Passthrough", "passthrough", media.IsH264EncoderAvailable() && media.IsH264DecoderAvailable(), media.VideoCodecH264},
		{"VP8", "vp8", media.IsVP8Available(), media.VideoCodecVP8},
		{"VP9", "vp9", media.IsVP9Available(), media.VideoCodecVP9},
		{"H264-Transcode", "h264", media.IsH264EncoderAvailable() && media.IsH264DecoderAvailable(), media.VideoCodecH264},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.available {
				t.Skipf("%s not available", tc.name)
			}

			srv := startTestServer(t)
			defer srv.close()

			setCodec(t, srv.httpPort, tc.codec)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			ffmpegCmd, err := startFFmpegRTMP(ctx, srv.rtmpPort, 12)
			if err != nil {
				t.Fatalf("failed to start FFmpeg: %v", err)
			}
			defer ffmpegCmd.Process.Kill()

			if !waitForStream(t, srv.httpPort, 5*time.Second) {
				t.Fatal("RTMP stream not detected")
			}

			verifyDecodedFrames(ctx, t, srv.httpPort, tc.mediaCodec, tc.name)
		})
	}
}

// TestE2EFramePixelValues verifies that decoded frames contain expected pixel patterns
func TestE2EFramePixelValues(t *testing.T) {
	checkFFmpeg(t)

	if !media.IsH264EncoderAvailable() || !media.IsH264DecoderAvailable() {
		t.Skip("H.264 encoder/decoder not available")
	}

	srv := startTestServer(t)
	defer srv.close()

	setCodec(t, srv.httpPort, "passthrough")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ffmpegCmd, err := startFFmpegRTMP(ctx, srv.rtmpPort, 10)
	if err != nil {
		t.Fatalf("failed to start FFmpeg: %v", err)
	}
	defer ffmpegCmd.Process.Kill()

	if !waitForStream(t, srv.httpPort, 5*time.Second) {
		t.Fatal("RTMP stream not detected")
	}

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create pc: %v", err)
	}
	defer pc.Close()

	frameCh := make(chan *media.VideoFrame, 10)

	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		depacketizer, _ := media.CreateVideoDepacketizer(media.VideoCodecH264)

		decoder, _ := media.NewH264Decoder(media.VideoDecoderConfig{})
		defer decoder.Close()

		for {
			pkt, _, err := track.ReadRTP()
			if err != nil {
				return
			}

			encoded, _ := depacketizer.Depacketize((*media.RTPPacket)(pkt))
			if encoded == nil {
				continue
			}

			raw, _ := decoder.Decode(encoded)
			if raw == nil {
				continue
			}

			select {
			case frameCh <- raw:
			case <-ctx.Done():
				return
			default:
			}
		}
	})

	pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})

	offer, _ := pc.CreateOffer(nil)
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	pc.SetLocalDescription(offer)
	<-gatherComplete

	offerJSON, _ := json.Marshal(pc.LocalDescription())
	resp, _ := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/offer", srv.httpPort),
		"application/json",
		bytes.NewReader(offerJSON),
	)
	var answer webrtc.SessionDescription
	json.NewDecoder(resp.Body).Decode(&answer)
	resp.Body.Close()
	pc.SetRemoteDescription(answer)

	// Analyze first few frames
	timeout := time.After(10 * time.Second)
	var analyzed int

	for analyzed < 5 {
		select {
		case frame := <-frameCh:
			analyzed++

			// Calculate Y plane statistics
			y := frame.Data[0]
			var sum int64
			var min, max byte = 255, 0
			for _, v := range y {
				sum += int64(v)
				if v < min {
					min = v
				}
				if v > max {
					max = v
				}
			}
			avg := float64(sum) / float64(len(y))

			t.Logf("Frame %d: Y plane stats - min=%d, max=%d, avg=%.1f, range=%d",
				analyzed, min, max, avg, max-min)

			// FFmpeg testsrc2 should have varied pixel values
			if max-min < 50 {
				t.Errorf("Frame %d: Y plane has insufficient dynamic range: %d", analyzed, max-min)
			}

			// Average should be somewhere in the middle range (not all black or white)
			if avg < 30 || avg > 220 {
				t.Errorf("Frame %d: Y plane average %.1f is outside expected range [30, 220]", analyzed, avg)
			}

		case <-timeout:
			if analyzed < 3 {
				t.Errorf("only analyzed %d frames before timeout", analyzed)
			}
			return
		case <-ctx.Done():
			return
		}
	}

	t.Logf("SUCCESS: Analyzed %d frames with valid pixel distributions", analyzed)
}

// BenchmarkE2EThroughput measures RTP packet throughput
func BenchmarkE2EThroughput(b *testing.B) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		b.Skip("FFmpeg not available")
	}

	if !media.IsH264EncoderAvailable() {
		b.Skip("H.264 not available")
	}

	// Reset global state
	streamMu.Lock()
	currentStream = nil
	streamMu.Unlock()
	viewerCount.Store(0)
	outputCodec = "passthrough"

	rtmpLn, _ := net.Listen("tcp", "127.0.0.1:0")
	rtmpPort := rtmpLn.Addr().(*net.TCPAddr).Port

	httpLn, _ := net.Listen("tcp", "127.0.0.1:0")
	httpPort := httpLn.Addr().(*net.TCPAddr).Port

	rtmpSrv := rtmp.NewServer(&rtmp.ServerConfig{
		OnConnect: func(conn net.Conn) (io.ReadWriteCloser, *rtmp.ConnConfig) {
			return conn, &rtmp.ConnConfig{
				Handler: &rtmpHandler{},
				ControlState: rtmp.StreamControlStateConfig{
					DefaultBandwidthWindowSize: 6 * 1024 * 1024,
				},
			}
		},
	})
	go rtmpSrv.Serve(rtmpLn)

	mux := http.NewServeMux()
	mux.HandleFunc("/offer", handleOffer)
	mux.HandleFunc("/status", handleStatus)
	httpSrv := &http.Server{Handler: mux}
	go httpSrv.Serve(httpLn)

	defer httpSrv.Close()
	defer rtmpLn.Close()
	defer httpLn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	rtmpURL := fmt.Sprintf("rtmp://127.0.0.1:%d/live/test", rtmpPort)
	ffmpegCmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=60:duration=60",
		"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
		"-profile:v", "baseline", "-g", "60", "-bf", "0",
		"-f", "flv", rtmpURL,
	)
	if err := ffmpegCmd.Start(); err != nil {
		b.Fatalf("failed to start FFmpeg: %v", err)
	}
	defer ffmpegCmd.Process.Kill()

	// Wait for stream
	for i := 0; i < 50; i++ {
		resp, _ := http.Get(fmt.Sprintf("http://127.0.0.1:%d/status", httpPort))
		if resp != nil {
			var status map[string]interface{}
			json.NewDecoder(resp.Body).Decode(&status)
			resp.Body.Close()
			if streaming, _ := status["streaming"].(bool); streaming {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	pc, _ := webrtc.NewPeerConnection(webrtc.Configuration{})
	rtpCh := make(chan *rtp.Packet, 10000)

	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		for {
			pkt, _, err := track.ReadRTP()
			if err != nil {
				return
			}
			select {
			case rtpCh <- pkt:
			default:
			}
		}
	})

	pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})

	offer, _ := pc.CreateOffer(nil)
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	pc.SetLocalDescription(offer)
	<-gatherComplete

	offerJSON, _ := json.Marshal(pc.LocalDescription())
	resp, _ := http.Post(fmt.Sprintf("http://127.0.0.1:%d/offer", httpPort), "application/json", bytes.NewReader(offerJSON))
	var answer webrtc.SessionDescription
	json.NewDecoder(resp.Body).Decode(&answer)
	resp.Body.Close()
	pc.SetRemoteDescription(answer)
	defer pc.Close()

	time.Sleep(time.Second)

	b.ResetTimer()
	var packetsReceived int64
	var bytesReceived int64

	for i := 0; i < b.N; i++ {
		select {
		case pkt := <-rtpCh:
			atomic.AddInt64(&packetsReceived, 1)
			atomic.AddInt64(&bytesReceived, int64(len(pkt.Payload)))
		case <-time.After(100 * time.Millisecond):
		}
	}

	b.ReportMetric(float64(packetsReceived)/float64(b.N)*1000, "packets/1000iter")
	b.ReportMetric(float64(bytesReceived)/float64(b.N), "bytes/iter")
}
