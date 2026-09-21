package render

// The profiler's clock on Windows. Go's own monotonic clock there advances in
// whole timer-interrupt ticks (half a millisecond at best), which rounds a pass
// that runs eighteen times a frame to nothing or to far too much.
// QueryPerformanceCounter is the high-resolution one.

import (
	"syscall"
	"unsafe"
)

var (
	qpc     = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryPerformanceCounter")
	qpfreq  = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryPerformanceFrequency")
	qpcFreq float64
)

// profNow is a timestamp in milliseconds from an arbitrary origin.
func profNow() float64 {
	if qpcFreq == 0 {
		var f int64
		qpfreq.Call(uintptr(unsafe.Pointer(&f)))
		qpcFreq = float64(f) / 1000
	}
	var c int64
	qpc.Call(uintptr(unsafe.Pointer(&c)))
	return float64(c) / qpcFreq
}
