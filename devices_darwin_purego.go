//go:build darwin && !nodevices

package media

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// AVFoundation permission status values
const (
	AVAuthorizationStatusNotDetermined = 0
	AVAuthorizationStatusRestricted    = 1
	AVAuthorizationStatusDenied        = 2
	AVAuthorizationStatusAuthorized    = 3
)

var (
	avfOnce    sync.Once
	avfHandle  uintptr
	avfInitErr error
	avfLoaded  bool
)

// libmedia_avfoundation function pointers
var (
	mediaAVVideoDeviceCount           func() int32
	mediaAVAudioInputDeviceCount      func() int32
	mediaAVVideoDeviceID              func(index int32) uintptr
	mediaAVVideoDeviceLabel           func(index int32) uintptr
	mediaAVAudioInputDeviceID         func(index int32) uintptr
	mediaAVAudioInputDeviceLabel      func(index int32) uintptr
	mediaAVFreeString                 func(ptr uintptr)
	mediaAVCameraPermissionStatus     func() int32
	mediaAVMicrophonePermissionStatus func() int32
	mediaAVRequestCameraPermission    func()
	mediaAVRequestMicPermission       func()
	mediaAVVideoCaptureCreate         func(deviceID uintptr, width, height, fps int32, callback, userData uintptr) uint64
	mediaAVVideoCaptureStart          func(handle uint64) int32
	mediaAVVideoCaptureStop           func(handle uint64) int32
	mediaAVVideoCaptureDestroy        func(handle uint64)
	mediaAVGetError                   func() uintptr
	mediaAVVideoDeviceFPSRange        func(deviceID uintptr, minFPS, maxFPS uintptr) int32
)

func initAVFoundation() {
	avfOnce.Do(func() {
		// Try to find the library
		libName := "libmedia_avfoundation.dylib"
		searchPaths := []string{
			os.Getenv("MEDIA_AV_LIB_PATH"),
			os.Getenv("MEDIA_SDK_LIB_PATH"),
		}

		// Add relative paths
		if exe, err := os.Executable(); err == nil {
			searchPaths = append(searchPaths, filepath.Dir(exe))
		}
		searchPaths = append(searchPaths,
			"build",
			"build/ffi",
			"../build",
			"../build/ffi",
			"../../build",
			"../../build/ffi",
			"../../../build",
			"../../../build/ffi",
			"../../../../build",
			"../../../../build/ffi",
			"/usr/local/lib",
		)

		var libPath string
		for _, p := range searchPaths {
			if p == "" {
				continue
			}
			candidate := filepath.Join(p, libName)
			if _, err := os.Stat(candidate); err == nil {
				libPath = candidate
				break
			}
		}

		if libPath == "" {
			avfInitErr = fmt.Errorf("libmedia_avfoundation.dylib not found")
			return
		}

		var err error
		avfHandle, err = purego.Dlopen(libPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			avfInitErr = fmt.Errorf("failed to load %s: %w", libPath, err)
			return
		}

		// Load function pointers
		purego.RegisterLibFunc(&mediaAVVideoDeviceCount, avfHandle, "media_av_video_device_count")
		purego.RegisterLibFunc(&mediaAVAudioInputDeviceCount, avfHandle, "media_av_audio_input_device_count")
		purego.RegisterLibFunc(&mediaAVVideoDeviceID, avfHandle, "media_av_video_device_id")
		purego.RegisterLibFunc(&mediaAVVideoDeviceLabel, avfHandle, "media_av_video_device_label")
		purego.RegisterLibFunc(&mediaAVAudioInputDeviceID, avfHandle, "media_av_audio_input_device_id")
		purego.RegisterLibFunc(&mediaAVAudioInputDeviceLabel, avfHandle, "media_av_audio_input_device_label")
		purego.RegisterLibFunc(&mediaAVFreeString, avfHandle, "media_av_free_string")
		purego.RegisterLibFunc(&mediaAVCameraPermissionStatus, avfHandle, "media_av_camera_permission_status")
		purego.RegisterLibFunc(&mediaAVMicrophonePermissionStatus, avfHandle, "media_av_microphone_permission_status")
		purego.RegisterLibFunc(&mediaAVRequestCameraPermission, avfHandle, "media_av_request_camera_permission")
		purego.RegisterLibFunc(&mediaAVRequestMicPermission, avfHandle, "media_av_request_microphone_permission")
		purego.RegisterLibFunc(&mediaAVVideoCaptureCreate, avfHandle, "media_av_video_capture_create")
		purego.RegisterLibFunc(&mediaAVVideoCaptureStart, avfHandle, "media_av_video_capture_start")
		purego.RegisterLibFunc(&mediaAVVideoCaptureStop, avfHandle, "media_av_video_capture_stop")
		purego.RegisterLibFunc(&mediaAVVideoCaptureDestroy, avfHandle, "media_av_video_capture_destroy")
		purego.RegisterLibFunc(&mediaAVGetError, avfHandle, "media_av_get_error")
		purego.RegisterLibFunc(&mediaAVVideoDeviceFPSRange, avfHandle, "media_av_video_device_fps_range")

		avfLoaded = true
	})
}

// IsAVFoundationAvailable returns true if AVFoundation library is available.
func IsAVFoundationAvailable() bool {
	initAVFoundation()
	return avfLoaded
}

// AVFoundationProvider implements DeviceProvider using macOS AVFoundation via purego.
type AVFoundationProvider struct {
	mu sync.RWMutex
}

// NewAVFoundationProvider creates a new AVFoundation-based device provider.
func NewAVFoundationProvider() *AVFoundationProvider {
	initAVFoundation()
	return &AVFoundationProvider{}
}

func ptrToString(ptr uintptr) string {
	if ptr == 0 {
		return ""
	}
	return goStringFromPtr(ptr)
}

// ListVideoDevices returns available video input devices (cameras).
func (p *AVFoundationProvider) ListVideoDevices(ctx context.Context) ([]DeviceInfo, error) {
	if !avfLoaded {
		return nil, fmt.Errorf("AVFoundation not available: %v", avfInitErr)
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	count := mediaAVVideoDeviceCount()
	devices := make([]DeviceInfo, 0, count)

	for i := int32(0); i < count; i++ {
		idPtr := mediaAVVideoDeviceID(i)
		labelPtr := mediaAVVideoDeviceLabel(i)

		if idPtr != 0 && labelPtr != 0 {
			devices = append(devices, DeviceInfo{
				DeviceID: ptrToString(idPtr),
				Label:    ptrToString(labelPtr),
				Kind:     DeviceKindVideoInput,
			})
			mediaAVFreeString(idPtr)
			mediaAVFreeString(labelPtr)
		}
	}

	return devices, nil
}

// ListAudioInputDevices returns available audio input devices (microphones).
func (p *AVFoundationProvider) ListAudioInputDevices(ctx context.Context) ([]DeviceInfo, error) {
	if !avfLoaded {
		return nil, fmt.Errorf("AVFoundation not available: %v", avfInitErr)
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	count := mediaAVAudioInputDeviceCount()
	devices := make([]DeviceInfo, 0, count)

	for i := int32(0); i < count; i++ {
		idPtr := mediaAVAudioInputDeviceID(i)
		labelPtr := mediaAVAudioInputDeviceLabel(i)

		if idPtr != 0 && labelPtr != 0 {
			devices = append(devices, DeviceInfo{
				DeviceID: ptrToString(idPtr),
				Label:    ptrToString(labelPtr),
				Kind:     DeviceKindAudioInput,
			})
			mediaAVFreeString(idPtr)
			mediaAVFreeString(labelPtr)
		}
	}

	return devices, nil
}

// ListAudioOutputDevices returns available audio output devices.
func (p *AVFoundationProvider) ListAudioOutputDevices(ctx context.Context) ([]DeviceInfo, error) {
	// AVFoundation focuses on capture; would need CoreAudio for outputs
	return []DeviceInfo{}, nil
}

// CameraPermissionStatus returns the current camera permission status.
func CameraPermissionStatus() int {
	initAVFoundation()
	if !avfLoaded {
		return AVAuthorizationStatusNotDetermined
	}
	return int(mediaAVCameraPermissionStatus())
}

// MicrophonePermissionStatus returns the current microphone permission status.
func MicrophonePermissionStatus() int {
	initAVFoundation()
	if !avfLoaded {
		return AVAuthorizationStatusNotDetermined
	}
	return int(mediaAVMicrophonePermissionStatus())
}

// GetVideoDeviceFPSRange returns the min and max FPS supported by a video device.
// Returns (0, 0, error) if the device is not found or FPS range cannot be determined.
func GetVideoDeviceFPSRange(deviceID string) (minFPS, maxFPS int, err error) {
	initAVFoundation()
	if !avfLoaded {
		return 0, 0, fmt.Errorf("AVFoundation not available: %v", avfInitErr)
	}

	var deviceIDPtr uintptr
	if deviceID != "" {
		deviceIDBytes := append([]byte(deviceID), 0)
		deviceIDPtr = uintptr(unsafe.Pointer(&deviceIDBytes[0]))
	}

	var minVal, maxVal int32
	result := mediaAVVideoDeviceFPSRange(
		deviceIDPtr,
		uintptr(unsafe.Pointer(&minVal)),
		uintptr(unsafe.Pointer(&maxVal)),
	)

	if result != 0 {
		errPtr := mediaAVGetError()
		errMsg := "unknown error"
		if errPtr != 0 {
			errMsg = goStringFromPtr(errPtr)
		}
		return 0, 0, fmt.Errorf("failed to get FPS range: %s", errMsg)
	}

	return int(minVal), int(maxVal), nil
}

// RequestCameraPermission requests camera permission (async).
func RequestCameraPermission() {
	initAVFoundation()
	if avfLoaded {
		mediaAVRequestCameraPermission()
	}
}

// RequestMicrophonePermission requests microphone permission (async).
func RequestMicrophonePermission() {
	initAVFoundation()
	if avfLoaded {
		mediaAVRequestMicPermission()
	}
}

// Global callback state for purego
var (
	avCapturesMu     sync.RWMutex
	avCaptures       = make(map[uintptr]*AVVideoCaptureTrack)
	avCaptureCounter uintptr
	frameCallback    uintptr
	callbackOnce     sync.Once
)

// initFrameCallback initializes the purego callback once
func initFrameCallback() {
	callbackOnce.Do(func() {
		frameCallback = purego.NewCallback(avFrameCallbackHandler)
	})
}

// avFrameCallbackHandler is the callback function called by C code
func avFrameCallbackHandler(
	yPlane uintptr, yStride int32,
	uPlane uintptr, uStride int32,
	vPlane uintptr, vStride int32,
	width, height int32,
	timestampNs int64,
	userData uintptr,
) {
	avCapturesMu.RLock()
	capture, ok := avCaptures[userData]
	avCapturesMu.RUnlock()

	if !ok || capture == nil {
		return
	}

	capture.handleFrame(yPlane, yStride, uPlane, uStride, vPlane, vStride, width, height, timestampNs)
}

// AVVideoCaptureTrack implements VideoTrack for AVFoundation camera capture
type AVVideoCaptureTrack struct {
	id         string
	deviceID   string
	label      string
	handle     uint64
	captureID  uintptr
	width      int
	height     int
	fps        int
	state      atomic.Int32
	muted      atomic.Bool
	enabled    atomic.Bool
	callback   VideoFrameCallback
	endedCb    func()
	frameCh    chan *VideoFrame
	mu         sync.RWMutex
	bufferPool *BufferPool
}

func (t *AVVideoCaptureTrack) handleFrame(
	yPlane uintptr, yStride int32,
	uPlane uintptr, uStride int32,
	vPlane uintptr, vStride int32,
	width, height int32,
	timestampNs int64,
) {
	// Check state early to avoid work for ended tracks
	if t.state.Load() == int32(TrackStateEnded) {
		return
	}
	if t.muted.Load() || !t.enabled.Load() {
		return
	}

	// Create frame from callback data
	ySize := int(yStride) * int(height)
	uvHeight := int(height) / 2
	uSize := int(uStride) * uvHeight
	vSize := int(vStride) * uvHeight

	// Copy frame data to avoid referencing C memory after callback returns
	yData := make([]byte, ySize)
	uData := make([]byte, uSize)
	vData := make([]byte, vSize)

	copy(yData, unsafe.Slice((*byte)(unsafe.Pointer(yPlane)), ySize))
	copy(uData, unsafe.Slice((*byte)(unsafe.Pointer(uPlane)), uSize))
	copy(vData, unsafe.Slice((*byte)(unsafe.Pointer(vPlane)), vSize))

	frame := &VideoFrame{
		Data:      [][]byte{yData, uData, vData},
		Stride:    []int{int(yStride), int(uStride), int(vStride)},
		Width:     int(width),
		Height:    int(height),
		Format:    PixelFormatI420,
		Timestamp: timestampNs,
	}

	t.mu.RLock()
	cb := t.callback
	ch := t.frameCh
	t.mu.RUnlock()

	// Call callback if set
	if cb != nil {
		cb(frame)
	}

	// Send to channel if available (non-blocking)
	if ch != nil {
		select {
		case ch <- frame:
		default:
			// Drop frame if channel is full
		}
	}
}

// VideoTrack interface implementation

func (t *AVVideoCaptureTrack) ID() string {
	return t.id
}

func (t *AVVideoCaptureTrack) Kind() webrtc.RTPCodecType {
	return webrtc.RTPCodecTypeVideo
}

func (t *AVVideoCaptureTrack) Label() string {
	return t.label
}

func (t *AVVideoCaptureTrack) State() TrackState {
	return TrackState(t.state.Load())
}

func (t *AVVideoCaptureTrack) Muted() bool {
	return t.muted.Load()
}

func (t *AVVideoCaptureTrack) SetMuted(muted bool) {
	t.muted.Store(muted)
}

func (t *AVVideoCaptureTrack) Enabled() bool {
	return t.enabled.Load()
}

func (t *AVVideoCaptureTrack) SetEnabled(enabled bool) {
	t.enabled.Store(enabled)
}

func (t *AVVideoCaptureTrack) Constraints() TrackConstraints {
	return TrackConstraints{
		Width:     t.width,
		Height:    t.height,
		FrameRate: t.fps,
		DeviceID:  t.deviceID,
	}
}

func (t *AVVideoCaptureTrack) ApplyConstraints(constraints TrackConstraints) error {
	return errors.New("applying constraints not supported on AVFoundation capture")
}

func (t *AVVideoCaptureTrack) Clone() (MediaStreamTrack, error) {
	return nil, errors.New("cloning not supported on AVFoundation capture")
}

func (t *AVVideoCaptureTrack) OnEnded(callback func()) {
	t.mu.Lock()
	t.endedCb = callback
	t.mu.Unlock()
}

func (t *AVVideoCaptureTrack) ReadFrame(ctx context.Context) (*VideoFrame, error) {
	t.mu.RLock()
	ch := t.frameCh
	t.mu.RUnlock()

	if ch == nil {
		return nil, errors.New("frame channel not initialized")
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case frame, ok := <-ch:
		if !ok {
			return nil, errors.New("track closed")
		}
		return frame, nil
	}
}

func (t *AVVideoCaptureTrack) OnFrame(callback VideoFrameCallback) {
	t.mu.Lock()
	t.callback = callback
	t.mu.Unlock()
}

func (t *AVVideoCaptureTrack) Settings() VideoTrackSettings {
	return VideoTrackSettings{
		Width:     t.width,
		Height:    t.height,
		FrameRate: t.fps,
		DeviceID:  t.deviceID,
	}
}

func (t *AVVideoCaptureTrack) Close() error {
	// Set state first to signal callbacks to stop processing
	t.state.Store(int32(TrackStateEnded))

	if t.handle != 0 {
		mediaAVVideoCaptureStop(t.handle)
		mediaAVVideoCaptureDestroy(t.handle)
		t.handle = 0
	}

	// Remove from active captures - this prevents new callbacks from finding us
	avCapturesMu.Lock()
	delete(avCaptures, t.captureID)
	avCapturesMu.Unlock()

	// Get channel and set to nil atomically under write lock
	// This ensures no new sends happen after we close
	t.mu.Lock()
	endedCb := t.endedCb
	ch := t.frameCh
	t.frameCh = nil // Set to nil before closing to prevent sends
	t.mu.Unlock()

	if ch != nil {
		close(ch)
	}

	if endedCb != nil {
		endedCb()
	}

	return nil
}

// WriteRTP is not supported for capture tracks
func (t *AVVideoCaptureTrack) WriteRTP(p *rtp.Packet) error {
	return errors.New("WriteRTP not supported on capture track")
}

// OpenVideoDevice opens a video capture device.
func (p *AVFoundationProvider) OpenVideoDevice(ctx context.Context, deviceID string, constraints *VideoConstraints) (VideoTrack, error) {
	if !avfLoaded {
		return nil, fmt.Errorf("AVFoundation not available: %v", avfInitErr)
	}

	// Check permission
	status := mediaAVCameraPermissionStatus()
	switch status {
	case AVAuthorizationStatusNotDetermined:
		mediaAVRequestCameraPermission()
		return nil, fmt.Errorf("camera permission not yet determined, please grant permission and try again")
	case AVAuthorizationStatusDenied, AVAuthorizationStatusRestricted:
		return nil, fmt.Errorf("camera permission denied")
	}

	// Initialize callback
	initFrameCallback()

	// Set defaults
	width := 640
	height := 480
	fps := 30
	if constraints != nil {
		if constraints.Width > 0 {
			width = constraints.Width
		}
		if constraints.Height > 0 {
			height = constraints.Height
		}
		if constraints.FrameRate > 0 {
			fps = constraints.FrameRate
		}
	}

	// Generate capture ID for callback routing
	avCapturesMu.Lock()
	avCaptureCounter++
	captureID := avCaptureCounter
	avCapturesMu.Unlock()

	// Convert deviceID to C string pointer
	var deviceIDPtr uintptr
	if deviceID != "" {
		deviceIDBytes := append([]byte(deviceID), 0)
		deviceIDPtr = uintptr(unsafe.Pointer(&deviceIDBytes[0]))
	}

	// Create capture session
	handle := mediaAVVideoCaptureCreate(
		deviceIDPtr,
		int32(width),
		int32(height),
		int32(fps),
		frameCallback,
		captureID,
	)

	if handle == 0 {
		errPtr := mediaAVGetError()
		errMsg := "unknown error"
		if errPtr != 0 {
			errMsg = goStringFromPtr(errPtr)
		}
		return nil, fmt.Errorf("failed to create video capture: %s", errMsg)
	}

	// Create track
	track := &AVVideoCaptureTrack{
		id:        fmt.Sprintf("video-%d", captureID),
		deviceID:  deviceID,
		label:     "AVFoundation Camera",
		handle:    handle,
		captureID: captureID,
		width:     width,
		height:    height,
		fps:       fps,
		frameCh:   make(chan *VideoFrame, 3), // Buffer a few frames
	}
	track.state.Store(int32(TrackStateLive))
	track.enabled.Store(true)

	// Register in active captures map
	avCapturesMu.Lock()
	avCaptures[captureID] = track
	avCapturesMu.Unlock()

	// Start capture
	result := mediaAVVideoCaptureStart(handle)
	if result != 0 {
		track.Close()
		return nil, fmt.Errorf("failed to start video capture")
	}

	return track, nil
}

// OpenAudioDevice opens an audio capture device.
func (p *AVFoundationProvider) OpenAudioDevice(ctx context.Context, deviceID string, constraints *AudioConstraints) (AudioTrack, error) {
	if !avfLoaded {
		return nil, fmt.Errorf("AVFoundation not available: %v", avfInitErr)
	}

	// Check permission
	status := mediaAVMicrophonePermissionStatus()
	switch status {
	case AVAuthorizationStatusNotDetermined:
		mediaAVRequestMicPermission()
		return nil, fmt.Errorf("microphone permission not yet determined, please grant permission and try again")
	case AVAuthorizationStatusDenied, AVAuthorizationStatusRestricted:
		return nil, fmt.Errorf("microphone permission denied")
	}

	return nil, fmt.Errorf("audio capture not yet implemented")
}

// CaptureDisplay captures the screen/window.
func (p *AVFoundationProvider) CaptureDisplay(ctx context.Context, options DisplayVideoOptions) (VideoTrack, error) {
	return nil, fmt.Errorf("display capture not yet implemented")
}

// CaptureDisplayAudio captures display audio.
func (p *AVFoundationProvider) CaptureDisplayAudio(ctx context.Context) (AudioTrack, error) {
	return nil, fmt.Errorf("display audio capture not yet implemented")
}

func init() {
	// Only register on Darwin
	if runtime.GOOS == "darwin" {
		initAVFoundation()
		if avfLoaded {
			RegisterDeviceProvider(NewAVFoundationProvider())
		}
	}
}
