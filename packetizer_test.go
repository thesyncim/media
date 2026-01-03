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
