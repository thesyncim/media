//go:build (darwin || linux) && !cgo

// Shared utilities for purego-based codec implementations.

package media

import (
	"os"
	"path/filepath"
	"unsafe"
)

// goStringFromPtr converts a C string pointer to a Go string.
// Used by both VPX and Opus purego implementations.
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
		if length > 1024 { // Safety limit
			break
		}
	}
	if length == 0 {
		return ""
	}
	return string(unsafe.Slice((*byte)(p), length))
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
