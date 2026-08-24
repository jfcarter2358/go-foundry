package main

// editor_3d_camera.go — the 3D view's two camera modes.
//
// The 3D view used to fly and only fly. It now carries the same pair the model
// editor does (editor_model_window.go): fly for travelling a level, orbit for
// turning around whatever is selected. The gestures are deliberately the same
// in both viewports — V swaps modes, RMB/MMB turns, Shift+MMB pans, the wheel
// zooms, F frames the selection — so the two editors are driven alike.
//
// Unlike the model editor this camera cannot own its own state: Cam (camera.go)
// is global and every pass in the frame reads it. So only the orbit half lives
// here, and it is projected onto Cam rather than replacing it — Yaw and Pitch
// stay the *look* direction, and orbit derives the eye from them
// (eye = Target - look*Dist) exactly as modelCamera.position() does. That is
// what lets a mode switch happen without the view jumping, and it leaves fly
// mode behaving precisely as it did before.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// editorCam3D is the orbit half of the 3D view's camera. Fly is the mode
// switch; Target and Dist are only read while orbiting.
var editorCam3D = struct {
	Fly    bool
	Target mgl32.Vec3
	Dist   float32
}{Fly: true, Dist: 8}

const (
	editorCamMinDist float32 = 0.2
	editorCamMaxDist float32 = 500
)

// editorApplyCamAngles rebuilds Cam from Yaw/Pitch. Flying leaves the eye where
// it is and only turns the look; orbiting swings the eye around the pivot,
// which is the whole difference between the two modes.
func editorApplyCamAngles() {
	updateCamDirection()
	if !editorCam3D.Fly {
		editorCam3D.Dist = clampValue(editorCam3D.Dist, editorCamMinDist, editorCamMaxDist)
		Cam.Position = editorCam3D.Target.Sub(Cam.Direction.Mul(editorCam3D.Dist))
	}
	updateCamMatrix()
}

// editorSetFlyCam switches modes without moving the eye or changing where it
// looks. Only the derived half of the state is recomputed: fly simply keeps the
// eye the orbit left it at, orbit puts its pivot back out in front at the
// current distance.
func editorSetFlyCam(fly bool) {
	if editorCam3D.Fly == fly {
		return
	}
	editorCam3D.Fly = fly
	updateCamDirection()
	if !fly {
		editorCam3D.Dist = clampValue(editorCam3D.Dist, editorCamMinDist, editorCamMaxDist)
		editorCam3D.Target = Cam.Position.Add(Cam.Direction.Mul(editorCam3D.Dist))
	}
	editorApplyCamAngles()
}

// editorCamEnter re-derives the orbit pivot from wherever the camera ended up.
// Called when the editor is opened, so an orbit left over from the last session
// in the editor does not pull the view back to a pivot the player has since
// walked away from.
func editorCamEnter() {
	updateCamDirection()
	editorCam3D.Dist = clampValue(editorCam3D.Dist, editorCamMinDist, editorCamMaxDist)
	editorCam3D.Target = Cam.Position.Add(Cam.Direction.Mul(editorCam3D.Dist))
}

// editorCamLook applies a mouse-look delta. One path for both modes: the angles
// change either way, and editorApplyCamAngles decides whether that turns the
// head or swings the eye.
func editorCamLook(dx, dy int32) {
	Cam.Yaw += MouseRotationSpeed * float32(dx)
	Cam.Pitch -= MouseRotationSpeed * float32(dy)
	Cam.Pitch = clampValue(Cam.Pitch, -maxPitch, maxPitch)
	editorApplyCamAngles()
}

// editorCamRight and editorCamUp are the camera's screen-aligned axes, so a pan
// slides the view in the plane being looked at rather than along world axes.
func editorCamRight() mgl32.Vec3 {
	r := Cam.Direction.Cross(WorldUp)
	if r.Len() < 1e-5 {
		return mgl32.Vec3{1, 0, 0}
	}
	return r.Normalize()
}

func editorCamUp() mgl32.Vec3 {
	return editorCamRight().Cross(Cam.Direction).Normalize()
}

// editorCamPan slides the view sideways and vertically by a mouse delta. Scaled
// by the orbit radius so the world tracks the cursor at any zoom level.
func editorCamPan(dx, dy int32) {
	scale := editorCam3D.Dist * 0.0015
	delta := editorCamRight().Mul(-float32(dx) * scale).
		Add(editorCamUp().Mul(float32(dy) * scale))
	editorCamTranslate(delta)
}

// editorCamTranslate moves the view by a world-space delta, moving whichever of
// the eye and the pivot is the authoritative one in the current mode.
func editorCamTranslate(delta mgl32.Vec3) {
	if editorCam3D.Fly {
		Cam.Position = Cam.Position.Add(delta)
	} else {
		editorCam3D.Target = editorCam3D.Target.Add(delta)
	}
	editorApplyCamAngles()
}

// editorCamZoom handles a wheel notch. Orbiting shrinks the radius, which walks
// the eye towards the pivot; flying has no radius, so the wheel dollies the eye
// along the look direction instead — the same split the model editor makes.
func editorCamZoom(wheelY int32) {
	if wheelY == 0 {
		return
	}
	if editorCam3D.Fly {
		speed := MovementSpeed * DeltaTime * 60
		if speed <= 0 {
			speed = MovementSpeed
		}
		Cam.Position = Cam.Position.Add(Cam.Direction.Mul(float32(wheelY) * speed * 4))
		editorApplyCamAngles()
		return
	}
	// Multiplicative, so each notch covers the same visual fraction whether the
	// camera is close in or far out.
	factor := float32(math.Pow(0.88, float64(wheelY)))
	editorCam3D.Dist = clampValue(editorCam3D.Dist*factor, editorCamMinDist, editorCamMaxDist)
	editorApplyCamAngles()
}

// editorCamMove is WASD and the panel's navigation buttons. Flying moves the
// eye through MoveCamera, as it always has. Orbiting moves the *pivot* instead
// of doing nothing (which is what the model editor does): a level is far larger
// than one model, and dead movement keys in half the modes would be a
// regression from a viewport whose only way to travel is to fly.
func editorCamMove(f, u, r float32) {
	if editorCam3D.Fly {
		MoveCamera(f, u, r)
		return
	}
	// Flattened forward and world up, matching MoveCamera, so orbiting and
	// flying answer the keys the same way.
	d := Cam.Direction
	forward := mgl32.Vec3{d.X(), 0, d.Z()}
	if forward.Len() > 1e-6 {
		forward = forward.Normalize()
	}
	right := mgl32.Vec3{d.Z(), 0, -d.X()}
	if right.Len() > 1e-6 {
		right = right.Normalize()
	}
	editorCamTranslate(forward.Mul(f).Add(WorldUp.Mul(u)).Add(right.Mul(r)))
}

// editorOrbitKeys is EditorTick's keyboard path while orbiting. Fly mode hands
// straight to HandleInput (input.go) and behaves as it always has; this is the
// same set of keys read against editorCamMove, so they slide the pivot instead
// of the eye.
func editorOrbitKeys(keyStates []uint8) {
	dtScale := DeltaTime * 60.0
	moveSpeed := MovementSpeed * dtScale
	rotSpeed := KeyRotationSpeed * dtScale

	var f, u, r float32
	if keyStates[sdl.SCANCODE_W] != 0 {
		f += moveSpeed
	}
	if keyStates[sdl.SCANCODE_S] != 0 {
		f -= moveSpeed
	}
	// A and D carry the same signs MoveCamera gives them, so strafing points
	// the same way in both camera modes.
	if keyStates[sdl.SCANCODE_A] != 0 {
		r += moveSpeed
	}
	if keyStates[sdl.SCANCODE_D] != 0 {
		r -= moveSpeed
	}
	if keyStates[sdl.SCANCODE_SPACE] != 0 {
		u += moveSpeed
	}
	if keyStates[sdl.SCANCODE_LSHIFT] != 0 {
		u -= moveSpeed
	}

	if keyStates[sdl.SCANCODE_LEFT] != 0 {
		Cam.Yaw -= rotSpeed
	}
	if keyStates[sdl.SCANCODE_RIGHT] != 0 {
		Cam.Yaw += rotSpeed
	}
	if keyStates[sdl.SCANCODE_UP] != 0 {
		Cam.Pitch += rotSpeed
	}
	if keyStates[sdl.SCANCODE_DOWN] != 0 {
		Cam.Pitch -= rotSpeed
	}
	Cam.Pitch = clampValue(Cam.Pitch, -maxPitch, maxPitch)

	editorCamMove(f, u, r)
	editorApplyCamAngles()
}

// editorNavMove is where the side panel's navigation buttons go. Only the 3D
// view has an orbit mode to respect; every other mode always flies.
func editorNavMove(mode EditorMode, f, u, r float32) {
	if mode == Mode3D {
		editorCamMove(f, u, r)
		return
	}
	MoveCamera(f, u, r)
}

// ---------- framing ----------

// editorFrameSelection points the camera at whatever is selected, falling back
// to the active chunk when nothing is. Always lands in orbit mode: the point of
// framing something is to look at it from the outside.
func editorFrameSelection() {
	min, max, ok := editorSelectionBounds()
	if !ok {
		return
	}
	center := min.Add(max).Mul(0.5)
	radius := max.Sub(min).Len() / 2
	if radius < 0.25 {
		radius = 0.25
	}
	// The main projection is a 45 degree vertical FOV, so half of it is 22.5;
	// the margin keeps the subject off the window edges.
	dist := radius / float32(math.Tan(float64(mgl32.DegToRad(22.5)))) * 1.4

	editorCam3D.Fly = false
	editorCam3D.Target = center
	editorCam3D.Dist = clampValue(dist, editorCamMinDist, editorCamMaxDist)
	editorApplyCamAngles()
}

// editorSelectionBounds is the world AABB of the current selection, in the same
// priority order the panel shows it: vertices, then the spawn point, then the
// selected entity, then the selected volume, then the water body, then the
// sector, then the active chunk.
func editorSelectionBounds() (mgl32.Vec3, mgl32.Vec3, bool) {
	lvl := Editor.level
	if lvl == nil {
		return mgl32.Vec3{}, mgl32.Vec3{}, false
	}

	if len(Editor.SelectedVertices) > 0 {
		var min, max mgl32.Vec3
		found := false
		for _, sel := range Editor.SelectedVertices {
			pos, ok := vertexSelPos(sel)
			if !ok {
				continue
			}
			if !found {
				min, max, found = pos, pos, true
				continue
			}
			min, max = vecMin(min, pos), vecMax(max, pos)
		}
		if found {
			return min, max, true
		}
	}

	if Editor.SpawnSelected && lvl.Spawn != nil {
		smin, smax := lvl.Spawn.WorldAABB()
		return smin, smax, true
	}

	if e := activeEntity(); e != nil {
		if emin, emax, ok := e.WorldAABB(); ok {
			return emin, emax, true
		}
		// No authored volume to frame, so frame the point it sits at and let
		// the caller's minimum radius decide how close that ends up.
		return e.Position, e.Position, true
	}

	if v := activeVolume(); v != nil {
		if vmin, vmax, ok := v.WorldAABB(); ok {
			return vmin, vmax, true
		}
		return v.Position, v.Position, true
	}

	if Editor.ActiveChunkIdx >= 0 && Editor.ActiveChunkIdx < len(lvl.Chunks) {
		ch := &lvl.Chunks[Editor.ActiveChunkIdx]
		// A water body is picked as its biome sector, and the thing to frame is
		// the plane that sector produced — the polygon itself has no height.
		if Editor.ActiveBiomeSectorIdx >= 0 && Editor.ActiveBiomeSectorIdx < len(ch.BiomeSectors) {
			id := ch.BiomeSectors[Editor.ActiveBiomeSectorIdx].ID
			for wi := range ch.WaterSurfaces {
				if ch.WaterSurfaces[wi].ID == id {
					wmin, wmax := ch.WaterSurfaces[wi].WorldAABB()
					return wmin, wmax, true
				}
			}
		}
		if Editor.ActiveSectorIdx >= 0 && Editor.ActiveSectorIdx < len(ch.Sectors) {
			s := &ch.Sectors[Editor.ActiveSectorIdx]
			return mgl32.Vec3{s.MinBound.X(), s.Floor.Height, s.MinBound.Y()},
				mgl32.Vec3{s.MaxBound.X(), s.Ceiling.Height, s.MaxBound.Y()}, true
		}
		return ch.MinBound, ch.MaxBound, true
	}

	return mgl32.Vec3{}, mgl32.Vec3{}, false
}

// vecMin and vecMax are the component-wise pair used to grow an AABB.
func vecMin(a, b mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{
		float32(math.Min(float64(a.X()), float64(b.X()))),
		float32(math.Min(float64(a.Y()), float64(b.Y()))),
		float32(math.Min(float64(a.Z()), float64(b.Z()))),
	}
}

func vecMax(a, b mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{
		float32(math.Max(float64(a.X()), float64(b.X()))),
		float32(math.Max(float64(a.Y()), float64(b.Y()))),
		float32(math.Max(float64(a.Z()), float64(b.Z()))),
	}
}

// editorCameraLabel names the camera toggle by the mode it is in, matching the
// model editor's button.
func editorCameraLabel() string {
	if editorCam3D.Fly {
		return "Fly [V]"
	}
	return "Orbit [V]"
}
