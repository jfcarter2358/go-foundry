package sim

// third_person.go — the third-person camera and the body it looks at.
//
// A level whose `player: camera:` says third_person gets a boom instead of an
// eye (world/player_view.go). The look angles stay exactly what they were —
// gfx.Cam.Yaw and Pitch, turned by the mouse and the look stick — so movement,
// aim and every script reading the look are unchanged; only where the camera
// sits moves. It orbits a pivot near the head, pushed out over one shoulder,
// at the end of a boom that the level's shape pulls in (collision.CameraBoom)
// and that eases back out once nothing is in the way. Pulling in is never
// eased: a camera easing into a wall shows the inside of it.
//
// The pivot itself is placed by the same cast: up from the body's centre to
// the head, then out to the shoulder, so a low cave roof or a slot-canyon wall
// beside the player cannot put the pivot — and so the whole boom — inside rock.
//
// The body (world.PlayerBodyEntity) is placed after the camera each frame: at
// the player's feet, turned toward the way it is going at its turn rate, and
// playing the locomotion clip world.ChooseLocomotion picks from its motion.
// It is hidden whenever the camera is not third person, while the player
// rides a frame or sits in a seat (both keep the first-person eye), and when
// the boom has been squeezed so short the camera would be inside the head.
//
// On foot on the world only. Riding in a frame the camera stays the seated or
// standing eye SeatedCamera places; a boom in a moving frame's coordinates is
// not something this does yet.

import (
	"math"

	"foundry/internal/collision"
	"foundry/internal/gfx"
	"foundry/internal/platform"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// stepDT is the frame's dt, for the camera's easing: PlacePlayerCamera's
// signature is the frame loop's contract and carries none.
var stepDT float32

// boomState is the third-person camera's memory between frames.
var boomState struct {
	ok    bool
	boom  float32 // the boom length in force
	pivot mgl32.Vec3
}

// boomSnapDistance is how far the pivot may jump in one frame before the boom
// is taken to have been teleported and is set rather than eased.
const boomSnapDistance = float32(4)

// ResetThirdPerson forgets the boom and body state, so the next frame places
// both from scratch (a level load, a teleport).
func ResetThirdPerson() {
	boomState.ok = false
	bodyState = bodyMotion{}
}

// placeThirdPerson puts the camera on the boom. The player is on foot.
func placeThirdPerson(lvl *world.Level, c world.PlayerCamera) {
	p := world.PlayerEntity
	world.PlayerThirdPerson = true

	// The look's own limits while the camera is outside the body.
	lo, hi := c.PitchMin*math.Pi/180, c.PitchMax*math.Pi/180
	if lo > hi {
		lo, hi = hi, lo
	}
	gfx.Cam.Pitch = clampFloat(gfx.Cam.Pitch, maxF(lo, -gfx.MaxPitch), minF(hi, gfx.MaxPitch))
	dir := gfx.DirectionFromAngles(gfx.Cam.Yaw, gfx.Cam.Pitch)

	pivot := thirdPersonPivot(lvl, p.Position, gfx.Cam.Yaw, c)

	want := collision.CameraBoom(lvl, pivot, dir.Mul(-1), c.Distance, c.Radius)
	boomState.boom = easeBoom(boomState.boom, want, c.Smoothing, stepDT,
		!boomState.ok || pivot.Sub(boomState.pivot).Len() > boomSnapDistance)
	boomState.ok, boomState.pivot = true, pivot

	gfx.Cam.Up = gfx.WorldUp
	gfx.Cam.Direction = dir
	gfx.Cam.Position = pivot.Sub(dir.Mul(boomState.boom))
	gfx.RebuildCamMatrix()
}

// thirdPersonPivot is the point the boom swings round: Height above the body's
// centre and Shoulder to the camera's right, each leg cast against the level
// so the pivot stays Radius clear of it.
func thirdPersonPivot(lvl *world.Level, centre mgl32.Vec3, yaw float32, c world.PlayerCamera) mgl32.Vec3 {
	head := centre
	if c.Height != 0 {
		up := mgl32.Vec3{0, 1, 0}
		if c.Height < 0 {
			up = mgl32.Vec3{0, -1, 0}
		}
		head = centre.Add(up.Mul(collision.CameraBoom(lvl, centre, up, abs32(c.Height), c.Radius)))
	}
	if c.Shoulder == 0 {
		return head
	}
	// The camera's right, on the level: yaw 0 looks down +X, so right is +Z.
	right := mgl32.Vec3{-float32(math.Sin(float64(yaw))), 0, float32(math.Cos(float64(yaw)))}
	if c.Shoulder < 0 {
		right = right.Mul(-1)
	}
	return head.Add(right.Mul(collision.CameraBoom(lvl, head, right, abs32(c.Shoulder), c.Radius)))
}

// easeBoom is the boom length after one frame: straight in to want when that
// is shorter (or snap is set), eased out toward it at rate otherwise.
func easeBoom(cur, want, rate, dt float32, snap bool) float32 {
	if snap || want <= cur {
		return want
	}
	if rate <= 0 {
		return want
	}
	k := 1 - float32(math.Exp(-float64(rate*dt)))
	return cur + (want-cur)*k
}

// ---------- the body ----------

// bodyMotion is the body's memory between frames.
type bodyMotion struct {
	ok      bool
	lastPos mgl32.Vec3
	speed   float32 // smoothed horizontal speed
	yaw     float32
	air     float32
	jumped  bool
	clip    string // the locomotion name playing
}

var bodyState bodyMotion

// bodyTeleportSpeed is a frame-to-frame speed no walk reaches: a teleport.
const bodyTeleportSpeed = float32(60)

// UpdatePlayerBody places, turns and animates the third-person body for the
// frame. live is whether the game (not the editor) owns the frame; outside it
// the body is hidden. dt 0 — a pause — holds the body as it is.
func UpdatePlayerBody(lvl *world.Level, dt float32, live bool) {
	if !live {
		// The editor's camera is its own, whatever play last left.
		world.PlayerThirdPerson = false
	}
	b, p := world.PlayerBodyEntity, world.PlayerEntity
	if b == nil {
		return
	}
	cam := lvl.Player.Camera.Effective()
	active := live && p != nil && world.PlayerThirdPerson && p.Frame == "" && p.Seat == "" && len(b.Models) > 0
	if !active {
		b.Visible = false
		bodyState.ok = false
		return
	}
	b.Visible = boomState.boom >= cam.HideDistance
	if dt <= 0 {
		return
	}

	feet := p.Position.Sub(mgl32.Vec3{0, p.BoundHalfY, 0})
	moveX, moveZ := float32(0), float32(0)
	if bodyState.ok {
		d := feet.Sub(bodyState.lastPos)
		h := float32(math.Hypot(float64(d.X()), float64(d.Z()))) / dt
		if h > bodyTeleportSpeed {
			h = 0
		} else {
			moveX, moveZ = d.X(), d.Z()
		}
		k := 1 - float32(math.Exp(-float64(14*dt)))
		bodyState.speed += (h - bodyState.speed) * k
	} else {
		bodyState = bodyMotion{ok: true, yaw: lookYawToBodyYaw(gfx.Cam.Yaw)}
	}
	bodyState.lastPos = feet

	// Face the way it is going, at the turn rate.
	if moveX*moveX+moveZ*moveZ > 1e-8 && bodyState.speed > world.LocomotionStillSpeed {
		target := float32(math.Atan2(float64(-moveX), float64(-moveZ)))
		bodyState.yaw = turnToward(bodyState.yaw, target, lvl.Player.Body.EffectiveTurnRate()*dt)
	}

	// Off the ground: how long, and whether it started with a jump.
	if platform.IsOnGround {
		bodyState.air, bodyState.jumped = 0, false
	} else {
		if bodyState.air == 0 && p.Velocity.Y() > 0.5 {
			bodyState.jumped = true
		}
		bodyState.air += dt
	}

	b.Position = feet
	b.Rotation = mgl32.Vec3{0, bodyState.yaw, 0}

	choice := world.ChooseLocomotion(lvl.Player.Body.Locomotion.Effective(), world.LocomotionState{
		Speed:         bodyState.speed,
		OnGround:      platform.IsOnGround,
		VerticalSpeed: p.Velocity.Y(),
		AirTime:       bodyState.air,
		Crouched:      world.PlayerCrouched(),
		Sprinting:     world.PlayerSprinting(),
		Jumped:        bodyState.jumped,
	})
	bodyState.clip = playLocomotion(b, bodyState.clip, choice)
	b.UpdateAnimations(dt)
}

// lookYawToBodyYaw is the body yaw that faces the way the camera looks: the
// look's yaw 0 is +X, an entity's rotation 0 faces −Z.
func lookYawToBodyYaw(lookYaw float32) float32 {
	d := gfx.DirectionFromAngles(lookYaw, 0)
	return float32(math.Atan2(float64(-d.X()), float64(-d.Z())))
}

// turnToward moves an angle toward a target by at most step, the short way.
func turnToward(cur, target, step float32) float32 {
	d := wrapPi(target - cur)
	if d > step {
		d = step
	} else if d < -step {
		d = -step
	}
	return wrapPi(cur + d)
}

// playLocomotion starts choice's clip on every attachment that has it, unless
// it is what is already playing, and sets its rate. It returns the name now
// playing — the first of the clip and its fallbacks any attachment had.
func playLocomotion(b *world.Entity, current string, choice world.LocomotionChoice) string {
	names := append([]string{choice.Clip}, choice.Fallbacks...)
	for _, name := range names {
		if name == "" {
			continue
		}
		if name == current {
			setLocomotionRate(b, name, choice.Rate)
			return name
		}
		if startLocomotion(b, name) {
			setLocomotionRate(b, name, choice.Rate)
			return name
		}
	}
	return current
}

// startLocomotion starts a name on every attachment that knows it: an
// authored animation of that name (its clip, blend and loop), else a clip in
// the model by that name with the default crossfade.
func startLocomotion(b *world.Entity, name string) bool {
	any := false
	for i := range b.Models {
		m := &b.Models[i]
		if m.Anim == nil {
			continue
		}
		if m.Animation(name) != nil {
			if m.Play(name) {
				any = true
			}
			continue
		}
		if m.Anim.SetClip(name, world.DefaultAnimBlend*1.5) {
			any = true
		}
	}
	return any
}

// setLocomotionRate scales the playing clip by rate, over its authored speed.
func setLocomotionRate(b *world.Entity, name string, rate float32) {
	for i := range b.Models {
		m := &b.Models[i]
		if m.Anim == nil {
			continue
		}
		base := float32(1)
		if a := m.Animation(name); a != nil {
			base = a.EffectiveSpeed()
		}
		m.Anim.Speed = base * rate
	}
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func minF(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func maxF(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}
