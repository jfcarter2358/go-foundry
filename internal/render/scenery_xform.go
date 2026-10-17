package render

// scenery_xform.go — every placement's model matrix, worked out once a frame.
//
// world.Scenery.ModelMatrix builds the matrix from the placement's position,
// Euler rotation and scale on every call (three sincos and three products),
// and a DrawWorld frame asked for each of the generated canyon's 6,607
// placements' four times over — the cooked meshes' instance upload, the
// virtual shadow map's change tracker (its hash and its box), and live GI's
// source walk — about 3 ms of the CPU's frame (docs/canyon/PERF.md).
//
// fillSceneryXforms works them all out once at the top of the frame, before
// anything is started on another goroutine (frame_prep.go), and from then on
// the frame's readers only read: sceneryModel answers from the table while
// the frame it was made for is open, and asks the placement itself
// otherwise (a test, a preview, a placement added mid-frame). Placements do
// not move inside DrawWorld — the simulation and the editor's gizmos move
// them before or after it — so the table is the frame's truth.

import (
	"math"
	"unsafe"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

var sceneryXforms struct {
	frame uint64
	lvl   *world.Level
	base  *world.Scenery
	m     []mgl32.Mat4
	// keys are what each matrix was made from (position, rotation and
	// scale): a placement that has not moved since keeps its matrix rather
	// than three sincos and three products a frame — 2.7 ms of the biome
	// showcase's 14,800 placements (docs/regions/PRESETS_5.md).
	keys []sceneryXform
}

// fillSceneryXforms makes the frame's table. Called by DrawWorld with the
// frame open.
func fillSceneryXforms(lvl *world.Level) {
	// The last frame's GI walk reads the table: done before it is rewritten
	// (it always is by now — giCollect joins it — but never assumed).
	giPrep.job.wait()
	x := &sceneryXforms
	x.frame, x.lvl, x.base = drawFrameNo, lvl, nil
	if lvl == nil || len(lvl.Scenery) == 0 {
		x.m = x.m[:0]
		return
	}
	if len(x.keys) != len(lvl.Scenery) || len(x.m) != len(lvl.Scenery) {
		x.m = make([]mgl32.Mat4, len(lvl.Scenery))
		x.keys = make([]sceneryXform, len(lvl.Scenery))
		for i := range x.keys {
			x.keys[i].scale = mgl32.Vec3{float32(math.NaN()), 0, 0} // never matches
		}
	}
	for i := range lvl.Scenery {
		s := &lvl.Scenery[i]
		k := sceneryXform{s.Position, s.Rotation, s.Scale}
		if x.keys[i] != k {
			x.keys[i], x.m[i] = k, s.ModelMatrix()
		}
	}
	x.base = &lvl.Scenery[0]
}

// sceneryModel is s's ModelMatrix, s being one of lvl's placements.
func sceneryModel(lvl *world.Level, s *world.Scenery) mgl32.Mat4 {
	x := &sceneryXforms
	if drawFrameOpen && x.frame == drawFrameNo && x.lvl == lvl && x.base != nil && len(lvl.Scenery) == len(x.m) &&
		&lvl.Scenery[0] == x.base {
		i := (uintptr(unsafe.Pointer(s)) - uintptr(unsafe.Pointer(x.base))) / unsafe.Sizeof(world.Scenery{})
		if uintptr(unsafe.Pointer(s)) >= uintptr(unsafe.Pointer(x.base)) && i < uintptr(len(x.m)) && &lvl.Scenery[i] == s {
			return x.m[i]
		}
	}
	return s.ModelMatrix()
}
