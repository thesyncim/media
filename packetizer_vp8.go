package media

import (
	"fmt"
	"sync"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

// VP8Packetizer implements RTPPacketizer for VP8 using pion's codecs.
type VP8Packetizer struct {
	ssrc        uint32
	payloadType uint8
	mtu         int
	sequencer   rtp.Sequencer
	payloader   *codecs.VP8Payloader
	mu          sync.Mutex
}

// NewVP8Packetizer creates a new VP8 RTP packetizer.
func NewVP8Packetizer(ssrc uint32, pt uint8, mtu int) (*VP8Packetizer, error) {
	if mtu <= 0 {
		mtu = DefaultMTU
	}
	return &VP8Packetizer{
		ssrc:        ssrc,
		payloadType: pt,
		mtu:         mtu,
		sequencer:   rtp.NewRandomSequencer(),
		payloader:   &codecs.VP8Payloader{},
	}, nil
}

// Packetize converts an encoded VP8 frame to RTP packets.
func (p *VP8Packetizer) Packetize(frame *EncodedFrame) ([]*RTPPacket, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(frame.Data) == 0 {
		return nil, nil
	}

	payloads := p.payloader.Payload(uint16(p.mtu-12), frame.Data)
	if len(payloads) == 0 {
		return nil, nil
	}

	packets := make([]*RTPPacket, len(payloads))
	for i, payload := range payloads {
		packets[i] = &RTPPacket{
			Header: rtp.Header{
				Version:        2,
				Marker:         i == len(payloads)-1,
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

// PacketizeToBytes converts an encoded VP8 frame to raw RTP packet bytes.
func (p *VP8Packetizer) PacketizeToBytes(frame *EncodedFrame) ([][]byte, error) {
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

func (p *VP8Packetizer) SetSSRC(ssrc uint32)     { p.mu.Lock(); p.ssrc = ssrc; p.mu.Unlock() }
func (p *VP8Packetizer) SSRC() uint32            { p.mu.Lock(); defer p.mu.Unlock(); return p.ssrc }
func (p *VP8Packetizer) PayloadType() uint8      { p.mu.Lock(); defer p.mu.Unlock(); return p.payloadType }
func (p *VP8Packetizer) SetPayloadType(pt uint8) { p.mu.Lock(); p.payloadType = pt; p.mu.Unlock() }
func (p *VP8Packetizer) MTU() int                { p.mu.Lock(); defer p.mu.Unlock(); return p.mtu }
func (p *VP8Packetizer) SetMTU(mtu int)          { p.mu.Lock(); p.mtu = mtu; p.mu.Unlock() }

// VP8Depacketizer implements RTPDepacketizer for VP8 using pion's codecs.
type VP8Depacketizer struct {
	depacketizer codecs.VP8Packet
	buffer       []byte
	timestamp    uint32
	frameType    FrameType
	mu           sync.Mutex
}

// NewVP8Depacketizer creates a new VP8 RTP depacketizer.
func NewVP8Depacketizer() (*VP8Depacketizer, error) {
	return &VP8Depacketizer{}, nil
}

// Depacketize processes an RTP packet and returns a complete frame if available.
func (d *VP8Depacketizer) Depacketize(packet *RTPPacket) (*EncodedFrame, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, err := d.depacketizer.Unmarshal(packet.Payload); err != nil {
		return nil, fmt.Errorf("VP8 unmarshal failed: %w", err)
	}

	// Handle timestamp changes (new frame started)
	if d.timestamp != 0 && d.timestamp != packet.Header.Timestamp {
		d.buffer = d.buffer[:0]
	}
	d.timestamp = packet.Header.Timestamp

	// Detect keyframe from VP8 header
	if d.depacketizer.S == 1 && d.depacketizer.PID == 0 {
		if len(d.depacketizer.Payload) > 0 && (d.depacketizer.Payload[0]&0x01) == 0 {
			d.frameType = FrameTypeKey
		} else {
			d.frameType = FrameTypeDelta
		}
	}

	d.buffer = append(d.buffer, d.depacketizer.Payload...)

	if packet.Header.Marker {
		frame := &EncodedFrame{
			Data:      make([]byte, len(d.buffer)),
			FrameType: d.frameType,
			Timestamp: d.timestamp,
		}
		copy(frame.Data, d.buffer)
		d.buffer = d.buffer[:0]
		d.frameType = FrameTypeUnknown
		return frame, nil
	}
	return nil, nil
}

// DepacketizeBytes processes raw RTP packet bytes.
func (d *VP8Depacketizer) DepacketizeBytes(data []byte) (*EncodedFrame, error) {
	var pkt rtp.Packet
	if err := pkt.Unmarshal(data); err != nil {
		return nil, err
	}
	return d.Depacketize(&pkt)
}

// Reset clears any buffered partial frames.
func (d *VP8Depacketizer) Reset() {
	d.mu.Lock()
	d.buffer = d.buffer[:0]
	d.timestamp = 0
	d.frameType = FrameTypeUnknown
	d.mu.Unlock()
}

func init() {
	RegisterVideoPacketizer(VideoCodecVP8, func(ssrc uint32, pt uint8, mtu int) (RTPPacketizer, error) {
		return NewVP8Packetizer(ssrc, pt, mtu)
	})
	RegisterVideoDepacketizer(VideoCodecVP8, func() (RTPDepacketizer, error) {
		return NewVP8Depacketizer()
	})
}
