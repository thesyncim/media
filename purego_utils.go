//go:build darwin || linux

// Shared utilities for purego-based codec implementations.

package media

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"
)

// maxCStringLen is the maximum length we'll read from a C string.
const maxCStringLen = 8192

// goStringFromPtr converts a C string pointer to a Go string.
// Used by both VPX and Opus purego implementations.
// Note: Truncates strings longer than maxCStringLen characters.
func goStringFromPtr(ptr uintptr) string {
	if ptr == 0 {
		return ""
	}
	// Find string length
	p := unsafe.Pointer(ptr)
	var length int
	for {
		if *(*byte)(unsafe.Pointer(uintptr(p) + uintptr(length))) == 0 {
			break
		}
		length++
		if length > maxCStringLen { // Safety limit to prevent reading unmapped memory
			break
		}
	}
	if length == 0 {
		return ""
	}
	return string(unsafe.Slice((*byte)(p), length))
}

// safeStrideMul safely multiplies row and stride, checking for overflow.
// Returns (result, true) if multiplication is safe, or (0, false) on overflow.
func safeStrideMul(row, stride int) (int, bool) {
	if stride <= 0 || row < 0 {
		return 0, false
	}
	result := row * stride
	// Check for overflow: if result/stride != row, overflow occurred
	if stride != 0 && result/stride != row {
		return 0, false
	}
	return result, true
}

// findModuleRoot walks up the directory tree from the current working directory
// to find the module root (directory containing go.mod).
func findModuleRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}

	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

var (
	sourceRoot     string
	sourceRootOnce sync.Once
)

// findSourceRoot uses runtime.Caller to find the source directory.
// This works during development when source files are available.
// Falls back to findModuleRoot if runtime info is not available.
func findSourceRoot() string {
	sourceRootOnce.Do(func() {
		// Try to get this file's location via runtime
		_, file, _, ok := runtime.Caller(0)
		if ok && file != "" {
			// This file is in the package root, so its directory is the source root
			dir := filepath.Dir(file)
			// Verify it looks like our source by checking for go.mod
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				sourceRoot = dir
				return
			}
		}
		// Fallback to working directory method
		sourceRoot = findModuleRoot()
	})
	return sourceRoot
}
