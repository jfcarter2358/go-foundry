//go:build windows

package audio

// Windows' default timer resolution is about 15.6 ms, so a time.Sleep of five
// milliseconds is really a sleep of fifteen. A queue target sized for Linux
// drains completely between wakeups there, which is the classic "fine on my
// machine, crackles on Windows" bug. Buy the headroom outright rather than
// waiting for the field to discover it.
const defaultTargetMS = 100
