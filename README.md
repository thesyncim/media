# Stream Media SDK

Media processing library for Go with WebRTC-style APIs. Works with [pion/webrtc](https://github.com/pion/webrtc).

## Features

- **Encoding & Decoding** - VP8/VP9 video, Opus audio
- **RTP Packetization** - packetizers and depacketizers using pion/rtp
- **Device Capture** - camera/microphone on macOS (AVFoundation) and Linux (V4L2/ALSA)
- **pion Compatible** - `LocalTrack` implements `webrtc.TrackLocal`
- **No CGO** - uses [purego](https://github.com/ebitengine/purego) for native library calls
- **Test Sources** - video patterns (color bars, gradients) and audio patterns (sine, noise)

## Install

```bash
go get github.com/GetStream/media
```

You'll also need to build the native libraries (see below).

## Quick Start

### Encode video to RTP packets

```go
// Create VP8 encoder
encoder, _ := media.NewVP8Encoder(media.VideoEncoderConfig{
    Width:      1280,
    Height:     720,
    BitrateBps: 1_500_000,
    FPS:        30,
})
defer encoder.Close()

// Create RTP packetizer
packetizer, _ := media.CreateVideoPacketizer(media.VideoCodecVP8, ssrc, 96, 1200)

// Encode and packetize
frame := getVideoFrame() // I420 format
encoded, _ := encoder.Encode(frame)
packets, _ := packetizer.Packetize(encoded)

for _, pkt := range packets {
    // pkt is *rtp.Packet from pion/rtp
    conn.WriteRTP(pkt)
}
```

### Decode RTP packets to video frames

```go
// Create depacketizer and decoder
depacketizer, _ := media.CreateVideoDepacketizer(media.VideoCodecVP8)
decoder, _ := media.NewVP8Decoder(media.VideoDecoderConfig{})
defer decoder.Close()

// Process incoming RTP
for {
    pkt := readRTPPacket()
    encoded, _ := depacketizer.Depacketize(pkt)
    if encoded != nil {
        frame, _ := decoder.Decode(encoded)
        // frame is *media.VideoFrame (I420)
    }
}
```

### Use with pion/webrtc

```go
// LocalTrack implements webrtc.TrackLocal
track := media.NewLocalTrack(webrtc.RTPCodecCapability{
    MimeType: webrtc.MimeTypeVP8,
}, "video", "stream-1")

// Add directly to peer connection
pc.AddTrack(track)

// Write encoded frames
encoder, _ := media.NewVP8Encoder(config)
packetizer, _ := media.CreateVideoPacketizer(media.VideoCodecVP8, ssrc, 96, 1200)

for frame := range frames {
    encoded, _ := encoder.Encode(frame)
    packets, _ := packetizer.Packetize(encoded)
    for _, pkt := range packets {
        track.WriteRTP(pkt)
    }
}
```

### Capture from camera

```go
provider := media.NewAVFoundationProvider() // or NewLinuxDeviceProvider()

// List devices
devices, _ := provider.ListVideoDevices(ctx)
for _, d := range devices {
    fmt.Printf("%s: %s\n", d.DeviceID, d.Label)
}

// Open camera
track, _ := provider.OpenVideoDevice(ctx, "", &media.VideoConstraints{
    Width:     1280,
    Height:    720,
    FrameRate: 30,
})
defer track.Close()

// Read frames
for {
    frame, _ := track.ReadFrame(ctx)
    // process frame
}
```

### Test patterns (no hardware needed)

```go
// Video test pattern
source := media.NewTestPatternSource(media.TestPatternConfig{
    Width:   1280,
    Height:  720,
    FPS:     30,
    Pattern: media.PatternColorBars,
})
source.Start()
defer source.Stop()

frame, _ := source.ReadFrame(ctx)

// Audio test pattern
audioSource := media.NewAudioTestPatternSource(media.AudioTestPatternConfig{
    SampleRate: 48000,
    Channels:   2,
    Pattern:    media.AudioPatternSineWave,
    Frequency:  440,
})
```

## Building Native Libraries

The package requires native libraries for codecs. Build them from source:

```bash
cd clib
make all    # Downloads libvpx 1.15.0, libopus 1.5.2 and builds wrappers
```

Libraries are built to `build/`:
- `libstream_vpx.{dylib,so}` - VP8/VP9
- `libstream_opus.{dylib,so}` - Opus
- `libstream_avfoundation.dylib` - macOS capture
- `libstream_v4l2.so`, `libstream_alsa.so` - Linux capture

### macOS with Homebrew (faster)

```bash
brew install libvpx opus
cd clib
make USE_SYSTEM_LIBS=1 all
```

### Run tests

```bash
# After building libs
go test ./...

# Or use the test script
./test.sh all
```

## Codec Support

| Codec | Encode | Decode | RTP |
|-------|--------|--------|-----|
| VP8   | ✓      | ✓      | ✓   |
| VP9   | ✓      | ✓      | ✓   |
| Opus  | ✓      | ✓      | ✓   |

## Platform Support

| Platform | Video Capture | Audio Capture |
|----------|--------------|---------------|
| macOS arm64/amd64 | AVFoundation | AVFoundation |
| Linux amd64/arm64 | V4L2 | ALSA |

## Architecture

```
Encode path:
  VideoSource → VideoEncoder → RTPPacketizer → network

Decode path:
  network → RTPDepacketizer → VideoDecoder → VideoFrame
```

## Dependencies

- [pion/webrtc](https://github.com/pion/webrtc) - WebRTC types
- [pion/rtp](https://github.com/pion/rtp) - RTP packet handling
- [ebitengine/purego](https://github.com/ebitengine/purego) - Pure Go FFI

Native:
- libvpx ≥1.11.0
- libopus ≥1.3.0

## License

MIT
