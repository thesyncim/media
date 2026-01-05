//go:build (darwin || linux)

// Package media provides video compositing via libmedia_compositor using purego.

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
	mediaCompositorOnce    sync.Once
	mediaCompositorHandle  uintptr
	mediaCompositorInitErr error
	mediaCompositorLoaded  bool
)

// libmedia_compositor function pointers
var (
	mediaCompositorCreate     func(width, height int32) uintptr
	mediaCompositorDestroy    func(comp uintptr)
	mediaCompositorClear      func(comp uintptr, y, u, v uint8)
	mediaCompositorBlendLayer func(comp uintptr, srcY, srcU, srcV, srcA uintptr, srcW, srcH, srcStrideY, srcStrideUV int32, config uintptr)
	mediaCompositorGetResult  func(comp uintptr, outY, outU, outV, outStrideY, outStrideUV uintptr)
	mediaCompositorGetSize    func(comp uintptr, width, height uintptr)
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

// loadMediaCompositor loads the libmedia_compositor shared library.
func loadMediaCompositor() error {
	mediaCompositorOnce.Do(func() {
		mediaCompositorInitErr = loadMediaCompositorLib()
		if mediaCompositorInitErr == nil {
			mediaCompositorLoaded = true
		}
	})
	return mediaCompositorInitErr
}

func loadMediaCompositorLib() error {
	paths := getMediaCompositorLibPaths()

	var lastErr error
	for _, path := range paths {
		handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err == nil {
			mediaCompositorHandle = handle
			if err := loadMediaCompositorSymbols(); err != nil {
				purego.Dlclose(handle)
				lastErr = err
				continue
			}
			return nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return fmt.Errorf("failed to load libmedia_compositor: %w", lastErr)
	}
	return errors.New("libmedia_compositor not found in any standard location")
}

func getMediaCompositorLibPaths() []string {
	var paths []string

	libName := "libmedia_compositor.so"
	if runtime.GOOS == "darwin" {
		libName = "libmedia_compositor.dylib"
	}

	// Environment variable overrides
	if envPath := os.Getenv("MEDIA_COMPOSITOR_LIB_PATH"); envPath != "" {
		paths = append(paths, envPath)
	}
	if envPath := os.Getenv("MEDIA_SDK_LIB_PATH"); envPath != "" {
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

func loadMediaCompositorSymbols() error {
	purego.RegisterLibFunc(&mediaCompositorCreate, mediaCompositorHandle, "media_compositor_create")
	purego.RegisterLibFunc(&mediaCompositorDestroy, mediaCompositorHandle, "media_compositor_destroy")
	purego.RegisterLibFunc(&mediaCompositorClear, mediaCompositorHandle, "media_compositor_clear")
	purego.RegisterLibFunc(&mediaCompositorBlendLayer, mediaCompositorHandle, "media_compositor_blend_layer")
	purego.RegisterLibFunc(&mediaCompositorGetResult, mediaCompositorHandle, "media_compositor_get_result")
	purego.RegisterLibFunc(&mediaCompositorGetSize, mediaCompositorHandle, "media_compositor_get_size")
	return nil
}

// IsCompositorAvailable checks if libmedia_compositor is available.
func IsCompositorAvailable() bool {
	if err := loadMediaCompositor(); err != nil {
		return false
	}
	return mediaCompositorLoaded
}

// compositorCreate creates a new compositor instance.
func compositorCreate(width, height int) (uintptr, error) {
	if err := loadMediaCompositor(); err != nil {
		return 0, err
	}

	handle := mediaCompositorCreate(int32(width), int32(height))
	if handle == 0 {
		return 0, errors.New("failed to create compositor")
	}

	return handle, nil
}

// compositorDestroy destroys a compositor instance.
func compositorDestroy(handle uintptr) {
	if handle != 0 && mediaCompositorDestroy != nil {
		mediaCompositorDestroy(handle)
	}
}

// compositorClear clears the canvas with a solid YUV color.
func compositorClear(handle uintptr, y, u, v byte) {
	if handle != 0 && mediaCompositorClear != nil {
		mediaCompositorClear(handle, y, u, v)
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
	if handle == 0 || mediaCompositorBlendLayer == nil {
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

	mediaCompositorBlendLayer(
		handle,
		srcYPtr, srcUPtr, srcVPtr, srcAPtr,
		int32(srcW), int32(srcH), int32(srcStrideY), int32(srcStrideUV),
		configPtr,
	)
}

// compositorGetResult gets the composited result from the canvas.
// Returns copies of the Y, U, V planes and their strides.
func compositorGetResult(handle uintptr) (y, u, v []byte, strideY, strideUV int) {
	if handle == 0 || mediaCompositorGetResult == nil || mediaCompositorGetSize == nil {
		return nil, nil, nil, 0, 0
	}

	// Get canvas dimensions
	var width, height int32
	mediaCompositorGetSize(handle, uintptr(unsafe.Pointer(&width)), uintptr(unsafe.Pointer(&height)))
	if width <= 0 || height <= 0 {
		return nil, nil, nil, 0, 0
	}

	// Get result pointers
	var outY, outU, outV uintptr
	var outStrideY, outStrideUV int32
	mediaCompositorGetResult(
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
