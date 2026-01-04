package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// DetectVideoCodec detects the video codec from raw bitstream data.
// Supports detection of:
//   - H.264/AVC: Annex-B format (ITU-T H.264) and AVCC format (ISO/IEC 14496-15)
//   - VP8: RFC 6386 - VP8 Data Format and Decoding Guide
//   - VP9: VP9 Bitstream & Decoding Process Specification
//   - AV1: AV1 Bitstream & Decoding Process Specification
//   - IVF: WebM Project container format
//
// Returns VideoCodecUnknown if the codec cannot be determined.
func DetectVideoCodec(data []byte) VideoCodec {
	if len(data) < 4 {
		return VideoCodecUnknown
	}

	// Check for Annex-B start code (H.264/H.265)
	if isAnnexBStartCode(data) {
		nalType := getNALType(data)
		if isH264NALType(nalType) {
			return VideoCodecH264
		}
		// Could add H.265 detection here
	}

	// Check for AVCC format (H.264 in container)
	if isAVCCFormat(data) {
		return VideoCodecH264
	}

	// Check for IVF header (VP8/VP9)
	if len(data) >= 32 && string(data[0:4]) == "DKIF" {
		fourCC := string(data[8:12])
		switch fourCC {
		case "VP80":
			return VideoCodecVP8
		case "VP90":
			return VideoCodecVP9
		case "AV01":
			return VideoCodecAV1
		}
	}

	// Check for VP8 keyframe
	if isVP8Keyframe(data) {
		return VideoCodecVP8
	}

	// Check for VP9 frame
	if isVP9Frame(data) {
		return VideoCodecVP9
	}

	// Check for AV1 OBU
	if isAV1OBU(data) {
		return VideoCodecAV1
	}

	return VideoCodecUnknown
}

// isAnnexBStartCode checks for H.264/H.265 Annex-B start codes.
// Per ITU-T H.264 Annex B, NAL units are prefixed with:
//   - 4-byte start code: 0x00000001 (used at stream start and after certain NALUs)
//   - 3-byte start code: 0x000001 (used between NALUs)
func isAnnexBStartCode(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	// 4-byte start code: 0x00000001
	if data[0] == 0 && data[1] == 0 && data[2] == 0 && data[3] == 1 {
		return true
	}
	// 3-byte start code: 0x000001
	if data[0] == 0 && data[1] == 0 && data[2] == 1 {
		return true
	}
	return false
}

// getNALType extracts NAL unit type from Annex-B data.
// Per ITU-T H.264 Section 7.3.1, the NAL unit header is:
//   - forbidden_zero_bit (1 bit): must be 0
//   - nal_ref_idc (2 bits): reference priority
//   - nal_unit_type (5 bits): type identifier (values 1-12 and 19-21 for H.264)
func getNALType(data []byte) byte {
	if len(data) < 4 {
		return 0
	}
	offset := 3
	if data[2] == 0 {
		offset = 4
	}
	if len(data) <= offset {
		return 0
	}
	return data[offset] & 0x1F // H.264 NAL type is in lower 5 bits
}

// isH264NALType checks if NAL type is valid H.264.
// Per ITU-T H.264 Table 7-1, valid NAL unit types are:
//   - 1: Non-IDR slice, 2: Slice data partition A, 3-4: Slice data partitions B/C
//   - 5: IDR slice, 6: SEI, 7: SPS, 8: PPS, 9: AUD, 10: End of seq, 11: End of stream, 12: Filler
//   - 19: Coded slice of aux picture, 20: Coded slice extension, 21: Coded slice extension for depth
func isH264NALType(nalType byte) bool {
	return (nalType >= 1 && nalType <= 12) || (nalType >= 19 && nalType <= 21)
}

// isAVCCFormat checks for AVCC (length-prefixed) format.
// Per ISO/IEC 14496-15 (MPEG-4 Part 15), AVCC format uses:
//   - 4-byte big-endian NAL unit length prefix instead of start codes
//   - Commonly used in MP4/MOV containers and RTMP streams
func isAVCCFormat(data []byte) bool {
	if len(data) < 8 {
		return false
	}
	// Check if first 4 bytes could be a length prefix
	length := int(data[0])<<24 | int(data[1])<<16 | int(data[2])<<8 | int(data[3])
	// Sanity check: length should be reasonable and data should be long enough
	return length > 0 && length < len(data) && length < 10*1024*1024
}

// isVP8Keyframe checks for VP8 keyframe signature.
// Per RFC 6386 Section 9.1, VP8 uncompressed data chunk:
//   - Byte 0: frame_type (1 bit), version (3 bits), show_frame (1 bit), partition_size (19 bits)
//   - Bytes 3-5 (keyframe only): start code 0x9D 0x01 0x2A followed by width/height
func isVP8Keyframe(data []byte) bool {
	if len(data) < 10 {
		return false
	}
	// VP8 keyframe: first byte bit 0 = 0 (keyframe), bits 1-3 = version
	frameTag := data[0]
	if frameTag&0x01 != 0 { // Not a keyframe
		return false
	}
	// Check for VP8 start code after 3-byte frame tag
	if len(data) >= 6 && data[3] == 0x9D && data[4] == 0x01 && data[5] == 0x2A {
		return true
	}
	return false
}

// isVP9Frame checks for VP9 frame structure.
// Per VP9 Bitstream Specification Section 6.2, the uncompressed header starts with:
//   - frame_marker (2 bits): always 0b10 (decimal 2)
//   - profile_low_bit (1 bit), reserved/profile_high_bit (1 bit)
//   - show_existing_frame (1 bit), frame_type (1 bit), etc.
func isVP9Frame(data []byte) bool {
	if len(data) < 3 {
		return false
	}
	// VP9 frame marker is 2 bits = 0b10 at bits 6-7 of first byte
	frameMarker := (data[0] >> 6) & 0x03
	return frameMarker == 0x02
}

// isAV1OBU checks for AV1 OBU (Open Bitstream Unit) format.
// Per AV1 Bitstream Specification Section 5.3.2, OBU header is:
//   - obu_forbidden_bit (1 bit): must be 0
//   - obu_type (4 bits): 1=Seq header, 2=Temporal delimiter, 3=Frame header, 4=Tile group,
//     5=Metadata, 6=Frame, 7=Redundant frame header, 8=Tile list, 15=Padding
//   - obu_extension_flag (1 bit), obu_has_size_field (1 bit), obu_reserved_1bit (1 bit)
func isAV1OBU(data []byte) bool {
	if len(data) < 2 {
		return false
	}
	obuForbidden := (data[0] >> 7) & 0x01
	obuType := (data[0] >> 3) & 0x0F
	if obuForbidden != 0 {
		return false
	}
	// Valid OBU types: 1-8, 15
	return (obuType >= 1 && obuType <= 8) || obuType == 15
}

// =============================================================================
// Audio Codec Detection
// =============================================================================

// DetectAudioCodec detects the audio codec from raw bitstream data.
// Supports detection of:
//   - Opus: RFC 6716 - Definition of the Opus Audio Codec
//   - AAC: ISO/IEC 14496-3 (MPEG-4 Part 3) with ADTS header
//   - MP3: ISO/IEC 11172-3 (MPEG-1 Audio Layer III)
//   - FLAC: Free Lossless Audio Codec format
//   - Ogg: RFC 3533 - Ogg Encapsulation Format
//
// Returns AudioCodecUnknown if the codec cannot be determined.
// Note: Some audio codecs (Opus, PCM) may not have identifiable headers
// when used in certain container formats.
func DetectAudioCodec(data []byte) AudioCodec {
	if len(data) < 4 {
		return AudioCodecUnknown
	}

	// Check for Ogg container (commonly used for Opus/Vorbis)
	// Per RFC 3533, Ogg pages start with "OggS" capture pattern
	if len(data) >= 4 && string(data[0:4]) == "OggS" {
		// Ogg container detected, could contain Opus or Vorbis
		// Check for Opus magic in first page payload
		if len(data) >= 36 && string(data[28:36]) == "OpusHead" {
			return AudioCodecOpus
		}
		return AudioCodecUnknown // Generic Ogg, could be Vorbis
	}

	// Check for FLAC stream marker
	// Per FLAC format, streams start with "fLaC" marker
	if len(data) >= 4 && string(data[0:4]) == "fLaC" {
		return AudioCodecUnknown // FLAC not in AudioCodec enum, return unknown
	}

	// Check for AAC ADTS header
	// Per ISO/IEC 14496-3, ADTS frame starts with 0xFFF syncword (12 bits)
	if isAACAdts(data) {
		return AudioCodecAAC
	}

	// Check for MP3 frame header
	// Per ISO/IEC 11172-3, MP3 frames start with 0xFFE or 0xFFF syncword
	if isMP3Frame(data) {
		return AudioCodecUnknown // MP3 not in AudioCodec enum
	}

	return AudioCodecUnknown
}

// isAACAdts checks for AAC ADTS (Audio Data Transport Stream) header.
// Per ISO/IEC 14496-3 Section 1.A.2.2, ADTS header structure:
//   - syncword (12 bits): 0xFFF
//   - ID (1 bit): MPEG version (0=MPEG-4, 1=MPEG-2)
//   - layer (2 bits): always 0b00
//   - protection_absent (1 bit): 1=no CRC
func isAACAdts(data []byte) bool {
	if len(data) < 7 {
		return false
	}
	// Check for 0xFFF syncword (first 12 bits)
	if data[0] != 0xFF || (data[1]&0xF0) != 0xF0 {
		return false
	}
	// Layer must be 0b00 (bits 1-2 of byte 1)
	layer := (data[1] >> 1) & 0x03
	return layer == 0
}

// isMP3Frame checks for MP3 (MPEG Audio Layer III) frame header.
// Per ISO/IEC 11172-3 Section 2.4.2.3, frame header structure:
//   - syncword (11 bits): 0x7FF (all 1s)
//   - version (2 bits): MPEG version
//   - layer (2 bits): 0b01 for Layer III
//   - protection (1 bit), bitrate, sample rate, etc.
func isMP3Frame(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	// Check for frame sync (11 bits all 1s)
	if data[0] != 0xFF || (data[1]&0xE0) != 0xE0 {
		return false
	}
	// Check layer (bits 1-2 of byte 1) - Layer III = 0b01
	layer := (data[1] >> 1) & 0x03
	return layer == 1
}

// AutoDecoder automatically detects codec and creates appropriate decoder.
type AutoDecoder struct {
	decoder VideoDecoder
	codec   VideoCodec
	config  VideoDecoderConfig
	mu      sync.Mutex
}

// NewAutoDecoder creates a decoder that auto-detects the codec from the first frame.
func NewAutoDecoder(config VideoDecoderConfig) *AutoDecoder {
	return &AutoDecoder{
		config: config,
	}
}

// Decode decodes a frame, auto-detecting codec on first call if needed.
func (d *AutoDecoder) Decode(encoded *EncodedFrame) (*VideoFrame, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Auto-detect codec on first frame
	if d.decoder == nil {
		codec := DetectVideoCodec(encoded.Data)
		if codec == VideoCodecUnknown {
			return nil, errors.New("cannot detect video codec")
		}

		d.codec = codec
		d.config.Codec = codec

		var err error
		d.decoder, err = CreateVideoDecoder(d.config)
		if err != nil {
			return nil, err
		}
	}

	return d.decoder.Decode(encoded)
}

// Codec returns the detected codec, or VideoCodecUnknown if not yet detected.
func (d *AutoDecoder) Codec() VideoCodec {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.codec
}

// Close closes the underlying decoder.
func (d *AutoDecoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.decoder != nil {
		return d.decoder.Close()
	}
	return nil
}

// Transcoder transcodes video from one codec to another.
type Transcoder struct {
	decoder VideoDecoder
	encoder VideoEncoder
	scaler  *VideoScaler // for dimension mismatch

	inputCodec  VideoCodec
	outputCodec VideoCodec

	config TranscoderConfig
	mu     sync.Mutex
}

// TranscoderConfig configures a transcoder.
type TranscoderConfig struct {
	// Input settings (auto-detected if zero)
	InputCodec VideoCodec

	// Output settings
	OutputCodec VideoCodec
	Width       int
	Height      int
	BitrateBps  int
	FPS         int

	// Optional: specific encoder/decoder configs
	DecoderThreads int
	EncoderThreads int
}

// NewTranscoder creates a new video transcoder.
// If InputCodec is VideoCodecUnknown, it will auto-detect from first frame.
func NewTranscoder(config TranscoderConfig) (*Transcoder, error) {
	if config.OutputCodec == VideoCodecUnknown {
		return nil, errors.New("output codec must be specified")
	}

	// Set defaults
	if config.Width == 0 {
		config.Width = 1920
	}
	if config.Height == 0 {
		config.Height = 1080
	}
	if config.BitrateBps == 0 {
		config.BitrateBps = 2_000_000
	}
	if config.FPS == 0 {
		config.FPS = 30
	}

	t := &Transcoder{
		inputCodec:  config.InputCodec,
		outputCodec: config.OutputCodec,
		config:      config,
	}

	// Create encoder
	encoderConfig := VideoEncoderConfig{
		Width:      config.Width,
		Height:     config.Height,
		BitrateBps: config.BitrateBps,
		FPS:        config.FPS,
		Threads:    config.EncoderThreads,
	}

	var err error
	switch config.OutputCodec {
	case VideoCodecVP8:
		t.encoder, err = NewVP8Encoder(encoderConfig)
	case VideoCodecVP9:
		t.encoder, err = NewVP9Encoder(encoderConfig)
	case VideoCodecH264:
		t.encoder, err = NewH264Encoder(encoderConfig)
	case VideoCodecAV1:
		t.encoder, err = NewAV1Encoder(encoderConfig)
	default:
		return nil, errors.New("unsupported output codec")
	}
	if err != nil {
		return nil, err
	}

	// Create decoder if input codec is known
	if config.InputCodec != VideoCodecUnknown {
		t.decoder, err = t.createDecoder(config.InputCodec)
		if err != nil {
			t.encoder.Close()
			return nil, err
		}
	}

	return t, nil
}

func (t *Transcoder) createDecoder(codec VideoCodec) (VideoDecoder, error) {
	decoderConfig := VideoDecoderConfig{
		Codec:   codec,
		Threads: t.config.DecoderThreads,
	}
	return CreateVideoDecoder(decoderConfig)
}

// Transcode transcodes an encoded frame from input codec to output codec.
// On first call, auto-detects input codec if not specified.
func (t *Transcoder) Transcode(input *EncodedFrame) (*EncodedFrame, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Auto-detect input codec if needed
	if t.decoder == nil {
		codec := t.inputCodec
		if codec == VideoCodecUnknown {
			codec = DetectVideoCodec(input.Data)
			if codec == VideoCodecUnknown {
				return nil, errors.New("cannot detect input codec")
			}
			t.inputCodec = codec
		}

		var err error
		t.decoder, err = t.createDecoder(codec)
		if err != nil {
			return nil, err
		}
	}

	// Decode
	rawFrame, err := t.decoder.Decode(input)
	if err != nil {
		return nil, err
	}
	if rawFrame == nil {
		return nil, nil // Decoder buffering
	}

	// Validate decoded frame has data
	if len(rawFrame.Data) < 3 || len(rawFrame.Data[0]) == 0 || len(rawFrame.Data[1]) == 0 || len(rawFrame.Data[2]) == 0 {
		return nil, nil // Invalid frame data, skip
	}

	// Scale if dimensions don't match encoder's expected dimensions
	frameToEncode := rawFrame
	if rawFrame.Width != t.config.Width || rawFrame.Height != t.config.Height {
		// Create or update scaler
		if t.scaler == nil ||
			t.scaler.srcWidth != rawFrame.Width ||
			t.scaler.srcHeight != rawFrame.Height ||
			t.scaler.dstWidth != t.config.Width ||
			t.scaler.dstHeight != t.config.Height {
			t.scaler = NewVideoScaler(
				rawFrame.Width, rawFrame.Height,
				t.config.Width, t.config.Height,
				ScaleModeStretch,
			)
		}
		frameToEncode = t.scaler.Scale(rawFrame)
	}

	// Encode
	return t.encoder.Encode(frameToEncode)
}

// TranscodeRaw encodes a raw frame (skips decode step).
func (t *Transcoder) TranscodeRaw(frame *VideoFrame) (*EncodedFrame, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.encoder.Encode(frame)
}

// RequestKeyframe requests the encoder to produce a keyframe.
func (t *Transcoder) RequestKeyframe() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.encoder != nil {
		t.encoder.RequestKeyframe()
	}
}

// InputCodec returns the input codec (auto-detected or configured).
func (t *Transcoder) InputCodec() VideoCodec {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.inputCodec
}

// OutputCodec returns the output codec.
func (t *Transcoder) OutputCodec() VideoCodec {
	return t.outputCodec
}

// Close releases all resources.
func (t *Transcoder) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	var errs []error
	if t.decoder != nil {
		if err := t.decoder.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if t.encoder != nil {
		if err := t.encoder.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// Passthrough wraps an encoded stream without transcoding.
// Useful when input and output codecs match.
type Passthrough struct {
	codec VideoCodec
}

// NewPassthrough creates a passthrough for the given codec.
func NewPassthrough(codec VideoCodec) *Passthrough {
	return &Passthrough{codec: codec}
}

// Process returns the frame unchanged.
func (p *Passthrough) Process(frame *EncodedFrame) (*EncodedFrame, error) {
	return frame, nil
}

// Codec returns the codec.
func (p *Passthrough) Codec() VideoCodec {
	return p.codec
}

// Close is a no-op for passthrough.
func (p *Passthrough) Close() error {
	return nil
}

// FrameProcessor is the common interface for Transcoder and Passthrough.
type FrameProcessor interface {
	io.Closer
	OutputCodec() VideoCodec
}

// =============================================================================
// Multi-Output Transcoding
// =============================================================================

// OutputConfig describes a single output stream configuration.
// Use this to configure simulcast, SVC, or multi-codec output.
type OutputConfig struct {
	// ID uniquely identifies this variant (e.g., "720p-vp8", "1080p-h264-svc")
	ID string

	// Codec for this output variant
	Codec VideoCodec

	// Resolution (0 = same as input)
	Width  int
	Height int

	// Bitrate in bits per second
	BitrateBps int

	// Frames per second (0 = same as input)
	FPS int

	// SVC settings (optional, codec must support it)
	// VP8/VP9/AV1 support temporal layers; VP9/AV1 support spatial layers
	TemporalLayers int // 1-4 temporal layers (1 = no temporal SVC)
	SpatialLayers  int // 1-3 spatial layers (1 = no spatial SVC)

	// Quality hint (codec-specific, 0-63 for VP8/VP9)
	Quality int

	// Passthrough skips decode+encode when input codec and resolution match.
	// Use this when you want to forward the original stream without re-encoding.
	// Note: Bitrate settings are ignored when passthrough is active.
	Passthrough bool
}

// SimulcastPreset creates a common simulcast configuration.
// Returns variants for common resolutions: 1080p, 720p, 360p.
func SimulcastPreset(codec VideoCodec, baseBitrate int) []OutputConfig {
	return []OutputConfig{
		{ID: "high", Codec: codec, Width: 1920, Height: 1080, BitrateBps: baseBitrate},
		{ID: "medium", Codec: codec, Width: 1280, Height: 720, BitrateBps: baseBitrate / 2},
		{ID: "low", Codec: codec, Width: 640, Height: 360, BitrateBps: baseBitrate / 4},
	}
}

// SVCPreset creates an SVC configuration with temporal layers.
func SVCPreset(codec VideoCodec, width, height, bitrate, temporalLayers int) []OutputConfig {
	return []OutputConfig{
		{
			ID:             "svc",
			Codec:          codec,
			Width:          width,
			Height:         height,
			BitrateBps:     bitrate,
			TemporalLayers: temporalLayers,
		},
	}
}

// MultiCodecPreset creates variants for multiple codecs at the same resolution.
func MultiCodecPreset(width, height, bitrate int, codecs ...VideoCodec) []OutputConfig {
	variants := make([]OutputConfig, len(codecs))
	for i, codec := range codecs {
		variants[i] = OutputConfig{
			ID:         codec.String(),
			Codec:      codec,
			Width:      width,
			Height:     height,
			BitrateBps: bitrate,
		}
	}
	return variants
}

// VariantFrame holds a single variant's encoded output.
type VariantFrame struct {
	VariantID string
	Frame     *EncodedFrame
	Codec     VideoCodec
}

// TranscodeResult contains all output variants from a single transcode operation.
type TranscodeResult struct {
	Variants []VariantFrame
}

// Get returns the output for a specific variant ID, or nil if not found.
func (r *TranscodeResult) Get(variantID string) *EncodedFrame {
	for _, out := range r.Variants {
		if out.VariantID == variantID {
			return out.Frame
		}
	}
	return nil
}

// outputPipeline holds encoder and optional scaler for a variant.
type outputPipeline struct {
	variant     OutputConfig
	encoder     VideoEncoder // nil if passthrough
	scaler      *VideoScaler
	passthrough bool  // if true, forward input without encode
	removed     int32 // atomic: 1 if marked for removal (encoder may still be in use)
}

// MultiTranscoder transcodes video to multiple output variants simultaneously.
// Supports simulcast (multiple resolutions), SVC (layered output), and multi-codec.
// Also implements VideoSource interface for pipeline integration.
type MultiTranscoder struct {
	decoder    VideoDecoder
	pipelines  map[string]*outputPipeline
	inputCodec VideoCodec

	inputWidth  int
	inputHeight int
	inputFPS    int

	// VideoSource implementation
	source       VideoSource
	sourceConfig SourceConfig
	running      int32 // atomic: 0=stopped, 1=running
	cancel       context.CancelFunc
	frameCh      chan *VideoFrame
	frameCallback VideoFrameCallback

	// Encoding timeout to prevent hangs
	encodeTimeout time.Duration

	// Concurrency control
	inFlightEncodes int32 // atomic: tracks goroutines currently encoding

	mu sync.RWMutex // RWMutex allows concurrent reads (RequestKeyframe, etc)
}

// MultiTranscoderConfig configures a multi-output transcoder.
type MultiTranscoderConfig struct {
	// Input settings (auto-detected if InputCodec is unknown)
	InputCodec  VideoCodec
	InputWidth  int // Hint for scaler setup (0 = detect from first frame)
	InputHeight int
	InputFPS    int

	// Output variants - each produces an independent encoded stream
	Outputs []OutputConfig

	// Optional: decoder thread count
	DecoderThreads int

	// EncodeTimeout is the maximum time to wait for all encoders.
	// Default is 500ms. Set to 0 to wait forever (not recommended).
	EncodeTimeout time.Duration
}

// NewMultiTranscoder creates a transcoder that outputs multiple variants.
// The decoder is shared across all output variants for efficiency.
func NewMultiTranscoder(config MultiTranscoderConfig) (*MultiTranscoder, error) {
	if len(config.Outputs) == 0 {
		return nil, errors.New("at least one output variant required")
	}

	// Validate unique IDs
	seen := make(map[string]bool)
	for _, out := range config.Outputs {
		if out.ID == "" {
			return nil, errors.New("output variant ID cannot be empty")
		}
		if seen[out.ID] {
			return nil, errors.New("duplicate output variant ID: " + out.ID)
		}
		seen[out.ID] = true
		if out.Codec == VideoCodecUnknown {
			return nil, errors.New("output codec must be specified for variant: " + out.ID)
		}
	}

	encodeTimeout := config.EncodeTimeout
	if encodeTimeout == 0 {
		encodeTimeout = 500 * time.Millisecond // Default timeout
	}

	mt := &MultiTranscoder{
		inputCodec:    config.InputCodec,
		inputWidth:    config.InputWidth,
		inputHeight:   config.InputHeight,
		inputFPS:      config.InputFPS,
		pipelines:     make(map[string]*outputPipeline),
		encodeTimeout: encodeTimeout,
	}

	// Create encoders for each variant (skip for passthrough)
	for _, variant := range config.Outputs {
		pipeline := &outputPipeline{variant: variant, passthrough: variant.Passthrough}

		// For passthrough variants, no encoder needed
		if variant.Passthrough {
			mt.pipelines[variant.ID] = pipeline
			continue
		}

		// Determine output dimensions
		w, h := variant.Width, variant.Height
		if w == 0 {
			w = config.InputWidth
		}
		if h == 0 {
			h = config.InputHeight
		}
		if w == 0 {
			w = 1920 // Default
		}
		if h == 0 {
			h = 1080
		}

		fps := variant.FPS
		if fps == 0 {
			fps = config.InputFPS
		}
		if fps == 0 {
			fps = 30
		}

		bitrate := variant.BitrateBps
		if bitrate == 0 {
			bitrate = 2_000_000
		}

		encoderConfig := VideoEncoderConfig{
			Codec:          variant.Codec,
			Width:          w,
			Height:         h,
			FPS:            fps,
			BitrateBps:     bitrate,
			Quality:        variant.Quality,
			TemporalLayers: variant.TemporalLayers,
			SpatialLayers:  variant.SpatialLayers,
		}

		encoder, err := CreateVideoEncoder(encoderConfig)
		if err != nil {
			// Clean up already created encoders
			for _, p := range mt.pipelines {
				if p.encoder != nil {
					p.encoder.Close()
				}
			}
			return nil, fmt.Errorf("failed to create encoder for %s: %w", variant.ID, err)
		}
		pipeline.encoder = encoder
		mt.pipelines[variant.ID] = pipeline
	}

	// Create decoder if input codec is known
	if config.InputCodec != VideoCodecUnknown {
		decoder, err := CreateVideoDecoder(VideoDecoderConfig{
			Codec:   config.InputCodec,
			Threads: config.DecoderThreads,
		})
		if err != nil {
			mt.Close()
			return nil, fmt.Errorf("failed to create decoder: %w", err)
		}
		mt.decoder = decoder
	}

	return mt, nil
}

// Transcode decodes the input and encodes to all configured output variants.
// Auto-detects input codec on first call if not specified.
// Returns nil results for variants where encoding produced no output (buffering).
// Passthrough variants skip decode+encode when input codec matches.
func (mt *MultiTranscoder) Transcode(input *EncodedFrame) (*TranscodeResult, error) {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	// Auto-detect input codec if needed
	if mt.inputCodec == VideoCodecUnknown {
		codec := DetectVideoCodec(input.Data)
		if codec == VideoCodecUnknown {
			return nil, errors.New("cannot detect input codec")
		}
		mt.inputCodec = codec
	}

	result := &TranscodeResult{
		Variants: make([]VariantFrame, 0, len(mt.pipelines)),
	}

	// Check if we have any passthrough variants that match
	hasPassthrough := false
	needsDecode := false
	for _, pipeline := range mt.pipelines {
		if pipeline.passthrough && pipeline.variant.Codec == mt.inputCodec {
			hasPassthrough = true
		} else if !pipeline.passthrough {
			needsDecode = true
		}
	}

	// Handle passthrough variants (no decode/encode needed)
	if hasPassthrough {
		for id, pipeline := range mt.pipelines {
			if pipeline.passthrough && pipeline.variant.Codec == mt.inputCodec {
				// Forward input directly - zero CPU cost
				result.Variants = append(result.Variants, VariantFrame{
					VariantID: id,
					Frame:     input,
					Codec:     pipeline.variant.Codec,
				})
			}
		}
	}

	// If no variants need decoding, we're done
	if !needsDecode {
		return result, nil
	}

	// Create decoder on first use
	if mt.decoder == nil {
		decoder, err := CreateVideoDecoder(VideoDecoderConfig{Codec: mt.inputCodec})
		if err != nil {
			return nil, fmt.Errorf("failed to create decoder: %w", err)
		}
		mt.decoder = decoder
	}

	// Decode
	rawFrame, err := mt.decoder.Decode(input)
	if err != nil {
		return nil, err
	}
	if rawFrame == nil {
		// Decoder buffering - return any passthrough results we have
		if len(result.Variants) > 0 {
			return result, nil
		}
		return nil, nil
	}

	// Update input dimensions from decoded frame
	if mt.inputWidth == 0 {
		mt.inputWidth = rawFrame.Width
		mt.inputHeight = rawFrame.Height
	}

	// Encode non-passthrough variants
	encodeResult, err := mt.transcodeRawNonPassthrough(rawFrame)
	if err != nil {
		return nil, err
	}

	// Merge encode results with passthrough results
	result.Variants = append(result.Variants, encodeResult.Variants...)
	return result, nil
}

// TranscodeRaw encodes a raw frame to all output variants (skips decode step).
// Useful when input is already decoded or from a capture source.
func (mt *MultiTranscoder) TranscodeRaw(frame *VideoFrame) (*TranscodeResult, error) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	return mt.transcodeRaw(frame)
}

func (mt *MultiTranscoder) transcodeRaw(frame *VideoFrame) (*TranscodeResult, error) {
	return mt.transcodeRawNonPassthrough(frame)
}

// transcodeRawNonPassthrough encodes raw frame to non-passthrough variants only.
func (mt *MultiTranscoder) transcodeRawNonPassthrough(frame *VideoFrame) (*TranscodeResult, error) {
	// Count non-passthrough pipelines (excluding removed ones)
	var nonPassthrough []*outputPipeline
	var ids []string
	for id, pipeline := range mt.pipelines {
		// Skip removed or passthrough pipelines
		if atomic.LoadInt32(&pipeline.removed) == 1 {
			continue
		}
		if !pipeline.passthrough && pipeline.encoder != nil {
			nonPassthrough = append(nonPassthrough, pipeline)
			ids = append(ids, id)
		}
	}

	if len(nonPassthrough) == 0 {
		return &TranscodeResult{}, nil
	}

	// For single output, encode directly without goroutine overhead
	if len(nonPassthrough) == 1 {
		return mt.transcodeRawSinglePipeline(frame, ids[0], nonPassthrough[0])
	}

	// For multiple outputs, encode in parallel for better performance
	return mt.transcodeRawParallelPipelines(frame, ids, nonPassthrough)
}

// transcodeRawSinglePipeline handles single pipeline without goroutine overhead.
func (mt *MultiTranscoder) transcodeRawSinglePipeline(frame *VideoFrame, id string, pipeline *outputPipeline) (*TranscodeResult, error) {
	result := &TranscodeResult{
		Variants: make([]VariantFrame, 0, 1),
	}

	inputFrame := mt.prepareInputFrame(frame, pipeline)
	encoded, err := pipeline.encoder.Encode(inputFrame)
	if err != nil {
		return nil, fmt.Errorf("encode error for %s: %w", id, err)
	}

	if encoded != nil {
		result.Variants = append(result.Variants, VariantFrame{
			VariantID: id,
			Frame:     encoded,
			Codec:     pipeline.variant.Codec,
		})
	}

	return result, nil
}

// transcodeRawParallelPipelines encodes to multiple pipelines concurrently.
func (mt *MultiTranscoder) transcodeRawParallelPipelines(frame *VideoFrame, ids []string, pipelines []*outputPipeline) (*TranscodeResult, error) {
	numPipelines := len(pipelines)

	// Check if too many encodes are already in flight (prevents goroutine accumulation)
	const maxInFlight = 100 // Max concurrent encoder goroutines
	if atomic.LoadInt32(&mt.inFlightEncodes) > maxInFlight {
		return nil, errors.New("too many in-flight encodes, dropping frame")
	}

	type encodeResult struct {
		id    string
		frame *EncodedFrame
		codec VideoCodec
		err   error
	}

	resultCh := make(chan encodeResult, numPipelines)

	// Launch parallel encoders
	for i, pipeline := range pipelines {
		atomic.AddInt32(&mt.inFlightEncodes, 1)
		go func(variantID string, p *outputPipeline) {
			defer atomic.AddInt32(&mt.inFlightEncodes, -1)

			// Check if pipeline was removed before we start encoding
			if atomic.LoadInt32(&p.removed) == 1 {
				resultCh <- encodeResult{id: variantID, err: errors.New("pipeline removed")}
				return
			}

			inputFrame := mt.prepareInputFrame(frame, p)

			// Check again after scaling (which can be slow)
			if atomic.LoadInt32(&p.removed) == 1 {
				resultCh <- encodeResult{id: variantID, err: errors.New("pipeline removed")}
				return
			}

			encoded, err := p.encoder.Encode(inputFrame)
			resultCh <- encodeResult{
				id:    variantID,
				frame: encoded,
				codec: p.variant.Codec,
				err:   err,
			}
		}(ids[i], pipeline)
	}

	// Collect results with timeout
	result := &TranscodeResult{
		Variants: make([]VariantFrame, 0, numPipelines),
	}

	var firstErr error
	timeout := time.After(mt.encodeTimeout)
	received := 0

	for received < numPipelines {
		select {
		case res := <-resultCh:
			received++
			if res.err != nil && firstErr == nil {
				firstErr = fmt.Errorf("encode error for %s: %w", res.id, res.err)
				continue
			}
			if res.frame != nil {
				result.Variants = append(result.Variants, VariantFrame{
					VariantID: res.id,
					Frame:     res.frame,
					Codec:     res.codec,
				})
			}
		case <-timeout:
			// Some encoders timed out - return what we have
			if firstErr == nil {
				firstErr = fmt.Errorf("encode timeout: got %d/%d results", received, numPipelines)
			}
			// Return partial results so we don't block
			if len(result.Variants) > 0 {
				return result, nil // Return partial success
			}
			return nil, firstErr
		}
	}

	if firstErr != nil {
		return nil, firstErr
	}

	return result, nil
}

// prepareInputFrame scales the frame if needed for the target pipeline.
func (mt *MultiTranscoder) prepareInputFrame(frame *VideoFrame, pipeline *outputPipeline) *VideoFrame {
	targetW := pipeline.variant.Width
	targetH := pipeline.variant.Height
	if targetW == 0 {
		targetW = frame.Width
	}
	if targetH == 0 {
		targetH = frame.Height
	}

	// No scaling needed
	if frame.Width == targetW && frame.Height == targetH {
		return frame
	}

	// Create or update scaler (thread-safe: each pipeline has its own scaler)
	if pipeline.scaler == nil ||
		pipeline.scaler.srcWidth != frame.Width ||
		pipeline.scaler.srcHeight != frame.Height ||
		pipeline.scaler.dstWidth != targetW ||
		pipeline.scaler.dstHeight != targetH {
		pipeline.scaler = NewVideoScaler(
			frame.Width, frame.Height,
			targetW, targetH,
			ScaleModeStretch,
		)
	}
	return pipeline.scaler.Scale(frame)
}

// RequestKeyframe requests a keyframe from a specific variant.
func (mt *MultiTranscoder) RequestKeyframe(variantID string) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	if pipeline, ok := mt.pipelines[variantID]; ok && pipeline.encoder != nil {
		pipeline.encoder.RequestKeyframe()
	}
}

// RequestKeyframeAll requests keyframes from all variants.
func (mt *MultiTranscoder) RequestKeyframeAll() {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	for _, pipeline := range mt.pipelines {
		if pipeline.encoder != nil {
			pipeline.encoder.RequestKeyframe()
		}
	}
}

// SetBitrate updates the bitrate for a specific variant.
func (mt *MultiTranscoder) SetBitrate(variantID string, bitrateBps int) error {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	pipeline, ok := mt.pipelines[variantID]
	if !ok {
		return errors.New("variant not found: " + variantID)
	}
	if pipeline.encoder == nil {
		return errors.New("cannot set bitrate on passthrough variant: " + variantID)
	}
	return pipeline.encoder.SetBitrate(bitrateBps)
}

// Variants returns the list of output variant IDs.
func (mt *MultiTranscoder) Variants() []string {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	ids := make([]string, 0, len(mt.pipelines))
	for id := range mt.pipelines {
		ids = append(ids, id)
	}
	return ids
}

// VariantConfig returns the configuration for a specific variant.
func (mt *MultiTranscoder) VariantConfig(variantID string) (OutputConfig, bool) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	if pipeline, ok := mt.pipelines[variantID]; ok {
		return pipeline.variant, true
	}
	return OutputConfig{}, false
}

// AddOutput adds a new output variant dynamically.
// The new variant will be available for the next Transcode call.
// Thread-safe: can be called while transcoding is in progress.
func (mt *MultiTranscoder) AddOutput(config OutputConfig) error {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	if config.ID == "" {
		return errors.New("output variant ID cannot be empty")
	}
	if _, exists := mt.pipelines[config.ID]; exists {
		return errors.New("output variant already exists: " + config.ID)
	}
	if config.Codec == VideoCodecUnknown {
		return errors.New("output codec must be specified")
	}

	pipeline := &outputPipeline{variant: config, passthrough: config.Passthrough}

	// For passthrough variants, no encoder needed
	if config.Passthrough {
		mt.pipelines[config.ID] = pipeline
		return nil
	}

	// Determine output dimensions
	w, h := config.Width, config.Height
	if w == 0 {
		w = mt.inputWidth
	}
	if h == 0 {
		h = mt.inputHeight
	}
	if w == 0 {
		w = 1920
	}
	if h == 0 {
		h = 1080
	}

	fps := config.FPS
	if fps == 0 {
		fps = mt.inputFPS
	}
	if fps == 0 {
		fps = 30
	}

	bitrate := config.BitrateBps
	if bitrate == 0 {
		bitrate = 2_000_000
	}

	encoderConfig := VideoEncoderConfig{
		Codec:          config.Codec,
		Width:          w,
		Height:         h,
		FPS:            fps,
		BitrateBps:     bitrate,
		Quality:        config.Quality,
		TemporalLayers: config.TemporalLayers,
		SpatialLayers:  config.SpatialLayers,
	}

	encoder, err := CreateVideoEncoder(encoderConfig)
	if err != nil {
		return fmt.Errorf("failed to create encoder for %s: %w", config.ID, err)
	}

	pipeline.encoder = encoder
	mt.pipelines[config.ID] = pipeline
	return nil
}

// RemoveOutput removes an output variant dynamically.
// Thread-safe: can be called while transcoding is in progress.
// The variant will no longer appear in TranscodeResult after removal.
// Note: If encoding is in progress, the encoder is marked for removal and
// will be cleaned up on the next Transcode call.
func (mt *MultiTranscoder) RemoveOutput(variantID string) error {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	pipeline, exists := mt.pipelines[variantID]
	if !exists {
		return errors.New("variant not found: " + variantID)
	}

	// Mark as removed - encoding goroutines will skip this pipeline
	atomic.StoreInt32(&pipeline.removed, 1)

	// Try to close encoder, but don't block if it's in use
	// The encoder will be cleaned up on the next Transcode call
	go func(p *outputPipeline) {
		// Give in-flight encoding a moment to complete
		time.Sleep(100 * time.Millisecond)
		if p.encoder != nil {
			p.encoder.Close()
		}
	}(pipeline)

	delete(mt.pipelines, variantID)
	return nil
}

// OutputCount returns the number of active output variants.
func (mt *MultiTranscoder) OutputCount() int {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	return len(mt.pipelines)
}

// InputCodec returns the detected or configured input codec.
func (mt *MultiTranscoder) InputCodec() VideoCodec {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	return mt.inputCodec
}

// Close releases all resources.
func (mt *MultiTranscoder) Close() error {
	mt.Stop() // Stop if running

	mt.mu.Lock()
	defer mt.mu.Unlock()

	var errs []error
	if mt.decoder != nil {
		if err := mt.decoder.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	for _, pipeline := range mt.pipelines {
		if pipeline.encoder != nil {
			if err := pipeline.encoder.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// =============================================================================
// VideoSource Interface Implementation
// =============================================================================

// SetSource sets a VideoSource for pull-mode input.
// When Start() is called, frames will be read from this source.
func (mt *MultiTranscoder) SetSource(source VideoSource) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	mt.source = source
	if source != nil {
		cfg := source.Config()
		mt.sourceConfig = SourceConfig{
			Width:      cfg.Width,
			Height:     cfg.Height,
			FPS:        cfg.FPS,
			Format:     PixelFormatI420,
			SourceType: SourceTypeCustom,
		}
		if mt.inputWidth == 0 {
			mt.inputWidth = cfg.Width
		}
		if mt.inputHeight == 0 {
			mt.inputHeight = cfg.Height
		}
		if mt.inputFPS == 0 {
			mt.inputFPS = cfg.FPS
		}
	}
}

// Start begins reading from the source and transcoding.
// Decoded frames are available via ReadFrame() or SetCallback().
func (mt *MultiTranscoder) Start(ctx context.Context) error {
	if !atomic.CompareAndSwapInt32(&mt.running, 0, 1) {
		return errors.New("already running")
	}

	mt.mu.Lock()
	source := mt.source
	mt.frameCh = make(chan *VideoFrame, 5)
	ctx, mt.cancel = context.WithCancel(ctx)
	mt.mu.Unlock()

	if source == nil {
		atomic.StoreInt32(&mt.running, 0)
		return errors.New("no source set")
	}

	if err := source.Start(ctx); err != nil {
		atomic.StoreInt32(&mt.running, 0)
		return err
	}

	go mt.processLoop(ctx, source)
	return nil
}

// processLoop reads frames from source and delivers decoded frames.
func (mt *MultiTranscoder) processLoop(ctx context.Context, source VideoSource) {
	defer func() {
		atomic.StoreInt32(&mt.running, 0)
		mt.mu.Lock()
		if mt.frameCh != nil {
			close(mt.frameCh)
		}
		mt.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		frame, err := source.ReadFrame(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		if frame == nil {
			continue
		}

		// Clone frame to ensure it outlives the source buffer
		clonedFrame := frame.Clone()

		// Deliver via callback if set
		mt.mu.Lock()
		cb := mt.frameCallback
		mt.mu.Unlock()

		if cb != nil {
			cb(clonedFrame)
		}

		// Also deliver via channel
		select {
		case mt.frameCh <- clonedFrame:
		default:
			// Drop frame if buffer full
		}
	}
}

// Stop halts the processing loop.
func (mt *MultiTranscoder) Stop() error {
	if !atomic.CompareAndSwapInt32(&mt.running, 1, 0) {
		return nil // Already stopped
	}

	mt.mu.Lock()
	cancel := mt.cancel
	source := mt.source
	mt.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if source != nil {
		source.Stop()
	}
	return nil
}

// ReadFrame reads the next decoded frame from the transcoder.
// This is useful when using the transcoder as a VideoSource in a pipeline.
func (mt *MultiTranscoder) ReadFrame(ctx context.Context) (*VideoFrame, error) {
	mt.mu.Lock()
	ch := mt.frameCh
	mt.mu.Unlock()

	if ch == nil {
		return nil, errors.New("not running")
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case frame, ok := <-ch:
		if !ok {
			return nil, io.EOF
		}
		return frame, nil
	}
}

// ReadFrameInto reads the next frame into a pre-allocated buffer.
// Optional: returns ErrNotSupported as this implementation uses Clone().
func (mt *MultiTranscoder) ReadFrameInto(ctx context.Context, buf *VideoFrameBuffer) error {
	return ErrNotSupported
}

// SetCallback sets push-mode callback for decoded frame delivery.
func (mt *MultiTranscoder) SetCallback(cb VideoFrameCallback) {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	mt.frameCallback = cb
}

// Config returns the source configuration for this transcoder.
// Implements VideoSource interface.
func (mt *MultiTranscoder) Config() SourceConfig {
	mt.mu.Lock()
	defer mt.mu.Unlock()
	return mt.sourceConfig
}

// =============================================================================
// Adaptive Bitrate Helper
// =============================================================================

// ABRController provides adaptive bitrate control for multi-output transcoding.
// It monitors network conditions and adjusts variant selection.
type ABRController struct {
	transcoder *MultiTranscoder
	variants   []string // Ordered by quality (highest first)
	current    int      // Current variant index
	mu         sync.Mutex
}

// NewABRController creates an ABR controller for the given multi-transcoder.
// Variants should be ordered by quality (highest bitrate first).
func NewABRController(mt *MultiTranscoder, variantOrder []string) *ABRController {
	return &ABRController{
		transcoder: mt,
		variants:   variantOrder,
		current:    0,
	}
}

// CurrentVariant returns the currently selected variant ID.
func (c *ABRController) CurrentVariant() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current < len(c.variants) {
		return c.variants[c.current]
	}
	return ""
}

// StepDown moves to a lower quality variant.
// Returns false if already at lowest quality.
func (c *ABRController) StepDown() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current < len(c.variants)-1 {
		c.current++
		return true
	}
	return false
}

// StepUp moves to a higher quality variant.
// Returns false if already at highest quality.
func (c *ABRController) StepUp() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current > 0 {
		c.current--
		return true
	}
	return false
}

// SetVariant sets the current variant by ID.
func (c *ABRController) SetVariant(variantID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, v := range c.variants {
		if v == variantID {
			c.current = i
			return true
		}
	}
	return false
}

// SelectOutput picks the appropriate output from a TranscodeResult.
func (c *ABRController) SelectOutput(result *TranscodeResult) *VariantFrame {
	c.mu.Lock()
	variantID := ""
	if c.current < len(c.variants) {
		variantID = c.variants[c.current]
	}
	c.mu.Unlock()

	for i := range result.Variants {
		if result.Variants[i].VariantID == variantID {
			return &result.Variants[i]
		}
	}

	// Fallback to first available
	if len(result.Variants) > 0 {
		return &result.Variants[0]
	}
	return nil
}

// =============================================================================
// Audio/Video Muxing and Sync
// =============================================================================

// SyncedMedia represents synchronized audio with multiple video outputs.
// This is the common case for simulcast: one audio stream paired with
// multiple video variants at different resolutions/codecs.
type SyncedMedia struct {
	// Audio is the shared encoded audio (same for all video variants)
	Audio      *EncodedAudio
	AudioCodec AudioCodec

	// Videos contains all video variant outputs synced to this audio
	Videos []VariantFrame

	// Timestamp is the unified presentation timestamp in nanoseconds
	Timestamp int64
}

// GetVideo returns the video for a specific variant ID, or nil if not found.
func (o *SyncedMedia) GetVideo(variantID string) *EncodedFrame {
	for _, v := range o.Videos {
		if v.VariantID == variantID {
			return v.Frame
		}
	}
	return nil
}

// AudioConfig describes shared audio encoding settings.
type AudioConfig struct {
	Codec      AudioCodec
	BitrateBps int
	SampleRate int
	Channels   int
}

// DefaultAudioConfig returns sensible defaults for audio encoding.
func DefaultAudioConfig() AudioConfig {
	return AudioConfig{
		Codec:      AudioCodecOpus,
		BitrateBps: 64000,
		SampleRate: 48000,
		Channels:   2,
	}
}

// SyncMode controls how audio/video synchronization is handled.
type SyncMode int

const (
	// SyncStrict waits for both audio and video before outputting.
	// Provides best sync but may increase latency.
	SyncStrict SyncMode = iota

	// SyncLoose outputs audio and video independently.
	// Lower latency but may have slight A/V drift.
	SyncLoose

	// SyncVideoMaster uses video timestamps as reference.
	// Audio is adjusted to match video timing.
	SyncVideoMaster

	// SyncAudioMaster uses audio timestamps as reference.
	// Video frames may be dropped/duplicated to match audio.
	SyncAudioMaster
)

// MediaMuxer synchronizes one audio stream with multiple video outputs.
// Use this for simulcast scenarios where all video variants share audio.
type MediaMuxer struct {
	videoTranscoder *MultiTranscoder
	audioEncoder    AudioEncoder

	// Sync configuration
	syncMode       SyncMode
	maxDriftMs     int64
	videoTimeBase  int64 // ns per video timestamp unit (90kHz = 11111ns)
	audioTimeBase  int64 // ns per audio timestamp unit (48kHz = 20833ns)

	// Buffering
	pendingVideos []*TranscodeResult
	pendingAudio  []*EncodedAudio

	// State
	lastVideoTs int64
	lastAudioTs int64

	config MuxerConfig
	mu     sync.Mutex
}

// MuxerConfig configures an audio/video muxer.
type MuxerConfig struct {
	// Video input settings
	VideoInputCodec  VideoCodec
	VideoInputWidth  int
	VideoInputHeight int
	VideoInputFPS    int

	// Video output variants (simulcast, multi-codec, etc.)
	VideoOutputs []OutputConfig

	// Shared audio settings (one audio stream for all video variants)
	Audio AudioConfig

	// Sync settings
	SyncMode   SyncMode
	MaxDriftMs int // Maximum A/V drift before correction (default: 40ms)

	// Buffer settings
	MaxBufferedFrames int // Max frames to buffer for sync (default: 5)
}

// NewMediaMuxer creates an audio/video muxer with shared audio.
func NewMediaMuxer(config MuxerConfig) (*MediaMuxer, error) {
	if len(config.VideoOutputs) == 0 {
		return nil, errors.New("at least one video output required")
	}

	// Defaults
	if config.MaxDriftMs == 0 {
		config.MaxDriftMs = 40
	}
	if config.MaxBufferedFrames == 0 {
		config.MaxBufferedFrames = 5
	}
	if config.Audio.Codec == AudioCodecUnknown {
		config.Audio = DefaultAudioConfig()
	}

	// Create video transcoder
	videoTranscoder, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec:  config.VideoInputCodec,
		InputWidth:  config.VideoInputWidth,
		InputHeight: config.VideoInputHeight,
		InputFPS:    config.VideoInputFPS,
		Outputs:     config.VideoOutputs,
	})
	if err != nil {
		return nil, fmt.Errorf("video transcoder: %w", err)
	}

	// Create single audio encoder (shared across all variants)
	audioEncoder, err := CreateAudioEncoder(AudioEncoderConfig{
		Codec:      config.Audio.Codec,
		SampleRate: config.Audio.SampleRate,
		Channels:   config.Audio.Channels,
		BitrateBps: config.Audio.BitrateBps,
	})
	if err != nil {
		videoTranscoder.Close()
		return nil, fmt.Errorf("audio encoder: %w", err)
	}

	return &MediaMuxer{
		videoTranscoder: videoTranscoder,
		audioEncoder:    audioEncoder,
		syncMode:        config.SyncMode,
		maxDriftMs:      int64(config.MaxDriftMs),
		videoTimeBase:   1000000000 / 90000,  // 90kHz -> ~11111 ns per tick
		audioTimeBase:   1000000000 / 48000,  // 48kHz -> ~20833 ns per tick
		pendingVideos:   make([]*TranscodeResult, 0, config.MaxBufferedFrames),
		pendingAudio:    make([]*EncodedAudio, 0, config.MaxBufferedFrames),
		config:          config,
	}, nil
}

// PushVideo transcodes and buffers a video frame.
func (m *MediaMuxer) PushVideo(frame *EncodedFrame) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	result, err := m.videoTranscoder.Transcode(frame)
	if err != nil {
		return err
	}
	if result != nil && len(result.Variants) > 0 {
		m.pendingVideos = append(m.pendingVideos, result)
		// Update last video timestamp
		if len(result.Variants) > 0 && result.Variants[0].Frame != nil {
			m.lastVideoTs = int64(result.Variants[0].Frame.Timestamp) * m.videoTimeBase
		}
	}

	// Trim buffer if too large
	if len(m.pendingVideos) > m.config.MaxBufferedFrames {
		m.pendingVideos = m.pendingVideos[1:]
	}

	return nil
}

// PushVideoRaw transcodes and buffers a raw video frame.
func (m *MediaMuxer) PushVideoRaw(frame *VideoFrame) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	result, err := m.videoTranscoder.TranscodeRaw(frame)
	if err != nil {
		return err
	}
	if result != nil && len(result.Variants) > 0 {
		m.pendingVideos = append(m.pendingVideos, result)
		if len(result.Variants) > 0 && result.Variants[0].Frame != nil {
			m.lastVideoTs = int64(result.Variants[0].Frame.Timestamp) * m.videoTimeBase
		}
	}

	if len(m.pendingVideos) > m.config.MaxBufferedFrames {
		m.pendingVideos = m.pendingVideos[1:]
	}

	return nil
}

// PushAudio encodes and buffers audio samples.
func (m *MediaMuxer) PushAudio(samples *AudioSamples) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	encoded, err := m.audioEncoder.Encode(samples)
	if err != nil {
		return err
	}
	if encoded != nil {
		m.pendingAudio = append(m.pendingAudio, encoded)
		m.lastAudioTs = int64(encoded.Timestamp) * m.audioTimeBase
	}

	if len(m.pendingAudio) > m.config.MaxBufferedFrames {
		m.pendingAudio = m.pendingAudio[1:]
	}

	return nil
}

// Pull retrieves synchronized A/V output.
// Returns nil if no synchronized output is available yet.
func (m *MediaMuxer) Pull() *SyncedMedia {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch m.syncMode {
	case SyncLoose:
		return m.pullLoose()
	case SyncVideoMaster:
		return m.pullVideoMaster()
	case SyncAudioMaster:
		return m.pullAudioMaster()
	default: // SyncStrict
		return m.pullStrict()
	}
}

// pullStrict waits for both audio and video, matching timestamps.
func (m *MediaMuxer) pullStrict() *SyncedMedia {
	if len(m.pendingVideos) == 0 || len(m.pendingAudio) == 0 {
		return nil
	}

	video := m.pendingVideos[0]
	audio := m.pendingAudio[0]

	// Get timestamps
	var videoTs int64
	if len(video.Variants) > 0 && video.Variants[0].Frame != nil {
		videoTs = int64(video.Variants[0].Frame.Timestamp) * m.videoTimeBase
	}
	audioTs := int64(audio.Timestamp) * m.audioTimeBase

	// Check drift
	drift := abs64(videoTs - audioTs) / 1e6 // ms
	if drift > m.maxDriftMs {
		// Drop the older one
		if videoTs < audioTs {
			m.pendingVideos = m.pendingVideos[1:]
		} else {
			m.pendingAudio = m.pendingAudio[1:]
		}
		return nil
	}

	// Consume both
	m.pendingVideos = m.pendingVideos[1:]
	m.pendingAudio = m.pendingAudio[1:]

	return &SyncedMedia{
		Audio:      audio,
		AudioCodec: m.config.Audio.Codec,
		Videos:     video.Variants,
		Timestamp:  videoTs,
	}
}

// pullLoose returns whatever is available without strict sync.
func (m *MediaMuxer) pullLoose() *SyncedMedia {
	if len(m.pendingVideos) == 0 && len(m.pendingAudio) == 0 {
		return nil
	}

	out := &SyncedMedia{
		AudioCodec: m.config.Audio.Codec,
	}

	if len(m.pendingVideos) > 0 {
		video := m.pendingVideos[0]
		m.pendingVideos = m.pendingVideos[1:]
		out.Videos = video.Variants
		if len(video.Variants) > 0 && video.Variants[0].Frame != nil {
			out.Timestamp = int64(video.Variants[0].Frame.Timestamp) * m.videoTimeBase
		}
	}

	if len(m.pendingAudio) > 0 {
		out.Audio = m.pendingAudio[0]
		m.pendingAudio = m.pendingAudio[1:]
		if out.Timestamp == 0 {
			out.Timestamp = int64(out.Audio.Timestamp) * m.audioTimeBase
		}
	}

	return out
}

// pullVideoMaster outputs at video rate, matching audio.
func (m *MediaMuxer) pullVideoMaster() *SyncedMedia {
	if len(m.pendingVideos) == 0 {
		return nil
	}

	video := m.pendingVideos[0]
	m.pendingVideos = m.pendingVideos[1:]

	var videoTs int64
	if len(video.Variants) > 0 && video.Variants[0].Frame != nil {
		videoTs = int64(video.Variants[0].Frame.Timestamp) * m.videoTimeBase
	}

	out := &SyncedMedia{
		Videos:     video.Variants,
		AudioCodec: m.config.Audio.Codec,
		Timestamp:  videoTs,
	}

	// Find closest audio
	if len(m.pendingAudio) > 0 {
		bestIdx := 0
		bestDiff := int64(1 << 62)
		for i, a := range m.pendingAudio {
			audioTs := int64(a.Timestamp) * m.audioTimeBase
			diff := abs64(videoTs - audioTs)
			if diff < bestDiff {
				bestDiff = diff
				bestIdx = i
			}
		}

		// Use if within drift tolerance
		if bestDiff/1e6 <= m.maxDriftMs {
			out.Audio = m.pendingAudio[bestIdx]
			m.pendingAudio = append(m.pendingAudio[:bestIdx], m.pendingAudio[bestIdx+1:]...)
		}
	}

	return out
}

// pullAudioMaster outputs at audio rate, matching video.
func (m *MediaMuxer) pullAudioMaster() *SyncedMedia {
	if len(m.pendingAudio) == 0 {
		return nil
	}

	audio := m.pendingAudio[0]
	m.pendingAudio = m.pendingAudio[1:]
	audioTs := int64(audio.Timestamp) * m.audioTimeBase

	out := &SyncedMedia{
		Audio:      audio,
		AudioCodec: m.config.Audio.Codec,
		Timestamp:  audioTs,
	}

	// Find closest video
	if len(m.pendingVideos) > 0 {
		bestIdx := 0
		bestDiff := int64(1 << 62)
		for i, v := range m.pendingVideos {
			if len(v.Variants) > 0 && v.Variants[0].Frame != nil {
				videoTs := int64(v.Variants[0].Frame.Timestamp) * m.videoTimeBase
				diff := abs64(audioTs - videoTs)
				if diff < bestDiff {
					bestDiff = diff
					bestIdx = i
				}
			}
		}

		if bestDiff/1e6 <= m.maxDriftMs {
			out.Videos = m.pendingVideos[bestIdx].Variants
			m.pendingVideos = append(m.pendingVideos[:bestIdx], m.pendingVideos[bestIdx+1:]...)
		}
	}

	return out
}

// Flush returns all remaining buffered output.
func (m *MediaMuxer) Flush() []*SyncedMedia {
	m.mu.Lock()
	defer m.mu.Unlock()

	var results []*SyncedMedia

	// Drain using loose mode
	origMode := m.syncMode
	m.syncMode = SyncLoose
	for {
		out := m.pullLoose()
		if out == nil {
			break
		}
		results = append(results, out)
	}
	m.syncMode = origMode

	return results
}

// RequestVideoKeyframe requests a keyframe from a specific video variant.
func (m *MediaMuxer) RequestVideoKeyframe(variantID string) {
	m.videoTranscoder.RequestKeyframe(variantID)
}

// RequestVideoKeyframeAll requests keyframes from all video variants.
func (m *MediaMuxer) RequestVideoKeyframeAll() {
	m.videoTranscoder.RequestKeyframeAll()
}

// SetVideoBitrate updates the bitrate for a specific video variant.
func (m *MediaMuxer) SetVideoBitrate(variantID string, bitrateBps int) error {
	return m.videoTranscoder.SetBitrate(variantID, bitrateBps)
}

// VideoVariants returns the list of video variant IDs.
func (m *MediaMuxer) VideoVariants() []string {
	return m.videoTranscoder.Variants()
}

// Stats returns current buffer status.
func (m *MediaMuxer) Stats() (pendingVideo, pendingAudio int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.pendingVideos), len(m.pendingAudio)
}

// Close releases all resources.
func (m *MediaMuxer) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error

	if m.videoTranscoder != nil {
		if err := m.videoTranscoder.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if m.audioEncoder != nil {
		if err := m.audioEncoder.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
