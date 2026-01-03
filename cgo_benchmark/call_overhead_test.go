//go:build cgo && (darwin || linux)
// +build cgo
// +build darwin linux

package cgo_benchmark

import "testing"

// BenchmarkCGOCallOverhead measures the CGO call overhead for comparison with purego.
func BenchmarkCGOCallOverhead(b *testing.B) {
	b.Run("Noop", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = Noop()
		}
	})

	b.Run("GetVersion", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = GetOpusVersion()
		}
	})

	b.Run("CreateDestroy", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			CreateDestroy()
		}
	})
}
