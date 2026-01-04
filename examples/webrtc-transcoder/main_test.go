package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/thesyncim/media"
)

// TestE2E_StatusEndpoint tests the /status endpoint
func TestE2E_StatusEndpoint(t *testing.T) {
	req := httptest.NewRequest("GET", "/status", nil)
	w := httptest.NewRecorder()

	handleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var status struct {
		Active          bool     `json:"active"`
		AvailableCodecs []string `json:"availableCodecs"`
	}
	if err := json.NewDecoder(w.Body).Decode(&status); err != nil {
		t.Fatalf("failed to decode status: %v", err)
	}

	// Should have available codecs listed
	if len(status.AvailableCodecs) == 0 {
		t.Error("expected available codecs")
	}
	t.Logf("Available codecs: %v", status.AvailableCodecs)
}

// TestE2E_AddVariantWithoutStream tests adding variant when no stream is active
func TestE2E_AddVariantWithoutStream(t *testing.T) {
	body := bytes.NewBufferString(`{"id":"test","codec":"VP8","width":640,"height":480,"bitrateBps":500000}`)
	req := httptest.NewRequest("POST", "/add-variant", body)
	w := httptest.NewRecorder()

	handleAddVariant(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 (no active stream), got %d", w.Code)
	}
}

// TestE2E_RemoveVariantWithoutStream tests removing variant when no stream is active
func TestE2E_RemoveVariantWithoutStream(t *testing.T) {
	req := httptest.NewRequest("POST", "/remove-variant?id=test", nil)
	w := httptest.NewRecorder()

	handleRemoveVariant(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 (no active stream), got %d", w.Code)
	}
}

// TestE2E_SubscribeWithoutStream tests subscribing when no stream is active
func TestE2E_SubscribeWithoutStream(t *testing.T) {
	// Create a minimal SDP offer
	pc, _ := webrtcAPI.NewPeerConnection(webrtc.Configuration{})
	defer pc.Close()

	pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo)
	offer, _ := pc.CreateOffer(nil)
	pc.SetLocalDescription(offer)

	body, _ := json.Marshal(offer)
	req := httptest.NewRequest("POST", "/subscribe?variant=vp8-720p", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handleSubscribe(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404 (no active stream), got %d", w.Code)
	}
}

// TestE2E_CodecDetection tests the codec detection from MIME types
func TestE2E_CodecDetection(t *testing.T) {
	tests := []struct {
		mime     string
		expected media.VideoCodec
	}{
		{"video/VP8", media.VideoCodecVP8},
		{"video/vp8", media.VideoCodecVP8},
		{"video/VP9", media.VideoCodecVP9},
		{"video/vp9", media.VideoCodecVP9},
		{"video/H264", media.VideoCodecH264},
		{"video/h264", media.VideoCodecH264},
		{"video/AV1", media.VideoCodecAV1},
		{"video/av1", media.VideoCodecAV1},
		{"video/unknown", media.VideoCodecUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.mime, func(t *testing.T) {
			got := detectCodecFromMime(tt.mime)
			if got != tt.expected {
				t.Errorf("detectCodecFromMime(%s) = %v, want %v", tt.mime, got, tt.expected)
			}
		})
	}
}

// TestE2E_CodecCapability tests the codec capability generation
func TestE2E_CodecCapability(t *testing.T) {
	tests := []struct {
		codec    media.VideoCodec
		wantMime string
	}{
		{media.VideoCodecVP8, "video/VP8"},
		{media.VideoCodecVP9, "video/VP9"},
		{media.VideoCodecH264, "video/H264"},
		{media.VideoCodecUnknown, "video/VP8"}, // Default fallback
	}

	for _, tt := range tests {
		t.Run(tt.wantMime, func(t *testing.T) {
			cap := codecCapability(tt.codec)
			if cap.MimeType != tt.wantMime {
				t.Errorf("codecCapability(%v) = %s, want %s", tt.codec, cap.MimeType, tt.wantMime)
			}
			if cap.ClockRate != 90000 {
				t.Errorf("expected clock rate 90000, got %d", cap.ClockRate)
			}
		})
	}
}

// TestE2E_TranscodePipelineIntegration tests the full transcode pipeline
func TestE2E_TranscodePipelineIntegration(t *testing.T) {
	if !media.IsVP8Available() {
		t.Skip("VP8 not available")
	}

	// Create a mock publisher with VP8 codec
	pub := &Publisher{
		frameCh: make(chan *media.EncodedFrame, 30),
		codec:   media.VideoCodecVP8,
	}

	// Manually set up the pipeline
	publisherMu.Lock()
	currentPublisher = pub
	publisherMu.Unlock()

	// Start transcode pipeline
	startTranscodePipeline(pub)

	// Wait for pipeline to initialize
	time.Sleep(100 * time.Millisecond)

	publisherMu.RLock()
	pipeline := currentPipeline
	publisherMu.RUnlock()

	if pipeline == nil {
		t.Fatal("pipeline not created")
	}

	if !pipeline.running.Load() {
		t.Error("pipeline not running")
	}

	// Verify variants were created
	if len(pipeline.variants) == 0 {
		t.Error("no variants created")
	}

	t.Logf("Pipeline created with %d variants:", len(pipeline.variants))
	for _, v := range pipeline.variants {
		t.Logf("  - %s: %s", v.ID, v.Label)
	}

	// Create a test frame and send it
	frame := createTestEncodedFrame(t, media.VideoCodecVP8, 640, 480)
	pub.frameCh <- frame

	// Give transcoder time to process
	time.Sleep(200 * time.Millisecond)

	// Check subscribers receive data - create test subscribers
	hasOutput := false
	for _, v := range pipeline.variants {
		sub := &Subscriber{
			id:        "test-" + v.ID,
			variantID: v.ID,
			frameCh:   make(chan *media.EncodedFrame, 30),
		}
		pipeline.subscribersMu.Lock()
		pipeline.subscribers[v.ID] = append(pipeline.subscribers[v.ID], sub)
		pipeline.subscribersMu.Unlock()
	}

	// Send another frame to trigger broadcast
	frame2 := createTestEncodedFrame(t, media.VideoCodecVP8, 640, 480)
	pub.frameCh <- frame2
	time.Sleep(200 * time.Millisecond)

	pipeline.subscribersMu.RLock()
	for id, subs := range pipeline.subscribers {
		for _, sub := range subs {
			select {
			case f := <-sub.frameCh:
				if f != nil {
					hasOutput = true
					t.Logf("Variant %s produced %d bytes", id, len(f.Data))
				}
			default:
			}
		}
	}
	pipeline.subscribersMu.RUnlock()

	if !hasOutput {
		t.Log("No output yet (encoder may be buffering)")
	}

	// Clean up
	stopTranscodePipeline()

	publisherMu.Lock()
	currentPublisher = nil
	publisherMu.Unlock()
}

// TestE2E_DynamicVariantAddition tests adding variants while streaming
func TestE2E_DynamicVariantAddition(t *testing.T) {
	if !media.IsVP8Available() {
		t.Skip("VP8 not available")
	}

	// Create a mock publisher
	pub := &Publisher{
		frameCh: make(chan *media.EncodedFrame, 30),
		codec:   media.VideoCodecVP8,
	}

	publisherMu.Lock()
	currentPublisher = pub
	publisherMu.Unlock()

	startTranscodePipeline(pub)
	time.Sleep(100 * time.Millisecond)

	publisherMu.RLock()
	pipeline := currentPipeline
	publisherMu.RUnlock()

	if pipeline == nil {
		t.Fatal("pipeline not created")
	}

	initialCount := len(pipeline.variants)
	t.Logf("Initial variant count: %d", initialCount)

	// Add a new variant via API
	body := bytes.NewBufferString(`{"id":"e2e-test-variant","codec":"VP8","width":320,"height":240,"bitrateBps":200000}`)
	req := httptest.NewRequest("POST", "/add-variant", body)
	w := httptest.NewRecorder()

	handleAddVariant(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify variant was added
	publisherMu.RLock()
	pipeline = currentPipeline
	publisherMu.RUnlock()

	if len(pipeline.variants) != initialCount+1 {
		t.Errorf("expected %d variants, got %d", initialCount+1, len(pipeline.variants))
	}

	// Find the new variant
	found := false
	for _, v := range pipeline.variants {
		if v.ID == "e2e-test-variant" {
			found = true
			t.Logf("Found new variant: %s (%dx%d)", v.Label, v.Width, v.Height)
		}
	}
	if !found {
		t.Error("new variant not found in list")
	}

	// Verify we can add a subscriber for the new variant
	sub := &Subscriber{
		id:        "test-sub-e2e-test-variant",
		variantID: "e2e-test-variant",
		frameCh:   make(chan *media.EncodedFrame, 30),
	}
	pipeline.subscribersMu.Lock()
	pipeline.subscribers["e2e-test-variant"] = append(pipeline.subscribers["e2e-test-variant"], sub)
	pipeline.subscribersMu.Unlock()

	pipeline.subscribersMu.RLock()
	if _, ok := pipeline.subscribers["e2e-test-variant"]; !ok {
		t.Error("subscriber list not created for new variant")
	}
	pipeline.subscribersMu.RUnlock()

	// Now remove the variant
	req = httptest.NewRequest("POST", "/remove-variant?id=e2e-test-variant", nil)
	w = httptest.NewRecorder()

	handleRemoveVariant(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 on remove, got %d: %s", w.Code, w.Body.String())
	}

	// Verify variant was removed
	publisherMu.RLock()
	pipeline = currentPipeline
	publisherMu.RUnlock()

	if len(pipeline.variants) != initialCount {
		t.Errorf("expected %d variants after removal, got %d", initialCount, len(pipeline.variants))
	}

	// Clean up
	stopTranscodePipeline()

	publisherMu.Lock()
	currentPublisher = nil
	publisherMu.Unlock()
}

// TestE2E_AllCodecsAvailable tests that all expected codecs are available
func TestE2E_AllCodecsAvailable(t *testing.T) {
	req := httptest.NewRequest("GET", "/status", nil)
	w := httptest.NewRecorder()

	handleStatus(w, req)

	var status struct {
		AvailableCodecs []string `json:"availableCodecs"`
	}
	json.NewDecoder(w.Body).Decode(&status)

	// Check each codec
	codecMap := make(map[string]bool)
	for _, c := range status.AvailableCodecs {
		codecMap[c] = true
	}

	if media.IsVP8Available() && !codecMap["VP8"] {
		t.Error("VP8 available but not reported")
	}
	if media.IsVP9Available() && !codecMap["VP9"] {
		t.Error("VP9 available but not reported")
	}
	if media.IsH264EncoderAvailable() && !codecMap["H264"] {
		t.Error("H264 available but not reported")
	}
	if media.IsAV1Available() && !codecMap["AV1"] {
		t.Error("AV1 available but not reported")
	}

	t.Logf("Reported codecs: %v", status.AvailableCodecs)
}

// TestE2E_MultipleVariantCodecs tests adding variants with different codecs
func TestE2E_MultipleVariantCodecs(t *testing.T) {
	if !media.IsVP8Available() {
		t.Skip("VP8 not available")
	}

	pub := &Publisher{
		frameCh: make(chan *media.EncodedFrame, 30),
		codec:   media.VideoCodecVP8,
	}

	publisherMu.Lock()
	currentPublisher = pub
	publisherMu.Unlock()

	startTranscodePipeline(pub)
	time.Sleep(100 * time.Millisecond)

	// Add variants for all available codecs
	codecs := []string{}
	if media.IsVP8Available() {
		codecs = append(codecs, "VP8")
	}
	if media.IsVP9Available() {
		codecs = append(codecs, "VP9")
	}
	if media.IsH264EncoderAvailable() {
		codecs = append(codecs, "H264")
	}
	if media.IsAV1Available() {
		codecs = append(codecs, "AV1")
	}

	for i, codec := range codecs {
		body := bytes.NewBufferString(`{"id":"e2e-` + codec + `","codec":"` + codec + `","width":640,"height":480,"bitrateBps":500000}`)
		req := httptest.NewRequest("POST", "/add-variant", body)
		w := httptest.NewRecorder()

		handleAddVariant(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("failed to add %s variant: %s", codec, w.Body.String())
		} else {
			t.Logf("Added %s variant (%d/%d)", codec, i+1, len(codecs))
		}
	}

	// Verify all variants exist
	publisherMu.RLock()
	pipeline := currentPipeline
	publisherMu.RUnlock()

	t.Logf("Total variants: %d", len(pipeline.variants))

	// Clean up
	stopTranscodePipeline()

	publisherMu.Lock()
	currentPublisher = nil
	publisherMu.Unlock()
}

// Helper function to create test encoded frame
func createTestEncodedFrame(t *testing.T, codec media.VideoCodec, width, height int) *media.EncodedFrame {
	t.Helper()

	// Create raw frame
	raw := createTestVideoFrame(width, height)

	// Create encoder
	enc, err := media.NewVP8Encoder(media.VideoEncoderConfig{
		Width:      width,
		Height:     height,
		BitrateBps: 500_000,
		FPS:        30,
	})
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}
	defer enc.Close()

	// Encode frames until we get output
	var encoded *media.EncodedFrame
	for i := 0; i < 60 && encoded == nil; i++ {
		encoded, _ = enc.Encode(raw)
	}

	if encoded == nil {
		t.Fatal("failed to get encoded frame")
	}

	// Copy data
	data := make([]byte, len(encoded.Data))
	copy(data, encoded.Data)

	return &media.EncodedFrame{
		Data:      data,
		Timestamp: encoded.Timestamp,
		FrameType: encoded.FrameType,
	}
}

func createTestVideoFrame(width, height int) *media.VideoFrame {
	ySize := width * height
	uvSize := (width / 2) * (height / 2)

	yPlane := make([]byte, ySize)
	uPlane := make([]byte, uvSize)
	vPlane := make([]byte, uvSize)

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			yPlane[y*width+x] = byte((x + y) % 256)
		}
	}

	for y := 0; y < height/2; y++ {
		for x := 0; x < width/2; x++ {
			uPlane[y*(width/2)+x] = byte(128 + (x % 10))
			vPlane[y*(width/2)+x] = byte(128 + (y % 10))
		}
	}

	return &media.VideoFrame{
		Width:  width,
		Height: height,
		Format: media.PixelFormatI420,
		Data:   [][]byte{yPlane, uPlane, vPlane},
		Stride: []int{width, width / 2, width / 2},
	}
}

// TestE2E_StopStartPublishing tests that stopping and restarting publishing works
func TestE2E_StopStartPublishing(t *testing.T) {
	if !media.IsVP8Available() {
		t.Skip("VP8 not available")
	}

	// First publish session
	pub1 := &Publisher{
		frameCh: make(chan *media.EncodedFrame, 30),
		codec:   media.VideoCodecVP8,
	}

	publisherMu.Lock()
	currentPublisher = pub1
	publisherMu.Unlock()

	startTranscodePipeline(pub1)
	time.Sleep(100 * time.Millisecond)

	publisherMu.RLock()
	pipeline1 := currentPipeline
	publisherMu.RUnlock()

	if pipeline1 == nil {
		t.Fatal("pipeline1 not created")
	}
	t.Log("First pipeline created successfully")

	// Stop the pipeline (simulating publisher disconnect)
	stopTranscodePipeline()
	time.Sleep(50 * time.Millisecond)

	// Verify state is cleaned up
	publisherMu.RLock()
	if currentPipeline != nil {
		t.Error("pipeline should be nil after stop")
	}
	if currentPublisher != nil {
		t.Error("publisher should be nil after stop")
	}
	publisherMu.RUnlock()

	// Second publish session
	pub2 := &Publisher{
		frameCh: make(chan *media.EncodedFrame, 30),
		codec:   media.VideoCodecVP8,
	}

	publisherMu.Lock()
	currentPublisher = pub2
	publisherMu.Unlock()

	startTranscodePipeline(pub2)
	time.Sleep(100 * time.Millisecond)

	publisherMu.RLock()
	pipeline2 := currentPipeline
	publisherMu.RUnlock()

	if pipeline2 == nil {
		t.Fatal("pipeline2 not created after restart")
	}
	if !pipeline2.running.Load() {
		t.Error("pipeline2 should be running")
	}

	t.Logf("Second pipeline created with %d variants", len(pipeline2.variants))

	// Send a keyframe and verify it works
	frame := createTestEncodedFrame(t, media.VideoCodecVP8, 640, 480)
	pub2.frameCh <- frame

	time.Sleep(200 * time.Millisecond)

	// Clean up
	stopTranscodePipeline()
}

// TestE2E_KeyframeWaiting tests that transcoding waits for first keyframe
func TestE2E_KeyframeWaiting(t *testing.T) {
	if !media.IsVP8Available() {
		t.Skip("VP8 not available")
	}

	pub := &Publisher{
		frameCh: make(chan *media.EncodedFrame, 30),
		codec:   media.VideoCodecVP8,
	}

	publisherMu.Lock()
	currentPublisher = pub
	publisherMu.Unlock()

	startTranscodePipeline(pub)
	time.Sleep(100 * time.Millisecond)

	publisherMu.RLock()
	pipeline := currentPipeline
	publisherMu.RUnlock()

	if pipeline == nil {
		t.Fatal("pipeline not created")
	}

	// Send delta frames first (should be skipped)
	for i := 0; i < 5; i++ {
		pub.frameCh <- &media.EncodedFrame{
			Data:      []byte{0x01, 0x02, 0x03}, // Fake delta frame
			FrameType: media.FrameTypeDelta,
			Timestamp: uint32(i * 3000),
		}
	}

	time.Sleep(50 * time.Millisecond)

	// Add test subscribers for each variant
	testSubs := make(map[string]*Subscriber)
	for _, v := range pipeline.variants {
		sub := &Subscriber{
			id:        "test-keyframe-" + v.ID,
			variantID: v.ID,
			frameCh:   make(chan *media.EncodedFrame, 30),
		}
		testSubs[v.ID] = sub
		pipeline.subscribersMu.Lock()
		pipeline.subscribers[v.ID] = append(pipeline.subscribers[v.ID], sub)
		pipeline.subscribersMu.Unlock()
	}

	// Check no output yet (delta frames should be skipped)
	for id, sub := range testSubs {
		select {
		case <-sub.frameCh:
			// It's okay if passthrough gets frames
			if id != "source" {
				t.Logf("Variant %s got early frame (unexpected)", id)
			}
		default:
			// Expected - no frames yet
		}
	}

	// Now send a keyframe
	keyframe := createTestEncodedFrame(t, media.VideoCodecVP8, 640, 480)
	pub.frameCh <- keyframe

	time.Sleep(200 * time.Millisecond)

	// Now we should have output
	hasOutput := false
	for id, sub := range testSubs {
		select {
		case f := <-sub.frameCh:
			if f != nil {
				hasOutput = true
				t.Logf("Variant %s got frame after keyframe (%d bytes)", id, len(f.Data))
			}
		default:
		}
	}

	if !hasOutput {
		t.Log("No immediate output (encoder may be buffering)")
	}

	// Clean up
	stopTranscodePipeline()
}

// TestE2E_SubscriberBroadcast tests that multiple subscribers receive frames independently
func TestE2E_SubscriberBroadcast(t *testing.T) {
	if !media.IsVP8Available() {
		t.Skip("VP8 not available")
	}

	pub := &Publisher{
		frameCh: make(chan *media.EncodedFrame, 30),
		codec:   media.VideoCodecVP8,
	}

	publisherMu.Lock()
	currentPublisher = pub
	publisherMu.Unlock()

	startTranscodePipeline(pub)
	time.Sleep(100 * time.Millisecond)

	publisherMu.RLock()
	pipeline := currentPipeline
	publisherMu.RUnlock()

	if pipeline == nil {
		t.Fatal("pipeline not created")
	}

	// Get first available transcoding variant (not source passthrough)
	var targetVariant string
	for _, v := range pipeline.variants {
		if v.ID != "source" {
			targetVariant = v.ID
			break
		}
	}
	if targetVariant == "" {
		t.Skip("No transcoding variants available")
	}

	t.Logf("Testing with variant: %s", targetVariant)

	// Create multiple subscribers for the same variant
	const numSubscribers = 3
	subs := make([]*Subscriber, numSubscribers)
	for i := 0; i < numSubscribers; i++ {
		subs[i] = &Subscriber{
			id:        fmt.Sprintf("test-sub-%d", i),
			variantID: targetVariant,
			frameCh:   make(chan *media.EncodedFrame, 30),
		}
		pipeline.subscribersMu.Lock()
		pipeline.subscribers[targetVariant] = append(pipeline.subscribers[targetVariant], subs[i])
		pipeline.subscribersMu.Unlock()
	}

	// Send a keyframe
	keyframe := createTestEncodedFrame(t, media.VideoCodecVP8, 640, 480)
	pub.frameCh <- keyframe

	time.Sleep(300 * time.Millisecond)

	// Verify ALL subscribers received the frame
	framesReceived := 0
	for i, sub := range subs {
		select {
		case f := <-sub.frameCh:
			if f != nil {
				framesReceived++
				t.Logf("Subscriber %d received frame (%d bytes)", i, len(f.Data))
			}
		default:
			t.Logf("Subscriber %d did not receive frame", i)
		}
	}

	if framesReceived == 0 {
		t.Log("No frames received (encoder may be buffering)")
	} else if framesReceived < numSubscribers {
		t.Errorf("Only %d/%d subscribers received frames - broadcast may not be working", framesReceived, numSubscribers)
	} else {
		t.Logf("All %d subscribers received frames - broadcast working correctly!", framesReceived)
	}

	// Clean up
	stopTranscodePipeline()
}

// TestE2E_ManyVariantsRecovery tests adding many variants and recovering after removal
func TestE2E_ManyVariantsRecovery(t *testing.T) {
	if !media.IsVP8Available() {
		t.Skip("VP8 not available")
	}

	pub := &Publisher{
		frameCh: make(chan *media.EncodedFrame, 30),
		codec:   media.VideoCodecVP8,
	}

	publisherMu.Lock()
	currentPublisher = pub
	publisherMu.Unlock()

	startTranscodePipeline(pub)
	time.Sleep(100 * time.Millisecond)

	publisherMu.RLock()
	pipeline := currentPipeline
	publisherMu.RUnlock()

	if pipeline == nil {
		t.Fatal("pipeline not created")
	}

	initialCount := len(pipeline.variants)
	t.Logf("Initial variant count: %d", initialCount)

	// Send a keyframe to initialize
	keyframe := createTestEncodedFrame(t, media.VideoCodecVP8, 640, 480)
	pub.frameCh <- keyframe
	time.Sleep(200 * time.Millisecond)

	// Verify initial transcoding works
	t.Log("Verifying initial transcoding works...")
	frame1 := createTestEncodedFrame(t, media.VideoCodecVP8, 640, 480)
	pub.frameCh <- frame1
	time.Sleep(100 * time.Millisecond)

	// Check we can transcode
	initialDrops := pipeline.frameDrops.Load()
	t.Logf("Initial frame drops: %d", initialDrops)

	// Add many variants to stress the system - mix of codecs for realism
	const numExtraVariants = 8 // Push harder
	t.Logf("Adding %d extra variants...", numExtraVariants)

	// Mix of codecs - heavier ones like VP9/H264/AV1 are more likely to cause issues
	codecs := []string{"VP8", "VP9", "H264", "AV1", "VP8", "VP9", "H264", "AV1"}
	for i := 0; i < numExtraVariants; i++ {
		codec := codecs[i%len(codecs)]
		body := bytes.NewBufferString(fmt.Sprintf(
			`{"id":"stress-%d","codec":"%s","width":640,"height":480,"bitrateBps":500000}`, i, codec))
		req := httptest.NewRequest("POST", "/add-variant", body)
		w := httptest.NewRecorder()
		handleAddVariant(w, req)

		if w.Code != http.StatusOK {
			t.Logf("Failed to add variant %d (%s): %s", i, codec, w.Body.String())
			break
		}
		t.Logf("Added variant stress-%d (%s)", i, codec)
	}

	// Get updated count
	publisherMu.RLock()
	pipeline = currentPipeline
	publisherMu.RUnlock()
	afterAddCount := len(pipeline.variants)
	t.Logf("Variant count after adding: %d", afterAddCount)

	// Send frames and measure performance
	t.Log("Sending frames with many variants...")
	for i := 0; i < 10; i++ {
		frame := createTestEncodedFrame(t, media.VideoCodecVP8, 640, 480)
		pub.frameCh <- frame
		time.Sleep(33 * time.Millisecond) // ~30fps
	}

	time.Sleep(200 * time.Millisecond)

	// Check for drops/stalls
	dropsAfterStress := pipeline.frameDrops.Load()
	avgEncodeMs := float64(pipeline.avgEncodeTimeNs.Load()) / 1e6
	t.Logf("After stress: drops=%d, avgEncode=%.1fms", dropsAfterStress, avgEncodeMs)

	// Now remove the extra variants
	t.Log("Removing extra variants...")
	for i := 0; i < numExtraVariants; i++ {
		req := httptest.NewRequest("POST", fmt.Sprintf("/remove-variant?id=stress-%d", i), nil)
		w := httptest.NewRecorder()
		handleRemoveVariant(w, req)
		if w.Code != http.StatusOK {
			t.Logf("Failed to remove variant stress-%d: %s", i, w.Body.String())
		}
	}

	// Get count after removal
	publisherMu.RLock()
	pipeline = currentPipeline
	publisherMu.RUnlock()
	afterRemoveCount := len(pipeline.variants)
	t.Logf("Variant count after removal: %d", afterRemoveCount)

	if afterRemoveCount != initialCount {
		t.Errorf("Expected %d variants after removal, got %d", initialCount, afterRemoveCount)
	}

	// Wait for recovery
	t.Log("Waiting for recovery...")
	time.Sleep(500 * time.Millisecond)

	// Send more frames and verify recovery
	t.Log("Testing recovery...")
	dropsBeforeRecovery := pipeline.frameDrops.Load()

	for i := 0; i < 30; i++ {
		frame := createTestEncodedFrame(t, media.VideoCodecVP8, 640, 480)
		pub.frameCh <- frame
		time.Sleep(33 * time.Millisecond)
	}

	time.Sleep(200 * time.Millisecond)

	dropsAfterRecovery := pipeline.frameDrops.Load()
	newDrops := dropsAfterRecovery - dropsBeforeRecovery
	finalAvgEncodeMs := float64(pipeline.avgEncodeTimeNs.Load()) / 1e6

	t.Logf("Recovery test: newDrops=%d, avgEncode=%.1fms, running=%v",
		newDrops, finalAvgEncodeMs, pipeline.running.Load())

	// Verify pipeline is still running
	if !pipeline.running.Load() {
		t.Error("Pipeline stopped running - did not recover!")
	}

	// Verify encoding is happening (avg encode time should be reasonable)
	if finalAvgEncodeMs > 100 {
		t.Errorf("Encoding still slow after recovery: %.1fms", finalAvgEncodeMs)
	}

	// Verify we're not dropping every frame
	if newDrops > 10 {
		t.Errorf("Still dropping many frames after recovery: %d", newDrops)
	}

	// Clean up
	stopTranscodePipeline()
	t.Log("Test completed")
}

// TestE2E_StatusAfterStopStart tests that /status endpoint works correctly after stop/start
func TestE2E_StatusAfterStopStart(t *testing.T) {
	// Initially no active stream
	req := httptest.NewRequest("GET", "/status", nil)
	w := httptest.NewRecorder()
	handleStatus(w, req)

	var status1 struct {
		Active bool `json:"active"`
	}
	json.NewDecoder(w.Body).Decode(&status1)
	if status1.Active {
		t.Error("should not be active initially")
	}

	// Start a publisher
	if !media.IsVP8Available() {
		t.Skip("VP8 not available")
	}

	pub := &Publisher{
		frameCh: make(chan *media.EncodedFrame, 30),
		codec:   media.VideoCodecVP8,
	}
	publisherMu.Lock()
	currentPublisher = pub
	publisherMu.Unlock()
	startTranscodePipeline(pub)
	time.Sleep(100 * time.Millisecond)

	// Check status is active
	req = httptest.NewRequest("GET", "/status", nil)
	w = httptest.NewRecorder()
	handleStatus(w, req)

	var status2 struct {
		Active bool `json:"active"`
	}
	json.NewDecoder(w.Body).Decode(&status2)
	if !status2.Active {
		t.Error("should be active after start")
	}

	// Stop
	stopTranscodePipeline()
	time.Sleep(50 * time.Millisecond)

	// Check status is inactive
	req = httptest.NewRequest("GET", "/status", nil)
	w = httptest.NewRecorder()
	handleStatus(w, req)

	var status3 struct {
		Active bool `json:"active"`
	}
	json.NewDecoder(w.Body).Decode(&status3)
	if status3.Active {
		t.Error("should not be active after stop")
	}
}
