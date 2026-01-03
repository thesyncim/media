// Package media provides a WebRTC-like media abstraction layer for the FFI.
//
// This package provides high-level abstractions similar to the browser WebRTC API:
//
//   - MediaStreamTrack: Individual audio or video track
//   - MediaStream: Collection of tracks
//   - MediaDevices: Device enumeration and media capture (getUserMedia, getDisplayMedia)
//   - VideoEncoder/VideoDecoder: Codec encode/decode
//   - AudioEncoder/AudioDecoder: Audio codec encode/decode
//   - RTPPacketizer/RTPDepacketizer: RTP packetization
//   - Pipeline: Connects sources, encoders, and RTP transport
//
// # Architecture
//
// The media pipeline follows this flow:
//
//	Encode Path (publishing):
//	  MediaSource/Track -> VideoEncoder -> RTPPacketizer -> RTPWriter -> Network
//
//	Decode Path (subscribing):
//	  Network -> RTPReader -> RTPDepacketizer -> VideoDecoder -> VideoFrame -> App
//
// # Usage from Go
//
// The package is designed to be used directly from Go applications:
//
//	// Get camera stream
//	devices := media.GetMediaDevices()
//	stream, _ := devices.GetUserMedia(ctx, media.UserMediaOptions{
//	    Video: &media.VideoConstraints{Width: 1280, Height: 720},
//	    Audio: &media.AudioConstraints{},
//	})
//
//	// Create encoder
//	encoder, _ := media.CreateVideoEncoder(media.VideoEncoderConfig{
//	    Codec:  media.VideoCodecVP8,
//	    Width:  1280,
//	    Height: 720,
//	    FPS:    30,
//	    BitrateBps: 1500000,
//	})
//
//	// Create packetizer
//	packetizer, _ := media.CreateVideoPacketizer(media.VideoCodecVP8, ssrc, 96, 1200)
//
//	// Create pipeline
//	pipeline, _ := media.NewVideoEncodePipeline(media.VideoPipelineConfig{
//	    Track:      stream.GetVideoTracks()[0],
//	    Encoder:    encoder,
//	    Packetizer: packetizer,
//	    Writer:     rtpWriter,
//	})
//	pipeline.Start()
//
// # Plugin/Module System
//
// Codecs and sources are registered at init time. Use build tags to include/exclude:
//
//	// With all codecs
//	go build -tags "with_vpx with_opus with_x264 with_aom"
//
//	// Minimal build (test pattern + FFmpeg fallback)
//	go build
//
// # Supported Codecs
//
// Video:
//   - VP8/VP9 (libvpx)
//   - H.264 (x264 or FFmpeg)
//   - AV1 (libaom or FFmpeg)
//
// Audio:
//   - Opus (libopus)
//
// # Thread Safety
//
// All types in this package are thread-safe unless otherwise noted.
// Callbacks may be invoked from any goroutine.
package media
