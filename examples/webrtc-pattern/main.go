// WebRTC test pattern server - streams generated video to browser
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/thesyncim/media"
)

func main() {
	http.HandleFunc("/", serveHTML)
	http.HandleFunc("/offer", handleOffer)
	http.HandleFunc("/cameras", handleCameras)

	log.Println("Open http://localhost:8080 in your browser")
	log.Println("Supported codecs: VP8, VP9, H.264, AV1")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleCameras(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	provider := media.GetDeviceProvider()
	if provider == nil {
		json.NewEncoder(w).Encode([]struct{}{})
		return
	}

	devices, err := provider.ListVideoDevices(context.Background())
	if err != nil {
		log.Printf("ListVideoDevices error: %v", err)
		json.NewEncoder(w).Encode([]struct{}{})
		return
	}

	// Convert to simple format for JSON
	type cameraInfo struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	cameras := make([]cameraInfo, len(devices))
	for i, d := range devices {
		cameras[i] = cameraInfo{ID: d.DeviceID, Name: d.Label}
	}
	json.NewEncoder(w).Encode(cameras)
}

type streamConfig struct {
	Codec            string `json:"codec"`
	Width            int    `json:"width"`
	Height           int    `json:"height"`
	Bitrate          int    `json:"bitrate"`
	FPS              int    `json:"fps"`
	Source           string `json:"source"` // "pattern" or "camera"
	CameraID         string `json:"cameraId"`
	KeyframeInterval int    `json:"keyframeInterval"` // Keyframe interval in frames (0 = auto)
}

type offerRequest struct {
	SDP    string       `json:"sdp"`
	Type   string       `json:"type"`
	Config streamConfig `json:"config"`
}

func handleOffer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req offerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("Decode error: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  req.SDP,
	}

	// Map codec string to types
	var codec media.VideoCodec
	var mimeType string
	switch req.Config.Codec {
	case "vp9":
		codec, mimeType = media.VideoCodecVP9, webrtc.MimeTypeVP9
	case "h264":
		codec, mimeType = media.VideoCodecH264, webrtc.MimeTypeH264
	case "av1":
		codec, mimeType = media.VideoCodecAV1, webrtc.MimeTypeAV1
	default:
		codec, mimeType = media.VideoCodecVP8, webrtc.MimeTypeVP8
	}

	// Defaults
	if req.Config.Width == 0 {
		req.Config.Width = 640
	}
	if req.Config.Height == 0 {
		req.Config.Height = 480
	}
	if req.Config.Bitrate == 0 {
		req.Config.Bitrate = 1000
	}
	if req.Config.FPS == 0 {
		req.Config.FPS = 30
	}
	if req.Config.Source == "" {
		req.Config.Source = "pattern"
	}

	log.Printf("Stream config: %s %dx%d @ %d kbps, source: %s",
		req.Config.Codec, req.Config.Width, req.Config.Height, req.Config.Bitrate, req.Config.Source)

	answer, err := startStream(offer, codec, mimeType, req.Config)
	if err != nil {
		log.Printf("startStream error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(answer)
}

func startStream(offer webrtc.SessionDescription, codec media.VideoCodec, mimeType string, cfg streamConfig) (*webrtc.SessionDescription, error) {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, fmt.Errorf("NewPeerConnection: %w", err)
	}

	// Create video track
	track, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: mimeType},
		"video", "stream",
	)
	if err != nil {
		return nil, fmt.Errorf("NewTrackLocalStaticRTP: %w", err)
	}

	sender, err := pc.AddTrack(track)
	if err != nil {
		return nil, fmt.Errorf("AddTrack: %w", err)
	}

	// PLI channel
	pliChan := make(chan struct{}, 1)

	// Read RTCP for PLI
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buf); err != nil {
				return
			}
			select {
			case pliChan <- struct{}{}:
				log.Println("PLI received")
			default:
			}
		}
	}()

	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("ICE: %s", state)
	})

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("Connection: %s", state)
		if state == webrtc.PeerConnectionStateConnected {
			go streamVideo(pc, track, pliChan, codec, cfg)
		}
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			pc.Close()
		}
	})

	if err = pc.SetRemoteDescription(offer); err != nil {
		return nil, fmt.Errorf("SetRemoteDescription: %w", err)
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return nil, fmt.Errorf("CreateAnswer: %w", err)
	}

	gatherComplete := webrtc.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(answer); err != nil {
		return nil, fmt.Errorf("SetLocalDescription: %w", err)
	}

	<-gatherComplete
	log.Printf("ICE gathering complete, candidates: %d", len(pc.LocalDescription().SDP))

	return pc.LocalDescription(), nil
}

func streamVideo(pc *webrtc.PeerConnection, track *webrtc.TrackLocalStaticRTP, pliChan <-chan struct{}, codec media.VideoCodec, cfg streamConfig) {
	log.Printf("Starting stream: %s %dx%d @ %d kbps, source: %s", codec, cfg.Width, cfg.Height, cfg.Bitrate, cfg.Source)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create video source
	var source media.VideoSource
	var err error

	if cfg.Source == "camera" {
		source, err = media.NewCameraSource(media.CameraConfig{
			DeviceID:  cfg.CameraID,
			Width:     cfg.Width,
			Height:    cfg.Height,
			FPS:       cfg.FPS,
			ScaleMode: media.ScaleModeFit, // Scale without crop
		})
		if err != nil {
			log.Printf("Camera error: %v, falling back to pattern", err)
			source = nil
		}
	}

	if source == nil {
		source = media.NewTestPatternSource(media.TestPatternConfig{
			Width:   cfg.Width,
			Height:  cfg.Height,
			FPS:     cfg.FPS,
			Pattern: media.PatternMovingBox,
		})
	}

	defer source.Close()

	config := media.VideoEncoderConfig{
		Width:            cfg.Width,
		Height:           cfg.Height,
		BitrateBps:       cfg.Bitrate * 1000,
		FPS:              cfg.FPS,
		KeyframeInterval: cfg.KeyframeInterval,
	}

	var encoder media.VideoEncoder
	switch codec {
	case media.VideoCodecVP9:
		encoder, err = media.NewVP9Encoder(config)
	case media.VideoCodecH264:
		encoder, err = media.NewH264Encoder(config)
	case media.VideoCodecAV1:
		encoder, err = media.NewAV1Encoder(config)
	default:
		encoder, err = media.NewVP8Encoder(config)
	}
	if err != nil {
		log.Printf("Encoder error for %s: %v", codec, err)
		return
	}
	defer encoder.Close()

	packetizer, err := media.CreateVideoPacketizer(codec, 0x12345678, 96, 1200)
	if err != nil {
		log.Printf("Packetizer error: %v", err)
		return
	}

	source.Start(ctx)
	defer source.Stop()

	ticker := time.NewTicker(time.Second / time.Duration(cfg.FPS))
	defer ticker.Stop()

	frameCount := 0
	for {
		select {
		case <-pliChan:
			encoder.RequestKeyframe()
			log.Println("Keyframe requested")

		case <-ticker.C:
			if pc.ConnectionState() != webrtc.PeerConnectionStateConnected {
				log.Println("Connection closed, stopping stream")
				return
			}

			frame, err := source.ReadFrame(ctx)
			if err != nil {
				continue
			}

			encoded, err := encoder.Encode(frame)
			if err != nil || encoded == nil {
				continue
			}

			packets, err := packetizer.Packetize(encoded)
			if err != nil {
				continue
			}

			for _, pkt := range packets {
				if err := track.WriteRTP(pkt); err != nil {
					log.Printf("WriteRTP error: %v", err)
					return
				}
			}

			frameCount++
			if frameCount%(cfg.FPS) == 0 {
				log.Printf("Sent %d frames (%s)", frameCount, codec)
			}
		}
	}
}

func serveHTML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head>
    <title>WebRTC Stream</title>
    <style>
        * { box-sizing: border-box; }
        body { font-family: system-ui; max-width: 1000px; margin: 20px auto; padding: 20px; background: #f5f5f5; }
        h1 { color: #333; margin-bottom: 20px; }
        video { width: 100%; background: #000; border-radius: 8px; box-shadow: 0 4px 12px rgba(0,0,0,0.15); }
        .controls { background: #fff; padding: 20px; border-radius: 8px; margin: 20px 0; box-shadow: 0 2px 8px rgba(0,0,0,0.1); }
        .control-row { display: flex; flex-wrap: wrap; gap: 15px; align-items: center; margin-bottom: 15px; }
        .control-row:last-child { margin-bottom: 0; }
        .control-group { display: flex; flex-direction: column; gap: 4px; }
        .control-group label { font-size: 11px; color: #666; text-transform: uppercase; font-weight: 600; }
        select, input { padding: 10px 14px; font-size: 14px; border-radius: 6px; border: 1px solid #ddd; background: #fff; min-width: 120px; }
        select:focus, input:focus { outline: none; border-color: #4CAF50; }
        button { padding: 12px 28px; font-size: 15px; border-radius: 6px; border: none; cursor: pointer; font-weight: 600; }
        .btn-start { background: #4CAF50; color: white; }
        .btn-start:hover { background: #45a049; }
        .btn-stop { background: #f44336; color: white; }
        .btn-stop:hover { background: #d32f2f; }
        #stats { font-family: 'SF Mono', Monaco, monospace; font-size: 13px; background: #fff; padding: 15px; margin: 15px 0; border-radius: 8px; box-shadow: 0 2px 8px rgba(0,0,0,0.1); }
        .stats-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); gap: 12px; }
        .stat-card { background: #f8f9fa; padding: 10px 12px; border-radius: 6px; border-left: 3px solid #4CAF50; }
        .stat-card.warning { border-left-color: #ff9800; }
        .stat-card.error { border-left-color: #f44336; }
        .stat-label { font-size: 10px; color: #666; text-transform: uppercase; letter-spacing: 0.5px; }
        .stat-value { font-size: 16px; font-weight: 600; color: #333; margin-top: 2px; }
        .stat-unit { font-size: 11px; color: #888; font-weight: normal; }
        #log { font-family: 'SF Mono', Monaco, monospace; font-size: 11px; background: #263238; color: #aed581; padding: 12px; margin-top: 15px; max-height: 100px; overflow: auto; border-radius: 6px; white-space: pre; }
        .badge { display: inline-block; padding: 4px 10px; border-radius: 4px; font-size: 12px; font-weight: 600; }
        .badge-codec { background: #e3f2fd; color: #1976d2; }
        .badge-res { background: #f3e5f5; color: #7b1fa2; }
        .badge-source { background: #e8f5e9; color: #388e3c; }
        .status-bar { display: flex; gap: 10px; flex-wrap: wrap; margin-top: 10px; }
    </style>
</head>
<body>
    <h1>WebRTC Stream</h1>
    <video id="video" autoplay playsinline muted></video>

    <div class="controls">
        <div class="control-row">
            <div class="control-group">
                <label>Source</label>
                <select id="source" onchange="updateCameraList()">
                    <option value="pattern">Test Pattern</option>
                    <option value="camera">Camera</option>
                </select>
            </div>
            <div class="control-group" id="camera-group" style="display:none">
                <label>Camera</label>
                <select id="camera"></select>
            </div>
            <div class="control-group">
                <label>Codec</label>
                <select id="codec">
                    <option value="vp8">VP8</option>
                    <option value="vp9">VP9</option>
                    <option value="h264">H.264</option>
                    <option value="av1">AV1</option>
                </select>
            </div>
            <div class="control-group">
                <label>Resolution</label>
                <select id="resolution">
                    <option value="426x240">240p</option>
                    <option value="640x360">360p</option>
                    <option value="640x480" selected>480p</option>
                    <option value="1280x720">720p</option>
                    <option value="1920x1080">1080p</option>
                </select>
            </div>
            <div class="control-group">
                <label>Bitrate (kbps)</label>
                <input type="number" id="bitrate" value="1000" min="100" max="10000" step="100">
            </div>
            <div class="control-group">
                <label>FPS</label>
                <select id="fps">
                    <option value="15">15</option>
                    <option value="24">24</option>
                    <option value="30" selected>30</option>
                    <option value="60">60</option>
                </select>
            </div>
            <div class="control-group">
                <label>Keyframe Interval</label>
                <select id="keyframeInterval">
                    <option value="0">Auto</option>
                    <option value="30">1 sec</option>
                    <option value="60">2 sec</option>
                    <option value="150">5 sec</option>
                    <option value="300">10 sec</option>
                </select>
            </div>
        </div>
        <div class="control-row">
            <button id="startBtn" class="btn-start" onclick="start()">Start Stream</button>
            <button id="stopBtn" class="btn-stop" onclick="stop()" style="display:none">Stop</button>
            <div class="status-bar" id="status-bar"></div>
        </div>
    </div>
    <div id="stats"></div>
    <div id="log"></div>

<script>
let pc = null;
let prevStats = {};
let startTime = 0;
let statsInterval = null;

async function loadCameras() {
    try {
        const resp = await fetch('/cameras');
        const cameras = await resp.json();
        const select = document.getElementById('camera');
        if (cameras.length === 0) {
            select.innerHTML = '<option value="">No cameras found</option>';
        } else {
            select.innerHTML = cameras.map(c =>
                '<option value="' + c.id + '">' + c.name + '</option>'
            ).join('');
        }
    } catch (e) {
        console.error('Failed to load cameras:', e);
        document.getElementById('camera').innerHTML = '<option value="">Error loading cameras</option>';
    }
}

function updateCameraList() {
    const source = document.getElementById('source').value;
    const cameraGroup = document.getElementById('camera-group');
    cameraGroup.style.display = source === 'camera' ? 'flex' : 'none';
    if (source === 'camera') loadCameras();
}

function addLog(msg) {
    const log = document.getElementById('log');
    const time = new Date().toLocaleTimeString();
    log.textContent += '[' + time + '] ' + msg + '\n';
    log.scrollTop = log.scrollHeight;
}

function formatBytes(bytes) {
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
    return (bytes / (1024 * 1024)).toFixed(2) + ' MB';
}

function formatDuration(seconds) {
    const m = Math.floor(seconds / 60);
    const s = Math.floor(seconds % 60);
    return m + ':' + s.toString().padStart(2, '0');
}

function stop() {
    if (pc) {
        pc.close();
        pc = null;
    }
    if (statsInterval) {
        clearInterval(statsInterval);
        statsInterval = null;
    }
    document.getElementById('startBtn').style.display = '';
    document.getElementById('stopBtn').style.display = 'none';
    document.getElementById('status-bar').innerHTML = '';
    document.getElementById('video').srcObject = null;
    addLog('Stopped');
}

async function start() {
    if (pc) stop();

    const [width, height] = document.getElementById('resolution').value.split('x').map(Number);
    const config = {
        codec: document.getElementById('codec').value,
        width: width,
        height: height,
        bitrate: parseInt(document.getElementById('bitrate').value),
        fps: parseInt(document.getElementById('fps').value),
        source: document.getElementById('source').value,
        cameraId: document.getElementById('camera').value || '',
        keyframeInterval: parseInt(document.getElementById('keyframeInterval').value)
    };

    addLog('Starting ' + config.codec.toUpperCase() + ' ' + width + 'x' + height + ' @ ' + config.bitrate + ' kbps...');
    startTime = Date.now();

    pc = new RTCPeerConnection();

    pc.ontrack = e => {
        addLog('Track received');
        document.getElementById('video').srcObject = new MediaStream([e.track]);
        document.getElementById('video').play();
        document.getElementById('status-bar').innerHTML =
            '<span class="badge badge-codec">' + config.codec.toUpperCase() + '</span>' +
            '<span class="badge badge-res">' + width + 'x' + height + '</span>' +
            '<span class="badge badge-source">' + config.source + '</span>';
    };

    pc.oniceconnectionstatechange = () => addLog('ICE: ' + pc.iceConnectionState);
    pc.onconnectionstatechange = () => {
        addLog('Connection: ' + pc.connectionState);
        if (pc.connectionState === 'failed' || pc.connectionState === 'closed') stop();
    };

    pc.addTransceiver('video', { direction: 'recvonly' });

    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);

    await new Promise(resolve => {
        const timeout = setTimeout(resolve, 2000);
        pc.onicecandidate = e => { if (!e.candidate) { clearTimeout(timeout); resolve(); } };
    });

    const resp = await fetch('/offer', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ sdp: pc.localDescription.sdp, type: 'offer', config: config })
    });

    if (!resp.ok) {
        addLog('Error: ' + await resp.text());
        return;
    }

    const answer = await resp.json();
    await pc.setRemoteDescription(answer);
    addLog('Connected!');

    document.getElementById('startBtn').style.display = 'none';
    document.getElementById('stopBtn').style.display = '';

    statsInterval = setInterval(() => updateStats(pc), 1000);
}

async function updateStats(pc) {
    const stats = await pc.getStats();
    let inbound = null, candidatePair = null, codecReport = null;

    stats.forEach(report => {
        if (report.type === 'inbound-rtp' && report.kind === 'video') inbound = report;
        if (report.type === 'candidate-pair' && report.state === 'succeeded') candidatePair = report;
        if (report.type === 'codec' && report.mimeType?.includes('video')) codecReport = report;
    });

    if (!inbound) return;

    // Get codec info from the inbound report's codecId
    if (inbound.codecId) {
        const c = stats.get(inbound.codecId);
        if (c) codecReport = c;
    }

    const now = inbound.timestamp;
    const elapsed = (Date.now() - startTime) / 1000;

    // Calculate instantaneous bitrate (with sanity checks)
    let bitrate = 0;
    if (prevStats.bytesReceived !== undefined && prevStats.timestamp) {
        const bytesDelta = inbound.bytesReceived - prevStats.bytesReceived;
        const timeDelta = (now - prevStats.timestamp) / 1000;
        if (timeDelta > 0 && bytesDelta >= 0) {
            bitrate = (bytesDelta * 8) / timeDelta / 1000;
        }
    }

    // Calculate packet loss rate
    let lossRate = 0;
    if (inbound.packetsReceived > 0) {
        lossRate = (inbound.packetsLost / (inbound.packetsReceived + inbound.packetsLost)) * 100;
    }

    // Jitter in ms
    const jitterMs = (inbound.jitter || 0) * 1000;

    // RTT from candidate pair
    const rtt = candidatePair?.currentRoundTripTime ? (candidatePair.currentRoundTripTime * 1000).toFixed(0) : '--';

    // Frame metrics (ensure numeric values)
    const fps = Number(inbound.framesPerSecond) || 0;
    const framesDropped = Number(inbound.framesDropped) || 0;
    const keyFrames = Number(inbound.keyFramesDecoded) || 0;
    const pliCount = Number(inbound.pliCount) || 0;
    const nackCount = Number(inbound.nackCount) || 0;
    const firCount = Number(inbound.firCount) || 0;
    const framesDecoded = Number(inbound.framesDecoded) || 0;

    // Decode time
    const totalDecodeTime = Number(inbound.totalDecodeTime) || 0;
    const avgDecodeTime = totalDecodeTime && framesDecoded
        ? ((totalDecodeTime / framesDecoded) * 1000).toFixed(2)
        : '--';

    // Packets lost
    const packetsLost = Number(inbound.packetsLost) || 0;

    // Resolution from frame dimensions
    const width = inbound.frameWidth || '--';
    const height = inbound.frameHeight || '--';

    // Codec info
    const codecName = codecReport?.mimeType?.split('/')[1]?.toUpperCase() || '--';
    const clockRate = codecReport?.clockRate ? (codecReport.clockRate / 1000) + ' kHz' : '';

    // Decoder implementation
    const decoder = inbound.decoderImplementation || '--';

    const lossClass = lossRate > 5 ? 'error' : lossRate > 1 ? 'warning' : '';
    const jitterClass = jitterMs > 50 ? 'error' : jitterMs > 20 ? 'warning' : '';
    const fpsClass = fps < 20 ? 'error' : fps < 28 ? 'warning' : '';

    document.getElementById('stats').innerHTML = '<div class="stats-grid">' +
        '<div class="stat-card"><div class="stat-label">Codec</div><div class="stat-value">' + codecName + ' <span class="stat-unit">' + clockRate + '</span></div></div>' +
        '<div class="stat-card"><div class="stat-label">Resolution</div><div class="stat-value">' + width + 'x' + height + '</div></div>' +
        '<div class="stat-card ' + fpsClass + '"><div class="stat-label">Frame Rate</div><div class="stat-value">' + fps.toFixed(1) + ' <span class="stat-unit">fps</span></div></div>' +
        '<div class="stat-card"><div class="stat-label">Bitrate</div><div class="stat-value">' + bitrate.toFixed(0) + ' <span class="stat-unit">kbps</span></div></div>' +
        '<div class="stat-card"><div class="stat-label">RTT</div><div class="stat-value">' + rtt + ' <span class="stat-unit">ms</span></div></div>' +
        '<div class="stat-card ' + jitterClass + '"><div class="stat-label">Jitter</div><div class="stat-value">' + jitterMs.toFixed(1) + ' <span class="stat-unit">ms</span></div></div>' +
        '<div class="stat-card ' + lossClass + '"><div class="stat-label">Packet Loss</div><div class="stat-value">' + lossRate.toFixed(2) + '<span class="stat-unit">%</span> (' + packetsLost + ')</div></div>' +
        '<div class="stat-card"><div class="stat-label">Frames Decoded</div><div class="stat-value">' + framesDecoded + '</div></div>' +
        '<div class="stat-card"><div class="stat-label">Keyframes</div><div class="stat-value">' + keyFrames + '</div></div>' +
        '<div class="stat-card"><div class="stat-label">Frames Dropped</div><div class="stat-value">' + framesDropped + '</div></div>' +
        '<div class="stat-card"><div class="stat-label">PLI / NACK / FIR</div><div class="stat-value">' + pliCount + ' / ' + nackCount + ' / ' + firCount + '</div></div>' +
        '<div class="stat-card"><div class="stat-label">Decode Time</div><div class="stat-value">' + avgDecodeTime + ' <span class="stat-unit">ms/frame</span></div></div>' +
        '<div class="stat-card"><div class="stat-label">Decoder</div><div class="stat-value">' + decoder + '</div></div>' +
        '<div class="stat-card"><div class="stat-label">Total Received</div><div class="stat-value">' + formatBytes(inbound.bytesReceived) + '</div></div>' +
        '<div class="stat-card"><div class="stat-label">Stream Duration</div><div class="stat-value">' + formatDuration(elapsed) + '</div></div>' +
        '</div>';

    prevStats = { bytesReceived: inbound.bytesReceived, timestamp: now };
}
</script>
</body>
</html>`)
}
