package main

// spawn_point.go — the player spawn point: a marker "primitive" (PrimitiveSpawnPoint)
// that carries the position and facing the player starts gameplay with.
//
// It rides on the primitive system rather than being its own level concept so it
// inherits everything primitives already have for free: placement from the 3D and
// 2D catalogs, ray picking, the translate gizmo, the properties panel, chunk-local
// save/load, undo, and chunk import. What makes it a marker rather than geometry is
// the three exemptions below — it never draws outside the editor, never casts a
// shadow, and never collides — plus the fact that its Position is the player's
// *feet*, the same reference point placeSelectedPrimitiveAt uses in 3D.
//
// Facing is derived from the primitive's own Rotation by transforming the mesh's
// local forward axis (+X, matching yaw 0 in DirectionFromAngles) through the
// rotation, so the arrow the user sees in the viewport and the direction the player
// looks are the same vector by construction — no separate yaw field to drift.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// spawnArrowLift raises the arrow mesh off the floor plane. Position is the
// player's feet, so an unlifted marker would z-fight with the floor it sits on.
const spawnArrowLift = float32(0.16)

// spawnArrowForward is the arrow's local pointing axis. +X is yaw 0 in
// DirectionFromAngles, which makes an unrotated spawn point face the same way an
// unrotated camera does.
var spawnArrowForward = mgl32.Vec3{1, 0, 0}

// isSpawnPoint reports whether p is a spawn marker rather than real geometry.
func isSpawnPoint(p *Primitive) bool { return p.Kind == PrimitiveSpawnPoint }

// primitiveCollides reports whether p should take part in collision. Spawn
// markers never do, even if a hand-edited level sets collision on one.
func primitiveCollides(p *Primitive) bool { return p.Collision && !isSpawnPoint(p) }

// SpawnDirection returns the unit direction a spawn point faces, derived from its
// Euler rotation the same way ModelMatrix builds it (Y * X * Z), so it always
// agrees with the arrow on screen.
func SpawnDirection(p *Primitive) mgl32.Vec3 {
	R := mgl32.HomogRotate3DY(p.Rotation.Y()).
		Mul4(mgl32.HomogRotate3DX(p.Rotation.X())).
		Mul4(mgl32.HomogRotate3DZ(p.Rotation.Z()))
	d := R.Mul4x1(spawnArrowForward.Vec4(0)).Vec3()
	if d.Len() < 1e-6 {
		return spawnArrowForward
	}
	return d.Normalize()
}

// SpawnYawPitch returns the camera angles (radians) a spawn point spawns the
// player with. Pitch is clamped to the same limit mouse-look enforces so a
// straight-up marker can't hand gameplay a gimbal-flipped camera.
func SpawnYawPitch(p *Primitive) (yaw, pitch float32) {
	d := SpawnDirection(p)
	yaw = float32(math.Atan2(float64(d.Z()), float64(d.X())))
	pitch = float32(math.Asin(float64(clampValue(d.Y(), -1, 1))))
	if pitch > maxPitch {
		pitch = maxPitch
	}
	if pitch < -maxPitch {
		pitch = -maxPitch
	}
	return yaw, pitch
}

// FindSpawnPoint returns the level's active spawn point, or nil when the level has
// none. Levels are free to hold several (handy while blocking out a map); the
// first one in chunk order wins, which is stable across saves because chunk and
// primitive order both round-trip through the YAML.
func FindSpawnPoint(lvl *Level) *Primitive {
	if lvl == nil {
		return nil
	}
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		for pi := range ch.Primitives {
			if isSpawnPoint(&ch.Primitives[pi]) {
				return &ch.Primitives[pi]
			}
		}
	}
	return nil
}

// ApplySpawnPoint teleports the player and camera to the level's spawn point and
// reports whether one was found. Callers fall back to whatever camera they already
// had when it returns false, so a level with no spawn point behaves as before.
func ApplySpawnPoint(lvl *Level) bool {
	p := FindSpawnPoint(lvl)
	if p == nil {
		return false
	}

	Cam.Yaw, Cam.Pitch = SpawnYawPitch(p)
	Cam.Position = p.Position.Add(mgl32.Vec3{0, eyeHeight, 0})
	updateCamDirection()
	updateCamMatrix()

	if PlayerEntity != nil {
		PlayerEntity.Position = p.Position
		PlayerEntity.Velocity = mgl32.Vec3{}
	}
	// Spawning mid-air is legitimate (a marker over a ledge), so let gravity
	// re-establish ground contact rather than asserting it here.
	IsOnGround = false
	JumpRequested = false
	return true
}

// ---------- arrow mesh ----------
//
// A shaft box plus a pyramid head, pointing along +X, sized so it reads as a
// marker next to a 1.7-unit player rather than as a piece of level geometry.
const (
	spawnShaftBackX  = float32(-0.45) // tail
	spawnShaftFrontX = float32(0.1)   // where the shaft meets the head base
	spawnTipX        = float32(0.5)   // apex
	spawnShaftHalfY  = float32(0.045)
	spawnShaftHalfZ  = float32(0.055)
	spawnHeadHalfY   = float32(0.13)
	spawnHeadHalfZ   = float32(0.18)
)

// generateSpawnArrow creates the spawn marker mesh: a unit-long arrow pointing
// along +X, lifted clear of the floor plane.
func generateSpawnArrow() ([][]float32, []int) {
	var verts [][]float32
	var idx []int

	y0 := spawnArrowLift - spawnShaftHalfY
	y1 := spawnArrowLift + spawnShaftHalfY
	z0, z1 := -spawnShaftHalfZ, spawnShaftHalfZ
	x0, x1 := spawnShaftBackX, spawnShaftFrontX

	// Shaft box. Corner order per face is CCW seen from outside; addQuad emits the
	// index pattern that matches the winding generateRectPrism uses.
	addQuad(&verts, &idx, // +Y
		[3]float32{x0, y1, z1}, [3]float32{x1, y1, z1}, [3]float32{x1, y1, z0}, [3]float32{x0, y1, z0})
	addQuad(&verts, &idx, // -Y
		[3]float32{x0, y0, z0}, [3]float32{x1, y0, z0}, [3]float32{x1, y0, z1}, [3]float32{x0, y0, z1})
	addQuad(&verts, &idx, // +Z
		[3]float32{x0, y0, z1}, [3]float32{x1, y0, z1}, [3]float32{x1, y1, z1}, [3]float32{x0, y1, z1})
	addQuad(&verts, &idx, // -Z
		[3]float32{x1, y0, z0}, [3]float32{x0, y0, z0}, [3]float32{x0, y1, z0}, [3]float32{x1, y1, z0})
	addQuad(&verts, &idx, // -X (tail cap)
		[3]float32{x0, y0, z0}, [3]float32{x0, y0, z1}, [3]float32{x0, y1, z1}, [3]float32{x0, y1, z0})

	// Head: a rectangular base at spawnShaftFrontX rising to a point at spawnTipX.
	hy0 := spawnArrowLift - spawnHeadHalfY
	hy1 := spawnArrowLift + spawnHeadHalfY
	hz0, hz1 := -spawnHeadHalfZ, spawnHeadHalfZ
	bx := spawnShaftFrontX
	tip := [3]float32{spawnTipX, spawnArrowLift, 0}

	bl := [3]float32{bx, hy0, hz1}
	br := [3]float32{bx, hy0, hz0}
	tr := [3]float32{bx, hy1, hz0}
	tl := [3]float32{bx, hy1, hz1}

	// Base cap faces -X, away from the tip.
	addQuad(&verts, &idx, br, bl, tl, tr)
	// Four sloping faces, each wound CCW seen from outside the pyramid.
	addTri(&verts, &idx, bl, br, tip) // -Y
	addTri(&verts, &idx, br, tr, tip) // -Z
	addTri(&verts, &idx, tr, tl, tip) // +Y
	addTri(&verts, &idx, tl, bl, tip) // +Z

	return verts, idx
}

// addQuad appends one textured quad. a,b,c,d run counter-clockwise as seen from
// the front of the face; the emitted indices reverse that, matching the winding
// every other primitive generator uses (see generateRectPrism).
func addQuad(verts *[][]float32, idx *[]int, a, b, c, d [3]float32) {
	base := len(*verts)
	*verts = append(*verts,
		[]float32{a[0], a[1], a[2], 0, 0},
		[]float32{b[0], b[1], b[2], 1, 0},
		[]float32{c[0], c[1], c[2], 1, 1},
		[]float32{d[0], d[1], d[2], 0, 1},
	)
	*idx = append(*idx, base, base+2, base+1, base, base+3, base+2)
}

// addTri appends one textured triangle, same winding convention as addQuad.
func addTri(verts *[][]float32, idx *[]int, a, b, c [3]float32) {
	base := len(*verts)
	*verts = append(*verts,
		[]float32{a[0], a[1], a[2], 0, 0},
		[]float32{b[0], b[1], b[2], 1, 0},
		[]float32{c[0], c[1], c[2], 0.5, 1},
	)
	*idx = append(*idx, base, base+2, base+1)
}

// spawnPointBounds returns the marker's half-extents for the given scale. Used
// only for frustum culling and as the picking fallback — the mesh itself is what
// clicks actually hit.
func spawnPointBounds(scale mgl32.Vec3) (hx, hy, hz float32) {
	return 0.5 * scale.X(), 0.35 * scale.Y(), 0.25 * scale.Z()
}
