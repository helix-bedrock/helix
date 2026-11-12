//go:build !windows

package world

// fineClock is a monotonic clock in nanoseconds with a fine step, for timing ticks. Off
// Windows Go's own clock is fine already.
func fineClock() int64 { return coarseClock() }
