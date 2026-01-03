package media

import (
	"sync"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

// AV1Packetizer implements RTPPacketizer for AV1.
// Uses pion's AV1Payloader which correctly implements RFC 9000.
type AV1Packetizer struct {
	ssrc        uint32
	payloadType uint8
	mtu         int
	sequencer   rtp.Sequencer
	clockRate   uint32
	payloader   *codecs.AV1Payloader
	mu          sync.Mutex
}

// NewAV1Packetizer creates a new AV1 RTP packetizer.
func NewAV1Packetizer(ssrc uint32, payloadType uint8, mtu int) *AV1Packetizer {
	if mtu <= 0 {
		mtu = 1200
	}
	return &AV1Packetizer{
		ssrc:        ssrc,
		payloadType: payloadType,
		mtu:         mtu,
		sequencer:   rtp.NewRandomSequencer(),
		clockRate:   90000,
		payloader:   &codecs.AV1Payloader{},
	}
}

// Packetize converts an AV1 encoded frame into RTP packets.
// Input should be a sequence of OBUs (Open Bitstream Units).
func (p *AV1Packetizer) Packetize(frame *EncodedFrame) ([]*rtp.Packet, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(frame.Data) == 0 {
		return nil, nil
	}

	// Use pion's AV1Payloader which correctly handles RFC 9000 format
	payloads := p.payloader.Payload(uint16(p.mtu-12), frame.Data)
	if len(payloads) == 0 {
		return nil, nil
	}

	packets := make([]*rtp.Packet, len(payloads))
	for i, payload := range payloads {
		packets[i] = &rtp.Packet{
			Header: rtp.Header{
				Version:        2,
				Padding:        false,
				Extension:      false,
				Marker:         i == len(payloads)-1, // Marker on last packet
				PayloadType:    p.payloadType,
				SequenceNumber: p.sequencer.NextSequenceNumber(),
				Timestamp:      frame.Timestamp,
				SSRC:           p.ssrc,
			},
			Payload: payload,
		}
	}

	return packets, nil
}

// Codec returns the codec type.
func (p *AV1Packetizer) Codec() VideoCodec {
	return VideoCodecAV1
}

// PacketizeToBytes converts an encoded AV1 frame to raw RTP packet bytes.
func (p *AV1Packetizer) PacketizeToBytes(frame *EncodedFrame) ([][]byte, error) {
	packets, err := p.Packetize(frame)
	if err != nil {
		return nil, err
	}
	result := make([][]byte, len(packets))
	for i, pkt := range packets {
		result[i], _ = pkt.Marshal()
	}
	return result, nil
}

func (p *AV1Packetizer) SetSSRC(ssrc uint32)     { p.mu.Lock(); p.ssrc = ssrc; p.mu.Unlock() }
func (p *AV1Packetizer) SSRC() uint32            { p.mu.Lock(); defer p.mu.Unlock(); return p.ssrc }
func (p *AV1Packetizer) PayloadType() uint8      { p.mu.Lock(); defer p.mu.Unlock(); return p.payloadType }
func (p *AV1Packetizer) SetPayloadType(pt uint8) { p.mu.Lock(); p.payloadType = pt; p.mu.Unlock() }
func (p *AV1Packetizer) MTU() int                { p.mu.Lock(); defer p.mu.Unlock(); return p.mtu }
func (p *AV1Packetizer) SetMTU(mtu int)          { p.mu.Lock(); p.mtu = mtu; p.mu.Unlock() }

// AV1Depacketizer reassembles AV1 OBUs from RTP packets.
// Uses pion's AV1Packet which correctly implements RFC 9000.
type AV1Depacketizer struct {
	depacketizer codecs.AV1Packet
	fragments    []byte
	timestamp    uint32
	frameType    FrameType
	mu           sync.Mutex
}

// NewAV1Depacketizer creates a new AV1 RTP depacketizer.
func NewAV1Depacketizer() *AV1Depacketizer {
	return &AV1Depacketizer{}
}

// Depacketize processes an RTP packet and returns a complete frame if available.
func (d *AV1Depacketizer) Depacketize(pkt *rtp.Packet) (*EncodedFrame, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(pkt.Payload) < 1 {
		return nil, nil
	}

	// Handle timestamp changes (new frame started)
	if d.timestamp != 0 && d.timestamp != pkt.Header.Timestamp {
		d.fragments = d.fragments[:0]
	}
	d.timestamp = pkt.Header.Timestamp

	// Use pion's AV1Packet to unmarshal
	obuData, err := d.depacketizer.Unmarshal(pkt.Payload)
	if err != nil {
		return nil, err
	}

	d.fragments = append(d.fragments, obuData...)

	// Check N bit for keyframe detection
	if len(pkt.Payload) > 0 && (pkt.Payload[0]&0x08) != 0 {
		d.frameType = FrameTypeKey
	} else if d.frameType != FrameTypeKey {
		d.frameType = FrameTypeDelta
	}

	if pkt.Header.Marker {
		frame := &EncodedFrame{
			Data:      make([]byte, len(d.fragments)),
			FrameType: d.frameType,
			Timestamp: d.timestamp,
		}
		copy(frame.Data, d.fragments)
		d.fragments = d.fragments[:0]
		d.frameType = FrameTypeUnknown
		return frame, nil
	}

	return nil, nil
}

// DepacketizeBytes processes raw RTP packet bytes.
func (d *AV1Depacketizer) DepacketizeBytes(data []byte) (*EncodedFrame, error) {
	var pkt rtp.Packet
	if err := pkt.Unmarshal(data); err != nil {
		return nil, err
	}
	return d.Depacketize(&pkt)
}

// Reset clears any buffered partial frames.
func (d *AV1Depacketizer) Reset() {
	d.mu.Lock()
	d.fragments = d.fragments[:0]
	d.timestamp = 0
	d.frameType = FrameTypeUnknown
	d.mu.Unlock()
}

// Codec returns the codec type.
func (d *AV1Depacketizer) Codec() VideoCodec {
	return VideoCodecAV1
}

func init() {
	RegisterVideoPacketizer(VideoCodecAV1, func(ssrc uint32, pt uint8, mtu int) (RTPPacketizer, error) {
		return NewAV1Packetizer(ssrc, pt, mtu), nil
	})
	RegisterVideoDepacketizer(VideoCodecAV1, func() (RTPDepacketizer, error) {
		return NewAV1Depacketizer(), nil
	})
}
