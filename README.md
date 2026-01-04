# Media SDK

Media processing library for Go with WebRTC-style APIs. Works with [pion/webrtc](https://github.com/pion/webrtc).

## Features

- **Encoding & Decoding** - VP8/VP9, H.264, AV1 video; Opus audio
- **RTP Packetization** - packetizers/depacketizers for VP8/VP9/H.264/AV1/Opus using pion/rtp
- **Media Devices** - getUserMedia-style APIs; macOS video capture via AVFoundation (audio/display capture WIP); Linux device enumeration via V4L2/ALSA
- **pion Compatible** - `LocalTrack` implements `webrtc.TrackLocal`
- **Purego by default** - no CGO required; optional CGO build for lower overhead
- **Sources & Utilities** - test patterns, camera source, compositor, scaler, codec detection helpers

## Install

```bash
go get github.com/thesyncim/media
```

Requires Go 1.23+ and the native libraries (see below).

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
ssrc := uint32(1234)
packetizer, _ := media.CreateVideoPacketizer(
    media.VideoCodecVP8,
    ssrc,
    media.VideoCodecVP8.DefaultPayloadType(),
    1200,
)

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
packetizer, _ := media.CreateVideoPacketizer(
    media.VideoCodecVP8,
    ssrc,
    media.VideoCodecVP8.DefaultPayloadType(),
    1200,
)

for frame := range frames {
    encoded, _ := encoder.Encode(frame)
    packets, _ := packetizer.Packetize(encoded)
    for _, pkt := range packets {
        track.WriteRTP(pkt)
    }
}
```

### Capture from camera

Note: macOS video capture is implemented. Audio and display capture are not yet implemented,
and Linux capture is currently enumeration-only.

```go
provider := media.NewAVFoundationProvider() // macOS only
// provider := media.NewLinuxDeviceProvider() // enumeration only (capture WIP)

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
source.Start(ctx)
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

## Transcoding Examples (MultiTranscoder)

MultiTranscoder decodes once and fans out many outputs in parallel. This is the
same engine used by `examples/webrtc-transcoder`.

### 1) One incoming stream -> passthrough + simulcast + multi-codec

```go
// Passthrough source + VP8 simulcast + VP9/AV1 variants at 720p
outputs := []media.OutputConfig{
    {ID: "source", Codec: media.VideoCodecVP8, Passthrough: true},
}
outputs = append(outputs, media.SimulcastPreset(media.VideoCodecVP8, 2_500_000)...)
outputs = append(outputs, media.MultiCodecPreset(
    1280, 720, 1_200_000,
    media.VideoCodecVP9, media.VideoCodecAV1,
)...)

mt, _ := media.NewMultiTranscoder(media.MultiTranscoderConfig{
    InputCodec:  media.VideoCodecVP8,
    InputWidth:  1920,
    InputHeight: 1080,
    Outputs:     outputs,
    OnKeyframeNeeded: func() error {
        return sendPLI() // request keyframe from source
    },
})
depacketizer, _ := media.CreateVideoDepacketizer(media.VideoCodecVP8)

packetizers := map[string]media.RTPPacketizer{}
for _, id := range mt.Variants() {
    cfg, _ := mt.VariantConfig(id)
    pkt, _ := media.CreateVideoPacketizer(
        cfg.Codec,
        ssrcFor(id),
        cfg.Codec.DefaultPayloadType(),
        1200,
    )
    packetizers[id] = pkt
}

for {
    pkt := readRTPPacket()
    encoded, _ := depacketizer.Depacketize(pkt)
    if encoded == nil {
        continue
    }

    result, _ := mt.Transcode(encoded)
    for _, v := range result.Variants {
        packets, _ := packetizers[v.VariantID].Packetize(v.Frame)
        fanoutToSubscribers(v.VariantID, packets)
    }
}
```

### 2) Raw frames -> multi-output encodes (no decode)

```go
mt, _ := media.NewMultiTranscoder(media.MultiTranscoderConfig{
    Outputs: []media.OutputConfig{
        {ID: "vp9-svc", Codec: media.VideoCodecVP9, Width: 1280, Height: 720, BitrateBps: 1_200_000, TemporalLayers: 3},
        {ID: "h264-720p", Codec: media.VideoCodecH264, Width: 1280, Height: 720, BitrateBps: 1_500_000},
        {ID: "av1-360p", Codec: media.VideoCodecAV1, Width: 640, Height: 360, BitrateBps: 500_000},
    },
})

source := media.NewTestPatternSource(media.TestPatternConfig{
    Width: 1920, Height: 1080, FPS: 30, Pattern: media.PatternColorBars,
})
source.Start(ctx)
defer source.Stop()

for {
    frame, _ := source.ReadFrame(ctx) // *media.VideoFrame
    result, _ := mt.TranscodeRaw(frame)
    for _, v := range result.Variants {
        useEncoded(v.VariantID, v.Frame)
    }
}
```

### 3) Per-viewer ABR picker

```go
abr := media.NewABRController(mt, []string{"high", "medium", "low"})

result, _ := mt.Transcode(encoded)
picked := abr.SelectOutput(result)
if picked != nil {
    deliver(picked.VariantID, picked.Frame)
}
```

## Building Native Libraries

The package requires native libraries for codecs and device capture.

```bash
make build         # builds libstream_* into ./build
# or
make build-system  # uses system libraries (faster for development)
```

When running purego builds, point the loader at your build directory:

```bash
export STREAM_SDK_LIB_PATH=$PWD/build
```

Libraries built to `build/`:
- `libstream_vpx.{dylib,so}` - VP8/VP9
- `libstream_opus.{dylib,so}` - Opus
- `libstream_h264.{dylib,so}` - H.264 (x264 encoder; OpenH264 decoder loaded at runtime)
- `libstream_av1.{dylib,so}` - AV1
- `libstream_compositor.{dylib,so}` - video compositor
- `libstream_avfoundation.dylib` - macOS capture
- `libstream_v4l2.so`, `libstream_alsa.so` - Linux device libraries (capture WIP in Go)

### macOS with Homebrew (faster)

```bash
brew install libvpx opus x264 aom openh264
make build-system
```

OpenH264 is required at runtime for H.264 decode.

### Build tags

- `novpx`, `noopus`, `noh264`, `noav1` - disable specific codecs
- `nodevices` - disable device capture support
- `CGO_ENABLED=0` uses purego; `CGO_ENABLED=1` enables CGO variants

### Run tests

```bash
make test
make test-cgo
```

Or manually:

```bash
STREAM_SDK_LIB_PATH=$PWD/build CGO_ENABLED=0 go test ./...
```

## Codec Support

| Codec | Encode | Decode | RTP |
|-------|--------|--------|-----|
| VP8   | ✓      | ✓      | ✓   |
| VP9   | ✓      | ✓      | ✓   |
| H.264 | ✓      | ✓*     | ✓   |
| AV1   | ✓      | ✓      | ✓   |
| Opus  | ✓      | ✓      | ✓   |

*H.264 decode requires OpenH264 at runtime.
H.265 is defined in `VideoCodec` but not implemented yet.

## Platform Support

| Platform | Video Capture | Audio Capture | Display Capture |
|----------|---------------|---------------|-----------------|
| macOS arm64/amd64 | AVFoundation (video only) | Not yet implemented | Not yet implemented |
| Linux amd64/arm64 | Not yet implemented (enumeration only) | Not yet implemented (enumeration only) | Not yet implemented |

## Architecture

```
Encode path (video):
  VideoSource/VideoTrack → VideoEncoder → RTPPacketizer → RTPWriter → network

Encode path (audio):
  AudioSource/AudioTrack → AudioEncoder → RTPPacketizer → RTPWriter → network

Decode path:
  network → RTPReader → RTPDepacketizer → Decoder → Frame/Samples callback
```

## Dependencies

- [pion/webrtc/v4](https://github.com/pion/webrtc) - WebRTC types
- [pion/rtp](https://github.com/pion/rtp) - RTP packet handling
- [ebitengine/purego](https://github.com/ebitengine/purego) - Pure Go FFI

Native:
- libvpx
- libopus
- x264
- libaom
- OpenH264 (H.264 decode)

## License

MIT
