//go:build (darwin || linux) && !cgo

// Package media provides video compositing via libstream_compositor using purego.

package media

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	streamCompositorOnce    sync.Once
	streamCompositorHandle  uintptr
	streamCompositorInitErr error
	streamCompositorLoaded  bool
)

// libstream_compositor function pointers
var (
	streamCompositorCreate     func(width, height int32) uintptr
	streamCompositorDestroy    func(comp uintptr)
	streamCompositorClear      func(comp uintptr, y, u, v uint8)
	streamCompositorBlendLayer func(comp uintptr, srcY, srcU, srcV, srcA uintptr, srcW, srcH, srcStrideY, srcStrideUV int32, config uintptr)
	streamCompositorGetResult  func(comp uintptr, outY, outU, outV, outStrideY, outStrideUV uintptr)
	streamCompositorGetSize    func(comp uintptr, width, height uintptr)
)

// StreamLayerConfig matches the C struct for layer configuration.
type streamLayerConfigC struct {
	X         int32
	Y         int32
	Width     int32
	Height    int32
	ZOrder    int32
	Alpha     float32
	Visible   int32
	BlendMode int32
}

// loadStreamCompositor loads the libstream_compositor shared library.
func loadStreamCompositor() error {
	streamCompositorOnce.Do(func() {
		streamCompositorInitErr = loadStreamCompositorLib()
		if streamCompositorInitErr == nil {
			streamCompositorLoaded = true
		}
	})
	return streamCompositorInitErr
}

func loadStreamCompositorLib() error {
	paths := getStreamCompositorLibPaths()

	var lastErr error
	for _, path := range paths {
		handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err == nil {
			streamCompositorHandle = handle
			if err := loadStreamCompositorSymbols(); err != nil {
				purego.Dlclose(handle)
				lastErr = err
				continue
			}
			return nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return fmt.Errorf("failed to load libstream_compositor: %w", lastErr)
	}
	return errors.New("libstream_compositor not found in any standard location")
}

func getStreamCompositorLibPaths() []string {
	var paths []string

	libName := "libstream_compositor.so"
	if runtime.GOOS == "darwin" {
		libName = "libstream_compositor.dylib"
	}

	// Environment variable overrides
	if envPath := os.Getenv("STREAM_COMPOSITOR_LIB_PATH"); envPath != "" {
		paths = append(paths, envPath)
	}
	if envPath := os.Getenv("STREAM_SDK_LIB_PATH"); envPath != "" {
		paths = append(paths, filepath.Join(envPath, libName))
	}

	// Try to find based on executable location
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		paths = append(paths,
			filepath.Join(exeDir, libName),
			filepath.Join(exeDir, "..", "lib", libName),
			filepath.Join(exeDir, "..", "..", "build", libName),
		)
	}

	// Try to find based on working directory
	if wd, err := os.Getwd(); err == nil {
		paths = append(paths,
			filepath.Join(wd, "build", libName),
			filepath.Join(wd, "..", "build", libName),
			filepath.Join(wd, "..", "..", "build", libName),
			filepath.Join(wd, "..", "..", "..", "build", libName),
			filepath.Join(wd, "..", "..", "..", "..", "build", libName),
		)
	}

	// Try source root (uses runtime.Caller - works in IDE/tests)
	if root := findSourceRoot(); root != "" {
		paths = append(paths, filepath.Join(root, "build", libName))
	}

	// Try module root
	if root := findModuleRoot(); root != "" {
		paths = append(paths, filepath.Join(root, "build", libName))
	}

	// Development paths - relative
	devPaths := []string{
		"build",
		"../../build",
		"../../../../build",
	}
	for _, devPath := range devPaths {
		paths = append(paths, filepath.Join(devPath, libName))
	}

	// System paths
	switch runtime.GOOS {
	case "darwin":
		paths = append(paths,
			libName,
			"/usr/local/lib/"+libName,
			"/opt/homebrew/lib/"+libName,
		)
	case "linux":
		paths = append(paths,
			libName,
			"/usr/local/lib/"+libName,
			"/usr/lib/"+libName,
		)
	}

	return paths
}

func loadStreamCompositorSymbols() error {
	purego.RegisterLibFunc(&streamCompositorCreate, streamCompositorHandle, "stream_compositor_create")
	purego.RegisterLibFunc(&streamCompositorDestroy, streamCompositorHandle, "stream_compositor_destroy")
	purego.RegisterLibFunc(&streamCompositorClear, streamCompositorHandle, "stream_compositor_clear")
	purego.RegisterLibFunc(&streamCompositorBlendLayer, streamCompositorHandle, "stream_compositor_blend_layer")
	purego.RegisterLibFunc(&streamCompositorGetResult, streamCompositorHandle, "stream_compositor_get_result")
	purego.RegisterLibFunc(&streamCompositorGetSize, streamCompositorHandle, "stream_compositor_get_size")
	return nil
}

// IsCompositorAvailable checks if libstream_compositor is available.
func IsCompositorAvailable() bool {
	if err := loadStreamCompositor(); err != nil {
		return false
	}
	return streamCompositorLoaded
}

// compositorCreate creates a new compositor instance.
func compositorCreate(width, height int) (uintptr, error) {
	if err := loadStreamCompositor(); err != nil {
		return 0, err
	}

	handle := streamCompositorCreate(int32(width), int32(height))
	if handle == 0 {
		return 0, errors.New("failed to create compositor")
	}

	return handle, nil
}

// compositorDestroy destroys a compositor instance.
func compositorDestroy(handle uintptr) {
	if handle != 0 && streamCompositorDestroy != nil {
		streamCompositorDestroy(handle)
	}
}

// compositorClear clears the canvas with a solid YUV color.
func compositorClear(handle uintptr, y, u, v byte) {
	if handle != 0 && streamCompositorClear != nil {
		streamCompositorClear(handle, y, u, v)
	}
}

// compositorBlendLayer blends a layer onto the canvas.
func compositorBlendLayer(
	handle uintptr,
	srcY, srcU, srcV, srcA []byte,
	srcW, srcH, srcStrideY, srcStrideUV int,
	x, y, width, height, zOrder int,
	alpha float32,
	visible bool,
	blendMode BlendMode,
) {
	if handle == 0 || streamCompositorBlendLayer == nil {
		return
	}

	// Create layer config struct
	config := streamLayerConfigC{
		X:         int32(x),
		Y:         int32(y),
		Width:     int32(width),
		Height:    int32(height),
		ZOrder:    int32(zOrder),
		Alpha:     alpha,
		Visible:   0,
		BlendMode: int32(blendMode),
	}
	if visible {
		config.Visible = 1
	}

	// Get pointers to data
	var srcYPtr, srcUPtr, srcVPtr, srcAPtr uintptr
	if len(srcY) > 0 {
		srcYPtr = uintptr(unsafe.Pointer(&srcY[0]))
	}
	if len(srcU) > 0 {
		srcUPtr = uintptr(unsafe.Pointer(&srcU[0]))
	}
	if len(srcV) > 0 {
		srcVPtr = uintptr(unsafe.Pointer(&srcV[0]))
	}
	if len(srcA) > 0 {
		srcAPtr = uintptr(unsafe.Pointer(&srcA[0]))
	}

	configPtr := uintptr(unsafe.Pointer(&config))

	streamCompositorBlendLayer(
		handle,
		srcYPtr, srcUPtr, srcVPtr, srcAPtr,
		int32(srcW), int32(srcH), int32(srcStrideY), int32(srcStrideUV),
		configPtr,
	)
}

// compositorGetResult gets the composited result from the canvas.
// Returns copies of the Y, U, V planes and their strides.
func compositorGetResult(handle uintptr) (y, u, v []byte, strideY, strideUV int) {
	if handle == 0 || streamCompositorGetResult == nil || streamCompositorGetSize == nil {
		return nil, nil, nil, 0, 0
	}

	// Get canvas dimensions
	var width, height int32
	streamCompositorGetSize(handle, uintptr(unsafe.Pointer(&width)), uintptr(unsafe.Pointer(&height)))
	if width <= 0 || height <= 0 {
		return nil, nil, nil, 0, 0
	}

	// Get result pointers
	var outY, outU, outV uintptr
	var outStrideY, outStrideUV int32
	streamCompositorGetResult(
		handle,
		uintptr(unsafe.Pointer(&outY)),
		uintptr(unsafe.Pointer(&outU)),
		uintptr(unsafe.Pointer(&outV)),
		uintptr(unsafe.Pointer(&outStrideY)),
		uintptr(unsafe.Pointer(&outStrideUV)),
	)

	if outY == 0 || outU == 0 || outV == 0 {
		return nil, nil, nil, 0, 0
	}

	// Calculate sizes
	sizeY := int(outStrideY) * int(height)
	sizeUV := int(outStrideUV) * (int(height) / 2)

	// Copy data from C memory to Go slices
	y = make([]byte, sizeY)
	u = make([]byte, sizeUV)
	v = make([]byte, sizeUV)

	copy(y, unsafe.Slice((*byte)(unsafe.Pointer(outY)), sizeY))
	copy(u, unsafe.Slice((*byte)(unsafe.Pointer(outU)), sizeUV))
	copy(v, unsafe.Slice((*byte)(unsafe.Pointer(outV)), sizeUV))

	return y, u, v, int(outStrideY), int(outStrideUV)
}
