package media

import (
	"testing"
)

func TestVideoCodec_String(t *testing.T) {
	tests := []struct {
		codec VideoCodec
		want  string
	}{
		{VideoCodecVP8, "VP8"},
		{VideoCodecVP9, "VP9"},
		{VideoCodecH264, "H264"},
		{VideoCodecH265, "H265"},
		{VideoCodecAV1, "AV1"},
		{VideoCodecUnknown, "Unknown"},
		{VideoCodec(99), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.codec.String(); got != tt.want {
				t.Errorf("VideoCodec.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVideoCodec_MimeType(t *testing.T) {
	tests := []struct {
		codec VideoCodec
		want  string
	}{
		{VideoCodecVP8, "video/VP8"},
		{VideoCodecVP9, "video/VP9"},
		{VideoCodecH264, "video/H264"},
		{VideoCodecH265, "video/H265"},
		{VideoCodecAV1, "video/AV1"},
		{VideoCodecUnknown, ""},
	}

	for _, tt := range tests {
		t.Run(tt.codec.String(), func(t *testing.T) {
			if got := tt.codec.MimeType(); got != tt.want {
				t.Errorf("VideoCodec.MimeType() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVideoCodec_ClockRate(t *testing.T) {
	// All video codecs should use 90kHz clock
	codecs := []VideoCodec{VideoCodecVP8, VideoCodecVP9, VideoCodecH264, VideoCodecH265, VideoCodecAV1}

	for _, codec := range codecs {
		t.Run(codec.String(), func(t *testing.T) {
			if got := codec.ClockRate(); got != 90000 {
				t.Errorf("VideoCodec.ClockRate() = %v, want 90000", got)
			}
		})
	}
}

func TestVideoCodec_DefaultPayloadType(t *testing.T) {
	tests := []struct {
		codec VideoCodec
		want  uint8
	}{
		{VideoCodecVP8, 96},
		{VideoCodecVP9, 98},
		{VideoCodecH264, 102},
		{VideoCodecH265, 104},
		{VideoCodecAV1, 35},
	}

	for _, tt := range tests {
		t.Run(tt.codec.String(), func(t *testing.T) {
			if got := tt.codec.DefaultPayloadType(); got != tt.want {
				t.Errorf("VideoCodec.DefaultPayloadType() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAudioCodec_String(t *testing.T) {
	tests := []struct {
		codec AudioCodec
		want  string
	}{
		{AudioCodecOpus, "Opus"},
		{AudioCodecG711A, "PCMA"},
		{AudioCodecG711U, "PCMU"},
		{AudioCodecAAC, "AAC"},
		{AudioCodecUnknown, "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.codec.String(); got != tt.want {
				t.Errorf("AudioCodec.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAudioCodec_ClockRate(t *testing.T) {
	tests := []struct {
		codec AudioCodec
		want  uint32
	}{
		{AudioCodecOpus, 48000},
		{AudioCodecG711A, 8000},
		{AudioCodecG711U, 8000},
		{AudioCodecAAC, 48000},
	}

	for _, tt := range tests {
		t.Run(tt.codec.String(), func(t *testing.T) {
			if got := tt.codec.ClockRate(); got != tt.want {
				t.Errorf("AudioCodec.ClockRate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRateControlMode_String(t *testing.T) {
	tests := []struct {
		mode RateControlMode
		want string
	}{
		{RateControlVBR, "VBR"},
		{RateControlCBR, "CBR"},
		{RateControlCQ, "CQ"},
		{RateControlMode(99), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.mode.String(); got != tt.want {
				t.Errorf("RateControlMode.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestH264Profile_String(t *testing.T) {
	tests := []struct {
		profile H264Profile
		want    string
	}{
		{H264ProfileBaseline, "Baseline"},
		{H264ProfileMain, "Main"},
		{H264ProfileHigh, "High"},
		{H264ProfileHigh444, "High444"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.profile.String(); got != tt.want {
				t.Errorf("H264Profile.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVP9Profile_String(t *testing.T) {
	tests := []struct {
		profile VP9Profile
		want    string
	}{
		{VP9Profile0, "Profile0"},
		{VP9Profile1, "Profile1"},
		{VP9Profile2, "Profile2"},
		{VP9Profile3, "Profile3"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.profile.String(); got != tt.want {
				t.Errorf("VP9Profile.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAV1Profile_String(t *testing.T) {
	tests := []struct {
		profile AV1Profile
		want    string
	}{
		{AV1ProfileMain, "Main"},
		{AV1ProfileHigh, "High"},
		{AV1ProfileProfessional, "Professional"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.profile.String(); got != tt.want {
				t.Errorf("AV1Profile.String() = %v, want %v", got, tt.want)
			}
		})
	}
}
