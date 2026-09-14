package world

// spawn_point.go — the player spawn point: where gameplay starts, and which way
// it faces.
//
// It used to ride on the primitive system, inheriting placement, picking and
// save/load from it for free. Primitives are no longer part of a level, so the
// spawn is now its own first-class thing: one optional SpawnPoint per level,
// written under the top-level `spawn:` key. What it kept from the primitive
// days is the part that was always specific to it — an arrow mesh drawn only in
// the editor, a Position that is the player's *feet* rather than their eyes,
// and no collision of any kind.
//
// Facing is derived from Rotation by transforming the mesh's local forward axis
// (+X, matching yaw 0 in DirectionFromAngles) through that rotation, so the
// arrow the user sees in the viewport and the direction the player looks are the
// same vector by construction — no separate yaw field to drift.

import (
	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/platform"
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// spawnArrowLift raises the arrow mesh off the floor plane. Position is the
// player's feet, so an unlifted marker would z-fight with the floor it sits on.
const spawnArrowLift = float32(0.16)

// spawnBodyLift is how far above Position the player's body is put. Position is
// the feet, which is where the arrow sits, but collision places the body by its
// centre (MovePlayer resolves a cylinder centred on PlayerEntity.Position). A
// body put on the marker itself started half-buried in the floor, and the first
// tick shoved it out, taking the camera with it — so the view the player got
// was half a body higher than the one the spawn had set.
const spawnBodyLift = playerHalfHeight

// SpawnEyeHeight is how far above a spawn point's Position the camera starts:
// the body's lift, plus the EyeHeight the frame loop places the camera above the
// body at. Anything turning a camera into a spawn position uses it too, so a
// spawn made from a view gives that same view back.
const SpawnEyeHeight = spawnBodyLift + EyeHeight

// spawnMarkerTexture is what the arrow is drawn with. It is an editor-only
// marker, so this only ever needs to be a texture that exists.
const spawnMarkerTexture = "default"

// spawnArrowForward is the arrow's local pointing axis. +X is yaw 0 in
// DirectionFromAngles, which makes an unrotated spawn point face the same way an
// unrotated camera does.
var spawnArrowForward = mgl32.Vec3{1, 0, 0}

// SpawnPoint is a level's player start: a position (the player's feet), a
// facing, and the arrow mesh that shows both in the editor.
type SpawnPoint struct {
	Position mgl32.Vec3
	Rotation mgl32.Vec3 // Euler angles (X, Y, Z) in radians
	// Frame is the vehicle to start aboard, and Seat which of its seats. With
	// Frame set, Position and Rotation are in that vehicle's coordinates.
	Frame string
	Seat  string
	// DisableControl starts the player with their keys off, and DisableGravity
	// starts them not falling. Both are how the level begins, not how it stays:
	// a script gives either back with @level.prop.player_control and
	// player_gravity. Named as opt-outs so a spawn that never set them is the
	// ordinary walking start.
	DisableControl bool
	DisableGravity bool
	Geometry       gfx.Geometry
}

// ModelMatrix builds the marker's model matrix, composed in the same Y*X*Z
// order SpawnDirection derives its facing from.
func (s *SpawnPoint) ModelMatrix() mgl32.Mat4 {
	T := mgl32.Translate3D(s.Position.X(), s.Position.Y(), s.Position.Z())
	return T.Mul4(spawnRotationMatrix(s.Rotation))
}

// Load uploads the arrow mesh. Safe to call repeatedly — the previous upload is
// released first, which is what makes it usable as a "rebuild after an edit".
func (s *SpawnPoint) Load() error {
	s.Geometry.Delete()
	verts, idx := generateSpawnArrow()
	if err := gfx.EnsureTextureLoaded(spawnMarkerTexture); err != nil {
		return err
	}
	return s.Geometry.Load(verts, idx, spawnMarkerTexture, s.Position)
}

// Delete releases the marker's GPU buffers.
func (s *SpawnPoint) Delete() { s.Geometry.Delete() }

// WorldAABB returns the marker's world-space bounds, used for frustum culling
// and as the picking fallback.
func (s *SpawnPoint) WorldAABB() (mgl32.Vec3, mgl32.Vec3) {
	hx, hy, hz := SpawnPointBounds()
	h := mgl32.Vec3{hx, hy, hz}
	if s.Rotation != (mgl32.Vec3{}) {
		// A rotated marker uses the extents' length as a uniform radius so the
		// box stays valid at any orientation.
		r := h.Len()
		h = mgl32.Vec3{r, r, r}
	}
	return s.Position.Sub(h), s.Position.Add(h)
}

// spawnRotationMatrix composes an Euler triple the one way everything here
// agrees on: Y, then X, then Z.
func spawnRotationMatrix(rot mgl32.Vec3) mgl32.Mat4 {
	return mgl32.HomogRotate3DY(rot.Y()).
		Mul4(mgl32.HomogRotate3DX(rot.X())).
		Mul4(mgl32.HomogRotate3DZ(rot.Z()))
}

// SpawnDirection returns the unit direction a spawn point faces, derived from its
// Euler rotation the same way ModelMatrix builds it, so it always agrees with
// the arrow on screen.
func SpawnDirection(s *SpawnPoint) mgl32.Vec3 {
	d := spawnRotationMatrix(s.Rotation).Mul4x1(spawnArrowForward.Vec4(0)).Vec3()
	if d.Len() < 1e-6 {
		return spawnArrowForward
	}
	return d.Normalize()
}

// SpawnYawPitch returns the camera angles (radians) a spawn point spawns the
// player with. Pitch is clamped to the same limit mouse-look enforces so a
// straight-up marker can't hand gameplay a gimbal-flipped camera.
func SpawnYawPitch(s *SpawnPoint) (yaw, pitch float32) {
	d := SpawnDirection(s)
	yaw = float32(math.Atan2(float64(d.Z()), float64(d.X())))
	pitch = float32(math.Asin(float64(geom.ClampValue(d.Y(), -1, 1))))
	if pitch > gfx.MaxPitch {
		pitch = gfx.MaxPitch
	}
	if pitch < -gfx.MaxPitch {
		pitch = -gfx.MaxPitch
	}
	return yaw, pitch
}

// FindSpawnPoint returns the level's spawn point, or nil when it has none.
func FindSpawnPoint(lvl *Level) *SpawnPoint {
	if lvl == nil {
		return nil
	}
	return lvl.Spawn
}

// ApplySpawnPoint teleports the player and camera to the level's spawn point and
// reports whether one was found. Callers fall back to whatever camera they already
// had when it returns false, so a level with no spawn point behaves as before.
//
// It also owns the player's control and gravity switches for the start of play.
// They are reset before the nil check, so arriving in a level with no spawn
// does not inherit a frozen player from the level that was just left.
func ApplySpawnPoint(lvl *Level) bool {
	SetPlayerControl(true)
	SetPlayerGravity(true)
	// A crouch is not carried into a new level or a new session either.
	ResetPlayerStance()

	s := FindSpawnPoint(lvl)
	if s == nil {
		return false
	}
	SetPlayerControl(!s.DisableControl)
	SetPlayerGravity(!s.DisableGravity)

	gfx.Cam.Yaw, gfx.Cam.Pitch = SpawnYawPitch(s)

	if PlayerEntity != nil {
		PlayerEntity.Position = s.Position.Add(mgl32.Vec3{0, spawnBodyLift, 0})
		PlayerEntity.Velocity = mgl32.Vec3{}
		// Starting aboard is the same state mounting produces, so it is set the
		// same way rather than through a second path: a frame, and optionally a
		// seat. Position is already in that frame's coordinates.
		PlayerEntity.Frame = s.Frame
		PlayerEntity.Seat = ""
		if s.Frame != "" {
			if v := FindEntity(lvl, s.Frame); v != nil && v.IsVehicle() {
				st := v.EnsureVehicleState()
				st.AddOccupant(PlayerEntity.ID, s.Seat)
				PlayerEntity.Seat = s.Seat
			}
		}
	}

	// The camera follows the player into whatever frame it landed in, so a
	// spawn aboard a ship looks out of the ship rather than from where the ship
	// happens to sit in world coordinates.
	f := lvl.Frames.Frame(s.Frame)
	gfx.Cam.Position = f.ToWorld(s.Position.Add(mgl32.Vec3{0, SpawnEyeHeight, 0}))
	gfx.UpdateCamDirection()
	gfx.Cam.Direction = f.DirToWorld(gfx.Cam.Direction)
	gfx.Cam.Up = f.DirToWorld(gfx.WorldUp)
	gfx.UpdateCamMatrix()
	// Spawning mid-air is legitimate (a marker over a ledge), so let gravity
	// re-establish ground contact rather than asserting it here.
	platform.IsOnGround = false
	platform.JumpRequested = false
	return true
}

// SetPlayerControl turns the player's movement keys on or off. A jump already
// queued is dropped with them, or it would fire on the frame control is lost.
func SetPlayerControl(on bool) {
	platform.PlayerControlEnabled = on
	if !on {
		platform.JumpRequested = false
	}
}

// SetPlayerGravity turns gravity on or off for the player.
//
// Turning it off also stops any fall in progress. MovePlayer carries vertical
// velocity forward whether gravity is on or not and only zeroes it on landing,
// which it checks only with gravity on — so a player caught mid-fall would
// otherwise keep sinking at that speed forever, straight through the floor.
func SetPlayerGravity(on bool) {
	platform.GravityEnabled = on
	platform.IsOnGround = false
	platform.JumpRequested = false
	if !on && PlayerEntity != nil {
		PlayerEntity.Velocity[1] = 0
	}
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
	// index pattern that matches the winding the arrow's other faces use.
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
// the head's sloping faces use (see addTri).
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

// SpawnPointBounds returns the marker's half-extents. Used only for frustum
// culling and as the picking fallback — the mesh itself is what clicks actually
// hit. The marker has no scale of its own: it is a fixed-size annotation on the
// level, not geometry in it.
func SpawnPointBounds() (hx, hy, hz float32) {
	return 0.5, 0.35, 0.25
}
