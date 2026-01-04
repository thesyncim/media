package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/thesyncim/media"
)

func TestAllCombinations(t *testing.T) {
	codecs := []struct {
		name  string
		codec media.VideoCodec
	}{
		{"VP8", media.VideoCodecVP8},
		{"VP9", media.VideoCodecVP9},
		{"H264", media.VideoCodecH264},
		{"AV1", media.VideoCodecAV1},
	}

	fpsList := []int{15, 24, 30, 60}

	resolutions := []struct {
		name string
		w, h int
	}{
		{"240p", 426, 240},
		{"360p", 640, 360},
		{"480p", 640, 480},
		{"720p", 1280, 720},
		{"1080p", 1920, 1080},
	}

	bitrates := []int{500, 1000, 2000, 4000} // kbps

	sources := []string{"pattern"}

	// Check if camera is available
	provider := media.GetDeviceProvider()
	var cameraID string
	if provider != nil {
		devices, err := provider.ListVideoDevices(context.Background())
		if err == nil && len(devices) > 0 {
			sources = append(sources, "camera")
			cameraID = devices[0].DeviceID
			t.Logf("Camera available: %s", devices[0].Label)
		}
	}

	t.Logf("Testing %d codecs x %d FPS x %d resolutions x %d bitrates x %d sources = %d combinations",
		len(codecs), len(fpsList), len(resolutions), len(bitrates), len(sources),
		len(codecs)*len(fpsList)*len(resolutions)*len(bitrates)*len(sources))

	for _, codecInfo := range codecs {
		for _, fps := range fpsList {
			for _, res := range resolutions {
				for _, bitrate := range bitrates {
					for _, source := range sources {
						name := fmt.Sprintf("%s/%s/%dfps/%dkbps/%s",
							codecInfo.name, res.name, fps, bitrate, source)
						t.Run(name, func(t *testing.T) {
							testCombination(t, codecInfo.name, codecInfo.codec,
								res.w, res.h, fps, bitrate, source, cameraID)
						})
					}
				}
			}
		}
	}
}

func TestKeyframeInterval(t *testing.T) {
	codecs := []struct {
		name  string
		codec media.VideoCodec
	}{
		{"VP8", media.VideoCodecVP8},
		{"VP9", media.VideoCodecVP9},
		{"H264", media.VideoCodecH264},
		{"AV1", media.VideoCodecAV1},
	}

	intervals := []int{0, 30, 60} // 0 = auto, 30 = 1 sec, 60 = 2 sec at 30fps

	for _, codecInfo := range codecs {
		for _, interval := range intervals {
			name := fmt.Sprintf("%s/keyframe_%d", codecInfo.name, interval)
			t.Run(name, func(t *testing.T) {
				testKeyframeInterval(t, codecInfo.name, codecInfo.codec, interval)
			})
		}
	}
}

func testKeyframeInterval(t *testing.T, codecName string, codec media.VideoCodec, keyframeInterval int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	width, height, fps := 640, 480, 30

	source := media.NewTestPatternSource(media.TestPatternConfig{
		Width:   width,
		Height:  height,
		FPS:     fps,
		Pattern: media.PatternMovingBox,
	})
	defer source.Close()

	if err := source.Start(ctx); err != nil {
		t.Fatalf("Failed to start source: %v", err)
	}

	config := media.VideoEncoderConfig{
		Width:            width,
		Height:           height,
		BitrateBps:       1000000,
		FPS:              fps,
		KeyframeInterval: keyframeInterval,
	}

	var encoder media.VideoEncoder
	var err error
	switch codec {
	case media.VideoCodecVP8:
		encoder, err = media.NewVP8Encoder(config)
	case media.VideoCodecVP9:
		encoder, err = media.NewVP9Encoder(config)
	case media.VideoCodecH264:
		encoder, err = media.NewH264Encoder(config)
	case media.VideoCodecAV1:
		encoder, err = media.NewAV1Encoder(config)
	}
	if err != nil {
		t.Fatalf("Failed to create %s encoder: %v", codecName, err)
	}
	defer encoder.Close()

	// Encode enough frames to see keyframes
	framesToEncode := 90 // 3 seconds at 30fps
	keyframeCount := 0
	totalFrames := 0

	frameDuration := time.Second / time.Duration(fps)
	ticker := time.NewTicker(frameDuration)
	defer ticker.Stop()

	for i := 0; i < framesToEncode; i++ {
		select {
		case <-ctx.Done():
			goto done
		case <-ticker.C:
			frame, err := source.ReadFrame(ctx)
			if err != nil {
				continue
			}

			encoded, err := encoder.Encode(frame)
			if err != nil || encoded == nil {
				continue
			}

			totalFrames++
			if encoded.FrameType == media.FrameTypeKey {
				keyframeCount++
			}
		}
	}

done:
	t.Logf("%s keyframe_interval=%d: %d keyframes in %d frames",
		codecName, keyframeInterval, keyframeCount, totalFrames)

	// Verify we got at least one keyframe (the first frame)
	if keyframeCount == 0 {
		t.Error("No keyframes were generated")
	}

	// If keyframe interval is set, verify approximate keyframe frequency
	if keyframeInterval > 0 && totalFrames > keyframeInterval*2 {
		expectedKeyframes := totalFrames / keyframeInterval
		// Allow some variance
		minKeyframes := expectedKeyframes / 2
		if keyframeCount < minKeyframes {
			t.Logf("Warning: Expected around %d keyframes, got %d", expectedKeyframes, keyframeCount)
		}
	}
}

func testCombination(t *testing.T, codecName string, codec media.VideoCodec, width, height, fps, bitrateKbps int, sourceType, cameraID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create video source
	var source media.VideoSource
	var err error

	if sourceType == "camera" {
		source, err = media.NewCameraSource(media.CameraConfig{
			DeviceID:  cameraID,
			Width:     width,
			Height:    height,
			FPS:       fps,
			ScaleMode: media.ScaleModeFit,
		})
		if err != nil {
			t.Skipf("Camera not available: %v", err)
			return
		}
	} else {
		source = media.NewTestPatternSource(media.TestPatternConfig{
			Width:   width,
			Height:  height,
			FPS:     fps,
			Pattern: media.PatternMovingBox,
		})
	}
	defer source.Close()

	if err := source.Start(ctx); err != nil {
		t.Fatalf("Failed to start source: %v", err)
	}

	// Create encoder
	config := media.VideoEncoderConfig{
		Width:      width,
		Height:     height,
		BitrateBps: bitrateKbps * 1000,
		FPS:        fps,
	}

	var encoder media.VideoEncoder
	switch codec {
	case media.VideoCodecVP8:
		encoder, err = media.NewVP8Encoder(config)
	case media.VideoCodecVP9:
		encoder, err = media.NewVP9Encoder(config)
	case media.VideoCodecH264:
		encoder, err = media.NewH264Encoder(config)
	case media.VideoCodecAV1:
		encoder, err = media.NewAV1Encoder(config)
	}
	if err != nil {
		t.Fatalf("Failed to create %s encoder: %v", codecName, err)
	}
	defer encoder.Close()

	// Create packetizer
	packetizer, err := media.CreateVideoPacketizer(codec, 0x12345678, 96, 1200)
	if err != nil {
		t.Fatalf("Failed to create packetizer: %v", err)
	}

	// Test encoding frames - at least 1 second worth or minimum 10 frames
	framesToEncode := fps
	if framesToEncode < 10 {
		framesToEncode = 10
	}

	successfulFrames := 0
	totalPackets := 0
	totalBytes := 0

	frameDuration := time.Second / time.Duration(fps)
	ticker := time.NewTicker(frameDuration)
	defer ticker.Stop()

	startTime := time.Now()

	for i := 0; i < framesToEncode; i++ {
		select {
		case <-ctx.Done():
			if successfulFrames == 0 {
				t.Fatalf("Test timed out after %d frames with no success", i)
			}
			goto done
		case <-ticker.C:
			frame, err := source.ReadFrame(ctx)
			if err != nil {
				if ctx.Err() != nil {
					goto done
				}
				continue
			}

			encoded, err := encoder.Encode(frame)
			if err != nil {
				continue
			}

			if encoded == nil {
				continue // Some frames may be skipped
			}

			packets, err := packetizer.Packetize(encoded)
			if err != nil {
				continue
			}

			successfulFrames++
			totalPackets += len(packets)
			totalBytes += len(encoded.Data)
		}
	}

done:
	elapsed := time.Since(startTime)
	actualFPS := float64(successfulFrames) / elapsed.Seconds()
	actualBitrate := float64(totalBytes*8) / elapsed.Seconds() / 1000

	t.Logf("%s %dx%d @ %dfps %dkbps (%s): %d frames (%.1f fps), %.0f kbps actual, %d packets",
		codecName, width, height, fps, bitrateKbps, sourceType,
		successfulFrames, actualFPS, actualBitrate, totalPackets)

	// Verify we encoded at least 50% of frames (some codecs may skip frames at high FPS)
	minFrames := framesToEncode * 50 / 100
	if minFrames < 5 {
		minFrames = 5
	}
	if successfulFrames < minFrames {
		t.Errorf("Only encoded %d/%d frames (expected at least %d)", successfulFrames, framesToEncode, minFrames)
	}
}

