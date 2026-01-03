# PiP Compositor Demo

Real-time video compositing with multiple layout options, streamed via WebRTC.

## Features

- Two video sources composited together in real-time
- Multiple layout options: PiP corners, side-by-side, stacked
- Live layout switching without stream restart
- WebRTC streaming to browser

## Run

```bash
# Build native libs first (from repo root)
cd clib && make all && cd ..

# Run the demo
go run ./examples/pip-compositor

# Open http://localhost:8080
```

## Layouts

| Layout | Description |
|--------|-------------|
| pip-br | PiP in bottom-right corner |
| pip-tl | PiP in top-left corner |
| pip-tr | PiP in top-right corner |
| pip-bl | PiP in bottom-left corner |
| side-by-side | Two sources side by side |
| stacked | Two sources stacked vertically |

## Pipeline

```
TestPatternSource (Main) ─┐
                          ├─> Compositor ─> VP8Encoder ─> RTPPacketizer ─> WebRTC ─> Browser
TestPatternSource (PiP) ──┘
```
