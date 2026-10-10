// Package bravura carries Bravura's license on its own, for the installers
// that must ship it beside any build containing the font
// (internal/pack.StageEditor) without linking the font itself. The font
// and its SMuFL metadata in this directory are embedded by
// internal/score/glyph.
package bravura

import _ "embed"

// License is Bravura's SIL Open Font License, with its copyright notice.
//
//go:embed OFL.txt
var License string

// LicenseFileName is what an install calls the license file.
const LicenseFileName = "Bravura-OFL.txt"
