package particle

// particle_sim.go — the CPU simulation.
//
// One System per layer per emitter. It owns a preallocated, packed pool and
// never allocates during play: dead particles are swap-removed, and emission
// stalls at the cap rather than growing the slice. A frame's cost is therefore
// a function of the pool size an author set, which is the only way a particle
// budget means anything.
//
// WHY THE CPU. This was written when the engine targeted GL 4.1 core, with no
// compute shaders or SSBOs; it is 4.3 now, so a compute simulation is open to
// it, at the price of state that collision, a script and a debugger can no
// longer read. Simulating here costs a per-frame walk and an instance buffer upload,
// and buys particles that can bounce off the floor and be asked where they are.
// At the pool sizes an effect actually uses, that walk is not the frame's
// problem; the fill rate of the quads is.
//
// Randomness is a per-System xorshift seeded from the document, so an effect
// looks the same every time it plays and a test can assert an exact count.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// Collide is the world-collision seam. It is a variable rather than an import
// because internal/collision sits far above this package, and the layering test
// enforces that — the same inversion world.CurrentLevel and hooks.EditorActive
// use, and the one DESIGN.md's package layout describes.
//
// It answers: sweeping a sphere of radius r from `from` to `to`, where does it
// first touch, and what is the surface normal there? Nil means no collision,
// which is correct for a headless test and for every layer with Collision off.
//
// Installed by internal/app, which can see both sides.
var Collide func(from, to mgl32.Vec3, radius float32) (pos, normal mgl32.Vec3, hit bool)

// Particle is one live quad's state. Kept small and copied by value: the pool is
// walked linearly every frame, and a pointer chase per particle would cost more
// than the copy does.
type Particle struct {
	Pos, Vel mgl32.Vec3
	// Age counts up; Life is what it is counting toward. Both in seconds.
	Age, Life float32
	// Size is the birth size; the size ramp scales it.
	Size   float32
	Rot    float32
	RotVel float32
	// Seed is per-particle, for the draws that must stay stable across frames —
	// its turbulence offset and its sheet cell.
	Seed uint32
	// Stopped is set by CollideStop: the particle keeps ageing where it landed
	// but no longer integrates.
	Stopped bool
}

// Alive reports whether the particle should still be simulated and drawn.
func (p *Particle) Alive() bool { return p.Age < p.Life }

// LifeT is the particle's normalised age, which is what every ramp is keyed on.
func (p *Particle) LifeT() float32 {
	if p.Life <= 0 {
		return 1
	}
	t := p.Age / p.Life
	if t > 1 {
		return 1
	}
	return t
}

// System simulates one layer.
type System struct {
	// Layer is the parameters. Held by value so the editor can rewrite the
	// document under a running preview without the simulation reading a
	// half-updated struct mid-frame; the caller re-syncs it via SetLayer.
	Layer Layer
	// Gravity is the document's, in world units per second squared.
	Gravity float32

	// Live is the packed pool. Everything below len is alive.
	Live []Particle

	// Emitting gates continuous emission. Bursts still fire when it is off, so
	// a script can stop a fountain and still fire one-shots from it.
	Emitting bool

	// clock is the position within the current cycle, in seconds.
	clock float32
	// carry accumulates the fractional particle a rate leaves behind between
	// frames, so a rate of 0.5/s emits one particle every two seconds rather
	// than none ever.
	carry float32
	// firedBursts tracks which bursts have gone off this cycle, by index.
	firedBursts []bool
	// rng is the per-system xorshift state.
	rng uint32
	// origin is the emitter's transform as of the last Step, for LocalSpace and
	// for the radial/tangential accelerations.
	origin mgl32.Mat4
	// prevOrigin is the previous frame's, so a LocalSpace layer can carry its
	// particles rigidly with a moving emitter.
	prevOrigin mgl32.Mat4
	started    bool
}

// NewSystem builds a system for one layer, with its pool preallocated.
func NewSystem(l Layer, gravity float32, seed uint32) *System {
	s := &System{
		Layer:    l,
		Gravity:  gravity,
		Emitting: true,
		rng:      seedOr(seed),
	}
	s.Live = make([]Particle, 0, l.EffectiveMax())
	s.firedBursts = make([]bool, len(l.Bursts))
	s.origin = mgl32.Ident4()
	s.prevOrigin = mgl32.Ident4()
	return s
}

// seedOr keeps the xorshift off zero, which is its one fixed point.
func seedOr(seed uint32) uint32 {
	if seed == 0 {
		return 0x9E3779B9
	}
	return seed
}

// SetLayer swaps in new parameters, growing or shrinking the pool to match. The
// editor calls it whenever the document changes under a running preview.
func (s *System) SetLayer(l Layer, gravity float32) {
	s.Layer = l
	s.Gravity = gravity
	max := l.EffectiveMax()
	if cap(s.Live) < max {
		grown := make([]Particle, len(s.Live), max)
		copy(grown, s.Live)
		s.Live = grown
	}
	if len(s.Live) > max {
		s.Live = s.Live[:max]
	}
	if len(s.firedBursts) != len(l.Bursts) {
		s.firedBursts = make([]bool, len(l.Bursts))
	}
}

// Reset clears the pool and rewinds the cycle — the editor's Restart, and what
// a freshly placed emitter starts from.
func (s *System) Reset() {
	s.Live = s.Live[:0]
	s.clock = 0
	s.carry = 0
	s.started = false
	s.rng = seedOr(s.rng)
	for i := range s.firedBursts {
		s.firedBursts[i] = false
	}
}

// Count is how many particles are live.
func (s *System) Count() int { return len(s.Live) }

// Clock is how far into the current cycle the system is, in seconds. A looping
// layer's wraps at its duration; a one-shot's parks there.
//
// Exported for the effect's light: the flash is timed off the cycle rather
// than off a clock of its own, so one flash goes with one cycle wherever the
// cycle starts — at a spawn_effect, at a restart, at each lap of a looping
// effect — with nothing to keep in step and nothing to serialize.
func (s *System) Clock() float32 { return s.clock }

// Started reports whether the system has taken its first step. Until it has,
// its clock is 0 because nothing has run, not because a cycle just began.
func (s *System) Started() bool { return s.started }

// Done reports whether this system has finished for good: it has actually run,
// it is not going to emit again, and nothing is left alive.
//
// The "has actually run" half is what stops a one-shot effect being reaped on
// the very frame it was created, when it has no live particles yet because its
// first step has not happened. A looping layer is never done.
func (s *System) Done() bool {
	if !s.started || s.Layer.Loop || len(s.Live) > 0 {
		return false
	}
	if s.Layer.Muted {
		return true
	}
	// Past the end of its one cycle, with every burst in it already fired.
	if s.clock < s.Layer.EffectiveDuration() {
		return false
	}
	for _, fired := range s.firedBursts {
		if !fired {
			return false
		}
	}
	return true
}

// maxSubstep bounds one integration step. The frame clock is already clamped to
// 50 ms upstream, but a collision layer stepping that far at speed would tunnel
// through a floor, so the step is split — the same reasoning the swept
// entity collision is built on.
const maxSubstep = float32(1.0 / 60.0)

// Step advances the simulation by dt with the emitter at origin.
//
// origin is the emitter's world transform, resolved by the caller — which is
// what keeps this package ignorant of frames, entities and bones.
func (s *System) Step(dt float32, origin mgl32.Mat4) {
	if dt <= 0 || s.Layer.Muted {
		return
	}
	s.prevOrigin = s.origin
	s.origin = origin

	if !s.started {
		s.started = true
		if s.Layer.Prewarm {
			// One whole cycle before the first visible frame, so a continuous
			// effect is already in full flow rather than visibly starting up.
			s.prewarm(origin)
		}
	}

	// Split into steps no longer than maxSubstep. Without collision one step
	// would do, but branching on that would make a bouncing layer behave
	// differently from a non-bouncing one at the same frame rate for no reason
	// an author could see.
	remaining := dt
	for remaining > 0 {
		step := remaining
		if step > maxSubstep {
			step = maxSubstep
		}
		s.stepOnce(step, origin)
		remaining -= step
	}
}

// prewarm runs a cycle's worth of steps before anything is drawn.
func (s *System) prewarm(origin mgl32.Mat4) {
	d := s.Layer.EffectiveDuration()
	for t := float32(0); t < d; t += maxSubstep {
		s.stepOnce(maxSubstep, origin)
	}
}

// stepOnce is one substep: age and integrate what is alive, then emit.
func (s *System) stepOnce(dt float32, origin mgl32.Mat4) {
	s.integrate(dt)
	s.emit(dt, origin)
	s.advanceClock(dt)
}

// integrate ages every particle, moves the ones still going, and swap-removes
// the dead.
//
// Walked backwards so a swap-remove cannot skip the particle it swapped in — the
// classic bug in this loop, and invisible when it happens, since the skipped
// particle simply lives one frame too long.
func (s *System) integrate(dt float32) {
	l := &s.Layer
	// A LocalSpace layer's particles are carried rigidly by the emitter, so the
	// delta between last frame's transform and this one is applied to every
	// live particle before anything else moves them.
	var carry mgl32.Mat4
	carrying := l.LocalSpace && s.origin != s.prevOrigin
	if carrying {
		carry = s.origin.Mul4(s.prevOrigin.Inv())
	}

	emitterPos := originPos(s.origin)

	for i := len(s.Live) - 1; i >= 0; i-- {
		p := &s.Live[i]
		p.Age += dt
		if !p.Alive() {
			last := len(s.Live) - 1
			s.Live[i] = s.Live[last]
			s.Live = s.Live[:last]
			continue
		}
		if carrying {
			q := carry.Mul4x1(p.Pos.Vec4(1))
			p.Pos = mgl32.Vec3{q.X(), q.Y(), q.Z()}
		}
		if p.Stopped {
			continue
		}

		// Accelerations, all in world units per second squared.
		acc := mgl32.Vec3{0, -s.Gravity * l.Gravity, 0}

		if l.Radial != 0 || l.Tangential != 0 {
			out := p.Pos.Sub(emitterPos)
			if out.LenSqr() > 1e-8 {
				n := out.Normalize()
				if l.Radial != 0 {
					acc = acc.Add(n.Mul(l.Radial))
				}
				if l.Tangential != 0 {
					// About world up: a swirl is nearly always about the axis
					// the effect stands on.
					tan := mgl32.Vec3{0, 1, 0}.Cross(n)
					if tan.LenSqr() > 1e-8 {
						acc = acc.Add(tan.Normalize().Mul(l.Tangential))
					}
				}
			}
		}

		if l.TurbulenceAmp != 0 {
			acc = acc.Add(turbulence(p.Pos, p.Seed, l.TurbulenceFreq).Mul(l.TurbulenceAmp))
		}

		p.Vel = p.Vel.Add(acc.Mul(dt))

		if l.Drag > 0 {
			// A per-second fraction shed, applied exponentially so the result
			// does not depend on the substep size.
			p.Vel = p.Vel.Mul(float32(math.Exp(float64(-l.Drag * dt))))
		}

		next := p.Pos.Add(p.Vel.Mul(dt))
		if l.Collision != CollideNone && Collide != nil {
			s.resolve(p, next)
		} else {
			p.Pos = next
		}
		p.Rot += p.RotVel * dt
	}
}

// resolve applies the layer's collision response for one particle's step.
func (s *System) resolve(p *Particle, next mgl32.Vec3) {
	l := &s.Layer
	hitPos, normal, hit := Collide(p.Pos, next, l.CollideRadius)
	if !hit {
		p.Pos = next
		return
	}
	switch l.Collision {
	case CollideKill:
		// Ending it outright rather than moving it: a spark that hit the floor
		// is gone, and leaving it there for a frame reads as a flicker.
		p.Age = p.Life
	case CollideStop:
		p.Pos = hitPos
		p.Vel = mgl32.Vec3{}
		p.RotVel = 0
		p.Stopped = true
	case CollideBounce:
		p.Pos = hitPos
		// Reflect, then split into the part along the normal (scaled by
		// bounciness) and the part across it (scaled by what friction leaves).
		vn := normal.Mul(p.Vel.Dot(normal))
		vt := p.Vel.Sub(vn)
		p.Vel = vt.Mul(1 - clamp01(l.Friction)).Sub(vn.Mul(clamp01(l.Bounce)))
	}
}

// advanceClock moves the cycle on, wrapping or stopping at the end.
func (s *System) advanceClock(dt float32) {
	d := s.Layer.EffectiveDuration()
	s.clock += dt
	if s.clock < d {
		return
	}
	if s.Layer.Loop {
		s.clock -= d
		for i := range s.firedBursts {
			s.firedBursts[i] = false
		}
		return
	}
	// A one-shot: park at the end so no more is emitted, and let what is alive
	// finish. Clamped rather than left to grow, or a long-lived one-shot's clock
	// would drift off into the float's coarse range.
	s.clock = d
}

// emit releases this substep's particles: the continuous rate, then any burst
// the clock has just crossed.
func (s *System) emit(dt float32, origin mgl32.Mat4) {
	l := &s.Layer
	d := l.EffectiveDuration()

	// A finished one-shot emits nothing more.
	running := l.Loop || s.clock < d

	if s.Emitting && running && l.Rate > 0 {
		s.carry += l.Rate * dt
		n := int(s.carry)
		s.carry -= float32(n)
		for i := 0; i < n; i++ {
			if !s.spawn(origin) {
				// At the cap. Drop the rest of this frame's carry too, or it
				// accumulates into a burst the moment a particle dies.
				s.carry = 0
				break
			}
		}
	}

	if !running {
		return
	}
	for i := range l.Bursts {
		if i >= len(s.firedBursts) || s.firedBursts[i] {
			continue
		}
		if s.clock+dt < l.Bursts[i].Time {
			continue
		}
		s.firedBursts[i] = true
		s.Burst(l.Bursts[i].Count, origin)
	}
}

// Burst releases n particles immediately. Exported because it is what a script's
// `burst` verb calls, and what the editor's preview fires on demand — neither of
// which should have to wait for a timed burst to come around.
func (s *System) Burst(n int, origin mgl32.Mat4) {
	for i := 0; i < n; i++ {
		if !s.spawn(origin) {
			return
		}
	}
}

// spawn adds one particle. Reports false at the cap, which is the caller's
// signal to stop trying this frame.
func (s *System) spawn(origin mgl32.Mat4) bool {
	l := &s.Layer
	if len(s.Live) >= l.EffectiveMax() {
		return false
	}

	local, dir := s.emitPoint()
	// Into world space through the emitter's transform. A LocalSpace layer is
	// simulated in world space too — what LocalSpace changes is that live
	// particles are carried when the emitter moves, not the space they live in.
	// Simulating in emitter space instead would make gravity point sideways on a
	// banking ship.
	wp := origin.Mul4x1(local.Vec4(1))
	wd := origin.Mul4x1(dir.Vec4(0))
	pos := mgl32.Vec3{wp.X(), wp.Y(), wp.Z()}
	d := mgl32.Vec3{wd.X(), wd.Y(), wd.Z()}
	if d.LenSqr() < 1e-8 {
		d = mgl32.Vec3{0, 1, 0}
	} else {
		d = d.Normalize()
	}
	if l.Spread > 0 {
		d = s.spreadDir(d, l.Spread)
	}

	speed := s.rangeF(l.SpeedMin, l.SpeedMax)
	s.Live = append(s.Live, Particle{
		Pos:    pos,
		Vel:    d.Mul(speed),
		Life:   maxF(s.rangeF(l.LifeMin, l.LifeMax), 1e-3),
		Size:   s.rangeF(l.SizeMin, l.SizeMax),
		Rot:    s.rangeF(l.RotMin, l.RotMax),
		RotVel: s.rangeF(l.RotVelMin, l.RotVelMax),
		Seed:   s.next(),
	})
	return true
}

// emitPoint picks a birth position and outward direction in emitter-local
// space, from the layer's shape.
func (s *System) emitPoint() (pos, dir mgl32.Vec3) {
	l := &s.Layer
	switch l.Shape {
	case ShapeSphere, ShapeHemisphere:
		n := s.unitVector()
		if l.Shape == ShapeHemisphere && n.Y() < 0 {
			n = mgl32.Vec3{n.X(), -n.Y(), n.Z()}
		}
		return n.Mul(l.Radius * s.shellFrac()), n

	case ShapeBox:
		p := mgl32.Vec3{
			s.rangeF(-l.BoxHalf.X(), l.BoxHalf.X()),
			s.rangeF(-l.BoxHalf.Y(), l.BoxHalf.Y()),
			s.rangeF(-l.BoxHalf.Z(), l.BoxHalf.Z()),
		}
		return p, s.shapeDir(p, mgl32.Vec3{0, 1, 0})

	case ShapeCone:
		// A disc of radius Radius, fired into a cone of half-angle Angle. The
		// cone is the workhorse shape: a jet, a flame, a shotgun of sparks.
		a := s.rangeF(0, 2*math.Pi)
		r := l.Radius * s.shellFrac()
		p := mgl32.Vec3{r * cosF(a), 0, r * sinF(a)}
		half := degToRad(l.Angle)
		return p, s.coneDir(mgl32.Vec3{0, 1, 0}, half)

	case ShapeDisc:
		a := s.rangeF(0, 2*math.Pi)
		r := l.Radius * s.shellFrac()
		p := mgl32.Vec3{r * cosF(a), 0, r * sinF(a)}
		if p.LenSqr() < 1e-8 {
			return p, mgl32.Vec3{0, 1, 0}
		}
		return p, s.shapeDir(p, p.Normalize())

	default: // ShapePoint
		return mgl32.Vec3{}, s.shapeDir(mgl32.Vec3{}, mgl32.Vec3{0, 1, 0})
	}
}

// shapeDir applies the layer's direction mode to a shape's own normal.
func (s *System) shapeDir(_ mgl32.Vec3, shapeNormal mgl32.Vec3) mgl32.Vec3 {
	switch s.Layer.Dir {
	case DirRandom:
		return s.unitVector()
	case DirFixed:
		return s.Layer.Direction
	default:
		return shapeNormal
	}
}

// shellFrac picks how far out from the centre a particle is born: 1 on the
// surface, cube-rooted through the volume otherwise so the distribution is even
// rather than crowded at the middle.
func (s *System) shellFrac() float32 {
	t := clamp01(s.Layer.Thickness)
	if t >= 1 {
		return 1
	}
	u := s.unitF()
	// Cube root spreads points evenly through a ball; the lerp toward 1 is what
	// Thickness dials in as a hollow shell.
	solid := float32(math.Cbrt(float64(u)))
	return solid + (1-solid)*t
}

// coneDir picks a direction within half radians of axis.
func (s *System) coneDir(axis mgl32.Vec3, half float32) mgl32.Vec3 {
	if half <= 0 {
		return axis
	}
	// Uniform on the spherical cap rather than uniform in angle, or the
	// distribution bunches along the axis.
	cosHalf := cosF(half)
	z := 1 - s.unitF()*(1-cosHalf)
	r := sqrtF(maxF(0, 1-z*z))
	phi := s.rangeF(0, 2*math.Pi)
	local := mgl32.Vec3{r * cosF(phi), z, r * sinF(phi)}
	return basisAbout(axis).Mul4x1(local.Vec4(0)).Vec3()
}

// spreadDir jitters a direction within spread degrees.
func (s *System) spreadDir(d mgl32.Vec3, spreadDeg float32) mgl32.Vec3 {
	return s.coneDir(d, degToRad(spreadDeg))
}

// basisAbout builds a rotation taking +Y onto axis, for placing a cone.
func basisAbout(axis mgl32.Vec3) mgl32.Mat4 {
	y := axis
	if y.LenSqr() < 1e-8 {
		return mgl32.Ident4()
	}
	y = y.Normalize()
	ref := mgl32.Vec3{0, 0, 1}
	if absF(y.Z()) > 0.999 {
		ref = mgl32.Vec3{1, 0, 0}
	}
	x := ref.Cross(y).Normalize()
	z := y.Cross(x)
	return mgl32.Mat4{
		x.X(), x.Y(), x.Z(), 0,
		y.X(), y.Y(), y.Z(), 0,
		z.X(), z.Y(), z.Z(), 0,
		0, 0, 0, 1,
	}
}

// ---------- randomness ----------

// next advances the xorshift and returns the new state. Deliberately not
// math/rand: the sequence has to be reproducible from a document's seed across
// builds, and cheap enough to call several times per particle.
func (s *System) next() uint32 {
	x := s.rng
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	s.rng = x
	return x
}

// unitF returns a float in [0, 1).
func (s *System) unitF() float32 { return float32(s.next()&0xFFFFFF) / float32(1<<24) }

// rangeF returns a float in [lo, hi]. An inverted or empty range returns lo,
// which is what an author who typed one number into both boxes means.
func (s *System) rangeF(lo, hi float32) float32 {
	if hi <= lo {
		return lo
	}
	return lo + (hi-lo)*s.unitF()
}

// unitVector returns a direction uniform on the sphere.
func (s *System) unitVector() mgl32.Vec3 {
	z := s.rangeF(-1, 1)
	a := s.rangeF(0, 2*math.Pi)
	r := sqrtF(maxF(0, 1-z*z))
	return mgl32.Vec3{r * cosF(a), z, r * sinF(a)}
}

// turbulence is a cheap smooth pseudo-random field: three sines at different
// frequencies, offset per particle so two particles in the same place do not
// move identically. Not a real noise function, and does not need to be — what
// it has to look like is "not moving in a straight line".
func turbulence(p mgl32.Vec3, seed uint32, freq float32) mgl32.Vec3 {
	if freq <= 0 {
		freq = 1
	}
	o := float32(seed&0xFFFF) * 0.0001
	x, y, z := p.X()*freq+o, p.Y()*freq+o, p.Z()*freq+o
	return mgl32.Vec3{
		sinF(y*1.7) + sinF(z*0.9),
		sinF(z*1.3) + sinF(x*1.1),
		sinF(x*1.9) + sinF(y*0.7),
	}.Mul(0.5)
}

// ---------- small float helpers ----------
//
// Named rather than inlined so the maths above reads as maths; math's own take
// float64 and this package is float32 throughout.

func cosF(v float32) float32     { return float32(math.Cos(float64(v))) }
func sinF(v float32) float32     { return float32(math.Sin(float64(v))) }
func sqrtF(v float32) float32    { return float32(math.Sqrt(float64(v))) }
func degToRad(d float32) float32 { return d * math.Pi / 180 }

func absF(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func maxF(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func clamp01(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// originPos pulls the translation out of an emitter transform.
func originPos(m mgl32.Mat4) mgl32.Vec3 {
	return mgl32.Vec3{m[12], m[13], m[14]}
}
