package textureui

// texture_uv_guide.go — the texture editor's window onto whatever wears the
// open texture.
//
// The texture editor draws UV islands over its canvas so the painter can see
// which region belongs to which face. That outline can only come from the
// documents that own the faces — the model editor's model, and brushes laid out
// by the level and entity editors' Unwrap & Paint — but the texture editor is
// part of none of them and must not depend on them: a texture is edited on its
// own, with nothing else open at all.
//
// So the editor hands its answer down instead (EditorInit). The default says
// "nothing wears it", which is exactly right for a texture editor used alone and
// for every test.

import "github.com/go-gl/mathgl/mgl32"

// UVGuideForTexture returns the UV islands of every open face that wears tex,
// in UV space, or nil when nothing open does.
var UVGuideForTexture = func(tex string) [][]mgl32.Vec2 { return nil }
