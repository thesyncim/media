//go:build (darwin || linux) && !noh264 && cgo

// Package media provides H.264 codec support via libstream_h264 using CGO.

package media

/*
#cgo CFLAGS: -I${SRCDIR}/clib
#cgo darwin LDFLAGS: -L${SRCDIR}/build -lstream_h264 -Wl,-rpath,${SRCDIR}/build
#cgo linux LDFLAGS: -L${SRCDIR}/build -lstream_h264 -Wl,-rpath,${SRCDIR}/build

#include "stream_h264.h"
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

// Constants from stream_h264.h
const (
	streamH264ProfileBaseline = C.STREAM_H264_PROFILE_BASELINE
	streamH264ProfileMain     = C.STREAM_H264_PROFILE_MAIN
	streamH264ProfileHigh     = C.STREAM_H264_PROFILE_HIGH

	streamH264FrameI   = C.STREAM_H264_FRAME_I
	streamH264FrameP   = C.STREAM_H264_FRAME_P
	streamH264FrameB   = C.STREAM_H264_FRAME_B
	streamH264FrameIDR = C.STREAM_H264_FRAME_IDR

	streamH264OK           = C.STREAM_H264_OK
	streamH264Error        = C.STREAM_H264_ERROR
	streamH264ErrorNoMem   = C.STREAM_H264_ERROR_NOMEM
	streamH264ErrorInvalid = C.STREAM_H264_ERROR_INVALID
	streamH264ErrorCodec   = C.STREAM_H264_ERROR_CODEC
)

// h264ProfileToStream converts H264Profile to stream_h264 profile constant.
func h264ProfileToStream(p H264Profile) C.int {
	switch p {
	case H264ProfileMain:
		return streamH264ProfileMain
	case H264ProfileHigh:
		return streamH264ProfileHigh
	default:
		return streamH264ProfileBaseline
	}
}

// IsH264Available checks if libstream_h264 is available.
func IsH264Available() bool {
	return true
}

// IsH264EncoderAvailable checks if H.264 encoder is available.
func IsH264EncoderAvailable() bool {
	return C.stream_h264_encoder_available() != 0
}

// IsH264DecoderAvailable checks if H.264 decoder is available.
func IsH264DecoderAvailable() bool {
	return C.stream_h264_decoder_available() != 0
}

func getH264Error() string {
	cstr := C.stream_h264_get_error()
	if cstr == nil {
		return "unknown error"
	}
	return C.GoString(cstr)
}

// H264Encoder implements VideoEncoder for H.264.
type H264Encoder struct {
	config VideoEncoderConfig

	handle    C.stream_h264_encoder_t
	outputBuf []byte
	profile   H264Profile

	stats   EncoderStats
	statsMu sync.Mutex

	keyframeReq atomic.Bool
	mu          sync.Mutex

	sps []byte
	pps []byte
}

// NewH264Encoder creates a new H.264 encoder.
func NewH264Encoder(config VideoEncoderConfig) (*H264Encoder, error) {
	if C.stream_h264_encoder_available() == 0 {
		return nil, errors.New("H.264 encoder not available (x264 not compiled)")
	}

	profile := config.H264Profile
	if profile == 0 {
		profile = H264ProfileBaseline
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

	handle := C.stream_h264_encoder_create(
		C.int(config.Width),
		C.int(config.Height),
		C.int(fps),
		C.int(bitrateKbps),
		h264ProfileToStream(profile),
		C.int(threads),
	)

	if handle == 0 {
		return nil, fmt.Errorf("failed to create H.264 encoder: %s", getH264Error())
	}

	maxOutput := C.stream_h264_encoder_max_output_size(handle)
	if maxOutput <= 0 {
		maxOutput = C.int(config.Width * config.Height * 3 / 2)
	}

	enc := &H264Encoder{
		config:    config,
		handle:    handle,
		outputBuf: make([]byte, maxOutput),
		profile:   profile,
	}
	enc.keyframeReq.Store(true)

	enc.extractSPSPPS()

	return enc, nil
}

func (e *H264Encoder) extractSPSPPS() {
	spsOut := make([]byte, 256)
	ppsOut := make([]byte, 256)
	var spsLen, ppsLen C.int

	C.stream_h264_encoder_get_sps_pps(
		e.handle,
		(*C.uint8_t)(unsafe.Pointer(&spsOut[0])), 256, &spsLen,
		(*C.uint8_t)(unsafe.Pointer(&ppsOut[0])), 256, &ppsLen,
	)

	if spsLen > 0 {
		e.sps = make([]byte, spsLen)
		copy(e.sps, spsOut[:spsLen])
	}
	if ppsLen > 0 {
		e.pps = make([]byte, ppsLen)
		copy(e.pps, ppsOut[:ppsLen])
	}
}

// GetSPS returns the Sequence Parameter Set.
func (e *H264Encoder) GetSPS() []byte {
	return e.sps
}

// GetPPS returns the Picture Parameter Set.
func (e *H264Encoder) GetPPS() []byte {
	return e.pps
}

// Encode implements VideoEncoder.
func (e *H264Encoder) Encode(frame *VideoFrame) (*EncodedFrame, error) {
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
	var pts, dts C.int64_t

	result := C.stream_h264_encoder_encode(
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
		&dts,
	)

	if result < 0 {
		return nil, fmt.Errorf("encode failed: %s", getH264Error())
	}

	if result == 0 {
		return nil, nil
	}

	data := make([]byte, result)
	copy(data, e.outputBuf[:result])

	ft := FrameTypeDelta
	if frameType == streamH264FrameIDR || frameType == streamH264FrameI {
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
		Data:      data,
		FrameType: ft,
		Timestamp: timestamp,
		Duration:  90000 / uint32(fps),
	}, nil
}

// RequestKeyframe implements VideoEncoder.
func (e *H264Encoder) RequestKeyframe() {
	e.keyframeReq.Store(true)
	if e.handle != 0 {
		C.stream_h264_encoder_request_keyframe(e.handle)
	}
}

// SetBitrate implements VideoEncoder.
func (e *H264Encoder) SetBitrate(bitrateBps int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return fmt.Errorf("encoder not initialized")
	}

	if C.stream_h264_encoder_set_bitrate(e.handle, C.int(bitrateBps/1000)) != streamH264OK {
		return fmt.Errorf("failed to set bitrate: %s", getH264Error())
	}

	e.config.BitrateBps = bitrateBps
	return nil
}

func (e *H264Encoder) Config() VideoEncoderConfig { return e.config }
func (e *H264Encoder) Codec() VideoCodec          { return VideoCodecH264 }

func (e *H264Encoder) Stats() EncoderStats {
	e.statsMu.Lock()
	defer e.statsMu.Unlock()
	return e.stats
}

func (e *H264Encoder) Flush() ([]*EncodedFrame, error) { return nil, nil }

func (e *H264Encoder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.handle != 0 {
		C.stream_h264_encoder_destroy(e.handle)
		e.handle = 0
	}
	return nil
}

// H264Decoder implements VideoDecoder for H.264.
type H264Decoder struct {
	config    VideoDecoderConfig
	handle    C.stream_h264_decoder_t
	outputBuf *VideoFrameBuffer
	width     int
	height    int
	stats     DecoderStats
	statsMu   sync.Mutex
	mu        sync.Mutex
}

// NewH264Decoder creates a new H.264 decoder.
func NewH264Decoder(config VideoDecoderConfig) (*H264Decoder, error) {
	if C.stream_h264_decoder_available() == 0 {
		return nil, errors.New("H.264 decoder not available")
	}

	threads := C.int(4)
	if config.Threads > 0 {
		threads = C.int(config.Threads)
	}

	handle := C.stream_h264_decoder_create(threads)
	if handle == 0 {
		return nil, fmt.Errorf("failed to create H.264 decoder: %s", getH264Error())
	}

	return &H264Decoder{
		config: config,
		handle: handle,
	}, nil
}

func (d *H264Decoder) Decode(encoded *EncodedFrame) (*VideoFrame, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle == 0 {
		return nil, fmt.Errorf("decoder not initialized")
	}

	var outY, outU, outV *C.uint8_t
	var outYStride, outUVStride, outWidth, outHeight C.int

	result := C.stream_h264_decoder_decode(
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
		return nil, fmt.Errorf("decode failed: %s", getH264Error())
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

func (d *H264Decoder) DecodeRTP(data []byte, marker bool, timestamp uint32) (*VideoFrame, error) {
	return d.Decode(&EncodedFrame{Data: data, Timestamp: timestamp})
}

func (d *H264Decoder) Config() VideoDecoderConfig { return d.config }
func (d *H264Decoder) Codec() VideoCodec          { return VideoCodecH264 }

func (d *H264Decoder) Stats() DecoderStats {
	d.statsMu.Lock()
	defer d.statsMu.Unlock()
	return d.stats
}

func (d *H264Decoder) Flush() ([]*VideoFrame, error) { return nil, nil }

func (d *H264Decoder) Reset() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.handle == 0 {
		return fmt.Errorf("decoder not initialized")
	}
	if C.stream_h264_decoder_reset(d.handle) != streamH264OK {
		return fmt.Errorf("failed to reset decoder: %s", getH264Error())
	}
	return nil
}

func (d *H264Decoder) GetDimensions() (width, height int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.handle != 0 {
		var w, h C.int
		C.stream_h264_decoder_get_dimensions(d.handle, &w, &h)
		return int(w), int(h)
	}
	return d.width, d.height
}

func (d *H264Decoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.handle != 0 {
		C.stream_h264_decoder_destroy(d.handle)
		d.handle = 0
	}
	return nil
}

func init() {
	RegisterVideoEncoder(VideoCodecH264, func(config VideoEncoderConfig) (VideoEncoder, error) {
		return NewH264Encoder(config)
	})
	RegisterVideoDecoder(VideoCodecH264, func(config VideoDecoderConfig) (VideoDecoder, error) {
		return NewH264Decoder(config)
	})
}
