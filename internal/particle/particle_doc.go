package particle

// Package particle is the .fpar effect document and the CPU simulation that
// runs it.
//
// It is pure: no GL, no level, no editor. That is what lets the particle
// editor's preview and the running engine drive the same simulation, and what
// lets every one of the tests here run headlessly. The two things it genuinely
// needs from above — collision against the world, and the transform of whatever
// an emitter is riding — arrive as a function variable and a parameter
// respectively, rather than as imports.
//
// particle_doc.go holds the runtime document: one effect, made of layers.
//
// One document is one EFFECT, not one emitter. `torch.fpar` is flame plus smoke
// plus sparks, because that is the unit an author places and the unit a script
// names. Layers within it are independent simulations sharing a name and an
// origin.

import (
	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// ShapeKind is the region new particles are born in.
type ShapeKind int

const (
	ShapePoint ShapeKind = iota
	ShapeSphere
	ShapeHemisphere
	ShapeBox
	ShapeCone
	ShapeDisc
)

// ShapeKindNames is the YAML/UI name of each shape, indexed by the shape.
// Adding one means adding its name here and nowhere else.
var ShapeKindNames = []string{
	"point",
	"sphere",
	"hemisphere",
	"box",
	"cone",
	"disc",
}

// ShapeKindName returns a shape's name, falling back to the default.
func ShapeKindName(k ShapeKind) string {
	if int(k) < 0 || int(k) >= len(ShapeKindNames) {
		return ShapeKindNames[ShapePoint]
	}
	return ShapeKindNames[k]
}

// ShapeKindFromName is the inverse; an unknown name loads as a point.
func ShapeKindFromName(name string) ShapeKind {
	for i, n := range ShapeKindNames {
		if n == name {
			return ShapeKind(i)
		}
	}
	return ShapePoint
}

// DirMode is where a new particle's initial direction comes from.
type DirMode int

const (
	// DirShape points a particle outward along the shape's own normal — away
	// from a sphere's centre, up a cone, off a disc's face. The default,
	// because it is what makes the shape mean anything.
	DirShape DirMode = iota
	// DirRandom ignores the shape and picks a direction on the whole sphere.
	DirRandom
	// DirFixed sends every particle the same way, along Direction.
	DirFixed
)

// DirModeNames is the YAML/UI name of each direction mode.
var DirModeNames = []string{"shape", "random", "fixed"}

// DirModeName returns a mode's name.
func DirModeName(d DirMode) string {
	if int(d) < 0 || int(d) >= len(DirModeNames) {
		return DirModeNames[DirShape]
	}
	return DirModeNames[d]
}

// DirModeFromName is the inverse.
func DirModeFromName(name string) DirMode {
	for i, n := range DirModeNames {
		if n == name {
			return DirMode(i)
		}
	}
	return DirShape
}

// FrameMode is how a sheet cell is chosen for a particle.
type FrameMode int

const (
	// FrameOverLife walks the sheet from the first cell to the last across the
	// particle's lifetime — a flipbook.
	FrameOverLife FrameMode = iota
	// FrameRandom picks one cell at birth and keeps it, which is how one sheet
	// of four smoke puffs stops a cloud looking like one puff repeated.
	FrameRandom
	// FrameStatic uses the first cell always.
	FrameStatic
)

// FrameModeNames is the YAML/UI name of each frame mode.
var FrameModeNames = []string{"over-life", "random", "static"}

// FrameModeName returns a mode's name.
func FrameModeName(f FrameMode) string {
	if int(f) < 0 || int(f) >= len(FrameModeNames) {
		return FrameModeNames[FrameOverLife]
	}
	return FrameModeNames[f]
}

// FrameModeFromName is the inverse.
func FrameModeFromName(name string) FrameMode {
	for i, n := range FrameModeNames {
		if n == name {
			return FrameMode(i)
		}
	}
	return FrameOverLife
}

// CollideMode is what a particle does when it meets the world.
type CollideMode int

const (
	// CollideNone never tests, and costs nothing. The default: most effects are
	// smoke and fire, which have no business bouncing.
	CollideNone CollideMode = iota
	// CollideStop parks the particle where it hit and lets it live out its life
	// there — scorch, splatter, settled dust.
	CollideStop
	// CollideBounce reflects it, scaled by Bounce and Friction.
	CollideBounce
	// CollideKill ends the particle on contact.
	CollideKill
)

// CollideModeNames is the YAML/UI name of each collision mode.
var CollideModeNames = []string{"none", "stop", "bounce", "kill"}

// CollideModeName returns a mode's name.
func CollideModeName(c CollideMode) string {
	if int(c) < 0 || int(c) >= len(CollideModeNames) {
		return CollideModeNames[CollideNone]
	}
	return CollideModeNames[c]
}

// CollideModeFromName is the inverse.
func CollideModeFromName(name string) CollideMode {
	for i, n := range CollideModeNames {
		if n == name {
			return CollideMode(i)
		}
	}
	return CollideNone
}

// RenderKind is what a layer's particles are drawn AS.
type RenderKind int

const (
	// RenderSprite draws every particle as a textured quad — the sprite pass,
	// and everything its blend and billboard modes say.
	RenderSprite RenderKind = iota
	// RenderVolume draws every particle as a soft, noise-displaced ball of
	// volumetric smoke or fire, marched by the volumetric pass rather than
	// rasterised as a quad. A fireball is a handful of these: the simulation
	// moves them exactly as it moves quads, and the size, alpha and colour ramps
	// become radius, density and emitted light.
	RenderVolume
)

// RenderKindNames is the YAML/UI name of each kind.
var RenderKindNames = []string{"sprite", "volume"}

// RenderKindName returns a kind's name, falling back to sprite.
func RenderKindName(k RenderKind) string {
	if int(k) < 0 || int(k) >= len(RenderKindNames) {
		return RenderKindNames[RenderSprite]
	}
	return RenderKindNames[k]
}

// RenderKindFromName is the inverse; an unknown name loads as a sprite.
func RenderKindFromName(name string) RenderKind {
	for i, n := range RenderKindNames {
		if n == name {
			return RenderKind(i)
		}
	}
	return RenderSprite
}

// VolumeLook is how a RenderVolume layer's particles look. Unread by a sprite
// layer, and kept across a switch between the two so comparing them costs
// nothing.
type VolumeLook struct {
	// Density is the extinction per world unit at a ball's core, before the
	// alpha ramp scales it over the particle's life.
	Density float32
	// NoiseScale is how many repeats of the noise span one ball's radius. Low
	// is a few big lumps; high is a cauliflower.
	NoiseScale float32
	// Displacement is how far the noise pushes the ball's surface in and out,
	// as a fraction of its radius. 0 is a smooth sphere of smoke.
	Displacement float32
	// Billow is how fast the noise rolls outward from the ball's centre, in
	// noise repeats per unit of life — what makes a fireball churn as it grows
	// rather than inflate like a balloon.
	Billow float32
	// Erosion is how hard the detail noise eats the ball's edges, 0 to 1,
	// multiplied by ErosionRamp over life: rising erosion is how dense fire
	// breaks up into wisps of smoke as it dies.
	Erosion     float32
	ErosionRamp Ramp
	// Albedo is the colour of the smoke — the light it scatters back, lit by
	// the sun and the sky exactly as a cloud is.
	Albedo mgl32.Vec3
	// Absorption multiplies how much light the ball's own mass takes out on
	// its way from the sun, which is what shades a plume's underside.
	Absorption float32
	// Emission is how brightly the colour ramp (times Tint) glows as light of
	// its own, multiplied by EmissionRamp over life. 0 is plain smoke; a
	// fireball starts high and ramps to 0 as it cools.
	Emission     float32
	EmissionRamp Ramp
}

// DefaultVolumeLook is a grey ball of smoke.
func DefaultVolumeLook() VolumeLook {
	return VolumeLook{
		Density:      4,
		NoiseScale:   1.2,
		Displacement: 0.35,
		Billow:       0.6,
		Erosion:      0.3,
		Albedo:       mgl32.Vec3{0.45, 0.43, 0.4},
		Absorption:   1,
	}
}

// IsDefault reports whether the look is DefaultVolumeLook, ramps included —
// what decides whether a document writes a `volume:` block at all.
func (v VolumeLook) IsDefault() bool {
	d := DefaultVolumeLook()
	return v.Density == d.Density && v.NoiseScale == d.NoiseScale &&
		v.Displacement == d.Displacement && v.Billow == d.Billow &&
		v.Erosion == d.Erosion && v.Albedo == d.Albedo && v.Absorption == d.Absorption &&
		v.Emission == d.Emission && len(v.ErosionRamp) == 0 && len(v.EmissionRamp) == 0
}

// Burst is a one-off release of count particles at time t into the layer's
// cycle. A layer can carry any number, which is how an effect gets a bang
// followed by a trail.
type Burst struct {
	Time  float32
	Count int
}

// Stop is one keyed value on a scalar ramp, at T in [0, 1] of a particle's life.
type Stop struct {
	T float32
	V float32
}

// ColorStop is the same for a colour gradient.
type ColorStop struct {
	T float32
	C mgl32.Vec3
}

// Ramp is a scalar curve over a particle's normalised life, as a list of stops
// sorted by T. Empty means "always 1", which is what lets a layer that does not
// care about the curve carry none at all.
//
// Keyed stops rather than a spline: an author drags points on a strip, and a
// straight line between two of them is both what they see and what they get.
type Ramp []Stop

// At evaluates the ramp at t, clamped to the end stops.
func (r Ramp) At(t float32) float32 {
	if len(r) == 0 {
		return 1
	}
	if t <= r[0].T {
		return r[0].V
	}
	last := len(r) - 1
	if t >= r[last].T {
		return r[last].V
	}
	for i := 0; i < last; i++ {
		a, b := r[i], r[i+1]
		if t < a.T || t > b.T {
			continue
		}
		span := b.T - a.T
		if span <= 0 {
			return b.V
		}
		return a.V + (b.V-a.V)*(t-a.T)/span
	}
	return r[last].V
}

// Gradient is a colour curve over a particle's normalised life. Empty means
// "always white".
type Gradient []ColorStop

// At evaluates the gradient at t, clamped to the end stops.
func (g Gradient) At(t float32) mgl32.Vec3 {
	if len(g) == 0 {
		return mgl32.Vec3{1, 1, 1}
	}
	if t <= g[0].T {
		return g[0].C
	}
	last := len(g) - 1
	if t >= g[last].T {
		return g[last].C
	}
	for i := 0; i < last; i++ {
		a, b := g[i], g[i+1]
		if t < a.T || t > b.T {
			continue
		}
		span := b.T - a.T
		if span <= 0 {
			return b.C
		}
		f := (t - a.T) / span
		return a.C.Add(b.C.Sub(a.C).Mul(f))
	}
	return g[last].C
}

// Layer is one simulation within an effect: a flame, or the smoke above it.
type Layer struct {
	Name string
	// Muted skips the layer entirely. An authoring switch that is saved, so an
	// effect can ship with a variant turned off rather than deleted.
	Muted bool

	// ---- emission ----

	// Rate is particles per second, released continuously.
	Rate float32
	// Duration is one cycle's length in seconds. Bursts are timed within it.
	Duration float32
	// Loop restarts the cycle rather than stopping at Duration.
	Loop bool
	// Prewarm runs one whole cycle before the first frame is shown, so a
	// continuous effect is already in full flow when it comes into view rather
	// than visibly starting up.
	Prewarm bool
	// MaxParticles caps the pool. Reached, emission stalls rather than growing
	// the pool: a fixed ceiling is what keeps a frame's cost predictable.
	MaxParticles int
	// Bursts are one-off releases within the cycle.
	Bursts []Burst

	// ---- shape ----

	Shape     ShapeKind
	Radius    float32
	Angle     float32    // cone half-angle, degrees
	BoxHalf   mgl32.Vec3 // box half-extents
	Thickness float32    // 0 = the whole volume, 1 = the surface only
	Dir       DirMode
	Direction mgl32.Vec3 // for DirFixed
	Spread    float32    // degrees of random cone around the chosen direction

	// ---- initial state ----

	LifeMin, LifeMax     float32
	SpeedMin, SpeedMax   float32
	SizeMin, SizeMax     float32
	RotMin, RotMax       float32 // radians
	RotVelMin, RotVelMax float32 // radians per second

	// ---- motion ----

	// Gravity scales the world's own downward acceleration. 0 floats, 1 falls,
	// negative rises — which is what smoke wants.
	Gravity float32
	// Drag is a per-second fraction of velocity shed. 0 coasts forever.
	Drag float32
	// Radial pushes away from the emitter's origin, Tangential swirls about its
	// up axis. Together they are most of what makes an effect look designed
	// rather than sprayed.
	Radial     float32
	Tangential float32
	// Turbulence is a smooth pseudo-random field, sampled per particle per
	// frame. Amp is its strength, Freq how tightly it varies in space.
	TurbulenceAmp  float32
	TurbulenceFreq float32
	// LocalSpace simulates in the emitter's frame, so the whole effect is
	// carried rigidly when the emitter moves. Off, particles are released into
	// the world and left behind — which is the difference between a torch on a
	// moving ship and the smoke trailing from it.
	LocalSpace bool

	// ---- appearance ----

	// Render picks the pass: quads, or volumetric balls. Every field below it
	// but Tint and the ramps is read by one or the other, not both.
	Render RenderKind
	// Volume is how a RenderVolume layer looks.
	Volume VolumeLook

	Texture              string
	SheetCols, SheetRows int
	FrameMode            FrameMode
	Blend                gfx.SpriteBlend
	Billboard            gfx.BillboardMode
	Lit                  bool
	CastShadows          bool
	Tint                 mgl32.Vec3

	// ---- collision ----

	Collision     CollideMode
	CollideRadius float32
	Bounce        float32
	Friction      float32

	// ---- ramps over normalised life ----

	SizeRamp  Ramp
	AlphaRamp Ramp
	ColorRamp Gradient
}

// Doc is one effect: a name, a seed, and its layers.
type Doc struct {
	ID string
	// Seed fixes every random draw the simulation makes, so the editor's
	// preview is reproducible and a test can assert an exact particle count.
	Seed uint32
	// Gravity is the acceleration a layer's Gravity scales, in world units per
	// second squared. Carried on the document so an effect authored against one
	// gravity does not change meaning in a level with another.
	Gravity float32
	Layers  []Layer

	// Path is where this document was loaded from or last saved to. Not
	// serialized.
	Path string
	// dirty is set by every mutation and cleared by the editor once it has
	// rebuilt from the document — the same rev/dirty pair ModelDoc uses.
	dirty bool
	rev   int
}

// DefaultGravity is what a fresh effect falls at. Chosen to read as "earth-ish"
// at the scale a level is built in rather than to match any real constant.
const DefaultGravity = float32(9.8)

// NewDoc returns an effect with one layer, set up so that pressing play in the
// editor immediately shows something rather than an empty preview.
func NewDoc(id string) *Doc {
	return &Doc{
		ID:      id,
		Seed:    1,
		Gravity: DefaultGravity,
		Layers:  []Layer{NewLayer("layer")},
	}
}

// NewLayer returns a layer with defaults chosen to be visible immediately: a
// second of life, a little upward speed, and a size curve that fades out.
func NewLayer(name string) Layer {
	return Layer{
		Name:          name,
		Rate:          40,
		Duration:      2,
		Loop:          true,
		MaxParticles:  500,
		Shape:         ShapeSphere,
		Radius:        0.2,
		Angle:         25,
		BoxHalf:       mgl32.Vec3{0.5, 0.5, 0.5},
		Dir:           DirShape,
		Direction:     mgl32.Vec3{0, 1, 0},
		LifeMin:       0.8,
		LifeMax:       1.4,
		SpeedMin:      1,
		SpeedMax:      2,
		SizeMin:       0.2,
		SizeMax:       0.35,
		RotMin:        0,
		RotMax:        0,
		Gravity:       0,
		Drag:          0.4,
		Volume:        DefaultVolumeLook(),
		Texture:       "default",
		FrameMode:     FrameOverLife,
		Blend:         gfx.SpriteBlendAdditive,
		Billboard:     gfx.BillboardFull,
		Tint:          mgl32.Vec3{1, 1, 1},
		CollideRadius: 0.05,
		Bounce:        0.4,
		Friction:      0.2,
		AlphaRamp:     Ramp{{T: 0, V: 0}, {T: 0.15, V: 1}, {T: 1, V: 0}},
		SizeRamp:      Ramp{{T: 0, V: 1}, {T: 1, V: 1}},
	}
}

// EffectiveDuration is the cycle length, never zero — a layer with no duration
// authored would otherwise divide by it every frame.
func (l *Layer) EffectiveDuration() float32 {
	if l.Duration <= 0 {
		return 1
	}
	return l.Duration
}

// EffectiveMax is the pool ceiling, clamped to something a frame can afford. A
// layer with none authored gets a sane default rather than an unbounded pool.
func (l *Layer) EffectiveMax() int {
	if l.MaxParticles <= 0 {
		return 500
	}
	if l.MaxParticles > MaxPoolSize {
		return MaxPoolSize
	}
	return l.MaxParticles
}

// MaxPoolSize is the hard ceiling on one layer's pool, whatever it asks for.
// Well above what an effect needs and far below what would stall a frame.
const MaxPoolSize = 20000

// Dirty reports whether the document has changed since the last Clean.
func (d *Doc) Dirty() bool { return d.dirty }

// Invalidate marks the document changed. Every mutation goes through it, or
// through a caller that calls it.
func (d *Doc) Invalidate() { d.dirty = true; d.rev++ }

// Clean clears the dirty flag, for a caller that has just rebuilt from the
// document.
func (d *Doc) Clean() { d.dirty = false }

// Rev is a monotonic change counter, for a cache that wants to know whether it
// is looking at the same document it was.
func (d *Doc) Rev() int { return d.rev }

// Clone deep-copies the document, for the editor's undo stack. Every slice is
// copied rather than aliased, or an undo snapshot would share ramps with the
// live document and record nothing.
func (d *Doc) Clone() *Doc {
	if d == nil {
		return nil
	}
	cp := *d
	cp.Layers = make([]Layer, len(d.Layers))
	for i := range d.Layers {
		cp.Layers[i] = d.Layers[i].Clone()
	}
	return &cp
}

// Clone deep-copies one layer.
func (l Layer) Clone() Layer {
	cp := l
	cp.Bursts = append([]Burst(nil), l.Bursts...)
	cp.SizeRamp = append(Ramp(nil), l.SizeRamp...)
	cp.AlphaRamp = append(Ramp(nil), l.AlphaRamp...)
	cp.ColorRamp = append(Gradient(nil), l.ColorRamp...)
	cp.Volume.ErosionRamp = append(Ramp(nil), l.Volume.ErosionRamp...)
	cp.Volume.EmissionRamp = append(Ramp(nil), l.Volume.EmissionRamp...)
	return cp
}

// AddLayer appends a layer with a name no other layer in the document is using.
func (d *Doc) AddLayer(stem string) int {
	l := NewLayer(d.uniqueLayerName(stem))
	d.Layers = append(d.Layers, l)
	d.Invalidate()
	return len(d.Layers) - 1
}

// DuplicateLayer copies the layer at idx and returns the copy's index.
func (d *Doc) DuplicateLayer(idx int) int {
	if idx < 0 || idx >= len(d.Layers) {
		return -1
	}
	cp := d.Layers[idx].Clone()
	cp.Name = d.uniqueLayerName(cp.Name)
	d.Layers = append(d.Layers, cp)
	d.Invalidate()
	return len(d.Layers) - 1
}

// DeleteLayer removes the layer at idx.
func (d *Doc) DeleteLayer(idx int) {
	if idx < 0 || idx >= len(d.Layers) {
		return
	}
	d.Layers = append(d.Layers[:idx], d.Layers[idx+1:]...)
	d.Invalidate()
}

// MoveLayer shifts a layer by delta places, clamped to the ends. Order is draw
// order within the effect, which matters for the alpha layers.
func (d *Doc) MoveLayer(idx, delta int) int {
	to := idx + delta
	if idx < 0 || idx >= len(d.Layers) || to < 0 || to >= len(d.Layers) {
		return idx
	}
	d.Layers[idx], d.Layers[to] = d.Layers[to], d.Layers[idx]
	d.Invalidate()
	return to
}

// LayerIndexByName returns the layer with the given name, or -1.
func (d *Doc) LayerIndexByName(name string) int {
	for i := range d.Layers {
		if d.Layers[i].Name == name {
			return i
		}
	}
	return -1
}

// uniqueLayerName appends a numeric suffix until the name is free.
func (d *Doc) uniqueLayerName(stem string) string {
	if stem == "" {
		stem = "layer"
	}
	if d.LayerIndexByName(stem) < 0 {
		return stem
	}
	for n := 2; ; n++ {
		candidate := stem + "_" + itoa(n)
		if d.LayerIndexByName(candidate) < 0 {
			return candidate
		}
	}
}

// itoa is strconv.Itoa without the import; this package has no other use for it.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
