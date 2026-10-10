package levelio

// level_headless.go — reading a level with no window.
//
// The engine loads a level and uploads it in one pass, which is right when
// there is a GL context to upload to. The level compiler is the case where
// there is not: it runs from the command line as a build step, and everything
// it needs — the brushes, the lights, the placements — is on the CPU side of
// that pass.
//
// Rather than a second loader that would drift from the real one, this turns
// the uploads off and runs the real one. Both halves are already split for
// exactly this reason (gfx.GeometryUploadsEnabled, gfx.TextureUploadsEnabled),
// and running the same code means a level that compiles is a level that loads:
// the ID uniquing, the prefab materialisation, the entity decks and every
// validation the loader does all happen here too.

import (
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/world"
)

// LoadLevelHeadless reads a level without a GL context.
//
// The returned level is fully built except that nothing is on the GPU: face
// geometry has its vertices and its bounds but no buffers, and textures are
// registered with no handle. It is what the compiler wants and is not usable
// for drawing.
//
// Not safe to call while the engine is running — the flags it flips are
// package globals, and a frame drawn in the middle of one would upload nothing.
// The command-line compiler is a separate process for that reason.
func LoadLevelHeadless(path string) (world.Level, error) {
	defer withoutUploads()()
	return LoadLevel(path)
}

// withoutUploads turns both upload halves off and returns the function that
// puts them back.
//
// It also does what app.go's boot does for the texture table: the loader
// refuses a level naming a texture it cannot find, and finding one means having
// walked textures/ — which app.go does at startup and a tool has to do for
// itself. The walk is unconditional and the old table is put back afterwards,
// so that a caller pointing the install root somewhere new gets that root's
// textures rather than whatever a previous call left behind.
func withoutUploads() func() {
	geo, tex := gfx.GeometryUploadsEnabled, gfx.TextureUploadsEnabled
	gfx.GeometryUploadsEnabled, gfx.TextureUploadsEnabled = false, false
	if gfx.Textures == nil {
		gfx.Textures = map[string]uint32{}
	}
	names := gfx.TextureNames
	gfx.TextureNames = gfx.ScanTextures(fileutil.TexturesDir())
	return func() {
		gfx.GeometryUploadsEnabled, gfx.TextureUploadsEnabled = geo, tex
		gfx.TextureNames = names
	}
}
