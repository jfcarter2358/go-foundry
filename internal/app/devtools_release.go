//go:build release

package app

// devtools_release.go — the release build's half of the editor seam.
//
// Every function here is the stub for one in devtools_dev.go, in the same order
// and with the same signature, so the two files can be read side by side. The
// only import is what the signatures themselves need: no editor, no imgui, no
// secondary windows.
//
// What this buys is not just size. A shipped game that has an editor compiled
// into it has, somewhere in it, a keystroke that opens the editor — and every
// path that can mutate the level, write over a texture or save a script is
// reachable from a machine the author does not control. Stripping the code is
// the only version of that guarantee that does not depend on getting a
// predicate right.

import (
	"foundry/internal/gfx"
	"foundry/internal/render"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// devAvailable is false: there is no editor in this binary. The constant, and
// not a variable, is deliberate — devActive below is a constant expression, so
// every `if devActive()` in the frame loop compiles to nothing at all.
const devAvailable = false

func devActive() bool    { return false }
func devWireframe() bool { return false }

// devRunCommand runs nothing: a shipped game has no command-line tools, and
// `my-game build-release` is a player's typo rather than a request.
func devRunCommand(args []string) (exitCode int, ran bool) { return 0, false }

// devPendingProject is always "": a release build opens no other project.
func devPendingProject() string { return "" }

// devInit does nothing. The release build's startup loads the game's own start
// level and goes straight into gameplay — see startup_release.go.
func devInit(level *world.Level, levelPath, levelsDir string) {}

func devShutdown()                         {}
func devToggle()                           {}
func devTick(keyStates []uint8)            {}
func devRender()                           {}
func devHandleEvent(event sdl.Event)       {}
func devEndDrags()                         {}
func devAdoptLoadedLevel()                 {}
func devDrainWork()                        {}
func devSetMainWindowID(id uint32)         {}
func devUpdateFocus()                      {}
func devRenderOverlays(dt float32)         {}
func devRenderSecondaryWindows(dt float32) {}

func devRenderMarkers(lvl *world.Level, cull *render.Frustum) {}

func devRenderBrushGhost(draw func(g *gfx.Geometry, model mgl32.Mat4, doubleSided bool)) {}

// devRouteEvent consumes nothing: with no F1 reference and no secondary
// windows, every event belongs to the game.
func devRouteEvent(event sdl.Event) bool { return false }

// devInterceptQuit leaves quit_game alone: there is no editor to go back to, so
// the game's Quit button ends the process.
func devInterceptQuit() bool { return false }
