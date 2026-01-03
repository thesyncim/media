package media

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// Re-export pion's RTPCodecType for convenience
type RTPCodecType = webrtc.RTPCodecType

const (
	RTPCodecTypeUnknown = webrtc.RTPCodecTypeUnknown
	RTPCodecTypeAudio   = webrtc.RTPCodecTypeAudio
	RTPCodecTypeVideo   = webrtc.RTPCodecTypeVideo
)

// TrackState represents the state of a track.
type TrackState int

const (
	TrackStateLive  TrackState = iota // Track is active and producing/consuming media
	TrackStateEnded                   // Track has ended
	TrackStateMuted                   // Track is muted (still active but not producing)
)

func (s TrackState) String() string {
	switch s {
	case TrackStateLive:
		return "live"
	case TrackStateEnded:
		return "ended"
	case TrackStateMuted:
		return "muted"
	default:
		return "unknown"
	}
}

// TrackConstraints describes desired track properties (like browser MediaTrackConstraints).
type TrackConstraints struct {
	// Video constraints
	Width      int    // Desired width (0 = any)
	Height     int    // Desired height (0 = any)
	FrameRate  int    // Desired framerate (0 = any)
	FacingMode string // "user" (front camera) or "environment" (back camera)

	// Audio constraints
	SampleRate       int  // Desired sample rate (0 = any)
	ChannelCount     int  // Desired channels (0 = any)
	EchoCancellation bool // Enable echo cancellation
	NoiseSuppression bool // Enable noise suppression
	AutoGainControl  bool // Enable automatic gain control

	// Common
	DeviceID string // Specific device ID to use
}

// MediaStreamTrack represents a single audio or video track.
// This is similar to the browser's MediaStreamTrack interface.
type MediaStreamTrack interface {
	io.Closer

	// ID returns the unique identifier for this track.
	ID() string

	// Kind returns the track kind (audio or video) - compatible with pion.
	Kind() RTPCodecType

	// Label returns a human-readable label for the track source.
	Label() string

	// State returns the current track state.
	State() TrackState

	// Muted returns whether the track is muted.
	Muted() bool

	// SetMuted sets the muted state.
	SetMuted(muted bool)

	// Enabled returns whether the track is enabled.
	Enabled() bool

	// SetEnabled sets the enabled state.
	SetEnabled(enabled bool)

	// Constraints returns the track's current constraints.
	Constraints() TrackConstraints

	// ApplyConstraints applies new constraints to the track.
	ApplyConstraints(constraints TrackConstraints) error

	// Clone creates a clone of this track.
	Clone() (MediaStreamTrack, error)

	// OnEnded sets a callback for when the track ends.
	OnEnded(callback func())
}

// VideoTrack is a MediaStreamTrack that produces video frames.
type VideoTrack interface {
	MediaStreamTrack

	// ReadFrame reads the next video frame.
	ReadFrame(ctx context.Context) (*VideoFrame, error)

	// OnFrame sets a callback for when a frame is available.
	OnFrame(callback VideoFrameCallback)

	// Settings returns the actual video settings.
	Settings() VideoTrackSettings
}

// VideoTrackSettings describes the actual video track settings.
type VideoTrackSettings struct {
	Width      int
	Height     int
	FrameRate  int
	DeviceID   string
	FacingMode string
}

// AudioTrack is a MediaStreamTrack that produces audio samples.
type AudioTrack interface {
	MediaStreamTrack

	// ReadSamples reads the next audio samples.
	ReadSamples(ctx context.Context) (*AudioSamples, error)

	// OnSamples sets a callback for when samples are available.
	OnSamples(callback AudioSamplesCallback)

	// Settings returns the actual audio settings.
	Settings() AudioTrackSettings
}

// AudioTrackSettings describes the actual audio track settings.
type AudioTrackSettings struct {
	SampleRate       int
	ChannelCount     int
	DeviceID         string
	EchoCancellation bool
	NoiseSuppression bool
	AutoGainControl  bool
}

// MediaStream is a collection of tracks (like browser's MediaStream).
type MediaStream interface {
	io.Closer

	// ID returns the unique identifier for this stream.
	ID() string

	// Active returns whether any track in the stream is active.
	Active() bool

	// GetTracks returns all tracks in the stream.
	GetTracks() []MediaStreamTrack

	// GetVideoTracks returns all video tracks.
	GetVideoTracks() []VideoTrack

	// GetAudioTracks returns all audio tracks.
	GetAudioTracks() []AudioTrack

	// GetTrackByID returns a track by its ID.
	GetTrackByID(id string) MediaStreamTrack

	// AddTrack adds a track to the stream.
	AddTrack(track MediaStreamTrack)

	// RemoveTrack removes a track from the stream.
	RemoveTrack(track MediaStreamTrack)

	// Clone creates a clone of this stream with cloned tracks.
	Clone() (MediaStream, error)

	// OnAddTrack sets a callback for when a track is added.
	OnAddTrack(callback func(track MediaStreamTrack))

	// OnRemoveTrack sets a callback for when a track is removed.
	OnRemoveTrack(callback func(track MediaStreamTrack))
}

// BaseTrack provides common functionality for tracks.
type BaseTrack struct {
	id          string
	streamID    string
	rid         string
	label       string
	kind        RTPCodecType
	state       atomic.Int32
	muted       atomic.Bool
	enabled     atomic.Bool
	constraints TrackConstraints
	endedCb     func()
	mu          sync.RWMutex
}

// NewBaseTrack creates a new base track.
func NewBaseTrack(id, streamID, label string, kind RTPCodecType) *BaseTrack {
	t := &BaseTrack{
		id:       id,
		streamID: streamID,
		label:    label,
		kind:     kind,
	}
	t.state.Store(int32(TrackStateLive))
	t.enabled.Store(true)
	return t
}

func (t *BaseTrack) ID() string         { return t.id }
func (t *BaseTrack) StreamID() string   { return t.streamID }
func (t *BaseTrack) RID() string        { return t.rid }
func (t *BaseTrack) Kind() RTPCodecType { return t.kind }
func (t *BaseTrack) Label() string      { return t.label }

func (t *BaseTrack) SetRID(rid string) {
	t.mu.Lock()
	t.rid = rid
	t.mu.Unlock()
}

func (t *BaseTrack) State() TrackState {
	return TrackState(t.state.Load())
}

func (t *BaseTrack) SetState(state TrackState) {
	old := TrackState(t.state.Swap(int32(state)))
	if state == TrackStateEnded && old != TrackStateEnded {
		t.mu.RLock()
		cb := t.endedCb
		t.mu.RUnlock()
		if cb != nil {
			go cb()
		}
	}
}

func (t *BaseTrack) Muted() bool       { return t.muted.Load() }
func (t *BaseTrack) SetMuted(m bool)   { t.muted.Store(m) }
func (t *BaseTrack) Enabled() bool     { return t.enabled.Load() }
func (t *BaseTrack) SetEnabled(e bool) { t.enabled.Store(e) }

func (t *BaseTrack) Constraints() TrackConstraints {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.constraints
}

func (t *BaseTrack) SetConstraints(c TrackConstraints) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.constraints = c
}

func (t *BaseTrack) OnEnded(callback func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.endedCb = callback
}

// LocalTrack implements pion's webrtc.TrackLocal interface.
// It wraps a media source, encoder, and packetizer to produce RTP packets.
type LocalTrack struct {
	*BaseTrack
	codec    webrtc.RTPCodecCapability
	writeRTP func(*rtp.Packet) error
	bindMu   sync.RWMutex
	bindings []webrtc.TrackLocalContext
}

// NewLocalTrack creates a new LocalTrack that implements webrtc.TrackLocal.
func NewLocalTrack(codec webrtc.RTPCodecCapability, id, streamID string) *LocalTrack {
	kind := RTPCodecTypeVideo
	if codec.MimeType[:5] == "audio" {
		kind = RTPCodecTypeAudio
	}
	return &LocalTrack{
		BaseTrack: NewBaseTrack(id, streamID, id, kind),
		codec:     codec,
	}
}

// Codec returns the codec capability.
func (t *LocalTrack) Codec() webrtc.RTPCodecCapability {
	return t.codec
}

// Bind implements webrtc.TrackLocal.
func (t *LocalTrack) Bind(ctx webrtc.TrackLocalContext) (webrtc.RTPCodecParameters, error) {
	t.bindMu.Lock()
	defer t.bindMu.Unlock()

	t.bindings = append(t.bindings, ctx)

	// Find matching codec from negotiated parameters
	params := ctx.CodecParameters()
	for _, p := range params {
		if p.MimeType == t.codec.MimeType {
			return p, nil
		}
	}

	// Fallback: return our codec with default payload type
	return webrtc.RTPCodecParameters{
		RTPCodecCapability: t.codec,
	}, nil
}

// Unbind implements webrtc.TrackLocal.
func (t *LocalTrack) Unbind(ctx webrtc.TrackLocalContext) error {
	t.bindMu.Lock()
	defer t.bindMu.Unlock()

	for i, b := range t.bindings {
		if b.ID() == ctx.ID() {
			t.bindings = append(t.bindings[:i], t.bindings[i+1:]...)
			break
		}
	}
	return nil
}

// WriteRTP writes an RTP packet to all bound contexts.
func (t *LocalTrack) WriteRTP(p *rtp.Packet) error {
	t.bindMu.RLock()
	defer t.bindMu.RUnlock()

	for _, b := range t.bindings {
		if _, err := b.WriteStream().WriteRTP(&p.Header, p.Payload); err != nil {
			return err
		}
	}
	return nil
}

// Write writes raw RTP bytes to all bound contexts.
func (t *LocalTrack) Write(b []byte) (int, error) {
	var p rtp.Packet
	if err := p.Unmarshal(b); err != nil {
		return 0, err
	}
	return len(b), t.WriteRTP(&p)
}

// Close implements io.Closer.
func (t *LocalTrack) Close() error {
	t.SetState(TrackStateEnded)
	return nil
}

// Clone creates a clone of this track.
func (t *LocalTrack) Clone() (MediaStreamTrack, error) {
	return NewLocalTrack(t.codec, t.id+"-clone", t.streamID), nil
}

// ApplyConstraints is a no-op for LocalTrack.
func (t *LocalTrack) ApplyConstraints(constraints TrackConstraints) error {
	return nil
}

// Verify LocalTrack implements webrtc.TrackLocal
var _ webrtc.TrackLocal = (*LocalTrack)(nil)

// SimpleMediaStream is a basic MediaStream implementation.
type SimpleMediaStream struct {
	id            string
	tracks        []MediaStreamTrack
	mu            sync.RWMutex
	onAddTrack    func(MediaStreamTrack)
	onRemoveTrack func(MediaStreamTrack)
}

// NewMediaStream creates a new media stream.
func NewMediaStream(id string) *SimpleMediaStream {
	return &SimpleMediaStream{
		id:     id,
		tracks: make([]MediaStreamTrack, 0),
	}
}

func (s *SimpleMediaStream) ID() string { return s.id }

func (s *SimpleMediaStream) Active() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.tracks {
		if t.State() == TrackStateLive {
			return true
		}
	}
	return false
}

func (s *SimpleMediaStream) GetTracks() []MediaStreamTrack {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]MediaStreamTrack, len(s.tracks))
	copy(result, s.tracks)
	return result
}

func (s *SimpleMediaStream) GetVideoTracks() []VideoTrack {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []VideoTrack
	for _, t := range s.tracks {
		if vt, ok := t.(VideoTrack); ok {
			result = append(result, vt)
		}
	}
	return result
}

func (s *SimpleMediaStream) GetAudioTracks() []AudioTrack {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []AudioTrack
	for _, t := range s.tracks {
		if at, ok := t.(AudioTrack); ok {
			result = append(result, at)
		}
	}
	return result
}

func (s *SimpleMediaStream) GetTrackByID(id string) MediaStreamTrack {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.tracks {
		if t.ID() == id {
			return t
		}
	}
	return nil
}

func (s *SimpleMediaStream) AddTrack(track MediaStreamTrack) {
	s.mu.Lock()
	s.tracks = append(s.tracks, track)
	cb := s.onAddTrack
	s.mu.Unlock()

	if cb != nil {
		go cb(track)
	}
}

func (s *SimpleMediaStream) RemoveTrack(track MediaStreamTrack) {
	s.mu.Lock()
	for i, t := range s.tracks {
		if t.ID() == track.ID() {
			s.tracks = append(s.tracks[:i], s.tracks[i+1:]...)
			break
		}
	}
	cb := s.onRemoveTrack
	s.mu.Unlock()

	if cb != nil {
		go cb(track)
	}
}

func (s *SimpleMediaStream) Clone() (MediaStream, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	clone := NewMediaStream(fmt.Sprintf("%s-clone", s.id))
	for _, t := range s.tracks {
		clonedTrack, err := t.Clone()
		if err != nil {
			return nil, fmt.Errorf("failed to clone track %s: %w", t.ID(), err)
		}
		clone.AddTrack(clonedTrack)
	}
	return clone, nil
}

func (s *SimpleMediaStream) OnAddTrack(callback func(MediaStreamTrack)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onAddTrack = callback
}

func (s *SimpleMediaStream) OnRemoveTrack(callback func(MediaStreamTrack)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onRemoveTrack = callback
}

func (s *SimpleMediaStream) Close() error {
	s.mu.Lock()
	tracks := s.tracks
	s.tracks = nil
	s.mu.Unlock()

	var lastErr error
	for _, t := range tracks {
		if err := t.Close(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}
