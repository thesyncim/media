// WebRTC Multi-Transcoder Example
//
// Demonstrates the power of the media library's MultiTranscoder:
// - Publisher sends video via WebRTC (any codec: H264, VP8, VP9)
// - Server transcodes to N output variants simultaneously
// - Each variant is a separate WebRTC track with different codec/resolution/bitrate
// - Viewers can subscribe to any variant
//
// Usage:
//   go run main.go
//   Open http://localhost:8080
//   Click "Publish" to send your camera
//   Click "Subscribe" to view all transcoded variants side-by-side

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

// TranscodeVariant defines an output configuration
type TranscodeVariant struct {
	ID         string
	Codec      media.VideoCodec
	Width      int
	Height     int
	BitrateBps int
	Label      string // Human-readable label
}

// Default variants: different codecs and resolutions
var defaultVariants = []TranscodeVariant{
	{ID: "source", Codec: media.VideoCodecUnknown, Label: "Source (Passthrough)"},
	{ID: "vp8-720p", Codec: media.VideoCodecVP8, Width: 1280, Height: 720, BitrateBps: 1_500_000, Label: "VP8 720p"},
	{ID: "vp9-720p", Codec: media.VideoCodecVP9, Width: 1280, Height: 720, BitrateBps: 1_200_000, Label: "VP9 720p"},
	{ID: "h264-720p", Codec: media.VideoCodecH264, Width: 1280, Height: 720, BitrateBps: 1_500_000, Label: "H264 720p"},
	{ID: "av1-720p", Codec: media.VideoCodecAV1, Width: 1280, Height: 720, BitrateBps: 1_000_000, Label: "AV1 720p"},
}

// Publisher holds the incoming stream
type Publisher struct {
	pc          *webrtc.PeerConnection
	track       *webrtc.TrackRemote
	codec       media.VideoCodec
	depacketizer media.RTPDepacketizer

	frameCh     chan *media.EncodedFrame
	closed      atomic.Bool
	mu          sync.RWMutex
}

// RequestKeyframe sends PLI to request a keyframe from the remote camera
func (p *Publisher) RequestKeyframe() error {
	p.mu.RLock()
	pc := p.pc
	track := p.track
	p.mu.RUnlock()

	if pc == nil || track == nil {
		return nil
	}

	// Send PLI (Picture Loss Indication) to request keyframe
	return pc.WriteRTCP([]rtcp.Packet{
		&rtcp.PictureLossIndication{
			MediaSSRC: uint32(track.SSRC()),
		},
	})
}

// TranscodePipeline handles transcoding for one publisher
type TranscodePipeline struct {
	publisher    *Publisher
	transcoder   *media.MultiTranscoder
	variants     []TranscodeVariant

	// Broadcast: each variant has multiple subscribers
	subscribersMu sync.RWMutex
	subscribers   map[string][]*Subscriber // variantID -> list of subscribers

	ctx          context.Context
	cancel       context.CancelFunc
	running      atomic.Bool

	// Performance tracking
	frameDrops     atomic.Int64
	lastFrameTime  atomic.Int64  // nanoseconds
	avgEncodeTimeNs atomic.Int64 // average encode time in nanoseconds
}

// Subscriber represents a connected viewer
type Subscriber struct {
	id        string
	variantID string
	frameCh   chan *media.EncodedFrame
	closed    atomic.Bool
}

var (
	currentPublisher *Publisher
	currentPipeline  *TranscodePipeline
	publisherMu      sync.RWMutex

	webrtcAPI *webrtc.API
)

func init() {
	// Create WebRTC API with all codecs
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		log.Fatal(err)
	}
	webrtcAPI = webrtc.NewAPI(webrtc.WithMediaEngine(m))
}

func main() {
	// Check available codecs
	log.Println("WebRTC Multi-Transcoder Demo")
	log.Println("Available codecs:")
	log.Printf("  H.264: encoder=%v decoder=%v", media.IsH264EncoderAvailable(), media.IsH264DecoderAvailable())
	log.Printf("  VP8:   %v", media.IsVP8Available())
	log.Printf("  VP9:   %v", media.IsVP9Available())
	log.Printf("  AV1:   %v", media.IsAV1Available())
	log.Println()

	// HTTP handlers
	http.HandleFunc("/", serveHTML)
	http.HandleFunc("/publish", handlePublish)
	http.HandleFunc("/subscribe", handleSubscribe)
	http.HandleFunc("/status", handleStatus)
	http.HandleFunc("/add-variant", handleAddVariant)    // Dynamic output addition
	http.HandleFunc("/remove-variant", handleRemoveVariant) // Dynamic output removal

	log.Println("Server: http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handlePublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	var offer webrtc.SessionDescription
	if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Create peer connection
	pc, err := webrtcAPI.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	publisher := &Publisher{
		pc:      pc,
		frameCh: make(chan *media.EncodedFrame, 30),
	}

	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		if track.Kind() != webrtc.RTPCodecTypeVideo {
			return
		}

		codec := detectCodecFromMime(track.Codec().MimeType)
		log.Printf("Publisher: received %s track (%s)", track.Codec().MimeType, codec)

		publisher.mu.Lock()
		publisher.track = track
		publisher.codec = codec
		publisher.depacketizer, _ = media.CreateVideoDepacketizer(codec)
		publisher.mu.Unlock()

		// Start receiving frames
		go receiveFrames(publisher)

		// Start transcode pipeline
		publisherMu.Lock()
		if currentPublisher != nil {
			currentPublisher.closed.Store(true)
		}
		currentPublisher = publisher
		publisherMu.Unlock()

		startTranscodePipeline(publisher)

		// Handle RTCP (PLI requests from subscribers)
		go func() {
			for {
				packets, _, err := receiver.ReadRTCP()
				if err != nil {
					return
				}
				for _, pkt := range packets {
					// Check for PLI (Picture Loss Indication)
					switch pkt.(type) {
					case *rtcp.PictureLossIndication:
						publisherMu.RLock()
						p := currentPipeline
						publisherMu.RUnlock()
						if p != nil && p.transcoder != nil {
							p.transcoder.RequestKeyframeAll()
							log.Printf("Publisher: PLI received, requesting keyframes")
						}
					}
				}
			}
		}()
	})

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("Publisher: %s", state)
		if state == webrtc.PeerConnectionStateClosed || state == webrtc.PeerConnectionStateFailed {
			publisher.closed.Store(true)
			stopTranscodePipeline()
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
	if err := pc.SetLocalDescription(answer); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	<-gatherComplete

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pc.LocalDescription())
}

func receiveFrames(pub *Publisher) {
	buf := make([]byte, 1500)

	for !pub.closed.Load() {
		pub.mu.RLock()
		track := pub.track
		depack := pub.depacketizer
		pub.mu.RUnlock()

		if track == nil || depack == nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}

		n, _, err := track.Read(buf)
		if err != nil {
			if !pub.closed.Load() {
				log.Printf("Publisher read error: %v", err)
			}
			return
		}

		frame, err := depack.DepacketizeBytes(buf[:n])
		if err != nil {
			continue
		}
		if frame == nil {
			continue
		}

		select {
		case pub.frameCh <- frame:
		default:
			// Drop if buffer full
		}
	}
}

func startTranscodePipeline(pub *Publisher) {
	publisherMu.Lock()
	defer publisherMu.Unlock()

	if currentPipeline != nil {
		currentPipeline.cancel()
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Build output configs for MultiTranscoder
	var outputs []media.OutputConfig
	variants := make([]TranscodeVariant, 0)

	for _, v := range defaultVariants {
		if v.ID == "source" {
			// Passthrough variant
			variants = append(variants, TranscodeVariant{
				ID:    "source",
				Codec: pub.codec,
				Label: fmt.Sprintf("Source (%s)", pub.codec),
			})
			outputs = append(outputs, media.OutputConfig{
				ID:          "source",
				Codec:       pub.codec,
				Passthrough: true,
			})
		} else {
			// Check if codec is available
			available := false
			switch v.Codec {
			case media.VideoCodecVP8:
				available = media.IsVP8Available()
			case media.VideoCodecVP9:
				available = media.IsVP9Available()
			case media.VideoCodecH264:
				available = media.IsH264EncoderAvailable()
			case media.VideoCodecAV1:
				available = media.IsAV1Available()
			}
			if !available {
				continue
			}

			variants = append(variants, v)
			outputs = append(outputs, media.OutputConfig{
				ID:         v.ID,
				Codec:      v.Codec,
				Width:      v.Width,
				Height:     v.Height,
				BitrateBps: v.BitrateBps,
			})
		}
	}

	// Create MultiTranscoder
	mt, err := media.NewMultiTranscoder(media.MultiTranscoderConfig{
		InputCodec: pub.codec,
		Outputs:    outputs,
		// Auto-recovery: when decoder needs keyframe, request PLI from publisher
		OnKeyframeNeeded: func() error {
			log.Printf("Transcoder: auto-requesting keyframe from publisher")
			return pub.RequestKeyframe()
		},
	})
	if err != nil {
		log.Printf("Failed to create transcoder: %v", err)
		cancel()
		return
	}

	pipeline := &TranscodePipeline{
		publisher:   pub,
		transcoder:  mt,
		variants:    variants,
		subscribers: make(map[string][]*Subscriber),
		ctx:         ctx,
		cancel:      cancel,
	}
	pipeline.running.Store(true)
	currentPipeline = pipeline

	log.Printf("Transcode pipeline started with %d variants:", len(variants))
	for _, v := range variants {
		log.Printf("  - %s: %s", v.ID, v.Label)
	}

	// Start transcode loop
	go runTranscodeLoop(pipeline)
}

func runTranscodeLoop(p *TranscodePipeline) {
	defer func() {
		p.running.Store(false)
		p.transcoder.Close()
		// Close all subscriber channels
		p.subscribersMu.Lock()
		for _, subs := range p.subscribers {
			for _, sub := range subs {
				sub.closed.Store(true)
				close(sub.frameCh)
			}
		}
		p.subscribersMu.Unlock()
	}()

	frameCount := 0
	startTime := time.Now()
	gotKeyframe := false // Track if we've received a keyframe yet

	// Timeout for transcode operations - prevents hangs
	const transcodeTimeout = 500 * time.Millisecond

	for {
		select {
		case <-p.ctx.Done():
			return
		case frame := <-p.publisher.frameCh:
			if frame == nil {
				continue
			}

			// Wait for first keyframe before transcoding (decoder needs keyframe to start)
			if !gotKeyframe {
				if frame.FrameType != media.FrameTypeKey {
					continue // Skip until we get a keyframe
				}
				gotKeyframe = true
				log.Printf("Transcode: received first keyframe, starting decode")
			}

			// Transcode with timeout to prevent hangs
			encodeStart := time.Now()
			resultCh := make(chan struct {
				result *media.TranscodeResult
				err    error
			}, 1)

			go func(f *media.EncodedFrame) {
				result, err := p.transcoder.Transcode(f)
				resultCh <- struct {
					result *media.TranscodeResult
					err    error
				}{result, err}
			}(frame)

			var result *media.TranscodeResult
			var err error

			select {
			case <-p.ctx.Done():
				return
			case <-time.After(transcodeTimeout):
				p.frameDrops.Add(1)
				if p.frameDrops.Load()%10 == 0 {
					log.Printf("Transcode: timeout, dropped %d frames total", p.frameDrops.Load())
				}
				// Transcoder will auto-request keyframe from source after consecutive errors
				continue
			case res := <-resultCh:
				result = res.result
				err = res.err
			}

			encodeTime := time.Since(encodeStart)
			p.avgEncodeTimeNs.Store((p.avgEncodeTimeNs.Load()*9 + int64(encodeTime)) / 10)
			p.lastFrameTime.Store(time.Now().UnixNano())

			if err != nil {
				// Transcoder handles auto-recovery internally
				if frameCount%100 == 0 {
					log.Printf("Transcode error (frame %d): %v", frameCount, err)
				}
				continue
			}
			if result == nil {
				// Transcoder may be waiting for keyframe (auto-recovery in progress)
				continue
			}

			// Broadcast to all subscribers per variant
			p.subscribersMu.RLock()
			for _, variant := range result.Variants {
				if subs, ok := p.subscribers[variant.VariantID]; ok {
					for _, sub := range subs {
						if sub.closed.Load() {
							continue
						}
						select {
						case sub.frameCh <- variant.Frame:
						default:
							// Drop if subscriber too slow
						}
					}
				}
			}
			p.subscribersMu.RUnlock()

			frameCount++
			if frameCount%300 == 0 {
				elapsed := time.Since(startTime).Seconds()
				p.subscribersMu.RLock()
				totalSubs := 0
				for _, subs := range p.subscribers {
					totalSubs += len(subs)
				}
				p.subscribersMu.RUnlock()
				avgEncMs := float64(p.avgEncodeTimeNs.Load()) / 1e6
				log.Printf("Transcoded %d frames (%.1f fps), %d variants, %d subscribers, avg encode: %.1fms, drops: %d",
					frameCount, float64(frameCount)/elapsed, len(result.Variants), totalSubs, avgEncMs, p.frameDrops.Load())
			}
		}
	}
}

func stopTranscodePipeline() {
	publisherMu.Lock()
	defer publisherMu.Unlock()

	if currentPipeline != nil {
		currentPipeline.cancel()
		currentPipeline = nil
	}
	currentPublisher = nil
}

func handleSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	variantID := r.URL.Query().Get("variant")
	if variantID == "" {
		variantID = "source"
	}

	var offer webrtc.SessionDescription
	if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	publisherMu.RLock()
	pipeline := currentPipeline
	publisherMu.RUnlock()

	if pipeline == nil || !pipeline.running.Load() {
		http.Error(w, "No active stream", http.StatusNotFound)
		return
	}

	// Find variant
	var variant *TranscodeVariant
	for _, v := range pipeline.variants {
		if v.ID == variantID {
			variant = &v
			break
		}
	}
	if variant == nil {
		http.Error(w, "Variant not found: "+variantID, http.StatusNotFound)
		return
	}

	// Create subscriber with its own frame channel
	subID := fmt.Sprintf("%s-%d", variantID, time.Now().UnixNano())
	sub := &Subscriber{
		id:        subID,
		variantID: variantID,
		frameCh:   make(chan *media.EncodedFrame, 30),
	}

	// Register subscriber with pipeline
	pipeline.subscribersMu.Lock()
	pipeline.subscribers[variantID] = append(pipeline.subscribers[variantID], sub)
	pipeline.subscribersMu.Unlock()

	// Create peer connection
	pc, err := webrtcAPI.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Create output track
	codecCap := codecCapability(variant.Codec)
	track, err := webrtc.NewTrackLocalStaticRTP(codecCap, variantID, "transcoder")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	sender, err := pc.AddTrack(track)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// PLI handler - request keyframe when browser requests it
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buf); err != nil {
				return
			}
			// Any RTCP packet (likely PLI or REMB) - request keyframe
			publisherMu.RLock()
			p := currentPipeline
			publisherMu.RUnlock()
			if p != nil && p.transcoder != nil {
				if variantID == "source" {
					p.transcoder.RequestKeyframeAll()
				} else {
					p.transcoder.RequestKeyframe(variantID)
				}
			}
		}
	}()

	// We'll create the packetizer after SDP negotiation to get the correct payload type
	var packetizer media.RTPPacketizer
	var packetizerMu sync.Mutex

	// Cleanup function to remove subscriber
	cleanup := func() {
		sub.closed.Store(true)
		pipeline.subscribersMu.Lock()
		subs := pipeline.subscribers[variantID]
		for i, s := range subs {
			if s.id == subID {
				pipeline.subscribers[variantID] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		pipeline.subscribersMu.Unlock()
		pc.Close()
	}

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("Subscriber [%s]: %s", variantID, state)
		if state == webrtc.PeerConnectionStateConnected {
			// Get negotiated payload type from the sender
			senders := pc.GetSenders()
			if len(senders) > 0 {
				params := senders[0].GetParameters()
				if len(params.Codecs) > 0 {
					pt := uint8(params.Codecs[0].PayloadType)
					log.Printf("Subscriber [%s]: negotiated payload type %d, codec %s",
						variantID, pt, params.Codecs[0].MimeType)

					packetizerMu.Lock()
					packetizer, _ = media.CreateVideoPacketizer(variant.Codec, 0x12345678, pt, 1200)
					packetizerMu.Unlock()
				}
			}

			// Request keyframe when subscriber connects for faster video start
			publisherMu.RLock()
			p := currentPipeline
			publisherMu.RUnlock()
			if p != nil && p.transcoder != nil {
				if variantID == "source" {
					p.transcoder.RequestKeyframeAll()
				} else {
					p.transcoder.RequestKeyframe(variantID)
				}
				log.Printf("Subscriber [%s]: requested keyframe", variantID)
			}
		}
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			cleanup()
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
	if err := pc.SetLocalDescription(answer); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	<-gatherComplete

	// Start sending frames from subscriber's own channel
	go func() {
		// Wait for connection and packetizer to be ready
		for {
			if pc.ConnectionState() == webrtc.PeerConnectionStateConnected {
				packetizerMu.Lock()
				ready := packetizer != nil
				packetizerMu.Unlock()
				if ready {
					break
				}
			}
			if pc.ConnectionState() == webrtc.PeerConnectionStateFailed ||
				pc.ConnectionState() == webrtc.PeerConnectionStateClosed {
				log.Printf("Subscriber [%s]: connection failed before packetizer ready", variantID)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}

		log.Printf("Subscriber [%s]: packetizer ready, starting frame delivery", variantID)

		framesSent := 0
		gotKeyframe := false

		for frame := range sub.frameCh {
			if sub.closed.Load() || pc.ConnectionState() != webrtc.PeerConnectionStateConnected {
				log.Printf("Subscriber [%s]: disconnected after %d frames", variantID, framesSent)
				break
			}
			if frame == nil {
				continue
			}

			// Wait for keyframe before sending (subscriber needs keyframe to start decoding)
			if !gotKeyframe {
				if frame.FrameType != media.FrameTypeKey {
					continue
				}
				gotKeyframe = true
				log.Printf("Subscriber [%s]: got keyframe, starting playback", variantID)
			}

			packetizerMu.Lock()
			pkt := packetizer
			packetizerMu.Unlock()

			packets, err := pkt.Packetize(frame)
			if err != nil {
				log.Printf("Subscriber [%s]: packetize error: %v", variantID, err)
				continue
			}
			for _, p := range packets {
				if err := track.WriteRTP(p); err != nil {
					log.Printf("Subscriber [%s]: write error: %v", variantID, err)
					cleanup()
					return
				}
			}
			framesSent++
			if framesSent == 1 || framesSent%300 == 0 {
				log.Printf("Subscriber [%s]: sent %d frames (%d bytes, %d packets)",
					variantID, framesSent, len(frame.Data), len(packets))
			}
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pc.LocalDescription())
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	publisherMu.RLock()
	pipeline := currentPipeline
	publisherMu.RUnlock()

	status := struct {
		Active          bool               `json:"active"`
		Variants        []TranscodeVariant `json:"variants"`
		AvailableCodecs []string           `json:"availableCodecs"`
		// Performance stats
		FrameDrops    int64   `json:"frameDrops,omitempty"`
		AvgEncodeMs   float64 `json:"avgEncodeMs,omitempty"`
		SubscriberCount int   `json:"subscriberCount,omitempty"`
	}{}

	// Report available codecs
	if media.IsVP8Available() {
		status.AvailableCodecs = append(status.AvailableCodecs, "VP8")
	}
	if media.IsVP9Available() {
		status.AvailableCodecs = append(status.AvailableCodecs, "VP9")
	}
	if media.IsH264EncoderAvailable() {
		status.AvailableCodecs = append(status.AvailableCodecs, "H264")
	}
	if media.IsAV1Available() {
		status.AvailableCodecs = append(status.AvailableCodecs, "AV1")
	}

	if pipeline != nil && pipeline.running.Load() {
		status.Active = true
		status.Variants = pipeline.variants
		status.FrameDrops = pipeline.frameDrops.Load()
		status.AvgEncodeMs = float64(pipeline.avgEncodeTimeNs.Load()) / 1e6

		pipeline.subscribersMu.RLock()
		for _, subs := range pipeline.subscribers {
			status.SubscriberCount += len(subs)
		}
		pipeline.subscribersMu.RUnlock()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// Maximum number of encoder variants to prevent system overload
const maxVariants = 12

// handleAddVariant adds a new output variant dynamically
func handleAddVariant(w http.ResponseWriter, r *http.Request) {
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

	publisherMu.Lock()
	defer publisherMu.Unlock()

	if currentPipeline == nil || !currentPipeline.running.Load() {
		http.Error(w, "No active stream", http.StatusBadRequest)
		return
	}

	// Check variant limit to prevent overload
	if len(currentPipeline.variants) >= maxVariants {
		http.Error(w, fmt.Sprintf("Maximum %d variants reached. Remove some variants first.", maxVariants), http.StatusTooManyRequests)
		return
	}

	// Warn if system is already under load
	avgEncMs := float64(currentPipeline.avgEncodeTimeNs.Load()) / 1e6
	if avgEncMs > 33 { // More than one frame time at 30fps
		log.Printf("Warning: avg encode time %.1fms exceeds frame budget. Adding variant may cause drops.", avgEncMs)
	}

	// Parse codec
	var codec media.VideoCodec
	switch req.Codec {
	case "VP8", "vp8":
		codec = media.VideoCodecVP8
	case "VP9", "vp9":
		codec = media.VideoCodecVP9
	case "H264", "h264":
		codec = media.VideoCodecH264
	case "AV1", "av1":
		codec = media.VideoCodecAV1
	default:
		http.Error(w, "Unknown codec: "+req.Codec, http.StatusBadRequest)
		return
	}

	// Add to MultiTranscoder
	err := currentPipeline.transcoder.AddOutput(media.OutputConfig{
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

	// Add to variants list
	variant := TranscodeVariant{
		ID:         req.ID,
		Codec:      codec,
		Width:      req.Width,
		Height:     req.Height,
		BitrateBps: req.BitrateBps,
		Label:      fmt.Sprintf("%s %dx%d", req.Codec, req.Width, req.Height),
	}
	currentPipeline.variants = append(currentPipeline.variants, variant)

	log.Printf("Added variant dynamically: %s (%s %dx%d @ %d bps)",
		req.ID, req.Codec, req.Width, req.Height, req.BitrateBps)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"variant": variant,
	})
}

// handleRemoveVariant removes an output variant dynamically
func handleRemoveVariant(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	variantID := r.URL.Query().Get("id")
	if variantID == "" {
		http.Error(w, "id parameter required", http.StatusBadRequest)
		return
	}

	publisherMu.Lock()
	defer publisherMu.Unlock()

	if currentPipeline == nil || !currentPipeline.running.Load() {
		http.Error(w, "No active stream", http.StatusBadRequest)
		return
	}

	// Remove from MultiTranscoder
	err := currentPipeline.transcoder.RemoveOutput(variantID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Close all subscribers for this variant
	currentPipeline.subscribersMu.Lock()
	if subs, ok := currentPipeline.subscribers[variantID]; ok {
		for _, sub := range subs {
			sub.closed.Store(true)
			close(sub.frameCh)
		}
		delete(currentPipeline.subscribers, variantID)
	}
	currentPipeline.subscribersMu.Unlock()

	// Remove from variants list
	newVariants := make([]TranscodeVariant, 0)
	for _, v := range currentPipeline.variants {
		if v.ID != variantID {
			newVariants = append(newVariants, v)
		}
	}
	currentPipeline.variants = newVariants

	log.Printf("Removed variant dynamically: %s", variantID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func detectCodecFromMime(mime string) media.VideoCodec {
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

func codecCapability(codec media.VideoCodec) webrtc.RTPCodecCapability {
	switch codec {
	case media.VideoCodecVP8:
		return webrtc.RTPCodecCapability{MimeType: "video/VP8", ClockRate: 90000}
	case media.VideoCodecVP9:
		return webrtc.RTPCodecCapability{MimeType: "video/VP9", ClockRate: 90000}
	case media.VideoCodecH264:
		return webrtc.RTPCodecCapability{
			MimeType:    "video/H264",
			ClockRate:   90000,
			SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
		}
	case media.VideoCodecAV1:
		return webrtc.RTPCodecCapability{MimeType: "video/AV1", ClockRate: 90000}
	default:
		return webrtc.RTPCodecCapability{MimeType: "video/VP8", ClockRate: 90000}
	}
}

func serveHTML(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head>
    <title>WebRTC Multi-Transcoder</title>
    <style>
        * { box-sizing: border-box; }
        body {
            font-family: system-ui, -apple-system, sans-serif;
            background: linear-gradient(135deg, #1a1a2e 0%, #16213e 100%);
            color: #eee;
            margin: 0;
            padding: 20px;
            min-height: 100vh;
        }
        h1 { color: #00d9ff; margin-bottom: 5px; }
        h2 { color: #ff6b6b; margin: 20px 0 10px; font-size: 1.2em; }
        .subtitle { color: #888; margin-bottom: 20px; }
        .container { max-width: 1400px; margin: 0 auto; }

        .section {
            background: rgba(255,255,255,0.05);
            border-radius: 12px;
            padding: 20px;
            margin: 20px 0;
        }

        .publish-section { border-left: 4px solid #00d9ff; }
        .subscribe-section { border-left: 4px solid #ff6b6b; }

        video {
            width: 100%;
            background: #000;
            border-radius: 8px;
            aspect-ratio: 16/9;
        }

        .video-grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
            gap: 15px;
        }

        .video-card {
            background: rgba(0,0,0,0.3);
            border-radius: 8px;
            overflow: hidden;
        }

        .video-label {
            padding: 10px;
            background: rgba(0,0,0,0.5);
            font-size: 0.9em;
            display: flex;
            justify-content: space-between;
            align-items: center;
        }

        .codec-badge {
            padding: 3px 8px;
            border-radius: 4px;
            font-size: 0.8em;
            font-weight: bold;
        }
        .codec-vp8 { background: #4CAF50; }
        .codec-vp9 { background: #2196F3; }
        .codec-h264 { background: #FF9800; }
        .codec-av1 { background: #E91E63; }
        .codec-source { background: #9C27B0; }

        button {
            padding: 12px 24px;
            background: linear-gradient(135deg, #00d9ff 0%, #00a8cc 100%);
            color: #000;
            border: none;
            border-radius: 6px;
            cursor: pointer;
            font-weight: bold;
            font-size: 1em;
            transition: transform 0.1s, box-shadow 0.1s;
        }
        button:hover {
            transform: translateY(-2px);
            box-shadow: 0 4px 12px rgba(0,217,255,0.3);
        }
        button:disabled {
            background: #444;
            color: #888;
            cursor: not-allowed;
            transform: none;
            box-shadow: none;
        }

        .status {
            padding: 10px 15px;
            background: rgba(0,0,0,0.3);
            border-radius: 6px;
            font-family: monospace;
            margin: 10px 0;
        }
        .status.connected { border-left: 3px solid #4CAF50; }
        .status.error { border-left: 3px solid #f44336; }

        .stats {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
            gap: 10px;
            margin-top: 15px;
        }
        .stat-box {
            background: rgba(0,0,0,0.3);
            padding: 10px;
            border-radius: 6px;
            text-align: center;
        }
        .stat-value { font-size: 1.5em; color: #00d9ff; }
        .stat-label { font-size: 0.8em; color: #888; }
    </style>
</head>
<body>
    <div class="container">
        <h1>WebRTC Multi-Transcoder</h1>
        <p class="subtitle">Publish once, transcode to multiple codecs and resolutions in real-time</p>

        <div class="section publish-section">
            <h2>Publish</h2>
            <div class="video-card" style="max-width: 640px;">
                <video id="localVideo" autoplay muted playsinline></video>
                <div class="video-label">
                    <span>Your Camera</span>
                    <span id="publishCodec" class="codec-badge" style="display:none;"></span>
                </div>
            </div>
            <div style="margin-top: 15px; display: flex; flex-wrap: wrap; gap: 10px; align-items: center;">
                <label style="color: #888;">Resolution:</label>
                <select id="publishResolution" style="padding: 8px 12px; background: #333; color: #fff; border: 1px solid #555; border-radius: 4px;">
                    <option value="1920x1080">1080p</option>
                    <option value="1280x720" selected>720p</option>
                    <option value="854x480">480p</option>
                    <option value="640x360">360p</option>
                </select>
                <label style="color: #888;">Codec:</label>
                <select id="publishCodecSelect" style="padding: 8px 12px; background: #333; color: #fff; border: 1px solid #555; border-radius: 4px;">
                    <option value="VP8">VP8</option>
                    <option value="H264">H.264</option>
                    <option value="VP9">VP9</option>
                    <option value="AV1">AV1</option>
                </select>
                <button id="publishBtn" onclick="publish()">Start Publishing</button>
                <span id="publishStatus" class="status" style="display:inline-block;">Ready</span>
            </div>
        </div>

        <div class="section subscribe-section">
            <h2>Transcoded Outputs</h2>
            <p style="color: #888; margin-bottom: 15px;">Each variant is a separate WebRTC track with different codec/resolution</p>

            <!-- Dynamic Variant Controls -->
            <div id="addVariantSection" style="display: none; margin-bottom: 20px; padding: 15px; background: rgba(0,0,0,0.2); border-radius: 8px;">
                <h3 style="margin: 0 0 10px; color: #00d9ff; font-size: 1em;">Add New Output On-The-Fly</h3>
                <div style="display: flex; flex-wrap: wrap; gap: 10px; align-items: center;">
                    <select id="addCodec" style="padding: 8px; background: #333; color: #fff; border: 1px solid #555; border-radius: 4px;">
                    </select>
                    <select id="addResolution" style="padding: 8px; background: #333; color: #fff; border: 1px solid #555; border-radius: 4px;">
                        <option value="1920x1080">1080p</option>
                        <option value="1280x720" selected>720p</option>
                        <option value="854x480">480p</option>
                        <option value="640x360">360p</option>
                        <option value="320x180">180p</option>
                    </select>
                    <input id="addBitrate" type="number" value="500" placeholder="Bitrate (kbps)" style="width: 100px; padding: 8px; background: #333; color: #fff; border: 1px solid #555; border-radius: 4px;">
                    <span style="color: #888;">kbps</span>
                    <button onclick="addVariant()" style="background: linear-gradient(135deg, #4CAF50 0%, #388E3C 100%);">+ Add Variant</button>
                </div>
            </div>

            <div id="subscribersGrid" class="video-grid">
                <div style="padding: 40px; text-align: center; color: #666;">
                    Waiting for publisher...
                </div>
            </div>
            <div class="stats" id="stats" style="display: none;">
                <div class="stat-box">
                    <div class="stat-value" id="statVariants">0</div>
                    <div class="stat-label">Variants</div>
                </div>
                <div class="stat-box">
                    <div class="stat-value" id="statSubscribers">0</div>
                    <div class="stat-label">Subscribers</div>
                </div>
                <div class="stat-box">
                    <div class="stat-value" id="statEncodeMs">0</div>
                    <div class="stat-label">Encode (ms)</div>
                </div>
                <div class="stat-box">
                    <div class="stat-value" id="statDrops">0</div>
                    <div class="stat-label">Frame Drops</div>
                </div>
            </div>
            <div id="perfWarning" style="display: none; padding: 10px; background: rgba(255,100,100,0.2); border-radius: 6px; color: #ff6b6b; margin-top: 10px;">
                ⚠️ <span id="perfWarningText"></span>
            </div>
        </div>
    </div>

    <script>
    let localStream = null;
    let publishPC = null;
    let subscribers = {};
    let statusInterval = null;

    // Detect supported codecs on page load
    (function detectCodecs() {
        const select = document.getElementById('publishCodecSelect');
        const capabilities = RTCRtpSender.getCapabilities('video');
        if (!capabilities) return;

        const supported = new Set();
        capabilities.codecs.forEach(c => {
            const name = c.mimeType.split('/')[1];
            supported.add(name);
        });

        // Update dropdown to show which codecs are available
        Array.from(select.options).forEach(opt => {
            const codecName = opt.value;
            if (!supported.has(codecName)) {
                opt.textContent = opt.textContent + ' (not supported)';
                opt.disabled = true;
            }
        });

        console.log('Browser supported video codecs:', Array.from(supported));
    })();

    async function publish() {
        const btn = document.getElementById('publishBtn');
        const status = document.getElementById('publishStatus');

        if (publishPC) {
            // Stop publishing
            publishPC.close();
            publishPC = null;
            if (localStream) {
                localStream.getTracks().forEach(t => t.stop());
                localStream = null;
            }
            btn.textContent = 'Start Publishing';
            status.textContent = 'Stopped';
            status.className = 'status';
            document.getElementById('publishCodec').style.display = 'none';
            return;
        }

        try {
            status.textContent = 'Getting camera...';

            // Get selected resolution
            const [resWidth, resHeight] = document.getElementById('publishResolution').value.split('x').map(Number);

            // Get camera with selected resolution
            localStream = await navigator.mediaDevices.getUserMedia({
                video: {
                    width: { ideal: resWidth },
                    height: { ideal: resHeight },
                    frameRate: { ideal: 30 }
                },
                audio: false
            });

            document.getElementById('localVideo').srcObject = localStream;

            status.textContent = 'Connecting...';

            publishPC = new RTCPeerConnection({
                iceServers: [{ urls: 'stun:stun.l.google.com:19302' }]
            });

            // Get selected codec from dropdown
            const selectedCodec = document.getElementById('publishCodecSelect').value;
            const mimeType = 'video/' + selectedCodec;

            const transceiver = publishPC.addTransceiver(localStream.getVideoTracks()[0], {
                direction: 'sendonly'
            });

            // Set codec preference based on user selection
            const allCodecs = RTCRtpSender.getCapabilities('video').codecs;
            const preferredCodecs = allCodecs.filter(c => c.mimeType === mimeType);
            const otherCodecs = allCodecs.filter(c => c.mimeType !== mimeType);

            if (preferredCodecs.length > 0 && transceiver.setCodecPreferences) {
                transceiver.setCodecPreferences([...preferredCodecs, ...otherCodecs]);
                console.log('Codec preference set to:', selectedCodec);
            } else if (preferredCodecs.length === 0) {
                console.warn('Codec not supported by browser:', selectedCodec);
                status.textContent = 'Warning: ' + selectedCodec + ' not supported, using fallback';
            }

            publishPC.oniceconnectionstatechange = () => {
                if (publishPC.iceConnectionState === 'connected') {
                    status.textContent = 'Connected!';
                    status.className = 'status connected';

                    // Show codec being used
                    const sender = publishPC.getSenders()[0];
                    if (sender) {
                        const params = sender.getParameters();
                        if (params.codecs && params.codecs[0]) {
                            const codec = params.codecs[0].mimeType.split('/')[1];
                            const badge = document.getElementById('publishCodec');
                            badge.textContent = codec;
                            badge.className = 'codec-badge codec-' + codec.toLowerCase();
                            badge.style.display = 'inline-block';
                        }
                    }

                    // Start polling for variants
                    startStatusPolling();
                } else if (publishPC.iceConnectionState === 'failed') {
                    status.textContent = 'Connection failed';
                    status.className = 'status error';
                }
            };

            const offer = await publishPC.createOffer();
            await publishPC.setLocalDescription(offer);

            const resp = await fetch('/publish', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(publishPC.localDescription)
            });

            if (!resp.ok) throw new Error(await resp.text());

            const answer = await resp.json();
            await publishPC.setRemoteDescription(answer);

            btn.textContent = 'Stop Publishing';

        } catch (err) {
            status.textContent = 'Error: ' + err.message;
            status.className = 'status error';
            console.error(err);
        }
    }

    function startStatusPolling() {
        if (statusInterval) clearInterval(statusInterval);
        statusInterval = setInterval(pollStatus, 1000);
        pollStatus();
    }

    async function pollStatus() {
        try {
            const resp = await fetch('/status');
            const status = await resp.json();

            // Populate codec dropdown with available codecs
            if (status.availableCodecs) {
                const select = document.getElementById('addCodec');
                if (select.options.length === 0) {
                    status.availableCodecs.forEach(codec => {
                        const opt = document.createElement('option');
                        opt.value = codec;
                        opt.textContent = codec;
                        select.appendChild(opt);
                    });
                }
            }

            if (status.active && status.variants) {
                document.getElementById('addVariantSection').style.display = 'block';
                updateSubscribers(status.variants);
                document.getElementById('stats').style.display = 'grid';
                document.getElementById('statVariants').textContent = status.variants.length;
                document.getElementById('statSubscribers').textContent = status.subscriberCount || 0;
                document.getElementById('statEncodeMs').textContent = (status.avgEncodeMs || 0).toFixed(1);
                document.getElementById('statDrops').textContent = status.frameDrops || 0;

                // Show warning if performance is degraded
                const warning = document.getElementById('perfWarning');
                const warningText = document.getElementById('perfWarningText');
                if (status.avgEncodeMs > 33) {
                    warning.style.display = 'block';
                    warningText.textContent = 'Encoding is taking longer than frame budget (' + status.avgEncodeMs.toFixed(1) + 'ms > 33ms). Consider removing some variants.';
                } else if (status.frameDrops > 0) {
                    warning.style.display = 'block';
                    warningText.textContent = 'Frames are being dropped (' + status.frameDrops + '). System is overloaded.';
                } else {
                    warning.style.display = 'none';
                }
            }
        } catch (err) {
            console.error('Status poll error:', err);
        }
    }

    let variantCounter = 0;
    async function addVariant() {
        const codec = document.getElementById('addCodec').value;
        const [width, height] = document.getElementById('addResolution').value.split('x').map(Number);
        const bitrate = parseInt(document.getElementById('addBitrate').value) * 1000;

        variantCounter++;
        const id = 'dynamic-' + codec.toLowerCase() + '-' + variantCounter;

        try {
            const resp = await fetch('/add-variant', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    id: id,
                    codec: codec,
                    width: width,
                    height: height,
                    bitrateBps: bitrate
                })
            });

            if (resp.ok) {
                const result = await resp.json();
                console.log('Added variant:', result.variant);
                // pollStatus will pick up the new variant
            } else {
                const err = await resp.text();
                alert('Failed to add variant: ' + err);
            }
        } catch (err) {
            alert('Error adding variant: ' + err.message);
        }
    }

    async function removeVariant(id) {
        if (!confirm('Remove variant ' + id + '?')) return;

        try {
            const resp = await fetch('/remove-variant?id=' + encodeURIComponent(id), {
                method: 'POST'
            });

            if (resp.ok) {
                // Remove video card
                const card = document.getElementById('card-' + id);
                if (card) card.remove();
                // Close subscriber
                if (subscribers[id]) {
                    subscribers[id].pc.close();
                    delete subscribers[id];
                }
                console.log('Removed variant:', id);
            } else {
                const err = await resp.text();
                alert('Failed to remove variant: ' + err);
            }
        } catch (err) {
            alert('Error removing variant: ' + err.message);
        }
    }

    function updateSubscribers(variants) {
        const grid = document.getElementById('subscribersGrid');

        // Add new variants
        for (const v of variants) {
            if (!subscribers[v.ID]) {
                createSubscriber(v);
            }
        }
    }

    async function createSubscriber(variant) {
        const grid = document.getElementById('subscribersGrid');

        // Remove "waiting" message
        if (grid.querySelector('div[style*="text-align: center"]')) {
            grid.innerHTML = '';
        }

        // Create video card
        const card = document.createElement('div');
        card.className = 'video-card';
        card.id = 'card-' + variant.ID;

        const video = document.createElement('video');
        video.autoplay = true;
        video.playsinline = true;
        video.muted = true;

        const label = document.createElement('div');
        label.className = 'video-label';

        const codecClass = variant.ID === 'source' ? 'source' :
            (variant.Codec === 1 ? 'vp8' : variant.Codec === 2 ? 'vp9' : variant.Codec === 5 ? 'av1' : variant.Codec === 3 ? 'h264' : 'h265');

        const isDynamic = variant.ID.startsWith('dynamic-');
        const removeBtn = isDynamic ? '<button onclick="removeVariant(\'' + variant.ID + '\')" style="padding: 2px 8px; background: #f44336; font-size: 0.8em; margin-left: 5px;">X</button>' : '';

        label.innerHTML = '<span>' + variant.Label + '</span>' +
            '<span><span class="codec-badge codec-' + codecClass + '">' +
            (variant.ID === 'source' ? 'PASSTHROUGH' : getCodecName(variant.Codec)) + '</span>' + removeBtn + '</span>';

        card.appendChild(video);
        card.appendChild(label);
        grid.appendChild(card);

        // Create WebRTC connection
        const pc = new RTCPeerConnection({
            iceServers: [{ urls: 'stun:stun.l.google.com:19302' }]
        });

        pc.ontrack = (e) => {
            video.srcObject = e.streams[0];
        };

        pc.addTransceiver('video', { direction: 'recvonly' });

        const offer = await pc.createOffer();
        await pc.setLocalDescription(offer);

        const resp = await fetch('/subscribe?variant=' + variant.ID, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(pc.localDescription)
        });

        if (resp.ok) {
            const answer = await resp.json();
            await pc.setRemoteDescription(answer);
            subscribers[variant.ID] = { pc, video };
        }
    }

    function getCodecName(codec) {
        switch(codec) {
            case 1: return 'VP8';
            case 2: return 'VP9';
            case 3: return 'H264';
            case 4: return 'H265';
            case 5: return 'AV1';
            default: return 'Unknown';
        }
    }
    </script>
</body>
</html>`)
}
