//go:build windows

package world

import (
	"sync"
	"syscall"
	"unsafe"
)

// The performance counter is read straight from kernel32: 100 ns steps, about 50 ns a read,
// no allocation. These are handles to the system's functions, set once, not state.
var (
	queryCounter  = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryPerformanceCounter")
	counterPerSec = sync.OnceValue(readFrequency)
)

// nanosPerSecond turns counter steps into nanoseconds.
const nanosPerSecond = int64(1e9)

// readFrequency is the counter's steps per second, fixed at boot; 0 if it cannot be read.
func readFrequency() int64 {
	var f int64
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("QueryPerformanceFrequency")
	if proc.Find() != nil {
		return 0
	}
	_, _, _ = syscall.SyscallN(proc.Addr(), uintptr(unsafe.Pointer(&f)))
	return f
}

// fineClock is a monotonic clock in nanoseconds with a fine step, for timing ticks.
func fineClock() int64 {
	f := counterPerSec()
	if f <= 0 || queryCounter.Find() != nil {
		return coarseClock()
	}
	var c int64
	_, _, _ = syscall.SyscallN(queryCounter.Addr(), uintptr(unsafe.Pointer(&c)))
	// Split so the count times a billion never overflows.
	return c/f*nanosPerSecond + c%f*nanosPerSecond/f
}
