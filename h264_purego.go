//go:build (darwin || linux) && !noh264 && !cgo

// Package media provides H.264 codec support via libstream_h264 using purego.

package media

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	streamH264Once    sync.Once
	streamH264Handle  uintptr
	streamH264InitErr error
	streamH264Loaded  bool
)

// libstream_h264 function pointers
var (
	streamH264EncoderCreate       func(width, height, fps, bitrateKbps, profile, threads int32) uint64
	streamH264EncoderEncode       func(encoder uint64, yPlane, uPlane, vPlane uintptr, yStride, uvStride, forceKeyframe int32, outData uintptr, outCapacity int32, outFrameType, outPts, outDts uintptr) int32
	streamH264EncoderMaxOutputSize func(encoder uint64) int32
	streamH264EncoderRequestKF    func(encoder uint64)
	streamH264EncoderSetBitrate   func(encoder uint64, bitrateKbps int32) int32
	streamH264EncoderGetSPSPPS    func(encoder uint64, spsOut uintptr, spsCapacity int32, spsLen uintptr, ppsOut uintptr, ppsCapacity int32, ppsLen uintptr) int32
	streamH264EncoderGetStats     func(encoder uint64, framesEncoded, keyframesEncoded, bytesEncoded uintptr)
	streamH264EncoderDestroy      func(encoder uint64)

	streamH264DecoderCreate        func(threads int32) uint64
	streamH264DecoderDecode        func(decoder uint64, data uintptr, dataLen int32, outY, outU, outV, outYStride, outUVStride, outWidth, outHeight uintptr) int32
	streamH264DecoderGetDimensions func(decoder uint64, width, height uintptr)
	streamH264DecoderGetStats      func(decoder uint64, framesDecoded, keyframesDecoded, bytesDecoded, corruptedFrames uintptr)
	streamH264DecoderReset         func(decoder uint64) int32
	streamH264DecoderDestroy       func(decoder uint64)

	streamH264GetError         func() uintptr
	streamH264EncoderAvailable func() int32
	streamH264DecoderAvailable func() int32
)

// Constants from stream_h264.h
const (
	streamH264ProfileBaseline = 66
	streamH264ProfileMain     = 77
	streamH264ProfileHigh     = 100

	streamH264FrameI   = 0
	streamH264FrameP   = 1
	streamH264FrameB   = 2
	streamH264FrameIDR = 3

	streamH264OK           = 0
	streamH264Error        = -1
	streamH264ErrorNoMem   = -2
	streamH264ErrorInvalid = -3
	streamH264ErrorCodec   = -4
)

// h264ProfileToStreamPurego converts H264Profile to stream_h264 profile constant.
func h264ProfileToStreamPurego(p H264Profile) int {
	switch p {
	case H264ProfileMain:
		return streamH264ProfileMain
	case H264ProfileHigh:
		return streamH264ProfileHigh
	default:
		return streamH264ProfileBaseline
	}
}

func loadStreamH264() error {
	streamH264Once.Do(func() {
		streamH264InitErr = loadStreamH264Lib()
		if streamH264InitErr == nil {
			streamH264Loaded = true
		}
	})
	return streamH264InitErr
}

func loadStreamH264Lib() error {
	paths := getStreamH264LibPaths()

	var lastErr error
	for _, path := range paths {
		handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err == nil {
			streamH264Handle = handle
			if err := loadStreamH264Symbols(); err != nil {
				purego.Dlclose(handle)
				lastErr = err
				continue
			}
			return nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return fmt.Errorf("failed to load libstream_h264: %w", lastErr)
	}
	return errors.New("libstream_h264 not found in any standard location")
}

func getStreamH264LibPaths() []string {
	var paths []string

	libName := "libstream_h264.so"
	if runtime.GOOS == "darwin" {
		libName = "libstream_h264.dylib"
	}

	// Environment variable overrides (highest priority)
	if envPath := os.Getenv("STREAM_H264_LIB_PATH"); envPath != "" {
		paths = append(paths, envPath)
	}
	if envPath := os.Getenv("STREAM_SDK_LIB_PATH"); envPath != "" {
		paths = append(paths, filepath.Join(envPath, libName))
	}

	// Search relative to executable location
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		paths = append(paths,
			filepath.Join(exeDir, libName),
			filepath.Join(exeDir, "..", "lib", libName),
			filepath.Join(exeDir, "..", "..", "build", libName),
			filepath.Join(exeDir, "..", "..", "build", "ffi", libName),
		)
	}

	// Search relative to working directory (with parent traversal)
	if wd, err := os.Getwd(); err == nil {
		paths = append(paths,
			filepath.Join(wd, "build", libName),
			filepath.Join(wd, "build", "ffi", libName),
			filepath.Join(wd, "..", "build", libName),
			filepath.Join(wd, "..", "build", "ffi", libName),
			filepath.Join(wd, "..", "..", "build", libName),
			filepath.Join(wd, "..", "..", "build", "ffi", libName),
			filepath.Join(wd, "..", "..", "..", "build", libName),
			filepath.Join(wd, "..", "..", "..", "build", "ffi", libName),
			filepath.Join(wd, "..", "..", "..", "..", "build", libName),
			filepath.Join(wd, "..", "..", "..", "..", "build", "ffi", libName),
		)
	}

	// Search relative to source root (uses runtime.Caller - works in IDE/tests)
	if sourceRoot := findSourceRoot(); sourceRoot != "" {
		paths = append(paths,
			filepath.Join(sourceRoot, "build", libName),
			filepath.Join(sourceRoot, "build", "ffi", libName),
		)
	}

	// Search relative to module root (find go.mod from cwd)
	if moduleRoot := findModuleRoot(); moduleRoot != "" {
		paths = append(paths,
			filepath.Join(moduleRoot, "build", libName),
			filepath.Join(moduleRoot, "build", "ffi", libName),
		)
	}

	// Development paths - relative
	devPaths := []string{
		"build",
		"build/ffi",
		"../../build",
		"../../build/ffi",
		"../../../../build",
		"../../../../build/ffi",
	}
	for _, devPath := range devPaths {
		paths = append(paths, filepath.Join(devPath, libName))
	}

	// System paths (lowest priority)
	switch runtime.GOOS {
	case "darwin":
		paths = append(paths,
			"libstream_h264.dylib",
			"/usr/local/lib/libstream_h264.dylib",
			"/opt/homebrew/lib/libstream_h264.dylib",
		)
	case "linux":
		paths = append(paths,
			"libstream_h264.so",
			"/usr/local/lib/libstream_h264.so",
			"/usr/lib/libstream_h264.so",
		)
	}

	return paths
}

func loadStreamH264Symbols() error {
	purego.RegisterLibFunc(&streamH264EncoderCreate, streamH264Handle, "stream_h264_encoder_create")
	purego.RegisterLibFunc(&streamH264EncoderEncode, streamH264Handle, "stream_h264_encoder_encode")
	purego.RegisterLibFunc(&streamH264EncoderMaxOutputSize, streamH264Handle, "stream_h264_encoder_max_output_size")
	purego.RegisterLibFunc(&streamH264EncoderRequestKF, streamH264Handle, "stream_h264_encoder_request_keyframe")
	purego.RegisterLibFunc(&streamH264EncoderSetBitrate, streamH264Handle, "stream_h264_encoder_set_bitrate")
	purego.RegisterLibFunc(&streamH264EncoderGetSPSPPS, streamH264Handle, "stream_h264_encoder_get_sps_pps")
	purego.RegisterLibFunc(&streamH264EncoderGetStats, streamH264Handle, "stream_h264_encoder_get_stats")
	purego.RegisterLibFunc(&streamH264EncoderDestroy, streamH264Handle, "stream_h264_encoder_destroy")

	purego.RegisterLibFunc(&streamH264DecoderCreate, streamH264Handle, "stream_h264_decoder_create")
	purego.RegisterLibFunc(&streamH264DecoderDecode, streamH264Handle, "stream_h264_decoder_decode")
	purego.RegisterLibFunc(&streamH264DecoderGetDimensions, streamH264Handle, "stream_h264_decoder_get_dimensions")
	purego.RegisterLibFunc(&streamH264DecoderGetStats, streamH264Handle, "stream_h264_decoder_get_stats")
	purego.RegisterLibFunc(&streamH264DecoderReset, streamH264Handle, "stream_h264_decoder_reset")
	purego.RegisterLibFunc(&streamH264DecoderDestroy, streamH264Handle, "stream_h264_decoder_destroy")

	purego.RegisterLibFunc(&streamH264GetError, streamH264Handle, "stream_h264_get_error")
	purego.RegisterLibFunc(&streamH264EncoderAvailable, streamH264Handle, "stream_h264_encoder_available")
	purego.RegisterLibFunc(&streamH264DecoderAvailable, streamH264Handle, "stream_h264_decoder_available")

	return nil
}

// IsH264Available checks if libstream_h264 is available.
func IsH264Available() bool {
	if err := loadStreamH264(); err != nil {
		return false
	}
	return streamH264Loaded
}

// IsH264EncoderAvailable checks if H.264 encoder is available.
func IsH264EncoderAvailable() bool {
	if !IsH264Available() {
		return false
	}
	return streamH264EncoderAvailable() != 0
}

// IsH264DecoderAvailable checks if H.264 decoder is available.
func IsH264DecoderAvailable() bool {
	if !IsH264Available() {
		return false
	}
	return streamH264DecoderAvailable() != 0
}

func getH264Error() string {
	ptr := streamH264GetError()
	if ptr == 0 {
		return "unknown error"
	}
	return goStringFromPtr(ptr)
}

// H264Encoder implements VideoEncoder for H.264.
type H264Encoder struct {
	config VideoEncoderConfig

	handle    uint64
	outputBuf []byte
	profile   H264Profile

	stats   EncoderStats
	statsMu sync.Mutex

	keyframeReq atomic.Bool
	mu          sync.Mutex

	// Cached SPS/PPS
	sps []byte
	pps []byte
}

// NewH264Encoder creates a new H.264 encoder.
func NewH264Encoder(config VideoEncoderConfig) (*H264Encoder, error) {
	if err := loadStreamH264(); err != nil {
		return nil, fmt.Errorf("H.264 encoder not available: %w", err)
	}

	if streamH264EncoderAvailable() == 0 {
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

	handle := streamH264EncoderCreate(
		int32(config.Width),
		int32(config.Height),
		int32(fps),
		int32(bitrateKbps),
		int32(h264ProfileToStreamPurego(profile)),
		int32(threads),
	)

	if handle == 0 {
		return nil, fmt.Errorf("failed to create H.264 encoder: %s", getH264Error())
	}

	maxOutput := streamH264EncoderMaxOutputSize(handle)
	if maxOutput <= 0 {
		maxOutput = int32(config.Width * config.Height * 3 / 2)
	}

	enc := &H264Encoder{
		config:    config,
		handle:    handle,
		outputBuf: make([]byte, maxOutput),
		profile:   profile,
	}
	enc.keyframeReq.Store(true)

	// Cache SPS/PPS
	enc.extractSPSPPS()

	return enc, nil
}

func (e *H264Encoder) extractSPSPPS() {
	spsOut := make([]byte, 256)
	ppsOut := make([]byte, 256)
	var spsLen, ppsLen int32

	streamH264EncoderGetSPSPPS(
		e.handle,
		uintptr(unsafe.Pointer(&spsOut[0])), 256, uintptr(unsafe.Pointer(&spsLen)),
		uintptr(unsafe.Pointer(&ppsOut[0])), 256, uintptr(unsafe.Pointer(&ppsLen)),
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

	forceKeyframe := int32(0)
	if e.keyframeReq.Swap(false) {
		forceKeyframe = 1
	}

	var frameType int32
	var pts, dts int64

	result := streamH264EncoderEncode(
		e.handle,
		uintptr(unsafe.Pointer(&frame.Data[0][0])),
		uintptr(unsafe.Pointer(&frame.Data[1][0])),
		uintptr(unsafe.Pointer(&frame.Data[2][0])),
		int32(frame.Stride[0]),
		int32(frame.Stride[1]),
		forceKeyframe,
		uintptr(unsafe.Pointer(&e.outputBuf[0])),
		int32(len(e.outputBuf)),
		uintptr(unsafe.Pointer(&frameType)),
		uintptr(unsafe.Pointer(&pts)),
		uintptr(unsafe.Pointer(&dts)),
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
	timestamp := uint32(pts * (90000 / int64(fps)))

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
		streamH264EncoderRequestKF(e.handle)
	}
}

// SetBitrate implements VideoEncoder.
func (e *H264Encoder) SetBitrate(bitrateBps int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return fmt.Errorf("encoder not initialized")
	}

	bitrateKbps := int32(bitrateBps / 1000)
	if streamH264EncoderSetBitrate(e.handle, bitrateKbps) != streamH264OK {
		return fmt.Errorf("failed to set bitrate: %s", getH264Error())
	}

	e.config.BitrateBps = bitrateBps
	return nil
}

// Config implements VideoEncoder.
func (e *H264Encoder) Config() VideoEncoderConfig {
	return e.config
}

// Codec implements VideoEncoder.
func (e *H264Encoder) Codec() VideoCodec {
	return VideoCodecH264
}

// Stats implements VideoEncoder.
func (e *H264Encoder) Stats() EncoderStats {
	e.statsMu.Lock()
	defer e.statsMu.Unlock()
	return e.stats
}

// Flush implements VideoEncoder.
func (e *H264Encoder) Flush() ([]*EncodedFrame, error) {
	return nil, nil
}

// Close implements VideoEncoder.
func (e *H264Encoder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle != 0 {
		streamH264EncoderDestroy(e.handle)
		e.handle = 0
	}

	return nil
}

// H264Decoder implements VideoDecoder for H.264.
type H264Decoder struct {
	config VideoDecoderConfig

	handle    uint64
	outputBuf *VideoFrameBuffer
	width     int
	height    int

	stats   DecoderStats
	statsMu sync.Mutex
	mu      sync.Mutex
}

// NewH264Decoder creates a new H.264 decoder.
func NewH264Decoder(config VideoDecoderConfig) (*H264Decoder, error) {
	if err := loadStreamH264(); err != nil {
		return nil, fmt.Errorf("H.264 decoder not available: %w", err)
	}

	if streamH264DecoderAvailable() == 0 {
		return nil, errors.New("H.264 decoder not available")
	}

	threads := int32(4)
	if config.Threads > 0 {
		threads = int32(config.Threads)
	}

	handle := streamH264DecoderCreate(threads)
	if handle == 0 {
		return nil, fmt.Errorf("failed to create H.264 decoder: %s", getH264Error())
	}

	return &H264Decoder{
		config: config,
		handle: handle,
	}, nil
}

// Decode implements VideoDecoder.
func (d *H264Decoder) Decode(encoded *EncodedFrame) (*VideoFrame, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle == 0 {
		return nil, fmt.Errorf("decoder not initialized")
	}

	var outY, outU, outV uintptr
	var outYStride, outUVStride, outWidth, outHeight int32

	result := streamH264DecoderDecode(
		d.handle,
		uintptr(unsafe.Pointer(&encoded.Data[0])),
		int32(len(encoded.Data)),
		uintptr(unsafe.Pointer(&outY)),
		uintptr(unsafe.Pointer(&outU)),
		uintptr(unsafe.Pointer(&outV)),
		uintptr(unsafe.Pointer(&outYStride)),
		uintptr(unsafe.Pointer(&outUVStride)),
		uintptr(unsafe.Pointer(&outWidth)),
		uintptr(unsafe.Pointer(&outHeight)),
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
		src := unsafe.Slice((*byte)(unsafe.Pointer(outY+uintptr(row*int(outYStride)))), w)
		dstStart := row * d.outputBuf.StrideY
		copy(d.outputBuf.Y[dstStart:dstStart+w], src)
	}

	for row := 0; row < uvH; row++ {
		src := unsafe.Slice((*byte)(unsafe.Pointer(outU+uintptr(row*int(outUVStride)))), uvW)
		dstStart := row * d.outputBuf.StrideU
		copy(d.outputBuf.U[dstStart:dstStart+uvW], src)
	}

	for row := 0; row < uvH; row++ {
		src := unsafe.Slice((*byte)(unsafe.Pointer(outV+uintptr(row*int(outUVStride)))), uvW)
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
func (d *H264Decoder) DecodeRTP(data []byte, marker bool, timestamp uint32) (*VideoFrame, error) {
	encoded := &EncodedFrame{
		Data:      data,
		Timestamp: timestamp,
	}
	return d.Decode(encoded)
}

// Config implements VideoDecoder.
func (d *H264Decoder) Config() VideoDecoderConfig {
	return d.config
}

// Codec implements VideoDecoder.
func (d *H264Decoder) Codec() VideoCodec {
	return VideoCodecH264
}

// Stats implements VideoDecoder.
func (d *H264Decoder) Stats() DecoderStats {
	d.statsMu.Lock()
	defer d.statsMu.Unlock()
	return d.stats
}

// Flush implements VideoDecoder.
func (d *H264Decoder) Flush() ([]*VideoFrame, error) {
	return nil, nil
}

// Reset implements VideoDecoder.
func (d *H264Decoder) Reset() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle == 0 {
		return fmt.Errorf("decoder not initialized")
	}

	if streamH264DecoderReset(d.handle) != streamH264OK {
		return fmt.Errorf("failed to reset decoder: %s", getH264Error())
	}

	return nil
}

// GetDimensions implements VideoDecoder.
func (d *H264Decoder) GetDimensions() (width, height int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle != 0 {
		var w, h int32
		streamH264DecoderGetDimensions(d.handle, uintptr(unsafe.Pointer(&w)), uintptr(unsafe.Pointer(&h)))
		return int(w), int(h)
	}
	return d.width, d.height
}

// Close implements VideoDecoder.
func (d *H264Decoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle != 0 {
		streamH264DecoderDestroy(d.handle)
		d.handle = 0
	}

	return nil
}

// Register H.264 encoder and decoder
func init() {
	RegisterVideoEncoder(VideoCodecH264, func(config VideoEncoderConfig) (VideoEncoder, error) {
		return NewH264Encoder(config)
	})
	RegisterVideoDecoder(VideoCodecH264, func(config VideoDecoderConfig) (VideoDecoder, error) {
		return NewH264Decoder(config)
	})
}
