package media

import (
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/pion/rtp"
)

// H264 NAL unit types
const (
	nalTypeSlice    = 1
	nalTypeIDR      = 5
	nalTypeSEI      = 6
	nalTypeSPS      = 7
	nalTypePPS      = 8
	nalTypeFUA      = 28 // Fragmentation Unit A
)

// H264Packetizer implements RTPPacketizer for H.264.
type H264Packetizer struct {
	ssrc        uint32
	payloadType uint8
	mtu         int
	sequencer   rtp.Sequencer
	clockRate   uint32
	mu          sync.Mutex
}

// NewH264Packetizer creates a new H.264 RTP packetizer.
func NewH264Packetizer(ssrc uint32, payloadType uint8, mtu int) *H264Packetizer {
	if mtu <= 0 {
		mtu = 1200
	}
	return &H264Packetizer{
		ssrc:        ssrc,
		payloadType: payloadType,
		mtu:         mtu,
		sequencer:   rtp.NewRandomSequencer(),
		clockRate:   90000,
	}
}

// Packetize converts an H.264 encoded frame into RTP packets.
// Input should be Annex B format (with start codes).
func (p *H264Packetizer) Packetize(frame *EncodedFrame) ([]*rtp.Packet, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(frame.Data) == 0 {
		return nil, nil
	}

	// Parse NAL units from Annex B format
	nalUnits := parseAnnexBNALUnits(frame.Data)
	if len(nalUnits) == 0 {
		return nil, fmt.Errorf("no NAL units found in frame")
	}

	var packets []*rtp.Packet

	for i, nalu := range nalUnits {
		isLast := i == len(nalUnits)-1

		if len(nalu) <= p.mtu-12 { // RTP header is 12 bytes
			// Single NAL unit packet
			pkt := &rtp.Packet{
				Header: rtp.Header{
					Version:        2,
					Padding:        false,
					Extension:      false,
					Marker:         isLast,
					PayloadType:    p.payloadType,
					SequenceNumber: p.sequencer.NextSequenceNumber(),
					Timestamp:      frame.Timestamp,
					SSRC:           p.ssrc,
				},
				Payload: nalu,
			}
			packets = append(packets, pkt)
		} else {
			// Fragment using FU-A
			fuPackets := p.fragmentNALUnit(nalu, frame.Timestamp, isLast)
			packets = append(packets, fuPackets...)
		}
	}

	return packets, nil
}

// fragmentNALUnit fragments a large NAL unit into FU-A packets.
func (p *H264Packetizer) fragmentNALUnit(nalu []byte, timestamp uint32, isLastNALU bool) []*rtp.Packet {
	if len(nalu) == 0 {
		return nil
	}

	nalHeader := nalu[0]
	nalType := nalHeader & 0x1F
	nri := nalHeader & 0x60

	// Skip the NAL header byte
	payload := nalu[1:]
	maxPayload := p.mtu - 12 - 2 // RTP header (12) + FU indicator + FU header

	var packets []*rtp.Packet
	offset := 0

	for offset < len(payload) {
		end := offset + maxPayload
		if end > len(payload) {
			end = len(payload)
		}

		isStart := offset == 0
		isEnd := end == len(payload)

		// FU indicator: F=0, NRI from original, Type=28 (FU-A)
		fuIndicator := nri | nalTypeFUA

		// FU header: S=start, E=end, R=0, Type=original NAL type
		fuHeader := nalType
		if isStart {
			fuHeader |= 0x80 // Start bit
		}
		if isEnd {
			fuHeader |= 0x40 // End bit
		}

		// Build payload: FU indicator + FU header + fragment
		pktPayload := make([]byte, 2+end-offset)
		pktPayload[0] = fuIndicator
		pktPayload[1] = fuHeader
		copy(pktPayload[2:], payload[offset:end])

		// Marker bit only on the last packet of the last NAL unit
		marker := isEnd && isLastNALU

		pkt := &rtp.Packet{
			Header: rtp.Header{
				Version:        2,
				Padding:        false,
				Extension:      false,
				Marker:         marker,
				PayloadType:    p.payloadType,
				SequenceNumber: p.sequencer.NextSequenceNumber(),
				Timestamp:      timestamp,
				SSRC:           p.ssrc,
			},
			Payload: pktPayload,
		}
		packets = append(packets, pkt)

		offset = end
	}

	return packets
}

// Codec returns the codec type.
func (p *H264Packetizer) Codec() VideoCodec {
	return VideoCodecH264
}

// PacketizeToBytes converts an encoded H.264 frame to raw RTP packet bytes.
func (p *H264Packetizer) PacketizeToBytes(frame *EncodedFrame) ([][]byte, error) {
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

func (p *H264Packetizer) SetSSRC(ssrc uint32)     { p.mu.Lock(); p.ssrc = ssrc; p.mu.Unlock() }
func (p *H264Packetizer) SSRC() uint32            { p.mu.Lock(); defer p.mu.Unlock(); return p.ssrc }
func (p *H264Packetizer) PayloadType() uint8      { p.mu.Lock(); defer p.mu.Unlock(); return p.payloadType }
func (p *H264Packetizer) SetPayloadType(pt uint8) { p.mu.Lock(); p.payloadType = pt; p.mu.Unlock() }
func (p *H264Packetizer) MTU() int                { p.mu.Lock(); defer p.mu.Unlock(); return p.mtu }
func (p *H264Packetizer) SetMTU(mtu int)          { p.mu.Lock(); p.mtu = mtu; p.mu.Unlock() }

// parseAnnexBNALUnits parses Annex B format into individual NAL units.
// Annex B uses start codes: 0x00000001 or 0x000001
func parseAnnexBNALUnits(data []byte) [][]byte {
	var nalUnits [][]byte
	var start int = -1

	for i := 0; i < len(data); i++ {
		// Look for start code
		if i+3 < len(data) && data[i] == 0 && data[i+1] == 0 && data[i+2] == 0 && data[i+3] == 1 {
			// 4-byte start code
			if start >= 0 {
				// End of previous NAL unit
				nalu := data[start:i]
				if len(nalu) > 0 {
					nalUnits = append(nalUnits, nalu)
				}
			}
			start = i + 4
			i += 3
		} else if i+2 < len(data) && data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 {
			// 3-byte start code
			if start >= 0 {
				nalu := data[start:i]
				if len(nalu) > 0 {
					nalUnits = append(nalUnits, nalu)
				}
			}
			start = i + 3
			i += 2
		}
	}

	// Handle last NAL unit
	if start >= 0 && start < len(data) {
		nalu := data[start:]
		if len(nalu) > 0 {
			nalUnits = append(nalUnits, nalu)
		}
	}

	return nalUnits
}

// H264Depacketizer reassembles H.264 NAL units from RTP packets.
type H264Depacketizer struct {
	frameData   []byte    // Accumulated NAL data for current frame (Annex-B format)
	fuaBuffer   []byte    // Buffer for FU-A fragments (single NAL being assembled)
	fragmenting bool      // True when in the middle of FU-A fragmentation
	timestamp   uint32    // Current frame timestamp
	frameType   FrameType // Current frame type (key or delta)
	mu          sync.Mutex
}

// NewH264Depacketizer creates a new H.264 RTP depacketizer.
func NewH264Depacketizer() *H264Depacketizer {
	return &H264Depacketizer{}
}

// Depacketize processes an RTP packet and returns a complete frame if available.
// The returned frame contains Annex-B formatted NAL units (start codes + NAL data).
func (d *H264Depacketizer) Depacketize(pkt *rtp.Packet) (*EncodedFrame, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(pkt.Payload) == 0 {
		return nil, nil
	}

	nalType := pkt.Payload[0] & 0x1F

	// Handle timestamp changes (new frame started)
	if d.timestamp != 0 && d.timestamp != pkt.Header.Timestamp {
		d.frameData = d.frameData[:0]
		d.fuaBuffer = d.fuaBuffer[:0]
		d.fragmenting = false
		d.frameType = FrameTypeUnknown
	}
	d.timestamp = pkt.Header.Timestamp

	switch {
	case nalType >= 1 && nalType <= 23:
		// Single NAL unit packet - accumulate into frame
		// Detect keyframe (IDR = type 5)
		if nalType == nalTypeIDR {
			d.frameType = FrameTypeKey
		} else if d.frameType != FrameTypeKey {
			d.frameType = FrameTypeDelta
		}
		// Append with Annex-B start code
		d.frameData = append(d.frameData, 0, 0, 0, 1)
		d.frameData = append(d.frameData, pkt.Payload...)

	case nalType == 24:
		// STAP-A (Single-time aggregation packet) - multiple NALs in one packet
		if err := d.depacketizeSTAPA(pkt.Payload); err != nil {
			return nil, err
		}

	case nalType == 28:
		// FU-A (Fragmentation unit) - NAL split across multiple packets
		if err := d.depacketizeFUA(pkt.Payload); err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("unsupported NAL type: %d", nalType)
	}

	// Return frame when marker bit is set (end of frame)
	if pkt.Header.Marker && len(d.frameData) > 0 {
		frame := &EncodedFrame{
			Data:      make([]byte, len(d.frameData)),
			FrameType: d.frameType,
			Timestamp: d.timestamp,
		}
		copy(frame.Data, d.frameData)

		// Reset for next frame
		d.frameData = d.frameData[:0]
		d.frameType = FrameTypeUnknown
		return frame, nil
	}

	return nil, nil
}

func (d *H264Depacketizer) depacketizeSTAPA(payload []byte) error {
	// Skip STAP-A header
	offset := 1

	for offset < len(payload) {
		if offset+2 > len(payload) {
			break
		}
		naluSize := int(binary.BigEndian.Uint16(payload[offset:]))
		offset += 2

		if offset+naluSize > len(payload) {
			break
		}

		// Check NAL type for keyframe detection
		if naluSize > 0 {
			nalType := payload[offset] & 0x1F
			if nalType == nalTypeIDR {
				d.frameType = FrameTypeKey
			} else if d.frameType != FrameTypeKey {
				d.frameType = FrameTypeDelta
			}
		}

		// Add start code + NAL unit to frame data
		d.frameData = append(d.frameData, 0, 0, 0, 1)
		d.frameData = append(d.frameData, payload[offset:offset+naluSize]...)
		offset += naluSize
	}

	return nil
}

func (d *H264Depacketizer) depacketizeFUA(payload []byte) error {
	if len(payload) < 2 {
		return fmt.Errorf("FU-A packet too short")
	}

	fuIndicator := payload[0]
	fuHeader := payload[1]

	isStart := (fuHeader & 0x80) != 0
	isEnd := (fuHeader & 0x40) != 0
	nalType := fuHeader & 0x1F

	if isStart {
		// Check for keyframe (IDR = type 5)
		if nalType == nalTypeIDR {
			d.frameType = FrameTypeKey
		} else if d.frameType != FrameTypeKey {
			d.frameType = FrameTypeDelta
		}

		// Reconstruct NAL header and start new FU-A buffer
		nalHeader := (fuIndicator & 0xE0) | nalType
		d.fuaBuffer = d.fuaBuffer[:0]
		d.fuaBuffer = append(d.fuaBuffer, nalHeader)
		d.fragmenting = true
	}

	if !d.fragmenting {
		return nil
	}

	// Append fragment data (skip FU indicator and header)
	d.fuaBuffer = append(d.fuaBuffer, payload[2:]...)

	if isEnd {
		// FU-A complete - append to frame data with start code
		d.frameData = append(d.frameData, 0, 0, 0, 1)
		d.frameData = append(d.frameData, d.fuaBuffer...)
		d.fuaBuffer = d.fuaBuffer[:0]
		d.fragmenting = false
	}

	return nil
}

func (d *H264Depacketizer) reset() {
	d.frameData = d.frameData[:0]
	d.fuaBuffer = d.fuaBuffer[:0]
	d.fragmenting = false
	d.frameType = FrameTypeUnknown
}

// DepacketizeBytes processes raw RTP packet bytes.
func (d *H264Depacketizer) DepacketizeBytes(data []byte) (*EncodedFrame, error) {
	var pkt rtp.Packet
	if err := pkt.Unmarshal(data); err != nil {
		return nil, err
	}
	return d.Depacketize(&pkt)
}

// Reset clears any buffered partial frames.
func (d *H264Depacketizer) Reset() {
	d.mu.Lock()
	d.frameData = d.frameData[:0]
	d.fuaBuffer = d.fuaBuffer[:0]
	d.timestamp = 0
	d.frameType = FrameTypeUnknown
	d.fragmenting = false
	d.mu.Unlock()
}

// Codec returns the codec type.
func (d *H264Depacketizer) Codec() VideoCodec {
	return VideoCodecH264
}

func init() {
	RegisterVideoPacketizer(VideoCodecH264, func(ssrc uint32, pt uint8, mtu int) (RTPPacketizer, error) {
		return NewH264Packetizer(ssrc, pt, mtu), nil
	})
	RegisterVideoDepacketizer(VideoCodecH264, func() (RTPDepacketizer, error) {
		return NewH264Depacketizer(), nil
	})
}
