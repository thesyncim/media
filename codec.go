package media

// VideoCodec identifies the video codec type.
type VideoCodec int

const (
	VideoCodecUnknown VideoCodec = iota
	VideoCodecVP8
	VideoCodecVP9
	VideoCodecH264
	VideoCodecH265
	VideoCodecAV1
)

func (c VideoCodec) String() string {
	switch c {
	case VideoCodecVP8:
		return "VP8"
	case VideoCodecVP9:
		return "VP9"
	case VideoCodecH264:
		return "H264"
	case VideoCodecH265:
		return "H265"
	case VideoCodecAV1:
		return "AV1"
	default:
		return "Unknown"
	}
}

// MimeType returns the MIME type for this codec.
func (c VideoCodec) MimeType() string {
	switch c {
	case VideoCodecVP8:
		return "video/VP8"
	case VideoCodecVP9:
		return "video/VP9"
	case VideoCodecH264:
		return "video/H264"
	case VideoCodecH265:
		return "video/H265"
	case VideoCodecAV1:
		return "video/AV1"
	default:
		return ""
	}
}

// ClockRate returns the RTP clock rate for this codec.
func (c VideoCodec) ClockRate() uint32 {
	// All video codecs use 90kHz clock
	return 90000
}

// DefaultPayloadType returns a typical payload type for this codec.
// Note: Actual payload type is negotiated via SDP.
func (c VideoCodec) DefaultPayloadType() uint8 {
	switch c {
	case VideoCodecVP8:
		return 96
	case VideoCodecVP9:
		return 98
	case VideoCodecH264:
		return 102
	case VideoCodecH265:
		return 104
	case VideoCodecAV1:
		return 35
	default:
		return 96
	}
}

// AudioCodec identifies the audio codec type.
type AudioCodec int

const (
	AudioCodecUnknown AudioCodec = iota
	AudioCodecOpus
	AudioCodecG711A // A-law (PCMA)
	AudioCodecG711U // μ-law (PCMU)
	AudioCodecAAC
)

func (c AudioCodec) String() string {
	switch c {
	case AudioCodecOpus:
		return "Opus"
	case AudioCodecG711A:
		return "PCMA"
	case AudioCodecG711U:
		return "PCMU"
	case AudioCodecAAC:
		return "AAC"
	default:
		return "Unknown"
	}
}

// MimeType returns the MIME type for this codec.
func (c AudioCodec) MimeType() string {
	switch c {
	case AudioCodecOpus:
		return "audio/opus"
	case AudioCodecG711A:
		return "audio/PCMA"
	case AudioCodecG711U:
		return "audio/PCMU"
	case AudioCodecAAC:
		return "audio/AAC"
	default:
		return ""
	}
}

// ClockRate returns the RTP clock rate for this codec.
func (c AudioCodec) ClockRate() uint32 {
	switch c {
	case AudioCodecOpus:
		return 48000
	case AudioCodecG711A, AudioCodecG711U:
		return 8000
	case AudioCodecAAC:
		return 48000 // Varies, but 48kHz is common
	default:
		return 48000
	}
}

// DefaultPayloadType returns a typical payload type for this codec.
func (c AudioCodec) DefaultPayloadType() uint8 {
	switch c {
	case AudioCodecOpus:
		return 111
	case AudioCodecG711A:
		return 8 // Static payload type
	case AudioCodecG711U:
		return 0 // Static payload type
	case AudioCodecAAC:
		return 97
	default:
		return 111
	}
}

// RateControlMode defines the encoder rate control mode.
type RateControlMode int

const (
	RateControlVBR RateControlMode = iota // Variable bitrate
	RateControlCBR                        // Constant bitrate
	RateControlCQ                         // Constant quality (CRF)
)

func (r RateControlMode) String() string {
	switch r {
	case RateControlVBR:
		return "VBR"
	case RateControlCBR:
		return "CBR"
	case RateControlCQ:
		return "CQ"
	default:
		return "Unknown"
	}
}

// H264Profile defines H.264 encoding profiles.
type H264Profile int

const (
	H264ProfileBaseline H264Profile = iota
	H264ProfileMain
	H264ProfileHigh
	H264ProfileHigh444
)

func (p H264Profile) String() string {
	switch p {
	case H264ProfileBaseline:
		return "Baseline"
	case H264ProfileMain:
		return "Main"
	case H264ProfileHigh:
		return "High"
	case H264ProfileHigh444:
		return "High444"
	default:
		return "Unknown"
	}
}

// VP9Profile defines VP9 encoding profiles.
type VP9Profile int

const (
	VP9Profile0 VP9Profile = iota // 8-bit, 4:2:0
	VP9Profile1                   // 8-bit, 4:2:2 or 4:4:4
	VP9Profile2                   // 10/12-bit, 4:2:0
	VP9Profile3                   // 10/12-bit, 4:2:2 or 4:4:4
)

func (p VP9Profile) String() string {
	switch p {
	case VP9Profile0:
		return "Profile0"
	case VP9Profile1:
		return "Profile1"
	case VP9Profile2:
		return "Profile2"
	case VP9Profile3:
		return "Profile3"
	default:
		return "Unknown"
	}
}

// AV1Profile defines AV1 encoding profiles.
type AV1Profile int

const (
	AV1ProfileMain         AV1Profile = iota // 8-bit, 4:2:0
	AV1ProfileHigh                           // 8-bit, 4:2:0 or 4:4:4
	AV1ProfileProfessional                   // 10/12-bit
)

func (p AV1Profile) String() string {
	switch p {
	case AV1ProfileMain:
		return "Main"
	case AV1ProfileHigh:
		return "High"
	case AV1ProfileProfessional:
		return "Professional"
	default:
		return "Unknown"
	}
}
