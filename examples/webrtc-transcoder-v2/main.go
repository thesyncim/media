// WebRTC Multi-Transcoder Example v2
//
// A clean, modular implementation demonstrating:
// - Publisher sends video via WebRTC (H264, VP8, VP9, or AV1)
// - Server transcodes to multiple output variants simultaneously
// - Viewers subscribe to any variant (different codec/resolution/bitrate)
// - Dynamic variant addition/removal at runtime
// - Passthrough mode for zero-CPU relay
//
// Usage:
//   go run main.go
//   Open http://localhost:8080

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
	"github.com/thesyncim/media"
)

// ============================================================================
// Configuration
// ============================================================================

const (
	listenAddr       = ":8080"
	frameBufferSize  = 30
	transcodeTimeout = 500 * time.Millisecond
	keyframeInterval = 2 * time.Second
)

var defaultVariants = []VariantConfig{
	{ID: "source", Passthrough: true, Label: "Source (Passthrough)"},
	{ID: "vp8-720p", Codec: media.VideoCodecVP8, Width: 1280, Height: 720, BitrateBps: 1_500_000, Label: "VP8 720p"},
}

// ============================================================================
// Types
// ============================================================================

// VariantConfig defines an output variant configuration.
type VariantConfig struct {
	ID          string
	Codec       media.VideoCodec
	Width       int
	Height      int
	BitrateBps  int
	Label       string
	Passthrough bool
}

// Server holds all server state.
type Server struct {
	mu       sync.RWMutex
	pipeline *Pipeline
}

// Pipeline handles transcoding for one publisher.
type Pipeline struct {
	publisher   *Publisher
	transcoder  *media.MultiTranscoder
	variants    []VariantConfig
	subscribers map[string][]*Subscriber // variantID -> subscribers
	subMu       sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc

	// Stats
	frameCount  atomic.Int64
	frameDrops  atomic.Int64
	avgEncodeNs atomic.Int64
}

// Publisher represents the video source.
type Publisher struct {
	pc           *webrtc.PeerConnection
	track        *webrtc.TrackRemote
	codec        media.VideoCodec
	depacketizer media.RTPDepacketizer
	frameCh      chan *media.EncodedFrame
	closed       atomic.Bool
}

// Subscriber represents a viewer.
type Subscriber struct {
	id        string
	variantID string
	frameCh   chan *media.EncodedFrame
	closed    atomic.Bool
}

// ============================================================================
// Server
// ============================================================================

func NewServer() *Server {
	return &Server{}
}

// newPeerConnection creates a new PeerConnection with a fresh MediaEngine.
func newPeerConnection() (*webrtc.PeerConnection, error) {
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		return nil, err
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(m))
	return api.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}},
	})
}

func (s *Server) GetPipeline() *Pipeline {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pipeline
}

func (s *Server) SetPipeline(p *Pipeline) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pipeline != nil {
		s.pipeline.Stop()
	}
	s.pipeline = p
}

// ============================================================================
// Publisher
// ============================================================================

func NewPublisher(pc *webrtc.PeerConnection) *Publisher {
	return &Publisher{
		pc:      pc,
		frameCh: make(chan *media.EncodedFrame, frameBufferSize),
	}
}

func (p *Publisher) SetTrack(track *webrtc.TrackRemote) {
	p.track = track
	p.codec = mimeToCodec(track.Codec().MimeType)
	p.depacketizer, _ = media.CreateVideoDepacketizer(p.codec)
}

func (p *Publisher) RequestKeyframe() error {
	if p.pc == nil || p.track == nil {
		return nil
	}
	return p.pc.WriteRTCP([]rtcp.Packet{
		&rtcp.PictureLossIndication{MediaSSRC: uint32(p.track.SSRC())},
	})
}

func (p *Publisher) readFrames() {
	for !p.closed.Load() {
		if p.track == nil || p.depacketizer == nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}

		pkt, _, err := p.track.ReadRTP()
		if err != nil {
			return
		}

		frame, err := p.depacketizer.Depacketize(pkt)
		if err != nil || frame == nil {
			continue
		}

		// Non-blocking send, drop old frames if buffer full
		select {
		case p.frameCh <- frame:
		default:
			select {
			case <-p.frameCh:
			default:
			}
			select {
			case p.frameCh <- frame:
			default:
			}
		}
	}
}

// ============================================================================
// Pipeline
// ============================================================================

func NewPipeline(pub *Publisher, variants []VariantConfig) (*Pipeline, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Build output configs
	var outputs []media.OutputConfig
	var activeVariants []VariantConfig

	for _, v := range variants {
		if v.Passthrough {
			activeVariants = append(activeVariants, VariantConfig{
				ID:          v.ID,
				Codec:       pub.codec,
				Label:       fmt.Sprintf("Source (%s)", pub.codec),
				Passthrough: true,
			})
			outputs = append(outputs, media.OutputConfig{
				ID:          v.ID,
				Codec:       pub.codec,
				Passthrough: true,
			})
		} else if isCodecAvailable(v.Codec) {
			activeVariants = append(activeVariants, v)
			outputs = append(outputs, media.OutputConfig{
				ID:         v.ID,
				Codec:      v.Codec,
				Width:      v.Width,
				Height:     v.Height,
				BitrateBps: v.BitrateBps,
			})
		}
	}

	mt, err := media.NewMultiTranscoder(media.MultiTranscoderConfig{
		InputCodec:       pub.codec,
		Outputs:          outputs,
		OnKeyframeNeeded: pub.RequestKeyframe,
	})
	if err != nil {
		cancel()
		return nil, err
	}

	p := &Pipeline{
		publisher:   pub,
		transcoder:  mt,
		variants:    activeVariants,
		subscribers: make(map[string][]*Subscriber),
		ctx:         ctx,
		cancel:      cancel,
	}

	go p.run()
	go p.keyframeLoop()

	return p, nil
}

func (p *Pipeline) run() {
	defer p.transcoder.Close()

	gotKeyframe := false

	for {
		select {
		case <-p.ctx.Done():
			return
		case frame := <-p.publisher.frameCh:
			if frame == nil {
				continue
			}

			// Wait for first keyframe
			if !gotKeyframe {
				if frame.FrameType != media.FrameTypeKey {
					continue
				}
				gotKeyframe = true
				log.Println("Pipeline: received first keyframe")
			}

			// Transcode with timeout
			result := p.transcodeWithTimeout(frame)
			if result == nil {
				continue
			}

			// Broadcast to subscribers
			p.subMu.RLock()
			for _, v := range result.Variants {
				for _, sub := range p.subscribers[v.VariantID] {
					if !sub.closed.Load() {
						p.sendFrame(sub.frameCh, v.Frame)
					}
				}
			}
			p.subMu.RUnlock()

			p.frameCount.Add(1)
		}
	}
}

func (p *Pipeline) transcodeWithTimeout(frame *media.EncodedFrame) *media.TranscodeResult {
	type result struct {
		r   *media.TranscodeResult
		err error
	}

	ch := make(chan result, 1)
	start := time.Now()

	go func() {
		r, err := p.transcoder.Transcode(frame)
		ch <- result{r, err}
	}()

	select {
	case <-p.ctx.Done():
		return nil
	case <-time.After(transcodeTimeout):
		p.frameDrops.Add(1)
		return nil
	case res := <-ch:
		p.avgEncodeNs.Store((p.avgEncodeNs.Load()*9 + int64(time.Since(start))) / 10)
		if res.err != nil {
			log.Printf("Pipeline: transcode error: %v", res.err)
			return nil
		}
		return res.r
	}
}

func (p *Pipeline) sendFrame(ch chan *media.EncodedFrame, frame *media.EncodedFrame) {
	select {
	case ch <- frame:
	default:
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- frame:
		default:
		}
	}
}

func (p *Pipeline) keyframeLoop() {
	ticker := time.NewTicker(keyframeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.transcoder.RequestKeyframeAll()
		}
	}
}

func (p *Pipeline) AddSubscriber(variantID string, sub *Subscriber) {
	p.subMu.Lock()
	p.subscribers[variantID] = append(p.subscribers[variantID], sub)
	p.subMu.Unlock()
}

func (p *Pipeline) RemoveSubscriber(variantID, subID string) {
	p.subMu.Lock()
	subs := p.subscribers[variantID]
	for i, s := range subs {
		if s.id == subID {
			p.subscribers[variantID] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	p.subMu.Unlock()
}

func (p *Pipeline) Stop() {
	p.cancel()
	p.subMu.Lock()
	for _, subs := range p.subscribers {
		for _, sub := range subs {
			sub.closed.Store(true)
			close(sub.frameCh)
		}
	}
	p.subMu.Unlock()
}

func (p *Pipeline) Stats() (variants int, frames, drops int64, avgEncMs float64) {
	return len(p.variants), p.frameCount.Load(), p.frameDrops.Load(),
		float64(p.avgEncodeNs.Load()) / 1e6
}

// ============================================================================
// HTTP Handlers
// ============================================================================

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	var offer webrtc.SessionDescription
	if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	pc, err := newPeerConnection()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pub := NewPublisher(pc)

	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if track.Kind() != webrtc.RTPCodecTypeVideo {
			return
		}

		log.Printf("Publisher: received %s track", track.Codec().MimeType)
		pub.SetTrack(track)
		go pub.readFrames()

		pipeline, err := NewPipeline(pub, defaultVariants)
		if err != nil {
			log.Printf("Pipeline error: %v", err)
			return
		}
		s.SetPipeline(pipeline)
		log.Printf("Pipeline started with %d variants", len(pipeline.variants))
	})

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("Publisher: %s", state)
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			pub.closed.Store(true)
			s.SetPipeline(nil)
		}
	})

	if err := pc.SetRemoteDescription(offer); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	gatherComplete := webrtc.GatheringCompletePromise(pc)
	pc.SetLocalDescription(answer)
	<-gatherComplete

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pc.LocalDescription())
}

// Track recent subscriptions to prevent duplicates
var (
	recentSubs   = make(map[string]time.Time)
	recentSubsMu sync.Mutex
)

func (s *Server) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	variantID := r.URL.Query().Get("variant")
	if variantID == "" {
		variantID = "source"
	}

	// Rate limit: only allow one subscription per variant per 2 seconds
	recentSubsMu.Lock()
	if lastSub, ok := recentSubs[variantID]; ok && time.Since(lastSub) < 2*time.Second {
		recentSubsMu.Unlock()
		http.Error(w, "Too many subscription requests", http.StatusTooManyRequests)
		return
	}
	recentSubs[variantID] = time.Now()
	recentSubsMu.Unlock()

	var offer webrtc.SessionDescription
	if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	pipeline := s.GetPipeline()
	if pipeline == nil {
		http.Error(w, "No active stream", http.StatusNotFound)
		return
	}

	// Find variant
	var variant *VariantConfig
	for _, v := range pipeline.variants {
		if v.ID == variantID {
			variant = &v
			break
		}
	}
	if variant == nil {
		http.Error(w, "Variant not found", http.StatusNotFound)
		return
	}

	// Create subscriber
	sub := &Subscriber{
		id:        fmt.Sprintf("%s-%d", variantID, time.Now().UnixNano()),
		variantID: variantID,
		frameCh:   make(chan *media.EncodedFrame, frameBufferSize),
	}
	pipeline.AddSubscriber(variantID, sub)

	// Create peer connection with fresh MediaEngine
	pc, err := newPeerConnection()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Create track
	codec := variant.Codec
	if variant.Passthrough {
		codec = pipeline.publisher.codec
	}

	track, err := webrtc.NewTrackLocalStaticRTP(
		codecCapability(codec), variantID, "transcoder",
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	sender, _ := pc.AddTrack(track)

	// Handle connection state
	var packetizer media.RTPPacketizer
	var packetizerReady atomic.Bool

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("Subscriber [%s]: %s", variantID, state)

		if state == webrtc.PeerConnectionStateConnected {
			// Get negotiated parameters
			params := sender.GetParameters()
			if len(params.Codecs) > 0 {
				pt := uint8(params.Codecs[0].PayloadType)
				ssrc := uint32(time.Now().UnixNano() & 0xFFFFFFFF)
				if len(params.Encodings) > 0 && params.Encodings[0].SSRC != 0 {
					ssrc = uint32(params.Encodings[0].SSRC)
				}
				packetizer, _ = media.CreateVideoPacketizer(codec, ssrc, pt, 1200)
				packetizerReady.Store(true)
			}

			// Request keyframe
			pipeline.transcoder.RequestKeyframe(variantID)
		}

		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			sub.closed.Store(true)
			pipeline.RemoveSubscriber(variantID, sub.id)
		}
	})

	// Handle PLI
	go func() {
		for {
			pkts, _, err := sender.ReadRTCP()
			if err != nil {
				return
			}
			for _, pkt := range pkts {
				switch pkt.(type) {
				case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
					pipeline.transcoder.RequestKeyframe(variantID)
				}
			}
		}
	}()

	// Complete signaling
	pc.SetRemoteDescription(offer)
	answer, _ := pc.CreateAnswer(nil)
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	pc.SetLocalDescription(answer)
	<-gatherComplete

	// Start sending frames
	go func() {
		// Wait for packetizer
		for !packetizerReady.Load() {
			if pc.ConnectionState() == webrtc.PeerConnectionStateFailed {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}

		gotKeyframe := false
		for frame := range sub.frameCh {
			if sub.closed.Load() {
				return
			}

			// Wait for keyframe
			if !gotKeyframe {
				if frame.FrameType != media.FrameTypeKey {
					continue
				}
				gotKeyframe = true
			}

			packets, _ := packetizer.Packetize(frame)
			for _, pkt := range packets {
				track.WriteRTP(pkt)
			}
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pc.LocalDescription())
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	pipeline := s.GetPipeline()

	status := map[string]interface{}{
		"active":          pipeline != nil,
		"availableCodecs": availableCodecs(),
	}

	if pipeline != nil {
		variants, frames, drops, avgMs := pipeline.Stats()
		status["variants"] = pipeline.variants
		status["frameCount"] = frames
		status["frameDrops"] = drops
		status["avgEncodeMs"] = avgMs
		status["variantCount"] = variants
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (s *Server) handleAddVariant(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID         string `json:"id"`
		Codec      string `json:"codec"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
		BitrateBps int    `json:"bitrateBps"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	pipeline := s.GetPipeline()
	if pipeline == nil {
		http.Error(w, "No active stream", http.StatusBadRequest)
		return
	}

	codec := parseCodec(req.Codec)
	if codec == media.VideoCodecUnknown {
		http.Error(w, "Unknown codec", http.StatusBadRequest)
		return
	}

	err := pipeline.transcoder.AddOutput(media.OutputConfig{
		ID:         req.ID,
		Codec:      codec,
		Width:      req.Width,
		Height:     req.Height,
		BitrateBps: req.BitrateBps,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	variant := VariantConfig{
		ID:         req.ID,
		Codec:      codec,
		Width:      req.Width,
		Height:     req.Height,
		BitrateBps: req.BitrateBps,
		Label:      fmt.Sprintf("%s %dx%d", req.Codec, req.Width, req.Height),
	}
	pipeline.variants = append(pipeline.variants, variant)

	log.Printf("Added variant: %s", req.ID)
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "variant": variant})
}

func (s *Server) handleRemoveVariant(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	variantID := r.URL.Query().Get("id")
	if variantID == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}

	pipeline := s.GetPipeline()
	if pipeline == nil {
		http.Error(w, "No active stream", http.StatusBadRequest)
		return
	}

	if err := pipeline.transcoder.RemoveOutput(variantID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Remove variant from list
	var newVariants []VariantConfig
	for _, v := range pipeline.variants {
		if v.ID != variantID {
			newVariants = append(newVariants, v)
		}
	}
	pipeline.variants = newVariants

	log.Printf("Removed variant: %s", variantID)
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// ============================================================================
// Helpers
// ============================================================================

func mimeToCodec(mime string) media.VideoCodec {
	switch mime {
	case "video/H264", "video/h264":
		return media.VideoCodecH264
	case "video/VP8", "video/vp8":
		return media.VideoCodecVP8
	case "video/VP9", "video/vp9":
		return media.VideoCodecVP9
	case "video/AV1", "video/av1":
		return media.VideoCodecAV1
	default:
		return media.VideoCodecUnknown
	}
}

func parseCodec(s string) media.VideoCodec {
	switch s {
	case "VP8", "vp8":
		return media.VideoCodecVP8
	case "VP9", "vp9":
		return media.VideoCodecVP9
	case "H264", "h264":
		return media.VideoCodecH264
	case "AV1", "av1":
		return media.VideoCodecAV1
	default:
		return media.VideoCodecUnknown
	}
}

func isCodecAvailable(c media.VideoCodec) bool {
	switch c {
	case media.VideoCodecVP8:
		return media.IsVP8Available()
	case media.VideoCodecVP9:
		return media.IsVP9Available()
	case media.VideoCodecH264:
		return media.IsH264EncoderAvailable()
	case media.VideoCodecAV1:
		return media.IsAV1Available()
	default:
		return false
	}
}

func availableCodecs() []string {
	var codecs []string
	if media.IsVP8Available() {
		codecs = append(codecs, "VP8")
	}
	if media.IsVP9Available() {
		codecs = append(codecs, "VP9")
	}
	if media.IsH264EncoderAvailable() {
		codecs = append(codecs, "H264")
	}
	if media.IsAV1Available() {
		codecs = append(codecs, "AV1")
	}
	return codecs
}

func codecCapability(c media.VideoCodec) webrtc.RTPCodecCapability {
	switch c {
	case media.VideoCodecVP8:
		return webrtc.RTPCodecCapability{MimeType: "video/VP8", ClockRate: 90000}
	case media.VideoCodecVP9:
		return webrtc.RTPCodecCapability{MimeType: "video/VP9", ClockRate: 90000, SDPFmtpLine: "profile-id=0"}
	case media.VideoCodecH264:
		return webrtc.RTPCodecCapability{MimeType: "video/H264", ClockRate: 90000, SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"}
	case media.VideoCodecAV1:
		return webrtc.RTPCodecCapability{MimeType: "video/AV1", ClockRate: 90000, SDPFmtpLine: "profile=0"}
	default:
		return webrtc.RTPCodecCapability{MimeType: "video/VP8", ClockRate: 90000}
	}
}

// ============================================================================
// Main
// ============================================================================

func main() {
	log.Println("WebRTC Multi-Transcoder v2")
	log.Println("Available codecs:", availableCodecs())

	server := NewServer()

	http.HandleFunc("/", serveHTML)
	http.HandleFunc("/publish", server.handlePublish)
	http.HandleFunc("/subscribe", server.handleSubscribe)
	http.HandleFunc("/status", server.handleStatus)
	http.HandleFunc("/add-variant", server.handleAddVariant)
	http.HandleFunc("/remove-variant", server.handleRemoveVariant)

	log.Printf("Server: http://localhost%s", listenAddr)
	log.Fatal(http.ListenAndServe(listenAddr, nil))
}

func serveHTML(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, htmlTemplate)
}

const htmlTemplate = `<!DOCTYPE html>
<html>
<head>
    <title>WebRTC Multi-Transcoder v2</title>
    <style>
        * { box-sizing: border-box; }
        body { font-family: system-ui; background: #1a1a2e; color: #eee; margin: 0; padding: 20px; min-height: 100vh; }
        h1 { color: #00d9ff; }
        .container { max-width: 1400px; margin: 0 auto; }
        .section { background: rgba(255,255,255,0.05); border-radius: 12px; padding: 20px; margin: 20px 0; }
        video { width: 100%; background: #000; border-radius: 8px; aspect-ratio: 16/9; }
        .video-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(320px, 1fr)); gap: 15px; }
        .video-card { background: rgba(0,0,0,0.3); border-radius: 8px; overflow: hidden; }
        .video-label { padding: 10px; background: rgba(0,0,0,0.5); font-size: 0.9em; display: flex; justify-content: space-between; }
        .codec-badge { padding: 3px 8px; border-radius: 4px; font-size: 0.8em; font-weight: bold; }
        .codec-vp8 { background: #4CAF50; }
        .codec-vp9 { background: #2196F3; }
        .codec-h264 { background: #FF9800; }
        .codec-av1 { background: #E91E63; }
        button { padding: 12px 24px; background: linear-gradient(135deg, #00d9ff, #00a8cc); color: #000; border: none; border-radius: 6px; cursor: pointer; font-weight: bold; }
        button:hover { transform: translateY(-2px); }
        select, input { padding: 8px; background: #333; color: #fff; border: 1px solid #555; border-radius: 4px; }
        .controls { display: flex; flex-wrap: wrap; gap: 10px; align-items: center; margin-top: 15px; }
        .stats { display: grid; grid-template-columns: repeat(4, 1fr); gap: 10px; margin-top: 15px; }
        .stat-box { background: rgba(0,0,0,0.3); padding: 10px; border-radius: 6px; text-align: center; }
        .stat-value { font-size: 1.5em; color: #00d9ff; }
        .stat-label { font-size: 0.8em; color: #888; }
    </style>
</head>
<body>
<div class="container">
    <h1>WebRTC Multi-Transcoder v2</h1>
    <p style="color:#888">Publish once, transcode to multiple codecs in real-time</p>

    <div class="section">
        <h2 style="color:#00d9ff">Publish</h2>
        <div class="video-card" style="max-width:640px">
            <video id="localVideo" autoplay muted playsinline></video>
            <div class="video-label"><span>Your Camera</span><span id="publishCodec" class="codec-badge" style="display:none"></span></div>
        </div>
        <div class="controls">
            <select id="resolution">
                <option value="1280x720">720p</option>
                <option value="1920x1080">1080p</option>
                <option value="640x480">480p</option>
            </select>
            <select id="codec">
                <option value="VP8">VP8</option>
                <option value="H264">H.264</option>
                <option value="VP9">VP9</option>
            </select>
            <button onclick="publish()">Publish</button>
            <span id="status" style="color:#888">Ready</span>
        </div>
    </div>

    <div class="section">
        <h2 style="color:#ff6b6b">Transcoded Outputs</h2>
        <div id="addVariant" style="display:none;margin-bottom:20px;padding:15px;background:rgba(0,0,0,0.2);border-radius:8px">
            <div class="controls">
                <select id="addCodec"></select>
                <select id="addRes">
                    <option value="1280x720">720p</option>
                    <option value="854x480">480p</option>
                    <option value="640x360">360p</option>
                </select>
                <input id="addBitrate" type="number" value="500" style="width:80px"> kbps
                <button onclick="addVariant()" style="background:linear-gradient(135deg,#4CAF50,#388E3C)">+ Add</button>
            </div>
        </div>
        <div id="grid" class="video-grid"><div style="padding:40px;text-align:center;color:#666">Waiting for publisher...</div></div>
        <div class="stats" id="stats" style="display:none">
            <div class="stat-box"><div class="stat-value" id="sVariants">0</div><div class="stat-label">Variants</div></div>
            <div class="stat-box"><div class="stat-value" id="sFrames">0</div><div class="stat-label">Frames</div></div>
            <div class="stat-box"><div class="stat-value" id="sEncode">0</div><div class="stat-label">Encode (ms)</div></div>
            <div class="stat-box"><div class="stat-value" id="sDrops">0</div><div class="stat-label">Drops</div></div>
        </div>
    </div>
</div>

<script>
let pc, stream, subs = {}, polling, pollLock = false;

async function publish() {
    if (pc) { pc.close(); pc = null; stream?.getTracks().forEach(t=>t.stop()); return; }
    try {
        const [w,h] = document.getElementById('resolution').value.split('x').map(Number);
        stream = await navigator.mediaDevices.getUserMedia({video:{width:{ideal:w},height:{ideal:h}},audio:false});
        document.getElementById('localVideo').srcObject = stream;
        pc = new RTCPeerConnection({iceServers:[{urls:'stun:stun.l.google.com:19302'}]});
        const tr = pc.addTransceiver(stream.getVideoTracks()[0], {direction:'sendonly'});
        const codec = document.getElementById('codec').value;
        const caps = RTCRtpSender.getCapabilities('video').codecs;
        const pref = caps.filter(c=>c.mimeType==='video/'+codec);
        if (pref.length && tr.setCodecPreferences) tr.setCodecPreferences([...pref,...caps.filter(c=>c.mimeType!=='video/'+codec)]);
        pc.oniceconnectionstatechange = () => {
            if (pc.iceConnectionState==='connected') {
                document.getElementById('status').textContent = 'Connected';
                const p = pc.getSenders()[0]?.getParameters();
                if (p?.codecs?.[0]) {
                    const b = document.getElementById('publishCodec');
                    b.textContent = p.codecs[0].mimeType.split('/')[1];
                    b.style.display = 'inline-block';
                    b.className = 'codec-badge codec-'+b.textContent.toLowerCase();
                }
                startPolling();
            }
        };
        const offer = await pc.createOffer();
        await pc.setLocalDescription(offer);
        const resp = await fetch('/publish',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(pc.localDescription)});
        if (!resp.ok) throw new Error(await resp.text());
        await pc.setRemoteDescription(await resp.json());
    } catch(e) { document.getElementById('status').textContent = 'Error: '+e.message; }
}

function startPolling() { polling = setInterval(poll, 1000); poll(); }

async function poll() {
    if (pollLock) return; // Skip if previous poll still running
    pollLock = true;
    try {
    const r = await fetch('/status');
    const s = await r.json();
    if (s.availableCodecs) {
        const sel = document.getElementById('addCodec');
        if (!sel.options.length) s.availableCodecs.forEach(c=>{const o=document.createElement('option');o.value=o.textContent=c;sel.appendChild(o);});
    }
    if (s.active && s.variants) {
        document.getElementById('addVariant').style.display = 'block';
        document.getElementById('stats').style.display = 'grid';
        document.getElementById('sVariants').textContent = s.variantCount;
        document.getElementById('sFrames').textContent = s.frameCount;
        document.getElementById('sEncode').textContent = (s.avgEncodeMs||0).toFixed(1);
        document.getElementById('sDrops').textContent = s.frameDrops;
        for (const v of s.variants) {
            if (!subs[v.ID]) {
                subs[v.ID] = {pending: true}; // Mark immediately before async call
                await subscribe(v);
            }
        }
    }
    } finally { pollLock = false; }
}

async function subscribe(v) {
    const grid = document.getElementById('grid');
    if (grid.querySelector('div[style*="text-align"]')) grid.innerHTML = '';
    const card = document.createElement('div'); card.className = 'video-card'; card.id = 'card-'+v.ID;
    const video = document.createElement('video'); video.autoplay = video.muted = video.playsInline = true;
    const codecName = v.ID==='source'?'PASSTHROUGH':['','VP8','VP9','H264','H265','AV1'][v.Codec]||'';
    const codecClass = codecName.toLowerCase().replace('passthrough','vp8');
    card.innerHTML = '<div class="video-label"><span>'+v.Label+'</span><span class="codec-badge codec-'+codecClass+'">'+codecName+'</span></div>';
    card.prepend(video); grid.appendChild(card);
    subs[v.ID].video = video;
    try {
        const sub = new RTCPeerConnection({iceServers:[{urls:'stun:stun.l.google.com:19302'}]});
        sub.ontrack = e => video.srcObject = e.streams[0];
        sub.addTransceiver('video',{direction:'recvonly'});
        const offer = await sub.createOffer();
        await sub.setLocalDescription(offer);
        const resp = await fetch('/subscribe?variant='+v.ID,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(sub.localDescription)});
        await sub.setRemoteDescription(await resp.json());
        subs[v.ID].pc = sub;
    } catch(e) { console.error('Subscribe error:', e); }
}

async function addVariant() {
    const codec = document.getElementById('addCodec').value;
    const [w,h] = document.getElementById('addRes').value.split('x').map(Number);
    const bitrate = parseInt(document.getElementById('addBitrate').value)*1000;
    const id = 'dyn-'+codec.toLowerCase()+'-'+Date.now();
    const resp = await fetch('/add-variant',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({id,codec,width:w,height:h,bitrateBps:bitrate})});
    if (!resp.ok) alert(await resp.text());
}
</script>
</body>
</html>`
