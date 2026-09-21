//go:build !windows

package render

import "time"

var profEpoch = time.Now()

// profNow is a timestamp in milliseconds from an arbitrary origin.
func profNow() float64 { return float64(time.Since(profEpoch).Nanoseconds()) / 1e6 }
