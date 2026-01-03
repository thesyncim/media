//go:build cgo && (darwin || linux)
// +build cgo
// +build darwin linux

// Package cgo_benchmark provides CGO benchmarks for comparison with purego.
package cgo_benchmark

/*
#cgo pkg-config: opus
#include <opus.h>
#include <stdlib.h>

// Simple CGO function - just returns a pointer to the version string
const char* cgo_opus_get_version() {
    return opus_get_version_string();
}

// Simple CGO function - creates and destroys encoder (to measure allocation overhead)
int cgo_opus_create_destroy() {
    int error;
    OpusEncoder* enc = opus_encoder_create(48000, 1, OPUS_APPLICATION_VOIP, &error);
    if (enc) {
        opus_encoder_destroy(enc);
        return 0;
    }
    return error;
}

// Minimal CGO function - just a noop to measure pure call overhead
int cgo_noop() {
    return 42;
}
*/
import "C"

// Noop calls a minimal C function to measure pure call overhead
func Noop() int {
	return int(C.cgo_noop())
}

// GetOpusVersion calls opus_get_version_string via CGO
func GetOpusVersion() string {
	return C.GoString(C.cgo_opus_get_version())
}

// CreateDestroy creates and destroys an Opus encoder
func CreateDestroy() int {
	return int(C.cgo_opus_create_destroy())
}
