package media

import (
	"sync"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

// OpusPacketizer implements RTPPacketizer for Opus audio using pion's codecs.
type OpusPacketizer struct {
	ssrc        uint32
	payloadType uint8
	mtu         int
	sequencer   rtp.Sequencer
	payloader   *codecs.OpusPayloader
	mu          sync.Mutex
}

// NewOpusPacketizer creates a new Opus RTP packetizer.
func NewOpusPacketizer(ssrc uint32, pt uint8, mtu int) (*OpusPacketizer, error) {
	if mtu <= 0 {
		mtu = DefaultMTU
	}
	return &OpusPacketizer{
		ssrc:        ssrc,
		payloadType: pt,
		mtu:         mtu,
		sequencer:   rtp.NewRandomSequencer(),
		payloader:   &codecs.OpusPayloader{},
	}, nil
}

// Packetize converts encoded Opus audio to RTP packets.
func (p *OpusPacketizer) Packetize(frame *EncodedFrame) ([]*RTPPacket, error) {
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
				Marker:         true, // Audio typically sets marker
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

// PacketizeAudio is a convenience method for EncodedAudio.
func (p *OpusPacketizer) PacketizeAudio(audio *EncodedAudio) ([]*RTPPacket, error) {
	return p.Packetize(&EncodedFrame{
		Data:      audio.Data,
		Timestamp: audio.Timestamp,
		Duration:  audio.Duration,
	})
}

// PacketizeToBytes converts encoded Opus audio to raw RTP packet bytes.
func (p *OpusPacketizer) PacketizeToBytes(frame *EncodedFrame) ([][]byte, error) {
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

func (p *OpusPacketizer) SetSSRC(ssrc uint32)     { p.mu.Lock(); p.ssrc = ssrc; p.mu.Unlock() }
func (p *OpusPacketizer) SSRC() uint32            { p.mu.Lock(); defer p.mu.Unlock(); return p.ssrc }
func (p *OpusPacketizer) PayloadType() uint8      { p.mu.Lock(); defer p.mu.Unlock(); return p.payloadType }
func (p *OpusPacketizer) SetPayloadType(pt uint8) { p.mu.Lock(); p.payloadType = pt; p.mu.Unlock() }
func (p *OpusPacketizer) MTU() int                { p.mu.Lock(); defer p.mu.Unlock(); return p.mtu }
func (p *OpusPacketizer) SetMTU(mtu int)          { p.mu.Lock(); p.mtu = mtu; p.mu.Unlock() }

// OpusDepacketizer implements RTPDepacketizer for Opus audio using pion's codecs.
type OpusDepacketizer struct {
	depacketizer codecs.OpusPacket
	mu           sync.Mutex
}

// NewOpusDepacketizer creates a new Opus RTP depacketizer.
func NewOpusDepacketizer() (*OpusDepacketizer, error) {
	return &OpusDepacketizer{}, nil
}

// Depacketize processes an RTP packet and returns an encoded Opus frame.
func (d *OpusDepacketizer) Depacketize(packet *RTPPacket) (*EncodedFrame, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(packet.Payload) == 0 {
		return nil, nil
	}

	data := make([]byte, len(packet.Payload))
	copy(data, packet.Payload)

	return &EncodedFrame{
		Data:      data,
		FrameType: FrameTypeKey, // Opus frames are independent
		Timestamp: packet.Header.Timestamp,
	}, nil
}

// DepacketizeBytes processes raw RTP packet bytes.
func (d *OpusDepacketizer) DepacketizeBytes(data []byte) (*EncodedFrame, error) {
	var pkt rtp.Packet
	if err := pkt.Unmarshal(data); err != nil {
		return nil, err
	}
	return d.Depacketize(&pkt)
}

// DepacketizeToAudio extracts EncodedAudio from an RTP packet.
func (d *OpusDepacketizer) DepacketizeToAudio(packet *RTPPacket) (*EncodedAudio, error) {
	frame, err := d.Depacketize(packet)
	if err != nil || frame == nil {
		return nil, err
	}
	return &EncodedAudio{
		Data:      frame.Data,
		Timestamp: frame.Timestamp,
		Duration:  frame.Duration,
	}, nil
}

// Reset clears any buffered state (no-op for Opus).
func (d *OpusDepacketizer) Reset() {}

func init() {
	RegisterAudioPacketizer(AudioCodecOpus, func(ssrc uint32, pt uint8, mtu int) (RTPPacketizer, error) {
		return NewOpusPacketizer(ssrc, pt, mtu)
	})
	RegisterAudioDepacketizer(AudioCodecOpus, func() (RTPDepacketizer, error) {
		return NewOpusDepacketizer()
	})
}
