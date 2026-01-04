// RTMP to WebRTC relay - accepts RTMP push and streams to browser via WebRTC
//
// Supports:
//   - Passthrough: H.264 -> WebRTC H.264 (lowest latency)
//   - Transcode: H.264 -> VP8/VP9/H264 (codec flexibility)
//
// Usage:
//   1. Run: go run main.go
//   2. Open http://localhost:8080
//   3. Push: ffmpeg -re -i video.mp4 -c:v libx264 -f flv rtmp://localhost:1935/live/stream
//
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/thesyncim/media"
	"github.com/yutopp/go-rtmp"
	rtmpmsg "github.com/yutopp/go-rtmp/message"
)

var (
	// Config
	outputCodec   = "passthrough" // passthrough, vp8, vp9, h264
	outputBitrate = 2_000_000

	// State
	streamMu      sync.RWMutex
	currentStream *rtmpStream
	viewerCount   atomic.Int32
)

type rtmpStream struct {
	width, height int
	sps, pps      []byte
	frameCh       chan *media.EncodedFrame
	closed        atomic.Bool
}

func main() {
	go startRTMPServer()

	http.HandleFunc("/", serveHTML)
	http.HandleFunc("/offer", handleOffer)
	http.HandleFunc("/status", handleStatus)
	http.HandleFunc("/config", handleConfig)

	log.Println("RTMP to WebRTC Relay")
	log.Println("  RTMP: rtmp://localhost:1935/live/stream")
	log.Println("  Web:  http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func startRTMPServer() {
	ln, err := net.Listen("tcp", ":1935")
	if err != nil {
		log.Fatal(err)
	}
	log.Println("RTMP listening on :1935")

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
	srv.Serve(ln)
}

type rtmpHandler struct {
	rtmp.DefaultHandler
}

func (h *rtmpHandler) OnPublish(_ *rtmp.StreamContext, _ uint32, cmd *rtmpmsg.NetStreamPublish) error {
	log.Printf("RTMP: Publishing %s", cmd.PublishingName)

	stream := &rtmpStream{
		frameCh: make(chan *media.EncodedFrame, 60),
	}

	streamMu.Lock()
	if currentStream != nil {
		currentStream.closed.Store(true)
	}
	currentStream = stream
	streamMu.Unlock()

	return nil
}

func (h *rtmpHandler) OnVideo(timestamp uint32, payload io.Reader) error {
	streamMu.RLock()
	stream := currentStream
	streamMu.RUnlock()

	if stream == nil || stream.closed.Load() {
		return nil
	}

	// Read payload into buffer
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, payload); err != nil {
		return nil
	}
	data := buf.Bytes()

	if len(data) < 5 {
		return nil
	}

	// FLV video tag header
	frameType := (data[0] >> 4) & 0x0F
	codecID := data[0] & 0x0F

	if codecID != 7 { // Not AVC/H.264
		return nil
	}

	avcType := data[1]
	avcData := data[5:]

	switch avcType {
	case 0: // Sequence header
		if stream.sps == nil {
			stream.sps, stream.pps = extractSPSPPS(avcData)
			if stream.sps != nil {
				stream.width, stream.height = 1920, 1080 // Default, could parse SPS
				log.Printf("RTMP: Video %dx%d", stream.width, stream.height)
			}
		}

	case 1: // NALU
		if stream.sps == nil {
			return nil
		}

		nalus := parseAVCCNALUs(avcData)
		if len(nalus) == 0 {
			return nil
		}

		isKey := frameType == 1
		annexB := buildAnnexB(nalus, stream.sps, stream.pps, isKey)

		ft := media.FrameTypeDelta
		if isKey {
			ft = media.FrameTypeKey
		}

		frame := &media.EncodedFrame{
			Data:      annexB,
			FrameType: ft,
			Timestamp: timestamp * 90, // Convert ms to 90kHz
		}

		select {
		case stream.frameCh <- frame:
		default:
		}
	}

	return nil
}

func (h *rtmpHandler) OnClose() {
	log.Println("RTMP: Disconnected")
	streamMu.Lock()
	if currentStream != nil {
		currentStream.closed.Store(true)
		currentStream = nil
	}
	streamMu.Unlock()
}

func extractSPSPPS(data []byte) (sps, pps []byte) {
	if len(data) < 8 {
		return
	}
	offset := 5
	numSPS := int(data[offset] & 0x1F)
	offset++

	for i := 0; i < numSPS && offset+2 <= len(data); i++ {
		length := int(data[offset])<<8 | int(data[offset+1])
		offset += 2
		if offset+length <= len(data) {
			sps = make([]byte, length)
			copy(sps, data[offset:offset+length])
			offset += length
		}
	}

	if offset >= len(data) {
		return
	}
	numPPS := int(data[offset])
	offset++

	for i := 0; i < numPPS && offset+2 <= len(data); i++ {
		length := int(data[offset])<<8 | int(data[offset+1])
		offset += 2
		if offset+length <= len(data) {
			pps = make([]byte, length)
			copy(pps, data[offset:offset+length])
			offset += length
		}
	}
	return
}

func parseAVCCNALUs(data []byte) [][]byte {
	var nalus [][]byte
	for offset := 0; offset+4 <= len(data); {
		length := int(data[offset])<<24 | int(data[offset+1])<<16 | int(data[offset+2])<<8 | int(data[offset+3])
		offset += 4
		if length <= 0 || offset+length > len(data) {
			break
		}
		nalu := make([]byte, length)
		copy(nalu, data[offset:offset+length])
		nalus = append(nalus, nalu)
		offset += length
	}
	return nalus
}

func buildAnnexB(nalus [][]byte, sps, pps []byte, isKey bool) []byte {
	sc := []byte{0, 0, 0, 1}
	var out []byte

	if isKey && sps != nil && pps != nil {
		out = append(out, sc...)
		out = append(out, sps...)
		out = append(out, sc...)
		out = append(out, pps...)
	}

	for _, nalu := range nalus {
		out = append(out, sc...)
		out = append(out, nalu...)
	}
	return out
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	streamMu.RLock()
	hasStream := currentStream != nil && !currentStream.closed.Load()
	width, height := 0, 0
	if currentStream != nil {
		width, height = currentStream.width, currentStream.height
	}
	streamMu.RUnlock()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"streaming": hasStream,
		"width":     width,
		"height":    height,
		"viewers":   viewerCount.Load(),
		"codec":     outputCodec,
	})
}

func handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"codec":   outputCodec,
			"bitrate": outputBitrate,
		})
		return
	}

	var req struct {
		Codec   string `json:"codec"`
		Bitrate int    `json:"bitrate"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if req.Codec != "" {
		outputCodec = req.Codec
	}
	if req.Bitrate > 0 {
		outputBitrate = req.Bitrate
	}
	log.Printf("Config: codec=%s bitrate=%d", outputCodec, outputBitrate)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func handleOffer(w http.ResponseWriter, r *http.Request) {
	var offer webrtc.SessionDescription
	if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	answer, err := createViewer(offer)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	json.NewEncoder(w).Encode(answer)
}

func createViewer(offer webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, err
	}

	// Select codec
	var mime string
	var outCodec media.VideoCodec
	switch outputCodec {
	case "vp8":
		mime, outCodec = webrtc.MimeTypeVP8, media.VideoCodecVP8
	case "vp9":
		mime, outCodec = webrtc.MimeTypeVP9, media.VideoCodecVP9
	default:
		mime, outCodec = webrtc.MimeTypeH264, media.VideoCodecH264
	}

	track, _ := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: mime, ClockRate: 90000},
		"video", "rtmp",
	)
	sender, _ := pc.AddTrack(track)

	// PLI handler
	pliCh := make(chan struct{}, 1)
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buf); err != nil {
				return
			}
			select {
			case pliCh <- struct{}{}:
			default:
			}
		}
	}()

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("Viewer: %s", state)
		if state == webrtc.PeerConnectionStateConnected {
			viewerCount.Add(1)
			go streamToViewer(pc, track, pliCh, outCodec)
		}
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			viewerCount.Add(-1)
		}
	})

	pc.SetRemoteDescription(offer)
	answer, _ := pc.CreateAnswer(nil)
	done := webrtc.GatheringCompletePromise(pc)
	pc.SetLocalDescription(answer)
	<-done

	return pc.LocalDescription(), nil
}

func streamToViewer(pc *webrtc.PeerConnection, track *webrtc.TrackLocalStaticRTP, pliCh chan struct{}, outCodec media.VideoCodec) {
	// Create transcoder or passthrough
	var transcoder *media.Transcoder
	var packetizer media.RTPPacketizer
	var err error

	needsTranscode := outputCodec != "passthrough"

	if needsTranscode {
		streamMu.RLock()
		w, h := 1920, 1080
		if currentStream != nil && currentStream.width > 0 {
			w, h = currentStream.width, currentStream.height
		}
		streamMu.RUnlock()

		transcoder, err = media.NewTranscoder(media.TranscoderConfig{
			InputCodec:  media.VideoCodecH264,
			OutputCodec: outCodec,
			Width:       w,
			Height:      h,
			BitrateBps:  outputBitrate,
			FPS:         30,
		})
		if err != nil {
			log.Printf("Transcoder error: %v", err)
			return
		}
		defer transcoder.Close()
	}

	packetizer, _ = media.CreateVideoPacketizer(outCodec, 0x12345678, 96, 1200)

	for pc.ConnectionState() == webrtc.PeerConnectionStateConnected {
		streamMu.RLock()
		stream := currentStream
		streamMu.RUnlock()

		if stream == nil || stream.closed.Load() {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		select {
		case <-pliCh:
			if transcoder != nil {
				transcoder.RequestKeyframe()
			}

		case frame := <-stream.frameCh:
			if frame == nil {
				continue
			}

			var outFrame *media.EncodedFrame
			if needsTranscode && transcoder != nil {
				outFrame, err = transcoder.Transcode(frame)
				if err != nil || outFrame == nil {
					continue
				}
			} else {
				outFrame = frame
			}

			packets, _ := packetizer.Packetize(outFrame)
			for _, pkt := range packets {
				track.WriteRTP(pkt)
			}

		case <-time.After(100 * time.Millisecond):
		}
	}
}

func serveHTML(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head>
    <title>RTMP to WebRTC</title>
    <style>
        body { font-family: system-ui; max-width: 800px; margin: 40px auto; padding: 20px; background: #1a1a2e; color: #eee; }
        h1 { color: #ff6b6b; }
        video { width: 100%; background: #000; border-radius: 8px; }
        .controls { margin: 15px 0; display: flex; gap: 10px; align-items: center; }
        button, select { padding: 10px 20px; background: #333; color: #fff; border: 1px solid #555; border-radius: 4px; cursor: pointer; }
        button:hover { background: #444; }
        .status { padding: 10px; background: #252540; border-radius: 4px; margin: 10px 0; font-family: monospace; }
        code { background: #333; padding: 2px 6px; border-radius: 3px; }
        .info { background: #252540; padding: 15px; border-radius: 8px; margin: 15px 0; }
    </style>
</head>
<body>
    <h1>RTMP to WebRTC Relay</h1>
    <div id="status" class="status">Waiting for stream...</div>
    <video id="video" autoplay playsinline muted controls></video>
    <div class="controls">
        <button onclick="connect()">Connect</button>
        <select id="codec" onchange="setCodec(this.value)">
            <option value="passthrough">H.264 Passthrough</option>
            <option value="vp8">VP8 (transcode)</option>
            <option value="vp9">VP9 (transcode)</option>
            <option value="h264">H.264 (transcode)</option>
        </select>
    </div>
    <div class="info">
        <b>Push with FFmpeg:</b><br>
        <code>ffmpeg -re -i video.mp4 -c:v libx264 -f flv rtmp://localhost:1935/live/stream</code>
    </div>
<script>
let pc;
async function connect() {
    pc = new RTCPeerConnection();
    pc.ontrack = e => document.getElementById('video').srcObject = new MediaStream([e.track]);
    pc.addTransceiver('video', {direction: 'recvonly'});
    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);
    await new Promise(r => { pc.onicecandidate = e => !e.candidate && r(); setTimeout(r, 1000); });
    const resp = await fetch('/offer', {method: 'POST', body: JSON.stringify(pc.localDescription)});
    await pc.setRemoteDescription(await resp.json());
}
async function setCodec(c) {
    await fetch('/config', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({codec: c})});
}
setInterval(async () => {
    const s = await (await fetch('/status')).json();
    document.getElementById('status').innerHTML = s.streaming
        ? 'Stream: ' + s.width + 'x' + s.height + ' | Viewers: ' + s.viewers + ' | Output: ' + s.codec
        : 'Waiting for RTMP stream...';
    document.getElementById('codec').value = s.codec;
}, 2000);
</script>
</body>
</html>`)
}
