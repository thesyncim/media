//go:build (darwin || linux) && !novpx && !cgo

// Package media provides VP8/VP9 codec support via libstream_vpx using purego.
//
// This implementation uses purego to load libstream_vpx dynamically at runtime,
// which is a thin wrapper around libvpx with a simple primitive-only API.
//
// Library locations checked (in order):
//   - STREAM_VPX_LIB_PATH environment variable
//   - STREAM_SDK_LIB_PATH environment variable (same as main FFI)
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
	streamVPXOnce    sync.Once
	streamVPXHandle  uintptr
	streamVPXInitErr error
	streamVPXLoaded  bool
)

// libstream_vpx function pointers
var (
	streamVPXEncoderCreate        func(codec, width, height, fps, bitrateKbps, threads int32) uint64
	streamVPXEncoderCreateSVC     func(codec, width, height, fps, bitrateKbps, threads, temporalLayers, spatialLayers int32) uint64
	streamVPXEncoderEncode        func(encoder uint64, yPlane, uPlane, vPlane uintptr, yStride, uvStride, forceKeyframe int32, outData uintptr, outCapacity int32, outFrameType, outPts uintptr) int32
	streamVPXEncoderEncodeSVC     func(encoder uint64, yPlane, uPlane, vPlane uintptr, yStride, uvStride, forceKeyframe int32, outData uintptr, outCapacity int32, outFrameType, outPts, outTemporalLayer, outSpatialLayer uintptr) int32
	streamVPXEncoderMaxOutputSize func(encoder uint64) int32
	streamVPXEncoderRequestKF     func(encoder uint64)
	streamVPXEncoderSetBitrate    func(encoder uint64, bitrateKbps int32) int32
	streamVPXEncoderSetTemporal   func(encoder uint64, layers int32) int32
	streamVPXEncoderSetSpatial    func(encoder uint64, layers int32) int32
	streamVPXEncoderGetSVCConfig  func(encoder uint64, temporalLayers, spatialLayers, svcEnabled uintptr)
	streamVPXEncoderGetStats      func(encoder uint64, framesEncoded, keyframesEncoded, bytesEncoded uintptr)
	streamVPXEncoderDestroy       func(encoder uint64)

	streamVPXDecoderCreate        func(codec, threads int32) uint64
	streamVPXDecoderDecode        func(decoder uint64, data uintptr, dataLen int32, outY, outU, outV, outYStride, outUVStride, outWidth, outHeight uintptr) int32
	streamVPXDecoderGetDimensions func(decoder uint64, width, height uintptr)
	streamVPXDecoderGetStats      func(decoder uint64, framesDecoded, keyframesDecoded, bytesDecoded, corruptedFrames uintptr)
	streamVPXDecoderReset         func(decoder uint64) int32
	streamVPXDecoderDestroy       func(decoder uint64)

	streamVPXGetError       func() uintptr
	streamVPXCodecAvailable func(codec int32) int32
)

// Constants from stream_vpx.h
const (
	streamVPXCodecVP8 = 0
	streamVPXCodecVP9 = 1

	streamVPXFrameKey   = 0
	streamVPXFrameDelta = 1

	streamVPXOK           = 0
	streamVPXError        = -1
	streamVPXErrorNoMem   = -2
	streamVPXErrorInvalid = -3
	streamVPXErrorCodec   = -4
)

// loadStreamVPX loads the libstream_vpx shared library.
func loadStreamVPX() error {
	streamVPXOnce.Do(func() {
		streamVPXInitErr = loadStreamVPXLib()
		if streamVPXInitErr == nil {
			streamVPXLoaded = true
		}
	})
	return streamVPXInitErr
}

func loadStreamVPXLib() error {
	paths := getStreamVPXLibPaths()

	var lastErr error
	for _, path := range paths {
		handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err == nil {
			streamVPXHandle = handle
			if err := loadStreamVPXSymbols(); err != nil {
				purego.Dlclose(handle)
				lastErr = err
				continue
			}
			return nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return fmt.Errorf("failed to load libstream_vpx: %w", lastErr)
	}
	return errors.New("libstream_vpx not found in any standard location")
}

func getStreamVPXLibPaths() []string {
	var paths []string

	libName := "libstream_vpx.so"
	if runtime.GOOS == "darwin" {
		libName = "libstream_vpx.dylib"
	}

	// Environment variable overrides
	if envPath := os.Getenv("STREAM_VPX_LIB_PATH"); envPath != "" {
		paths = append(paths, envPath)
	}
	if envPath := os.Getenv("STREAM_SDK_LIB_PATH"); envPath != "" {
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
			"libstream_vpx.dylib",
			"/usr/local/lib/libstream_vpx.dylib",
			"/opt/homebrew/lib/libstream_vpx.dylib",
		)
	case "linux":
		paths = append(paths,
			"libstream_vpx.so",
			"/usr/local/lib/libstream_vpx.so",
			"/usr/lib/libstream_vpx.so",
		)
	}

	return paths
}

func loadStreamVPXSymbols() error {
	// Encoder functions
	purego.RegisterLibFunc(&streamVPXEncoderCreate, streamVPXHandle, "stream_vpx_encoder_create")
	purego.RegisterLibFunc(&streamVPXEncoderCreateSVC, streamVPXHandle, "stream_vpx_encoder_create_svc")
	purego.RegisterLibFunc(&streamVPXEncoderEncode, streamVPXHandle, "stream_vpx_encoder_encode")
	purego.RegisterLibFunc(&streamVPXEncoderEncodeSVC, streamVPXHandle, "stream_vpx_encoder_encode_svc")
	purego.RegisterLibFunc(&streamVPXEncoderMaxOutputSize, streamVPXHandle, "stream_vpx_encoder_max_output_size")
	purego.RegisterLibFunc(&streamVPXEncoderRequestKF, streamVPXHandle, "stream_vpx_encoder_request_keyframe")
	purego.RegisterLibFunc(&streamVPXEncoderSetBitrate, streamVPXHandle, "stream_vpx_encoder_set_bitrate")
	purego.RegisterLibFunc(&streamVPXEncoderSetTemporal, streamVPXHandle, "stream_vpx_encoder_set_temporal_layers")
	purego.RegisterLibFunc(&streamVPXEncoderSetSpatial, streamVPXHandle, "stream_vpx_encoder_set_spatial_layers")
	purego.RegisterLibFunc(&streamVPXEncoderGetSVCConfig, streamVPXHandle, "stream_vpx_encoder_get_svc_config")
	purego.RegisterLibFunc(&streamVPXEncoderGetStats, streamVPXHandle, "stream_vpx_encoder_get_stats")
	purego.RegisterLibFunc(&streamVPXEncoderDestroy, streamVPXHandle, "stream_vpx_encoder_destroy")

	// Decoder functions
	purego.RegisterLibFunc(&streamVPXDecoderCreate, streamVPXHandle, "stream_vpx_decoder_create")
	purego.RegisterLibFunc(&streamVPXDecoderDecode, streamVPXHandle, "stream_vpx_decoder_decode")
	purego.RegisterLibFunc(&streamVPXDecoderGetDimensions, streamVPXHandle, "stream_vpx_decoder_get_dimensions")
	purego.RegisterLibFunc(&streamVPXDecoderGetStats, streamVPXHandle, "stream_vpx_decoder_get_stats")
	purego.RegisterLibFunc(&streamVPXDecoderReset, streamVPXHandle, "stream_vpx_decoder_reset")
	purego.RegisterLibFunc(&streamVPXDecoderDestroy, streamVPXHandle, "stream_vpx_decoder_destroy")

	// Utility functions
	purego.RegisterLibFunc(&streamVPXGetError, streamVPXHandle, "stream_vpx_get_error")
	purego.RegisterLibFunc(&streamVPXCodecAvailable, streamVPXHandle, "stream_vpx_codec_available")

	return nil
}

// IsVPXAvailable checks if libstream_vpx is available.
func IsVPXAvailable() bool {
	if err := loadStreamVPX(); err != nil {
		return false
	}
	return streamVPXLoaded
}

// IsVP8Available checks if VP8 codec is available.
func IsVP8Available() bool {
	if !IsVPXAvailable() {
		return false
	}
	return streamVPXCodecAvailable(streamVPXCodecVP8) != 0
}

// IsVP9Available checks if VP9 codec is available.
func IsVP9Available() bool {
	if !IsVPXAvailable() {
		return false
	}
	return streamVPXCodecAvailable(streamVPXCodecVP9) != 0
}

func getVPXError() string {
	ptr := streamVPXGetError()
	if ptr == 0 {
		return "unknown error"
	}
	return goStringFromPtr(ptr)
}

// VPXEncoder implements VideoEncoder using libstream_vpx via purego.
type VPXEncoder struct {
	config VideoEncoderConfig
	codec  VideoCodec

	handle    uint64
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
	if err := loadStreamVPX(); err != nil {
		return nil, fmt.Errorf("%s encoder not available: %w", codec, err)
	}

	var codecType int32
	switch codec {
	case VideoCodecVP8:
		codecType = streamVPXCodecVP8
	case VideoCodecVP9:
		codecType = streamVPXCodecVP9
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
		handle = streamVPXEncoderCreateSVC(
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
		handle = streamVPXEncoderCreate(
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

	maxOutput := streamVPXEncoderMaxOutputSize(handle)
	if maxOutput <= 0 {
		maxOutput = int32(config.Width * config.Height * 3 / 2)
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

	forceKeyframe := int32(0)
	if e.keyframeReq.Swap(false) {
		forceKeyframe = 1
	}

	var frameType int32
	var pts int64
	var temporalLayer, spatialLayer int32
	var result int32

	if e.svcEnabled {
		result = streamVPXEncoderEncodeSVC(
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
			uintptr(unsafe.Pointer(&temporalLayer)),
			uintptr(unsafe.Pointer(&spatialLayer)),
		)
	} else {
		result = streamVPXEncoderEncode(
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
	timestamp := uint32(pts * (90000 / int64(fps)))

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
		streamVPXEncoderRequestKF(e.handle)
	}
}

// SetBitrate implements VideoEncoder.
func (e *VPXEncoder) SetBitrate(bitrateBps int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return fmt.Errorf("encoder not initialized")
	}

	bitrateKbps := int32(bitrateBps / 1000)
	if streamVPXEncoderSetBitrate(e.handle, bitrateKbps) != streamVPXOK {
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
	if streamVPXEncoderSetTemporal(e.handle, int32(temporalLayers)) != streamVPXOK {
		return fmt.Errorf("failed to set temporal layers: %s", getVPXError())
	}

	// Set spatial layers (VP9 only, but C wrapper handles VP8 gracefully)
	if e.codec == VideoCodecVP9 {
		if streamVPXEncoderSetSpatial(e.handle, int32(spatialLayers)) != streamVPXOK {
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
	streamVPXEncoderGetSVCConfig(e.handle,
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
		streamVPXEncoderDestroy(e.handle)
		e.handle = 0
	}

	return nil
}

// VPXDecoder implements VideoDecoder using libstream_vpx via purego.
type VPXDecoder struct {
	config VideoDecoderConfig
	codec  VideoCodec

	handle    uint64
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
	if err := loadStreamVPX(); err != nil {
		return nil, fmt.Errorf("%s decoder not available: %w", codec, err)
	}

	var codecType int32
	switch codec {
	case VideoCodecVP8:
		codecType = streamVPXCodecVP8
	case VideoCodecVP9:
		codecType = streamVPXCodecVP9
	default:
		return nil, fmt.Errorf("unsupported codec: %s", codec)
	}

	threads := int32(4)
	if config.Threads > 0 {
		threads = int32(config.Threads)
	}

	handle := streamVPXDecoderCreate(codecType, threads)
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

	var outY, outU, outV uintptr
	var outYStride, outUVStride, outWidth, outHeight int32

	result := streamVPXDecoderDecode(
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

	// Copy Y plane
	uvW := w / 2
	uvH := h / 2
	for row := 0; row < h; row++ {
		src := unsafe.Slice((*byte)(unsafe.Pointer(outY+uintptr(row*int(outYStride)))), w)
		dstStart := row * d.outputBuf.StrideY
		copy(d.outputBuf.Y[dstStart:dstStart+w], src)
	}

	// Copy U plane
	for row := 0; row < uvH; row++ {
		src := unsafe.Slice((*byte)(unsafe.Pointer(outU+uintptr(row*int(outUVStride)))), uvW)
		dstStart := row * d.outputBuf.StrideU
		copy(d.outputBuf.U[dstStart:dstStart+uvW], src)
	}

	// Copy V plane
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

	if streamVPXDecoderReset(d.handle) != streamVPXOK {
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
		streamVPXDecoderGetDimensions(d.handle, uintptr(unsafe.Pointer(&w)), uintptr(unsafe.Pointer(&h)))
		return int(w), int(h)
	}
	return d.width, d.height
}

// Close implements VideoDecoder.
func (d *VPXDecoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle != 0 {
		streamVPXDecoderDestroy(d.handle)
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
