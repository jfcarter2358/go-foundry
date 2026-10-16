package sim

// footsteps.go — walkers on soft ground (docs/trails/SPEC.md).
//
// Every walker — the player on foot, and every AI-driven entity — counts the
// ground it covers while it stands on something, and each stride's worth is a
// step: the foot comes down a little to the left or right of the line it
// walks, alternately. A step on soft ground (world.SoftGroundAt: the splat's
// sand and snow layers) does three things:
//
//   - presses a footprint into the ground trails: a world.TrailStamp the
//     next draw's trail update stamps (render/ground_trails.go), which the
//     splat shader turns into a dent with a rim;
//   - plays the kind's step sound at the foot (world.SoftGroundKinds);
//   - kicks up the kind's puff, a one-shot emitter.
//
// And the ground drags: the player's stride is shortened by the soft share's
// drag while standing in it, the speed an AI walker steers for likewise
// (world.WalkerDrag, read by ai.steer), and the player's eye sinks a few
// centimetres (world.PlayerGroundSink, eased).
// Landing from a jump presses both feet in at once.
//
// None of it is saved: a session's tracks are a picture, not state.

import (
	"math"
	"math/rand"

	"foundry/internal/audio"
	"foundry/internal/collision"
	"foundry/internal/gfx"
	"foundry/internal/platform"
	"foundry/internal/script"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// walker is one body's step state.
type walker struct {
	dist float32 // ground covered since the last step
	foot int     // which foot comes down next: 0 left, 1 right
	// ground is the soft ground under it, refreshed every step and every
	// groundEvery seconds; age is how long since.
	ground world.SoftGround
	age    float32
	seen   uint64
}

var steps struct {
	lvl     *world.Level
	walkers map[string]*walker
	frame   uint64
	// missing remembers a sound or an effect that is not there, so a
	// level with no step sounds asks the disk once rather than per step.
	missing map[string]bool
}

const (
	// groundEvery is how often a walker's ground is asked between steps.
	groundEvery = 0.3
	// The foot: a capsule this long along the walk and this wide, put down
	// footSide to the side of the body's line.
	footHalfLength = 0.075
	footRadius     = 0.06
	footSide       = 0.11
	playerKey      = "\x00player"
)

// stride is the length of one step at speed (m/s): a walk's ~0.7 m, a run's
// ~1.3 m.
func stride(speed float32) float32 {
	return min(max(0.45+0.17*speed, 0.55), 1.4)
}

// stepWalkers advances every walker's steps by dt. landed is the player's
// landing this frame (MovePlayer's report).
func stepWalkers(lvl *world.Level, dt float32, landed bool) {
	if lvl == nil || dt <= 0 {
		return
	}
	if steps.lvl != lvl || steps.walkers == nil {
		steps.lvl = lvl
		steps.walkers = map[string]*walker{}
		steps.missing = map[string]bool{}
		world.PlayerGroundSink = 0
		world.ClearWalkerDrag()
		// The level's soft ground: its index, and its weights maps decoded
		// in the background from here (world.SoftMapPrefetch).
		world.WarmSoftGround(lvl)
	}
	steps.frame++
	if p := world.PlayerEntity; p != nil {
		world.TrailFocus = p.Position.Sub(mgl32.Vec3{0, p.BoundHalfY, 0})
		sink := float32(0)
		if playerWalks(lvl, p) {
			w := walkerFor(playerKey)
			if landed {
				w.age = groundEvery // ask now: it just arrived
			}
			stepBody(lvl, p, w, dt, landed)
			if w.ground.Soft() {
				sink = w.ground.Sink
			}
		}
		// Eased, a fifth of a second, so walking onto sand settles rather
		// than snaps.
		k := 1 - float32(math.Exp(float64(-dt/0.2)))
		world.PlayerGroundSink += (sink - world.PlayerGroundSink) * k
	} else {
		world.TrailFocus = gfx.Cam.Position
	}
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if e.AI == nil || e.IsVehicle() || e.Frame != "" || !e.Collision.OnGround || !e.HasGravity() {
			continue
		}
		w := walkerFor(e.ID)
		stepBody(lvl, e, w, dt, false)
		publishDrag(e.ID, w.ground)
	}
	// Walkers gone from the level (destroyed, or never seen again) are
	// forgotten, every few seconds.
	if steps.frame%300 == 0 {
		for id, w := range steps.walkers {
			if steps.frame-w.seen > 300 {
				delete(steps.walkers, id)
				world.SetWalkerDrag(id, 0)
			}
		}
	}
}

// playerWalks reports whether the player is on foot, on the ground, in the
// world's own space.
func playerWalks(lvl *world.Level, p *world.Entity) bool {
	return platform.IsOnGround && !platform.FlyMode && p.Seat == "" && lvl.Frames.FrameFor(p).IsWorld()
}

func walkerFor(id string) *walker {
	w := steps.walkers[id]
	if w == nil {
		w = &walker{foot: rand.Intn(2), age: groundEvery}
		steps.walkers[id] = w
	}
	w.seen = steps.frame
	return w
}

// stepBody counts a body's ground and puts its feet down. both presses both
// feet in (a landing).
func stepBody(lvl *world.Level, e *world.Entity, w *walker, dt float32, both bool) {
	feet := e.Position.Sub(mgl32.Vec3{0, e.BoundHalfY, 0})
	w.age += dt
	if w.age >= groundEvery {
		w.age = 0
		w.ground = groundUnder(lvl, feet)
	}
	v := mgl32.Vec2{e.Velocity.X(), e.Velocity.Z()}
	speed := v.Len()
	dir := mgl32.Vec2{0, 1}
	if speed > 1e-3 {
		dir = v.Mul(1 / speed)
	} else if e == world.PlayerEntity {
		dir = mgl32.Vec2{gfx.Cam.Direction.X(), gfx.Cam.Direction.Z()}
		if l := dir.Len(); l > 1e-3 {
			dir = dir.Mul(1 / l)
		} else {
			dir = mgl32.Vec2{0, 1}
		}
	}
	if both {
		w.ground = groundUnder(lvl, feet)
		putFoot(lvl, feet, dir, 0, w.ground, 1, 1)
		putFoot(lvl, feet, dir, 1, w.ground, 1, 0.6)
		w.dist = 0
		return
	}
	if speed < 0.3 {
		return
	}
	w.dist += speed * dt
	if s := stride(speed); w.dist >= s {
		w.dist -= s
		if w.dist > s {
			w.dist = 0 // a teleport, not a stride
		}
		w.ground = groundUnder(lvl, feet)
		w.age = 0
		strength := min(0.75+0.06*speed, 1)
		putFoot(lvl, feet, dir, w.foot, w.ground, strength, min(0.35+0.12*speed, 1))
		w.foot ^= 1
	}
}

// putFoot puts foot (0 left, 1 right) down beside feet, facing dir, on
// ground g: a footprint, a sound at loud and a puff.
func putFoot(lvl *world.Level, feet mgl32.Vec3, dir mgl32.Vec2, foot int, g world.SoftGround, strength, loud float32) {
	if !g.Soft() {
		return
	}
	right := mgl32.Vec2{dir.Y(), -dir.X()}
	side := float32(footSide)
	if foot == 0 {
		side = -side
	}
	c := mgl32.Vec2{feet.X(), feet.Z()}.Add(right.Mul(side))
	world.PushTrailStamp(world.TrailStamp{
		A: c.Sub(dir.Mul(footHalfLength)), B: c.Add(dir.Mul(footHalfLength)),
		Radius: footRadius, Strength: strength, Rim: 0.35,
	})
	k := g.Profile()
	at := mgl32.Vec3{c.X(), feet.Y() + 0.02, c.Y()}
	if k.Sound != "" && !steps.missing["s:"+k.Sound] {
		if audio.SoundPath(k.Sound) == "" {
			steps.missing["s:"+k.Sound] = true
		} else {
			audio.Play(audio.PlayOptions{Clip: k.Sound, Gain: loud * min(g.Share*1.4, 1), Pitch: 0.9 + 0.2*rand.Float32(),
				Spatial: true, Position: at})
		}
	}
	if k.Effect != "" && !steps.missing["e:"+k.Effect] {
		if !spawnPuff(lvl, k.Effect, at, g.Color) {
			steps.missing["e:"+k.Effect] = true
		}
	}
}

// spawnPuff places a self-removing emitter of effect at p (spawn_effect's
// one-shot) tinted tint; false when the effect will not load.
func spawnPuff(lvl *world.Level, effect string, p, tint mgl32.Vec3) bool {
	e := world.NewEmitter(world.UniqueEmitterID(lvl, "step_"+effect), p)
	e.Effect = effect
	e.OneShot = true
	// The ground's own colour: red sand kicks up red, cinders black.
	if tint != (mgl32.Vec3{}) {
		e.Tint = tint
	}
	if err := e.Load(); err != nil {
		return false
	}
	for i := range e.Doc.Layers {
		_ = gfx.EnsureTextureLoaded(e.Doc.Layers[i].DrawTexture())
	}
	// Play changes the level, and leaving play must put it back (a puff
	// still in the air when play stops would otherwise be saved).
	script.PlaySessionMarkDirty()
	lvl.Emitters = append(lvl.Emitters, e)
	return true
}

// publishDrag hands an AI walker's ground drag to its steering
// (world.WalkerDrag), whenever its ground was asked.
func publishDrag(id string, g world.SoftGround) {
	d := float32(0)
	if g.Soft() {
		d = g.Drag
	}
	world.SetWalkerDrag(id, d)
}

// playerSoftDrag is how much of the player's stride the ground under them
// takes: 0 off soft ground, in the air or flying.
func playerSoftDrag() float32 {
	w := steps.walkers[playerKey]
	if w == nil || !w.ground.Soft() || !platform.IsOnGround || platform.FlyMode {
		return 0
	}
	return w.ground.Drag
}

// groundUnder is the soft ground under a body's feet: what a short ray down
// from just above them hits. A generated tile (scenery) answers by its own
// material (world.SoftGroundOn) — the cave floor rather than the terrain
// above it, the terrain rather than a rock lying on it; anything else the
// ray stops on (a brush, a block terrain) is firm. A ray that hits nothing
// (the scenery has no collision, as in a test) asks by position instead.
func groundUnder(lvl *world.Level, feet mgl32.Vec3) world.SoftGround {
	f := collision.RayFilter{Range: 1.5, Geometry: true} // the level's shape only: no bodies
	r := collision.Raycast(lvl, feet.Add(mgl32.Vec3{0, 0.5, 0}), mgl32.Vec3{0, -1, 0}, f)
	if h, ok := r.First(); ok && r.Blocked {
		if h.Kind == collision.RayHitScenery {
			return world.SoftGroundOn(lvl, h.ID, h.Point.X(), h.Point.Z())
		}
		return world.SoftGround{}
	}
	return world.SoftGroundAt(lvl, feet.X(), feet.Y(), feet.Z())
}
