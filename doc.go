// Package media provides WebRTC-style media primitives in Go, backed by
// native codec/device wrappers (libstream_*).
//
// Key pieces include:
//   - MediaStream/MediaStreamTrack and MediaDevices (getUserMedia-style APIs)
//   - Video/Audio encoders and decoders
//   - RTP packetizers/depacketizers and RTPReader/RTPWriter helpers
//   - Pipelines for RTP encode/decode
//   - Test pattern sources, compositor, and scaler utilities
//
// # Architecture
//
//   Encode (video): VideoSource/VideoTrack -> VideoEncoder -> RTPPacketizer -> RTPWriter
//   Encode (audio): AudioSource/AudioTrack -> AudioEncoder -> RTPPacketizer -> RTPWriter
//   Decode: RTPReader -> RTPDepacketizer -> Decoder -> Frame/Samples callback
//
// # Native Libraries
//
// Bindings load libstream_* libraries built from clib/ into build/.
// Set STREAM_SDK_LIB_PATH to the directory containing these libraries.
// By default the package uses purego (CGO_ENABLED=0). With CGO enabled it
// links against the same wrappers for lower overhead.
//
// Device capture support is platform-specific; audio/display capture is not
// implemented in the current providers.
//
// # Build Tags
//
// Optional tags disable features:
//   - novpx, noopus, noh264, noav1: disable specific codecs
//   - nodevices: disable device capture support
//
// # Supported Codecs
//
// Video: VP8/VP9 (libvpx), H.264 (x264 encoder + OpenH264 decoder), AV1 (libaom)
// Audio: Opus (libopus)
// Availability depends on which native libraries are present at runtime.
package media
