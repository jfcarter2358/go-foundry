package gfx

// camera_rig.go — a camera a script drives.
//
// By default the camera is the player's eye: sim.PlacePlayerCamera puts it on
// the player every frame, and the player's look angles (Cam.Yaw, Cam.Pitch)
// turn it. A cutscene, an orbit around a unit, a fixed security-camera angle
// or a strategy view all want the camera to be somewhere else, and to get
// there smoothly. That is this: a pose of its own, blends on each of its
// channels, and a switch that says whether it or the player owns the view.
//
// # Ownership
//
// The rig never touches Cam.Yaw or Cam.Pitch. Those are the player's look —
// what the save game records, what a re-attached camera goes back to, and
// what the player's aim is taken from — and a camera flying off to film a
// door has no business rewriting them. The rig writes only what is drawn
// with: Cam.Position, Cam.Direction, Cam.Up and the view matrix.
//
// It writes them twice a frame (sim.Step). HoldCameraRig lays the current pose
// back down the moment PlacePlayerCamera has put the player's eye there, so
// everything in the middle of the frame — the script tick's picks and casts
// above all — sees the camera the picture is taken from. StepCameraRig, after
// the script tick, moves the pose on, so a verb called this frame moves this
// frame's picture. Attaching again reads the player's eye out of what
// PlacePlayerCamera placed, which is what makes it cheap.
//
// # Angles
//
// Yaw and pitch here are the engine camera's own: yaw 0 looks along +X and a
// positive yaw turns right (toward +Z), pitch is positive up. The script
// surface speaks degrees; everything in this file is radians.
//
// Runtime only. ResetCameraRig puts the player back in charge whenever the
// script runtime is torn down, so a camera left detached when play ends does
// not strand the editor or the next level.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// RigTarget is something the rig looks at or orbits: a fixed point, or an
// object the script re-resolves every frame (Ref non-nil). gfx cannot see the
// world, so an object's position is written into Point from outside — see
// script.RefreshCameraTargets — and Lost is set when it no longer resolves,
// at which point the rig keeps using the last Point it had.
type RigTarget struct {
	Point mgl32.Vec3
	Ref   any
	Lost  bool
}

// floatBlend eases one number from where it was to where it is going.
type floatBlend struct {
	active           bool
	from, to         float32
	elapsed, seconds float32
	ease             Ease
}

func (b *floatBlend) start(from, to, seconds float32, e Ease) {
	*b = floatBlend{active: true, from: from, to: to, seconds: seconds, ease: e}
}

// step advances the blend and returns the value it has reached. The last step
// lands on the target exactly and switches the blend off.
func (b *floatBlend) step(dt float32) float32 {
	b.elapsed += dt
	if !(b.seconds > 0) || b.elapsed >= b.seconds {
		b.active = false
		return b.to
	}
	return b.from + (b.to-b.from)*b.ease.Apply(b.elapsed/b.seconds)
}

// fraction is how far through the blend it is, eased. An instant blend is
// already there.
func (b *floatBlend) fraction() float32 {
	if !(b.seconds > 0) || b.elapsed >= b.seconds {
		return 1
	}
	return b.ease.Apply(b.elapsed / b.seconds)
}

// vecBlend is floatBlend for a position.
type vecBlend struct {
	active           bool
	from, to         mgl32.Vec3
	elapsed, seconds float32
	ease             Ease
}

func (b *vecBlend) start(from, to mgl32.Vec3, seconds float32, e Ease) {
	*b = vecBlend{active: true, from: from, to: to, seconds: seconds, ease: e}
}

func (b *vecBlend) step(dt float32) mgl32.Vec3 {
	b.elapsed += dt
	if !(b.seconds > 0) || b.elapsed >= b.seconds {
		b.active = false
		return b.to
	}
	return b.from.Add(b.to.Sub(b.from).Mul(b.ease.Apply(b.elapsed / b.seconds)))
}

// rigOrbit swings the camera round a target at a distance, always looking at
// it. Its yaw and pitch are the camera's own facing, so "orbit yaw 90" and
// "look along +Z" are the same camera.
type rigOrbit struct {
	on                   bool
	target               RigTarget
	distance, yaw, pitch float32
	dist, yawB, pitchB   floatBlend
	// A new target does not teleport the camera: the centre slides from the
	// old one to the new over the call's blend, measured against the new
	// target's live position so a moving target is still arrived at.
	centreFrom  mgl32.Vec3
	centreBlend floatBlend
}

// rigAim keeps the camera pointed at a target: a turn toward it over the
// blend, then held on it for as long as it is set — re-aimed every frame, so
// a moving target stays in the middle of the frame.
type rigAim struct {
	on                 bool
	target             RigTarget
	fromYaw, fromPitch float32
	turn               floatBlend
}

// CameraRig is the script camera's whole state.
type CameraRig struct {
	// Detached says the rig owns the view. Off, the camera is the player's.
	Detached bool

	// The rig's pose, in world space.
	Pos              mgl32.Vec3
	Yaw, Pitch, Roll float32

	move          vecBlend
	yaw, pitch    floatBlend
	roll          floatBlend
	orbit         rigOrbit
	aim           rigAim
	fov           floatBlend
	ret           floatBlend // attaching again: 0 is the rig's pose, 1 the player's
	retFrom       mgl32.Vec3
	retYaw, retPi float32
	retRoll       float32
	returning     bool

	// eye is the player's eye as PlacePlayerCamera left it this frame, kept
	// by HoldCameraRig before it lays the blend's last pose back over the
	// camera — the return blend's far end. shown is that last pose.
	eye, shown         camPose
	eyeValid, shownSet bool
}

// camPose is everything the rig writes into the camera.
type camPose struct{ pos, dir, up mgl32.Vec3 }

func (p camPose) write() {
	Cam.Position, Cam.Direction, Cam.Up = p.pos, p.dir, p.up
	RebuildCamMatrix()
}

// Rig is the one script camera.
var Rig CameraRig

// LookDelta is this frame's mouse and look-stick look, in degrees: X the turn
// to the right, Y the turn upward. Written by the frame loop from the same
// motion that turns the player's view — with sensitivity, smoothing, the
// stick's curve and invert-Y already applied — and ALSO written while the
// camera is detached, when that motion turns nothing: which is how a script
// drives an orbit camera from the mouse or a pad.
var LookDelta mgl32.Vec2

// ResetCameraRig gives the view back to the player at once and forgets every
// blend. The field of view is the caller's to reset: it has its own default
// and its own owner (ScriptRuntimeReset).
func ResetCameraRig() {
	Rig = CameraRig{}
	LookDelta = mgl32.Vec2{}
}

// CameraDetached reports whether a script owns the view.
func CameraDetached() bool { return Rig.Detached }

// CameraReturning reports whether the view is blending back to the player.
func CameraReturning() bool { return Rig.returning }

// AnglesFromDirection is the yaw and pitch a direction looks along, in the
// camera's convention. A zero vector answers zero.
func AnglesFromDirection(d mgl32.Vec3) (yaw, pitch float32) {
	flat := math.Hypot(float64(d.X()), float64(d.Z()))
	if flat < 1e-9 && math.Abs(float64(d.Y())) < 1e-9 {
		return 0, 0
	}
	return float32(math.Atan2(float64(d.Z()), float64(d.X()))),
		float32(math.Atan2(float64(d.Y()), flat))
}

// CameraDetach hands the view to the rig where it is now: same position, same
// facing, so detaching on its own is invisible. Detaching an already detached
// camera does nothing; detaching one that is still blending back to the
// player catches it wherever the blend had got to.
func CameraDetach() {
	if Rig.Detached {
		return
	}
	yaw, pitch := AnglesFromDirection(Cam.Direction)
	Rig.Pos = Cam.Position
	Rig.Yaw, Rig.Pitch = yaw, clampPitch(pitch)
	Rig.Roll = currentRoll()
	Rig.Detached = true
	Rig.returning = false
	Rig.move, Rig.yaw, Rig.pitch, Rig.roll = vecBlend{}, floatBlend{}, floatBlend{}, floatBlend{}
	Rig.orbit, Rig.aim = rigOrbit{}, rigAim{}
}

// currentRoll is the roll the view has now: nonzero only while the rig is
// holding one, since the player's own camera never rolls against its up.
func currentRoll() float32 {
	if Rig.Detached || Rig.returning {
		return Rig.Roll
	}
	return 0
}

// CameraAttach gives the view back to the player, blending from the rig's pose
// to the player's eye over seconds (at once for zero or less). The blend is
// re-aimed every frame, so a player walking while it runs is still where the
// view ends up.
func CameraAttach(seconds float32, e Ease) {
	if !Rig.Detached {
		return
	}
	Rig.Detached = false
	Rig.move, Rig.yaw, Rig.pitch, Rig.roll = vecBlend{}, floatBlend{}, floatBlend{}, floatBlend{}
	Rig.orbit, Rig.aim = rigOrbit{}, rigAim{}
	if !(seconds > 0) {
		Rig.returning = false
		Rig.Roll = 0
		return
	}
	Rig.returning = true
	Rig.retFrom, Rig.retYaw, Rig.retPi, Rig.retRoll = Rig.Pos, Rig.Yaw, Rig.Pitch, Rig.Roll
	Rig.ret.start(0, 1, seconds, e)
	// Until the first step of the blend, the view is still the rig's pose.
	Rig.shown = camPose{Rig.Pos, DirectionFromAngles(Rig.Yaw, Rig.Pitch), rolledUp(DirectionFromAngles(Rig.Yaw, Rig.Pitch), Rig.Roll)}
	Rig.shownSet, Rig.eyeValid = true, false
}

// CameraMoveTo slides the camera to a point. It stops an orbit — the two would
// both be deciding where the camera is — but not a look, so a camera can dolly
// past something while staying on it.
func CameraMoveTo(p mgl32.Vec3, seconds float32, e Ease) {
	CameraDetach()
	Rig.orbit.on = false
	Rig.move.start(Rig.Pos, p, seconds, e)
}

// CameraTurnTo pans and tilts the camera to a facing. Orbiting, the same call
// swings the camera round its target to look from that direction instead,
// which is what "turn" means for a camera that must keep its subject in frame.
// It ends a look, which would otherwise take the facing straight back.
func CameraTurnTo(yaw, pitch, seconds float32, e Ease) {
	CameraDetach()
	Rig.aim.on = false
	pitch = clampPitch(pitch)
	if Rig.orbit.on {
		Rig.orbit.yawB.start(Rig.orbit.yaw, nearestAngle(Rig.orbit.yaw, yaw), seconds, e)
		Rig.orbit.pitchB.start(Rig.orbit.pitch, pitch, seconds, e)
		return
	}
	Rig.yaw.start(Rig.Yaw, nearestAngle(Rig.Yaw, yaw), seconds, e)
	Rig.pitch.start(Rig.Pitch, pitch, seconds, e)
}

// CameraRollTo tilts the horizon: a dutch angle, a ship banking.
func CameraRollTo(roll, seconds float32, e Ease) {
	CameraDetach()
	Rig.roll.start(Rig.Roll, nearestAngle(Rig.Roll, roll), seconds, e)
}

// CameraLookAt turns the camera toward a target over seconds and keeps it
// there until CameraStopLook or another facing is asked for. An orbit already
// looks at its own target, so this ends one.
func CameraLookAt(t RigTarget, seconds float32, e Ease) {
	CameraDetach()
	Rig.orbit.on = false
	Rig.yaw, Rig.pitch = floatBlend{}, floatBlend{}
	Rig.aim = rigAim{on: true, target: t, fromYaw: Rig.Yaw, fromPitch: Rig.Pitch}
	Rig.aim.turn.start(0, 1, seconds, e)
}

// CameraStopLook lets go of a look, leaving the camera facing wherever it was.
func CameraStopLook() { Rig.aim.on = false }

// OrbitParams are the parts of an orbit a call may change; nil keeps each as it
// is.
type OrbitParams struct {
	Distance, Yaw, Pitch *float32
}

// CameraOrbit starts or adjusts an orbit. With a target it orbits that — and
// a camera not already orbiting starts from exactly where it is, its distance
// and direction to the target taken as the orbit's first values, so nothing
// jumps. Without one it adjusts the orbit already running and reports false
// if there is none. Every parameter given blends to its new value over
// seconds.
func CameraOrbit(t *RigTarget, p OrbitParams, seconds float32, e Ease) bool {
	if t == nil && !(Rig.Detached && Rig.orbit.on) {
		return false
	}
	CameraDetach()
	o := &Rig.orbit
	if t != nil {
		if !o.on {
			d := t.Point.Sub(Rig.Pos)
			o.distance = d.Len()
			if o.distance < 1e-3 {
				// Standing on it: keep the facing, and back off to a distance
				// anyone could see the target from.
				o.distance = 5
				o.yaw, o.pitch = Rig.Yaw, Rig.Pitch
			} else {
				o.yaw, o.pitch = AnglesFromDirection(d)
				o.pitch = clampPitch(o.pitch)
			}
			o.centreBlend = floatBlend{}
		} else {
			o.centreFrom = o.centre()
			o.centreBlend.start(0, 1, seconds, e)
		}
		o.on = true
		o.target = *t
		Rig.move, Rig.yaw, Rig.pitch = vecBlend{}, floatBlend{}, floatBlend{}
		Rig.aim.on = false
	}
	if p.Distance != nil {
		d := *p.Distance
		if d < 0.01 {
			d = 0.01
		}
		o.dist.start(o.distance, d, seconds, e)
	}
	if p.Yaw != nil {
		o.yawB.start(o.yaw, nearestAngle(o.yaw, *p.Yaw), seconds, e)
	}
	if p.Pitch != nil {
		o.pitchB.start(o.pitch, clampPitch(*p.Pitch), seconds, e)
	}
	return true
}

// CameraStopOrbit ends an orbit and leaves the camera where it was.
func CameraStopOrbit() { Rig.orbit.on = false }

// CameraOrbiting reports whether the rig is orbiting, and its current distance,
// yaw and pitch — what a script adds a mouse drag to.
func CameraOrbiting() (on bool, distance, yaw, pitch float32) {
	o := Rig.orbit
	return Rig.Detached && o.on, o.distance, o.yaw, o.pitch
}

// CameraZoom eases the field of view, attached or not: zooming is a lens, not a
// position, and the player's own camera wants it for a scope.
func CameraZoom(fov, seconds float32, e Ease) {
	Rig.fov.start(FieldOfView, clampFOV(fov), seconds, e)
}

// StopCameraZoom abandons a zoom in progress, for a script writing the field
// of view directly — the later instruction is the one that meant it.
func StopCameraZoom() { Rig.fov.active = false }

// SetCameraPosition, SetCameraAngles and SetCameraRoll put the rig somewhere at
// once, detaching it: the property writes. Each ends whatever blend or
// behaviour was deciding that channel.
func SetCameraPosition(p mgl32.Vec3) {
	CameraDetach()
	Rig.orbit.on = false
	Rig.move = vecBlend{}
	Rig.Pos = p
	writeRig()
}

func SetCameraAngles(yaw, pitch float32) {
	CameraDetach()
	Rig.aim.on = false
	pitch = clampPitch(pitch)
	if Rig.orbit.on {
		Rig.orbit.yawB, Rig.orbit.pitchB = floatBlend{}, floatBlend{}
		Rig.orbit.yaw, Rig.orbit.pitch = yaw, pitch
		placeOrbit()
	} else {
		Rig.yaw, Rig.pitch = floatBlend{}, floatBlend{}
		Rig.Yaw, Rig.Pitch = yaw, pitch
	}
	writeRig()
}

func SetCameraRoll(roll float32) {
	CameraDetach()
	Rig.roll = floatBlend{}
	Rig.Roll = roll
	writeRig()
}

// CameraTargets are the targets the rig is following whose position a script
// has to refresh this frame.
func CameraTargets() []*RigTarget {
	var out []*RigTarget
	if Rig.Detached && Rig.orbit.on && Rig.orbit.target.Ref != nil {
		out = append(out, &Rig.orbit.target)
	}
	if Rig.Detached && Rig.aim.on && Rig.aim.target.Ref != nil {
		out = append(out, &Rig.aim.target)
	}
	return out
}

// HoldCameraRig lays the rig's current pose back over the camera, straight
// after PlacePlayerCamera has put the player's eye there. Without it the
// middle of the frame — the AI's view of the player's aim, the sound
// listener, and above all the script tick, where `pick`, `hitscan` and the
// pointer events are cast — would all see the player's eye while the picture
// is taken from somewhere else entirely. StepCameraRig, after the tick, then
// moves the pose on.
//
// Blending back to the player, the eye it has just been handed is what the
// blend is heading for, so it is kept rather than lost.
func HoldCameraRig() {
	switch {
	case Rig.returning:
		Rig.eye = camPose{Cam.Position, Cam.Direction, CamUp()}
		Rig.eyeValid = true
		if Rig.shownSet {
			Rig.shown.write()
		}
	case Rig.Detached:
		writeRig()
	}
}

// StepCameraRig advances every blend by dt and, while the rig owns the view,
// lays its pose over the camera. Called once a frame after the script tick
// (sim.Step), when the player's eye — if there is one — is already in Cam.
//
// playerPlaced says it is: the frame placed the camera on the player. Blending
// back to the player needs that, since the blend's far end is read out of
// Cam; a frame that did not place it (a pause, a level with no player) holds
// the blend rather than blending toward whatever the rig itself left there.
func StepCameraRig(dt float32, playerPlaced, hasPlayer bool) {
	if Rig.fov.active {
		FieldOfView = Rig.fov.step(dt)
	}

	if Rig.returning {
		if !hasPlayer {
			// Nobody to go back to: the view stays where the rig left it.
			Rig.returning, Rig.shownSet, Rig.eyeValid = false, false, false
			Rig.Roll = 0
			return
		}
		if !playerPlaced {
			return
		}
		// The player's eye: kept by HoldCameraRig when the frame called it,
		// otherwise still in the camera where PlacePlayerCamera put it.
		eye := camPose{Cam.Position, Cam.Direction, CamUp()}
		if Rig.eyeValid {
			eye = Rig.eye
		}
		Rig.eyeValid = false
		t := Rig.ret.step(dt)
		if !Rig.ret.active {
			Rig.returning, Rig.shownSet = false, false
			Rig.Roll = 0
			eye.write()
			return
		}
		pYaw, pPitch := AnglesFromDirection(eye.dir)
		pos := Rig.retFrom.Add(eye.pos.Sub(Rig.retFrom).Mul(t))
		yaw := Rig.retYaw + (nearestAngle(Rig.retYaw, pYaw)-Rig.retYaw)*t
		pitch := Rig.retPi + (pPitch-Rig.retPi)*t
		Rig.Roll = Rig.retRoll * (1 - t)
		dir := DirectionFromAngles(yaw, pitch)
		up := rolledUp(dir, Rig.Roll)
		// The player's own up is not always the world's (a seat in a banking
		// ship), and the blend has to arrive at it.
		up = up.Mul(1 - t).Add(eye.up.Mul(t))
		if up.Len() < 1e-4 {
			up = eye.up
		}
		Rig.shown, Rig.shownSet = camPose{pos, dir, up.Normalize()}, true
		Rig.shown.write()
		return
	}

	if !Rig.Detached {
		return
	}

	if Rig.orbit.on {
		o := &Rig.orbit
		if o.dist.active {
			o.distance = o.dist.step(dt)
		}
		if o.yawB.active {
			o.yaw = o.yawB.step(dt)
		}
		if o.pitchB.active {
			o.pitch = o.pitchB.step(dt)
		}
		if o.centreBlend.active {
			o.centreBlend.step(dt)
		}
		placeOrbit()
	} else {
		if Rig.move.active {
			Rig.Pos = Rig.move.step(dt)
		}
		if Rig.aim.on {
			Rig.aim.turn.step(dt)
			t := Rig.aim.turn.fraction()
			if yaw, pitch, ok := aimAngles(Rig.Pos, Rig.aim.target.Point); ok {
				Rig.Yaw = Rig.aim.fromYaw + (nearestAngle(Rig.aim.fromYaw, yaw)-Rig.aim.fromYaw)*t
				Rig.Pitch = Rig.aim.fromPitch + (pitch-Rig.aim.fromPitch)*t
			}
		} else {
			if Rig.yaw.active {
				Rig.Yaw = Rig.yaw.step(dt)
			}
			if Rig.pitch.active {
				Rig.Pitch = Rig.pitch.step(dt)
			}
		}
	}
	if Rig.roll.active {
		Rig.Roll = Rig.roll.step(dt)
	}
	writeRig()
}

// centre is where the orbit is centred this frame: the target, or partway to
// it from the previous centre while a change of target blends.
func (o *rigOrbit) centre() mgl32.Vec3 {
	if !o.centreBlend.active {
		return o.target.Point
	}
	t := o.centreBlend.fraction()
	return o.centreFrom.Add(o.target.Point.Sub(o.centreFrom).Mul(t))
}

// placeOrbit puts the rig's pose on its orbit.
func placeOrbit() {
	o := &Rig.orbit
	o.pitch = clampPitch(o.pitch)
	dir := DirectionFromAngles(o.yaw, o.pitch)
	Rig.Pos = o.centre().Sub(dir.Mul(o.distance))
	Rig.Yaw, Rig.Pitch = o.yaw, o.pitch
}

// writeRig lays the rig's pose over the camera.
func writeRig() {
	dir := DirectionFromAngles(Rig.Yaw, Rig.Pitch)
	Cam.Position = Rig.Pos
	Cam.Direction = dir
	Cam.Up = rolledUp(dir, Rig.Roll)
	RebuildCamMatrix()
}

// rolledUp is the world's up turned about the view direction by roll. Positive
// banks the camera right — its top leans right — so the horizon on screen
// turns anticlockwise, the way it does out of an aircraft banking right.
func rolledUp(dir mgl32.Vec3, roll float32) mgl32.Vec3 {
	if roll == 0 {
		return WorldUp
	}
	right := dir.Cross(WorldUp)
	if right.Len() < 1e-6 {
		return WorldUp
	}
	right = right.Normalize()
	up := right.Cross(dir).Normalize()
	s, c := float32(math.Sin(float64(roll))), float32(math.Cos(float64(roll)))
	return up.Mul(c).Add(right.Mul(s)).Normalize()
}

// aimAngles is the facing from eye to target, and false when they coincide.
func aimAngles(eye, target mgl32.Vec3) (yaw, pitch float32, ok bool) {
	d := target.Sub(eye)
	if d.Len() < 1e-4 {
		return 0, 0, false
	}
	yaw, pitch = AnglesFromDirection(d)
	return yaw, clampPitch(pitch), true
}

// nearestAngle is to, moved by whole turns to within half a turn of from, so a
// blend between them goes the short way round.
func nearestAngle(from, to float32) float32 {
	d := math.Remainder(float64(to-from), 2*math.Pi)
	return from + float32(d)
}

func clampPitch(p float32) float32 {
	if p > MaxPitch {
		return MaxPitch
	}
	if p < -MaxPitch {
		return -MaxPitch
	}
	return p
}

// LookDeltaVec is LookDelta as a vec, for the script surface.
func LookDeltaVec() mgl32.Vec3 { return mgl32.Vec3{LookDelta[0], LookDelta[1], 0} }

// RigSnapshot is what of the rig outlives a save: where the camera is, and the
// orbit or look it is holding. Blends in flight are not — see
// savefile.ViewState.
type RigSnapshot struct {
	Detached         bool
	Pos              mgl32.Vec3
	Yaw, Pitch, Roll float32

	Orbit                               bool
	OrbitTarget                         RigTarget
	OrbitDistance, OrbitYaw, OrbitPitch float32

	Look       bool
	LookTarget RigTarget
}

// SnapshotRig is the rig as it stands.
func SnapshotRig() RigSnapshot {
	return RigSnapshot{
		Detached: Rig.Detached, Pos: Rig.Pos, Yaw: Rig.Yaw, Pitch: Rig.Pitch, Roll: Rig.Roll,
		Orbit: Rig.Detached && Rig.orbit.on, OrbitTarget: Rig.orbit.target,
		OrbitDistance: Rig.orbit.distance, OrbitYaw: Rig.orbit.yaw, OrbitPitch: Rig.orbit.pitch,
		Look: Rig.Detached && Rig.aim.on, LookTarget: Rig.aim.target,
	}
}

// RestoreRig puts a snapshot back, every blend finished, and lays the pose
// over the camera at once.
func RestoreRig(s RigSnapshot) {
	ResetCameraRig()
	if !s.Detached {
		return
	}
	Rig.Detached = true
	Rig.Pos, Rig.Yaw, Rig.Pitch, Rig.Roll = s.Pos, s.Yaw, s.Pitch, s.Roll
	if s.Orbit {
		Rig.orbit = rigOrbit{on: true, target: s.OrbitTarget,
			distance: s.OrbitDistance, yaw: s.OrbitYaw, pitch: s.OrbitPitch}
		placeOrbit()
	}
	if s.Look {
		// An instant turn is already there: the look holds from its first frame.
		Rig.aim = rigAim{on: true, target: s.LookTarget, fromYaw: s.Yaw, fromPitch: s.Pitch}
	}
	writeRig()
}
