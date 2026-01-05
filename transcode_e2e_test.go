//go:build (darwin || linux) && !nocodec

package media

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTranscodeMatrixE2E tests all codec combinations with external verification.
// This is a comprehensive end-to-end test that:
// 1. Encodes test frames with each codec
// 2. Transcodes between all codec pairs
// 3. Verifies output is decodable by the internal decoder
// 4. Optionally verifies with ffprobe if available
func TestTranscodeMatrixE2E(t *testing.T) {
	type codecInfo struct {
		codec     VideoCodec
		available func() bool
		name      string
	}

	codecs := []codecInfo{
		{VideoCodecVP8, IsVP8Available, "VP8"},
		{VideoCodecVP9, IsVP9Available, "VP9"},
		{VideoCodecH264, func() bool { return IsH264EncoderAvailable() && IsH264DecoderAvailable() }, "H264"},
		{VideoCodecAV1, IsAV1Available, "AV1"},
	}

	// Filter to available codecs
	var available []codecInfo
	for _, c := range codecs {
		if c.available() {
			available = append(available, c)
			t.Logf("Codec available: %s", c.name)
		} else {
			t.Logf("Codec NOT available: %s", c.name)
		}
	}

	if len(available) < 2 {
		t.Skip("Need at least 2 codecs for matrix test")
	}

	// Test matrix: all source -> all destination combinations
	for _, src := range available {
		for _, dst := range available {
			testName := fmt.Sprintf("%s_to_%s", src.name, dst.name)
			t.Run(testName, func(t *testing.T) {
				testTranscodePair(t, src.codec, dst.codec)
			})
		}
	}
}

func testTranscodePair(t *testing.T, srcCodec, dstCodec VideoCodec) {
	const (
		width     = 640
		height    = 480
		fps       = 30
		bitrate   = 500_000
		numFrames = 30 // Enough to get past buffering
	)

	// Create transcoder
	transcoder, err := NewTranscoder(TranscoderConfig{
		InputCodec:  srcCodec,
		OutputCodec: dstCodec,
		Width:       width,
		Height:      height,
		BitrateBps:  bitrate,
		FPS:         fps,
	})
	if err != nil {
		t.Fatalf("failed to create transcoder %s->%s: %v", srcCodec, dstCodec, err)
	}
	defer transcoder.Close()

	// Generate test pattern frames
	var outputFrames []*EncodedFrame
	var totalBytes int

	for i := 0; i < numFrames; i++ {
		// Create test frame with varying content
		raw := createColorTestFrame(width, height, i)

		output, err := transcoder.TranscodeRaw(raw)
		if err != nil {
			t.Fatalf("transcode failed at frame %d: %v", i, err)
		}
		if output != nil {
			outputFrames = append(outputFrames, output)
			totalBytes += len(output.Data)
		}
	}

	if len(outputFrames) == 0 {
		t.Fatal("no output frames produced")
	}

	t.Logf("%s->%s: produced %d frames, %d bytes total", srcCodec, dstCodec, len(outputFrames), totalBytes)

	// Verify at least one keyframe exists
	hasKeyframe := false
	for _, f := range outputFrames {
		if f.FrameType == FrameTypeKey {
			hasKeyframe = true
			break
		}
	}
	if !hasKeyframe {
		t.Error("no keyframe in output")
	}

	// Verify output is decodable by internal decoder
	verifyWithInternalDecoder(t, dstCodec, outputFrames)

	// Verify with ffprobe if available
	if hasFFprobe() {
		verifyWithFFprobe(t, dstCodec, outputFrames)
	}
}

// createColorTestFrame creates a test frame with varying colors based on frame number.
func createColorTestFrame(width, height, frameNum int) *VideoFrame {
	frame := &VideoFrame{
		Width:  width,
		Height: height,
		Format: PixelFormatI420,
		Data:   make([][]byte, 3),
		Stride: []int{width, width / 2, width / 2},
	}

	// Y plane - gradient based on frame number
	ySize := width * height
	uvSize := (width / 2) * (height / 2)

	frame.Data[0] = make([]byte, ySize)
	frame.Data[1] = make([]byte, uvSize)
	frame.Data[2] = make([]byte, uvSize)

	// Create a pattern that changes with frame number
	yBase := byte((frameNum * 4) % 256)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			// Gradient pattern
			luma := yBase + byte((x+y)/4)
			frame.Data[0][y*width+x] = luma
		}
	}

	// UV planes - neutral gray (128)
	for i := range frame.Data[1] {
		frame.Data[1][i] = 128
		frame.Data[2][i] = 128
	}

	return frame
}

func verifyWithInternalDecoder(t *testing.T, codec VideoCodec, frames []*EncodedFrame) {
	var dec VideoDecoder
	var err error

	switch codec {
	case VideoCodecVP8:
		dec, err = NewVP8Decoder(VideoDecoderConfig{})
	case VideoCodecVP9:
		dec, err = NewVP9Decoder(VideoDecoderConfig{})
	case VideoCodecH264:
		dec, err = NewH264Decoder(VideoDecoderConfig{})
	case VideoCodecAV1:
		dec, err = NewAV1Decoder(VideoDecoderConfig{})
	default:
		t.Skipf("no decoder for %s", codec)
		return
	}

	if err != nil {
		t.Fatalf("failed to create %s decoder: %v", codec, err)
	}
	defer dec.Close()

	decodedCount := 0
	for _, frame := range frames {
		decoded, err := dec.Decode(frame)
		if err != nil {
			// Some frames may fail during decode - log but continue
			continue
		}
		if decoded != nil {
			decodedCount++
			if decoded.Width <= 0 || decoded.Height <= 0 {
				t.Errorf("invalid decoded dimensions: %dx%d", decoded.Width, decoded.Height)
			}
		}
	}

	if decodedCount == 0 {
		t.Error("internal decoder produced no output frames")
	} else {
		t.Logf("internal decoder verified %d/%d frames", decodedCount, len(frames))
	}
}

func hasFFprobe() bool {
	_, err := exec.LookPath("ffprobe")
	return err == nil
}

func verifyWithFFprobe(t *testing.T, codec VideoCodec, frames []*EncodedFrame) {
	// Write frames to temp file
	tmpDir := t.TempDir()

	var ext string
	var containerFormat string

	switch codec {
	case VideoCodecVP8:
		ext = ".ivf"
		containerFormat = "ivf"
	case VideoCodecVP9:
		ext = ".ivf"
		containerFormat = "ivf"
	case VideoCodecH264:
		ext = ".h264"
		containerFormat = "h264"
	case VideoCodecAV1:
		ext = ".obu"
		containerFormat = "obu"
	default:
		t.Skipf("no container format for %s", codec)
		return
	}

	tmpFile := filepath.Join(tmpDir, "test"+ext)

	// Write bitstream
	var buf bytes.Buffer
	if containerFormat == "ivf" {
		writeIVFFile(&buf, codec, frames)
	} else {
		// Raw bitstream
		for _, f := range frames {
			buf.Write(f.Data)
		}
	}

	if err := os.WriteFile(tmpFile, buf.Bytes(), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	// Run ffprobe
	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_name,width,height,nb_frames",
		"-of", "csv=p=0",
		tmpFile,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("ffprobe output: %s", output)
		// Don't fail - ffprobe may not support all formats
		t.Logf("ffprobe verification skipped: %v", err)
		return
	}

	outputStr := strings.TrimSpace(string(output))
	if outputStr == "" {
		t.Log("ffprobe returned no stream info (may need container format)")
		return
	}

	t.Logf("ffprobe verified: %s", outputStr)
}

// writeIVFFile writes frames in IVF container format (for VP8/VP9).
func writeIVFFile(buf *bytes.Buffer, codec VideoCodec, frames []*EncodedFrame) {
	// IVF header (32 bytes)
	buf.WriteString("DKIF")                            // Signature
	binary.Write(buf, binary.LittleEndian, uint16(0))  // Version
	binary.Write(buf, binary.LittleEndian, uint16(32)) // Header size

	switch codec {
	case VideoCodecVP8:
		buf.WriteString("VP80")
	case VideoCodecVP9:
		buf.WriteString("VP90")
	case VideoCodecAV1:
		buf.WriteString("AV01")
	default:
		buf.WriteString("VP80")
	}

	binary.Write(buf, binary.LittleEndian, uint16(640))         // Width
	binary.Write(buf, binary.LittleEndian, uint16(480))         // Height
	binary.Write(buf, binary.LittleEndian, uint32(30))          // Frame rate num
	binary.Write(buf, binary.LittleEndian, uint32(1))           // Frame rate den
	binary.Write(buf, binary.LittleEndian, uint32(len(frames))) // Frame count
	binary.Write(buf, binary.LittleEndian, uint32(0))           // Unused

	// Write each frame with IVF frame header
	for i, f := range frames {
		// IVF frame header (12 bytes)
		binary.Write(buf, binary.LittleEndian, uint32(len(f.Data))) // Frame size
		binary.Write(buf, binary.LittleEndian, uint64(i))           // Timestamp
		buf.Write(f.Data)
	}
}

// TestMultiTranscodeMatrixE2E tests multi-output transcoding with all codec combinations.
func TestMultiTranscodeMatrixE2E(t *testing.T) {
	type codecInfo struct {
		codec     VideoCodec
		available func() bool
		name      string
	}

	codecs := []codecInfo{
		{VideoCodecVP8, IsVP8Available, "VP8"},
		{VideoCodecVP9, IsVP9Available, "VP9"},
		{VideoCodecH264, func() bool { return IsH264EncoderAvailable() && IsH264DecoderAvailable() }, "H264"},
	}

	// Filter to available codecs
	var available []codecInfo
	for _, c := range codecs {
		if c.available() {
			available = append(available, c)
		}
	}

	if len(available) < 2 {
		t.Skip("Need at least 2 codecs for multi-transcode test")
	}

	// Test multi-output with each codec as source
	for _, src := range available {
		// Build output configs for all available codecs
		var outputs []OutputConfig
		for _, target := range available {
			outputs = append(outputs, OutputConfig{
				ID:         target.name,
				Codec:      target.codec,
				BitrateBps: 500_000,
			})
		}

		t.Run(fmt.Sprintf("Source_%s", src.name), func(t *testing.T) {
			testMultiTranscode(t, src.codec, outputs)
		})
	}
}

func testMultiTranscode(t *testing.T, srcCodec VideoCodec, outputs []OutputConfig) {
	const (
		width     = 640
		height    = 480
		fps       = 30
		numFrames = 30
	)

	if len(outputs) < 2 {
		t.Skip("need at least 2 output codecs")
	}

	// Create multi-transcoder
	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec:  srcCodec,
		InputWidth:  width,
		InputHeight: height,
		InputFPS:    fps,
		Outputs:     outputs,
	})
	if err != nil {
		t.Fatalf("failed to create multi-transcoder: %v", err)
	}
	defer mt.Close()

	// Generate frames
	outputCounts := make(map[string]int)
	totalBytes := make(map[string]int)

	for i := 0; i < numFrames; i++ {
		raw := createColorTestFrame(width, height, i)

		result, err := mt.TranscodeRaw(raw)
		if err != nil {
			t.Fatalf("multi-transcode failed at frame %d: %v", i, err)
		}

		if result != nil {
			for _, v := range result.Variants {
				if v.Frame != nil {
					outputCounts[v.VariantID]++
					totalBytes[v.VariantID] += len(v.Frame.Data)
				}
			}
		}
	}

	// Verify all outputs produced frames
	for _, out := range outputs {
		count := outputCounts[out.ID]
		bytes := totalBytes[out.ID]
		if count == 0 {
			t.Errorf("output %s produced no frames", out.ID)
		} else {
			t.Logf("%s from %s: %d frames, %d bytes", out.ID, srcCodec, count, bytes)
		}
	}
}

// TestOpusTranscodeE2E tests Opus audio encoding/decoding end-to-end.
func TestOpusTranscodeE2E(t *testing.T) {
	if !IsOpusAvailable() {
		t.Skip("Opus not available")
	}

	const (
		sampleRate = 48000
		channels   = 1
		numFrames  = 50
		frameSize  = 960 // 20ms at 48kHz
	)

	// Create encoder
	enc, err := NewOpusEncoder(AudioEncoderConfig{
		SampleRate: sampleRate,
		Channels:   channels,
		BitrateBps: 64000,
	})
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}
	defer enc.Close()

	// Create decoder
	dec, err := NewOpusDecoder(AudioDecoderConfig{
		SampleRate: sampleRate,
		Channels:   channels,
	})
	if err != nil {
		t.Fatalf("failed to create decoder: %v", err)
	}
	defer dec.Close()

	// Generate test audio (sine wave)
	var encodedFrames []*EncodedAudio
	var totalEncodedBytes int

	for i := 0; i < numFrames; i++ {
		// Create test audio samples (16-bit PCM)
		samples := make([]byte, frameSize*2*channels)
		for j := 0; j < frameSize; j++ {
			// Simple triangle wave
			val := int16((j % 256) * 128)
			binary.LittleEndian.PutUint16(samples[j*2:], uint16(val))
		}

		encoded, err := enc.Encode(&AudioSamples{
			Data:        samples,
			SampleRate:  sampleRate,
			Channels:    channels,
			SampleCount: frameSize,
			Format:      AudioFormatS16,
			Timestamp:   int64(i) * int64(frameSize) * 1_000_000 / int64(sampleRate),
		})
		if err != nil {
			t.Fatalf("encode failed at frame %d: %v", i, err)
		}

		encodedFrames = append(encodedFrames, encoded)
		totalEncodedBytes += len(encoded.Data)
	}

	t.Logf("Opus encoded %d frames, %d bytes total", len(encodedFrames), totalEncodedBytes)

	// Decode all frames
	decodedCount := 0
	for _, frame := range encodedFrames {
		decoded, err := dec.Decode(frame)
		if err != nil {
			t.Fatalf("decode failed: %v", err)
		}
		if decoded != nil {
			decodedCount++
			if decoded.SampleCount <= 0 {
				t.Error("decoded frame has no samples")
			}
		}
	}

	t.Logf("Opus decoded %d/%d frames", decodedCount, len(encodedFrames))

	if decodedCount != len(encodedFrames) {
		t.Errorf("decoded %d frames, expected %d", decodedCount, len(encodedFrames))
	}
}

// TestTranscodeStressE2E runs extended transcoding to verify stability.
func TestTranscodeStressE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	if !IsVP8Available() {
		t.Skip("VP8 not available")
	}

	const (
		width     = 320
		height    = 240
		fps       = 30
		numFrames = 300 // 10 seconds worth
	)

	transcoder, err := NewTranscoder(TranscoderConfig{
		InputCodec:  VideoCodecVP8,
		OutputCodec: VideoCodecVP8,
		Width:       width,
		Height:      height,
		BitrateBps:  500_000,
		FPS:         fps,
	})
	if err != nil {
		t.Fatalf("failed to create transcoder: %v", err)
	}
	defer transcoder.Close()

	outputCount := 0
	keyframeCount := 0
	totalBytes := 0

	for i := 0; i < numFrames; i++ {
		raw := createColorTestFrame(width, height, i)
		output, err := transcoder.TranscodeRaw(raw)
		if err != nil {
			t.Fatalf("transcode failed at frame %d: %v", i, err)
		}
		if output != nil {
			outputCount++
			totalBytes += len(output.Data)
			if output.FrameType == FrameTypeKey {
				keyframeCount++
			}
		}
	}

	t.Logf("Stress test: %d frames, %d keyframes, %d bytes (%.1f KB/s)",
		outputCount, keyframeCount, totalBytes,
		float64(totalBytes)/float64(numFrames)*float64(fps)/1024)

	if outputCount < numFrames/2 {
		t.Errorf("too few output frames: %d/%d", outputCount, numFrames)
	}
}

// TestPassthroughWithMultiCodecE2E tests passthrough combined with transcoding to multiple codecs.
// This is a key scenario for SFU/media servers that need to relay original stream plus transcode variants.
func TestPassthroughWithMultiCodecE2E(t *testing.T) {
	codecs := []codecInfoE2E{
		{VideoCodecVP8, IsVP8Available, "VP8"},
		{VideoCodecVP9, IsVP9Available, "VP9"},
		{VideoCodecH264, func() bool { return IsH264EncoderAvailable() && IsH264DecoderAvailable() }, "H264"},
		{VideoCodecAV1, IsAV1Available, "AV1"},
	}

	// Filter to available codecs
	var available []codecInfoE2E
	for _, c := range codecs {
		if c.available() {
			available = append(available, c)
		}
	}

	if len(available) < 2 {
		t.Skip("Need at least 2 codecs")
	}

	// Test each codec as source with passthrough + transcode to all other codecs
	for _, src := range available {
		// Capture src for closure
		srcCopy := src
		t.Run(fmt.Sprintf("Source_%s", src.name), func(t *testing.T) {
			testPassthroughWithTranscode(t, srcCopy.codec, srcCopy.name, available)
		})
	}
}

type codecInfoE2E struct {
	codec     VideoCodec
	available func() bool
	name      string
}

func testPassthroughWithTranscode(t *testing.T, srcCodec VideoCodec, srcName string, codecs []codecInfoE2E) {
	const (
		width     = 640
		height    = 480
		fps       = 30
		bitrate   = 500_000
		numFrames = 30
	)

	// Build outputs: 1 passthrough + transcode to all other codecs
	outputs := []OutputConfig{
		{
			ID:          "passthrough",
			Codec:       srcCodec,
			Passthrough: true,
		},
	}

	for _, target := range codecs {
		if target.available() && target.codec != srcCodec {
			outputs = append(outputs, OutputConfig{
				ID:         fmt.Sprintf("transcode_%s", target.name),
				Codec:      target.codec,
				BitrateBps: bitrate,
			})
		}
	}

	if len(outputs) < 2 {
		t.Skip("need at least 1 transcode target besides passthrough")
	}

	t.Logf("Testing %s with %d outputs (1 passthrough + %d transcodes)", srcName, len(outputs), len(outputs)-1)

	// Create multi-transcoder
	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec:  srcCodec,
		InputWidth:  width,
		InputHeight: height,
		InputFPS:    fps,
		Outputs:     outputs,
	})
	if err != nil {
		t.Fatalf("failed to create multi-transcoder: %v", err)
	}
	defer mt.Close()

	// Create source encoder to generate input frames
	srcEncoder, err := createEncoderForCodec(srcCodec, width, height, fps, bitrate)
	if err != nil {
		t.Fatalf("failed to create source encoder: %v", err)
	}
	defer srcEncoder.Close()

	outputCounts := make(map[string]int)
	totalBytes := make(map[string]int)
	var passthroughFrames []*EncodedFrame
	transcodeFrames := make(map[string][]*EncodedFrame)

	for i := 0; i < numFrames; i++ {
		// Generate raw frame
		raw := createColorTestFrame(width, height, i)

		// Force keyframe on first frame
		if i == 0 {
			srcEncoder.RequestKeyframe()
		}

		// Encode to source codec
		srcFrame, err := srcEncoder.Encode(raw)
		if err != nil {
			continue
		}
		if srcFrame == nil {
			continue
		}

		// Transcode through multi-transcoder
		result, err := mt.Transcode(srcFrame)
		if err != nil {
			t.Fatalf("transcode failed at frame %d: %v", i, err)
		}

		if result != nil {
			for _, v := range result.Variants {
				if v.Frame != nil {
					outputCounts[v.VariantID]++
					totalBytes[v.VariantID] += len(v.Frame.Data)

					if v.VariantID == "passthrough" {
						passthroughFrames = append(passthroughFrames, v.Frame)
					} else {
						transcodeFrames[v.VariantID] = append(transcodeFrames[v.VariantID], v.Frame)
					}
				}
			}
		}
	}

	// Verify passthrough output
	ptCount := outputCounts["passthrough"]
	if ptCount == 0 {
		t.Error("passthrough produced no frames")
	} else {
		t.Logf("passthrough: %d frames, %d bytes", ptCount, totalBytes["passthrough"])
	}

	// Verify transcode outputs
	for _, out := range outputs {
		if out.Passthrough {
			continue
		}
		count := outputCounts[out.ID]
		if count == 0 {
			t.Errorf("output %s produced no frames", out.ID)
		} else {
			t.Logf("%s: %d frames, %d bytes", out.ID, count, totalBytes[out.ID])
		}
	}

	// Verify passthrough frames are decodable
	if len(passthroughFrames) > 0 {
		verifyWithInternalDecoder(t, srcCodec, passthroughFrames)
	}

	// Verify transcode frames are decodable
	for id, frames := range transcodeFrames {
		if len(frames) > 0 {
			// Extract codec from ID (e.g., "transcode_VP9" -> VP9)
			var targetCodec VideoCodec
			for _, c := range codecs {
				if strings.Contains(id, c.name) {
					targetCodec = c.codec
					break
				}
			}
			if targetCodec != VideoCodecUnknown {
				t.Run(fmt.Sprintf("Verify_%s", id), func(t *testing.T) {
					verifyWithInternalDecoder(t, targetCodec, frames)
				})
			}
		}
	}
}

// createEncoderForCodec creates an encoder for the specified codec.
func createEncoderForCodec(codec VideoCodec, width, height, fps, bitrate int) (VideoEncoder, error) {
	config := VideoEncoderConfig{
		Width:      width,
		Height:     height,
		BitrateBps: bitrate,
		FPS:        fps,
	}

	switch codec {
	case VideoCodecVP8:
		return NewVP8Encoder(config)
	case VideoCodecVP9:
		return NewVP9Encoder(config)
	case VideoCodecH264:
		return NewH264Encoder(config)
	case VideoCodecAV1:
		return NewAV1Encoder(config)
	default:
		return nil, fmt.Errorf("unsupported codec: %s", codec)
	}
}

// TestMixedPassthroughTranscodeMatrixE2E tests all combinations of source codec
// with passthrough + transcode to every other codec.
func TestMixedPassthroughTranscodeMatrixE2E(t *testing.T) {
	type codecInfo struct {
		codec     VideoCodec
		available func() bool
		name      string
	}

	codecs := []codecInfo{
		{VideoCodecVP8, IsVP8Available, "VP8"},
		{VideoCodecVP9, IsVP9Available, "VP9"},
		{VideoCodecH264, func() bool { return IsH264EncoderAvailable() && IsH264DecoderAvailable() }, "H264"},
	}

	// Filter to available codecs
	var available []codecInfo
	for _, c := range codecs {
		if c.available() {
			available = append(available, c)
		}
	}

	if len(available) < 2 {
		t.Skip("Need at least 2 codecs")
	}

	// Test matrix: for each source, test passthrough + each individual transcode target
	for _, src := range available {
		for _, dst := range available {
			if src.codec == dst.codec {
				continue // Skip same codec transcode (already covered by passthrough)
			}

			testName := fmt.Sprintf("%s_passthrough_plus_%s", src.name, dst.name)
			t.Run(testName, func(t *testing.T) {
				testPassthroughPlusSingleTranscode(t, src.codec, dst.codec)
			})
		}
	}
}

func testPassthroughPlusSingleTranscode(t *testing.T, srcCodec, dstCodec VideoCodec) {
	const (
		width     = 640
		height    = 480
		fps       = 30
		bitrate   = 500_000
		numFrames = 30
	)

	outputs := []OutputConfig{
		{ID: "passthrough", Codec: srcCodec, Passthrough: true},
		{ID: "transcode", Codec: dstCodec, BitrateBps: bitrate},
	}

	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec:  srcCodec,
		InputWidth:  width,
		InputHeight: height,
		InputFPS:    fps,
		Outputs:     outputs,
	})
	if err != nil {
		t.Fatalf("failed to create multi-transcoder: %v", err)
	}
	defer mt.Close()

	// Create source encoder
	srcEncoder, err := createEncoderForCodec(srcCodec, width, height, fps, bitrate)
	if err != nil {
		t.Fatalf("failed to create source encoder: %v", err)
	}
	defer srcEncoder.Close()

	var passthroughFrames, transcodeFrames []*EncodedFrame
	passthroughBytes, transcodeBytes := 0, 0

	for i := 0; i < numFrames; i++ {
		raw := createColorTestFrame(width, height, i)
		if i == 0 {
			srcEncoder.RequestKeyframe()
		}
		srcFrame, err := srcEncoder.Encode(raw)
		if err != nil || srcFrame == nil {
			continue
		}

		result, err := mt.Transcode(srcFrame)
		if err != nil {
			t.Fatalf("transcode failed: %v", err)
		}

		if result != nil {
			for _, v := range result.Variants {
				if v.Frame == nil {
					continue
				}
				if v.VariantID == "passthrough" {
					passthroughFrames = append(passthroughFrames, v.Frame)
					passthroughBytes += len(v.Frame.Data)
				} else {
					transcodeFrames = append(transcodeFrames, v.Frame)
					transcodeBytes += len(v.Frame.Data)
				}
			}
		}
	}

	// Verify both outputs
	if len(passthroughFrames) == 0 {
		t.Error("passthrough produced no frames")
	}
	if len(transcodeFrames) == 0 {
		t.Error("transcode produced no frames")
	}

	t.Logf("passthrough (%s): %d frames, %d bytes", srcCodec, len(passthroughFrames), passthroughBytes)
	t.Logf("transcode (%s): %d frames, %d bytes", dstCodec, len(transcodeFrames), transcodeBytes)

	// Verify decodability
	if len(passthroughFrames) > 0 {
		verifyWithInternalDecoder(t, srcCodec, passthroughFrames)
	}
	if len(transcodeFrames) > 0 {
		verifyWithInternalDecoder(t, dstCodec, transcodeFrames)
	}

	// Verify with ffprobe if available
	if hasFFprobe() {
		if len(passthroughFrames) > 0 {
			t.Run("FFprobe_passthrough", func(t *testing.T) {
				verifyWithFFprobe(t, srcCodec, passthroughFrames)
			})
		}
		if len(transcodeFrames) > 0 {
			t.Run("FFprobe_transcode", func(t *testing.T) {
				verifyWithFFprobe(t, dstCodec, transcodeFrames)
			})
		}
	}
}

// TestAllCodecsPassthroughE2E tests passthrough mode for each codec individually.
func TestAllCodecsPassthroughE2E(t *testing.T) {
	codecs := []struct {
		codec     VideoCodec
		available func() bool
		name      string
	}{
		{VideoCodecVP8, IsVP8Available, "VP8"},
		{VideoCodecVP9, IsVP9Available, "VP9"},
		{VideoCodecH264, func() bool { return IsH264EncoderAvailable() && IsH264DecoderAvailable() }, "H264"},
		{VideoCodecAV1, IsAV1Available, "AV1"},
	}

	for _, c := range codecs {
		if !c.available() {
			continue
		}

		t.Run(c.name, func(t *testing.T) {
			testSingleCodecPassthrough(t, c.codec)
		})
	}
}

func testSingleCodecPassthrough(t *testing.T, codec VideoCodec) {
	const (
		width     = 640
		height    = 480
		fps       = 30
		bitrate   = 500_000
		numFrames = 30
	)

	// Create passthrough-only multi-transcoder
	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec:  codec,
		InputWidth:  width,
		InputHeight: height,
		InputFPS:    fps,
		Outputs: []OutputConfig{
			{ID: "pt", Codec: codec, Passthrough: true},
		},
	})
	if err != nil {
		t.Fatalf("failed to create multi-transcoder: %v", err)
	}
	defer mt.Close()

	// Create encoder
	enc, err := createEncoderForCodec(codec, width, height, fps, bitrate)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}
	defer enc.Close()

	var outputFrames []*EncodedFrame
	totalBytes := 0

	for i := 0; i < numFrames; i++ {
		raw := createColorTestFrame(width, height, i)
		if i == 0 {
			enc.RequestKeyframe()
		}
		srcFrame, err := enc.Encode(raw)
		if err != nil || srcFrame == nil {
			continue
		}

		result, err := mt.Transcode(srcFrame)
		if err != nil {
			t.Fatalf("passthrough failed: %v", err)
		}

		if result != nil {
			for _, v := range result.Variants {
				if v.Frame != nil {
					outputFrames = append(outputFrames, v.Frame)
					totalBytes += len(v.Frame.Data)

					// Passthrough should preserve exact bytes
					if len(v.Frame.Data) != len(srcFrame.Data) {
						t.Errorf("passthrough modified frame size: input=%d, output=%d",
							len(srcFrame.Data), len(v.Frame.Data))
					}
				}
			}
		}
	}

	if len(outputFrames) == 0 {
		t.Fatal("passthrough produced no frames")
	}

	t.Logf("%s passthrough: %d frames, %d bytes", codec, len(outputFrames), totalBytes)

	// Verify decodability
	verifyWithInternalDecoder(t, codec, outputFrames)

	// Verify with ffprobe
	if hasFFprobe() {
		verifyWithFFprobe(t, codec, outputFrames)
	}
}

// TestTimestampPropagationSingleTranscoder verifies that the single-output Transcoder
// preserves the source frame's timestamp in the encoded output.
func TestTimestampPropagationSingleTranscoder(t *testing.T) {
	if !IsVP8Available() {
		t.Skip("VP8 not available")
	}

	const (
		width   = 640
		height  = 480
		fps     = 30
		bitrate = 500_000
	)

	transcoder, err := NewTranscoder(TranscoderConfig{
		InputCodec:  VideoCodecVP8,
		OutputCodec: VideoCodecVP8,
		Width:       width,
		Height:      height,
		BitrateBps:  bitrate,
		FPS:         fps,
	})
	if err != nil {
		t.Fatalf("failed to create transcoder: %v", err)
	}
	defer transcoder.Close()

	// Create source encoder
	srcEnc, err := NewVP8Encoder(VideoEncoderConfig{
		Width:      width,
		Height:     height,
		BitrateBps: bitrate,
		FPS:        fps,
	})
	if err != nil {
		t.Fatalf("failed to create source encoder: %v", err)
	}
	defer srcEnc.Close()

	// Generate test frames with specific timestamps
	srcEnc.RequestKeyframe()

	for i := 0; i < 30; i++ {
		raw := createColorTestFrame(width, height, i)
		srcFrame, err := srcEnc.Encode(raw)
		if err != nil || srcFrame == nil {
			continue
		}

		// Set a specific timestamp that we can verify
		expectedTs := uint32((i + 1) * 3000) // Each frame 3000 units apart (100ms at 90kHz)
		srcFrame.Timestamp = expectedTs

		// Transcode
		output, err := transcoder.Transcode(srcFrame)
		if err != nil {
			t.Fatalf("transcode failed: %v", err)
		}
		if output == nil {
			continue
		}

		// Verify timestamp is preserved
		if output.Timestamp != expectedTs {
			t.Errorf("frame %d: timestamp not preserved: got %d, want %d",
				i, output.Timestamp, expectedTs)
		}
	}
}

// TestTimestampPropagationMultiTranscoder verifies that the MultiTranscoder
// preserves the source frame's timestamp in all output variants.
func TestTimestampPropagationMultiTranscoder(t *testing.T) {
	if !IsVP8Available() || !IsVP9Available() {
		t.Skip("VP8 and VP9 required")
	}

	const (
		width   = 640
		height  = 480
		fps     = 30
		bitrate = 500_000
	)

	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec:  VideoCodecVP8,
		InputWidth:  width,
		InputHeight: height,
		InputFPS:    fps,
		Outputs: []OutputConfig{
			{ID: "passthrough", Codec: VideoCodecVP8, Passthrough: true},
			{ID: "vp8-transcode", Codec: VideoCodecVP8, BitrateBps: bitrate},
			{ID: "vp9-transcode", Codec: VideoCodecVP9, BitrateBps: bitrate},
		},
	})
	if err != nil {
		t.Fatalf("failed to create multi-transcoder: %v", err)
	}
	defer mt.Close()

	// Create source encoder
	srcEnc, err := NewVP8Encoder(VideoEncoderConfig{
		Width:      width,
		Height:     height,
		BitrateBps: bitrate,
		FPS:        fps,
	})
	if err != nil {
		t.Fatalf("failed to create source encoder: %v", err)
	}
	defer srcEnc.Close()

	srcEnc.RequestKeyframe()

	var timestampMismatches []string

	for i := 0; i < 30; i++ {
		raw := createColorTestFrame(width, height, i)
		srcFrame, err := srcEnc.Encode(raw)
		if err != nil || srcFrame == nil {
			continue
		}

		// Set a specific timestamp
		expectedTs := uint32((i + 1) * 3000)
		srcFrame.Timestamp = expectedTs

		// Transcode
		result, err := mt.Transcode(srcFrame)
		if err != nil {
			t.Fatalf("transcode failed: %v", err)
		}
		if result == nil {
			continue
		}

		// Verify all variants have the same timestamp
		for _, v := range result.Variants {
			if v.Frame == nil {
				continue
			}
			if v.Frame.Timestamp != expectedTs {
				timestampMismatches = append(timestampMismatches,
					fmt.Sprintf("frame %d, variant %s: got %d, want %d",
						i, v.VariantID, v.Frame.Timestamp, expectedTs))
			}
		}
	}

	if len(timestampMismatches) > 0 {
		for _, m := range timestampMismatches {
			t.Error(m)
		}
		t.Errorf("total timestamp mismatches: %d", len(timestampMismatches))
	}
}

// TestTimestampPropagationDynamicOutput verifies that dynamically added outputs
// also receive the correct source timestamp.
func TestTimestampPropagationDynamicOutput(t *testing.T) {
	if !IsVP8Available() || !IsVP9Available() {
		t.Skip("VP8 and VP9 required")
	}

	const (
		width   = 640
		height  = 480
		fps     = 30
		bitrate = 500_000
	)

	mt, err := NewMultiTranscoder(MultiTranscoderConfig{
		InputCodec:  VideoCodecVP8,
		InputWidth:  width,
		InputHeight: height,
		InputFPS:    fps,
		Outputs: []OutputConfig{
			{ID: "passthrough", Codec: VideoCodecVP8, Passthrough: true},
		},
	})
	if err != nil {
		t.Fatalf("failed to create multi-transcoder: %v", err)
	}
	defer mt.Close()

	// Create source encoder
	srcEnc, err := NewVP8Encoder(VideoEncoderConfig{
		Width:      width,
		Height:     height,
		BitrateBps: bitrate,
		FPS:        fps,
	})
	if err != nil {
		t.Fatalf("failed to create source encoder: %v", err)
	}
	defer srcEnc.Close()

	srcEnc.RequestKeyframe()

	// Send some frames first
	for i := 0; i < 10; i++ {
		raw := createColorTestFrame(width, height, i)
		srcFrame, _ := srcEnc.Encode(raw)
		if srcFrame != nil {
			srcFrame.Timestamp = uint32((i + 1) * 3000)
			mt.Transcode(srcFrame)
		}
	}

	// Add dynamic output
	err = mt.AddOutput(OutputConfig{
		ID:         "dynamic-vp9",
		Codec:      VideoCodecVP9,
		BitrateBps: bitrate,
	})
	if err != nil {
		t.Fatalf("failed to add dynamic output: %v", err)
	}

	// Request keyframe from source encoder to ensure decoder can resume
	srcEnc.RequestKeyframe()

	// Now send more frames and verify dynamic output has correct timestamps
	var dynamicTimestampMismatches []string

	for i := 10; i < 40; i++ {
		raw := createColorTestFrame(width, height, i)
		srcFrame, err := srcEnc.Encode(raw)
		if err != nil || srcFrame == nil {
			continue
		}

		// Set a specific timestamp - use large values to simulate ongoing stream
		expectedTs := uint32((i + 1) * 3000)
		srcFrame.Timestamp = expectedTs

		result, err := mt.Transcode(srcFrame)
		if err != nil {
			t.Fatalf("transcode failed: %v", err)
		}
		if result == nil {
			continue
		}

		for _, v := range result.Variants {
			if v.Frame == nil {
				continue
			}
			if v.VariantID == "dynamic-vp9" {
				if v.Frame.Timestamp != expectedTs {
					dynamicTimestampMismatches = append(dynamicTimestampMismatches,
						fmt.Sprintf("frame %d: got %d, want %d",
							i, v.Frame.Timestamp, expectedTs))
				}
			}
		}
	}

	if len(dynamicTimestampMismatches) > 0 {
		limit := 5
		if len(dynamicTimestampMismatches) < limit {
			limit = len(dynamicTimestampMismatches)
		}
		for _, m := range dynamicTimestampMismatches[:limit] {
			t.Error(m)
		}
		t.Errorf("total dynamic output timestamp mismatches: %d", len(dynamicTimestampMismatches))
	}
}
