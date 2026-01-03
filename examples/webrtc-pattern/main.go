// WebRTC test pattern server - streams generated video to browser
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/thesyncim/media"
	"github.com/pion/webrtc/v4"
)

func main() {
	http.HandleFunc("/", serveHTML)
	http.HandleFunc("/offer/vp8", handleOffer(media.VideoCodecVP8, webrtc.MimeTypeVP8))
	http.HandleFunc("/offer/vp9", handleOffer(media.VideoCodecVP9, webrtc.MimeTypeVP9))
	http.HandleFunc("/offer/h264", handleOffer(media.VideoCodecH264, webrtc.MimeTypeH264))
	http.HandleFunc("/offer/av1", handleOffer(media.VideoCodecAV1, webrtc.MimeTypeAV1))

	log.Println("Open http://localhost:8080 in your browser")
	log.Println("Supported codecs: VP8, VP9, H.264, AV1")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleOffer(codec media.VideoCodec, mimeType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}

		var offer webrtc.SessionDescription
		if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
			log.Printf("Decode error: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		log.Printf("Received offer, codec: %s", codec)

		answer, err := startStream(offer, codec, mimeType)
		if err != nil {
			log.Printf("startStream error: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(answer)
	}
}

func startStream(offer webrtc.SessionDescription, codec media.VideoCodec, mimeType string) (*webrtc.SessionDescription, error) {
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
			go streamPattern(pc, track, pliChan, codec)
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

func streamPattern(pc *webrtc.PeerConnection, track *webrtc.TrackLocalStaticRTP, pliChan <-chan struct{}, codec media.VideoCodec) {
	log.Printf("Starting stream with %s...", codec)

	source := media.NewTestPatternSource(media.TestPatternConfig{
		Width:   640,
		Height:  480,
		FPS:     30,
		Pattern: media.PatternMovingBox,
	})

	// Note: Automatic keyframes are disabled in the encoders.
	// Keyframes are only generated on explicit request (via PLI/FIR).
	config := media.VideoEncoderConfig{
		Width:      640,
		Height:     480,
		BitrateBps: 1_000_000,
		FPS:        30,
	}

	var encoder media.VideoEncoder
	var err error
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

	ctx := context.Background()
	source.Start(ctx)
	defer source.Stop()

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
			if frameCount%30 == 0 {
				log.Printf("Sent %d frames", frameCount)
			}
		}
	}
}

func serveHTML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head>
    <title>WebRTC Test Pattern</title>
    <style>
        body { font-family: system-ui; max-width: 800px; margin: 40px auto; padding: 20px; }
        video { width: 100%; background: #000; border-radius: 8px; }
        button { padding: 12px 24px; font-size: 16px; cursor: pointer; }
        #stats { font-family: monospace; font-size: 13px; background: #e8f5e9; padding: 10px; margin: 10px 0; border-radius: 4px; }
        #log { font-family: monospace; font-size: 12px; background: #f5f5f5; padding: 10px; margin-top: 10px; max-height: 150px; overflow: auto; }
    </style>
</head>
<body>
    <h1>WebRTC Test Pattern</h1>
    <video id="video" autoplay playsinline muted></video>
    <br><br>
    <select id="codec">
        <option value="vp8">VP8</option>
        <option value="vp9">VP9</option>
        <option value="h264">H.264</option>
        <option value="av1">AV1</option>
    </select>
    <button onclick="start()">Start</button>
    <div id="stats"></div>
    <div id="log"></div>

<script>
function addLog(msg) {
    const log = document.getElementById('log');
    log.innerHTML += msg + '<br>';
    log.scrollTop = log.scrollHeight;
    console.log(msg);
}

async function start() {
    const codec = document.getElementById('codec').value;
    addLog('Starting with ' + codec.toUpperCase() + '...');

    const pc = new RTCPeerConnection();

    pc.ontrack = e => {
        addLog('Track received: ' + e.track.kind);
        const video = document.getElementById('video');
        video.srcObject = new MediaStream([e.track]);
        video.play();
    };

    pc.onicecandidate = e => {
        if (e.candidate) addLog('ICE candidate: ' + e.candidate.type);
    };

    pc.oniceconnectionstatechange = () => addLog('ICE: ' + pc.iceConnectionState);
    pc.onconnectionstatechange = () => addLog('Connection: ' + pc.connectionState);

    // Add recv-only video transceiver
    pc.addTransceiver('video', { direction: 'recvonly' });

    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);
    addLog('Offer created, waiting for candidates...');

    // Wait for at least one candidate or timeout
    await new Promise(resolve => {
        const timeout = setTimeout(resolve, 2000);
        pc.onicecandidate = e => {
            if (!e.candidate) {
                clearTimeout(timeout);
                resolve();
            }
        };
    });
    addLog('Sending offer with ' + pc.localDescription.sdp.split('a=candidate').length + ' candidates');

    const resp = await fetch('/offer/' + codec, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(pc.localDescription)
    });

    if (!resp.ok) {
        addLog('Error: ' + await resp.text());
        return;
    }

    const answer = await resp.json();
    addLog('Answer received');

    await pc.setRemoteDescription(answer);
    addLog('Remote description set');

    // Start stats polling
    setInterval(async () => {
        const stats = await pc.getStats();
        let html = '';
        stats.forEach(report => {
            if (report.type === 'inbound-rtp' && report.kind === 'video') {
                const kbps = Math.round((report.bytesReceived * 8) / (report.timestamp / 1000) / 1000);
                html = '<b>Video Stats:</b> ' +
                    report.framesDecoded + ' frames, ' +
                    report.framesPerSecond + ' fps, ' +
                    kbps + ' kbps, ' +
                    report.packetsLost + ' lost';
            }
        });
        if (html) document.getElementById('stats').innerHTML = html;
    }, 1000);
}
</script>
</body>
</html>`)
}
