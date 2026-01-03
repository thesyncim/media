//go:build (darwin || linux) && !noav1 && cgo

// Package media provides AV1 codec support via libstream_av1 using CGO.

package media

/*
#cgo CFLAGS: -I${SRCDIR}/clib
#cgo darwin LDFLAGS: -L${SRCDIR}/build -lstream_av1 -Wl,-rpath,${SRCDIR}/build
#cgo linux LDFLAGS: -L${SRCDIR}/build -lstream_av1 -Wl,-rpath,${SRCDIR}/build

#include "stream_av1.h"
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"
)

// Constants from stream_av1.h
const (
	streamAV1FrameKey       = C.STREAM_AV1_FRAME_KEY
	streamAV1FrameInter     = C.STREAM_AV1_FRAME_INTER
	streamAV1FrameIntraOnly = C.STREAM_AV1_FRAME_INTRA_ONLY
	streamAV1FrameSwitch    = C.STREAM_AV1_FRAME_SWITCH

	streamAV1UsageRealtime    = C.STREAM_AV1_USAGE_REALTIME
	streamAV1UsageGoodQuality = C.STREAM_AV1_USAGE_GOODQUALITY

	streamAV1OK           = C.STREAM_AV1_OK
	streamAV1Error        = C.STREAM_AV1_ERROR
	streamAV1ErrorNoMem   = C.STREAM_AV1_ERROR_NOMEM
	streamAV1ErrorInvalid = C.STREAM_AV1_ERROR_INVALID
	streamAV1ErrorCodec   = C.STREAM_AV1_ERROR_CODEC
)

// AV1Usage defines the encoding usage preset
type AV1Usage int

const (
	AV1UsageRealtime    AV1Usage = streamAV1UsageRealtime
	AV1UsageGoodQuality AV1Usage = streamAV1UsageGoodQuality
)

// IsAV1Available checks if libstream_av1 is available.
func IsAV1Available() bool {
	return true
}

// IsAV1EncoderAvailable checks if AV1 encoder is available.
func IsAV1EncoderAvailable() bool {
	return C.stream_av1_encoder_available() != 0
}

// IsAV1DecoderAvailable checks if AV1 decoder is available.
func IsAV1DecoderAvailable() bool {
	return C.stream_av1_decoder_available() != 0
}

func getAV1Error() string {
	cstr := C.stream_av1_get_error()
	if cstr == nil {
		return "unknown error"
	}
	return C.GoString(cstr)
}

// AV1Encoder implements VideoEncoder for AV1.
type AV1Encoder struct {
	config VideoEncoderConfig

	handle    C.stream_av1_encoder_t
	outputBuf []byte
	usage     AV1Usage

	stats   EncoderStats
	statsMu sync.Mutex

	keyframeReq atomic.Bool
	mu          sync.Mutex

	svcEnabled     bool
	temporalLayers int
	spatialLayers  int
}

// NewAV1Encoder creates a new AV1 encoder.
func NewAV1Encoder(config VideoEncoderConfig) (*AV1Encoder, error) {
	if C.stream_av1_encoder_available() == 0 {
		return nil, errors.New("AV1 encoder not available (libaom not compiled)")
	}

	usage := AV1UsageRealtime
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

	var handle C.stream_av1_encoder_t
	svcEnabled := temporalLayers > 1 || spatialLayers > 1

	if svcEnabled {
		handle = C.stream_av1_encoder_create_svc(
			C.int(config.Width),
			C.int(config.Height),
			C.int(fps),
			C.int(bitrateKbps),
			C.int(usage),
			C.int(threads),
			C.int(temporalLayers),
			C.int(spatialLayers),
		)
	} else {
		handle = C.stream_av1_encoder_create(
			C.int(config.Width),
			C.int(config.Height),
			C.int(fps),
			C.int(bitrateKbps),
			C.int(usage),
			C.int(threads),
		)
	}

	if handle == 0 {
		return nil, fmt.Errorf("failed to create AV1 encoder: %s", getAV1Error())
	}

	maxOutput := C.stream_av1_encoder_max_output_size(handle)
	if maxOutput <= 0 {
		maxOutput = C.int(config.Width * config.Height * 3 / 2)
	}

	enc := &AV1Encoder{
		config:         config,
		handle:         handle,
		outputBuf:      make([]byte, maxOutput),
		usage:          usage,
		svcEnabled:     svcEnabled,
		temporalLayers: temporalLayers,
		spatialLayers:  spatialLayers,
	}
	enc.keyframeReq.Store(true)

	return enc, nil
}

// Encode implements VideoEncoder.
func (e *AV1Encoder) Encode(frame *VideoFrame) (*EncodedFrame, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return nil, fmt.Errorf("encoder not initialized")
	}

	forceKeyframe := C.int(0)
	if e.keyframeReq.Swap(false) {
		forceKeyframe = 1
	}

	var frameType C.int
	var pts C.int64_t
	var temporalLayer, spatialLayer C.int
	var result C.int

	if e.svcEnabled {
		result = C.stream_av1_encoder_encode_svc(
			e.handle,
			(*C.uint8_t)(unsafe.Pointer(&frame.Data[0][0])),
			(*C.uint8_t)(unsafe.Pointer(&frame.Data[1][0])),
			(*C.uint8_t)(unsafe.Pointer(&frame.Data[2][0])),
			C.int(frame.Stride[0]),
			C.int(frame.Stride[1]),
			forceKeyframe,
			(*C.uint8_t)(unsafe.Pointer(&e.outputBuf[0])),
			C.int(len(e.outputBuf)),
			&frameType,
			&pts,
			&temporalLayer,
			&spatialLayer,
		)
	} else {
		result = C.stream_av1_encoder_encode(
			e.handle,
			(*C.uint8_t)(unsafe.Pointer(&frame.Data[0][0])),
			(*C.uint8_t)(unsafe.Pointer(&frame.Data[1][0])),
			(*C.uint8_t)(unsafe.Pointer(&frame.Data[2][0])),
			C.int(frame.Stride[0]),
			C.int(frame.Stride[1]),
			forceKeyframe,
			(*C.uint8_t)(unsafe.Pointer(&e.outputBuf[0])),
			C.int(len(e.outputBuf)),
			&frameType,
			&pts,
		)
	}

	if result < 0 {
		return nil, fmt.Errorf("encode failed: %s", getAV1Error())
	}

	if result == 0 {
		return nil, nil
	}

	data := make([]byte, result)
	copy(data, e.outputBuf[:result])

	ft := FrameTypeDelta
	if frameType == streamAV1FrameKey {
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
func (e *AV1Encoder) RequestKeyframe() {
	e.keyframeReq.Store(true)
	if e.handle != 0 {
		C.stream_av1_encoder_request_keyframe(e.handle)
	}
}

// SetBitrate implements VideoEncoder.
func (e *AV1Encoder) SetBitrate(bitrateBps int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return fmt.Errorf("encoder not initialized")
	}

	if C.stream_av1_encoder_set_bitrate(e.handle, C.int(bitrateBps/1000)) != streamAV1OK {
		return fmt.Errorf("failed to set bitrate: %s", getAV1Error())
	}

	e.config.BitrateBps = bitrateBps
	return nil
}

// SetSVCLayers sets both temporal and spatial layers at runtime.
func (e *AV1Encoder) SetSVCLayers(temporalLayers, spatialLayers int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return fmt.Errorf("encoder not initialized")
	}

	if C.stream_av1_encoder_set_temporal_layers(e.handle, C.int(temporalLayers)) != streamAV1OK {
		return fmt.Errorf("failed to set temporal layers: %s", getAV1Error())
	}

	if C.stream_av1_encoder_set_spatial_layers(e.handle, C.int(spatialLayers)) != streamAV1OK {
		return fmt.Errorf("failed to set spatial layers: %s", getAV1Error())
	}

	e.temporalLayers = temporalLayers
	e.spatialLayers = spatialLayers
	e.svcEnabled = temporalLayers > 1 || spatialLayers > 1
	e.config.TemporalLayers = temporalLayers
	e.config.SpatialLayers = spatialLayers
	return nil
}

// GetSVCConfig returns the current SVC configuration.
func (e *AV1Encoder) GetSVCConfig() (temporalLayers, spatialLayers int, svcEnabled bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return 1, 1, false
	}

	var tempLayers, spatLayers, enabled C.int
	C.stream_av1_encoder_get_svc_config(e.handle, &tempLayers, &spatLayers, &enabled)

	return int(tempLayers), int(spatLayers), enabled != 0
}

func (e *AV1Encoder) Config() VideoEncoderConfig { return e.config }
func (e *AV1Encoder) Codec() VideoCodec          { return VideoCodecAV1 }

func (e *AV1Encoder) Stats() EncoderStats {
	e.statsMu.Lock()
	defer e.statsMu.Unlock()
	return e.stats
}

func (e *AV1Encoder) Flush() ([]*EncodedFrame, error) { return nil, nil }

func (e *AV1Encoder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.handle != 0 {
		C.stream_av1_encoder_destroy(e.handle)
		e.handle = 0
	}
	return nil
}

// AV1Decoder implements VideoDecoder for AV1.
type AV1Decoder struct {
	config    VideoDecoderConfig
	handle    C.stream_av1_decoder_t
	outputBuf *VideoFrameBuffer
	width     int
	height    int
	stats     DecoderStats
	statsMu   sync.Mutex
	mu        sync.Mutex
}

// NewAV1Decoder creates a new AV1 decoder.
func NewAV1Decoder(config VideoDecoderConfig) (*AV1Decoder, error) {
	if C.stream_av1_decoder_available() == 0 {
		return nil, errors.New("AV1 decoder not available (libaom not compiled)")
	}

	threads := C.int(4)
	if config.Threads > 0 {
		threads = C.int(config.Threads)
	}

	handle := C.stream_av1_decoder_create(threads)
	if handle == 0 {
		return nil, fmt.Errorf("failed to create AV1 decoder: %s", getAV1Error())
	}

	return &AV1Decoder{
		config: config,
		handle: handle,
	}, nil
}

func (d *AV1Decoder) Decode(encoded *EncodedFrame) (*VideoFrame, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle == 0 {
		return nil, fmt.Errorf("decoder not initialized")
	}

	var outY, outU, outV *C.uint8_t
	var outYStride, outUVStride, outWidth, outHeight C.int

	result := C.stream_av1_decoder_decode(
		d.handle,
		(*C.uint8_t)(unsafe.Pointer(&encoded.Data[0])),
		C.int(len(encoded.Data)),
		&outY, &outU, &outV,
		&outYStride, &outUVStride,
		&outWidth, &outHeight,
	)

	if result < 0 {
		d.statsMu.Lock()
		d.stats.CorruptedFrames++
		d.statsMu.Unlock()
		return nil, fmt.Errorf("decode failed: %s", getAV1Error())
	}

	if result == 0 {
		return nil, nil
	}

	w := int(outWidth)
	h := int(outHeight)
	d.width = w
	d.height = h

	if d.outputBuf == nil || d.outputBuf.Width != w || d.outputBuf.Height != h {
		d.outputBuf = NewVideoFrameBuffer(w, h, PixelFormatI420)
	}

	uvW := w / 2
	uvH := h / 2
	for row := 0; row < h; row++ {
		src := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(outY))+uintptr(row*int(outYStride)))), w)
		copy(d.outputBuf.Y[row*d.outputBuf.StrideY:], src)
	}
	for row := 0; row < uvH; row++ {
		src := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(outU))+uintptr(row*int(outUVStride)))), uvW)
		copy(d.outputBuf.U[row*d.outputBuf.StrideU:], src)
	}
	for row := 0; row < uvH; row++ {
		src := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(outV))+uintptr(row*int(outUVStride)))), uvW)
		copy(d.outputBuf.V[row*d.outputBuf.StrideV:], src)
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

func (d *AV1Decoder) DecodeRTP(data []byte, marker bool, timestamp uint32) (*VideoFrame, error) {
	return d.Decode(&EncodedFrame{Data: data, Timestamp: timestamp})
}

func (d *AV1Decoder) Config() VideoDecoderConfig { return d.config }
func (d *AV1Decoder) Codec() VideoCodec          { return VideoCodecAV1 }

func (d *AV1Decoder) Stats() DecoderStats {
	d.statsMu.Lock()
	defer d.statsMu.Unlock()
	return d.stats
}

func (d *AV1Decoder) Flush() ([]*VideoFrame, error) { return nil, nil }

func (d *AV1Decoder) Reset() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.handle == 0 {
		return fmt.Errorf("decoder not initialized")
	}
	if C.stream_av1_decoder_reset(d.handle) != streamAV1OK {
		return fmt.Errorf("failed to reset decoder: %s", getAV1Error())
	}
	return nil
}

func (d *AV1Decoder) GetDimensions() (width, height int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.handle != 0 {
		var w, h C.int
		C.stream_av1_decoder_get_dimensions(d.handle, &w, &h)
		return int(w), int(h)
	}
	return d.width, d.height
}

func (d *AV1Decoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.handle != 0 {
		C.stream_av1_decoder_destroy(d.handle)
		d.handle = 0
	}
	return nil
}

func init() {
	RegisterVideoEncoder(VideoCodecAV1, func(config VideoEncoderConfig) (VideoEncoder, error) {
		return NewAV1Encoder(config)
	})
	RegisterVideoDecoder(VideoCodecAV1, func(config VideoDecoderConfig) (VideoDecoder, error) {
		return NewAV1Decoder(config)
	})
}
