package media

import (
	"fmt"
	"io"
	"sync"

	"github.com/pion/rtp"
)

// Re-export pion/rtp types for convenience
type (
	// RTPPacket is an alias to pion's rtp.Packet
	RTPPacket = rtp.Packet

	// RTPHeader is an alias to pion's rtp.Header
	RTPHeader = rtp.Header

	// RTPExtension is an alias to pion's rtp.Extension
	RTPExtension = rtp.Extension
)

// Re-export pion's header extension types
type (
	AbsSendTimeExtension  = rtp.AbsSendTimeExtension
	AudioLevelExtension   = rtp.AudioLevelExtension
	PlayoutDelayExtension = rtp.PlayoutDelayExtension
	TransportCCExtension  = rtp.TransportCCExtension
)

// RTP Header Extension IDs (commonly used) - kept for convenience
const (
	// One-byte header extension IDs
	ExtensionIDAbsSendTime      = 3  // abs-send-time (3 bytes: 24-bit NTP timestamp)
	ExtensionIDTransportWideCC  = 5  // transport-wide-cc (2 bytes: sequence number)
	ExtensionIDVideoOrientation = 4  // video-orientation (1 byte)
	ExtensionIDPlayoutDelay     = 6  // playout-delay (3 bytes: min/max delay)
	ExtensionIDMid              = 10 // mid extension (variable)
	ExtensionIDRid              = 11 // rid extension (variable)
	ExtensionIDRepairedRid      = 12 // repaired-rid extension (variable)
	ExtensionIDDependencyDesc   = 8  // dependency-descriptor (variable)
	ExtensionIDAudioLevel       = 1  // audio-level (1 byte: level + voice activity)
)

// VideoOrientation represents the CVO (Coordination of Video Orientation) extension.
// This is not provided by pion/rtp, so we keep our own implementation.
type VideoOrientation struct {
	CameraBackFacing bool // true = back camera, false = front camera
	FlipHorizontal   bool // Flip horizontally
	Rotation         int  // 0, 90, 180, 270 degrees clockwise
}

// Marshal returns the extension payload bytes.
func (v VideoOrientation) Marshal() []byte {
	var val uint8
	if v.CameraBackFacing {
		val |= 0x08
	}
	if v.FlipHorizontal {
		val |= 0x04
	}
	switch v.Rotation {
	case 90:
		val |= 0x01
	case 180:
		val |= 0x02
	case 270:
		val |= 0x03
	}
	return []byte{val}
}

// Unmarshal parses a video orientation extension.
func (v *VideoOrientation) Unmarshal(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("empty video orientation data")
	}
	b := data[0]
	v.CameraBackFacing = (b & 0x08) != 0
	v.FlipHorizontal = (b & 0x04) != 0
	switch b & 0x03 {
	case 1:
		v.Rotation = 90
	case 2:
		v.Rotation = 180
	case 3:
		v.Rotation = 270
	default:
		v.Rotation = 0
	}
	return nil
}

// RTPPacketizer segments encoded frames into RTP packets.
type RTPPacketizer interface {
	// Packetize converts an encoded frame to RTP packets.
	Packetize(frame *EncodedFrame) ([]*RTPPacket, error)

	// PacketizeToBytes converts an encoded frame to raw RTP packet bytes.
	PacketizeToBytes(frame *EncodedFrame) ([][]byte, error)

	// SetSSRC updates the SSRC for outgoing packets.
	SetSSRC(ssrc uint32)

	// SSRC returns the current SSRC.
	SSRC() uint32

	// PayloadType returns the configured payload type.
	PayloadType() uint8

	// SetPayloadType updates the payload type.
	SetPayloadType(pt uint8)

	// MTU returns the maximum transmission unit.
	MTU() int

	// SetMTU updates the MTU.
	SetMTU(mtu int)
}

// RTPDepacketizer reassembles RTP packets into encoded frames.
type RTPDepacketizer interface {
	// Depacketize processes an RTP packet and returns a complete frame if available.
	// Returns nil if the frame is not yet complete.
	Depacketize(packet *RTPPacket) (*EncodedFrame, error)

	// DepacketizeBytes processes raw RTP packet bytes.
	DepacketizeBytes(data []byte) (*EncodedFrame, error)

	// Reset clears any buffered partial frames.
	Reset()
}

// PacketizerFactory creates an RTP packetizer.
type PacketizerFactory func(ssrc uint32, pt uint8, mtu int) (RTPPacketizer, error)

// DepacketizerFactory creates an RTP depacketizer.
type DepacketizerFactory func() (RTPDepacketizer, error)

// rtpRegistry holds packetizer/depacketizer factories.
type rtpRegistry struct {
	packetizers        map[VideoCodec]PacketizerFactory
	depacketizers      map[VideoCodec]DepacketizerFactory
	audioPacketizers   map[AudioCodec]PacketizerFactory
	audioDepacketizers map[AudioCodec]DepacketizerFactory
	mu                 sync.RWMutex
}

var globalRTPRegistry = &rtpRegistry{
	packetizers:        make(map[VideoCodec]PacketizerFactory),
	depacketizers:      make(map[VideoCodec]DepacketizerFactory),
	audioPacketizers:   make(map[AudioCodec]PacketizerFactory),
	audioDepacketizers: make(map[AudioCodec]DepacketizerFactory),
}

// RegisterVideoPacketizer registers a video RTP packetizer factory.
func RegisterVideoPacketizer(codec VideoCodec, factory PacketizerFactory) {
	globalRTPRegistry.mu.Lock()
	defer globalRTPRegistry.mu.Unlock()
	globalRTPRegistry.packetizers[codec] = factory
}

// RegisterVideoDepacketizer registers a video RTP depacketizer factory.
func RegisterVideoDepacketizer(codec VideoCodec, factory DepacketizerFactory) {
	globalRTPRegistry.mu.Lock()
	defer globalRTPRegistry.mu.Unlock()
	globalRTPRegistry.depacketizers[codec] = factory
}

// RegisterAudioPacketizer registers an audio RTP packetizer factory.
func RegisterAudioPacketizer(codec AudioCodec, factory PacketizerFactory) {
	globalRTPRegistry.mu.Lock()
	defer globalRTPRegistry.mu.Unlock()
	globalRTPRegistry.audioPacketizers[codec] = factory
}

// RegisterAudioDepacketizer registers an audio RTP depacketizer factory.
func RegisterAudioDepacketizer(codec AudioCodec, factory DepacketizerFactory) {
	globalRTPRegistry.mu.Lock()
	defer globalRTPRegistry.mu.Unlock()
	globalRTPRegistry.audioDepacketizers[codec] = factory
}

// CreateVideoPacketizer creates a video RTP packetizer.
func CreateVideoPacketizer(codec VideoCodec, ssrc uint32, pt uint8, mtu int) (RTPPacketizer, error) {
	globalRTPRegistry.mu.RLock()
	factory, ok := globalRTPRegistry.packetizers[codec]
	globalRTPRegistry.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("video packetizer not available: %v", codec)
	}

	return factory(ssrc, pt, mtu)
}

// CreateVideoDepacketizer creates a video RTP depacketizer.
func CreateVideoDepacketizer(codec VideoCodec) (RTPDepacketizer, error) {
	globalRTPRegistry.mu.RLock()
	factory, ok := globalRTPRegistry.depacketizers[codec]
	globalRTPRegistry.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("video depacketizer not available: %v", codec)
	}

	return factory()
}

// CreateAudioPacketizer creates an audio RTP packetizer.
func CreateAudioPacketizer(codec AudioCodec, ssrc uint32, pt uint8, mtu int) (RTPPacketizer, error) {
	globalRTPRegistry.mu.RLock()
	factory, ok := globalRTPRegistry.audioPacketizers[codec]
	globalRTPRegistry.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("audio packetizer not available: %v", codec)
	}

	return factory(ssrc, pt, mtu)
}

// CreateAudioDepacketizer creates an audio RTP depacketizer.
func CreateAudioDepacketizer(codec AudioCodec) (RTPDepacketizer, error) {
	globalRTPRegistry.mu.RLock()
	factory, ok := globalRTPRegistry.audioDepacketizers[codec]
	globalRTPRegistry.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("audio depacketizer not available: %v", codec)
	}

	return factory()
}

// RTPWriter is an interface for writing RTP packets.
type RTPWriter interface {
	// WriteRTP writes an RTP packet.
	WriteRTP(packet *RTPPacket) error

	// WriteRTPBytes writes raw RTP packet bytes.
	WriteRTPBytes(data []byte) error
}

// RTPReader is an interface for reading RTP packets.
type RTPReader interface {
	// ReadRTP reads an RTP packet.
	ReadRTP() (*RTPPacket, error)

	// ReadRTPBytes reads raw RTP packet bytes.
	ReadRTPBytes(buf []byte) (int, error)
}

// RTPSender sends media over RTP (like RTCRtpSender).
// It manages the pipeline: Track -> Encoder -> Packetizer -> RTP
type RTPSender interface {
	io.Closer

	// Track returns the track being sent.
	Track() MediaStreamTrack

	// ReplaceTrack replaces the track being sent.
	ReplaceTrack(track MediaStreamTrack) error

	// SetParameters sets the encoding parameters.
	SetParameters(params RTPSendParameters) error

	// GetParameters returns the current encoding parameters.
	GetParameters() RTPSendParameters

	// GetStats returns sender statistics.
	GetStats() RTPSenderStats

	// SetWriter sets the RTP writer to send packets to.
	SetWriter(writer RTPWriter)
}

// RTPSendParameters configures RTP sending.
type RTPSendParameters struct {
	Encodings []RTPEncodingParameters
}

// RTPEncodingParameters configures a single encoding (for simulcast).
type RTPEncodingParameters struct {
	RID          string     // Restriction identifier for simulcast
	Active       bool       // Whether this encoding is active
	MaxBitrate   int        // Maximum bitrate in bps
	MaxFramerate float64    // Maximum framerate
	ScaleDownBy  float64    // Scale factor (1.0 = full resolution)
	SSRC         uint32     // SSRC for this encoding
	Codec        VideoCodec // Codec for this encoding
	Priority     string     // "very-low", "low", "medium", "high"
}

// RTPSenderStats provides sender statistics.
type RTPSenderStats struct {
	PacketsSent   uint64
	BytesSent     uint64
	FramesSent    uint64
	KeyframesSent uint64
	NACKsReceived uint64
	PLIsReceived  uint64
	FIRsReceived  uint64
	RTT           float64 // Round-trip time in seconds
}

// RTPReceiver receives media over RTP (like RTCRtpReceiver).
// It manages the pipeline: RTP -> Depacketizer -> Decoder -> Track
type RTPReceiver interface {
	io.Closer

	// Track returns the track being received.
	Track() MediaStreamTrack

	// GetParameters returns the current receiving parameters.
	GetParameters() RTPReceiveParameters

	// GetStats returns receiver statistics.
	GetStats() RTPReceiverStats

	// SetReader sets the RTP reader to receive packets from.
	SetReader(reader RTPReader)

	// OnFrame sets a callback for decoded video frames.
	OnFrame(callback VideoFrameCallback)

	// OnSamples sets a callback for decoded audio samples.
	OnSamples(callback AudioSamplesCallback)
}

// RTPReceiveParameters configures RTP receiving.
type RTPReceiveParameters struct {
	Encodings []RTPDecodingParameters
}

// RTPDecodingParameters configures decoding for a single stream.
type RTPDecodingParameters struct {
	SSRC        uint32
	PayloadType uint8
	Codec       VideoCodec
}

// RTPReceiverStats provides receiver statistics.
type RTPReceiverStats struct {
	PacketsReceived   uint64
	BytesReceived     uint64
	PacketsLost       uint64
	Jitter            float64 // Jitter in seconds
	FramesReceived    uint64
	FramesDecoded     uint64
	FramesDropped     uint64
	KeyframesReceived uint64
	NACKsSent         uint64
	PLIsSent          uint64
	FIRsSent          uint64
}

// Default MTU for RTP packets (UDP safe)
const DefaultMTU = 1200

// IsRTPTimestampOlder returns true if ts1 is older than or equal to ts2,
// handling 32-bit wraparound correctly per RTP timestamp comparison rules.
// This is used by depacketizers to discard late-arriving packets.
func IsRTPTimestampOlder(ts1, ts2 uint32) bool {
	if ts1 == ts2 {
		return true
	}
	// Standard RTP timestamp comparison with wraparound handling:
	// ts1 is older if (ts2 - ts1) < 2^31
	diff := ts2 - ts1
	return diff < 0x80000000
}
