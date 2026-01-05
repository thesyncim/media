package media

import (
	"encoding/binary"
	"encoding/hex"
	"log"
	"os"
	"sync"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

// av1DebugLog controls debug logging for AV1 depacketization
var av1DebugLog = false
var av1DebugCount = 0

// AV1CaptureFile is the file path to capture raw RTP packets for debugging.
// Set this before receiving packets to enable capture mode.
var AV1CaptureFile = ""
var av1CapturedPackets [][]byte
var av1CaptureLimit = 50 // Capture first N packets

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
// Uses pion's AV1Packet for RFC 9000 parsing, then reformats OBUs for libaom.
type AV1Depacketizer struct {
	av1Packet         codecs.AV1Packet // Pion's AV1Packet for proper RFC 9000 parsing
	obuBuffer         []byte           // Accumulated complete OBUs
	seqHeader         []byte           // Cached sequence header for delta frames
	timestamp         uint32
	frameType         FrameType
	lastCompletedTs   uint32 // Track last completed frame timestamp
	hasCompletedFrame bool   // Whether we've completed at least one frame
	mu                sync.Mutex
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

	// Discard late-arriving packets for already completed frames
	if d.hasCompletedFrame && isTimestampOlderAV1(pkt.Header.Timestamp, d.lastCompletedTs) {
		return nil, nil
	}

	// Capture raw packets for offline debugging
	if AV1CaptureFile != "" && len(av1CapturedPackets) < av1CaptureLimit {
		rawPkt, _ := pkt.Marshal()
		av1CapturedPackets = append(av1CapturedPackets, rawPkt)
		if len(av1CapturedPackets) == av1CaptureLimit {
			AV1SaveCapturedPackets()
		}
	}

	// Handle timestamp changes (new frame started)
	if d.timestamp != 0 && d.timestamp != pkt.Header.Timestamp {
		d.obuBuffer = d.obuBuffer[:0]
	}
	d.timestamp = pkt.Header.Timestamp

	// Debug: Log raw RTP payload for first few packets
	if av1DebugLog && av1DebugCount < 10 {
		aggHeader := pkt.Payload[0]
		z := (aggHeader >> 7) & 1
		y := (aggHeader >> 6) & 1
		w := (aggHeader >> 4) & 3
		n := (aggHeader >> 3) & 1
		maxBytes := 100
		if len(pkt.Payload) < maxBytes {
			maxBytes = len(pkt.Payload)
		}
		log.Printf("AV1 RTP: seq=%d ts=%d marker=%v len=%d Z=%d Y=%d W=%d N=%d payload=%s",
			pkt.Header.SequenceNumber, pkt.Header.Timestamp, pkt.Header.Marker,
			len(pkt.Payload), z, y, w, n, hex.EncodeToString(pkt.Payload[:maxBytes]))
	}

	// Use pion's AV1Packet to properly parse RFC 9000 format
	obus, err := d.av1Packet.Unmarshal(pkt.Payload)
	if err != nil {
		if av1DebugLog && av1DebugCount < 10 {
			log.Printf("AV1 Unmarshal error: %v", err)
		}
		return nil, nil // Drop corrupt packets
	}

	// Check for new coded video sequence
	if d.av1Packet.N {
		d.frameType = FrameTypeKey
	} else if d.frameType != FrameTypeKey {
		d.frameType = FrameTypeDelta
	}

	// Accumulate OBU data, adding size fields for each OBU element
	for _, obu := range d.av1Packet.OBUElements {
		if len(obu) > 0 {
			d.obuBuffer = append(d.obuBuffer, av1EnsureOBUSize(obu)...)
		}
	}

	// Also handle the remaining bytes returned from Unmarshal (last OBU fragment or complete OBU)
	if len(obus) > 0 {
		d.obuBuffer = append(d.obuBuffer, av1EnsureOBUSize(obus)...)
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

		// Track this as completed
		d.lastCompletedTs = d.timestamp
		d.hasCompletedFrame = true

		d.obuBuffer = d.obuBuffer[:0]
		d.frameType = FrameTypeUnknown
		return frame, nil
	}

	return nil, nil
}

// isTimestampOlderAV1 returns true if ts1 is older than or equal to ts2, handling 32-bit wraparound.
func isTimestampOlderAV1(ts1, ts2 uint32) bool {
	if ts1 == ts2 {
		return true
	}
	diff := ts2 - ts1
	return diff < 0x80000000
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
// Since we now add size fields during depacketization, this mainly adds
// Temporal Delimiter and prepends sequence header for delta frames.
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
			forbidden := (header >> 7) & 0x01
			obuType := (header >> 3) & 0x0F
			if forbidden == 0 && obuType == 1 {
				hasSeqHdr = true
			}
		}
		if !hasSeqHdr {
			result = append(result, seqHeader...)
		}
	}

	// Append the OBU data (should already have size fields from depacketization)
	result = append(result, data...)

	return result
}

// av1EnsureOBUSize takes an OBU element and ensures it has a size field.
// If the OBU already has hasSize=1, returns it unchanged.
// If hasSize=0, rewrites the header and prepends the size field.
func av1EnsureOBUSize(obu []byte) []byte {
	if len(obu) == 0 {
		return obu
	}

	header := obu[0]
	hasSize := (header >> 1) & 0x01
	extFlag := (header >> 2) & 0x01

	// Already has size field - return as-is
	if hasSize == 1 {
		return obu
	}

	// Need to add size field
	headerSize := 1
	if extFlag == 1 {
		headerSize = 2
	}

	if len(obu) < headerSize {
		return obu
	}

	payloadLen := len(obu) - headerSize

	// Build new OBU with size field
	newHeader := header | 0x02 // Set hasSize bit
	result := []byte{newHeader}

	if extFlag == 1 && len(obu) > 1 {
		result = append(result, obu[1]) // Extension byte
	}

	result = append(result, av1WriteLEB128(uint64(payloadLen))...)
	result = append(result, obu[headerSize:]...)

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
	d.timestamp = 0
	d.frameType = FrameTypeUnknown
	d.lastCompletedTs = 0
	d.hasCompletedFrame = false
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

// AV1SaveCapturedPackets writes captured RTP packets to the configured file.
// File format: [packet_count:uint32] + for each: [len:uint32][data:bytes]
func AV1SaveCapturedPackets() {
	if AV1CaptureFile == "" || len(av1CapturedPackets) == 0 {
		return
	}

	f, err := os.Create(AV1CaptureFile)
	if err != nil {
		log.Printf("AV1 Capture: failed to create file %s: %v", AV1CaptureFile, err)
		return
	}
	defer f.Close()

	// Write packet count
	countBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(countBuf, uint32(len(av1CapturedPackets)))
	if _, err := f.Write(countBuf); err != nil {
		log.Printf("AV1 Capture: failed to write count: %v", err)
		return
	}

	// Write each packet
	lenBuf := make([]byte, 4)
	for i, pkt := range av1CapturedPackets {
		binary.BigEndian.PutUint32(lenBuf, uint32(len(pkt)))
		if _, err := f.Write(lenBuf); err != nil {
			log.Printf("AV1 Capture: failed to write packet %d length: %v", i, err)
			return
		}
		if _, err := f.Write(pkt); err != nil {
			log.Printf("AV1 Capture: failed to write packet %d data: %v", i, err)
			return
		}
	}

	log.Printf("AV1 Capture: saved %d packets to %s", len(av1CapturedPackets), AV1CaptureFile)
}

// AV1LoadCapturedPackets loads RTP packets from a capture file.
// Returns the raw RTP packet bytes for replay testing.
func AV1LoadCapturedPackets(filename string) ([][]byte, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	if len(data) < 4 {
		return nil, nil
	}

	count := binary.BigEndian.Uint32(data[:4])
	offset := 4
	packets := make([][]byte, 0, count)

	for i := uint32(0); i < count && offset+4 <= len(data); i++ {
		pktLen := binary.BigEndian.Uint32(data[offset : offset+4])
		offset += 4

		if offset+int(pktLen) > len(data) {
			break
		}

		pkt := make([]byte, pktLen)
		copy(pkt, data[offset:offset+int(pktLen)])
		packets = append(packets, pkt)
		offset += int(pktLen)
	}

	return packets, nil
}

// AV1ClearCapturedPackets clears any captured packets and resets capture state.
func AV1ClearCapturedPackets() {
	av1CapturedPackets = nil
}

// AV1SetCaptureLimit sets the maximum number of packets to capture.
func AV1SetCaptureLimit(limit int) {
	av1CaptureLimit = limit
}
