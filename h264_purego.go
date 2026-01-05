//go:build (darwin || linux) && !noh264

// Package media provides H.264 codec support via libmedia_h264 using purego.

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
	mediaH264Once    sync.Once
	mediaH264Handle  uintptr
	mediaH264InitErr error
	mediaH264Loaded  bool
)

// libmedia_h264 function pointers
var (
	mediaH264EncoderCreate       func(width, height, fps, bitrateKbps, profile, threads int32) uint64
	mediaH264EncoderEncode       func(encoder uint64, yPlane, uPlane, vPlane uintptr, yStride, uvStride, forceKeyframe int32, outData uintptr, outCapacity int32, outFrameType, outPts, outDts uintptr) int32
	mediaH264EncoderMaxOutputSize func(encoder uint64) int32
	mediaH264EncoderRequestKF    func(encoder uint64)
	mediaH264EncoderSetBitrate   func(encoder uint64, bitrateKbps int32) int32
	mediaH264EncoderGetSPSPPS    func(encoder uint64, spsOut uintptr, spsCapacity int32, spsLen uintptr, ppsOut uintptr, ppsCapacity int32, ppsLen uintptr) int32
	mediaH264EncoderGetStats     func(encoder uint64, framesEncoded, keyframesEncoded, bytesEncoded uintptr)
	mediaH264EncoderDestroy      func(encoder uint64)

	mediaH264DecoderCreate        func(threads int32) uint64
	mediaH264DecoderDecode        func(decoder uint64, data uintptr, dataLen int32, outY, outU, outV, outYStride, outUVStride, outWidth, outHeight uintptr) int32
	mediaH264DecoderGetDimensions func(decoder uint64, width, height uintptr)
	mediaH264DecoderGetStats      func(decoder uint64, framesDecoded, keyframesDecoded, bytesDecoded, corruptedFrames uintptr)
	mediaH264DecoderReset         func(decoder uint64) int32
	mediaH264DecoderDestroy       func(decoder uint64)

	mediaH264GetError         func() uintptr
	mediaH264EncoderAvailable func() int32
	mediaH264DecoderAvailable func() int32
)

// Constants from media_h264.h
const (
	mediaH264ProfileBaseline = 66
	mediaH264ProfileMain     = 77
	mediaH264ProfileHigh     = 100

	mediaH264FrameI   = 0
	mediaH264FrameP   = 1
	mediaH264FrameB   = 2
	mediaH264FrameIDR = 3

	mediaH264OK           = 0
	mediaH264Error        = -1
	mediaH264ErrorNoMem   = -2
	mediaH264ErrorInvalid = -3
	mediaH264ErrorCodec   = -4
)

// mediaH264DecodeResult is a heap-allocated struct for decoder output parameters.
// This struct must be heap-allocated for purego to work correctly on arm64.
// Using local stack variables for output parameters can fail due to GC moving
// the stack during the C call.
type mediaH264DecodeResult struct {
	YPtr     uintptr // Pointer to Y plane
	UPtr     uintptr // Pointer to U plane
	VPtr     uintptr // Pointer to V plane
	YStride  int32   // Y plane stride
	UVStride int32   // UV plane stride
	Width    int32   // Frame width
	Height   int32   // Frame height
}

// h264ProfileToStreamPurego converts H264Profile to media_h264 profile constant.
func h264ProfileToStreamPurego(p H264Profile) int {
	switch p {
	case H264ProfileMain:
		return mediaH264ProfileMain
	case H264ProfileHigh:
		return mediaH264ProfileHigh
	default:
		return mediaH264ProfileBaseline
	}
}

func loadMediaH264() error {
	mediaH264Once.Do(func() {
		mediaH264InitErr = loadMediaH264Lib()
		if mediaH264InitErr == nil {
			mediaH264Loaded = true
		}
	})
	return mediaH264InitErr
}

func loadMediaH264Lib() error {
	paths := getMediaH264LibPaths()

	var lastErr error
	for _, path := range paths {
		handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err == nil {
			mediaH264Handle = handle
			if err := loadMediaH264Symbols(); err != nil {
				purego.Dlclose(handle)
				lastErr = err
				continue
			}
			return nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return fmt.Errorf("failed to load libmedia_h264: %w", lastErr)
	}
	return errors.New("libmedia_h264 not found in any standard location")
}

func getMediaH264LibPaths() []string {
	var paths []string

	libName := "libmedia_h264.so"
	if runtime.GOOS == "darwin" {
		libName = "libmedia_h264.dylib"
	}

	// Environment variable overrides (highest priority)
	if envPath := os.Getenv("MEDIA_H264_LIB_PATH"); envPath != "" {
		paths = append(paths, envPath)
	}
	if envPath := os.Getenv("MEDIA_SDK_LIB_PATH"); envPath != "" {
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
			"libmedia_h264.dylib",
			"/usr/local/lib/libmedia_h264.dylib",
			"/opt/homebrew/lib/libmedia_h264.dylib",
		)
	case "linux":
		paths = append(paths,
			"libmedia_h264.so",
			"/usr/local/lib/libmedia_h264.so",
			"/usr/lib/libmedia_h264.so",
		)
	}

	return paths
}

func loadMediaH264Symbols() error {
	purego.RegisterLibFunc(&mediaH264EncoderCreate, mediaH264Handle, "media_h264_encoder_create")
	purego.RegisterLibFunc(&mediaH264EncoderEncode, mediaH264Handle, "media_h264_encoder_encode")
	purego.RegisterLibFunc(&mediaH264EncoderMaxOutputSize, mediaH264Handle, "media_h264_encoder_max_output_size")
	purego.RegisterLibFunc(&mediaH264EncoderRequestKF, mediaH264Handle, "media_h264_encoder_request_keyframe")
	purego.RegisterLibFunc(&mediaH264EncoderSetBitrate, mediaH264Handle, "media_h264_encoder_set_bitrate")
	purego.RegisterLibFunc(&mediaH264EncoderGetSPSPPS, mediaH264Handle, "media_h264_encoder_get_sps_pps")
	purego.RegisterLibFunc(&mediaH264EncoderGetStats, mediaH264Handle, "media_h264_encoder_get_stats")
	purego.RegisterLibFunc(&mediaH264EncoderDestroy, mediaH264Handle, "media_h264_encoder_destroy")

	purego.RegisterLibFunc(&mediaH264DecoderCreate, mediaH264Handle, "media_h264_decoder_create")
	purego.RegisterLibFunc(&mediaH264DecoderDecode, mediaH264Handle, "media_h264_decoder_decode")
	purego.RegisterLibFunc(&mediaH264DecoderGetDimensions, mediaH264Handle, "media_h264_decoder_get_dimensions")
	purego.RegisterLibFunc(&mediaH264DecoderGetStats, mediaH264Handle, "media_h264_decoder_get_stats")
	purego.RegisterLibFunc(&mediaH264DecoderReset, mediaH264Handle, "media_h264_decoder_reset")
	purego.RegisterLibFunc(&mediaH264DecoderDestroy, mediaH264Handle, "media_h264_decoder_destroy")

	purego.RegisterLibFunc(&mediaH264GetError, mediaH264Handle, "media_h264_get_error")
	purego.RegisterLibFunc(&mediaH264EncoderAvailable, mediaH264Handle, "media_h264_encoder_available")
	purego.RegisterLibFunc(&mediaH264DecoderAvailable, mediaH264Handle, "media_h264_decoder_available")

	return nil
}

// IsH264Available checks if libmedia_h264 is available.
func IsH264Available() bool {
	if err := loadMediaH264(); err != nil {
		return false
	}
	return mediaH264Loaded
}

// IsH264EncoderAvailable checks if H.264 encoder is available.
func IsH264EncoderAvailable() bool {
	if !IsH264Available() {
		return false
	}
	return mediaH264EncoderAvailable() != 0
}

// IsH264DecoderAvailable checks if H.264 decoder is available.
func IsH264DecoderAvailable() bool {
	if !IsH264Available() {
		return false
	}
	return mediaH264DecoderAvailable() != 0
}

func getH264Error() string {
	ptr := mediaH264GetError()
	if ptr == 0 {
		return "unknown error"
	}
	return goStringFromPtr(ptr)
}

// H264Encoder implements VideoEncoder for H.264 (x264).
type H264Encoder struct {
	config VideoEncoderConfig

	handle       uint64
	outputBuf    []byte
	maxOutputLen int
	profile      H264Profile

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
	if err := loadMediaH264(); err != nil {
		return nil, fmt.Errorf("H.264 encoder not available: %w", err)
	}

	if mediaH264EncoderAvailable() == 0 {
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

	handle := mediaH264EncoderCreate(
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

	maxOutput := mediaH264EncoderMaxOutputSize(handle)
	if maxOutput <= 0 {
		maxOutput = int32(config.Width * config.Height * 3 / 2)
	}

	enc := &H264Encoder{
		config:       config,
		handle:       handle,
		outputBuf:    make([]byte, maxOutput),
		maxOutputLen: int(maxOutput),
		profile:      profile,
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

	mediaH264EncoderGetSPSPPS(
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

	result := mediaH264EncoderEncode(
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
	if frameType == mediaH264FrameIDR || frameType == mediaH264FrameI {
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

// EncodeInto implements VideoEncoder. Zero-allocation encode into provided buffer.
func (e *H264Encoder) EncodeInto(frame *VideoFrame, buf []byte) (EncodeResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return EncodeResult{}, fmt.Errorf("encoder not initialized")
	}

	if len(buf) < e.maxOutputLen {
		return EncodeResult{}, ErrBufferTooSmall
	}

	forceKeyframe := int32(0)
	if e.keyframeReq.Swap(false) {
		forceKeyframe = 1
	}

	var frameType int32
	var pts, dts int64

	result := mediaH264EncoderEncode(
		e.handle,
		uintptr(unsafe.Pointer(&frame.Data[0][0])),
		uintptr(unsafe.Pointer(&frame.Data[1][0])),
		uintptr(unsafe.Pointer(&frame.Data[2][0])),
		int32(frame.Stride[0]),
		int32(frame.Stride[1]),
		forceKeyframe,
		uintptr(unsafe.Pointer(&buf[0])),
		int32(len(buf)),
		uintptr(unsafe.Pointer(&frameType)),
		uintptr(unsafe.Pointer(&pts)),
		uintptr(unsafe.Pointer(&dts)),
	)

	if result < 0 {
		return EncodeResult{}, fmt.Errorf("encode failed: %s", getH264Error())
	}

	ft := FrameTypeDelta
	if frameType == mediaH264FrameIDR || frameType == mediaH264FrameI {
		ft = FrameTypeKey
	}

	e.statsMu.Lock()
	e.stats.FramesEncoded++
	if ft == FrameTypeKey {
		e.stats.KeyframesEncoded++
	}
	e.stats.BytesEncoded += uint64(result)
	e.statsMu.Unlock()

	return EncodeResult{
		N:         int(result),
		FrameType: ft,
		PTS:       pts,
		DTS:       dts,
	}, nil
}

// MaxEncodedSize implements VideoEncoder.
func (e *H264Encoder) MaxEncodedSize() int {
	return e.maxOutputLen
}

// RequestKeyframe implements VideoEncoder.
func (e *H264Encoder) RequestKeyframe() {
	e.keyframeReq.Store(true)
	if e.handle != 0 {
		mediaH264EncoderRequestKF(e.handle)
	}
}

// Provider implements VideoEncoder.
func (e *H264Encoder) Provider() Provider {
	return ProviderX264
}

// SetResolution implements VideoEncoder.
// H264 encoder does not support dynamic resolution change.
func (e *H264Encoder) SetResolution(width, height int) error {
	return ErrNotSupported
}

// SetBitrate implements VideoEncoder.
func (e *H264Encoder) SetBitrate(bitrateBps int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return fmt.Errorf("encoder not initialized")
	}

	bitrateKbps := int32(bitrateBps / 1000)
	if mediaH264EncoderSetBitrate(e.handle, bitrateKbps) != mediaH264OK {
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
		mediaH264EncoderDestroy(e.handle)
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

	// Persistent output buffer for purego workaround on arm64
	// purego has issues with output pointer parameters, so we use heap-allocated struct
	decodeResult *mediaH264DecodeResult

	stats   DecoderStats
	statsMu sync.Mutex
	mu      sync.Mutex
}

// NewH264Decoder creates a new H.264 decoder.
func NewH264Decoder(config VideoDecoderConfig) (*H264Decoder, error) {
	if err := loadMediaH264(); err != nil {
		return nil, fmt.Errorf("H.264 decoder not available: %w", err)
	}

	if mediaH264DecoderAvailable() == 0 {
		return nil, errors.New("H.264 decoder not available")
	}

	threads := int32(4)
	if config.Threads > 0 {
		threads = int32(config.Threads)
	}

	handle := mediaH264DecoderCreate(threads)
	if handle == 0 {
		return nil, fmt.Errorf("failed to create H.264 decoder: %s", getH264Error())
	}

	return &H264Decoder{
		config:       config,
		handle:       handle,
		decodeResult: &mediaH264DecodeResult{}, // Heap-allocated for purego arm64
	}, nil
}

// Decode implements VideoDecoder.
func (d *H264Decoder) Decode(encoded *EncodedFrame) (*VideoFrame, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle == 0 {
		return nil, fmt.Errorf("decoder not initialized")
	}

	// Check for empty data before accessing slice
	if len(encoded.Data) == 0 {
		return nil, fmt.Errorf("empty encoded data")
	}

	// Use heap-allocated struct for output parameters
	// This is required on arm64 because purego/GC can move stack variables during C calls
	out := d.decodeResult

	result := mediaH264DecoderDecode(
		d.handle,
		uintptr(unsafe.Pointer(&encoded.Data[0])),
		int32(len(encoded.Data)),
		uintptr(unsafe.Pointer(&out.YPtr)),
		uintptr(unsafe.Pointer(&out.UPtr)),
		uintptr(unsafe.Pointer(&out.VPtr)),
		uintptr(unsafe.Pointer(&out.YStride)),
		uintptr(unsafe.Pointer(&out.UVStride)),
		uintptr(unsafe.Pointer(&out.Width)),
		uintptr(unsafe.Pointer(&out.Height)),
	)

	// Keep the struct and input alive during and after the C call
	runtime.KeepAlive(encoded.Data)
	runtime.KeepAlive(out)

	if result < 0 {
		d.statsMu.Lock()
		d.stats.CorruptedFrames++
		d.statsMu.Unlock()
		return nil, fmt.Errorf("decode failed: %s", getH264Error())
	}

	if result == 0 {
		return nil, nil
	}

	// Validate output dimensions, strides, and plane pointers
	if out.YStride <= 0 || out.UVStride <= 0 || out.Width <= 0 || out.Height <= 0 || out.YPtr == 0 {
		d.statsMu.Lock()
		d.stats.CorruptedFrames++
		d.statsMu.Unlock()
		return nil, fmt.Errorf("invalid decoder output: stride=%d/%d, size=%dx%d",
			out.YStride, out.UVStride, out.Width, out.Height)
	}

	w := int(out.Width)
	h := int(out.Height)
	d.width = w
	d.height = h

	if d.outputBuf == nil || d.outputBuf.Width != w || d.outputBuf.Height != h {
		d.outputBuf = NewVideoFrameBuffer(w, h, PixelFormatI420)
		if d.outputBuf == nil {
			return nil, fmt.Errorf("failed to allocate output buffer")
		}
	}

	uvW := w / 2
	uvH := h / 2
	for row := 0; row < h; row++ {
		src := unsafe.Slice((*byte)(unsafe.Pointer(out.YPtr+uintptr(row*int(out.YStride)))), w)
		dstStart := row * d.outputBuf.StrideY
		copy(d.outputBuf.Y[dstStart:dstStart+w], src)
	}

	for row := 0; row < uvH; row++ {
		src := unsafe.Slice((*byte)(unsafe.Pointer(out.UPtr+uintptr(row*int(out.UVStride)))), uvW)
		dstStart := row * d.outputBuf.StrideU
		copy(d.outputBuf.U[dstStart:dstStart+uvW], src)
	}

	for row := 0; row < uvH; row++ {
		src := unsafe.Slice((*byte)(unsafe.Pointer(out.VPtr+uintptr(row*int(out.UVStride)))), uvW)
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

// DecodeInto implements VideoDecoder. Zero-allocation decode into provided frame.
func (d *H264Decoder) DecodeInto(encoded *EncodedFrame, frame *VideoFrame) (DecodeResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle == 0 {
		return DecodeResult{}, fmt.Errorf("decoder not initialized")
	}

	// Use heap-allocated struct for output parameters
	out := d.decodeResult

	result := mediaH264DecoderDecode(
		d.handle,
		uintptr(unsafe.Pointer(&encoded.Data[0])),
		int32(len(encoded.Data)),
		uintptr(unsafe.Pointer(&out.YPtr)),
		uintptr(unsafe.Pointer(&out.UPtr)),
		uintptr(unsafe.Pointer(&out.VPtr)),
		uintptr(unsafe.Pointer(&out.YStride)),
		uintptr(unsafe.Pointer(&out.UVStride)),
		uintptr(unsafe.Pointer(&out.Width)),
		uintptr(unsafe.Pointer(&out.Height)),
	)

	runtime.KeepAlive(encoded.Data)
	runtime.KeepAlive(out)

	if result < 0 {
		d.statsMu.Lock()
		d.stats.CorruptedFrames++
		d.statsMu.Unlock()
		return DecodeResult{}, fmt.Errorf("decode failed: %s", getH264Error())
	}

	if result == 0 {
		return DecodeResult{}, nil // No output ready (buffering)
	}

	w := int(out.Width)
	h := int(out.Height)
	d.width = w
	d.height = h

	// Copy into provided frame
	uvW := w / 2
	uvH := h / 2
	strideY := w
	strideUV := uvW

	// Ensure frame has allocated buffers
	if len(frame.Data[0]) < strideY*h {
		return DecodeResult{}, ErrBufferTooSmall
	}
	if len(frame.Data[1]) < strideUV*uvH || len(frame.Data[2]) < strideUV*uvH {
		return DecodeResult{}, ErrBufferTooSmall
	}

	frame.Width = w
	frame.Height = h
	frame.Stride[0] = strideY
	frame.Stride[1] = strideUV
	frame.Stride[2] = strideUV

	for row := 0; row < h; row++ {
		src := unsafe.Slice((*byte)(unsafe.Pointer(out.YPtr+uintptr(row*int(out.YStride)))), w)
		dstStart := row * strideY
		copy(frame.Data[0][dstStart:dstStart+w], src)
	}

	for row := 0; row < uvH; row++ {
		src := unsafe.Slice((*byte)(unsafe.Pointer(out.UPtr+uintptr(row*int(out.UVStride)))), uvW)
		dstStart := row * strideUV
		copy(frame.Data[1][dstStart:dstStart+uvW], src)
	}

	for row := 0; row < uvH; row++ {
		src := unsafe.Slice((*byte)(unsafe.Pointer(out.VPtr+uintptr(row*int(out.UVStride)))), uvW)
		dstStart := row * strideUV
		copy(frame.Data[2][dstStart:dstStart+uvW], src)
	}

	ft := FrameTypeDelta
	if encoded.FrameType == FrameTypeKey {
		ft = FrameTypeKey
	}

	d.statsMu.Lock()
	d.stats.FramesDecoded++
	d.stats.BytesDecoded += uint64(len(encoded.Data))
	if ft == FrameTypeKey {
		d.stats.KeyframesDecoded++
	}
	d.statsMu.Unlock()

	return DecodeResult{
		Width:     w,
		Height:    h,
		FrameType: ft,
		PTS:       int64(encoded.Timestamp),
	}, nil
}

// DecodeRTP implements VideoDecoder.
func (d *H264Decoder) DecodeRTP(data []byte, marker bool, timestamp uint32) (*VideoFrame, error) {
	encoded := &EncodedFrame{
		Data:      data,
		Timestamp: timestamp,
	}
	return d.Decode(encoded)
}

// Provider implements VideoDecoder.
func (d *H264Decoder) Provider() Provider {
	return ProviderOpenH264 // H264Decoder uses OpenH264 (BSD)
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

	if mediaH264DecoderReset(d.handle) != mediaH264OK {
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
		mediaH264DecoderGetDimensions(d.handle, uintptr(unsafe.Pointer(&w)), uintptr(unsafe.Pointer(&h)))
		return int(w), int(h)
	}
	return d.width, d.height
}

// Close implements VideoDecoder.
func (d *H264Decoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle != 0 {
		mediaH264DecoderDestroy(d.handle)
		d.handle = 0
	}

	return nil
}

// Register H.264 encoder and decoder (x264)
func init() {
	// Check if x264 is available
	if err := loadMediaH264(); err == nil && mediaH264EncoderAvailable() != 0 {
		setProviderAvailable(ProviderX264)
		registerVideoEncoder(VideoCodecH264, ProviderX264, func(config VideoEncoderConfig) (VideoEncoder, error) {
			return NewH264Encoder(config)
		})
	}

	// Decoder uses OpenH264 (BSD), so register as OpenH264
	if err := loadMediaH264(); err == nil && mediaH264DecoderAvailable() != 0 {
		setProviderAvailable(ProviderOpenH264)
		registerVideoDecoder(VideoCodecH264, ProviderOpenH264, func(config VideoDecoderConfig) (VideoDecoder, error) {
			return NewH264Decoder(config)
		})
	}
}
