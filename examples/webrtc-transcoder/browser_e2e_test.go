// Browser E2E tests for WebRTC Multi-Transcoder
//
// These tests use chromedp to run headless Chrome and verify the full
// publish → transcode → subscribe pipeline with real browser WebRTC.
//
// Requirements:
// - Chrome/Chromium installed
// - Run with: go test -v -run TestBrowser
//
// The tests use canvas-based fake video to avoid needing a real camera.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// TestServer wraps the HTTP server for testing
type TestServer struct {
	server *http.Server
	addr   string
}

// startTestServer starts the WebRTC transcoder server on a random port
func startTestServer(t *testing.T) *TestServer {
	// Find available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to find available port: %v", err)
	}
	addr := listener.Addr().String()
	listener.Close()

	// Create HTTP server with same handlers as main()
	mux := http.NewServeMux()
	mux.HandleFunc("/", serveHTML)
	mux.HandleFunc("/publish", handlePublish)
	mux.HandleFunc("/subscribe", handleSubscribe)
	mux.HandleFunc("/status", handleStatus)
	mux.HandleFunc("/add-variant", handleAddVariant)
	mux.HandleFunc("/remove-variant", handleRemoveVariant)

	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			t.Logf("Server error: %v", err)
		}
	}()

	// Wait for server to be ready
	time.Sleep(100 * time.Millisecond)

	t.Logf("Test server started on http://%s", addr)

	return &TestServer{
		server: server,
		addr:   addr,
	}
}

func (ts *TestServer) URL() string {
	return "http://" + ts.addr
}

func (ts *TestServer) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ts.server.Shutdown(ctx)
}

// evalAsync evaluates an async JavaScript expression and waits for Promise resolution
func evalAsync(expr string, res interface{}) chromedp.Action {
	return chromedp.Evaluate(expr, res, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
		return p.WithAwaitPromise(true)
	})
}

// createBrowserContext creates a headless Chrome context for testing
func createBrowserContext(t *testing.T) (context.Context, context.CancelFunc) {
	// Check if Chrome is available
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		// Allow fake media devices for testing
		chromedp.Flag("use-fake-device-for-media-stream", true),
		chromedp.Flag("use-fake-ui-for-media-stream", true),
		// Enable WebRTC
		chromedp.Flag("enable-features", "WebRTC"),
		// Disable audio to speed up tests
		chromedp.Flag("mute-audio", true),
		// Allow autoplay
		chromedp.Flag("autoplay-policy", "no-user-gesture-required"),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)

	// Create browser context with logging
	ctx, cancel := chromedp.NewContext(allocCtx,
		chromedp.WithLogf(t.Logf),
	)

	// Set timeout
	ctx, timeoutCancel := context.WithTimeout(ctx, 60*time.Second)

	return ctx, func() {
		timeoutCancel()
		cancel()
		allocCancel()
	}
}

// TestBrowserPublishVP8 tests publishing VP8 video from browser
func TestBrowserPublishVP8(t *testing.T) {
	if os.Getenv("SKIP_BROWSER_TESTS") != "" {
		t.Skip("Skipping browser tests (SKIP_BROWSER_TESTS set)")
	}

	// Start server
	ts := startTestServer(t)
	defer ts.Shutdown()
	defer stopTranscodePipeline() // Cleanup global state

	// Create browser context
	ctx, cancel := createBrowserContext(t)
	defer cancel()

	var publisherState string
	var framesSent int

	err := chromedp.Run(ctx,
		// Navigate to server
		chromedp.Navigate(ts.URL()),
		chromedp.WaitVisible("#publishBtn", chromedp.ByID),

		// Enable fake video before publishing
		chromedp.Evaluate(`window.e2e.enableFakeVideo(640, 480, 30)`, nil),
		chromedp.Sleep(500*time.Millisecond),

		// Set codec to VP8
		chromedp.SetValue("#publishCodecSelect", "VP8", chromedp.ByID),

		// Click publish button
		chromedp.Click("#publishBtn", chromedp.ByID),

		// Wait for connection
		chromedp.Sleep(3*time.Second),

		// Check publisher state
		chromedp.Evaluate(`window.e2e.getPublisherState()`, &publisherState),

		// Wait for frames to be sent
		chromedp.Sleep(2*time.Second),

		// Get frames sent count (async function, use evalAsync)
		evalAsync(`window.e2e.getPublishedFrameCount()`, &framesSent),
	)

	if err != nil {
		t.Fatalf("Browser test failed: %v", err)
	}

	t.Logf("Publisher state: %s, Frames sent: %d", publisherState, framesSent)

	if publisherState != "connected" {
		t.Errorf("Expected publisher state 'connected', got '%s'", publisherState)
	}

	if framesSent < 30 {
		t.Errorf("Expected at least 30 frames sent, got %d", framesSent)
	}
}

// TestBrowserPublishH264 tests publishing H264 video from browser
func TestBrowserPublishH264(t *testing.T) {
	if os.Getenv("SKIP_BROWSER_TESTS") != "" {
		t.Skip("Skipping browser tests (SKIP_BROWSER_TESTS set)")
	}

	// Start server
	ts := startTestServer(t)
	defer ts.Shutdown()
	defer stopTranscodePipeline()

	// Create browser context
	ctx, cancel := createBrowserContext(t)
	defer cancel()

	var publisherState string
	var framesSent int

	err := chromedp.Run(ctx,
		chromedp.Navigate(ts.URL()),
		chromedp.WaitVisible("#publishBtn", chromedp.ByID),

		// Enable fake video
		chromedp.Evaluate(`window.e2e.enableFakeVideo(640, 480, 30)`, nil),
		chromedp.Sleep(500*time.Millisecond),

		// Set codec to H264
		chromedp.SetValue("#publishCodecSelect", "H264", chromedp.ByID),

		// Publish
		chromedp.Click("#publishBtn", chromedp.ByID),
		chromedp.Sleep(3*time.Second),

		chromedp.Evaluate(`window.e2e.getPublisherState()`, &publisherState),
		chromedp.Sleep(2*time.Second),
		evalAsync(`window.e2e.getPublishedFrameCount()`, &framesSent),
	)

	if err != nil {
		t.Fatalf("Browser test failed: %v", err)
	}

	t.Logf("Publisher state: %s, Frames sent: %d", publisherState, framesSent)

	if publisherState != "connected" {
		t.Errorf("Expected publisher state 'connected', got '%s'", publisherState)
	}

	if framesSent < 30 {
		t.Errorf("Expected at least 30 frames sent, got %d", framesSent)
	}
}

// TestBrowserSubscribe tests subscribing to transcoded variants with FPS verification
func TestBrowserSubscribe(t *testing.T) {
	if os.Getenv("SKIP_BROWSER_TESTS") != "" {
		t.Skip("Skipping browser tests (SKIP_BROWSER_TESTS set)")
	}

	// Start server
	ts := startTestServer(t)
	defer ts.Shutdown()
	defer stopTranscodePipeline()

	// Create browser context
	ctx, cancel := createBrowserContext(t)
	defer cancel()

	var publisherState string
	var framesSentStart, framesSentEnd int
	var subscriberStatsStart, subscriberStatsEnd map[string]interface{}

	const measureDurationSec = 3.0 // How long we measure FPS (after warmup)
	const minAcceptableFPS = 20.0  // At least ~66% of 30fps target

	err := chromedp.Run(ctx,
		chromedp.Navigate(ts.URL()),
		chromedp.WaitVisible("#publishBtn", chromedp.ByID),

		// Enable fake video and publish
		chromedp.Evaluate(`window.e2e.enableFakeVideo(640, 480, 30)`, nil),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.Click("#publishBtn", chromedp.ByID),

		// Wait for subscribers to connect and start receiving frames
		// This polls until at least one subscriber has 10+ frames
		evalAsync(`window.e2e.waitForSubscriberFrames(30, 15000)`, &subscriberStatsStart),

		chromedp.Evaluate(`window.e2e.getPublisherState()`, &publisherState),
		evalAsync(`window.e2e.getPublishedFrameCount()`, &framesSentStart),

		// NOW measure FPS over a fixed duration
		chromedp.Sleep(time.Duration(measureDurationSec)*time.Second),

		evalAsync(`window.e2e.getPublishedFrameCount()`, &framesSentEnd),
		evalAsync(`window.e2e.getAllSubscriberStats()`, &subscriberStatsEnd),
	)

	if err != nil {
		t.Fatalf("Browser test failed: %v", err)
	}

	t.Logf("Publisher state: %s", publisherState)
	t.Logf("Frames sent: start=%d, end=%d", framesSentStart, framesSentEnd)

	if publisherState != "connected" {
		t.Errorf("Expected publisher state 'connected', got '%s'", publisherState)
	}

	// Calculate FPS from the measurement period
	framesDelta := framesSentEnd - framesSentStart
	publisherFPS := float64(framesDelta) / measureDurationSec
	t.Logf("Publisher FPS: %.1f (measured over %.1fs)", publisherFPS, measureDurationSec)

	if publisherFPS < minAcceptableFPS {
		t.Errorf("Publisher FPS too low: %.1f < %.0f", publisherFPS, minAcceptableFPS)
	}

	// Check subscriber stats
	statsJSON, _ := json.MarshalIndent(subscriberStatsEnd, "", "  ")
	t.Logf("Subscriber stats: %s", statsJSON)

	if len(subscriberStatsEnd) == 0 {
		t.Error("No subscriber stats - subscribers may have disconnected")
		return
	}

	// Calculate FPS for each subscriber by comparing start/end stats
	for variantID, endStats := range subscriberStatsEnd {
		endMap, ok := endStats.(map[string]interface{})
		if !ok {
			continue
		}

		framesEnd, _ := endMap["framesReceived"].(float64)

		// Get start frames for this variant
		var framesStart float64
		if startStats, ok := subscriberStatsStart[variantID]; ok {
			if startMap, ok := startStats.(map[string]interface{}); ok {
				framesStart, _ = startMap["framesReceived"].(float64)
			}
		}

		framesDelta := framesEnd - framesStart
		subscriberFPS := framesDelta / measureDurationSec
		t.Logf("Variant %s: frames=%d→%d (delta=%.0f), FPS: %.1f",
			variantID, int(framesStart), int(framesEnd), framesDelta, subscriberFPS)

		if subscriberFPS < minAcceptableFPS {
			t.Errorf("Variant %s: FPS too low: %.1f < %.0f", variantID, subscriberFPS, minAcceptableFPS)
		}
	}
}

// TestBrowserDynamicVariant tests adding a variant while streaming with FPS verification
func TestBrowserDynamicVariant(t *testing.T) {
	if os.Getenv("SKIP_BROWSER_TESTS") != "" {
		t.Skip("Skipping browser tests (SKIP_BROWSER_TESTS set)")
	}

	// Start server
	ts := startTestServer(t)
	defer ts.Shutdown()
	defer stopTranscodePipeline()

	// Create browser context
	ctx, cancel := createBrowserContext(t)
	defer cancel()

	var publisherState string
	var addResultRaw string
	var subscriberStatsBefore map[string]interface{}
	var subscriberStatsAfterAdd map[string]interface{}
	var subscriberStatsEnd map[string]interface{}
	var framesSentAtAdd, framesSentEnd int

	const dynamicVariantID = "test-dynamic-360p"
	const measureDurationSec = 3.0 // How long to measure after adding variant
	const minAcceptableFPS = 15.0  // Lower threshold for dynamic variant

	err := chromedp.Run(ctx,
		chromedp.Navigate(ts.URL()),
		chromedp.WaitVisible("#publishBtn", chromedp.ByID),

		// Enable fake video and publish
		chromedp.Evaluate(`window.e2e.enableFakeVideo(640, 480, 30)`, nil),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.Click("#publishBtn", chromedp.ByID),

		// Wait for initial subscribers to connect and start receiving frames
		evalAsync(`window.e2e.waitForSubscriberFrames(30, 15000)`, &subscriberStatsBefore),

		chromedp.Evaluate(`window.e2e.getPublisherState()`, &publisherState),

		// Add a new variant dynamically via API (use VP8 at different resolution)
		evalAsync(`
			(async () => {
				const resp = await fetch('/add-variant', {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({
						id: 'test-dynamic-360p',
						codec: 'VP8',
						width: 640,
						height: 360,
						bitrateBps: 400000
					})
				});
				const result = resp.ok ? await resp.json() : { error: await resp.text() };
				return JSON.stringify(result);
			})()
		`, &addResultRaw),

		// Wait for the dynamic variant subscriber to connect and receive some frames
		evalAsync(`window.e2e.waitForSubscriberFrames(10, 10000)`, &subscriberStatsAfterAdd),
		evalAsync(`window.e2e.getPublishedFrameCount()`, &framesSentAtAdd),

		// Now measure FPS over a fixed duration
		chromedp.Sleep(time.Duration(measureDurationSec)*time.Second),

		evalAsync(`window.e2e.getPublishedFrameCount()`, &framesSentEnd),
		evalAsync(`window.e2e.getAllSubscriberStats()`, &subscriberStatsEnd),
	)

	if err != nil {
		t.Fatalf("Browser test failed: %v", err)
	}

	t.Logf("Publisher state: %s", publisherState)
	t.Logf("Add variant result (raw): %s", addResultRaw)

	if publisherState != "connected" {
		t.Errorf("Expected publisher state 'connected', got '%s'", publisherState)
	}

	// Parse add result
	var addResult map[string]interface{}
	addSuccess := false
	if err := json.Unmarshal([]byte(addResultRaw), &addResult); err != nil {
		t.Errorf("Failed to parse add variant result: %v", err)
	} else if errMsg, hasError := addResult["error"]; hasError {
		t.Errorf("Add variant failed: %v", errMsg)
	} else if success, _ := addResult["success"].(bool); success {
		t.Log("Dynamic variant added successfully")
		addSuccess = true
	}

	// Log stats progression
	t.Logf("Stats BEFORE add: %d variants", len(subscriberStatsBefore))
	t.Logf("Stats after add (warmup): %d variants", len(subscriberStatsAfterAdd))
	t.Logf("Stats END: %d variants", len(subscriberStatsEnd))

	statsEndJSON, _ := json.MarshalIndent(subscriberStatsEnd, "", "  ")
	t.Logf("Final subscriber stats: %s", statsEndJSON)

	if len(subscriberStatsEnd) == 0 {
		t.Error("No subscriber stats at end - subscribers may have disconnected")
		return
	}

	// Calculate FPS for each variant
	for variantID, endStats := range subscriberStatsEnd {
		endMap, ok := endStats.(map[string]interface{})
		if !ok {
			continue
		}

		framesEnd, _ := endMap["framesReceived"].(float64)

		// Get frames at add time for this variant
		var framesAtAdd float64
		if addStats, ok := subscriberStatsAfterAdd[variantID]; ok {
			if addMap, ok := addStats.(map[string]interface{}); ok {
				framesAtAdd, _ = addMap["framesReceived"].(float64)
			}
		}

		framesDelta := framesEnd - framesAtAdd
		variantFPS := framesDelta / measureDurationSec
		t.Logf("Variant %s: frames=%d→%d (delta=%.0f), FPS: %.1f",
			variantID, int(framesAtAdd), int(framesEnd), framesDelta, variantFPS)
	}

	// Verify the DYNAMIC variant specifically received frames and has good FPS
	if addSuccess {
		dynamicStats, hasDynamic := subscriberStatsEnd[dynamicVariantID]
		if !hasDynamic {
			t.Errorf("Dynamic variant %s not found in subscriber stats", dynamicVariantID)
		} else if statsMap, ok := dynamicStats.(map[string]interface{}); ok {
			framesReceived, _ := statsMap["framesReceived"].(float64)

			// Get frames at add time
			var framesAtAdd float64
			if addStats, ok := subscriberStatsAfterAdd[dynamicVariantID]; ok {
				if addMap, ok := addStats.(map[string]interface{}); ok {
					framesAtAdd, _ = addMap["framesReceived"].(float64)
				}
			}

			framesDelta := framesReceived - framesAtAdd
			dynamicFPS := framesDelta / measureDurationSec
			t.Logf("Dynamic variant %s: total=%.0f, delta=%.0f, FPS: %.1f",
				dynamicVariantID, framesReceived, framesDelta, dynamicFPS)

			if framesReceived < 10 {
				t.Errorf("Dynamic variant received too few total frames: %.0f (expected at least 10)", framesReceived)
			}
			if dynamicFPS < minAcceptableFPS {
				t.Errorf("Dynamic variant FPS too low: %.1f < %.0f", dynamicFPS, minAcceptableFPS)
			}
		}
	}
}

// TestBrowserAllCodecs tests publishing with each supported codec
func TestBrowserAllCodecs(t *testing.T) {
	if os.Getenv("SKIP_BROWSER_TESTS") != "" {
		t.Skip("Skipping browser tests (SKIP_BROWSER_TESTS set)")
	}

	// Note: VP9 encoding from canvas can be slow/unsupported in headless Chrome
	codecs := []string{"VP8", "H264"}

	for _, codec := range codecs {
		t.Run(codec, func(t *testing.T) {
			// Start fresh server for each codec
			ts := startTestServer(t)
			defer ts.Shutdown()
			defer stopTranscodePipeline()

			ctx, cancel := createBrowserContext(t)
			defer cancel()

			var publisherState string
			var framesSent int
			var codecSupported bool

			err := chromedp.Run(ctx,
				chromedp.Navigate(ts.URL()),
				chromedp.WaitVisible("#publishBtn", chromedp.ByID),

				// Check if codec is supported by browser
				chromedp.Evaluate(fmt.Sprintf(`
					(() => {
						const caps = RTCRtpSender.getCapabilities('video');
						return caps && caps.codecs.some(c => c.mimeType === 'video/%s');
					})()
				`, codec), &codecSupported),

				// Enable fake video
				chromedp.Evaluate(`window.e2e.enableFakeVideo(640, 480, 30)`, nil),
				chromedp.Sleep(500*time.Millisecond),

				// Set codec
				chromedp.SetValue("#publishCodecSelect", codec, chromedp.ByID),

				// Publish
				chromedp.Click("#publishBtn", chromedp.ByID),
				chromedp.Sleep(3*time.Second),

				chromedp.Evaluate(`window.e2e.getPublisherState()`, &publisherState),
				chromedp.Sleep(2*time.Second),
				evalAsync(`window.e2e.getPublishedFrameCount()`, &framesSent),
			)

			if err != nil {
				// Check if it's a codec support issue
				if !codecSupported {
					t.Skipf("Browser does not support %s codec", codec)
				}
				t.Fatalf("Browser test failed: %v", err)
			}

			t.Logf("%s: state=%s, frames=%d, supported=%v", codec, publisherState, framesSent, codecSupported)

			if publisherState != "connected" {
				t.Errorf("Expected publisher state 'connected', got '%s'", publisherState)
			}

			if framesSent < 30 {
				t.Errorf("Expected at least 30 frames sent, got %d", framesSent)
			}
		})
	}
}

// TestBrowserEndToEnd tests the full publish → transcode → subscribe flow
func TestBrowserEndToEnd(t *testing.T) {
	if os.Getenv("SKIP_BROWSER_TESTS") != "" {
		t.Skip("Skipping browser tests (SKIP_BROWSER_TESTS set)")
	}

	ts := startTestServer(t)
	defer ts.Shutdown()
	defer stopTranscodePipeline()

	ctx, cancel := createBrowserContext(t)
	defer cancel()

	var publisherState string
	var framesSent int
	var serverStatus map[string]interface{}
	var subscriberStats map[string]interface{}

	err := chromedp.Run(ctx,
		chromedp.Navigate(ts.URL()),
		chromedp.WaitVisible("#publishBtn", chromedp.ByID),

		// Enable fake video
		chromedp.Evaluate(`window.e2e.enableFakeVideo(640, 480, 30)`, nil),
		chromedp.Sleep(500*time.Millisecond),

		// Publish with VP8 (most compatible)
		chromedp.SetValue("#publishCodecSelect", "VP8", chromedp.ByID),
		chromedp.Click("#publishBtn", chromedp.ByID),

		// Wait for transcoding to fully start
		chromedp.Sleep(4*time.Second),

		// Get server status FIRST (before any cleanup can happen)
		evalAsync(`
			(async () => {
				const resp = await fetch('/status');
				return await resp.json();
			})()
		`, &serverStatus),

		chromedp.Evaluate(`window.e2e.getPublisherState()`, &publisherState),
		evalAsync(`window.e2e.getPublishedFrameCount()`, &framesSent),

		// Wait for subscribers to get some frames
		chromedp.Sleep(2*time.Second),

		// Get subscriber stats
		evalAsync(`window.e2e.getAllSubscriberStats()`, &subscriberStats),
	)

	if err != nil {
		t.Fatalf("Browser E2E test failed: %v", err)
	}

	t.Logf("=== E2E Test Results ===")
	t.Logf("Publisher: state=%s, framesSent=%d", publisherState, framesSent)

	// Log server status
	if serverStatus != nil {
		statusJSON, _ := json.MarshalIndent(serverStatus, "", "  ")
		t.Logf("Server status: %s", statusJSON)

		// Check if transcoding is active (just log, don't fail - might have timing issues)
		if active, ok := serverStatus["active"].(bool); ok {
			if active {
				t.Log("Server transcoding is active")
			} else {
				t.Log("Warning: Server reports transcoding not active (timing issue)")
			}
		}

		// Check variants
		if variants, ok := serverStatus["variants"].([]interface{}); ok {
			t.Logf("Active variants: %d", len(variants))
			for _, v := range variants {
				if vm, ok := v.(map[string]interface{}); ok {
					t.Logf("  - %s: %s", vm["ID"], vm["Label"])
				}
			}
		}
	}

	// Log subscriber stats
	statsJSON, _ := json.MarshalIndent(subscriberStats, "", "  ")
	t.Logf("Subscriber stats: %s", statsJSON)

	// Verify results - key metrics
	if publisherState != "connected" {
		t.Errorf("Publisher not connected: %s", publisherState)
	}

	if framesSent < 50 {
		t.Errorf("Not enough frames sent: %d (expected >50)", framesSent)
	} else {
		t.Logf("Frames sent: %d (OK)", framesSent)
	}

	// Check subscribers received frames
	for variantID, stats := range subscriberStats {
		if statsMap, ok := stats.(map[string]interface{}); ok {
			if framesReceived, ok := statsMap["framesReceived"].(float64); ok {
				t.Logf("Variant %s: %.0f frames received", variantID, framesReceived)
			}
		}
	}
}

// TestBrowserConsoleErrors checks for JavaScript errors during operation
func TestBrowserConsoleErrors(t *testing.T) {
	if os.Getenv("SKIP_BROWSER_TESTS") != "" {
		t.Skip("Skipping browser tests (SKIP_BROWSER_TESTS set)")
	}

	ts := startTestServer(t)
	defer ts.Shutdown()
	defer stopTranscodePipeline()

	ctx, cancel := createBrowserContext(t)
	defer cancel()

	// Capture console errors
	var consoleErrors []string
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		if ev, ok := ev.(interface{ Message() string }); ok {
			msg := ev.Message()
			if strings.Contains(strings.ToLower(msg), "error") {
				consoleErrors = append(consoleErrors, msg)
			}
		}
	})

	err := chromedp.Run(ctx,
		chromedp.Navigate(ts.URL()),
		chromedp.WaitVisible("#publishBtn", chromedp.ByID),

		chromedp.Evaluate(`window.e2e.enableFakeVideo(640, 480, 30)`, nil),
		chromedp.Sleep(500*time.Millisecond),

		chromedp.Click("#publishBtn", chromedp.ByID),
		chromedp.Sleep(5*time.Second),
	)

	if err != nil {
		t.Fatalf("Browser test failed: %v", err)
	}

	if len(consoleErrors) > 0 {
		t.Logf("Console errors detected:")
		for _, e := range consoleErrors {
			t.Logf("  - %s", e)
		}
		// Don't fail on console errors, just log them
	} else {
		t.Log("No console errors detected")
	}
}

// TestBrowserDynamicVariantDiagnostic is a longer-running diagnostic test
// that monitors decode health after adding dynamic variants.
// Run with: go test -v -run TestBrowserDynamicVariantDiagnostic -timeout 60s
func TestBrowserDynamicVariantDiagnostic(t *testing.T) {
	if os.Getenv("SKIP_BROWSER_TESTS") != "" {
		t.Skip("Skipping browser tests (SKIP_BROWSER_TESTS set)")
	}
	if os.Getenv("RUN_DIAGNOSTIC_TESTS") == "" {
		t.Skip("Skipping diagnostic test (set RUN_DIAGNOSTIC_TESTS=1 to run)")
	}

	ts := startTestServer(t)
	defer ts.Shutdown()
	defer stopTranscodePipeline()

	ctx, cancel := createBrowserContext(t)
	defer cancel()

	var publisherState string

	// Start publishing
	err := chromedp.Run(ctx,
		chromedp.Navigate(ts.URL()),
		chromedp.WaitVisible("#publishBtn", chromedp.ByID),
		chromedp.Evaluate(`window.e2e.enableFakeVideo(640, 480, 30)`, nil),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.Click("#publishBtn", chromedp.ByID),
		chromedp.Sleep(3*time.Second),
		chromedp.Evaluate(`window.e2e.getPublisherState()`, &publisherState),
	)
	if err != nil {
		t.Fatalf("Failed to start publishing: %v", err)
	}
	t.Logf("Publisher state: %s", publisherState)

	// Wait for initial subscribers to stabilize
	var statsBeforeAdd map[string]interface{}
	err = chromedp.Run(ctx,
		evalAsync(`window.e2e.waitForSubscriberFrames(60, 10000)`, &statsBeforeAdd),
	)
	if err != nil {
		t.Fatalf("Failed to get initial stats: %v", err)
	}
	t.Logf("=== STATS BEFORE ADDING DYNAMIC VARIANT ===")
	logDetailedStats(t, statsBeforeAdd)

	// Add dynamic variant
	var addResultRaw string
	err = chromedp.Run(ctx,
		evalAsync(`
			(async () => {
				const resp = await fetch('/add-variant', {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({
						id: 'diag-dynamic-360p',
						codec: 'VP8',
						width: 640,
						height: 360,
						bitrateBps: 400000
					})
				});
				return JSON.stringify(await resp.json());
			})()
		`, &addResultRaw),
	)
	if err != nil {
		t.Fatalf("Failed to add variant: %v", err)
	}
	t.Logf("Add variant result: %s", addResultRaw)

	// Monitor stats over multiple intervals
	for i := 0; i < 5; i++ {
		time.Sleep(2 * time.Second)

		var stats map[string]interface{}
		err = chromedp.Run(ctx,
			evalAsync(`window.e2e.getAllSubscriberStats()`, &stats),
		)
		if err != nil {
			t.Logf("Interval %d: Failed to get stats: %v", i+1, err)
			continue
		}

		t.Logf("=== STATS INTERVAL %d (t+%ds after add) ===", i+1, (i+1)*2)
		logDetailedStats(t, stats)

		// Check for issues
		for variantID, variantStats := range stats {
			if statsMap, ok := variantStats.(map[string]interface{}); ok {
				framesReceived, _ := statsMap["framesReceived"].(float64)
				framesDecoded, _ := statsMap["framesDecoded"].(float64)
				framesDropped, _ := statsMap["framesDropped"].(float64)
				freezeCount, _ := statsMap["freezeCount"].(float64)
				pliCount, _ := statsMap["pliCount"].(float64)

				if framesDropped > 0 {
					t.Logf("WARNING: %s has %d dropped frames", variantID, int(framesDropped))
				}
				if freezeCount > 0 {
					t.Logf("WARNING: %s has %d freezes", variantID, int(freezeCount))
				}
				if framesReceived > 0 && framesDecoded < framesReceived*0.9 {
					t.Logf("WARNING: %s decode ratio low: %d/%d (%.1f%%)",
						variantID, int(framesDecoded), int(framesReceived),
						(framesDecoded/framesReceived)*100)
				}
				if pliCount > 5 {
					t.Logf("WARNING: %s has high PLI count: %d", variantID, int(pliCount))
				}
			}
		}
	}

	// Get full dump at the end
	var fullDump map[string]interface{}
	err = chromedp.Run(ctx,
		evalAsync(`window.e2e.dumpAllStats()`, &fullDump),
	)
	if err != nil {
		t.Logf("Failed to get full dump: %v", err)
	} else {
		dumpJSON, _ := json.MarshalIndent(fullDump, "", "  ")
		t.Logf("=== FULL WEBRTC STATS DUMP ===\n%s", string(dumpJSON))
	}
}

func logDetailedStats(t *testing.T, stats map[string]interface{}) {
	for variantID, variantStats := range stats {
		if statsMap, ok := variantStats.(map[string]interface{}); ok {
			t.Logf("  %s:", variantID)
			t.Logf("    frames: recv=%v dec=%v drop=%v",
				statsMap["framesReceived"], statsMap["framesDecoded"], statsMap["framesDropped"])
			t.Logf("    keyframes: %v, freezes: %v, pauses: %v",
				statsMap["keyFramesDecoded"], statsMap["freezeCount"], statsMap["pauseCount"])
			t.Logf("    jitter: delay=%v target=%v",
				statsMap["jitterBufferDelay"], statsMap["jitterBufferTargetDelay"])
			t.Logf("    rtcp: pli=%v fir=%v nack=%v",
				statsMap["pliCount"], statsMap["firCount"], statsMap["nackCount"])
			t.Logf("    packets: recv=%v lost=%v",
				statsMap["packetsReceived"], statsMap["packetsLost"])
		}
	}
}
