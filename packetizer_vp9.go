package media

import (
	"fmt"
	"sync"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

// VP9Packetizer implements RTPPacketizer for VP9 using pion's codecs.
type VP9Packetizer struct {
	ssrc        uint32
	payloadType uint8
	mtu         int
	sequencer   rtp.Sequencer
	payloader   *codecs.VP9Payloader
	mu          sync.Mutex
}

// NewVP9Packetizer creates a new VP9 RTP packetizer.
func NewVP9Packetizer(ssrc uint32, pt uint8, mtu int) (*VP9Packetizer, error) {
	if mtu <= 0 {
		mtu = DefaultMTU
	}
	return &VP9Packetizer{
		ssrc:        ssrc,
		payloadType: pt,
		mtu:         mtu,
		sequencer:   rtp.NewRandomSequencer(),
		payloader:   &codecs.VP9Payloader{},
	}, nil
}

// Packetize converts an encoded VP9 frame to RTP packets.
func (p *VP9Packetizer) Packetize(frame *EncodedFrame) ([]*RTPPacket, error) {
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

// PacketizeToBytes converts an encoded VP9 frame to raw RTP packet bytes.
func (p *VP9Packetizer) PacketizeToBytes(frame *EncodedFrame) ([][]byte, error) {
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

func (p *VP9Packetizer) SetSSRC(ssrc uint32)     { p.mu.Lock(); p.ssrc = ssrc; p.mu.Unlock() }
func (p *VP9Packetizer) SSRC() uint32            { p.mu.Lock(); defer p.mu.Unlock(); return p.ssrc }
func (p *VP9Packetizer) PayloadType() uint8      { p.mu.Lock(); defer p.mu.Unlock(); return p.payloadType }
func (p *VP9Packetizer) SetPayloadType(pt uint8) { p.mu.Lock(); p.payloadType = pt; p.mu.Unlock() }
func (p *VP9Packetizer) MTU() int                { p.mu.Lock(); defer p.mu.Unlock(); return p.mtu }
func (p *VP9Packetizer) SetMTU(mtu int)          { p.mu.Lock(); p.mtu = mtu; p.mu.Unlock() }
func (p *VP9Packetizer) Codec() VideoCodec       { return VideoCodecVP9 }

// VP9Depacketizer implements RTPDepacketizer for VP9 using pion's codecs.
type VP9Depacketizer struct {
	depacketizer      codecs.VP9Packet
	buffer            []byte
	timestamp         uint32
	frameType         FrameType
	lastCompletedTs   uint32 // Track last completed frame timestamp
	hasCompletedFrame bool   // Whether we've completed at least one frame
	mu                sync.Mutex
}

// NewVP9Depacketizer creates a new VP9 RTP depacketizer.
func NewVP9Depacketizer() (*VP9Depacketizer, error) {
	return &VP9Depacketizer{}, nil
}

// Depacketize processes an RTP packet and returns a complete frame if available.
func (d *VP9Depacketizer) Depacketize(packet *RTPPacket) (*EncodedFrame, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, err := d.depacketizer.Unmarshal(packet.Payload); err != nil {
		return nil, fmt.Errorf("VP9 unmarshal failed: %w", err)
	}

	// Discard late-arriving packets for already completed frames
	if d.hasCompletedFrame && IsRTPTimestampOlder(packet.Header.Timestamp, d.lastCompletedTs) {
		return nil, nil
	}

	// Handle timestamp changes (new frame started)
	if d.timestamp != 0 && d.timestamp != packet.Header.Timestamp {
		d.buffer = d.buffer[:0]
	}
	d.timestamp = packet.Header.Timestamp

	// Detect keyframe from VP9 header
	if d.depacketizer.B { // Beginning of frame
		if d.depacketizer.P { // Inter-picture predicted
			d.frameType = FrameTypeDelta
		} else {
			d.frameType = FrameTypeKey
		}
	}

	d.buffer = append(d.buffer, d.depacketizer.Payload...)

	// Frame complete when marker or end flag is set
	if packet.Header.Marker || d.depacketizer.E {
		frame := &EncodedFrame{
			Data:            make([]byte, len(d.buffer)),
			FrameType:       d.frameType,
			Timestamp:       d.timestamp,
			TemporalLayerID: d.depacketizer.TID,
			SpatialLayerID:  d.depacketizer.SID,
		}
		copy(frame.Data, d.buffer)

		// Track this as completed
		d.lastCompletedTs = d.timestamp
		d.hasCompletedFrame = true

		d.buffer = d.buffer[:0]
		d.frameType = FrameTypeUnknown
		return frame, nil
	}
	return nil, nil
}


// DepacketizeBytes processes raw RTP packet bytes.
func (d *VP9Depacketizer) DepacketizeBytes(data []byte) (*EncodedFrame, error) {
	var pkt rtp.Packet
	if err := pkt.Unmarshal(data); err != nil {
		return nil, err
	}
	return d.Depacketize(&pkt)
}

// Reset clears any buffered partial frames and resets tracking state.
func (d *VP9Depacketizer) Reset() {
	d.mu.Lock()
	d.buffer = d.buffer[:0]
	d.timestamp = 0
	d.frameType = FrameTypeUnknown
	d.lastCompletedTs = 0
	d.hasCompletedFrame = false
	d.mu.Unlock()
}

// Codec returns the codec type.
func (d *VP9Depacketizer) Codec() VideoCodec { return VideoCodecVP9 }

func init() {
	RegisterVideoPacketizer(VideoCodecVP9, func(ssrc uint32, pt uint8, mtu int) (RTPPacketizer, error) {
		return NewVP9Packetizer(ssrc, pt, mtu)
	})
	RegisterVideoDepacketizer(VideoCodecVP9, func() (RTPDepacketizer, error) {
		return NewVP9Depacketizer()
	})
}
