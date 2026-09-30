package render

// init.go — standing the world's render passes up.
//
// One entry point, called once from the engine's own startup, because the
// alternative has already failed once. GrassInit and SpriteInit used
// to be called from editor.EditorInit — reasonable when the editor was the only
// thing that started up, and silently wrong the moment `-tags release` began
// compiling the editor out entirely. A release build called none of them, and
// because each pass early-outs on its own `initialized` flag, nothing said so:
// a shipped game simply had no biome scatter, no ground patches, no sprites
// and no particles, and drew the structural geometry perfectly while missing
// all of it.
//
// So the rule this file exists to hold is: a pass that draws LEVEL CONTENT is
// initialised here, from internal/app, in both builds. Only an authoring aid —
// a gizmo, an overlay, something with no meaning once the editor is gone —
// belongs in EditorInit.
//
// Must be called with the GL context current, and before the first frame.

// InitWorldPasses compiles the programs and uploads the template meshes every
// world pass needs.
//
// Early enough in startup to be before the level loads, which matters for one
// of them: GrassInit is also what installs world.EvictBiomeCaches, and the
// loader can release a chunk authored `disabled: true` through it.
//
// The first two panic on a shader that will not compile, which is the
// behaviour they have always had — a broken shader is a broken install, not a
// runtime condition worth limping through. SkyInit logs and disables itself
// instead, because a level without its sky is still a level.
func InitWorldPasses() {
	GrassInit()
	SpriteInit()
	SkyInit()
	VolumetricInit()
	TerrainInit()
}
