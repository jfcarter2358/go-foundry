//go:build !(linux && cgo) && !(windows && cgo)

package mididev

// none.go — no MIDI input: a platform this package has no way in for, or a
// build without cgo (which both ways in need).

func listPorts() ([]Port, error) { return nil, ErrUnavailable }

func openPort(string) (*Input, error) { return nil, ErrUnavailable }
