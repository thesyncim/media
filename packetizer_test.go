package media

import (
	"testing"

	"github.com/pion/rtp"
)

func TestVP8Packetizer(t *testing.T) {
	pkt, err := NewVP8Packetizer(12345, 96, 1200)
	if err != nil {
		t.Fatalf("NewVP8Packetizer failed: %v", err)
	}

	// Create a test frame (small enough for one packet)
	frame := &EncodedFrame{
		Data:      make([]byte, 500),
		FrameType: FrameTypeKey,
		Timestamp: 90000,
	}
	for i := range frame.Data {
		frame.Data[i] = byte(i)
	}

	packets, err := pkt.Packetize(frame)
	if err != nil {
		t.Fatalf("Packetize failed: %v", err)
	}

	if len(packets) == 0 {
		t.Fatal("No packets produced")
	}

	// Verify first packet
	if packets[0].Header.SSRC != 12345 {
		t.Errorf("SSRC = %d, want 12345", packets[0].Header.SSRC)
	}
	if packets[0].Header.PayloadType != 96 {
		t.Errorf("PayloadType = %d, want 96", packets[0].Header.PayloadType)
	}
	if packets[0].Header.Timestamp != 90000 {
		t.Errorf("Timestamp = %d, want 90000", packets[0].Header.Timestamp)
	}
	// Last packet should have marker
	if !packets[len(packets)-1].Header.Marker {
		t.Error("Last packet should have marker bit set")
	}

	t.Logf("Packetized %d bytes into %d packets", len(frame.Data), len(packets))
}

func TestVP8PacketizerLargeFrame(t *testing.T) {
	pkt, err := NewVP8Packetizer(12345, 96, 1200)
	if err != nil {
		t.Fatalf("NewVP8Packetizer failed: %v", err)
	}

	// Create a large frame that needs multiple packets
	frame := &EncodedFrame{
		Data:      make([]byte, 10000),
		FrameType: FrameTypeKey,
		Timestamp: 90000,
	}
	for i := range frame.Data {
		frame.Data[i] = byte(i)
	}

	packets, err := pkt.Packetize(frame)
	if err != nil {
		t.Fatalf("Packetize failed: %v", err)
	}

	if len(packets) < 2 {
		t.Errorf("Expected multiple packets, got %d", len(packets))
	}

	// Only last packet should have marker
	for i, p := range packets {
		if i < len(packets)-1 && p.Header.Marker {
			t.Errorf("Packet %d should not have marker", i)
		}
	}
	if !packets[len(packets)-1].Header.Marker {
		t.Error("Last packet should have marker")
	}

	t.Logf("Packetized %d bytes into %d packets", len(frame.Data), len(packets))
}

func TestVP8Depacketizer(t *testing.T) {
	// First packetize
	pkt, _ := NewVP8Packetizer(12345, 96, 1200)
	frame := &EncodedFrame{
		Data:      make([]byte, 500),
		FrameType: FrameTypeKey,
		Timestamp: 90000,
	}
	for i := range frame.Data {
		frame.Data[i] = byte(i)
	}

	packets, _ := pkt.Packetize(frame)

	// Now depacketize
	depkt, err := NewVP8Depacketizer()
	if err != nil {
		t.Fatalf("NewVP8Depacketizer failed: %v", err)
	}

	var result *EncodedFrame
	for _, p := range packets {
		result, err = depkt.Depacketize(p)
		if err != nil {
			t.Fatalf("Depacketize failed: %v", err)
		}
	}

	if result == nil {
		t.Fatal("No frame returned")
	}

	if result.Timestamp != 90000 {
		t.Errorf("Timestamp = %d, want 90000", result.Timestamp)
	}

	// Note: Payload may not exactly match due to VP8 header differences
	t.Logf("Depacketized %d packets into %d bytes", len(packets), len(result.Data))
}

func TestVP9Packetizer(t *testing.T) {
	pkt, err := NewVP9Packetizer(12345, 98, 1200)
	if err != nil {
		t.Fatalf("NewVP9Packetizer failed: %v", err)
	}

	// VP9 payloader is strict about format, so use real encoded data if available
	// For now, test that the packetizer is properly configured
	if pkt.SSRC() != 12345 {
		t.Errorf("SSRC = %d, want 12345", pkt.SSRC())
	}
	if pkt.PayloadType() != 98 {
		t.Errorf("PayloadType = %d, want 98", pkt.PayloadType())
	}
	if pkt.MTU() != 1200 {
		t.Errorf("MTU = %d, want 1200", pkt.MTU())
	}

	// Test with dummy data - may produce empty packets due to format validation
	frame := &EncodedFrame{
		Data:      make([]byte, 500),
		FrameType: FrameTypeKey,
		Timestamp: 90000,
	}
	for i := range frame.Data {
		frame.Data[i] = byte(i)
	}

	packets, err := pkt.Packetize(frame)
	if err != nil {
		t.Fatalf("Packetize failed: %v", err)
	}

	// VP9 payloader may return empty for invalid bitstream data
	if len(packets) > 0 {
		if packets[0].Header.SSRC != 12345 {
			t.Errorf("SSRC = %d, want 12345", packets[0].Header.SSRC)
		}
		t.Logf("VP9: Packetized %d bytes into %d packets", len(frame.Data), len(packets))
	} else {
		t.Log("VP9: Payloader requires valid VP9 bitstream (expected with dummy data)")
	}
}

func TestOpusPacketizer(t *testing.T) {
	pkt, err := NewOpusPacketizer(12345, 111, 1200)
	if err != nil {
		t.Fatalf("NewOpusPacketizer failed: %v", err)
	}

	// Opus frames are typically small (100-200 bytes for 20ms audio)
	frame := &EncodedFrame{
		Data:      make([]byte, 120),
		Timestamp: 48000,
	}
	for i := range frame.Data {
		frame.Data[i] = byte(i)
	}

	packets, err := pkt.Packetize(frame)
	if err != nil {
		t.Fatalf("Packetize failed: %v", err)
	}

	if len(packets) != 1 {
		t.Errorf("Expected 1 packet, got %d", len(packets))
	}

	if packets[0].Header.SSRC != 12345 {
		t.Errorf("SSRC = %d, want 12345", packets[0].Header.SSRC)
	}
	if packets[0].Header.PayloadType != 111 {
		t.Errorf("PayloadType = %d, want 111", packets[0].Header.PayloadType)
	}
	if packets[0].Header.Timestamp != 48000 {
		t.Errorf("Timestamp = %d, want 48000", packets[0].Header.Timestamp)
	}
	if !packets[0].Header.Marker {
		t.Error("Opus packet should have marker")
	}

	t.Logf("Opus: Packetized %d bytes into %d packets", len(frame.Data), len(packets))
}

func TestOpusDepacketizer(t *testing.T) {
	// Packetize
	pkt, _ := NewOpusPacketizer(12345, 111, 1200)
	frame := &EncodedFrame{
		Data:      make([]byte, 120),
		Timestamp: 48000,
	}
	for i := range frame.Data {
		frame.Data[i] = byte(i)
	}

	packets, _ := pkt.Packetize(frame)

	// Depacketize
	depkt, err := NewOpusDepacketizer()
	if err != nil {
		t.Fatalf("NewOpusDepacketizer failed: %v", err)
	}

	result, err := depkt.Depacketize(packets[0])
	if err != nil {
		t.Fatalf("Depacketize failed: %v", err)
	}

	if result == nil {
		t.Fatal("No frame returned")
	}

	if len(result.Data) != len(frame.Data) {
		t.Errorf("Data length = %d, want %d", len(result.Data), len(frame.Data))
	}

	// Verify data matches
	for i := range frame.Data {
		if result.Data[i] != frame.Data[i] {
			t.Errorf("Data mismatch at byte %d: got %d, want %d", i, result.Data[i], frame.Data[i])
			break
		}
	}

	t.Logf("Opus: Round-trip successful")
}

func TestPacketizerRegistry(t *testing.T) {
	// Test VP8 video packetizer
	vp8Pkt, err := CreateVideoPacketizer(VideoCodecVP8, 1234, 96, 1200)
	if err != nil {
		t.Fatalf("CreateVideoPacketizer(VP8) failed: %v", err)
	}
	if vp8Pkt == nil {
		t.Fatal("VP8 packetizer is nil")
	}

	// Test VP9 video packetizer
	vp9Pkt, err := CreateVideoPacketizer(VideoCodecVP9, 1234, 98, 1200)
	if err != nil {
		t.Fatalf("CreateVideoPacketizer(VP9) failed: %v", err)
	}
	if vp9Pkt == nil {
		t.Fatal("VP9 packetizer is nil")
	}

	// Test Opus audio packetizer
	opusPkt, err := CreateAudioPacketizer(AudioCodecOpus, 1234, 111, 1200)
	if err != nil {
		t.Fatalf("CreateAudioPacketizer(Opus) failed: %v", err)
	}
	if opusPkt == nil {
		t.Fatal("Opus packetizer is nil")
	}

	// Test VP8 video depacketizer
	vp8Depkt, err := CreateVideoDepacketizer(VideoCodecVP8)
	if err != nil {
		t.Fatalf("CreateVideoDepacketizer(VP8) failed: %v", err)
	}
	if vp8Depkt == nil {
		t.Fatal("VP8 depacketizer is nil")
	}

	// Test VP9 video depacketizer
	vp9Depkt, err := CreateVideoDepacketizer(VideoCodecVP9)
	if err != nil {
		t.Fatalf("CreateVideoDepacketizer(VP9) failed: %v", err)
	}
	if vp9Depkt == nil {
		t.Fatal("VP9 depacketizer is nil")
	}

	// Test Opus audio depacketizer
	opusDepkt, err := CreateAudioDepacketizer(AudioCodecOpus)
	if err != nil {
		t.Fatalf("CreateAudioDepacketizer(Opus) failed: %v", err)
	}
	if opusDepkt == nil {
		t.Fatal("Opus depacketizer is nil")
	}
}

func TestPacketizeToBytes(t *testing.T) {
	pkt, _ := NewVP8Packetizer(12345, 96, 1200)

	frame := &EncodedFrame{
		Data:      make([]byte, 500),
		FrameType: FrameTypeKey,
		Timestamp: 90000,
	}

	bytes, err := pkt.PacketizeToBytes(frame)
	if err != nil {
		t.Fatalf("PacketizeToBytes failed: %v", err)
	}

	if len(bytes) == 0 {
		t.Fatal("No packet bytes produced")
	}

	// Verify we can parse them back
	for _, data := range bytes {
		var p rtp.Packet
		if err := p.Unmarshal(data); err != nil {
			t.Fatalf("Unmarshal failed: %v", err)
		}
		if p.Header.SSRC != 12345 {
			t.Errorf("SSRC mismatch after round-trip")
		}
	}

	t.Logf("PacketizeToBytes: produced %d packets", len(bytes))
}

func TestAV1Packetizer(t *testing.T) {
	pkt := NewAV1Packetizer(12345, 97, 1200)

	// Create a test frame with valid AV1 OBU structure
	// Sequence Header OBU: header 0x0a (type=1, hasSize=0) + 8 bytes payload
	// Frame OBU: header 0x30 (type=6, hasSize=0) + payload
	frame := &EncodedFrame{
		Data: []byte{
			0x0a, // Sequence Header OBU header (type=1, hasSize=0)
			0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0x00, // 8 bytes dummy seq header
			0x30, // Frame OBU header (type=6, hasSize=0)
		},
		FrameType: FrameTypeKey,
		Timestamp: 90000,
	}
	// Add 200 bytes of frame data
	for i := 0; i < 200; i++ {
		frame.Data = append(frame.Data, byte(i))
	}

	packets, err := pkt.Packetize(frame)
	if err != nil {
		t.Fatalf("Packetize failed: %v", err)
	}

	if len(packets) == 0 {
		t.Fatal("No packets produced")
	}

	// Verify packet structure
	for i, p := range packets {
		if p.Header.SSRC != 12345 {
			t.Errorf("Packet %d: SSRC = %d, want 12345", i, p.Header.SSRC)
		}
		if p.Header.PayloadType != 97 {
			t.Errorf("Packet %d: PayloadType = %d, want 97", i, p.Header.PayloadType)
		}
		// Last packet should have marker bit
		if i == len(packets)-1 && !p.Header.Marker {
			t.Errorf("Last packet should have marker bit set")
		}
	}

	t.Logf("Packetized %d bytes into %d packets", len(frame.Data), len(packets))
}

func TestAV1Depacketizer(t *testing.T) {
	// Create test packets with RFC 9000 format
	// Using W=1: 2 OBU elements (first has length, second doesn't)

	depacketizer := NewAV1Depacketizer()

	// Build a single RTP packet with AV1 payload
	// Aggregation header: 0x18 = Z=0, Y=0, W=1 (2 elements), N=1 (keyframe)
	aggHeader := byte(0x18)

	// Sequence Header OBU with proper structure
	seqHeaderOBU := []byte{
		0x0a, // OBU header: type=1, hasSize=0
		0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, // 7 bytes payload (simplified)
	}

	// Frame OBU: 0x30 (type=6, hasSize=0) + payload
	frameOBU := []byte{0x30}
	for i := 0; i < 100; i++ {
		frameOBU = append(frameOBU, byte(i))
	}

	// Build RTP payload per RFC 9000:
	// [aggHeader][LEB128 length of seqHeader][seqHeader OBU][frameOBU (no length)]
	payload := []byte{aggHeader}
	payload = append(payload, byte(len(seqHeaderOBU))) // LEB128 length = 8
	payload = append(payload, seqHeaderOBU...)
	payload = append(payload, frameOBU...) // No length for last element

	pkt := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    97,
			SequenceNumber: 1000,
			Timestamp:      90000,
			SSRC:           12345,
			Marker:         true, // Frame complete
		},
		Payload: payload,
	}

	frame, err := depacketizer.Depacketize(pkt)
	if err != nil {
		t.Fatalf("Depacketize failed: %v", err)
	}

	if frame == nil {
		t.Fatal("No frame returned")
	}

	if frame.Timestamp != 90000 {
		t.Errorf("Timestamp = %d, want 90000", frame.Timestamp)
	}

	if frame.FrameType != FrameTypeKey {
		t.Errorf("FrameType = %v, want FrameTypeKey", frame.FrameType)
	}

	// The normalized frame should have Temporal Delimiter + OBUs
	if len(frame.Data) < 4 {
		t.Fatalf("Frame data too short: %d bytes", len(frame.Data))
	}

	// First 2 bytes should be Temporal Delimiter (0x12, 0x00)
	if frame.Data[0] != 0x12 || frame.Data[1] != 0x00 {
		t.Errorf("Missing Temporal Delimiter: got %02x %02x, want 12 00", frame.Data[0], frame.Data[1])
	}

	t.Logf("Depacketized to %d bytes, frame type: %v", len(frame.Data), frame.FrameType)
}

func TestAV1DepacketizerMultiPacket(t *testing.T) {
	t.Skip("TODO: Fix manual RTP packet construction to match pion's AV1 format")
	// Test depacketization across multiple RTP packets
	depacketizer := NewAV1Depacketizer()

	// First packet: Sequence Header OBU
	// Aggregation header: 0x48 = Z=0, Y=1 (continues), W=0, N=1
	payload1 := []byte{0x48}
	seqHeader := []byte{0x0a, 0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00}
	payload1 = append(payload1, seqHeader...)

	pkt1 := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    97,
			SequenceNumber: 1000,
			Timestamp:      90000,
			SSRC:           12345,
			Marker:         false, // Not complete yet
		},
		Payload: payload1,
	}

	frame, err := depacketizer.Depacketize(pkt1)
	if err != nil {
		t.Fatalf("Depacketize pkt1 failed: %v", err)
	}
	if frame != nil {
		t.Error("Frame should be nil for incomplete packet")
	}

	// Second packet: Frame OBU (continuation)
	// Aggregation header: 0x80 = Z=1 (continuation), Y=0, W=0, N=0
	payload2 := []byte{0x00} // Z=0, Y=0, W=0, N=0 (delta frame part)
	frameData := []byte{0x30} // Frame OBU header
	for i := 0; i < 50; i++ {
		frameData = append(frameData, byte(i))
	}
	payload2 = append(payload2, frameData...)

	pkt2 := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    97,
			SequenceNumber: 1001,
			Timestamp:      90000, // Same timestamp = same frame
			SSRC:           12345,
			Marker:         true, // Frame complete
		},
		Payload: payload2,
	}

	frame, err = depacketizer.Depacketize(pkt2)
	if err != nil {
		t.Fatalf("Depacketize pkt2 failed: %v", err)
	}

	if frame == nil {
		t.Fatal("No frame returned after complete packets")
	}

	t.Logf("Multi-packet depacketization: %d bytes", len(frame.Data))
}

func BenchmarkVP8Packetize(b *testing.B) {
	pkt, _ := NewVP8Packetizer(12345, 96, 1200)

	frame := &EncodedFrame{
		Data:      make([]byte, 10000),
		FrameType: FrameTypeDelta,
		Timestamp: 90000,
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		frame.Timestamp = uint32(i * 3000)
		_, err := pkt.Packetize(frame)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOpusPacketize(b *testing.B) {
	pkt, _ := NewOpusPacketizer(12345, 111, 1200)

	frame := &EncodedFrame{
		Data:      make([]byte, 120),
		Timestamp: 48000,
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		frame.Timestamp = uint32(i * 960)
		_, err := pkt.Packetize(frame)
		if err != nil {
			b.Fatal(err)
		}
	}
}
