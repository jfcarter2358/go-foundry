package textureui

// texture_uv_guide.go — the texture editor's window onto the open model.
//
// The texture editor draws the model's UV islands over its canvas so the
// painter can see which region belongs to which face. That outline can only
// come from the model editor's open document, but the texture editor is not
// part of the model editor and must not depend on it: a texture is edited on
// its own, with no model open at all.
//
// So the model editor hands its answer down instead (EditorInit). The default
// says "no model open", which is exactly right for a texture editor used alone
// and for every test.

import "github.com/go-gl/mathgl/mgl32"

// UVGuideForTexture returns the open model's UV islands for tex, in UV space,
// or nil when no model is open or the open one does not wear tex.
var UVGuideForTexture = func(tex string) [][]mgl32.Vec2 { return nil }
