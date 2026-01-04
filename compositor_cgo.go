//go:build (darwin || linux) && cgo

// Package media provides video compositing via libstream_compositor using CGO.

package media

/*
#cgo CFLAGS: -I${SRCDIR}/clib
#cgo darwin LDFLAGS: -L${SRCDIR}/build -lstream_compositor -Wl,-rpath,${SRCDIR}/build
#cgo linux LDFLAGS: -L${SRCDIR}/build -lstream_compositor -Wl,-rpath,${SRCDIR}/build

#include "stream_compositor.h"
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"unsafe"
)

// IsCompositorAvailable checks if libstream_compositor is available.
// With CGO this is always true since it links at compile time.
func IsCompositorAvailable() bool {
	return true
}

// compositorCreate creates a new compositor instance.
func compositorCreate(width, height int) (uintptr, error) {
	handle := C.stream_compositor_create(C.int32_t(width), C.int32_t(height))
	if handle == nil {
		return 0, errors.New("failed to create compositor")
	}
	return uintptr(unsafe.Pointer(handle)), nil
}

// compositorDestroy destroys a compositor instance.
func compositorDestroy(handle uintptr) {
	if handle != 0 {
		C.stream_compositor_destroy((*C.StreamCompositor)(unsafe.Pointer(handle)))
	}
}

// compositorClear clears the canvas with a solid YUV color.
func compositorClear(handle uintptr, y, u, v byte) {
	if handle != 0 {
		C.stream_compositor_clear(
			(*C.StreamCompositor)(unsafe.Pointer(handle)),
			C.uint8_t(y), C.uint8_t(u), C.uint8_t(v),
		)
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
	if handle == 0 {
		return
	}

	// Create layer config
	var config C.StreamLayerConfig
	config.x = C.int32_t(x)
	config.y = C.int32_t(y)
	config.width = C.int32_t(width)
	config.height = C.int32_t(height)
	config.z_order = C.int32_t(zOrder)
	config.alpha = C.float(alpha)
	if visible {
		config.visible = 1
	} else {
		config.visible = 0
	}
	config.blend_mode = C.StreamBlendMode(blendMode)

	// Get pointers to data
	var srcYPtr, srcUPtr, srcVPtr, srcAPtr *C.uint8_t
	if len(srcY) > 0 {
		srcYPtr = (*C.uint8_t)(unsafe.Pointer(&srcY[0]))
	}
	if len(srcU) > 0 {
		srcUPtr = (*C.uint8_t)(unsafe.Pointer(&srcU[0]))
	}
	if len(srcV) > 0 {
		srcVPtr = (*C.uint8_t)(unsafe.Pointer(&srcV[0]))
	}
	if len(srcA) > 0 {
		srcAPtr = (*C.uint8_t)(unsafe.Pointer(&srcA[0]))
	}

	C.stream_compositor_blend_layer(
		(*C.StreamCompositor)(unsafe.Pointer(handle)),
		srcYPtr, srcUPtr, srcVPtr, srcAPtr,
		C.int32_t(srcW), C.int32_t(srcH),
		C.int32_t(srcStrideY), C.int32_t(srcStrideUV),
		&config,
	)
}

// compositorGetResult gets the composited result from the canvas.
// Returns copies of the Y, U, V planes and their strides.
func compositorGetResult(handle uintptr) (y, u, v []byte, strideY, strideUV int) {
	if handle == 0 {
		return nil, nil, nil, 0, 0
	}

	comp := (*C.StreamCompositor)(unsafe.Pointer(handle))

	// Get canvas dimensions
	var width, height C.int32_t
	C.stream_compositor_get_size(comp, &width, &height)
	if width <= 0 || height <= 0 {
		return nil, nil, nil, 0, 0
	}

	// Get result pointers
	var outY, outU, outV *C.uint8_t
	var outStrideY, outStrideUV C.int32_t
	C.stream_compositor_get_result(comp, &outY, &outU, &outV, &outStrideY, &outStrideUV)

	if outY == nil || outU == nil || outV == nil {
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
