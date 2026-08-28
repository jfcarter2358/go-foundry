package world

// extrusion.go — the legacy surface displacement types, kept as loader input.
//
// Extrusion and TerrainPoint were how a surface stopped being flat before
// SurfaceMesh (surface_mesh.go), and every level written before it says so in
// these. They no longer build anything: the loader converts them to a mesh once
// (surface_mesh_migrate.go) and the writer emits meshes, so a level round-trips
// out of this representation and never back into it.
//
// They stay declared rather than being deleted outright because deleting them
// would make every level on disk unreadable, and a format that cannot read what
// it wrote last week is not a format. Nothing in the engine reads these fields
// after Sector.Load; nothing new should be written that does.
//
// The wall parameterisation below is not legacy — WallSurfaceFrame is built on
// it, and it is the definition of what a wall's (U, V) means.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// ExtrusionPoint is a point in surface-local coordinate space.
//
// For walls:          U = distance along wall, V = distance from ceiling down.
// For floors/ceilings: U = world X, V = world Z.
//
// SurfacePoint uses the identical convention, which is what lets the migration
// carry coordinates across unchanged.
type ExtrusionPoint struct {
	U float32
	V float32
}

// Extrusion is a polygonal region of a surface displaced along its normal:
// Outer is the hole cut in the surface, Inner the shape at Depth, and side
// faces connect Outer[i] to Inner[i].
//
// A single prism, and only ever one — which is why it could not express a bump
// on the floor of a recess, or a bevel on a hole's rim, and why it was replaced
// by a mesh whose extrude operation can be applied to its own output.
type Extrusion struct {
	Outer       []ExtrusionPoint // CCW polygon on surface (the hole)
	Inner       []ExtrusionPoint // CCW polygon at extruded depth (same vertex count)
	Depth       float32          // displacement along surface normal (+outward, -inward)
	Texture     string           // optional texture for back face + sides; empty = surface texture
	Visible     bool             // if true, render side faces and back face; if false, only the hole is cut
	BackVisible bool             // if true, render the inner polygon face; if false, it's open
	UVOffset    mgl32.Vec2       // UV translation for side/back faces
	UVScale     mgl32.Vec2       // UV scale for side/back faces; zero = {1,1}
	UVRotation  float32          // UV rotation in radians for side/back faces
	Alpha       float32          // per-extrusion alpha; only affects custom-texture back faces
}

// TerrainPoint is an additional point placed on a surface for terrain
// displacement. Coordinates follow the same convention as ExtrusionPoint;
// Depth is a world-Y offset for a floor or ceiling.
type TerrainPoint struct {
	U     float32
	V     float32
	Depth float32 // displacement along surface normal
}

// ---------- wall parameterisation ----------

// WallFrame computes the wall's local coordinate frame vectors.
// wallDir is the normalised direction from p1→p2 in XZ.
// wallNorm is the outward-facing normal (perpendicular in XZ, right-hand rule).
func WallFrame(p1, p2 mgl32.Vec2) (wallDir, wallNorm mgl32.Vec2) {
	dx := p2.X() - p1.X()
	dz := p2.Y() - p1.Y()
	l := float32(math.Sqrt(float64(dx*dx + dz*dz)))
	if l < 1e-9 {
		return mgl32.Vec2{1, 0}, mgl32.Vec2{0, -1}
	}
	wallDir = mgl32.Vec2{dx / l, dz / l}
	// Normal: rotate wallDir 90° CW in XZ → outward for CCW-wound sector
	wallNorm = mgl32.Vec2{-wallDir.Y(), wallDir.X()}
	return
}

// WallLocalToWorld converts wall-local (u, v, depth) to world (X, Y, Z).
//
// u: distance along wall (0 at p1, wallLen at p2)
// v: distance from ceiling downward (0 at ceiling, wallH at floor)
// depth: displacement along wall outward normal
//
// Heights are linearly interpolated between the two wall endpoints.
func WallLocalToWorld(
	u, v, depth float32,
	p1, p2 mgl32.Vec2,
	dir, norm mgl32.Vec2,
	wallLen float32,
	floorH1, floorH2, ceilH1, ceilH2 float32,
) (x, y, z float32) {
	t := u / wallLen
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	cH := ceilH1 + (ceilH2-ceilH1)*t
	y = cH - v
	x = p1.X() + dir.X()*u + norm.X()*depth
	z = p1.Y() + dir.Y()*u + norm.Y()*depth
	return
}
