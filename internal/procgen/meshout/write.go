package meshout

// write.go — a mesh to disk: as a GLB, and cooked as a Nanite mesh.
//
// The cook is the in-memory one (nanitecook.CookMeshFile): nothing is written
// as a .glb on the way, and an unchanged mesh with unchanged options is not
// recooked, because the .fnm's header carries the mesh's hash. WriteGLB is
// for a stage that wants the source kept too — to open in a DCC tool, or to
// cook later with different options.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"

	"foundry/internal/meshcollide"
	"foundry/internal/model"
	"foundry/internal/nanitecook"

	"github.com/go-gl/mathgl/mgl32"
)

// WriteGLB writes m as a binary glTF.
func WriteGLB(path string, m *model.ImportMesh) error {
	data, err := model.EncodeGLB(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// CookOptions are a generator's cook settings: the project's (game.yaml's
// nanite block) with the generator's own choices over them.
type CookOptions struct {
	// LockBorder and LockRect are nanitecook.Options's; a tiled surface sets
	// one so its tiles meet at every LOD.
	LockBorder bool
	LockRect   *[4]float32
	// AttrRadiusCap is nanitecook.Options's: the radius the DAG's normal
	// and UV weights scale with, capped (0: not), so a big scan's LOD is
	// decided by its shape rather than its size (docs/canyon/PERF.md).
	AttrRadiusCap float32
	// Force recooks even when the file on disk matches.
	Force bool
	// Serial keeps the cook on the calling goroutine (nanitecook's
	// Options.Serial). Same bytes either way. Measured on the 64-tile run in
	// docs/canyon/PHASE_0.md it was SLOWER (96 s against 31 s) and no
	// lighter (4.9 GB peak against 3.6 GB), so it is off by default; it is
	// here for a stage that has measured otherwise.
	Serial bool
	// PositionStep, when set (a power of two, metres), is the quantisation
	// grid the cook must use: Cook picks the position bits that give exactly
	// it for this mesh's extent (see QuantBitsFor). Tiles of one field cooked
	// on one step snap their shared seam vertices identically — without it a
	// tile taller than its neighbours gets a coarser grid and the seam opens
	// by up to half a step.
	PositionStep float32
	// CollisionMesh cooks the collision to be the mesh alone (meshcollide's
	// Settings.Mode mesh): no convex decomposition, which on a dense terrain
	// tile is about 40% of the cook. For meshes only ever placed with
	// `collision: mesh`; a placement asking for hulls gets its box.
	CollisionMesh bool
	// CollisionHulls cooks the collision with its convex decomposition
	// whatever the mesh's size (meshcollide's Settings.Mode hulls): for a
	// small rock a body should roll off, which auto would only give one
	// under the project's auto size. Ignored with CollisionMesh.
	CollisionHulls bool
}

// QuantBitsFor is the nanitecook PositionBits that make a mesh spanning
// lo..hi quantise on step (a power of two), or 0 when no bit count can (the
// step is coarser than the extent, or finer than float32 allows at this
// distance from the origin). It mirrors nanite.NewQuant's choice exactly.
func QuantBitsFor(lo, hi mgl32.Vec3, step float32) int {
	if step <= 0 {
		return 0
	}
	e := int(math.Round(math.Log2(float64(step))))
	if math.Ldexp(1, e) != float64(step) {
		return 0
	}
	ext := float64(max(hi[0]-lo[0], hi[1]-lo[1], hi[2]-lo[2]))
	if ext <= 0 {
		return 0
	}
	bits := int(math.Floor(math.Log2(ext))) - e
	if bits < 1 || bits > 30 {
		return 0
	}
	maxAbs := 0.0
	for k := 0; k < 3; k++ {
		maxAbs = max(maxAbs, math.Abs(float64(lo[k])), math.Abs(float64(hi[k])))
	}
	if maxAbs > 0 && e < int(math.Ceil(math.Log2(maxAbs)))-23 {
		return 0
	}
	return bits
}

// meshBounds is m's position bounds.
func meshBounds(m *model.ImportMesh) (lo, hi mgl32.Vec3) {
	for i, p := range m.Positions {
		if i == 0 {
			lo, hi = p, p
			continue
		}
		lo = mgl32.Vec3{min(lo[0], p[0]), min(lo[1], p[1]), min(lo[2], p[2])}
		hi = mgl32.Vec3{max(hi[0], p[0]), max(hi[1], p[1]), max(hi[2], p[2])}
	}
	return
}

// Cook cooks m into out (a .fnm), and reports whether it skipped as up to
// date. Safe from Parallel workers: a cook touches nothing shared. A pool of
// cooks holds a pool of meshes in memory: on the 64-tile run (33k triangles a
// tile) 20 workers peaked at 3.6 GB in 31 s and 4 workers at 1.2 GB in 40 s,
// so a stage cooking big tiles should bound its concurrency by memory.
func Cook(m *model.ImportMesh, out string, opt CookOptions) (skipped bool, err error) {
	o := nanitecook.ProjectOptions()
	o.LockBorder = opt.LockBorder
	o.LockRect = opt.LockRect
	o.Serial = opt.Serial
	o.AttrRadiusCap = opt.AttrRadiusCap
	if opt.CollisionMesh {
		o.Collision.Mode = meshcollide.ModeMesh
	} else if opt.CollisionHulls {
		o.Collision.Mode = meshcollide.ModeHulls
	}
	if opt.PositionStep > 0 {
		lo, hi := meshBounds(m)
		if o.PositionBits = QuantBitsFor(lo, hi, opt.PositionStep); o.PositionBits == 0 {
			return false, fmt.Errorf("no quantisation gives a %g m step for a mesh spanning %v .. %v", opt.PositionStep, lo, hi)
		}
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return false, err
	}
	res, err := nanitecook.CookMeshFile(m, out, opt.Force, o)
	return res.Skipped, err
}
