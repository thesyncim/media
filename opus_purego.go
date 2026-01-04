//go:build (darwin || linux) && !noopus && !cgo

// Package media provides Opus audio codec support via libstream_opus using purego.
//
// This implementation uses purego to load libstream_opus dynamically at runtime,
// which is a thin wrapper around libopus with a simple primitive-only API.

package media

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	streamOpusOnce    sync.Once
	streamOpusHandle  uintptr
	streamOpusInitErr error
	streamOpusLoaded  bool
)

// libstream_opus function pointers
var (
	streamOpusEncoderCreate        func(sampleRate, channels, application int32) uint64
	streamOpusEncoderEncode        func(encoder uint64, pcm uintptr, frameSize int32, outData uintptr, outCapacity int32) int32
	streamOpusEncoderEncodeFloat   func(encoder uint64, pcm uintptr, frameSize int32, outData uintptr, outCapacity int32) int32
	streamOpusEncoderSetBitrate    func(encoder uint64, bitrate int32) int32
	streamOpusEncoderGetBitrate    func(encoder uint64) int32
	streamOpusEncoderSetComplexity func(encoder uint64, complexity int32) int32
	streamOpusEncoderSetDTX        func(encoder uint64, enabled int32) int32
	streamOpusEncoderSetFEC        func(encoder uint64, enabled int32) int32
	streamOpusEncoderSetPacketLoss func(encoder uint64, percentage int32) int32
	streamOpusEncoderGetStats      func(encoder uint64, framesEncoded, bytesEncoded uintptr)
	streamOpusEncoderDestroy       func(encoder uint64)

	streamOpusDecoderCreate      func(sampleRate, channels int32) uint64
	streamOpusDecoderDecode      func(decoder uint64, data uintptr, dataLen int32, pcm uintptr, frameSize, decodeFEC int32) int32
	streamOpusDecoderDecodeFloat func(decoder uint64, data uintptr, dataLen int32, pcm uintptr, frameSize, decodeFEC int32) int32
	streamOpusDecoderGetStats    func(decoder uint64, framesDecoded, bytesDecoded, plcFrames uintptr)
	streamOpusDecoderReset       func(decoder uint64) int32
	streamOpusPacketGetSamples   func(data uintptr, dataLen, sampleRate int32) int32
	streamOpusDecoderDestroy     func(decoder uint64)

	streamOpusGetError   func() uintptr
	streamOpusGetVersion func() uintptr
)

// Constants from stream_opus.h
const (
	streamOpusApplicationVOIP     = 2048
	streamOpusApplicationAudio    = 2049
	streamOpusApplicationLowDelay = 2051

	streamOpusOK           = 0
	streamOpusError        = -1
	streamOpusErrorNoMem   = -2
	streamOpusErrorInvalid = -3
	streamOpusErrorCodec   = -4
)

// OpusApplication defines the application type for Opus encoder
type OpusApplication int

const (
	// OpusApplicationVOIP is optimized for voice over IP
	OpusApplicationVOIP OpusApplication = streamOpusApplicationVOIP
	// OpusApplicationAudio is optimized for audio (music, etc)
	OpusApplicationAudio OpusApplication = streamOpusApplicationAudio
	// OpusApplicationLowDelay is optimized for low latency
	OpusApplicationLowDelay OpusApplication = streamOpusApplicationLowDelay
)

// loadStreamOpus loads the libstream_opus shared library.
func loadStreamOpus() error {
	streamOpusOnce.Do(func() {
		streamOpusInitErr = loadStreamOpusLib()
		if streamOpusInitErr == nil {
			streamOpusLoaded = true
		}
	})
	return streamOpusInitErr
}

func loadStreamOpusLib() error {
	paths := getStreamOpusLibPaths()

	var lastErr error
	for _, path := range paths {
		handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err == nil {
			streamOpusHandle = handle
			if err := loadStreamOpusSymbols(); err != nil {
				purego.Dlclose(handle)
				lastErr = err
				continue
			}
			return nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return fmt.Errorf("failed to load libstream_opus: %w", lastErr)
	}
	return errors.New("libstream_opus not found in any standard location")
}

func getStreamOpusLibPaths() []string {
	var paths []string

	libName := "libstream_opus.so"
	if runtime.GOOS == "darwin" {
		libName = "libstream_opus.dylib"
	}

	// Environment variable overrides
	if envPath := os.Getenv("STREAM_OPUS_LIB_PATH"); envPath != "" {
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

	// Try source root (uses runtime.Caller - works in IDE/tests)
	if root := findSourceRoot(); root != "" {
		paths = append(paths, filepath.Join(root, "build", libName))
	}

	// Try module root
	if root := findModuleRoot(); root != "" {
		paths = append(paths, filepath.Join(root, "build", libName))
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
			"libstream_opus.dylib",
			"/usr/local/lib/libstream_opus.dylib",
			"/opt/homebrew/lib/libstream_opus.dylib",
		)
	case "linux":
		paths = append(paths,
			"libstream_opus.so",
			"/usr/local/lib/libstream_opus.so",
			"/usr/lib/libstream_opus.so",
		)
	}

	return paths
}

func loadStreamOpusSymbols() error {
	// Encoder functions
	purego.RegisterLibFunc(&streamOpusEncoderCreate, streamOpusHandle, "stream_opus_encoder_create")
	purego.RegisterLibFunc(&streamOpusEncoderEncode, streamOpusHandle, "stream_opus_encoder_encode")
	purego.RegisterLibFunc(&streamOpusEncoderEncodeFloat, streamOpusHandle, "stream_opus_encoder_encode_float")
	purego.RegisterLibFunc(&streamOpusEncoderSetBitrate, streamOpusHandle, "stream_opus_encoder_set_bitrate")
	purego.RegisterLibFunc(&streamOpusEncoderGetBitrate, streamOpusHandle, "stream_opus_encoder_get_bitrate")
	purego.RegisterLibFunc(&streamOpusEncoderSetComplexity, streamOpusHandle, "stream_opus_encoder_set_complexity")
	purego.RegisterLibFunc(&streamOpusEncoderSetDTX, streamOpusHandle, "stream_opus_encoder_set_dtx")
	purego.RegisterLibFunc(&streamOpusEncoderSetFEC, streamOpusHandle, "stream_opus_encoder_set_fec")
	purego.RegisterLibFunc(&streamOpusEncoderSetPacketLoss, streamOpusHandle, "stream_opus_encoder_set_packet_loss")
	purego.RegisterLibFunc(&streamOpusEncoderGetStats, streamOpusHandle, "stream_opus_encoder_get_stats")
	purego.RegisterLibFunc(&streamOpusEncoderDestroy, streamOpusHandle, "stream_opus_encoder_destroy")

	// Decoder functions
	purego.RegisterLibFunc(&streamOpusDecoderCreate, streamOpusHandle, "stream_opus_decoder_create")
	purego.RegisterLibFunc(&streamOpusDecoderDecode, streamOpusHandle, "stream_opus_decoder_decode")
	purego.RegisterLibFunc(&streamOpusDecoderDecodeFloat, streamOpusHandle, "stream_opus_decoder_decode_float")
	purego.RegisterLibFunc(&streamOpusDecoderGetStats, streamOpusHandle, "stream_opus_decoder_get_stats")
	purego.RegisterLibFunc(&streamOpusDecoderReset, streamOpusHandle, "stream_opus_decoder_reset")
	purego.RegisterLibFunc(&streamOpusPacketGetSamples, streamOpusHandle, "stream_opus_packet_get_samples")
	purego.RegisterLibFunc(&streamOpusDecoderDestroy, streamOpusHandle, "stream_opus_decoder_destroy")

	// Utility functions
	purego.RegisterLibFunc(&streamOpusGetError, streamOpusHandle, "stream_opus_get_error")
	purego.RegisterLibFunc(&streamOpusGetVersion, streamOpusHandle, "stream_opus_get_version")

	return nil
}

// IsOpusAvailable checks if libstream_opus is available.
func IsOpusAvailable() bool {
	if err := loadStreamOpus(); err != nil {
		return false
	}
	return streamOpusLoaded
}

// GetOpusVersion returns the libopus version string.
func GetOpusVersion() string {
	if !IsOpusAvailable() {
		return ""
	}
	ptr := streamOpusGetVersion()
	if ptr == 0 {
		return ""
	}
	return goStringFromPtr(ptr)
}

func getOpusError() string {
	ptr := streamOpusGetError()
	if ptr == 0 {
		return "unknown error"
	}
	return goStringFromPtr(ptr)
}

// OpusEncoder implements AudioEncoder for Opus.
type OpusEncoder struct {
	config AudioEncoderConfig

	handle       uint64
	outputBuf    []byte
	pcmBuf       []int16 // Buffer for converting bytes to int16
	sampleRate   int
	channels     int
	application  OpusApplication
	samplesPerMs int

	stats   AudioEncoderStats
	statsMu sync.Mutex
	mu      sync.Mutex
}

// NewOpusEncoder creates a new Opus encoder.
func NewOpusEncoder(config AudioEncoderConfig) (*OpusEncoder, error) {
	if err := loadStreamOpus(); err != nil {
		return nil, fmt.Errorf("Opus encoder not available: %w", err)
	}

	sampleRate := config.SampleRate
	if sampleRate <= 0 {
		sampleRate = 48000
	}

	channels := config.Channels
	if channels <= 0 {
		channels = 1
	}
	if channels > 2 {
		return nil, fmt.Errorf("Opus supports max 2 channels, got %d", channels)
	}

	application := OpusApplicationVOIP
	if config.Application != 0 {
		application = OpusApplication(config.Application)
	}

	handle := streamOpusEncoderCreate(int32(sampleRate), int32(channels), int32(application))
	if handle == 0 {
		return nil, fmt.Errorf("failed to create Opus encoder: %s", getOpusError())
	}

	// Set bitrate if specified
	if config.BitrateBps > 0 {
		streamOpusEncoderSetBitrate(handle, int32(config.BitrateBps))
	}

	// Enable FEC by default for RTC
	streamOpusEncoderSetFEC(handle, 1)
	streamOpusEncoderSetPacketLoss(handle, 10) // Assume 10% packet loss

	enc := &OpusEncoder{
		config:       config,
		handle:       handle,
		outputBuf:    make([]byte, 4000), // Max Opus packet size
		sampleRate:   sampleRate,
		channels:     channels,
		application:  application,
		samplesPerMs: sampleRate / 1000,
	}

	return enc, nil
}

// Encode encodes audio samples to Opus.
func (e *OpusEncoder) Encode(samples *AudioSamples) (*EncodedAudio, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return nil, fmt.Errorf("encoder not initialized")
	}

	// Convert bytes to int16 samples
	// AudioSamples.Data is []byte in S16LE format
	numSamples := len(samples.Data) / 2
	if numSamples == 0 {
		return nil, fmt.Errorf("empty audio samples")
	}

	// Resize buffer if needed
	if cap(e.pcmBuf) < numSamples {
		e.pcmBuf = make([]int16, numSamples)
	}
	e.pcmBuf = e.pcmBuf[:numSamples]

	// Convert bytes to int16 (little-endian)
	for i := 0; i < numSamples; i++ {
		e.pcmBuf[i] = int16(binary.LittleEndian.Uint16(samples.Data[i*2:]))
	}

	frameSize := numSamples / e.channels

	result := streamOpusEncoderEncode(
		e.handle,
		uintptr(unsafe.Pointer(&e.pcmBuf[0])),
		int32(frameSize),
		uintptr(unsafe.Pointer(&e.outputBuf[0])),
		int32(len(e.outputBuf)),
	)

	if result < 0 {
		return nil, fmt.Errorf("encode failed: %s", getOpusError())
	}

	data := make([]byte, result)
	copy(data, e.outputBuf[:result])

	// Calculate timestamp (48kHz clock for RTP)
	timestamp := uint32(samples.Timestamp * 48 / 1000000)

	e.statsMu.Lock()
	e.stats.FramesEncoded++
	e.stats.BytesEncoded += uint64(result)
	e.stats.SamplesEncoded += uint64(frameSize)
	e.statsMu.Unlock()

	return &EncodedAudio{
		Data:      data,
		Timestamp: timestamp,
		Duration:  uint32(frameSize),
	}, nil
}

// EncodeFloat encodes float audio samples to Opus.
func (e *OpusEncoder) EncodeFloat(samples []float32, frameSize int) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return nil, fmt.Errorf("encoder not initialized")
	}

	result := streamOpusEncoderEncodeFloat(
		e.handle,
		uintptr(unsafe.Pointer(&samples[0])),
		int32(frameSize),
		uintptr(unsafe.Pointer(&e.outputBuf[0])),
		int32(len(e.outputBuf)),
	)

	if result < 0 {
		return nil, fmt.Errorf("encode failed: %s", getOpusError())
	}

	data := make([]byte, result)
	copy(data, e.outputBuf[:result])

	e.statsMu.Lock()
	e.stats.FramesEncoded++
	e.stats.BytesEncoded += uint64(result)
	e.stats.SamplesEncoded += uint64(frameSize)
	e.statsMu.Unlock()

	return data, nil
}

// SetBitrate sets the encoder bitrate.
func (e *OpusEncoder) SetBitrate(bitrateBps int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return fmt.Errorf("encoder not initialized")
	}

	if streamOpusEncoderSetBitrate(e.handle, int32(bitrateBps)) != streamOpusOK {
		return fmt.Errorf("failed to set bitrate: %s", getOpusError())
	}

	e.config.BitrateBps = bitrateBps
	return nil
}

// GetBitrate returns the current bitrate.
func (e *OpusEncoder) GetBitrate() int {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return 0
	}
	return int(streamOpusEncoderGetBitrate(e.handle))
}

// SetComplexity sets the encoder complexity (0-10).
func (e *OpusEncoder) SetComplexity(complexity int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return fmt.Errorf("encoder not initialized")
	}

	if streamOpusEncoderSetComplexity(e.handle, int32(complexity)) != streamOpusOK {
		return fmt.Errorf("failed to set complexity: %s", getOpusError())
	}
	return nil
}

// SetDTX enables or disables discontinuous transmission.
func (e *OpusEncoder) SetDTX(enabled bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return fmt.Errorf("encoder not initialized")
	}

	val := int32(0)
	if enabled {
		val = 1
	}
	if streamOpusEncoderSetDTX(e.handle, val) != streamOpusOK {
		return fmt.Errorf("failed to set DTX: %s", getOpusError())
	}
	return nil
}

// SetFEC enables or disables forward error correction.
func (e *OpusEncoder) SetFEC(enabled bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return fmt.Errorf("encoder not initialized")
	}

	val := int32(0)
	if enabled {
		val = 1
	}
	if streamOpusEncoderSetFEC(e.handle, val) != streamOpusOK {
		return fmt.Errorf("failed to set FEC: %s", getOpusError())
	}
	return nil
}

// SetPacketLossPercent sets expected packet loss for FEC tuning.
func (e *OpusEncoder) SetPacketLossPercent(percent int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return fmt.Errorf("encoder not initialized")
	}

	if streamOpusEncoderSetPacketLoss(e.handle, int32(percent)) != streamOpusOK {
		return fmt.Errorf("failed to set packet loss: %s", getOpusError())
	}
	return nil
}

// Config returns the encoder configuration.
func (e *OpusEncoder) Config() AudioEncoderConfig {
	return e.config
}

// Codec returns AudioCodecOpus.
func (e *OpusEncoder) Codec() AudioCodec {
	return AudioCodecOpus
}

// Stats returns encoder statistics.
func (e *OpusEncoder) Stats() AudioEncoderStats {
	e.statsMu.Lock()
	defer e.statsMu.Unlock()
	return e.stats
}

// Close releases encoder resources.
func (e *OpusEncoder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle != 0 {
		streamOpusEncoderDestroy(e.handle)
		e.handle = 0
	}
	return nil
}

// OpusDecoder implements AudioDecoder for Opus.
type OpusDecoder struct {
	config     AudioDecoderConfig
	handle     uint64
	sampleRate int
	channels   int
	outputBuf  []int16
	byteBuf    []byte // Buffer for converting int16 to bytes

	stats   AudioDecoderStats
	statsMu sync.Mutex
	mu      sync.Mutex
}

// NewOpusDecoder creates a new Opus decoder.
func NewOpusDecoder(config AudioDecoderConfig) (*OpusDecoder, error) {
	if err := loadStreamOpus(); err != nil {
		return nil, fmt.Errorf("Opus decoder not available: %w", err)
	}

	sampleRate := config.SampleRate
	if sampleRate <= 0 {
		sampleRate = 48000
	}

	channels := config.Channels
	if channels <= 0 {
		channels = 1
	}
	if channels > 2 {
		return nil, fmt.Errorf("Opus supports max 2 channels, got %d", channels)
	}

	handle := streamOpusDecoderCreate(int32(sampleRate), int32(channels))
	if handle == 0 {
		return nil, fmt.Errorf("failed to create Opus decoder: %s", getOpusError())
	}

	// Buffer for 120ms of audio (max Opus frame size)
	maxSamples := sampleRate * 120 / 1000 * channels
	dec := &OpusDecoder{
		config:     config,
		handle:     handle,
		sampleRate: sampleRate,
		channels:   channels,
		outputBuf:  make([]int16, maxSamples),
		byteBuf:    make([]byte, maxSamples*2),
	}

	return dec, nil
}

// Decode decodes Opus data to PCM samples.
func (d *OpusDecoder) Decode(encoded *EncodedAudio) (*AudioSamples, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle == 0 {
		return nil, fmt.Errorf("decoder not initialized")
	}

	// Max frame size for output
	maxFrameSize := d.sampleRate * 120 / 1000

	var dataPtr uintptr
	dataLen := int32(0)
	if len(encoded.Data) > 0 {
		dataPtr = uintptr(unsafe.Pointer(&encoded.Data[0]))
		dataLen = int32(len(encoded.Data))
	}

	result := streamOpusDecoderDecode(
		d.handle,
		dataPtr,
		dataLen,
		uintptr(unsafe.Pointer(&d.outputBuf[0])),
		int32(maxFrameSize),
		0, // No FEC decoding by default
	)

	if result < 0 {
		d.statsMu.Lock()
		d.stats.CorruptedFrames++
		d.statsMu.Unlock()
		return nil, fmt.Errorf("decode failed: %s", getOpusError())
	}

	// Convert int16 to bytes (little-endian)
	numSamples := int(result) * d.channels
	if len(d.byteBuf) < numSamples*2 {
		d.byteBuf = make([]byte, numSamples*2)
	}
	for i := 0; i < numSamples; i++ {
		binary.LittleEndian.PutUint16(d.byteBuf[i*2:], uint16(d.outputBuf[i]))
	}

	// Copy output
	data := make([]byte, numSamples*2)
	copy(data, d.byteBuf[:numSamples*2])

	d.statsMu.Lock()
	d.stats.FramesDecoded++
	d.stats.BytesDecoded += uint64(len(encoded.Data))
	d.stats.SamplesDecoded += uint64(result)
	d.statsMu.Unlock()

	return &AudioSamples{
		Data:        data,
		SampleRate:  d.sampleRate,
		Channels:    d.channels,
		SampleCount: int(result),
		Format:      AudioFormatS16,
		Timestamp:   int64(encoded.Timestamp) * 1000000 / 48,
	}, nil
}

// DecodeWithPLC performs packet loss concealment.
func (d *OpusDecoder) DecodeWithPLC(data []byte) (*AudioSamples, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle == 0 {
		return nil, fmt.Errorf("decoder not initialized")
	}

	// Estimate frame size (20ms is typical)
	frameSize := d.sampleRate * 20 / 1000

	var dataPtr uintptr
	dataLen := int32(0)
	if len(data) > 0 {
		dataPtr = uintptr(unsafe.Pointer(&data[0]))
		dataLen = int32(len(data))
	}

	result := streamOpusDecoderDecode(
		d.handle,
		dataPtr,
		dataLen,
		uintptr(unsafe.Pointer(&d.outputBuf[0])),
		int32(frameSize),
		0,
	)

	if result < 0 {
		return nil, fmt.Errorf("PLC decode failed: %s", getOpusError())
	}

	// Convert int16 to bytes
	numSamples := int(result) * d.channels
	outData := make([]byte, numSamples*2)
	for i := 0; i < numSamples; i++ {
		binary.LittleEndian.PutUint16(outData[i*2:], uint16(d.outputBuf[i]))
	}

	d.statsMu.Lock()
	d.stats.FramesDecoded++
	if len(data) == 0 {
		d.stats.PLCFrames++
	}
	d.statsMu.Unlock()

	return &AudioSamples{
		Data:        outData,
		SampleRate:  d.sampleRate,
		Channels:    d.channels,
		SampleCount: int(result),
		Format:      AudioFormatS16,
	}, nil
}

// Config returns the decoder configuration.
func (d *OpusDecoder) Config() AudioDecoderConfig {
	return d.config
}

// Codec returns AudioCodecOpus.
func (d *OpusDecoder) Codec() AudioCodec {
	return AudioCodecOpus
}

// Stats returns decoder statistics.
func (d *OpusDecoder) Stats() AudioDecoderStats {
	d.statsMu.Lock()
	defer d.statsMu.Unlock()
	return d.stats
}

// Reset resets decoder state.
func (d *OpusDecoder) Reset() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle == 0 {
		return fmt.Errorf("decoder not initialized")
	}

	if streamOpusDecoderReset(d.handle) != streamOpusOK {
		return fmt.Errorf("failed to reset decoder: %s", getOpusError())
	}
	return nil
}

// Close releases decoder resources.
func (d *OpusDecoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle != 0 {
		streamOpusDecoderDestroy(d.handle)
		d.handle = 0
	}
	return nil
}

// GetOpusPacketSamples returns the number of samples in an Opus packet.
func GetOpusPacketSamples(data []byte, sampleRate int) int {
	if !IsOpusAvailable() || len(data) == 0 {
		return 0
	}
	return int(streamOpusPacketGetSamples(
		uintptr(unsafe.Pointer(&data[0])),
		int32(len(data)),
		int32(sampleRate),
	))
}

// Register Opus encoder and decoder
func init() {
	RegisterAudioEncoder(AudioCodecOpus, func(config AudioEncoderConfig) (AudioEncoder, error) {
		return NewOpusEncoder(config)
	})
	RegisterAudioDecoder(AudioCodecOpus, func(config AudioDecoderConfig) (AudioDecoder, error) {
		return NewOpusDecoder(config)
	})
}
