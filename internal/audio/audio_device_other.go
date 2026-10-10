//go:build !windows

package audio

// 60 ms is comfortably above what a scheduler with a 1 ms timer will miss,
// and low enough that a footstep still lands with the step.
const defaultTargetMS = 60
