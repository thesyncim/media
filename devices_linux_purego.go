//go:build linux && !nodevices && !cgo

package media

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	// V4L2 library state
	v4l2Once    sync.Once
	v4l2Handle  uintptr
	v4l2InitErr error
	v4l2Loaded  bool

	// ALSA library state
	alsaOnce    sync.Once
	alsaHandle  uintptr
	alsaInitErr error
	alsaLoaded  bool
)

// V4L2 function pointers
var (
	streamV4L2DeviceCount      func() int32
	streamV4L2DevicePath       func(index int32) uintptr
	streamV4L2DeviceName       func(index int32) uintptr
	streamV4L2FreeString       func(ptr uintptr)
	streamV4L2CaptureCreate    func(devicePath uintptr, width, height, fps int32, callback, userData uintptr) uint64
	streamV4L2CaptureStart     func(handle uint64) int32
	streamV4L2CaptureStop      func(handle uint64) int32
	streamV4L2CaptureDestroy   func(handle uint64)
	streamV4L2CaptureGetWidth  func(handle uint64) int32
	streamV4L2CaptureGetHeight func(handle uint64) int32
	streamV4L2CaptureGetFPS    func(handle uint64) int32
	streamV4L2GetError         func() uintptr
)

// ALSA function pointers
var (
	streamALSAInputDeviceCount     func() int32
	streamALSAInputDeviceID        func(index int32) uintptr
	streamALSAInputDeviceName      func(index int32) uintptr
	streamALSAFreeString           func(ptr uintptr)
	streamALSACaptureCreate        func(deviceID uintptr, sampleRate, channels int32, callback, userData uintptr) uint64
	streamALSACaptureStart         func(handle uint64) int32
	streamALSACaptureStop          func(handle uint64) int32
	streamALSACaptureDestroy       func(handle uint64)
	streamALSACaptureGetSampleRate func(handle uint64) int32
	streamALSACaptureGetChannels   func(handle uint64) int32
	streamALSAGetError             func() uintptr
)

// findLibrary searches for a library in common locations
func findLibrary(libName string) string {
	searchPaths := []string{
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
		"/usr/lib",
	)

	for _, p := range searchPaths {
		if p == "" {
			continue
		}
		candidate := filepath.Join(p, libName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	return ""
}

func initV4L2() {
	v4l2Once.Do(func() {
		libPath := findLibrary("libstream_v4l2.so")
		if libPath == "" {
			v4l2InitErr = fmt.Errorf("libstream_v4l2.so not found")
			return
		}

		var err error
		v4l2Handle, err = purego.Dlopen(libPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			v4l2InitErr = fmt.Errorf("failed to load %s: %w", libPath, err)
			return
		}

		// Load function pointers
		purego.RegisterLibFunc(&streamV4L2DeviceCount, v4l2Handle, "stream_v4l2_device_count")
		purego.RegisterLibFunc(&streamV4L2DevicePath, v4l2Handle, "stream_v4l2_device_path")
		purego.RegisterLibFunc(&streamV4L2DeviceName, v4l2Handle, "stream_v4l2_device_name")
		purego.RegisterLibFunc(&streamV4L2FreeString, v4l2Handle, "stream_v4l2_free_string")
		purego.RegisterLibFunc(&streamV4L2CaptureCreate, v4l2Handle, "stream_v4l2_capture_create")
		purego.RegisterLibFunc(&streamV4L2CaptureStart, v4l2Handle, "stream_v4l2_capture_start")
		purego.RegisterLibFunc(&streamV4L2CaptureStop, v4l2Handle, "stream_v4l2_capture_stop")
		purego.RegisterLibFunc(&streamV4L2CaptureDestroy, v4l2Handle, "stream_v4l2_capture_destroy")
		purego.RegisterLibFunc(&streamV4L2CaptureGetWidth, v4l2Handle, "stream_v4l2_capture_get_width")
		purego.RegisterLibFunc(&streamV4L2CaptureGetHeight, v4l2Handle, "stream_v4l2_capture_get_height")
		purego.RegisterLibFunc(&streamV4L2CaptureGetFPS, v4l2Handle, "stream_v4l2_capture_get_fps")
		purego.RegisterLibFunc(&streamV4L2GetError, v4l2Handle, "stream_v4l2_get_error")

		v4l2Loaded = true
	})
}

func initALSA() {
	alsaOnce.Do(func() {
		libPath := findLibrary("libstream_alsa.so")
		if libPath == "" {
			alsaInitErr = fmt.Errorf("libstream_alsa.so not found")
			return
		}

		var err error
		alsaHandle, err = purego.Dlopen(libPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			alsaInitErr = fmt.Errorf("failed to load %s: %w", libPath, err)
			return
		}

		// Load function pointers
		purego.RegisterLibFunc(&streamALSAInputDeviceCount, alsaHandle, "stream_alsa_input_device_count")
		purego.RegisterLibFunc(&streamALSAInputDeviceID, alsaHandle, "stream_alsa_input_device_id")
		purego.RegisterLibFunc(&streamALSAInputDeviceName, alsaHandle, "stream_alsa_input_device_name")
		purego.RegisterLibFunc(&streamALSAFreeString, alsaHandle, "stream_alsa_free_string")
		purego.RegisterLibFunc(&streamALSACaptureCreate, alsaHandle, "stream_alsa_capture_create")
		purego.RegisterLibFunc(&streamALSACaptureStart, alsaHandle, "stream_alsa_capture_start")
		purego.RegisterLibFunc(&streamALSACaptureStop, alsaHandle, "stream_alsa_capture_stop")
		purego.RegisterLibFunc(&streamALSACaptureDestroy, alsaHandle, "stream_alsa_capture_destroy")
		purego.RegisterLibFunc(&streamALSACaptureGetSampleRate, alsaHandle, "stream_alsa_capture_get_sample_rate")
		purego.RegisterLibFunc(&streamALSACaptureGetChannels, alsaHandle, "stream_alsa_capture_get_channels")
		purego.RegisterLibFunc(&streamALSAGetError, alsaHandle, "stream_alsa_get_error")

		alsaLoaded = true
	})
}

// IsV4L2Available returns true if V4L2 library is available.
func IsV4L2Available() bool {
	initV4L2()
	return v4l2Loaded
}

// IsALSAAvailable returns true if ALSA library is available.
func IsALSAAvailable() bool {
	initALSA()
	return alsaLoaded
}

// LinuxDeviceProvider implements DeviceProvider using V4L2 and ALSA via purego.
type LinuxDeviceProvider struct {
	mu sync.RWMutex
}

// NewLinuxDeviceProvider creates a new Linux-based device provider.
func NewLinuxDeviceProvider() *LinuxDeviceProvider {
	initV4L2()
	initALSA()
	return &LinuxDeviceProvider{}
}

func ptrToStringLinux(ptr uintptr) string {
	if ptr == 0 {
		return ""
	}
	return goStringFromPtr(ptr)
}

// ListVideoDevices returns available video input devices (cameras).
func (p *LinuxDeviceProvider) ListVideoDevices(ctx context.Context) ([]DeviceInfo, error) {
	if !v4l2Loaded {
		return nil, fmt.Errorf("V4L2 not available: %v", v4l2InitErr)
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	count := streamV4L2DeviceCount()
	devices := make([]DeviceInfo, 0, count)

	for i := int32(0); i < count; i++ {
		pathPtr := streamV4L2DevicePath(i)
		namePtr := streamV4L2DeviceName(i)

		if pathPtr != 0 && namePtr != 0 {
			devices = append(devices, DeviceInfo{
				DeviceID: ptrToStringLinux(pathPtr),
				Label:    ptrToStringLinux(namePtr),
				Kind:     DeviceKindVideoInput,
			})
			streamV4L2FreeString(pathPtr)
			streamV4L2FreeString(namePtr)
		}
	}

	return devices, nil
}

// ListAudioInputDevices returns available audio input devices (microphones).
func (p *LinuxDeviceProvider) ListAudioInputDevices(ctx context.Context) ([]DeviceInfo, error) {
	if !alsaLoaded {
		return nil, fmt.Errorf("ALSA not available: %v", alsaInitErr)
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	count := streamALSAInputDeviceCount()
	devices := make([]DeviceInfo, 0, count)

	for i := int32(0); i < count; i++ {
		idPtr := streamALSAInputDeviceID(i)
		namePtr := streamALSAInputDeviceName(i)

		if idPtr != 0 && namePtr != 0 {
			devices = append(devices, DeviceInfo{
				DeviceID: ptrToStringLinux(idPtr),
				Label:    ptrToStringLinux(namePtr),
				Kind:     DeviceKindAudioInput,
			})
			streamALSAFreeString(idPtr)
			streamALSAFreeString(namePtr)
		}
	}

	return devices, nil
}

// ListAudioOutputDevices returns available audio output devices.
func (p *LinuxDeviceProvider) ListAudioOutputDevices(ctx context.Context) ([]DeviceInfo, error) {
	// Not implemented yet - would need PulseAudio or similar for full output device enumeration
	return []DeviceInfo{}, nil
}

// V4L2VideoCapture wraps a V4L2 video capture session.
type V4L2VideoCapture struct {
	handle     uint64
	callback   VideoFrameCallback
	config     SourceConfig
	running    bool
	frameCh    chan *VideoFrame
	mu         sync.RWMutex
	bufferPool *BufferPool
}

// Active captures map for callback routing
var activeLinuxCaptures sync.Map // handle -> *V4L2VideoCapture

//export goV4L2FrameCallback
func goV4L2FrameCallback(
	yPlane uintptr, yStride int32,
	uPlane uintptr, uStride int32,
	vPlane uintptr, vStride int32,
	width, height int32,
	timestampNs int64,
	userData uintptr,
) {
	capture, ok := activeLinuxCaptures.Load(userData)
	if !ok {
		return
	}

	cap := capture.(*V4L2VideoCapture)
	cap.mu.RLock()
	cb := cap.callback
	cap.mu.RUnlock()

	if cb == nil {
		return
	}

	// Create frame from callback data
	ySize := int(yStride) * int(height)
	uvHeight := int(height) / 2
	uSize := int(uStride) * uvHeight
	vSize := int(vStride) * uvHeight

	frame := &VideoFrame{
		Data: [][]byte{
			unsafe.Slice((*byte)(unsafe.Pointer(yPlane)), ySize),
			unsafe.Slice((*byte)(unsafe.Pointer(uPlane)), uSize),
			unsafe.Slice((*byte)(unsafe.Pointer(vPlane)), vSize),
		},
		Stride:    []int{int(yStride), int(uStride), int(vStride)},
		Width:     int(width),
		Height:    int(height),
		Format:    PixelFormatI420,
		Timestamp: timestampNs,
	}

	cb(frame)
}

// OpenVideoDevice opens a video capture device.
func (p *LinuxDeviceProvider) OpenVideoDevice(ctx context.Context, deviceID string, constraints *VideoConstraints) (VideoTrack, error) {
	if !v4l2Loaded {
		return nil, fmt.Errorf("V4L2 not available: %v", v4l2InitErr)
	}

	// TODO: Implement full video capture with purego callbacks
	// This is complex because we need to pass Go callbacks to C
	// For now, return an error indicating work in progress
	_ = constraints // Will be used when capture is implemented
	return nil, fmt.Errorf("video capture via purego not yet fully implemented (requires callback bridge)")
}

// OpenAudioDevice opens an audio capture device.
func (p *LinuxDeviceProvider) OpenAudioDevice(ctx context.Context, deviceID string, constraints *AudioConstraints) (AudioTrack, error) {
	if !alsaLoaded {
		return nil, fmt.Errorf("ALSA not available: %v", alsaInitErr)
	}

	return nil, fmt.Errorf("audio capture not yet implemented")
}

// CaptureDisplay captures the screen/window.
func (p *LinuxDeviceProvider) CaptureDisplay(ctx context.Context, options DisplayVideoOptions) (VideoTrack, error) {
	return nil, fmt.Errorf("display capture not yet implemented")
}

// CaptureDisplayAudio captures display audio.
func (p *LinuxDeviceProvider) CaptureDisplayAudio(ctx context.Context) (AudioTrack, error) {
	return nil, fmt.Errorf("display audio capture not yet implemented")
}

func init() {
	// Only register on Linux
	if runtime.GOOS == "linux" {
		initV4L2()
		initALSA()
		// Register provider if at least one library is available
		if v4l2Loaded || alsaLoaded {
			RegisterDeviceProvider(NewLinuxDeviceProvider())
		}
	}
}
