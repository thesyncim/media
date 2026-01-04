//go:build (darwin || linux) && !noopus && cgo

// Package media provides Opus audio codec support via libmedia_opus using CGO.
//
// This implementation uses CGO to link directly against libmedia_opus,
// providing better performance than the purego variant.

package media

/*
#cgo CFLAGS: -I${SRCDIR}/clib
#cgo darwin LDFLAGS: -L${SRCDIR}/build -lmedia_opus -Wl,-rpath,${SRCDIR}/build
#cgo linux LDFLAGS: -L${SRCDIR}/build -lmedia_opus -Wl,-rpath,${SRCDIR}/build

#include "media_opus.h"
#include <stdlib.h>
*/
import "C"

import (
	"encoding/binary"
	"fmt"
	"sync"
	"unsafe"
)

// Constants from media_opus.h
const (
	streamOpusApplicationVOIP     = C.MEDIA_OPUS_APPLICATION_VOIP
	streamOpusApplicationAudio    = C.MEDIA_OPUS_APPLICATION_AUDIO
	streamOpusApplicationLowDelay = C.MEDIA_OPUS_APPLICATION_LOWDELAY

	streamOpusOK           = C.MEDIA_OPUS_OK
	streamOpusError        = C.MEDIA_OPUS_ERROR
	streamOpusErrorNoMem   = C.MEDIA_OPUS_ERROR_NOMEM
	streamOpusErrorInvalid = C.MEDIA_OPUS_ERROR_INVALID
	streamOpusErrorCodec   = C.MEDIA_OPUS_ERROR_CODEC
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

// IsOpusAvailable checks if libmedia_opus is available.
// With CGO this is always true since it links at compile time.
func IsOpusAvailable() bool {
	return true
}

// GetOpusVersion returns the libopus version string.
func GetOpusVersion() string {
	cstr := C.media_opus_get_version()
	if cstr == nil {
		return ""
	}
	return C.GoString(cstr)
}

func getOpusError() string {
	cstr := C.media_opus_get_error()
	if cstr == nil {
		return "unknown error"
	}
	return C.GoString(cstr)
}

// OpusEncoder implements AudioEncoder for Opus.
type OpusEncoder struct {
	config AudioEncoderConfig

	handle       C.media_opus_encoder_t
	outputBuf    []byte
	pcmBuf       []C.int16_t
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

	handle := C.media_opus_encoder_create(
		C.int32_t(sampleRate),
		C.int32_t(channels),
		C.int32_t(application),
	)
	if handle == 0 {
		return nil, fmt.Errorf("failed to create Opus encoder: %s", getOpusError())
	}

	// Set bitrate if specified
	if config.BitrateBps > 0 {
		C.media_opus_encoder_set_bitrate(handle, C.int32_t(config.BitrateBps))
	}

	// Enable FEC by default for RTC
	C.media_opus_encoder_set_fec(handle, 1)
	C.media_opus_encoder_set_packet_loss(handle, 10)

	enc := &OpusEncoder{
		config:       config,
		handle:       handle,
		outputBuf:    make([]byte, 4000),
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
	numSamples := len(samples.Data) / 2
	if numSamples == 0 {
		return nil, fmt.Errorf("empty audio samples")
	}

	// Resize buffer if needed
	if cap(e.pcmBuf) < numSamples {
		e.pcmBuf = make([]C.int16_t, numSamples)
	}
	e.pcmBuf = e.pcmBuf[:numSamples]

	// Convert bytes to int16 (little-endian)
	for i := 0; i < numSamples; i++ {
		e.pcmBuf[i] = C.int16_t(binary.LittleEndian.Uint16(samples.Data[i*2:]))
	}

	frameSize := numSamples / e.channels

	result := C.media_opus_encoder_encode(
		e.handle,
		(*C.int16_t)(unsafe.Pointer(&e.pcmBuf[0])),
		C.int32_t(frameSize),
		(*C.uint8_t)(unsafe.Pointer(&e.outputBuf[0])),
		C.int32_t(len(e.outputBuf)),
	)

	if result < 0 {
		return nil, fmt.Errorf("encode failed: %s", getOpusError())
	}

	data := make([]byte, result)
	copy(data, e.outputBuf[:result])

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

	result := C.media_opus_encoder_encode_float(
		e.handle,
		(*C.float)(unsafe.Pointer(&samples[0])),
		C.int32_t(frameSize),
		(*C.uint8_t)(unsafe.Pointer(&e.outputBuf[0])),
		C.int32_t(len(e.outputBuf)),
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

	if C.media_opus_encoder_set_bitrate(e.handle, C.int32_t(bitrateBps)) != streamOpusOK {
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
	return int(C.media_opus_encoder_get_bitrate(e.handle))
}

// SetComplexity sets the encoder complexity (0-10).
func (e *OpusEncoder) SetComplexity(complexity int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.handle == 0 {
		return fmt.Errorf("encoder not initialized")
	}

	if C.media_opus_encoder_set_complexity(e.handle, C.int32_t(complexity)) != streamOpusOK {
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

	val := C.int32_t(0)
	if enabled {
		val = 1
	}
	if C.media_opus_encoder_set_dtx(e.handle, val) != streamOpusOK {
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

	val := C.int32_t(0)
	if enabled {
		val = 1
	}
	if C.media_opus_encoder_set_fec(e.handle, val) != streamOpusOK {
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

	if C.media_opus_encoder_set_packet_loss(e.handle, C.int32_t(percent)) != streamOpusOK {
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
		C.media_opus_encoder_destroy(e.handle)
		e.handle = 0
	}
	return nil
}

// OpusDecoder implements AudioDecoder for Opus.
type OpusDecoder struct {
	config     AudioDecoderConfig
	handle     C.media_opus_decoder_t
	sampleRate int
	channels   int
	outputBuf  []C.int16_t
	byteBuf    []byte

	stats   AudioDecoderStats
	statsMu sync.Mutex
	mu      sync.Mutex
}

// NewOpusDecoder creates a new Opus decoder.
func NewOpusDecoder(config AudioDecoderConfig) (*OpusDecoder, error) {
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

	handle := C.media_opus_decoder_create(
		C.int32_t(sampleRate),
		C.int32_t(channels),
	)
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
		outputBuf:  make([]C.int16_t, maxSamples),
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

	maxFrameSize := d.sampleRate * 120 / 1000

	var dataPtr *C.uint8_t
	dataLen := C.int32_t(0)
	if len(encoded.Data) > 0 {
		dataPtr = (*C.uint8_t)(unsafe.Pointer(&encoded.Data[0]))
		dataLen = C.int32_t(len(encoded.Data))
	}

	result := C.media_opus_decoder_decode(
		d.handle,
		dataPtr,
		dataLen,
		(*C.int16_t)(unsafe.Pointer(&d.outputBuf[0])),
		C.int32_t(maxFrameSize),
		0,
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

	frameSize := d.sampleRate * 20 / 1000

	var dataPtr *C.uint8_t
	dataLen := C.int32_t(0)
	if len(data) > 0 {
		dataPtr = (*C.uint8_t)(unsafe.Pointer(&data[0]))
		dataLen = C.int32_t(len(data))
	}

	result := C.media_opus_decoder_decode(
		d.handle,
		dataPtr,
		dataLen,
		(*C.int16_t)(unsafe.Pointer(&d.outputBuf[0])),
		C.int32_t(frameSize),
		0,
	)

	if result < 0 {
		return nil, fmt.Errorf("PLC decode failed: %s", getOpusError())
	}

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

	if C.media_opus_decoder_reset(d.handle) != streamOpusOK {
		return fmt.Errorf("failed to reset decoder: %s", getOpusError())
	}
	return nil
}

// Close releases decoder resources.
func (d *OpusDecoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.handle != 0 {
		C.media_opus_decoder_destroy(d.handle)
		d.handle = 0
	}
	return nil
}

// GetOpusPacketSamples returns the number of samples in an Opus packet.
func GetOpusPacketSamples(data []byte, sampleRate int) int {
	if len(data) == 0 {
		return 0
	}
	return int(C.media_opus_packet_get_samples(
		(*C.uint8_t)(unsafe.Pointer(&data[0])),
		C.int32_t(len(data)),
		C.int32_t(sampleRate),
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
