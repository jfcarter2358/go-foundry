package particle

// particle_file.go — the .fpar effect document format.
//
// The schema follows level_schema.go's and model_file.go's conventions: pointer
// fields with omitempty wherever "absent" and "zero" must stay
// distinguishable, name tables rather than raw enum ints so a document stays
// readable and a reordered enum cannot silently reinterpret an old file, and
// WriteFileAtomic for the write so a crash mid-save cannot leave half a
// document behind.
//
// The pointer-vs-zero rule earns its keep here more than anywhere else in the
// engine, because most of a layer's numbers have a meaningful zero: a Rate of 0
// is a burst-only layer, a Gravity of 0 is a floating one, a LifeMin of 0 is
// legitimate. Those are plain fields with a plain omitempty and a documented
// "absent means 0". The handful where absent must mean something *else* —
// Duration, MaxParticles, the sizes — are pointers.

import (
	"fmt"
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"os"
	"path/filepath"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// ParticlesDirName is where effects live, beside models/ and textures/.
const ParticlesDirName = "particles"

// ParticleExt is the native document extension.
const ParticleExt = ".fpar"

// ParticlesDir is where the editor's open/save dialogs look.
func ParticlesDir() string { return filepath.Join(fileutil.ResourcesPath, ParticlesDirName) }

// ParticlePath resolves a bare effect name to a full path.
func ParticlePath(name string) string { return filepath.Join(ParticlesDir(), name+ParticleExt) }

// RawVec3 is a bare {x, y, z} triple, matching world.RawVec3's spelling so the
// two formats read alike.
type RawVec3 struct {
	X float32 `yaml:"x"`
	Y float32 `yaml:"y"`
	Z float32 `yaml:"z"`
}

// RawColor is an {r, g, b} triple.
type RawColor struct {
	R float32 `yaml:"r"`
	G float32 `yaml:"g"`
	B float32 `yaml:"b"`
}

// RawBurst is one timed release.
type RawBurst struct {
	Time  float32 `yaml:"t"`
	Count int     `yaml:"n"`
}

// RawStop is one keyed value on a scalar ramp.
type RawStop struct {
	T float32 `yaml:"t"`
	V float32 `yaml:"v"`
}

// RawColorStop is one keyed colour on a gradient.
type RawColorStop struct {
	T float32  `yaml:"t"`
	C RawColor `yaml:"c"`
}

// RawLayer is one simulation within an effect.
type RawLayer struct {
	Name  string `yaml:"name"`
	Muted bool   `yaml:"muted,omitempty"`

	// Emission. Rate 0 is a burst-only layer, so it is a plain field; Duration
	// and MaxParticles have no sensible zero and are pointers.
	Rate         float32    `yaml:"rate,omitempty"`
	Duration     *float32   `yaml:"duration,omitempty"` // default 1
	Loop         *bool      `yaml:"loop,omitempty"`     // default true
	Prewarm      bool       `yaml:"prewarm,omitempty"`
	MaxParticles *int       `yaml:"max,omitempty"` // default 500
	Bursts       []RawBurst `yaml:"bursts,omitempty"`

	// Shape.
	Shape     string   `yaml:"shape,omitempty"` // point (default) | sphere | ...
	Radius    float32  `yaml:"radius,omitempty"`
	Angle     float32  `yaml:"angle,omitempty"`
	BoxHalf   *RawVec3 `yaml:"box_half,omitempty"`
	Thickness float32  `yaml:"thickness,omitempty"`
	Dir       string   `yaml:"direction_mode,omitempty"` // shape (default) | random | fixed
	Direction *RawVec3 `yaml:"direction,omitempty"`
	Spread    float32  `yaml:"spread,omitempty"`

	// Initial state. All plain: zero is meaningful for every one of them.
	LifeMin   float32 `yaml:"life_min,omitempty"`
	LifeMax   float32 `yaml:"life_max,omitempty"`
	SpeedMin  float32 `yaml:"speed_min,omitempty"`
	SpeedMax  float32 `yaml:"speed_max,omitempty"`
	SizeMin   float32 `yaml:"size_min,omitempty"`
	SizeMax   float32 `yaml:"size_max,omitempty"`
	RotMin    float32 `yaml:"rot_min,omitempty"`
	RotMax    float32 `yaml:"rot_max,omitempty"`
	RotVelMin float32 `yaml:"rot_vel_min,omitempty"`
	RotVelMax float32 `yaml:"rot_vel_max,omitempty"`

	// Motion.
	Gravity        float32 `yaml:"gravity,omitempty"`
	Drag           float32 `yaml:"drag,omitempty"`
	Radial         float32 `yaml:"radial,omitempty"`
	Tangential     float32 `yaml:"tangential,omitempty"`
	TurbulenceAmp  float32 `yaml:"turbulence,omitempty"`
	TurbulenceFreq float32 `yaml:"turbulence_freq,omitempty"`
	LocalSpace     bool    `yaml:"local_space,omitempty"`

	// Appearance.
	Render      string         `yaml:"render,omitempty"` // sprite (default) | volume
	Volume      *RawVolumeLook `yaml:"volume,omitempty"` // absent = DefaultVolumeLook
	Texture     string         `yaml:"texture,omitempty"`
	Material    string         `yaml:"material,omitempty"` // .fmat worn by the layer
	SheetCols   int            `yaml:"sheet_cols,omitempty"`
	SheetRows   int            `yaml:"sheet_rows,omitempty"`
	FrameMode   string         `yaml:"frames,omitempty"`    // over-life (default) | random | static
	Blend       string         `yaml:"blend,omitempty"`     // cutout (default) | alpha | additive
	Billboard   string         `yaml:"billboard,omitempty"` // full (default) | y-axis | fixed
	Lit         bool           `yaml:"lit,omitempty"`
	CastShadows bool           `yaml:"cast_shadows,omitempty"`
	Tint        *RawColor      `yaml:"tint,omitempty"` // default white

	// Collision.
	Collision     string  `yaml:"collision,omitempty"` // none (default) | stop | bounce | kill
	CollideRadius float32 `yaml:"collide_radius,omitempty"`
	Bounce        float32 `yaml:"bounce,omitempty"`
	Friction      float32 `yaml:"friction,omitempty"`

	// Ramps.
	SizeRamp  []RawStop      `yaml:"size_ramp,omitempty"`
	AlphaRamp []RawStop      `yaml:"alpha_ramp,omitempty"`
	ColorRamp []RawColorStop `yaml:"color_ramp,omitempty"`
}

// RawVolumeLook is a volume layer's look (VolumeLook). The whole block is
// omitted while the look is the default, which is every sprite layer; inside
// it, a key is written only when it differs from the default, and every number
// with a meaningful zero that is not its default is a pointer.
type RawVolumeLook struct {
	Density      *float32  `yaml:"density,omitempty"`
	NoiseScale   *float32  `yaml:"noise_scale,omitempty"`
	Displacement *float32  `yaml:"displacement,omitempty"`
	Billow       *float32  `yaml:"billow,omitempty"`
	Erosion      *float32  `yaml:"erosion,omitempty"`
	ErosionRamp  []RawStop `yaml:"erosion_ramp,omitempty"`
	Albedo       *RawColor `yaml:"albedo,omitempty"`
	Absorption   *float32  `yaml:"absorption,omitempty"`
	Emission     float32   `yaml:"emission,omitempty"` // default 0
	EmissionRamp []RawStop `yaml:"emission_ramp,omitempty"`
}

// RawFlash is the effect's light (Flash). The whole block is omitted while the
// flash is the default — which is off — and inside it a key is written only
// when it differs from that default.
type RawFlash struct {
	Color      *RawColor `yaml:"color,omitempty"`
	Brightness float32   `yaml:"brightness,omitempty"` // default 0: no light
	Radius     *float32  `yaml:"radius,omitempty"`     // default 8
	Duration   *float32  `yaml:"duration,omitempty"`   // default 0.08
}

// RawParticle is the top-level document.
type RawParticle struct {
	ID      string     `yaml:"id"`
	Seed    uint32     `yaml:"seed,omitempty"`
	Gravity *float32   `yaml:"gravity,omitempty"` // default DefaultGravity
	Light   *RawFlash  `yaml:"light,omitempty"`   // absent = no flash
	Layers  []RawLayer `yaml:"layers"`
}

// ---------- conversion ----------

func rawToDoc(raw RawParticle) *Doc {
	d := &Doc{
		ID:      raw.ID,
		Seed:    raw.Seed,
		Gravity: f32Default(raw.Gravity, DefaultGravity),
		Light:   flashFromRaw(raw.Light),
	}
	for _, rl := range raw.Layers {
		d.Layers = append(d.Layers, rawToLayer(rl))
	}
	return d
}

func rawToLayer(r RawLayer) Layer {
	l := Layer{
		Name:           r.Name,
		Muted:          r.Muted,
		Rate:           r.Rate,
		Duration:       f32Default(r.Duration, 1),
		Loop:           boolDefault(r.Loop, true),
		Prewarm:        r.Prewarm,
		MaxParticles:   intDefault(r.MaxParticles, 500),
		Shape:          ShapeKindFromName(r.Shape),
		Radius:         r.Radius,
		Angle:          r.Angle,
		BoxHalf:        vec3Default(r.BoxHalf, mgl32.Vec3{0.5, 0.5, 0.5}),
		Thickness:      r.Thickness,
		Dir:            DirModeFromName(r.Dir),
		Direction:      vec3Default(r.Direction, mgl32.Vec3{0, 1, 0}),
		Spread:         r.Spread,
		LifeMin:        r.LifeMin,
		LifeMax:        r.LifeMax,
		SpeedMin:       r.SpeedMin,
		SpeedMax:       r.SpeedMax,
		SizeMin:        r.SizeMin,
		SizeMax:        r.SizeMax,
		RotMin:         r.RotMin,
		RotMax:         r.RotMax,
		RotVelMin:      r.RotVelMin,
		RotVelMax:      r.RotVelMax,
		Gravity:        r.Gravity,
		Drag:           r.Drag,
		Radial:         r.Radial,
		Tangential:     r.Tangential,
		TurbulenceAmp:  r.TurbulenceAmp,
		TurbulenceFreq: r.TurbulenceFreq,
		LocalSpace:     r.LocalSpace,
		Render:         RenderKindFromName(r.Render),
		Volume:         volumeLookFromRaw(r.Volume),
		Texture:        r.Texture,
		Material:       r.Material,
		SheetCols:      r.SheetCols,
		SheetRows:      r.SheetRows,
		FrameMode:      FrameModeFromName(r.FrameMode),
		Blend:          gfx.SpriteBlendFromName(r.Blend),
		Billboard:      gfx.BillboardModeFromName(r.Billboard),
		Lit:            r.Lit,
		CastShadows:    r.CastShadows,
		Tint:           colorDefault(r.Tint),
		Collision:      CollideModeFromName(r.Collision),
		CollideRadius:  r.CollideRadius,
		Bounce:         r.Bounce,
		Friction:       r.Friction,
	}
	for _, b := range r.Bursts {
		l.Bursts = append(l.Bursts, Burst{Time: b.Time, Count: b.Count})
	}
	for _, s := range r.SizeRamp {
		l.SizeRamp = append(l.SizeRamp, Stop{T: s.T, V: s.V})
	}
	for _, s := range r.AlphaRamp {
		l.AlphaRamp = append(l.AlphaRamp, Stop{T: s.T, V: s.V})
	}
	for _, s := range r.ColorRamp {
		l.ColorRamp = append(l.ColorRamp, ColorStop{T: s.T, C: mgl32.Vec3{s.C.R, s.C.G, s.C.B}})
	}
	if l.Texture == "" && l.Material == "" {
		// A layer with neither is drawn as the fallback texture rather than as
		// nothing; a layer whose MATERIAL supplies the texture is left alone,
		// or the fallback would win over what the material names.
		l.Texture = "default"
	}
	return l
}

func docToRaw(d *Doc) RawParticle {
	raw := RawParticle{
		ID:      d.ID,
		Seed:    d.Seed,
		Gravity: f32PtrIfNot(d.Gravity, DefaultGravity),
		Light:   flashToRaw(d.Light),
	}
	for i := range d.Layers {
		raw.Layers = append(raw.Layers, layerToRaw(&d.Layers[i]))
	}
	return raw
}

func layerToRaw(l *Layer) RawLayer {
	r := RawLayer{
		Name:           l.Name,
		Muted:          l.Muted,
		Rate:           l.Rate,
		Duration:       f32PtrIfNot(l.Duration, 1),
		Loop:           boolPtrIfNot(l.Loop, true),
		Prewarm:        l.Prewarm,
		MaxParticles:   intPtrIfNot(l.MaxParticles, 500),
		Shape:          nameIfNot(ShapeKindName(l.Shape), ShapeKindNames[ShapePoint]),
		Radius:         l.Radius,
		Angle:          l.Angle,
		BoxHalf:        vec3PtrIfNot(l.BoxHalf, mgl32.Vec3{0.5, 0.5, 0.5}),
		Thickness:      l.Thickness,
		Dir:            nameIfNot(DirModeName(l.Dir), DirModeNames[DirShape]),
		Direction:      vec3PtrIfNot(l.Direction, mgl32.Vec3{0, 1, 0}),
		Spread:         l.Spread,
		LifeMin:        l.LifeMin,
		LifeMax:        l.LifeMax,
		SpeedMin:       l.SpeedMin,
		SpeedMax:       l.SpeedMax,
		SizeMin:        l.SizeMin,
		SizeMax:        l.SizeMax,
		RotMin:         l.RotMin,
		RotMax:         l.RotMax,
		RotVelMin:      l.RotVelMin,
		RotVelMax:      l.RotVelMax,
		Gravity:        l.Gravity,
		Drag:           l.Drag,
		Radial:         l.Radial,
		Tangential:     l.Tangential,
		TurbulenceAmp:  l.TurbulenceAmp,
		TurbulenceFreq: l.TurbulenceFreq,
		LocalSpace:     l.LocalSpace,
		Render:         nameIfNot(RenderKindName(l.Render), RenderKindNames[RenderSprite]),
		Volume:         volumeLookToRaw(l.Volume),
		Texture:        l.Texture,
		Material:       l.Material,
		SheetCols:      l.SheetCols,
		SheetRows:      l.SheetRows,
		FrameMode:      nameIfNot(FrameModeName(l.FrameMode), FrameModeNames[FrameOverLife]),
		Blend:          nameIfNot(gfx.SpriteBlendName(l.Blend), gfx.SpriteBlendNames[gfx.SpriteBlendCutout]),
		Billboard:      nameIfNot(gfx.BillboardModeName(l.Billboard), gfx.BillboardModeNames[gfx.BillboardFull]),
		Lit:            l.Lit,
		CastShadows:    l.CastShadows,
		Tint:           colorPtrIfNotWhite(l.Tint),
		Collision:      nameIfNot(CollideModeName(l.Collision), CollideModeNames[CollideNone]),
		CollideRadius:  l.CollideRadius,
		Bounce:         l.Bounce,
		Friction:       l.Friction,
	}
	for _, b := range l.Bursts {
		r.Bursts = append(r.Bursts, RawBurst{Time: b.Time, Count: b.Count})
	}
	for _, s := range l.SizeRamp {
		r.SizeRamp = append(r.SizeRamp, RawStop{T: s.T, V: s.V})
	}
	for _, s := range l.AlphaRamp {
		r.AlphaRamp = append(r.AlphaRamp, RawStop{T: s.T, V: s.V})
	}
	for _, s := range l.ColorRamp {
		r.ColorRamp = append(r.ColorRamp, RawColorStop{T: s.T, C: RawColor{R: s.C.X(), G: s.C.Y(), B: s.C.Z()}})
	}
	return r
}

// flashFromRaw reads the `light:` block. No block at all is no flash — the
// zero Flash, so that the absence round-trips — while a block that IS there
// gets DefaultFlash's numbers for whatever it leaves out, so `light:
// {brightness: 4}` is a working muzzle flash rather than a black light of no
// radius.
func flashFromRaw(r *RawFlash) Flash {
	if r == nil {
		return Flash{}
	}
	f := DefaultFlash()
	if r.Color != nil {
		f.Color = mgl32.Vec3{r.Color.R, r.Color.G, r.Color.B}
	}
	f.Brightness = r.Brightness
	f.Radius = f32Default(r.Radius, f.Radius)
	f.Duration = f32Default(r.Duration, f.Duration)
	return f
}

// flashToRaw is the inverse. A flash that lights nothing writes no block at
// all, which is what keeps the `light:` key out of every effect that never
// asked for one — the zero Flash of a document built in code included.
func flashToRaw(f Flash) *RawFlash {
	if !f.On() {
		return nil
	}
	def := DefaultFlash()
	r := RawFlash{
		Brightness: f.Brightness,
		Radius:     f32PtrIfNot(f.Radius, def.Radius),
		Duration:   f32PtrIfNot(f.Duration, def.Duration),
	}
	if f.Color != def.Color {
		r.Color = &RawColor{R: f.Color.X(), G: f.Color.Y(), B: f.Color.Z()}
	}
	return &r
}

func volumeLookFromRaw(r *RawVolumeLook) VolumeLook {
	v := DefaultVolumeLook()
	if r == nil {
		return v
	}
	v.Density = f32Default(r.Density, v.Density)
	v.NoiseScale = f32Default(r.NoiseScale, v.NoiseScale)
	v.Displacement = f32Default(r.Displacement, v.Displacement)
	v.Billow = f32Default(r.Billow, v.Billow)
	v.Erosion = f32Default(r.Erosion, v.Erosion)
	if r.Albedo != nil {
		v.Albedo = mgl32.Vec3{r.Albedo.R, r.Albedo.G, r.Albedo.B}
	}
	v.Absorption = f32Default(r.Absorption, v.Absorption)
	v.Emission = r.Emission
	for _, s := range r.ErosionRamp {
		v.ErosionRamp = append(v.ErosionRamp, Stop{T: s.T, V: s.V})
	}
	for _, s := range r.EmissionRamp {
		v.EmissionRamp = append(v.EmissionRamp, Stop{T: s.T, V: s.V})
	}
	return v
}

func volumeLookToRaw(v VolumeLook) *RawVolumeLook {
	if v.IsDefault() {
		return nil
	}
	d := DefaultVolumeLook()
	r := &RawVolumeLook{
		Density:      f32PtrIfNot(v.Density, d.Density),
		NoiseScale:   f32PtrIfNot(v.NoiseScale, d.NoiseScale),
		Displacement: f32PtrIfNot(v.Displacement, d.Displacement),
		Billow:       f32PtrIfNot(v.Billow, d.Billow),
		Erosion:      f32PtrIfNot(v.Erosion, d.Erosion),
		Absorption:   f32PtrIfNot(v.Absorption, d.Absorption),
		Emission:     v.Emission,
	}
	if v.Albedo != d.Albedo {
		r.Albedo = &RawColor{R: v.Albedo.X(), G: v.Albedo.Y(), B: v.Albedo.Z()}
	}
	for _, s := range v.ErosionRamp {
		r.ErosionRamp = append(r.ErosionRamp, RawStop{T: s.T, V: s.V})
	}
	for _, s := range v.EmissionRamp {
		r.EmissionRamp = append(r.EmissionRamp, RawStop{T: s.T, V: s.V})
	}
	return r
}

// ---------- validation ----------

// validateRawParticle rejects a document that would misbehave rather than
// merely look odd. Anything that can be clamped is clamped on load instead —
// the rule level_schema.go follows, so a file written by a newer build still
// opens.
func validateRawParticle(raw RawParticle) error {
	if len(raw.Layers) == 0 {
		return fmt.Errorf("effect %q has no layers", raw.ID)
	}
	seen := map[string]bool{}
	for i, l := range raw.Layers {
		if l.Name == "" {
			return fmt.Errorf("effect %q layer %d has no name", raw.ID, i)
		}
		if seen[l.Name] {
			// Names address layers from a script and from the editor's list; two
			// the same would make one of them unreachable.
			return fmt.Errorf("effect %q has two layers called %q", raw.ID, l.Name)
		}
		seen[l.Name] = true
		if l.MaxParticles != nil && *l.MaxParticles > MaxPoolSize {
			return fmt.Errorf("effect %q layer %q asks for %d particles; the cap is %d",
				raw.ID, l.Name, *l.MaxParticles, MaxPoolSize)
		}
	}
	return nil
}

// ---------- I/O ----------

// Fingerprint identifies the document's content as SaveParticle would write
// it, for comparing against what was last saved. See fileutil.Fingerprint.
func Fingerprint(d *Doc) uint64 { return fileutil.Fingerprint(docToRaw(d)) }

// SaveParticle writes the document to path as YAML.
func SaveParticle(d *Doc, path string) error {
	raw := docToRaw(d)
	if err := validateRawParticle(raw); err != nil {
		return err
	}
	data, err := yaml.Marshal(&raw)
	if err != nil {
		return fmt.Errorf("marshal effect %q: %w", d.ID, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create particles directory: %w", err)
	}
	if err := fileutil.WriteFileAtomic(path, data); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// LoadParticle reads a .fpar document.
func LoadParticle(path string) (*Doc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var raw RawParticle
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := validateRawParticle(raw); err != nil {
		return nil, err
	}
	d := rawToDoc(raw)
	d.Path = path
	if d.ID == "" {
		d.ID = trimParticleExt(filepath.Base(path))
	}
	return d, nil
}

// ScanParticleFiles lists the .fpar documents available to the open dialog and
// to the level editor's effect picker.
func ScanParticleFiles() []string { return fileutil.ScanFilesByExt(ParticlesDir(), ParticleExt) }

func trimParticleExt(name string) string {
	return name[:len(name)-len(filepath.Ext(name))]
}

// ---------- the pointer-vs-zero helpers ----------

func f32Default(p *float32, def float32) float32 {
	if p != nil {
		return *p
	}
	return def
}

func f32PtrIfNot(v, def float32) *float32 {
	if v == def {
		return nil
	}
	return &v
}

func intDefault(p *int, def int) int {
	if p != nil {
		return *p
	}
	return def
}

func intPtrIfNot(v, def int) *int {
	if v == def {
		return nil
	}
	return &v
}

func boolDefault(p *bool, def bool) bool {
	if p != nil {
		return *p
	}
	return def
}

func boolPtrIfNot(v, def bool) *bool {
	if v == def {
		return nil
	}
	return &v
}

func nameIfNot(name, def string) string {
	if name == def {
		return ""
	}
	return name
}

func vec3Default(p *RawVec3, def mgl32.Vec3) mgl32.Vec3 {
	if p == nil {
		return def
	}
	return mgl32.Vec3{p.X, p.Y, p.Z}
}

func vec3PtrIfNot(v, def mgl32.Vec3) *RawVec3 {
	if v == def {
		return nil
	}
	return &RawVec3{X: v.X(), Y: v.Y(), Z: v.Z()}
}

func colorDefault(c *RawColor) mgl32.Vec3 {
	if c == nil {
		return mgl32.Vec3{1, 1, 1}
	}
	return mgl32.Vec3{c.R, c.G, c.B}
}

func colorPtrIfNotWhite(v mgl32.Vec3) *RawColor {
	if v == (mgl32.Vec3{1, 1, 1}) {
		return nil
	}
	return &RawColor{R: v.X(), G: v.Y(), B: v.Z()}
}
