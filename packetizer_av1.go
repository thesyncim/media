package media

import (
	"encoding/hex"
	"log"
	"sync"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

// av1DebugLog controls debug logging for AV1 depacketization
var av1DebugLog = true
var av1DebugCount = 0

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
// Implements RFC 9000 AV1 RTP payload format parsing directly.
// Reformats OBUs to include size fields for libaom compatibility.
type AV1Depacketizer struct {
	obuBuffer    []byte   // Accumulated complete OBUs
	partialOBU   []byte   // Partial OBU fragment being accumulated
	seqHeader    []byte   // Cached sequence header for delta frames
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

	if len(pkt.Payload) < 2 {
		return nil, nil
	}

	// Handle timestamp changes (new frame started)
	if d.timestamp != 0 && d.timestamp != pkt.Header.Timestamp {
		d.obuBuffer = d.obuBuffer[:0]
		d.partialOBU = d.partialOBU[:0]
	}
	d.timestamp = pkt.Header.Timestamp

	// Parse RFC 9000 aggregation header
	aggHeader := pkt.Payload[0]
	z := (aggHeader >> 7) & 1 // First OBU is continuation from previous packet
	y := (aggHeader >> 6) & 1 // Last OBU continues to next packet
	w := (aggHeader >> 4) & 3 // OBU count hint (0=1, 1=2, 2=variable, 3=reserved)
	n := (aggHeader >> 3) & 1 // New temporal unit

	if n == 1 {
		d.frameType = FrameTypeKey
	} else if d.frameType != FrameTypeKey {
		d.frameType = FrameTypeDelta
	}

	offset := 1 // Skip aggregation header

	// Determine number of OBU elements and how lengths are encoded per RFC 9000:
	// W=0: 1 element, no length prefix
	// W=1: 2 elements, first has length, second doesn't
	// W=2: variable elements, all but last have length
	// W=3: reserved

	switch w {
	case 0:
		// Single OBU element, rest of packet is the element data
		obuData := pkt.Payload[offset:]
		if z == 1 {
			// Continuation of previous OBU
			d.partialOBU = append(d.partialOBU, obuData...)
		} else {
			// New complete OBU
			d.partialOBU = append(d.partialOBU[:0], obuData...)
		}
		if y == 0 {
			// OBU is complete
			d.obuBuffer = append(d.obuBuffer, d.partialOBU...)
			d.partialOBU = d.partialOBU[:0]
		}

	case 1:
		// Exactly 2 elements: first has length, second doesn't
		// Handle Z=1 continuation first
		if z == 1 && len(d.partialOBU) > 0 {
			// First element is continuation
			elemLen, lenBytes := av1ReadLEB128(pkt.Payload[offset:])
			if lenBytes > 0 && offset+lenBytes+int(elemLen) <= len(pkt.Payload) {
				offset += lenBytes
				d.partialOBU = append(d.partialOBU, pkt.Payload[offset:offset+int(elemLen)]...)
				offset += int(elemLen)
				d.obuBuffer = append(d.obuBuffer, d.partialOBU...)
				d.partialOBU = d.partialOBU[:0]
			}
		} else if offset < len(pkt.Payload) {
			// First element with length prefix
			elemLen, lenBytes := av1ReadLEB128(pkt.Payload[offset:])
			if lenBytes > 0 && offset+lenBytes+int(elemLen) <= len(pkt.Payload) {
				offset += lenBytes
				d.obuBuffer = append(d.obuBuffer, pkt.Payload[offset:offset+int(elemLen)]...)
				offset += int(elemLen)
			}
		}
		// Second element takes rest of packet
		if offset < len(pkt.Payload) {
			obuData := pkt.Payload[offset:]
			if y == 1 {
				d.partialOBU = append(d.partialOBU[:0], obuData...)
			} else {
				d.obuBuffer = append(d.obuBuffer, obuData...)
			}
		}

	case 2:
		// Variable number of elements, all but last have length prefix
		// Handle Z=1 continuation first
		if z == 1 && len(d.partialOBU) > 0 {
			elemLen, lenBytes := av1ReadLEB128(pkt.Payload[offset:])
			if lenBytes > 0 && offset+lenBytes+int(elemLen) <= len(pkt.Payload) {
				offset += lenBytes
				d.partialOBU = append(d.partialOBU, pkt.Payload[offset:offset+int(elemLen)]...)
				offset += int(elemLen)
				d.obuBuffer = append(d.obuBuffer, d.partialOBU...)
				d.partialOBU = d.partialOBU[:0]
			}
		}

		// Parse elements until we reach the last one
		for offset < len(pkt.Payload) {
			// Try to read LEB128 length
			elemLen, lenBytes := av1ReadLEB128(pkt.Payload[offset:])

			// Check if this element would fit - if not, this is the last element
			if lenBytes == 0 || offset+lenBytes+int(elemLen) > len(pkt.Payload) {
				// Last element - rest of packet
				obuData := pkt.Payload[offset:]
				if y == 1 {
					d.partialOBU = append(d.partialOBU[:0], obuData...)
				} else {
					d.obuBuffer = append(d.obuBuffer, obuData...)
				}
				break
			}

			// Complete element with length prefix
			offset += lenBytes
			d.obuBuffer = append(d.obuBuffer, pkt.Payload[offset:offset+int(elemLen)]...)
			offset += int(elemLen)
		}
	}

	if pkt.Header.Marker {
		// Frame complete - process accumulated OBUs
		av1DebugCount++
		if av1DebugCount <= 3 {
			maxBytes := 64
			if len(d.obuBuffer) < maxBytes {
				maxBytes = len(d.obuBuffer)
			}
			log.Printf("AV1 Depacketizer: frame %d, %d bytes, keyframe=%v, first bytes: %s",
				av1DebugCount, len(d.obuBuffer), d.frameType == FrameTypeKey,
				hex.EncodeToString(d.obuBuffer[:maxBytes]))
			av1LogOBUStructure(d.obuBuffer)
		}

		// Cache sequence header from keyframes
		if d.frameType == FrameTypeKey {
			seqHdr := av1ExtractSequenceHeader(d.obuBuffer)
			if seqHdr != nil {
				d.seqHeader = seqHdr
			}
		}

		// Convert to proper OBU format with size fields for libaom
		frameData := av1NormalizeOBUs(d.obuBuffer, d.seqHeader, d.frameType == FrameTypeKey)

		if av1DebugCount <= 3 {
			log.Printf("AV1 Depacketizer: normalized %d -> %d bytes",
				len(d.obuBuffer), len(frameData))
			av1LogOBUStructure(frameData)
		}

		frame := &EncodedFrame{
			Data:      frameData,
			FrameType: d.frameType,
			Timestamp: d.timestamp,
		}
		d.obuBuffer = d.obuBuffer[:0]
		d.partialOBU = d.partialOBU[:0]
		d.frameType = FrameTypeUnknown
		return frame, nil
	}

	return nil, nil
}

// av1ExtractSequenceHeader extracts the sequence header OBU from frame data.
func av1ExtractSequenceHeader(data []byte) []byte {
	offset := 0
	for offset < len(data) {
		if offset >= len(data) {
			break
		}

		header := data[offset]
		forbidden := (header >> 7) & 0x01
		obuType := (header >> 3) & 0x0F
		extFlag := (header >> 2) & 0x01
		hasSize := (header >> 1) & 0x01

		if forbidden != 0 {
			break
		}

		headerSize := 1
		if extFlag == 1 {
			headerSize = 2
		}

		if offset+headerSize > len(data) {
			break
		}

		if hasSize == 1 {
			sizeOffset := offset + headerSize
			if sizeOffset >= len(data) {
				break
			}
			obuPayloadSize, sizeBytes := av1ReadLEB128(data[sizeOffset:])
			if sizeBytes == 0 {
				break
			}

			totalOBULen := headerSize + sizeBytes + int(obuPayloadSize)
			if offset+totalOBULen > len(data) {
				break
			}

			// If this is a Sequence Header, return it
			if obuType == 1 {
				return data[offset : offset+totalOBULen]
			}

			offset += totalOBULen
		} else {
			// OBU without size field - last OBU
			if obuType == 1 {
				return data[offset:]
			}
			break
		}
	}
	return nil
}

// av1LogOBUStructure logs the OBU structure for debugging
func av1LogOBUStructure(data []byte) {
	offset := 0
	obuNum := 0
	for offset < len(data) && obuNum < 10 {
		if offset >= len(data) {
			break
		}

		header := data[offset]
		forbidden := (header >> 7) & 0x01
		obuType := (header >> 3) & 0x0F
		extFlag := (header >> 2) & 0x01
		hasSize := (header >> 1) & 0x01

		typeName := av1OBUTypeName(obuType)

		if forbidden != 0 {
			log.Printf("  OBU %d @ offset %d: INVALID (forbidden bit set) header=0x%02x", obuNum, offset, header)
			break
		}

		headerSize := 1
		if extFlag == 1 {
			headerSize = 2
		}

		if hasSize == 1 && offset+headerSize < len(data) {
			size, sizeBytes := av1ReadLEB128(data[offset+headerSize:])
			if sizeBytes > 0 {
				log.Printf("  OBU %d @ offset %d: type=%d (%s) ext=%d hasSize=1 size=%d",
					obuNum, offset, obuType, typeName, extFlag, size)
				offset += headerSize + sizeBytes + int(size)
			} else {
				log.Printf("  OBU %d @ offset %d: type=%d (%s) ext=%d hasSize=1 (invalid LEB128)",
					obuNum, offset, obuType, typeName, extFlag)
				break
			}
		} else {
			log.Printf("  OBU %d @ offset %d: type=%d (%s) ext=%d hasSize=0 (low-overhead, rest of frame)",
				obuNum, offset, obuType, typeName, extFlag)
			break // Can't determine size without parsing OBU internals
		}
		obuNum++
	}
}

func av1OBUTypeName(t byte) string {
	switch t {
	case 1:
		return "SequenceHeader"
	case 2:
		return "TemporalDelimiter"
	case 3:
		return "FrameHeader"
	case 4:
		return "TileGroup"
	case 5:
		return "Metadata"
	case 6:
		return "Frame"
	case 7:
		return "RedundantFrameHeader"
	case 8:
		return "TileList"
	case 15:
		return "Padding"
	default:
		return "Unknown"
	}
}

// av1NormalizeOBUs converts WebRTC AV1 data to a format libaom can decode.
// WebRTC AV1 (RFC 9000) sends OBUs directly - we just need to ensure size fields.
// seqHeader is cached from keyframes and prepended to delta frames if missing.
func av1NormalizeOBUs(data []byte, seqHeader []byte, isKeyframe bool) []byte {
	if len(data) == 0 {
		return data
	}

	var result []byte

	// Add Temporal Delimiter OBU at the start of each temporal unit
	// Header: 0x12 (type=2, hasSize=1), Size: 0x00 (empty payload)
	result = append(result, 0x12, 0x00)

	// For delta frames, prepend cached sequence header if not present in data
	if !isKeyframe && seqHeader != nil {
		hasSeqHdr := false
		if len(data) > 0 {
			header := data[0]
			obuType := (header >> 3) & 0x0F
			if obuType == 1 {
				hasSeqHdr = true
			}
		}
		if !hasSeqHdr {
			result = append(result, seqHeader...)
		}
	}

	offset := 0

	// Parse and copy OBUs, ensuring all have size fields
	for offset < len(data) {
		if offset >= len(data) {
			break
		}

		header := data[offset]

		// Check if this looks like a valid OBU header
		forbidden := (header >> 7) & 0x01
		obuType := (header >> 3) & 0x0F
		extFlag := (header >> 2) & 0x01
		hasSize := (header >> 1) & 0x01

		// If forbidden bit is set or invalid type, skip this byte
		if forbidden != 0 || !((obuType >= 1 && obuType <= 8) || obuType == 15) {
			offset++
			continue
		}

		headerSize := 1
		if extFlag == 1 {
			headerSize = 2
		}

		if offset+headerSize > len(data) {
			break
		}

		if hasSize == 1 {
			// OBU has size field - copy it as-is
			sizeOffset := offset + headerSize
			if sizeOffset >= len(data) {
				break
			}

			obuPayloadSize, sizeBytes := av1ReadLEB128(data[sizeOffset:])
			if sizeBytes == 0 {
				break
			}

			totalOBULen := headerSize + sizeBytes + int(obuPayloadSize)
			if offset+totalOBULen > len(data) {
				// Size points past end of data - treat rest as payload
				totalOBULen = len(data) - offset
			}

			result = append(result, data[offset:offset+totalOBULen]...)
			offset += totalOBULen
		} else {
			// OBU without size field - this is the last OBU, takes rest of data
			payloadStart := offset + headerSize
			payloadLen := len(data) - payloadStart

			// Rewrite header with hasSize=1
			newHeader := header | 0x02
			result = append(result, newHeader)

			if extFlag == 1 {
				result = append(result, data[offset+1])
			}

			result = append(result, av1WriteLEB128(uint64(payloadLen))...)
			result = append(result, data[payloadStart:]...)
			offset = len(data)
		}
	}

	return result
}

// av1ReadLEB128 reads a LEB128 encoded value from data.
// Returns the value and number of bytes consumed.
func av1ReadLEB128(data []byte) (uint64, int) {
	var value uint64
	for i := 0; i < len(data) && i < 8; i++ {
		b := data[i]
		value |= uint64(b&0x7F) << (i * 7)
		if (b & 0x80) == 0 {
			return value, i + 1
		}
	}
	return 0, 0 // Invalid LEB128
}

// av1WriteLEB128 encodes a value as LEB128.
func av1WriteLEB128(value uint64) []byte {
	if value == 0 {
		return []byte{0}
	}
	var result []byte
	for value > 0 {
		b := byte(value & 0x7F)
		value >>= 7
		if value > 0 {
			b |= 0x80
		}
		result = append(result, b)
	}
	return result
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
	d.obuBuffer = d.obuBuffer[:0]
	d.partialOBU = d.partialOBU[:0]
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
