package world

import "time"

// Tick timing reads fineClock, not time.Now: on Windows Go's clock moves in steps of about
// half a millisecond, so most parts of a tick would read as 0 and a 0.4 ms tick as 0 or 0.5.

// clockBase is where coarseClock counts from, so its readings are monotonic. Set once.
var clockBase = time.Now()

// coarseClock is Go's monotonic clock in nanoseconds.
func coarseClock() int64 { return int64(time.Since(clockBase)) }
