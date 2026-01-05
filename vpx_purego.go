//go:build (darwin || linux) && !novpx

// Package media provides VP8/VP9 codec support via libmedia_vpx using purego.
//
// This implementation uses purego to load libmedia_vpx dynamically at runtime,
// which is a thin wrapper around libvpx with a simple primitive-only API.
//
// Library locations checked (in order):
//   - MEDIA_VPX_LIB_PATH environment variable
//   - MEDIA_SDK_LIB_PATH environment variable (same as main FFI)
//   - build/ffi directory (development)
//   - System library paths

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
	mediaVPXOnce    sync.Once
	mediaVPXHandle  uintptr
	mediaVPXInitErr error
	mediaVPXLoaded  bool
)

// libmedia_vpx function pointers
var (
	mediaVPXEncoderCreate        func(codec, width, height, fps, bitrateKbps, threads int32) uint64
	mediaVPXEncoderCreateSVC     func(codec, width, height, fps, bitrateKbps, threads, temporalLayers, spatialLayers int32) uint64
	mediaVPXEncoderEncode        func(encoder uint64, yPlane, uPlane, vPlane uintptr, yStride, uvStride, forceKeyframe int32, outData uintptr, outCapacity int32, outFrameType, outPts uintptr) int32
	mediaVPXEncoderEncodeSVC     func(encoder uint64, yPlane, uPlane, vPlane uintptr, yStride, uvStride, forceKeyframe int32, outData uintptr, outCapacity int32, outFrameType, outPts, outTemporalLayer, outSpatialLayer uintptr) int32
	mediaVPXEncoderMaxOutputSize func(encoder uint64) int32
	mediaVPXEncoderRequestKF     func(encoder uint64)
	mediaVPXEncoderSetBitrate    func(encoder uint64, bitrateKbps int32) int32
	mediaVPXEncoderSetTemporal   func(encoder uint64, layers int32) int32
	mediaVPXEncoderSetSpatial    func(encoder uint64, layers int32) int32
	mediaVPXEncoderGetSVCConfig  func(encoder uint64, temporalLayers, spatialLayers, svcEnabled uintptr)
	mediaVPXEncoderGetStats      func(encoder uint64, framesEncoded, keyframesEncoded, bytesEncoded uintptr)
	mediaVPXEncoderDestroy       func(encoder uint64)

	mediaVPXDecoderCreate        func(codec, threads int32) uint64
	mediaVPXDecoderDecode        func(decoder uint64, data uintptr, dataLen int32, outY, outU, outV, outYStride, outUVStride, outWidth, outHeight uintptr) int32
	mediaVPXDecoderDecodeV2      func(decoder uint64, data uintptr, dataLen int32, resultOut uintptr) int32
	mediaVPXDecoderGetDimensions func(decoder uint64, width, height uintptr)
	mediaVPXDecoderGetStats      func(decoder uint64, framesDecoded, keyframesDecoded, bytesDecoded, corruptedFrames uintptr)
	mediaVPXDecoderReset         func(decoder uint64) int32
	mediaVPXDecoderDestroy       func(decoder uint64)

	mediaVPXGetError       func() uintptr
	mediaVPXCodecAvailable func(codec int32) int32
)

// mediaVPXDecodeResult matches media_vpx_decode_result_t in C
// This struct must be heap-allocated for purego to work correctly on arm64
type mediaVPXDecodeResult struct {
	YPtr     uint64 // Pointer to Y plane
	UPtr     uint64 // Pointer to U plane
	VPtr     uint64 // Pointer to V plane
	YStride  int32  // Y plane stride
	UVStride int32  // UV plane stride
	Width    int32  // Frame width
	Height   int32  // Frame height
	Result   int32  // 1=decoded, 0=buffering, <0=error
	Reserved int32  // Padding for alignment
}

// Constants from media_vpx.h
const (
	mediaVPXCodecVP8 = 0
	mediaVPXCodecVP9 = 1

	mediaVPXFrameKey   = 0
	mediaVPXFrameDelta = 1

	mediaVPXOK           = 0
	mediaVPXError        = -1
	mediaVPXErrorNoMem   = -2
	mediaVPXErrorInvalid = -3
	mediaVPXErrorCodec   = -4
)

// loadMediaVPX loads the libmedia_vpx shared library.
func loadMediaVPX() error {
	mediaVPXOnce.Do(func() {
		mediaVPXInitErr = loadMediaVPXLib()
		if mediaVPXInitErr == nil {
			mediaVPXLoaded = true
		}
	})
	return mediaVPXInitErr
}

func loadMediaVPXLib() error {
	paths := getMediaVPXLibPaths()

	var lastErr error
	for _, path := range paths {
		handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err == nil {
			mediaVPXHandle = handle
			if err := loadMediaVPXSymbols(); err != nil {
				purego.Dlclose(handle)
				lastErr = err
				continue
			}
			return nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return fmt.Errorf("failed to load libmedia_vpx: %w", lastErr)
	}
	return errors.New("libmedia_vpx not found in any standard location")
}

func getMediaVPXLibPaths() []string {
	var paths []string

	libName := "libmedia_vpx.so"
	if runtime.GOOS == "darwin" {
		libName = "libmedia_vpx.dylib"
	}

	// Environment variable overrides
	if envPath := os.Getenv("MEDIA_VPX_LIB_PATH"); envPath != "" {
		paths = append(paths, envPath)
	}
	if envPath := os.Getenv("MEDIA_SDK_LIB_PATH"); envPath != "" {
		paths = append(paths, filepath.Join(envPath, libName))
	}

	// Try to find based on executable location
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		paths = append(paths,
			filepath.Join(exeDir, libName),
			filepath.Join(exeDir, "..", "lib", libName),
			filepath.Join(exeDir, "..", "..", "build", "ffi", libName),
		)
	}

	// Try to find based on working directory
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

	// System paths
	switch runtime.GOOS {
	case "darwin":
		paths = append(paths,
			"libmedia_vpx.dylib",
			"/usr/local/lib/libmedia_vpx.dylib",
			"/opt/homebrew/lib/libmedia_vpx.dylib",
		)
	case "linux":
		paths = append(paths,
			"libmedia_vpx.so",
			"/usr/local/lib/libmedia_vpx.so",
			"/usr/lib/libmedia_vpx.so",
		)
	}

	return paths
}

func loadMediaVPXSymbols() error {
	// Encoder functions
	purego.RegisterLibFunc(&mediaVPXEncoderCreate, mediaVPXHandle, "media_vpx_encoder_create")
	purego.RegisterLibFunc(&mediaVPXEncoderCreateSVC, mediaVPXHandle, "media_vpx_encoder_create_svc")
	purego.RegisterLibFunc(&mediaVPXEncoderEncode, mediaVPXHandle, "media_vpx_encoder_encode")
	purego.RegisterLibFunc(&mediaVPXEncoderEncodeSVC, mediaVPXHandle, "media_vpx_encoder_encode_svc")
	purego.RegisterLibFunc(&mediaVPXEncoderMaxOutputSize, mediaVPXHandle, "media_vpx_encoder_max_output_size")
	purego.RegisterLibFunc(&mediaVPXEncoderRequestKF, mediaVPXHandle, "media_vpx_encoder_request_keyframe")
	purego.RegisterLibFunc(&mediaVPXEncoderSetBitrate, mediaVPXHandle, "media_vpx_encoder_set_bitrate")
	purego.RegisterLibFunc(&mediaVPXEncoderSetTemporal, mediaVPXHandle, "media_vpx_encoder_set_temporal_layers")
	purego.RegisterLibFunc(&mediaVPXEncoderSetSpatial, mediaVPXHandle, "media_vpx_encoder_set_spatial_layers")
	purego.RegisterLibFunc(&mediaVPXEncoderGetSVCConfig, mediaVPXHandle, "media_vpx_encoder_get_svc_config")
	purego.RegisterLibFunc(&mediaVPXEncoderGetStats, mediaVPXHandle, "media_vpx_encoder_get_stats")
	purego.RegisterLibFunc(&mediaVPXEncoderDestroy, mediaVPXHandle, "media_vpx_encoder_destroy")

	// Decoder functions
	purego.RegisterLibFunc(&mediaVPXDecoderCreate, mediaVPXHandle, "media_vpx_decoder_create")
	purego.RegisterLibFunc(&mediaVPXDecoderDecode, mediaVPXHandle, "media_vpx_decoder_decode")
	purego.RegisterLibFunc(&mediaVPXDecoderDecodeV2, mediaVPXHandle, "media_vpx_decoder_decode_v2")
	purego.RegisterLibFunc(&mediaVPXDecoderGetDimensions, mediaVPXHandle, "media_vpx_decoder_get_dimensions")
	purego.RegisterLibFunc(&mediaVPXDecoderGetStats, mediaVPXHandle, "media_vpx_decoder_get_stats")
	purego.RegisterLibFunc(&mediaVPXDecoderReset, mediaVPXHandle, "media_vpx_decoder_reset")
	purego.RegisterLibFunc(&mediaVPXDecoderDestroy, mediaVPXHandle, "media_vpx_decoder_destroy")

	// Utility functions
	purego.RegisterLibFunc(&mediaVPXGetError, mediaVPXHandle, "media_vpx_get_error")
	purego.RegisterLibFunc(&mediaVPXCodecAvailable, mediaVPXHandle, "media_vpx_codec_available")

	return nil
}

// IsVPXAvailable checks if libmedia_vpx is available.
func IsVPXAvailable() bool {
	if err := loadMediaVPX(); err != nil {
		return false
	}
	return mediaVPXLoaded
}

// IsVP8Available checks if VP8 codec is available.
func IsVP8Available() bool {
	if !IsVPXAvailable() {
		return false
	}
	return mediaVPXCodecAvailable(mediaVPXCodecVP8) != 0
}

// IsVP9Available checks if VP9 codec is available.
func IsVP9Available() bool {
	if !IsVPXAvailable() {
		return false
	}
	return mediaVPXCodecAvailable(mediaVPXCodecVP9) != 0
}

func getVPXError() string {
	ptr := mediaVPXGetError()
	if ptr == 0 {
		return "unknown error"
	}
	return goStringFromPtr(ptr)
}

// VPXEncoder implements VideoEncoder using libmedia_vpx via purego.
type VPXEncoder struct {
	config VideoEncoderConfig
	codec  VideoCodec

	handle       uint64
	outputBuf    []byte
	maxOutputLen int

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
	if err := loadMediaVPX(); err != nil {
		return nil, fmt.Errorf("%s encoder not available: %w", codec, err)
	}

	var codecType int32
	switch codec {
	case VideoCodecVP8:
		codecType = mediaVPXCodecVP8
	case VideoCodecVP9:
		codecType = mediaVPXCodecVP9
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

	var handle uint64
	svcEnabled := temporalLayers > 1 || spatialLayers > 1

	if svcEnabled {
		handle = mediaVPXEncoderCreateSVC(
			codecType,
			int32(config.Width),
			int32(config.Height),
			int32(fps),
			int32(bitrateKbps),
			int32(threads),
			int32(temporalLayers),
			int32(spatialLayers),
		)
	} else {
		handle = mediaVPXEncoderCreate(
			codecType,
			int32(config.Width),
			int32(config.Height),
			int32(fps),
			int32(bitrateKbps),
			int32(threads),
		)
	}

	if handle == 0 {
		return nil, fmt.Errorf("failed to create %s encoder: %s", codec, getVPXError())
	}

	maxOutput := mediaVPXEncoderMaxOutputSize(handle)
	if maxOutput <= 0 {
		maxOutput = int32(config.Width * config.Height * 3 / 2)
	}

	enc := &VPXEncoder{
		config:         config,
		codec:          codec,
		handle:         handle,
		outputBuf:      make([]byte, maxOutput),
		maxOutputLen:   int(maxOutput),
		svcEnabled:     svcEnabled,
		temporalLayers: temporalLayers,
		spatialLayers:  spatialLayers,
	}
	enc.keyframeReq.Store(true)

	return enc, nil
}

// Encode implements VideoEncoder.
func (e *VPXEncoder) Encode(frame *VideoFrame, buf []byte) (EncodeResult, error) {
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
	var pts int64
	var temporalLayer, spatialLayer int32
	var result int32

	if e.svcEnabled {
		result = mediaVPXEncoderEncodeSVC(
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
			uintptr(unsafe.Pointer(&temporalLayer)),
			uintptr(unsafe.Pointer(&spatialLayer)),
		)
	} else {
		result = mediaVPXEncoderEncode(
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
		)
	}

	if result < 0 {
		return EncodeResult{}, fmt.Errorf("encode failed: %s", getVPXError())
	}

	ft := FrameTypeDelta
	if frameType == mediaVPXFrameKey {
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
		DTS:       pts,
	}, nil
}

// MaxEncodedSize implements VideoEncoder.
func (e *VPXEncoder) MaxEncodedSize() int {
	return e.maxOutputLen
}

// Provider implements VideoEncoder.
func (e *VPXEncoder) Provider() Provider {
	return ProviderLibvpx
}

// RequestKeyframe implements VideoEncoder.
func (e *VPXEncoder) RequestKeyframe() {
	e.keyframeReq.Store(true)
	if e.handle != 0 {
		mediaVPXEncoderRequestKF(e.handle)
	}
}

// SetResolution implements VideoEncoder.
// VPX encoder does not currently support dynamic resolution change.
func (e *VPXEncoder) SetResolution(width, height int) error {
	return ErrNotSupported
}

// SetBitrate implements VideoEncoder.
func (e *VPXEncoder) SetBitrate(bitrateBps int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return fmt.Errorf("encoder not initialized")
	}

	bitrateKbps := int32(bitrateBps / 1000)
	if mediaVPXEncoderSetBitrate(e.handle, bitrateKbps) != mediaVPXOK {
		return fmt.Errorf("failed to set bitrate: %s", getVPXError())
	}

	e.config.BitrateBps = bitrateBps
	return nil
}

// SetSVCLayers sets both temporal and spatial layers at runtime in a single call.
// This is more efficient than calling SetTemporalLayers and SetSpatialLayers separately
// as it only triggers a single keyframe for the configuration change.
//
// Parameters:
//   - temporalLayers: 1-4 for VP9, 1-3 for VP8 (1 = disabled)
//   - spatialLayers: 1-3 for VP9 only (1 = disabled, VP8 ignores this)
//
// Returns error if layers are out of range or spatial layers requested for VP8.
func (e *VPXEncoder) SetSVCLayers(temporalLayers, spatialLayers int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return fmt.Errorf("encoder not initialized")
	}

	// Validate spatial layers for VP8
	if e.codec == VideoCodecVP8 && spatialLayers > 1 {
		return fmt.Errorf("spatial layers not supported for VP8")
	}

	// Set temporal layers first
	if mediaVPXEncoderSetTemporal(e.handle, int32(temporalLayers)) != mediaVPXOK {
		return fmt.Errorf("failed to set temporal layers: %s", getVPXError())
	}

	// Set spatial layers (VP9 only, but C wrapper handles VP8 gracefully)
	if e.codec == VideoCodecVP9 {
		if mediaVPXEncoderSetSpatial(e.handle, int32(spatialLayers)) != mediaVPXOK {
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
// Prefer SetSVCLayers if you need to change both temporal and spatial layers.
func (e *VPXEncoder) SetTemporalLayers(layers int) error {
	return e.SetSVCLayers(layers, e.spatialLayers)
}

// SetSpatialLayers sets the number of spatial layers at runtime (VP9 only).
// Prefer SetSVCLayers if you need to change both temporal and spatial layers.
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

	var tempLayers, spatLayers, enabled int32
	mediaVPXEncoderGetSVCConfig(e.handle,
		uintptr(unsafe.Pointer(&tempLayers)),
		uintptr(unsafe.Pointer(&spatLayers)),
		uintptr(unsafe.Pointer(&enabled)),
	)

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
		mediaVPXEncoderDestroy(e.handle)
		e.handle = 0
	}

	return nil
}

// VPXDecoder implements VideoDecoder using libmedia_vpx via purego.
type VPXDecoder struct {
	config VideoDecoderConfig
	codec  VideoCodec

	handle    uint64
	outputBuf *VideoFrameBuffer
	width     int
	height    int

	// Persistent output buffer for purego workaround on arm64
	// purego has issues with output pointer parameters, so we use heap-allocated struct
	// The struct layout must match media_vpx_decode_result_t in C exactly
	decodeResult *mediaVPXDecodeResult

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
	if err := loadMediaVPX(); err != nil {
		return nil, fmt.Errorf("%s decoder not available: %w", codec, err)
	}

	var codecType int32
	switch codec {
	case VideoCodecVP8:
		codecType = mediaVPXCodecVP8
	case VideoCodecVP9:
		codecType = mediaVPXCodecVP9
	default:
		return nil, fmt.Errorf("unsupported codec: %s", codec)
	}

	threads := int32(4)
	if config.Threads > 0 {
		threads = int32(config.Threads)
	}

	handle := mediaVPXDecoderCreate(codecType, threads)
	if handle == 0 {
		return nil, fmt.Errorf("failed to create %s decoder: %s", codec, getVPXError())
	}

	return &VPXDecoder{
		config:       config,
		codec:        codec,
		handle:       handle,
		decodeResult: &mediaVPXDecodeResult{}, // Heap-allocated for purego v2 API
	}, nil
}

// Decode implements VideoDecoder.
func (d *VPXDecoder) Decode(encoded *EncodedFrame) (*VideoFrame, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle == 0 {
		return nil, fmt.Errorf("decoder not initialized")
	}

	// Check for empty data before accessing slice
	if len(encoded.Data) == 0 {
		return nil, fmt.Errorf("empty encoded data")
	}

	// Use the v2 API which is more purego-friendly on arm64
	out := d.decodeResult

	result := mediaVPXDecoderDecodeV2(
		d.handle,
		uintptr(unsafe.Pointer(&encoded.Data[0])),
		int32(len(encoded.Data)),
		uintptr(unsafe.Pointer(out)),
	)

	runtime.KeepAlive(encoded.Data)
	// Keep the struct alive during and after the C call
	runtime.KeepAlive(out)

	if result < 0 {
		d.statsMu.Lock()
		d.stats.CorruptedFrames++
		d.statsMu.Unlock()
		return nil, fmt.Errorf("decode failed: %s", getVPXError())
	}

	if result == 0 {
		return nil, nil // Buffering, no frame yet
	}

	w := int(out.Width)
	h := int(out.Height)

	// Validate dimensions and strides
	if w <= 0 || h <= 0 || out.YPtr == 0 || out.YStride <= 0 || out.UVStride <= 0 {
		d.statsMu.Lock()
		d.stats.CorruptedFrames++
		d.statsMu.Unlock()
		return nil, fmt.Errorf("invalid decoder output: stride=%d/%d, size=%dx%d",
			out.YStride, out.UVStride, w, h)
	}

	d.width = w
	d.height = h

	// Allocate or reuse output buffer
	if d.outputBuf == nil || d.outputBuf.Width != w || d.outputBuf.Height != h {
		d.outputBuf = NewVideoFrameBuffer(w, h, PixelFormatI420)
		if d.outputBuf == nil {
			return nil, fmt.Errorf("failed to allocate output buffer")
		}
	}

	// Copy Y plane
	uvW := w / 2
	uvH := h / 2
	for row := 0; row < h; row++ {
		src := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(out.YPtr)+uintptr(row*int(out.YStride)))), w)
		dstStart := row * d.outputBuf.StrideY
		copy(d.outputBuf.Y[dstStart:dstStart+w], src)
	}

	// Copy U plane
	for row := 0; row < uvH; row++ {
		src := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(out.UPtr)+uintptr(row*int(out.UVStride)))), uvW)
		dstStart := row * d.outputBuf.StrideU
		copy(d.outputBuf.U[dstStart:dstStart+uvW], src)
	}

	// Copy V plane
	for row := 0; row < uvH; row++ {
		src := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(out.VPtr)+uintptr(row*int(out.UVStride)))), uvW)
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
func (d *VPXDecoder) DecodeInto(encoded *EncodedFrame, frame *VideoFrame) (DecodeResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle == 0 {
		return DecodeResult{}, fmt.Errorf("decoder not initialized")
	}

	// Use the v2 API which is more purego-friendly on arm64
	out := d.decodeResult

	result := mediaVPXDecoderDecodeV2(
		d.handle,
		uintptr(unsafe.Pointer(&encoded.Data[0])),
		int32(len(encoded.Data)),
		uintptr(unsafe.Pointer(out)),
	)

	runtime.KeepAlive(encoded.Data)
	// Keep the struct alive during and after the C call
	runtime.KeepAlive(out)

	if result < 0 {
		d.statsMu.Lock()
		d.stats.CorruptedFrames++
		d.statsMu.Unlock()
		return DecodeResult{}, fmt.Errorf("decode failed: %s", getVPXError())
	}

	if result == 0 {
		return DecodeResult{}, nil // Buffering
	}

	w := int(out.Width)
	h := int(out.Height)

	// Validate dimensions - if C returned success but dimensions are 0, treat as buffering
	if w == 0 || h == 0 || out.YPtr == 0 {
		return DecodeResult{}, nil
	}

	d.width = w
	d.height = h

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
		src := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(out.YPtr)+uintptr(row*int(out.YStride)))), w)
		dstStart := row * strideY
		copy(frame.Data[0][dstStart:dstStart+w], src)
	}

	for row := 0; row < uvH; row++ {
		src := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(out.UPtr)+uintptr(row*int(out.UVStride)))), uvW)
		dstStart := row * strideUV
		copy(frame.Data[1][dstStart:dstStart+uvW], src)
	}

	for row := 0; row < uvH; row++ {
		src := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(out.VPtr)+uintptr(row*int(out.UVStride)))), uvW)
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
func (d *VPXDecoder) DecodeRTP(data []byte, marker bool, timestamp uint32) (*VideoFrame, error) {
	encoded := &EncodedFrame{
		Data:      data,
		Timestamp: timestamp,
	}
	return d.Decode(encoded)
}

// Provider implements VideoDecoder.
func (d *VPXDecoder) Provider() Provider {
	return ProviderLibvpx
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

	if mediaVPXDecoderReset(d.handle) != mediaVPXOK {
		return fmt.Errorf("failed to reset decoder: %s", getVPXError())
	}

	return nil
}

// GetDimensions implements VideoDecoder.
func (d *VPXDecoder) GetDimensions() (width, height int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle != 0 {
		var w, h int32
		mediaVPXDecoderGetDimensions(d.handle, uintptr(unsafe.Pointer(&w)), uintptr(unsafe.Pointer(&h)))
		return int(w), int(h)
	}
	return d.width, d.height
}

// Close implements VideoDecoder.
func (d *VPXDecoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle != 0 {
		mediaVPXDecoderDestroy(d.handle)
		d.handle = 0
	}

	return nil
}

// Register VP8/VP9 encoders and decoders (libvpx)
func init() {
	if err := loadMediaVPX(); err != nil {
		return
	}

	// Check VP8 availability
	if mediaVPXCodecAvailable(mediaVPXCodecVP8) != 0 {
		setProviderAvailable(ProviderLibvpx)
		registerVideoEncoder(VideoCodecVP8, ProviderLibvpx, func(config VideoEncoderConfig) (VideoEncoder, error) {
			return NewVP8Encoder(config)
		})
		registerVideoDecoder(VideoCodecVP8, ProviderLibvpx, func(config VideoDecoderConfig) (VideoDecoder, error) {
			return NewVP8Decoder(config)
		})
	}

	// Check VP9 availability
	if mediaVPXCodecAvailable(mediaVPXCodecVP9) != 0 {
		setProviderAvailable(ProviderLibvpx)
		registerVideoEncoder(VideoCodecVP9, ProviderLibvpx, func(config VideoEncoderConfig) (VideoEncoder, error) {
			return NewVP9Encoder(config)
		})
		registerVideoDecoder(VideoCodecVP9, ProviderLibvpx, func(config VideoDecoderConfig) (VideoDecoder, error) {
			return NewVP9Decoder(config)
		})
	}
}
