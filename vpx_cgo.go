//go:build (darwin || linux) && !novpx && cgo

// Package media provides VP8/VP9 codec support via libmedia_vpx using CGO.
//
// This implementation uses CGO to link directly against libmedia_vpx,
// providing better performance than the purego variant by eliminating
// function pointer indirection overhead.

package media

/*
#cgo CFLAGS: -I${SRCDIR}/clib
#cgo darwin LDFLAGS: -L${SRCDIR}/build -lmedia_vpx -Wl,-rpath,${SRCDIR}/build
#cgo linux LDFLAGS: -L${SRCDIR}/build -lmedia_vpx -Wl,-rpath,${SRCDIR}/build

#include "media_vpx.h"
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"
)

// Constants from media_vpx.h
const (
	streamVPXCodecVP8 = C.MEDIA_VPX_CODEC_VP8
	streamVPXCodecVP9 = C.MEDIA_VPX_CODEC_VP9

	streamVPXFrameKey   = C.MEDIA_VPX_FRAME_KEY
	streamVPXFrameDelta = C.MEDIA_VPX_FRAME_DELTA

	streamVPXOK           = C.MEDIA_VPX_OK
	streamVPXError        = C.MEDIA_VPX_ERROR
	streamVPXErrorNoMem   = C.MEDIA_VPX_ERROR_NOMEM
	streamVPXErrorInvalid = C.MEDIA_VPX_ERROR_INVALID
	streamVPXErrorCodec   = C.MEDIA_VPX_ERROR_CODEC
)

// IsVPXAvailable checks if libmedia_vpx is available.
// With CGO this is always true since it links at compile time.
func IsVPXAvailable() bool {
	return true
}

// IsVP8Available checks if VP8 codec is available.
func IsVP8Available() bool {
	return C.media_vpx_codec_available(C.int(streamVPXCodecVP8)) != 0
}

// IsVP9Available checks if VP9 codec is available.
func IsVP9Available() bool {
	return C.media_vpx_codec_available(C.int(streamVPXCodecVP9)) != 0
}

func getVPXError() string {
	cstr := C.media_vpx_get_error()
	if cstr == nil {
		return "unknown error"
	}
	return C.GoString(cstr)
}

// VPXEncoder implements VideoEncoder using libmedia_vpx via CGO.
type VPXEncoder struct {
	config VideoEncoderConfig
	codec  VideoCodec

	handle    C.media_vpx_encoder_t
	outputBuf []byte

	stats   EncoderStats
	statsMu sync.Mutex

	keyframeReq atomic.Bool
	mu          sync.Mutex

	// SVC state
	svcEnabled     bool
	temporalLayers int
	spatialLayers  int
}

// NewVP8Encoder creates a new VP8 encoder.
func NewVP8Encoder(config VideoEncoderConfig) (*VPXEncoder, error) {
	return newVPXEncoder(config, VideoCodecVP8)
}

// NewVP9Encoder creates a new VP9 encoder.
func NewVP9Encoder(config VideoEncoderConfig) (*VPXEncoder, error) {
	return newVPXEncoder(config, VideoCodecVP9)
}

func newVPXEncoder(config VideoEncoderConfig, codec VideoCodec) (*VPXEncoder, error) {
	var codecType C.int
	switch codec {
	case VideoCodecVP8:
		codecType = C.MEDIA_VPX_CODEC_VP8
	case VideoCodecVP9:
		codecType = C.MEDIA_VPX_CODEC_VP9
	default:
		return nil, fmt.Errorf("unsupported codec: %s", codec)
	}

	threads := config.Threads
	if threads <= 0 {
		threads = 4
	}

	bitrateKbps := config.BitrateBps / 1000
	if bitrateKbps <= 0 {
		bitrateKbps = 1000
	}

	fps := config.FPS
	if fps <= 0 {
		fps = 30
	}

	temporalLayers := config.TemporalLayers
	if temporalLayers <= 0 {
		temporalLayers = 1
	}
	spatialLayers := config.SpatialLayers
	if spatialLayers <= 0 {
		spatialLayers = 1
	}

	var handle C.media_vpx_encoder_t
	svcEnabled := temporalLayers > 1 || spatialLayers > 1

	if svcEnabled {
		handle = C.media_vpx_encoder_create_svc(
			codecType,
			C.int(config.Width),
			C.int(config.Height),
			C.int(fps),
			C.int(bitrateKbps),
			C.int(threads),
			C.int(temporalLayers),
			C.int(spatialLayers),
		)
	} else {
		handle = C.media_vpx_encoder_create(
			codecType,
			C.int(config.Width),
			C.int(config.Height),
			C.int(fps),
			C.int(bitrateKbps),
			C.int(threads),
		)
	}

	if handle == 0 {
		return nil, fmt.Errorf("failed to create %s encoder: %s", codec, getVPXError())
	}

	maxOutput := C.media_vpx_encoder_max_output_size(handle)
	if maxOutput <= 0 {
		maxOutput = C.int(config.Width * config.Height * 3 / 2)
	}

	enc := &VPXEncoder{
		config:         config,
		codec:          codec,
		handle:         handle,
		outputBuf:      make([]byte, maxOutput),
		svcEnabled:     svcEnabled,
		temporalLayers: temporalLayers,
		spatialLayers:  spatialLayers,
	}
	enc.keyframeReq.Store(true)

	return enc, nil
}

// Encode implements VideoEncoder.
func (e *VPXEncoder) Encode(frame *VideoFrame) (*EncodedFrame, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return nil, fmt.Errorf("encoder not initialized")
	}

	// Validate frame data
	if frame == nil {
		return nil, fmt.Errorf("frame is nil")
	}
	if len(frame.Data) < 3 {
		return nil, fmt.Errorf("frame requires 3 planes, got %d", len(frame.Data))
	}
	if len(frame.Data[0]) == 0 || len(frame.Data[1]) == 0 || len(frame.Data[2]) == 0 {
		return nil, fmt.Errorf("frame planes cannot be empty: Y=%d U=%d V=%d",
			len(frame.Data[0]), len(frame.Data[1]), len(frame.Data[2]))
	}
	if len(frame.Stride) < 3 || frame.Stride[0] <= 0 || frame.Stride[1] <= 0 {
		return nil, fmt.Errorf("invalid strides: %v", frame.Stride)
	}

	// Validate dimensions match encoder configuration
	if frame.Width != e.config.Width || frame.Height != e.config.Height {
		return nil, fmt.Errorf("frame dimensions %dx%d don't match encoder %dx%d",
			frame.Width, frame.Height, e.config.Width, e.config.Height)
	}

	// Validate plane sizes
	expectedY := frame.Stride[0] * frame.Height
	expectedUV := frame.Stride[1] * (frame.Height / 2)
	if len(frame.Data[0]) < expectedY {
		return nil, fmt.Errorf("Y plane too small: got %d, need %d", len(frame.Data[0]), expectedY)
	}
	if len(frame.Data[1]) < expectedUV || len(frame.Data[2]) < expectedUV {
		return nil, fmt.Errorf("UV planes too small: U=%d V=%d, need %d each",
			len(frame.Data[1]), len(frame.Data[2]), expectedUV)
	}

	forceKeyframe := C.int(0)
	if e.keyframeReq.Swap(false) {
		forceKeyframe = 1
	}

	var frameType C.int
	var pts C.int64_t
	var temporalLayer, spatialLayer C.int
	var result C.int

	yPtr := (*C.uint8_t)(unsafe.Pointer(&frame.Data[0][0]))
	uPtr := (*C.uint8_t)(unsafe.Pointer(&frame.Data[1][0]))
	vPtr := (*C.uint8_t)(unsafe.Pointer(&frame.Data[2][0]))
	outPtr := (*C.uint8_t)(unsafe.Pointer(&e.outputBuf[0]))

	if e.svcEnabled {
		result = C.media_vpx_encoder_encode_svc(
			e.handle,
			yPtr, uPtr, vPtr,
			C.int(frame.Stride[0]),
			C.int(frame.Stride[1]),
			forceKeyframe,
			outPtr,
			C.int(len(e.outputBuf)),
			&frameType,
			&pts,
			&temporalLayer,
			&spatialLayer,
		)
	} else {
		result = C.media_vpx_encoder_encode(
			e.handle,
			yPtr, uPtr, vPtr,
			C.int(frame.Stride[0]),
			C.int(frame.Stride[1]),
			forceKeyframe,
			outPtr,
			C.int(len(e.outputBuf)),
			&frameType,
			&pts,
		)
	}

	if result < 0 {
		return nil, fmt.Errorf("encode failed: %s", getVPXError())
	}

	if result == 0 {
		return nil, nil // No output yet
	}

	// Copy output
	data := make([]byte, result)
	copy(data, e.outputBuf[:result])

	ft := FrameTypeDelta
	if frameType == streamVPXFrameKey {
		ft = FrameTypeKey
	}

	fps := e.config.FPS
	if fps <= 0 {
		fps = 30
	}
	timestamp := uint32(int64(pts) * (90000 / int64(fps)))

	e.statsMu.Lock()
	e.stats.FramesEncoded++
	if ft == FrameTypeKey {
		e.stats.KeyframesEncoded++
	}
	e.stats.BytesEncoded += uint64(result)
	e.statsMu.Unlock()

	return &EncodedFrame{
		Data:            data,
		FrameType:       ft,
		Timestamp:       timestamp,
		Duration:        90000 / uint32(fps),
		TemporalLayerID: uint8(temporalLayer),
		SpatialLayerID:  uint8(spatialLayer),
	}, nil
}

// RequestKeyframe implements VideoEncoder.
func (e *VPXEncoder) RequestKeyframe() {
	e.keyframeReq.Store(true)
	if e.handle != 0 {
		C.media_vpx_encoder_request_keyframe(e.handle)
	}
}

// SetBitrate implements VideoEncoder.
func (e *VPXEncoder) SetBitrate(bitrateBps int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return fmt.Errorf("encoder not initialized")
	}

	bitrateKbps := C.int(bitrateBps / 1000)
	if C.media_vpx_encoder_set_bitrate(e.handle, bitrateKbps) != streamVPXOK {
		return fmt.Errorf("failed to set bitrate: %s", getVPXError())
	}

	e.config.BitrateBps = bitrateBps
	return nil
}

// SetSVCLayers sets both temporal and spatial layers at runtime.
func (e *VPXEncoder) SetSVCLayers(temporalLayers, spatialLayers int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return fmt.Errorf("encoder not initialized")
	}

	if e.codec == VideoCodecVP8 && spatialLayers > 1 {
		return fmt.Errorf("spatial layers not supported for VP8")
	}

	if C.media_vpx_encoder_set_temporal_layers(e.handle, C.int(temporalLayers)) != streamVPXOK {
		return fmt.Errorf("failed to set temporal layers: %s", getVPXError())
	}

	if e.codec == VideoCodecVP9 {
		if C.media_vpx_encoder_set_spatial_layers(e.handle, C.int(spatialLayers)) != streamVPXOK {
			return fmt.Errorf("failed to set spatial layers: %s", getVPXError())
		}
	}

	e.temporalLayers = temporalLayers
	e.spatialLayers = spatialLayers
	e.svcEnabled = temporalLayers > 1 || spatialLayers > 1
	e.config.TemporalLayers = temporalLayers
	e.config.SpatialLayers = spatialLayers
	return nil
}

// SetTemporalLayers sets the number of temporal layers at runtime.
func (e *VPXEncoder) SetTemporalLayers(layers int) error {
	return e.SetSVCLayers(layers, e.spatialLayers)
}

// SetSpatialLayers sets the number of spatial layers at runtime (VP9 only).
func (e *VPXEncoder) SetSpatialLayers(layers int) error {
	return e.SetSVCLayers(e.temporalLayers, layers)
}

// GetSVCConfig returns the current SVC configuration.
func (e *VPXEncoder) GetSVCConfig() (temporalLayers, spatialLayers int, svcEnabled bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return 1, 1, false
	}

	var tempLayers, spatLayers, enabled C.int
	C.media_vpx_encoder_get_svc_config(e.handle, &tempLayers, &spatLayers, &enabled)

	return int(tempLayers), int(spatLayers), enabled != 0
}

// Config implements VideoEncoder.
func (e *VPXEncoder) Config() VideoEncoderConfig {
	return e.config
}

// Codec implements VideoEncoder.
func (e *VPXEncoder) Codec() VideoCodec {
	return e.codec
}

// Stats implements VideoEncoder.
func (e *VPXEncoder) Stats() EncoderStats {
	e.statsMu.Lock()
	defer e.statsMu.Unlock()
	return e.stats
}

// Flush implements VideoEncoder.
func (e *VPXEncoder) Flush() ([]*EncodedFrame, error) {
	return nil, nil
}

// Close implements VideoEncoder.
func (e *VPXEncoder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle != 0 {
		C.media_vpx_encoder_destroy(e.handle)
		e.handle = 0
	}

	return nil
}

// VPXDecoder implements VideoDecoder using libmedia_vpx via CGO.
type VPXDecoder struct {
	config VideoDecoderConfig
	codec  VideoCodec

	handle    C.media_vpx_decoder_t
	outputBuf *VideoFrameBuffer
	width     int
	height    int

	stats   DecoderStats
	statsMu sync.Mutex
	mu      sync.Mutex
}

// NewVP8Decoder creates a new VP8 decoder.
func NewVP8Decoder(config VideoDecoderConfig) (*VPXDecoder, error) {
	return newVPXDecoder(config, VideoCodecVP8)
}

// NewVP9Decoder creates a new VP9 decoder.
func NewVP9Decoder(config VideoDecoderConfig) (*VPXDecoder, error) {
	return newVPXDecoder(config, VideoCodecVP9)
}

func newVPXDecoder(config VideoDecoderConfig, codec VideoCodec) (*VPXDecoder, error) {
	var codecType C.int
	switch codec {
	case VideoCodecVP8:
		codecType = C.MEDIA_VPX_CODEC_VP8
	case VideoCodecVP9:
		codecType = C.MEDIA_VPX_CODEC_VP9
	default:
		return nil, fmt.Errorf("unsupported codec: %s", codec)
	}

	threads := C.int(4)
	if config.Threads > 0 {
		threads = C.int(config.Threads)
	}

	handle := C.media_vpx_decoder_create(codecType, threads)
	if handle == 0 {
		return nil, fmt.Errorf("failed to create %s decoder: %s", codec, getVPXError())
	}

	return &VPXDecoder{
		config: config,
		codec:  codec,
		handle: handle,
	}, nil
}

// Decode implements VideoDecoder.
func (d *VPXDecoder) Decode(encoded *EncodedFrame) (*VideoFrame, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle == 0 {
		return nil, fmt.Errorf("decoder not initialized")
	}

	var outY, outU, outV *C.uint8_t
	var outYStride, outUVStride, outWidth, outHeight C.int

	dataPtr := (*C.uint8_t)(unsafe.Pointer(&encoded.Data[0]))

	result := C.media_vpx_decoder_decode(
		d.handle,
		dataPtr,
		C.int(len(encoded.Data)),
		&outY,
		&outU,
		&outV,
		&outYStride,
		&outUVStride,
		&outWidth,
		&outHeight,
	)

	if result < 0 {
		d.statsMu.Lock()
		d.stats.CorruptedFrames++
		d.statsMu.Unlock()
		return nil, fmt.Errorf("decode failed: %s", getVPXError())
	}

	if result == 0 {
		return nil, nil // Buffering, no frame yet
	}

	w := int(outWidth)
	h := int(outHeight)
	d.width = w
	d.height = h

	// Allocate or reuse output buffer
	if d.outputBuf == nil || d.outputBuf.Width != w || d.outputBuf.Height != h {
		d.outputBuf = NewVideoFrameBuffer(w, h, PixelFormatI420)
	}

	// Copy Y plane from C memory to Go memory
	uvW := w / 2
	uvH := h / 2
	for row := 0; row < h; row++ {
		src := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(outY))+uintptr(row*int(outYStride)))), w)
		dstStart := row * d.outputBuf.StrideY
		copy(d.outputBuf.Y[dstStart:dstStart+w], src)
	}

	// Copy U plane
	for row := 0; row < uvH; row++ {
		src := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(outU))+uintptr(row*int(outUVStride)))), uvW)
		dstStart := row * d.outputBuf.StrideU
		copy(d.outputBuf.U[dstStart:dstStart+uvW], src)
	}

	// Copy V plane
	for row := 0; row < uvH; row++ {
		src := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(outV))+uintptr(row*int(outUVStride)))), uvW)
		dstStart := row * d.outputBuf.StrideV
		copy(d.outputBuf.V[dstStart:dstStart+uvW], src)
	}

	d.outputBuf.TimestampNs = int64(encoded.Timestamp) * 1000000 / 90

	d.statsMu.Lock()
	d.stats.FramesDecoded++
	d.stats.BytesDecoded += uint64(len(encoded.Data))
	if encoded.FrameType == FrameTypeKey {
		d.stats.KeyframesDecoded++
	}
	d.statsMu.Unlock()

	frame := d.outputBuf.ToVideoFrame()
	return &frame, nil
}

// DecodeRTP implements VideoDecoder.
func (d *VPXDecoder) DecodeRTP(data []byte, marker bool, timestamp uint32) (*VideoFrame, error) {
	encoded := &EncodedFrame{
		Data:      data,
		Timestamp: timestamp,
	}
	return d.Decode(encoded)
}

// Config implements VideoDecoder.
func (d *VPXDecoder) Config() VideoDecoderConfig {
	return d.config
}

// Codec implements VideoDecoder.
func (d *VPXDecoder) Codec() VideoCodec {
	return d.codec
}

// Stats implements VideoDecoder.
func (d *VPXDecoder) Stats() DecoderStats {
	d.statsMu.Lock()
	defer d.statsMu.Unlock()
	return d.stats
}

// Flush implements VideoDecoder.
func (d *VPXDecoder) Flush() ([]*VideoFrame, error) {
	return nil, nil
}

// Reset implements VideoDecoder.
func (d *VPXDecoder) Reset() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle == 0 {
		return fmt.Errorf("decoder not initialized")
	}

	if C.media_vpx_decoder_reset(d.handle) != streamVPXOK {
		return fmt.Errorf("failed to reset decoder: %s", getVPXError())
	}

	return nil
}

// GetDimensions implements VideoDecoder.
func (d *VPXDecoder) GetDimensions() (width, height int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle != 0 {
		var w, h C.int
		C.media_vpx_decoder_get_dimensions(d.handle, &w, &h)
		return int(w), int(h)
	}
	return d.width, d.height
}

// Close implements VideoDecoder.
func (d *VPXDecoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle != 0 {
		C.media_vpx_decoder_destroy(d.handle)
		d.handle = 0
	}

	return nil
}

// Register VP8/VP9 encoders and decoders
func init() {
	RegisterVideoEncoder(VideoCodecVP8, func(config VideoEncoderConfig) (VideoEncoder, error) {
		return NewVP8Encoder(config)
	})
	RegisterVideoEncoder(VideoCodecVP9, func(config VideoEncoderConfig) (VideoEncoder, error) {
		return NewVP9Encoder(config)
	})
	RegisterVideoDecoder(VideoCodecVP8, func(config VideoDecoderConfig) (VideoDecoder, error) {
		return NewVP8Decoder(config)
	})
	RegisterVideoDecoder(VideoCodecVP9, func(config VideoDecoderConfig) (VideoDecoder, error) {
		return NewVP9Decoder(config)
	})
}
