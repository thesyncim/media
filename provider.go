package media

import "sync/atomic"

// Provider identifies a codec implementation.
type Provider uint8

const (
	ProviderAuto     Provider = iota // Let library choose best available
	ProviderX264                     // GPL H.264 encoder
	ProviderOpenH264                 // BSD H.264 enc/dec
	ProviderLibvpx                   // BSD VP8/VP9
	ProviderAOM                      // BSD AV1 (libaom)
	ProviderDAV1D                    // BSD AV1 decoder
	ProviderSVTAV1                   // BSD AV1 encoder
	ProviderLibopus                  // BSD Opus
	providerCount
)

// License represents the software license of a provider.
type License uint8

const (
	LicenseGPL License = iota // Copyleft - requires source disclosure
	LicenseBSD                // Permissive - no copyleft obligations
)

// Permissive returns true if the license has no copyleft obligations.
func (l License) Permissive() bool { return l == LicenseBSD }

func (l License) String() string {
	switch l {
	case LicenseGPL:
		return "GPL"
	case LicenseBSD:
		return "BSD"
	default:
		return "unknown"
	}
}

// Features is a bitmask of provider capabilities.
type Features uint32

const (
	FeatureSVC               Features = 1 << iota // Scalable Video Coding
	FeatureBFrames                                // B-frame support
	Feature10Bit                                  // 10-bit color depth
	FeatureLowLatency                             // Optimized for real-time
	FeatureDynamicBitrate                         // Runtime bitrate changes
	FeatureDynamicResolution                      // Runtime resolution changes
)

// Has returns true if all specified features are supported.
func (f Features) Has(feature Features) bool { return f&feature == feature }

// providerMeta contains static metadata about a provider.
type providerMeta struct {
	Name     string
	License  License
	Encoder  bool
	Decoder  bool
	Features Features
}

// Static metadata table - indexed by Provider, zero allocations.
var providerInfo = [providerCount]providerMeta{
	ProviderAuto:     {"auto", LicenseBSD, false, false, 0},
	ProviderX264:     {"x264", LicenseGPL, true, false, FeatureBFrames | FeatureLowLatency | FeatureDynamicBitrate | Feature10Bit},
	ProviderOpenH264: {"openh264", LicenseBSD, true, true, FeatureSVC | FeatureLowLatency | FeatureDynamicBitrate},
	ProviderLibvpx:   {"libvpx", LicenseBSD, true, true, FeatureSVC | FeatureBFrames | FeatureLowLatency | FeatureDynamicBitrate | Feature10Bit},
	ProviderAOM:      {"libaom", LicenseBSD, true, true, FeatureSVC | FeatureBFrames | FeatureLowLatency | FeatureDynamicBitrate | Feature10Bit},
	ProviderDAV1D:    {"dav1d", LicenseBSD, false, true, Feature10Bit},
	ProviderSVTAV1:   {"svt-av1", LicenseBSD, true, false, FeatureSVC | FeatureLowLatency | FeatureDynamicBitrate | Feature10Bit},
	ProviderLibopus:  {"libopus", LicenseBSD, true, true, FeatureDynamicBitrate | FeatureLowLatency},
}

// Runtime availability - set by init() in provider implementations.
var providerAvailable [providerCount]atomic.Bool

// String returns the provider name.
func (p Provider) String() string {
	if p >= providerCount {
		return "unknown"
	}
	return providerInfo[p].Name
}

// License returns the provider's license type.
func (p Provider) License() License {
	if p >= providerCount {
		return LicenseGPL
	}
	return providerInfo[p].License
}

// Features returns the provider's feature bitmask.
func (p Provider) Features() Features {
	if p >= providerCount {
		return 0
	}
	return providerInfo[p].Features
}

// CanEncode returns true if the provider supports encoding.
func (p Provider) CanEncode() bool {
	if p >= providerCount {
		return false
	}
	return providerInfo[p].Encoder
}

// CanDecode returns true if the provider supports decoding.
func (p Provider) CanDecode() bool {
	if p >= providerCount {
		return false
	}
	return providerInfo[p].Decoder
}

// Available returns true if the provider is usable at runtime.
func (p Provider) Available() bool {
	if p >= providerCount {
		return false
	}
	return providerAvailable[p].Load()
}

// setAvailable marks a provider as available (called by implementations).
func setProviderAvailable(p Provider) {
	if p < providerCount {
		providerAvailable[p].Store(true)
	}
}
