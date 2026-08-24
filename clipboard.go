package main

// clipboard.go — the system clipboard, handed to every ImGui context.
//
// ImGui ships a fallback clipboard, but it is a buffer inside the process: with
// it, Ctrl+C in the script editor and Ctrl+V in a browser have nothing to do
// with each other. Giving it SDL's implementation is what makes copy and paste
// mean the same thing inside the editor as everywhere else on the desktop —
// which is most of the point, since a script is usually being moved between the
// editor and somewhere else.
//
// Registered per ImGui context (imguiConfigureIO), because imgui-go keys its
// clipboard registry by IO handle and every UIWindow has its own context.

import (
	"log"
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

// sdlClipboard implements imgui.Clipboard against SDL.
type sdlClipboard struct{}

// Text returns what the clipboard holds, with its line endings normalised.
//
// Text copied out of a Windows program arrives with CRLF, and a stray carriage
// return in a script is an invisible parse error — the kind that reads as "this
// line looks fine and the compiler disagrees".
func (sdlClipboard) Text() (string, error) {
	if !sdl.HasClipboardText() {
		return "", nil
	}
	s, err := sdl.GetClipboardText()
	if err != nil {
		return "", err
	}
	return normalizeClipboardText(s), nil
}

// SetText puts text on the clipboard.
//
// The error is logged rather than returned: ImGui's copy path has nowhere to
// report one, and a clipboard that quietly did nothing is worth a line in the
// log rather than silence.
func (sdlClipboard) SetText(value string) {
	if err := sdl.SetClipboardText(value); err != nil {
		log.Printf("clipboard: %v", err)
	}
}

// clipboardText is what the clipboard holds, or "" if it holds nothing this
// code can use. The error is swallowed on purpose — every caller's answer to
// "the clipboard could not be read" is "then there is nothing to paste".
func clipboardText() string {
	s, err := sdlClipboard{}.Text()
	if err != nil {
		return ""
	}
	return s
}

// normalizeClipboardText converts CRLF and lone CR line endings to LF.
func normalizeClipboardText(s string) string {
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}
