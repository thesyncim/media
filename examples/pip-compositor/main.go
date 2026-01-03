// Picture-in-Picture compositor demo - composites two video sources and streams via WebRTC
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/GetStream/media"
	"github.com/pion/webrtc/v4"
)

// Layout defines PiP positioning
type Layout struct {
	Name     string  `json:"name"`
	MainX    int     `json:"mainX"`
	MainY    int     `json:"mainY"`
	MainW    int     `json:"mainW"`
	MainH    int     `json:"mainH"`
	OverlayX int     `json:"overlayX"`
	OverlayY int     `json:"overlayY"`
	OverlayW int     `json:"overlayW"`
	OverlayH int     `json:"overlayH"`
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

type Compositor struct {
	mu          sync.RWMutex
	layout      Layout
	outputFrame *media.VideoFrameBuffer
}

func NewCompositor(layout Layout) *Compositor {
	return &Compositor{
		layout:      layout,
		outputFrame: media.NewVideoFrameBuffer(640, 480, media.PixelFormatI420),
	}
}

func (c *Compositor) SetLayout(layout Layout) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.layout = layout
}

func (c *Compositor) Composite(main, overlay *media.VideoFrame) *media.VideoFrame {
	c.mu.RLock()
	layout := c.layout
	c.mu.RUnlock()

	// Clear output to black (Y=16, U=V=128 for black in YUV)
	for i := range c.outputFrame.Y {
		c.outputFrame.Y[i] = 16
	}
	for i := range c.outputFrame.U {
		c.outputFrame.U[i] = 128
	}
	for i := range c.outputFrame.V {
		c.outputFrame.V[i] = 128
	}

	// Scale and blit main frame
	if main != nil && layout.MainW > 0 && layout.MainH > 0 {
		c.blitFrame(main, layout.MainX, layout.MainY, layout.MainW, layout.MainH)
	}

	// Scale and blit overlay frame
	if overlay != nil && layout.OverlayW > 0 && layout.OverlayH > 0 {
		c.blitFrame(overlay, layout.OverlayX, layout.OverlayY, layout.OverlayW, layout.OverlayH)
	}

	frame := c.outputFrame.ToVideoFrame()
	return &frame
}

func (c *Compositor) blitFrame(src *media.VideoFrame, x, y, w, h int) {
	// Simple nearest-neighbor scaling and blit
	srcW := src.Width
	srcH := src.Height

	for dy := 0; dy < h && (y+dy) < 480; dy++ {
		srcY := dy * srcH / h
		for dx := 0; dx < w && (x+dx) < 640; dx++ {
			srcX := dx * srcW / w

			dstIdx := (y+dy)*c.outputFrame.StrideY + (x + dx)
			srcIdx := srcY*src.Stride[0] + srcX

			if dstIdx < len(c.outputFrame.Y) && srcIdx < len(src.Data[0]) {
				c.outputFrame.Y[dstIdx] = src.Data[0][srcIdx]
			}

			// UV planes (half resolution)
			if dy%2 == 0 && dx%2 == 0 {
				dstUVIdx := ((y+dy)/2)*c.outputFrame.StrideU + ((x + dx) / 2)
				srcUVIdx := (srcY/2)*src.Stride[1] + (srcX / 2)

				if dstUVIdx < len(c.outputFrame.U) && srcUVIdx < len(src.Data[1]) {
					c.outputFrame.U[dstUVIdx] = src.Data[1][srcUVIdx]
				}
				if dstUVIdx < len(c.outputFrame.V) && srcUVIdx < len(src.Data[2]) {
					c.outputFrame.V[dstUVIdx] = src.Data[2][srcUVIdx]
				}
			}
		}
	}
}

func (c *Compositor) Close() {
	// Nothing to clean up for simple compositor
}

var (
	globalCompositor *Compositor
	layoutMu         sync.RWMutex
)

func main() {
	globalCompositor = NewCompositor(layouts["pip-br"])
	defer globalCompositor.Close()

	http.HandleFunc("/", serveHTML)
	http.HandleFunc("/offer", handleOffer)
	http.HandleFunc("/layout", handleLayout)

	log.Println("PiP Compositor Demo - Open http://localhost:8080 in your browser")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleLayout(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		layoutMu.RLock()
		json.NewEncoder(w).Encode(layouts)
		layoutMu.RUnlock()
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

		globalCompositor.SetLayout(layout)
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

	// Create two test pattern sources
	mainSource := media.NewTestPatternSource(media.TestPatternConfig{
		Width:   640,
		Height:  480,
		FPS:     30,
		Pattern: media.PatternMovingBox,
	})

	overlaySource := media.NewTestPatternSource(media.TestPatternConfig{
		Width:   320,
		Height:  240,
		FPS:     30,
		Pattern: media.PatternColorBars,
	})

	encoder, err := media.NewVP8Encoder(media.VideoEncoderConfig{
		Width:            640,
		Height:           480,
		BitrateBps:       1_500_000,
		FPS:              30,
		KeyframeInterval: 60,
	})
	if err != nil {
		log.Printf("Encoder error: %v", err)
		return
	}
	defer encoder.Close()

	packetizer, err := media.CreateVideoPacketizer(media.VideoCodecVP8, 0x12345678, 96, 1200)
	if err != nil {
		log.Printf("Packetizer error: %v", err)
		return
	}

	ctx := context.Background()
	mainSource.Start(ctx)
	overlaySource.Start(ctx)
	defer mainSource.Stop()
	defer overlaySource.Stop()

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

			mainFrame, _ := mainSource.ReadFrame(ctx)
			overlayFrame, _ := overlaySource.ReadFrame(ctx)

			if mainFrame == nil {
				continue
			}

			composited := globalCompositor.Composite(mainFrame, overlayFrame)
			if composited == nil {
				continue
			}

			encoded, err := encoder.Encode(composited)
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
    </style>
</head>
<body>
    <h1>Picture-in-Picture Compositor</h1>
    <p class="subtitle">Two video sources composited in real-time, streamed via WebRTC</p>

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
