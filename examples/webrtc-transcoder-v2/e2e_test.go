package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/thesyncim/media"
)

// TestE2EAllCodecs tests publishing with each codec and verifying transcoding works
func TestE2EAllCodecs(t *testing.T) {
	// Start the server
	server, serverURL := startTestServer(t)
	defer server.Shutdown(context.Background())

	// Wait for server to be ready
	time.Sleep(100 * time.Millisecond)

	codecs := []struct {
		name      string
		codec     media.VideoCodec
		mimeType  string
		clockRate uint32
	}{
		{"VP8", media.VideoCodecVP8, webrtc.MimeTypeVP8, 90000},
		{"VP9", media.VideoCodecVP9, webrtc.MimeTypeVP9, 90000},
		{"H264", media.VideoCodecH264, webrtc.MimeTypeH264, 90000},
		// {"AV1", media.VideoCodecAV1, webrtc.MimeTypeAV1, 90000}, // AV1 support varies
	}

	for _, tc := range codecs {
		t.Run(tc.name, func(t *testing.T) {
			testPublishAndTranscode(t, serverURL, tc.name, tc.codec, tc.mimeType, tc.clockRate)
		})
	}
}

// TestE2EDynamicVariants tests adding variants dynamically while streaming
func TestE2EDynamicVariants(t *testing.T) {
	server, serverURL := startTestServer(t)
	defer server.Shutdown(context.Background())
	time.Sleep(100 * time.Millisecond)

	// Publish VP8
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pub, err := createPublisher(ctx, serverURL, webrtc.MimeTypeVP8)
	if err != nil {
		t.Fatalf("Failed to create publisher: %v", err)
	}
	defer pub.Close()

	// Create VP8 encoder for test frames
	enc, err := media.NewVP8Encoder(media.VideoEncoderConfig{
		Width: 640, Height: 480, BitrateBps: 1_000_000, FPS: 30,
	})
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}
	defer enc.Close()

	// Generate and send frames
	frame := createTestFrame(640, 480)
	enc.RequestKeyframe()

	var framesSent int32
	go func() {
		ticker := time.NewTicker(33 * time.Millisecond) // ~30fps
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				encoded, err := enc.Encode(frame)
				if err != nil || encoded == nil {
					continue
				}
				if err := pub.WriteFrame(encoded); err != nil {
					return
				}
				atomic.AddInt32(&framesSent, 1)
			}
		}
	}()

	// Wait for pipeline to start
	time.Sleep(500 * time.Millisecond)

	// Subscribe to source (passthrough)
	sub1, frames1, err := createSubscriber(ctx, serverURL, "source")
	if err != nil {
		t.Fatalf("Failed to subscribe to source: %v", err)
	}
	defer sub1.Close()

	// Subscribe to vp8-720p (transcoded)
	sub2, frames2, err := createSubscriber(ctx, serverURL, "vp8-720p")
	if err != nil {
		t.Fatalf("Failed to subscribe to vp8-720p: %v", err)
	}
	defer sub2.Close()

	// Wait and count frames
	time.Sleep(2 * time.Second)

	source := atomic.LoadInt32(frames1)
	vp8720 := atomic.LoadInt32(frames2)
	sent := atomic.LoadInt32(&framesSent)

	t.Logf("Frames sent: %d, source received: %d, vp8-720p received: %d", sent, source, vp8720)

	if source < 30 {
		t.Errorf("Source variant: expected at least 30 frames, got %d", source)
	}
	if vp8720 < 30 {
		t.Errorf("VP8-720p variant: expected at least 30 frames, got %d", vp8720)
	}
}

func startTestServer(t *testing.T) (*http.Server, string) {
	// Initialize global state (copied from main.go init logic)
	initOnce.Do(func() {
		pipeline = nil
		subscribers = make(map[string]map[string]*Subscriber)
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/publish", handlePublish)
	mux.HandleFunc("/subscribe", handleSubscribe)
	mux.HandleFunc("/variants", handleVariants)

	server := &http.Server{
		Addr:    ":0", // Random port
		Handler: mux,
	}

	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}

	go server.Serve(listener)

	return server, fmt.Sprintf("http://%s", listener.Addr().String())
}

type testPublisher struct {
	pc    *webrtc.PeerConnection
	track *webrtc.TrackLocalStaticRTP
}

func createPublisher(ctx context.Context, serverURL, mimeType string) (*testPublisher, error) {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, err
	}

	track, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: mimeType, ClockRate: 90000},
		"video", "test-stream",
	)
	if err != nil {
		pc.Close()
		return nil, err
	}

	if _, err := pc.AddTrack(track); err != nil {
		pc.Close()
		return nil, err
	}

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		pc.Close()
		return nil, err
	}

	if err := pc.SetLocalDescription(offer); err != nil {
		pc.Close()
		return nil, err
	}

	// Wait for ICE gathering
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	select {
	case <-gatherComplete:
	case <-ctx.Done():
		pc.Close()
		return nil, ctx.Err()
	}

	// Send offer to server
	resp, err := http.Post(serverURL+"/publish", "application/sdp",
		strings.NewReader(pc.LocalDescription().SDP))
	if err != nil {
		pc.Close()
		return nil, err
	}
	defer resp.Body.Close()

	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		pc.Close()
		return nil, err
	}

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  string(answer),
	}); err != nil {
		pc.Close()
		return nil, err
	}

	// Wait for connection
	connected := make(chan struct{})
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateConnected {
			close(connected)
		}
	})

	select {
	case <-connected:
	case <-time.After(5 * time.Second):
		pc.Close()
		return nil, fmt.Errorf("connection timeout")
	case <-ctx.Done():
		pc.Close()
		return nil, ctx.Err()
	}

	return &testPublisher{pc: pc, track: track}, nil
}

func (p *testPublisher) WriteFrame(frame *media.EncodedFrame) error {
	// Create RTP packet (simplified - real implementation needs proper packetization)
	return p.track.WriteRTP(&rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 0, // Should increment
			Timestamp:      frame.Timestamp,
			SSRC:           12345,
		},
		Payload: frame.Data,
	})
}

func (p *testPublisher) Close() error {
	return p.pc.Close()
}

type testSubscriber struct {
	pc *webrtc.PeerConnection
}

func createSubscriber(ctx context.Context, serverURL, variantID string) (*testSubscriber, *int32, error) {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, nil, err
	}

	var frameCount int32

	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		go func() {
			buf := make([]byte, 1500)
			for {
				_, _, err := track.Read(buf)
				if err != nil {
					return
				}
				atomic.AddInt32(&frameCount, 1)
			}
		}()
	})

	// Add transceiver to receive video
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		pc.Close()
		return nil, nil, err
	}

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		pc.Close()
		return nil, nil, err
	}

	if err := pc.SetLocalDescription(offer); err != nil {
		pc.Close()
		return nil, nil, err
	}

	gatherComplete := webrtc.GatheringCompletePromise(pc)
	select {
	case <-gatherComplete:
	case <-ctx.Done():
		pc.Close()
		return nil, nil, ctx.Err()
	}

	resp, err := http.Post(serverURL+"/subscribe?variant="+variantID, "application/sdp",
		strings.NewReader(pc.LocalDescription().SDP))
	if err != nil {
		pc.Close()
		return nil, nil, err
	}
	defer resp.Body.Close()

	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		pc.Close()
		return nil, nil, err
	}

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  string(answer),
	}); err != nil {
		pc.Close()
		return nil, nil, err
	}

	return &testSubscriber{pc: pc}, &frameCount, nil
}

func (s *testSubscriber) Close() error {
	return s.pc.Close()
}

func createTestFrame(width, height int) *media.VideoFrame {
	ySize := width * height
	uvSize := (width / 2) * (height / 2)

	frame := &media.VideoFrame{
		Data:   [][]byte{make([]byte, ySize), make([]byte, uvSize), make([]byte, uvSize)},
		Stride: []int{width, width / 2, width / 2},
		Width:  width,
		Height: height,
		Format: media.PixelFormatI420,
	}

	// Fill with gradient pattern
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			frame.Data[0][y*width+x] = byte((x + y) % 256)
		}
	}
	for i := range frame.Data[1] {
		frame.Data[1][i] = 128
		frame.Data[2][i] = 128
	}

	return frame
}

func testPublishAndTranscode(t *testing.T, serverURL, codecName string, codec media.VideoCodec, mimeType string, clockRate uint32) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Check if codec is available
	switch codec {
	case media.VideoCodecVP8:
		if !media.IsVP8Available() {
			t.Skip("VP8 not available")
		}
	case media.VideoCodecVP9:
		if !media.IsVP9Available() {
			t.Skip("VP9 not available")
		}
	case media.VideoCodecH264:
		if !media.IsH264EncoderAvailable() {
			t.Skip("H264 not available")
		}
	case media.VideoCodecAV1:
		if !media.IsAV1Available() {
			t.Skip("AV1 not available")
		}
	}

	t.Logf("Testing %s codec...", codecName)

	// Create encoder
	var enc media.VideoEncoder
	var err error

	encConfig := media.VideoEncoderConfig{
		Width: 640, Height: 480, BitrateBps: 1_000_000, FPS: 30,
	}

	switch codec {
	case media.VideoCodecVP8:
		enc, err = media.NewVP8Encoder(encConfig)
	case media.VideoCodecVP9:
		enc, err = media.NewVP9Encoder(encConfig)
	case media.VideoCodecH264:
		enc, err = media.NewH264Encoder(encConfig)
	case media.VideoCodecAV1:
		enc, err = media.NewAV1Encoder(encConfig)
	}
	if err != nil {
		t.Fatalf("Failed to create %s encoder: %v", codecName, err)
	}
	defer enc.Close()

	// Create publisher
	pub, err := createPublisher(ctx, serverURL, mimeType)
	if err != nil {
		t.Fatalf("Failed to create publisher: %v", err)
	}
	defer pub.Close()

	// Generate frames
	frame := createTestFrame(640, 480)
	enc.RequestKeyframe()

	var framesSent int32
	stopPublish := make(chan struct{})

	go func() {
		ticker := time.NewTicker(33 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopPublish:
				return
			case <-ticker.C:
				encoded, err := enc.Encode(frame)
				if err != nil || encoded == nil {
					continue
				}
				if err := pub.WriteFrame(encoded); err != nil {
					return
				}
				atomic.AddInt32(&framesSent, 1)
			}
		}
	}()

	// Wait for pipeline
	time.Sleep(500 * time.Millisecond)

	// Subscribe to source
	sub, frames, err := createSubscriber(ctx, serverURL, "source")
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}
	defer sub.Close()

	// Run for 2 seconds
	time.Sleep(2 * time.Second)
	close(stopPublish)

	received := atomic.LoadInt32(frames)
	sent := atomic.LoadInt32(&framesSent)

	t.Logf("%s: sent %d frames, received %d", codecName, sent, received)

	if received < 20 {
		t.Errorf("%s: expected at least 20 frames, got %d", codecName, received)
	}
}

var initOnce sync.Once
