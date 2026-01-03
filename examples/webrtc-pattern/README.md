# WebRTC Test Pattern Server

Streams a generated video pattern to browser via WebRTC.

## Run

```bash
# Build native libs first (from repo root)
cd clib && make all && cd ..

# Run the server
go run ./examples/webrtc-pattern

# Open http://localhost:8080
```

## How it works

1. Browser creates WebRTC offer and sends to server
2. Server creates:
   - `TestPatternSource` - generates moving box video frames
   - `VP8Encoder` - encodes frames to VP8
   - `RTPPacketizer` - segments encoded data into RTP packets
3. Server answers with WebRTC SDP
4. RTP packets flow to browser via WebRTC
5. Browser decodes VP8 and displays video

## Pipeline

```
TestPatternSource → VP8Encoder → RTPPacketizer → pion/webrtc → Browser
```
