package media

import (
	"errors"
	"fmt"
	"sync"
)

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
	syncMode      SyncMode
	maxDriftMs    int64
	videoTimeBase int64 // ns per video timestamp unit (90kHz = 11111ns)
	audioTimeBase int64 // ns per audio timestamp unit (48kHz = 20833ns)

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
	audioEncoder, err := NewAudioEncoder(AudioEncoderConfig{
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
		videoTimeBase:   1000000000 / 90000, // 90kHz -> ~11111 ns per tick
		audioTimeBase:   1000000000 / 48000, // 48kHz -> ~20833 ns per tick
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
	drift := abs64(videoTs-audioTs) / 1e6 // ms
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
