//go:build (darwin || linux) && !noav1 && !cgo

// Package media provides AV1 codec support via libmedia_av1 using purego.

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
	mediaAV1Once    sync.Once
	mediaAV1Handle  uintptr
	mediaAV1InitErr error
	mediaAV1Loaded  bool
)

// libmedia_av1 function pointers
var (
	mediaAV1EncoderCreate        func(width, height, fps, bitrateKbps, usage, threads int32) uint64
	mediaAV1EncoderCreateSVC     func(width, height, fps, bitrateKbps, usage, threads, temporalLayers, spatialLayers int32) uint64
	mediaAV1EncoderEncode        func(encoder uint64, yPlane, uPlane, vPlane uintptr, yStride, uvStride, forceKeyframe int32, outData uintptr, outCapacity int32, outFrameType, outPts uintptr) int32
	mediaAV1EncoderEncodeSVC     func(encoder uint64, yPlane, uPlane, vPlane uintptr, yStride, uvStride, forceKeyframe int32, outData uintptr, outCapacity int32, outFrameType, outPts, outTemporalLayer, outSpatialLayer uintptr) int32
	mediaAV1EncoderMaxOutputSize func(encoder uint64) int32
	mediaAV1EncoderRequestKF     func(encoder uint64)
	mediaAV1EncoderSetBitrate    func(encoder uint64, bitrateKbps int32) int32
	mediaAV1EncoderSetTemporal   func(encoder uint64, layers int32) int32
	mediaAV1EncoderSetSpatial    func(encoder uint64, layers int32) int32
	mediaAV1EncoderGetSVCConfig  func(encoder uint64, temporalLayers, spatialLayers, svcEnabled uintptr)
	mediaAV1EncoderGetStats      func(encoder uint64, framesEncoded, keyframesEncoded, bytesEncoded uintptr)
	mediaAV1EncoderDestroy       func(encoder uint64)

	mediaAV1DecoderCreate        func(threads int32) uint64
	mediaAV1DecoderDecode        func(decoder uint64, data uintptr, dataLen int32, outY, outU, outV, outYStride, outUVStride, outWidth, outHeight uintptr) int32
	mediaAV1DecoderGetDimensions func(decoder uint64, width, height uintptr)
	mediaAV1DecoderGetStats      func(decoder uint64, framesDecoded, keyframesDecoded, bytesDecoded, corruptedFrames uintptr)
	mediaAV1DecoderReset         func(decoder uint64) int32
	mediaAV1DecoderDestroy       func(decoder uint64)

	mediaAV1GetError         func() uintptr
	mediaAV1EncoderAvailable func() int32
	mediaAV1DecoderAvailable func() int32
)

// Constants from media_av1.h
const (
	mediaAV1FrameKey       = 0
	mediaAV1FrameInter     = 1
	mediaAV1FrameIntraOnly = 2
	mediaAV1FrameSwitch    = 3

	mediaAV1UsageRealtime    = 1
	mediaAV1UsageGoodQuality = 0

	mediaAV1OK           = 0
	mediaAV1Error        = -1
	mediaAV1ErrorNoMem   = -2
	mediaAV1ErrorInvalid = -3
	mediaAV1ErrorCodec   = -4
)

// AV1Usage defines the encoding usage preset
type AV1Usage int

const (
	AV1UsageRealtime    AV1Usage = mediaAV1UsageRealtime
	AV1UsageGoodQuality AV1Usage = mediaAV1UsageGoodQuality
)

func loadMediaAV1() error {
	mediaAV1Once.Do(func() {
		mediaAV1InitErr = loadMediaAV1Lib()
		if mediaAV1InitErr == nil {
			mediaAV1Loaded = true
		}
	})
	return mediaAV1InitErr
}

func loadMediaAV1Lib() error {
	paths := getMediaAV1LibPaths()

	var lastErr error
	for _, path := range paths {
		handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err == nil {
			mediaAV1Handle = handle
			if err := loadMediaAV1Symbols(); err != nil {
				purego.Dlclose(handle)
				lastErr = err
				continue
			}
			return nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return fmt.Errorf("failed to load libmedia_av1: %w", lastErr)
	}
	return errors.New("libmedia_av1 not found in any standard location")
}

func getMediaAV1LibPaths() []string {
	var paths []string

	libName := "libmedia_av1.so"
	if runtime.GOOS == "darwin" {
		libName = "libmedia_av1.dylib"
	}

	// Environment variable overrides (highest priority)
	if envPath := os.Getenv("MEDIA_AV1_LIB_PATH"); envPath != "" {
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
			"libmedia_av1.dylib",
			"/usr/local/lib/libmedia_av1.dylib",
			"/opt/homebrew/lib/libmedia_av1.dylib",
		)
	case "linux":
		paths = append(paths,
			"libmedia_av1.so",
			"/usr/local/lib/libmedia_av1.so",
			"/usr/lib/libmedia_av1.so",
		)
	}

	return paths
}

func loadMediaAV1Symbols() error {
	purego.RegisterLibFunc(&mediaAV1EncoderCreate, mediaAV1Handle, "media_av1_encoder_create")
	purego.RegisterLibFunc(&mediaAV1EncoderCreateSVC, mediaAV1Handle, "media_av1_encoder_create_svc")
	purego.RegisterLibFunc(&mediaAV1EncoderEncode, mediaAV1Handle, "media_av1_encoder_encode")
	purego.RegisterLibFunc(&mediaAV1EncoderEncodeSVC, mediaAV1Handle, "media_av1_encoder_encode_svc")
	purego.RegisterLibFunc(&mediaAV1EncoderMaxOutputSize, mediaAV1Handle, "media_av1_encoder_max_output_size")
	purego.RegisterLibFunc(&mediaAV1EncoderRequestKF, mediaAV1Handle, "media_av1_encoder_request_keyframe")
	purego.RegisterLibFunc(&mediaAV1EncoderSetBitrate, mediaAV1Handle, "media_av1_encoder_set_bitrate")
	purego.RegisterLibFunc(&mediaAV1EncoderSetTemporal, mediaAV1Handle, "media_av1_encoder_set_temporal_layers")
	purego.RegisterLibFunc(&mediaAV1EncoderSetSpatial, mediaAV1Handle, "media_av1_encoder_set_spatial_layers")
	purego.RegisterLibFunc(&mediaAV1EncoderGetSVCConfig, mediaAV1Handle, "media_av1_encoder_get_svc_config")
	purego.RegisterLibFunc(&mediaAV1EncoderGetStats, mediaAV1Handle, "media_av1_encoder_get_stats")
	purego.RegisterLibFunc(&mediaAV1EncoderDestroy, mediaAV1Handle, "media_av1_encoder_destroy")

	purego.RegisterLibFunc(&mediaAV1DecoderCreate, mediaAV1Handle, "media_av1_decoder_create")
	purego.RegisterLibFunc(&mediaAV1DecoderDecode, mediaAV1Handle, "media_av1_decoder_decode")
	purego.RegisterLibFunc(&mediaAV1DecoderGetDimensions, mediaAV1Handle, "media_av1_decoder_get_dimensions")
	purego.RegisterLibFunc(&mediaAV1DecoderGetStats, mediaAV1Handle, "media_av1_decoder_get_stats")
	purego.RegisterLibFunc(&mediaAV1DecoderReset, mediaAV1Handle, "media_av1_decoder_reset")
	purego.RegisterLibFunc(&mediaAV1DecoderDestroy, mediaAV1Handle, "media_av1_decoder_destroy")

	purego.RegisterLibFunc(&mediaAV1GetError, mediaAV1Handle, "media_av1_get_error")
	purego.RegisterLibFunc(&mediaAV1EncoderAvailable, mediaAV1Handle, "media_av1_encoder_available")
	purego.RegisterLibFunc(&mediaAV1DecoderAvailable, mediaAV1Handle, "media_av1_decoder_available")

	return nil
}

// IsAV1Available checks if libmedia_av1 is available.
func IsAV1Available() bool {
	if err := loadMediaAV1(); err != nil {
		return false
	}
	return mediaAV1Loaded
}

// IsAV1EncoderAvailable checks if AV1 encoder is available.
func IsAV1EncoderAvailable() bool {
	if !IsAV1Available() {
		return false
	}
	return mediaAV1EncoderAvailable() != 0
}

// IsAV1DecoderAvailable checks if AV1 decoder is available.
func IsAV1DecoderAvailable() bool {
	if !IsAV1Available() {
		return false
	}
	return mediaAV1DecoderAvailable() != 0
}

func getAV1Error() string {
	ptr := mediaAV1GetError()
	if ptr == 0 {
		return "unknown error"
	}
	return goStringFromPtr(ptr)
}

// AV1Encoder implements VideoEncoder for AV1.
type AV1Encoder struct {
	config VideoEncoderConfig

	handle    uint64
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
	if err := loadMediaAV1(); err != nil {
		return nil, fmt.Errorf("AV1 encoder not available: %w", err)
	}

	if mediaAV1EncoderAvailable() == 0 {
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

	var handle uint64
	svcEnabled := temporalLayers > 1 || spatialLayers > 1

	if svcEnabled {
		handle = mediaAV1EncoderCreateSVC(
			int32(config.Width),
			int32(config.Height),
			int32(fps),
			int32(bitrateKbps),
			int32(usage),
			int32(threads),
			int32(temporalLayers),
			int32(spatialLayers),
		)
	} else {
		handle = mediaAV1EncoderCreate(
			int32(config.Width),
			int32(config.Height),
			int32(fps),
			int32(bitrateKbps),
			int32(usage),
			int32(threads),
		)
	}

	if handle == 0 {
		return nil, fmt.Errorf("failed to create AV1 encoder: %s", getAV1Error())
	}

	maxOutput := mediaAV1EncoderMaxOutputSize(handle)
	if maxOutput <= 0 {
		maxOutput = int32(config.Width * config.Height * 3 / 2)
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

	forceKeyframe := int32(0)
	if e.keyframeReq.Swap(false) {
		forceKeyframe = 1
	}

	var frameType int32
	var pts int64
	var temporalLayer, spatialLayer int32
	var result int32

	if e.svcEnabled {
		result = mediaAV1EncoderEncodeSVC(
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
		result = mediaAV1EncoderEncode(
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
		return nil, fmt.Errorf("encode failed: %s", getAV1Error())
	}

	if result == 0 {
		return nil, nil
	}

	data := make([]byte, result)
	copy(data, e.outputBuf[:result])

	ft := FrameTypeDelta
	if frameType == mediaAV1FrameKey {
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
func (e *AV1Encoder) RequestKeyframe() {
	e.keyframeReq.Store(true)
	if e.handle != 0 {
		mediaAV1EncoderRequestKF(e.handle)
	}
}

// SetBitrate implements VideoEncoder.
func (e *AV1Encoder) SetBitrate(bitrateBps int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return fmt.Errorf("encoder not initialized")
	}

	bitrateKbps := int32(bitrateBps / 1000)
	if mediaAV1EncoderSetBitrate(e.handle, bitrateKbps) != mediaAV1OK {
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

	if mediaAV1EncoderSetTemporal(e.handle, int32(temporalLayers)) != mediaAV1OK {
		return fmt.Errorf("failed to set temporal layers: %s", getAV1Error())
	}

	if mediaAV1EncoderSetSpatial(e.handle, int32(spatialLayers)) != mediaAV1OK {
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

	var tempLayers, spatLayers, enabled int32
	mediaAV1EncoderGetSVCConfig(e.handle,
		uintptr(unsafe.Pointer(&tempLayers)),
		uintptr(unsafe.Pointer(&spatLayers)),
		uintptr(unsafe.Pointer(&enabled)),
	)

	return int(tempLayers), int(spatLayers), enabled != 0
}

// Config implements VideoEncoder.
func (e *AV1Encoder) Config() VideoEncoderConfig {
	return e.config
}

// Codec implements VideoEncoder.
func (e *AV1Encoder) Codec() VideoCodec {
	return VideoCodecAV1
}

// Stats implements VideoEncoder.
func (e *AV1Encoder) Stats() EncoderStats {
	e.statsMu.Lock()
	defer e.statsMu.Unlock()
	return e.stats
}

// Flush implements VideoEncoder.
func (e *AV1Encoder) Flush() ([]*EncodedFrame, error) {
	return nil, nil
}

// Close implements VideoEncoder.
func (e *AV1Encoder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle != 0 {
		mediaAV1EncoderDestroy(e.handle)
		e.handle = 0
	}

	return nil
}

// AV1Decoder implements VideoDecoder for AV1.
type AV1Decoder struct {
	config VideoDecoderConfig

	handle    uint64
	outputBuf *VideoFrameBuffer
	width     int
	height    int

	stats   DecoderStats
	statsMu sync.Mutex
	mu      sync.Mutex
}

// NewAV1Decoder creates a new AV1 decoder.
func NewAV1Decoder(config VideoDecoderConfig) (*AV1Decoder, error) {
	if err := loadMediaAV1(); err != nil {
		return nil, fmt.Errorf("AV1 decoder not available: %w", err)
	}

	if mediaAV1DecoderAvailable() == 0 {
		return nil, errors.New("AV1 decoder not available (libaom not compiled)")
	}

	threads := int32(4)
	if config.Threads > 0 {
		threads = int32(config.Threads)
	}

	handle := mediaAV1DecoderCreate(threads)
	if handle == 0 {
		return nil, fmt.Errorf("failed to create AV1 decoder: %s", getAV1Error())
	}

	return &AV1Decoder{
		config: config,
		handle: handle,
	}, nil
}

// Decode implements VideoDecoder.
func (d *AV1Decoder) Decode(encoded *EncodedFrame) (*VideoFrame, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle == 0 {
		return nil, fmt.Errorf("decoder not initialized")
	}

	var outY, outU, outV uintptr
	var outYStride, outUVStride, outWidth, outHeight int32

	result := mediaAV1DecoderDecode(
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
func (d *AV1Decoder) DecodeRTP(data []byte, marker bool, timestamp uint32) (*VideoFrame, error) {
	encoded := &EncodedFrame{
		Data:      data,
		Timestamp: timestamp,
	}
	return d.Decode(encoded)
}

// Config implements VideoDecoder.
func (d *AV1Decoder) Config() VideoDecoderConfig {
	return d.config
}

// Codec implements VideoDecoder.
func (d *AV1Decoder) Codec() VideoCodec {
	return VideoCodecAV1
}

// Stats implements VideoDecoder.
func (d *AV1Decoder) Stats() DecoderStats {
	d.statsMu.Lock()
	defer d.statsMu.Unlock()
	return d.stats
}

// Flush implements VideoDecoder.
func (d *AV1Decoder) Flush() ([]*VideoFrame, error) {
	return nil, nil
}

// Reset implements VideoDecoder.
func (d *AV1Decoder) Reset() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle == 0 {
		return fmt.Errorf("decoder not initialized")
	}

	if mediaAV1DecoderReset(d.handle) != mediaAV1OK {
		return fmt.Errorf("failed to reset decoder: %s", getAV1Error())
	}

	return nil
}

// GetDimensions implements VideoDecoder.
func (d *AV1Decoder) GetDimensions() (width, height int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle != 0 {
		var w, h int32
		mediaAV1DecoderGetDimensions(d.handle, uintptr(unsafe.Pointer(&w)), uintptr(unsafe.Pointer(&h)))
		return int(w), int(h)
	}
	return d.width, d.height
}

// Close implements VideoDecoder.
func (d *AV1Decoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle != 0 {
		mediaAV1DecoderDestroy(d.handle)
		d.handle = 0
	}

	return nil
}

// Register AV1 encoder and decoder
func init() {
	RegisterVideoEncoder(VideoCodecAV1, func(config VideoEncoderConfig) (VideoEncoder, error) {
		return NewAV1Encoder(config)
	})
	RegisterVideoDecoder(VideoCodecAV1, func(config VideoDecoderConfig) (VideoDecoder, error) {
		return NewAV1Decoder(config)
	})
}
