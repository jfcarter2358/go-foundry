package world

// volume_ambient.go — regions the sky's light does not reach
// (docs/adventure/PHASE_B.md).
//
// The level's ambient light is a sky: light_model.glsl's ambientAt is a
// hemisphere, brighter facing up, the same everywhere. With live GI on
// (gi.go) the probes find out that a cave's floor cannot see that sky and
// light it by what bounces in instead. With GI off — a player's choice
// (graphics.yaml gi_quality: off), and the cheapest frame — a cave is lit
// exactly like the desert outside it, and only baked vertex AO darkens what
// was baked: a rock scattered on the cave floor, a character walking in, a
// ruin built inside, are lit as in the open.
//
// An AMBIENT volume says so instead: inside its shape the sky term is
// multiplied down — Sky for surfaces facing up (they see the roof), Ground
// for surfaces facing down (they see the floor, which is in the dark too) —
// eased in over Fade metres from its walls, so a cave mouth darkens as you
// walk in rather than at a line. Every lit surface reads it through
// ambientLight (world, the Nanite material pass, entities, terrain, scatter,
// sprites), and fog.glsl dims the sky-lit haze along the part of the view
// ray inside it (Fog), so a cave's air is not as bright as the valley's.
// Dust raises the light shafts' density inside (fog_shafts.go): a cave's
// beam through dusty air.
//
// It is a field on Volume, like Fog, for Fog's reasons: a shape, a rotation,
// half-extents, an ID, an `enabled` a script can flip, the editor's cage and
// gizmos are all there already.
//
// With GI on the probes already see the walls; the volume still scales the
// share of the sky GI leaves (giAmbientFloor), so a level authored for GI
// off looks the same with it on, only with bounce light added.

import (
	"math"
	"sort"

	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// MaxAmbientVolumes is how many the shader carries at once: the nearest to
// the camera win. MUST match MAX_AMBIENT_VOLUMES in scene_block.glsl.
const MaxAmbientVolumes = 8

// VolumeAmbient is the ambient cut a volume makes.
type VolumeAmbient struct {
	// Enabled switches it off and keeps the numbers.
	Enabled bool
	// Sky multiplies the sky light a surface facing straight up receives,
	// deep inside the region; Ground one facing straight down. Between the
	// two by the surface's normal.
	Sky, Ground float32
	// Fade is how far in from the region's walls, in metres, the cut takes
	// to reach full strength. 0 is a hard edge.
	Fade float32
	// Fog is how much of the cut the sky-lit haze inside takes: 1 the same,
	// 0 none (fog.glsl).
	Fog float32
	// Dust is extinction per metre added to the light shafts' medium inside
	// (fog_shafts.go); 0 adds none.
	Dust float32
}

// VolumeAmbient defaults.
const (
	VolumeAmbientDefaultSky  float32 = 0.15
	VolumeAmbientDefaultFade float32 = 3
)

// DefaultVolumeAmbient is what ticking the box gives: a cave's darkness.
func DefaultVolumeAmbient() VolumeAmbient {
	return VolumeAmbient{
		Enabled: true,
		Sky:     VolumeAmbientDefaultSky,
		Ground:  VolumeAmbientDefaultSky,
		Fade:    VolumeAmbientDefaultFade,
		Fog:     1,
	}
}

// Clamp puts every field in range.
func (a *VolumeAmbient) Clamp() {
	if a == nil {
		return
	}
	a.Sky = clampF(a.Sky, 0, 2)
	a.Ground = clampF(a.Ground, 0, 2)
	a.Fade = clampF(a.Fade, 0, 1000)
	a.Fog = clampF(a.Fog, 0, 1)
	a.Dust = clampF(a.Dust, 0, 1)
}

// HasAmbient reports whether the volume cuts the ambient: switched on, the
// volume enabled, and a region to cut it in.
func (v *Volume) HasAmbient() bool {
	return v != nil && v.Ambient != nil && v.Ambient.Enabled && v.Enabled && v.HasBounds()
}

// AmbientVolumes returns the volumes whose cut is in effect, the nearest to
// camPos first (by their boxes), at most MaxAmbientVolumes.
func AmbientVolumes(vols []Volume, lvl *Level, camPos mgl32.Vec3) []*Volume {
	type cand struct {
		v *Volume
		d float32
	}
	var cs []cand
	for i := range vols {
		v := &vols[i]
		if !v.HasAmbient() {
			continue
		}
		d := float32(0)
		if lo, hi, ok := v.WorldAABB(lvl); ok {
			d = boxDistance(camPos, lo, hi)
		}
		cs = append(cs, cand{v, d})
	}
	sort.SliceStable(cs, func(a, b int) bool { return cs[a].d < cs[b].d })
	if len(cs) > MaxAmbientVolumes {
		cs = cs[:MaxAmbientVolumes]
	}
	out := make([]*Volume, len(cs))
	for i, c := range cs {
		out[i] = c.v
	}
	return out
}

// boxDistance is how far p is from the box [lo, hi]: 0 inside.
func boxDistance(p, lo, hi mgl32.Vec3) float32 {
	var s float64
	for i := 0; i < 3; i++ {
		d := math.Max(float64(lo[i]-p[i]), math.Max(0, float64(p[i]-hi[i])))
		s += d * d
	}
	return float32(math.Sqrt(s))
}

// PackAmbientVolumes writes the ambient volumes in effect into the scene
// block, nearest camPos first. The slots past the count are zeroed, as
// PackFogVolumes leaves its own. No GL.
func PackAmbientVolumes(b *gfx.SceneBlock, vols []Volume, lvl *Level, camPos mgl32.Vec3) {
	n := 0
	for _, v := range AmbientVolumes(vols, lvl, camPos) {
		a := v.Ambient
		basis := v.WorldFogBasis(lvl)
		for c := 0; c < 3; c++ {
			b.AmbVolBasis[n][c] = [4]float32{basis[3*c], basis[3*c+1], basis[3*c+2], 0}
		}
		c := v.WorldPosition(lvl)
		b.AmbVolCenter[n] = [4]float32{c[0], c[1], c[2], float32(v.Shape)}
		b.AmbVolHalf[n] = [4]float32{v.HalfX, v.HalfY, v.HalfZ, a.Fade}
		b.AmbVolParams[n] = [4]float32{a.Sky, a.Ground, a.Fog, a.Dust}
		n++
	}
	b.NumAmbVolumes = int32(n)
	for i := n; i < MaxAmbientVolumes; i++ {
		b.AmbVolBasis[i] = [3][4]float32{}
		b.AmbVolCenter[i], b.AmbVolHalf[i], b.AmbVolParams[i] = [4]float32{}, [4]float32{}, [4]float32{}
	}
}

// AmbientFactorAt is light_model.glsl's ambientVolumeFactor on the CPU — the
// multiplier on the sky light at p for a surface of normal n — for tests and
// tools. 1 outside every volume.
func AmbientFactorAt(vols []Volume, lvl *Level, p, n mgl32.Vec3) float32 {
	f := float32(1)
	for _, v := range AmbientVolumes(vols, lvl, p) {
		a := v.Ambient
		basis := v.WorldFogBasis(lvl)
		q := basis.Mul3x1(p.Sub(v.WorldPosition(lvl)))
		inside := shapeInsideDepth(v.Shape, q, v.HalfExtents())
		if inside <= 0 {
			continue
		}
		w := float32(1)
		if a.Fade > 0 {
			w = clampF(inside/a.Fade, 0, 1)
		}
		up := clampF(0.5+0.5*n[1], 0, 1)
		k := a.Ground + (a.Sky-a.Ground)*up
		f = min(f, 1+(k-1)*w)
	}
	return f
}

// shapeInsideDepth is how far inside the shape a point is, in world units
// (≤ 0 outside), from q, the point in the shape's normalised space, and the
// half extents. Mirrors light_model.glsl's ambVolInside.
func shapeInsideDepth(shape VolumeShape, q, half mgl32.Vec3) float32 {
	switch shape {
	case VolumeSphere:
		return (1 - q.Len()) * min(half[0], half[1], half[2])
	case VolumeCylinder:
		r := (1 - mgl32.Vec2{q[0], q[2]}.Len()) * min(half[0], half[2])
		return min(r, (1-abs32(q[1]))*half[1])
	default:
		return min((1-abs32(q[0]))*half[0], (1-abs32(q[1]))*half[1], (1-abs32(q[2]))*half[2])
	}
}

func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

// ---------- YAML ----------

// RawVolumeAmbient is the `ambient:` block. Disabled for the reason
// RawVolumeFog's is; Ground absent means the same as Sky.
type RawVolumeAmbient struct {
	// Disabled keeps the numbers with the cut switched off.
	Disabled bool `yaml:"disabled,omitempty"`
	// Sky multiplies the sky light an upward-facing surface gets deep inside
	// (0 none, 1 unchanged). Default 0.15.
	Sky *float32 `yaml:"sky,omitempty"`
	// Ground is the same for a downward-facing surface (a roof, an
	// overhang); absent means the same as sky.
	Ground *float32 `yaml:"ground,omitempty"`
	// Fade is how far in from the walls, in metres, the cut eases in.
	// Default 3; 0 is a hard edge.
	Fade *float32 `yaml:"fade,omitempty"`
	// Fog is how much of the cut the sky-lit haze inside takes, 0..1.
	// Default 1.
	Fog *float32 `yaml:"fog,omitempty"`
	// Dust is extinction per metre added to the light shafts' medium inside
	// (fog: shafts:). Default 0.
	Dust *float32 `yaml:"dust,omitempty"`
}

// VolumeAmbientFromRaw reads the block; nil stays nil.
func VolumeAmbientFromRaw(r *RawVolumeAmbient) *VolumeAmbient {
	if r == nil {
		return nil
	}
	a := DefaultVolumeAmbient()
	a.Enabled = !r.Disabled
	if r.Sky != nil {
		a.Sky = *r.Sky
	}
	a.Ground = a.Sky
	if r.Ground != nil {
		a.Ground = *r.Ground
	}
	if r.Fade != nil {
		a.Fade = *r.Fade
	}
	if r.Fog != nil {
		a.Fog = *r.Fog
	}
	if r.Dust != nil {
		a.Dust = *r.Dust
	}
	a.Clamp()
	return &a
}

// VolumeAmbientToRaw is the inverse: sky, fade and fog always, ground only
// where it differs from sky, dust only when there is some.
func VolumeAmbientToRaw(a *VolumeAmbient) *RawVolumeAmbient {
	if a == nil {
		return nil
	}
	c := *a
	c.Clamp()
	r := &RawVolumeAmbient{Disabled: !c.Enabled, Sky: ptrF(c.Sky), Fade: ptrF(c.Fade), Fog: ptrF(c.Fog)}
	if c.Ground != c.Sky {
		r.Ground = ptrF(c.Ground)
	}
	if c.Dust != 0 {
		r.Dust = ptrF(c.Dust)
	}
	return r
}
