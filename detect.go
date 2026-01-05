package media

import (
	"errors"
	"sync"
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

// =============================================================================
// Auto-Detecting Decoder
// =============================================================================

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
		d.decoder, err = NewVideoDecoder(d.config)
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
