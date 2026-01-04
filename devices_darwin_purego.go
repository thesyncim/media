//go:build darwin && !nodevices && !cgo

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

// libstream_avfoundation function pointers
var (
	streamAVVideoDeviceCount           func() int32
	streamAVAudioInputDeviceCount      func() int32
	streamAVVideoDeviceID              func(index int32) uintptr
	streamAVVideoDeviceLabel           func(index int32) uintptr
	streamAVAudioInputDeviceID         func(index int32) uintptr
	streamAVAudioInputDeviceLabel      func(index int32) uintptr
	streamAVFreeString                 func(ptr uintptr)
	streamAVCameraPermissionStatus     func() int32
	streamAVMicrophonePermissionStatus func() int32
	streamAVRequestCameraPermission    func()
	streamAVRequestMicPermission       func()
	streamAVVideoCaptureCreate         func(deviceID uintptr, width, height, fps int32, callback, userData uintptr) uint64
	streamAVVideoCaptureStart          func(handle uint64) int32
	streamAVVideoCaptureStop           func(handle uint64) int32
	streamAVVideoCaptureDestroy        func(handle uint64)
	streamAVGetError                   func() uintptr
	streamAVVideoDeviceFPSRange        func(deviceID uintptr, minFPS, maxFPS uintptr) int32
)

func initAVFoundation() {
	avfOnce.Do(func() {
		// Try to find the library
		libName := "libstream_avfoundation.dylib"
		searchPaths := []string{
			os.Getenv("STREAM_AV_LIB_PATH"),
			os.Getenv("STREAM_SDK_LIB_PATH"),
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
			avfInitErr = fmt.Errorf("libstream_avfoundation.dylib not found")
			return
		}

		var err error
		avfHandle, err = purego.Dlopen(libPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			avfInitErr = fmt.Errorf("failed to load %s: %w", libPath, err)
			return
		}

		// Load function pointers
		purego.RegisterLibFunc(&streamAVVideoDeviceCount, avfHandle, "stream_av_video_device_count")
		purego.RegisterLibFunc(&streamAVAudioInputDeviceCount, avfHandle, "stream_av_audio_input_device_count")
		purego.RegisterLibFunc(&streamAVVideoDeviceID, avfHandle, "stream_av_video_device_id")
		purego.RegisterLibFunc(&streamAVVideoDeviceLabel, avfHandle, "stream_av_video_device_label")
		purego.RegisterLibFunc(&streamAVAudioInputDeviceID, avfHandle, "stream_av_audio_input_device_id")
		purego.RegisterLibFunc(&streamAVAudioInputDeviceLabel, avfHandle, "stream_av_audio_input_device_label")
		purego.RegisterLibFunc(&streamAVFreeString, avfHandle, "stream_av_free_string")
		purego.RegisterLibFunc(&streamAVCameraPermissionStatus, avfHandle, "stream_av_camera_permission_status")
		purego.RegisterLibFunc(&streamAVMicrophonePermissionStatus, avfHandle, "stream_av_microphone_permission_status")
		purego.RegisterLibFunc(&streamAVRequestCameraPermission, avfHandle, "stream_av_request_camera_permission")
		purego.RegisterLibFunc(&streamAVRequestMicPermission, avfHandle, "stream_av_request_microphone_permission")
		purego.RegisterLibFunc(&streamAVVideoCaptureCreate, avfHandle, "stream_av_video_capture_create")
		purego.RegisterLibFunc(&streamAVVideoCaptureStart, avfHandle, "stream_av_video_capture_start")
		purego.RegisterLibFunc(&streamAVVideoCaptureStop, avfHandle, "stream_av_video_capture_stop")
		purego.RegisterLibFunc(&streamAVVideoCaptureDestroy, avfHandle, "stream_av_video_capture_destroy")
		purego.RegisterLibFunc(&streamAVGetError, avfHandle, "stream_av_get_error")
		purego.RegisterLibFunc(&streamAVVideoDeviceFPSRange, avfHandle, "stream_av_video_device_fps_range")

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

	count := streamAVVideoDeviceCount()
	devices := make([]DeviceInfo, 0, count)

	for i := int32(0); i < count; i++ {
		idPtr := streamAVVideoDeviceID(i)
		labelPtr := streamAVVideoDeviceLabel(i)

		if idPtr != 0 && labelPtr != 0 {
			devices = append(devices, DeviceInfo{
				DeviceID: ptrToString(idPtr),
				Label:    ptrToString(labelPtr),
				Kind:     DeviceKindVideoInput,
			})
			streamAVFreeString(idPtr)
			streamAVFreeString(labelPtr)
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

	count := streamAVAudioInputDeviceCount()
	devices := make([]DeviceInfo, 0, count)

	for i := int32(0); i < count; i++ {
		idPtr := streamAVAudioInputDeviceID(i)
		labelPtr := streamAVAudioInputDeviceLabel(i)

		if idPtr != 0 && labelPtr != 0 {
			devices = append(devices, DeviceInfo{
				DeviceID: ptrToString(idPtr),
				Label:    ptrToString(labelPtr),
				Kind:     DeviceKindAudioInput,
			})
			streamAVFreeString(idPtr)
			streamAVFreeString(labelPtr)
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
	return int(streamAVCameraPermissionStatus())
}

// MicrophonePermissionStatus returns the current microphone permission status.
func MicrophonePermissionStatus() int {
	initAVFoundation()
	if !avfLoaded {
		return AVAuthorizationStatusNotDetermined
	}
	return int(streamAVMicrophonePermissionStatus())
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
	result := streamAVVideoDeviceFPSRange(
		deviceIDPtr,
		uintptr(unsafe.Pointer(&minVal)),
		uintptr(unsafe.Pointer(&maxVal)),
	)

	if result != 0 {
		errPtr := streamAVGetError()
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
		streamAVRequestCameraPermission()
	}
}

// RequestMicrophonePermission requests microphone permission (async).
func RequestMicrophonePermission() {
	initAVFoundation()
	if avfLoaded {
		streamAVRequestMicPermission()
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
	if t.handle != 0 {
		streamAVVideoCaptureStop(t.handle)
		streamAVVideoCaptureDestroy(t.handle)
		t.handle = 0
	}

	// Remove from active captures
	avCapturesMu.Lock()
	delete(avCaptures, t.captureID)
	avCapturesMu.Unlock()

	t.state.Store(int32(TrackStateEnded))

	t.mu.RLock()
	endedCb := t.endedCb
	ch := t.frameCh
	t.mu.RUnlock()

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
	status := streamAVCameraPermissionStatus()
	switch status {
	case AVAuthorizationStatusNotDetermined:
		streamAVRequestCameraPermission()
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
	handle := streamAVVideoCaptureCreate(
		deviceIDPtr,
		int32(width),
		int32(height),
		int32(fps),
		frameCallback,
		captureID,
	)

	if handle == 0 {
		errPtr := streamAVGetError()
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
	result := streamAVVideoCaptureStart(handle)
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
	status := streamAVMicrophonePermissionStatus()
	switch status {
	case AVAuthorizationStatusNotDetermined:
		streamAVRequestMicPermission()
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
