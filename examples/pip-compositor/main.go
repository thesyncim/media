// Picture-in-Picture compositor demo - composites two video sources and streams via WebRTC
// Uses the native SIMD-optimized VideoCompositor for high-performance blending
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/thesyncim/media"
	"github.com/pion/webrtc/v4"
)

// Layout defines PiP positioning
type Layout struct {
	Name     string `json:"name"`
	MainX    int    `json:"mainX"`
	MainY    int    `json:"mainY"`
	MainW    int    `json:"mainW"`
	MainH    int    `json:"mainH"`
	OverlayX int    `json:"overlayX"`
	OverlayY int    `json:"overlayY"`
	OverlayW int    `json:"overlayW"`
	OverlayH int    `json:"overlayH"`
}

var layouts = map[string]Layout{
	"pip-br": {
		Name: "PiP Bottom-Right", MainX: 0, MainY: 0, MainW: 640, MainH: 480,
		OverlayX: 460, OverlayY: 340, OverlayW: 160, OverlayH: 120,
	},
	"pip-tl": {
		Name: "PiP Top-Left", MainX: 0, MainY: 0, MainW: 640, MainH: 480,
		OverlayX: 20, OverlayY: 20, OverlayW: 160, OverlayH: 120,
	},
	"pip-tr": {
		Name: "PiP Top-Right", MainX: 0, MainY: 0, MainW: 640, MainH: 480,
		OverlayX: 460, OverlayY: 20, OverlayW: 160, OverlayH: 120,
	},
	"pip-bl": {
		Name: "PiP Bottom-Left", MainX: 0, MainY: 0, MainW: 640, MainH: 480,
		OverlayX: 20, OverlayY: 340, OverlayW: 160, OverlayH: 120,
	},
	"side-by-side": {
		Name: "Side by Side", MainX: 0, MainY: 60, MainW: 320, MainH: 360,
		OverlayX: 320, OverlayY: 60, OverlayW: 320, OverlayH: 360,
	},
	"stacked": {
		Name: "Stacked", MainX: 160, MainY: 0, MainW: 320, MainH: 240,
		OverlayX: 160, OverlayY: 240, OverlayW: 320, OverlayH: 240,
	},
}

var (
	globalLayout    Layout
	globalLayoutMu  sync.RWMutex
)

func main() {
	globalLayout = layouts["pip-br"]

	// Check if native compositor is available
	if media.IsCompositorAvailable() {
		log.Println("Native SIMD compositor available")
	} else {
		log.Println("WARNING: Native compositor not available, using fallback")
	}

	http.HandleFunc("/", serveHTML)
	http.HandleFunc("/offer", handleOffer)
	http.HandleFunc("/layout", handleLayout)

	log.Println("PiP Compositor Demo - Open http://localhost:8080 in your browser")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleLayout(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		globalLayoutMu.RLock()
		json.NewEncoder(w).Encode(layouts)
		globalLayoutMu.RUnlock()
		return
	}

	if r.Method == http.MethodPost {
		var req struct {
			Layout string `json:"layout"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		layout, ok := layouts[req.Layout]
		if !ok {
			http.Error(w, "Unknown layout", http.StatusBadRequest)
			return
		}

		globalLayoutMu.Lock()
		globalLayout = layout
		globalLayoutMu.Unlock()
		log.Printf("Layout changed to: %s", layout.Name)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func handleOffer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var offer webrtc.SessionDescription
	if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	answer, err := startStream(offer)
	if err != nil {
		log.Printf("startStream error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(answer)
}

func startStream(offer webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, fmt.Errorf("NewPeerConnection: %w", err)
	}

	track, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8},
		"video", "compositor",
	)
	if err != nil {
		return nil, fmt.Errorf("NewTrackLocalStaticRTP: %w", err)
	}

	sender, err := pc.AddTrack(track)
	if err != nil {
		return nil, fmt.Errorf("AddTrack: %w", err)
	}

	pliChan := make(chan struct{}, 1)
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buf); err != nil {
				return
			}
			select {
			case pliChan <- struct{}{}:
			default:
			}
		}
	}()

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("Connection: %s", state)
		if state == webrtc.PeerConnectionStateConnected {
			go streamComposited(pc, track, pliChan)
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
	return pc.LocalDescription(), nil
}

func streamComposited(pc *webrtc.PeerConnection, track *webrtc.TrackLocalStaticRTP, pliChan <-chan struct{}) {
	log.Println("Starting composited stream...")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create two test pattern sources
	mainSource := media.NewTestPatternSource(media.TestPatternConfig{
		Width:   640,
		Height:  480,
		FPS:     30,
		Pattern: media.PatternMovingBox,
	})
	defer mainSource.Close()

	overlaySource := media.NewTestPatternSource(media.TestPatternConfig{
		Width:   320,
		Height:  240,
		FPS:     30,
		Pattern: media.PatternColorBars,
	})
	defer overlaySource.Close()

	// Create native VideoCompositor
	compositor, err := media.NewVideoCompositor(media.CompositorConfig{
		Width:      640,
		Height:     480,
		FPS:        30,
		Background: [3]byte{16, 128, 128}, // Black in YUV
	})
	if err != nil {
		log.Printf("Compositor error: %v", err)
		return
	}
	defer compositor.Close()

	// Add layers - will be updated per-frame based on layout
	mainLayerID := compositor.AddLayer(mainSource, 0, 0)
	overlayLayerID := compositor.AddLayer(overlaySource, 0, 0)

	encoder, err := media.NewVP8Encoder(media.VideoEncoderConfig{
		Width:      640,
		Height:     480,
		BitrateBps: 1_500_000,
		FPS:        30,
	})
	if err != nil {
		log.Printf("Encoder error: %v", err)
		return
	}
	defer encoder.Close()
	encodeBuf := make([]byte, encoder.MaxEncodedSize())

	packetizer, err := media.CreateVideoPacketizer(media.VideoCodecVP8, 0x12345678, 96, 1200)
	if err != nil {
		log.Printf("Packetizer error: %v", err)
		return
	}

	mainSource.Start(ctx)
	overlaySource.Start(ctx)
	compositor.Start(ctx)
	defer mainSource.Stop()
	defer overlaySource.Stop()
	defer compositor.Stop()

	ticker := time.NewTicker(time.Second / 30)
	defer ticker.Stop()

	frameCount := 0
	for {
		select {
		case <-pliChan:
			encoder.RequestKeyframe()
			log.Println("Keyframe requested")

		case <-ticker.C:
			if pc.ConnectionState() != webrtc.PeerConnectionStateConnected {
				log.Println("Connection closed")
				return
			}

			// Update layer positions based on current layout
			globalLayoutMu.RLock()
			layout := globalLayout
			globalLayoutMu.RUnlock()

			compositor.SetLayerPosition(mainLayerID, layout.MainX, layout.MainY)
			compositor.SetLayerSize(mainLayerID, layout.MainW, layout.MainH)
			compositor.SetLayerPosition(overlayLayerID, layout.OverlayX, layout.OverlayY)
			compositor.SetLayerSize(overlayLayerID, layout.OverlayW, layout.OverlayH)
			compositor.SetLayerZOrder(overlayLayerID, 1) // Overlay on top

			// Read composited frame
			composited, err := compositor.ReadFrame(ctx)
			if err != nil || composited == nil {
				continue
			}

			result, err := encoder.Encode(composited, encodeBuf)
			if err != nil || result.N == 0 {
				continue
			}
			encoded := &media.EncodedFrame{
				Data:      encodeBuf[:result.N],
				FrameType: result.FrameType,
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
			if frameCount%90 == 0 {
				log.Printf("Sent %d composited frames", frameCount)
			}
		}
	}
}

func serveHTML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head>
    <title>PiP Compositor Demo</title>
    <style>
        * { box-sizing: border-box; }
        body { font-family: system-ui; max-width: 900px; margin: 40px auto; padding: 20px; background: #1a1a2e; color: #eee; }
        h1 { color: #00d4ff; margin-bottom: 5px; }
        .subtitle { color: #888; margin-bottom: 20px; }
        video { width: 100%; background: #000; border-radius: 8px; border: 2px solid #333; }
        .controls { display: flex; gap: 10px; flex-wrap: wrap; margin: 15px 0; }
        button { padding: 10px 20px; font-size: 14px; cursor: pointer; background: #333; color: #fff; border: 1px solid #555; border-radius: 4px; transition: all 0.2s; }
        button:hover { background: #444; border-color: #00d4ff; }
        button.active { background: #00d4ff; color: #000; border-color: #00d4ff; }
        #stats { font-family: monospace; font-size: 12px; background: #252540; padding: 10px; margin: 10px 0; border-radius: 4px; }
        #log { font-family: monospace; font-size: 11px; background: #1e1e30; padding: 10px; max-height: 120px; overflow: auto; border-radius: 4px; }
        .layout-preview { display: flex; gap: 8px; margin: 15px 0; flex-wrap: wrap; }
        .layout-btn { position: relative; width: 80px; height: 60px; background: #252540; border: 2px solid #444; border-radius: 4px; cursor: pointer; overflow: hidden; }
        .layout-btn.active { border-color: #00d4ff; }
        .layout-btn .main { position: absolute; background: #00d4ff; opacity: 0.7; }
        .layout-btn .overlay { position: absolute; background: #ff6b6b; opacity: 0.9; }
        .badge { display: inline-block; padding: 2px 8px; background: #00d4ff; color: #000; border-radius: 4px; font-size: 11px; margin-left: 10px; }
    </style>
</head>
<body>
    <h1>Picture-in-Picture Compositor <span class="badge">SIMD Optimized</span></h1>
    <p class="subtitle">Two video sources composited in real-time using native SIMD blending, streamed via WebRTC</p>

    <video id="video" autoplay playsinline muted></video>

    <div class="controls">
        <button onclick="start()">Start Stream</button>
    </div>

    <h3 style="margin-bottom: 10px;">Layout</h3>
    <div class="layout-preview" id="layoutPreview"></div>

    <div id="stats">Click Start to begin</div>
    <div id="log"></div>

<script>
let pc;
let currentLayout = 'pip-br';

function addLog(msg) {
    const log = document.getElementById('log');
    log.innerHTML += new Date().toLocaleTimeString() + ' ' + msg + '<br>';
    log.scrollTop = log.scrollHeight;
}

function createLayoutPreviews() {
    const layouts = {
        'pip-br': { main: [0, 0, 100, 100], overlay: [70, 68, 28, 28] },
        'pip-tl': { main: [0, 0, 100, 100], overlay: [3, 4, 25, 25] },
        'pip-tr': { main: [0, 0, 100, 100], overlay: [72, 4, 25, 25] },
        'pip-bl': { main: [0, 0, 100, 100], overlay: [3, 68, 25, 28] },
        'side-by-side': { main: [0, 12, 50, 75], overlay: [50, 12, 50, 75] },
        'stacked': { main: [25, 0, 50, 50], overlay: [25, 50, 50, 50] }
    };

    const container = document.getElementById('layoutPreview');
    Object.entries(layouts).forEach(([key, val]) => {
        const btn = document.createElement('div');
        btn.className = 'layout-btn' + (key === currentLayout ? ' active' : '');
        btn.onclick = () => setLayout(key);
        btn.title = key;

        const main = document.createElement('div');
        main.className = 'main';
        main.style.cssText = 'left:'+val.main[0]+'%;top:'+val.main[1]+'%;width:'+val.main[2]+'%;height:'+val.main[3]+'%';

        const overlay = document.createElement('div');
        overlay.className = 'overlay';
        overlay.style.cssText = 'left:'+val.overlay[0]+'%;top:'+val.overlay[1]+'%;width:'+val.overlay[2]+'%;height:'+val.overlay[3]+'%';

        btn.appendChild(main);
        btn.appendChild(overlay);
        container.appendChild(btn);
    });
}

async function setLayout(layout) {
    const resp = await fetch('/layout', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ layout: layout })
    });

    if (resp.ok) {
        currentLayout = layout;
        document.querySelectorAll('.layout-btn').forEach((btn, i) => {
            const layouts = ['pip-br', 'pip-tl', 'pip-tr', 'pip-bl', 'side-by-side', 'stacked'];
            btn.className = 'layout-btn' + (layouts[i] === layout ? ' active' : '');
        });
        addLog('Layout changed to: ' + layout);
    }
}

async function start() {
    addLog('Connecting...');

    pc = new RTCPeerConnection();

    pc.ontrack = e => {
        addLog('Track received');
        document.getElementById('video').srcObject = new MediaStream([e.track]);
    };

    pc.onconnectionstatechange = () => addLog('Connection: ' + pc.connectionState);

    pc.addTransceiver('video', { direction: 'recvonly' });

    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);

    await new Promise(r => {
        const timeout = setTimeout(r, 1000);
        pc.onicecandidate = e => { if (!e.candidate) { clearTimeout(timeout); r(); } };
    });

    const resp = await fetch('/offer', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(pc.localDescription)
    });

    if (!resp.ok) {
        addLog('Error: ' + await resp.text());
        return;
    }

    await pc.setRemoteDescription(await resp.json());
    addLog('Connected!');

    setInterval(async () => {
        const stats = await pc.getStats();
        let html = '';
        stats.forEach(report => {
            if (report.type === 'inbound-rtp' && report.kind === 'video') {
                const kbps = Math.round((report.bytesReceived * 8) / (report.timestamp / 1000) / 1000);
                html = '<b>Video:</b> ' + report.framesDecoded + ' frames | ' +
                    (report.framesPerSecond||0) + ' fps | ' + kbps + ' kbps | ' +
                    report.packetsLost + ' lost';
            }
        });
        if (html) document.getElementById('stats').innerHTML = html;
    }, 1000);
}

createLayoutPreviews();
</script>
</body>
</html>`)
}
