// Package editoricon is the Foundry editor's icon: the window icon SDL shows,
// and — through rsrc_windows_amd64.syso, which the linker picks up from any
// package in the build — the icon resource of the Windows editor .exe, which is
// what its Explorer entry, taskbar button, shortcuts and Add/Remove Programs
// entry all display.
//
// Only the development build imports this package (internal/app/startup_dev.go).
// That is the whole mechanism keeping a shipped game from wearing Foundry's
// icon: a .syso cannot be left out by build tag, only by nobody importing the
// package it sits in.
//
// Every file here is written by `go run ./cmd/genicon`; edit that, not these.
package editoricon

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"
)

//go:embed foundry.png
var iconPNG []byte

// Apply sets the editor's icon on win.
func Apply(win *sdl.Window) error {
	src, err := png.Decode(bytes.NewReader(iconPNG))
	if err != nil {
		return fmt.Errorf("editor icon: %w", err)
	}
	img := image.NewNRGBA(src.Bounds())
	draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)

	// RGBA32 is SDL's name for "R, G, B, A bytes in that order in memory",
	// whatever the machine's endianness — which is exactly an NRGBA's Pix.
	w, h := int32(img.Rect.Dx()), int32(img.Rect.Dy())
	surf, err := sdl.CreateRGBSurfaceWithFormatFrom(unsafe.Pointer(&img.Pix[0]), w, h, 32, int32(img.Stride), uint32(sdl.PIXELFORMAT_RGBA32))
	if err != nil {
		return fmt.Errorf("editor icon: %w", err)
	}
	// SetIcon copies the pixels, so the surface (and img under it) can go.
	win.SetIcon(surf)
	surf.Free()
	return nil
}
