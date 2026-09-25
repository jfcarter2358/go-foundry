package script

// script_actions_decal.go — the verb that leaves a mark on the world.
//
//	decal "bullet_hole" <pos>                 a mark on whatever surface is there
//	decal "bullet_hole" <pos> {"size": 0.1}   ... with options
//	decal "scorch" @crate {"blend": "multiply", "life": 20}
//
// This is the runtime half of decals, and it is what they are mostly for: an
// authored decal is a sign on a wall, but a SPAWNED one is every bullet hole,
// scorch mark and blood splatter a fight leaves behind, and there are hundreds of
// those.
//
// The design decision worth knowing about is that the verb takes a POINT and
// works out the plane itself. What a script has when it wants a mark is where the
// shot landed — `hitscan(:shot:, {"points": true})` hands it over — and a decal
// needs an orientation. Making the script carry a normal around would mean
// teaching hitscan to report one, teaching every weapon script to thread it
// through, and getting it wrong in the one place that forgot; asking the geometry
// instead (world.SurfaceNormalNear) makes `decal "hole" point` the whole of the
// call. A script that HAS a normal — because it is placing a mark on a surface it
// chose rather than one it hit — passes it as the "normal" option and skips the
// search.
//
// Everything it places is Transient, which is what keeps a firefight from filling
// a level: those are capped at world.MaxTransientDecals with the oldest recycled,
// and neither the level file nor a save game writes them as level content.
//
// Nothing here touches GL. The mesh is clipped out of the level's own triangles
// by the render pass's SyncDecals on the next frame, and the texture is loaded
// through the same deferred path a sprite's is (markTex).

import (
	"fmt"
	"math"

	"foundry/internal/flang"
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// decalSearchRadius is how far from the given point the verb looks for a surface
// to lie on, as a multiple of the decal's own width.
//
// A multiple rather than a fixed distance because the thing being placed sets the
// scale: a 2-metre poster nudged a few centimetres off the wall should still find
// it, and a 5-centimetre bullet hole must not find the floor half a metre below.
const decalSearchRadius = float32(1.5)

// decalOption is one switch of the options dict, its effect, and its one-line
// reference entry. A table rather than a switch for the reason hitscan's is one:
// the "no option called X" message can then enumerate the real ones.
type decalOption struct {
	summary string
	apply   func(o *decalOpts, v flang.Value) error
}

// decalOpts is a parsed options dict.
type decalOpts struct {
	width, height float32
	depth         float32
	hasDepth      bool
	roll          float32
	hasRoll       bool
	normal        mgl32.Vec3
	hasNormal     bool
	material      string
	blend         gfx.DecalBlend
	hasBlend      bool
	tint          mgl32.Vec3
	hasTint       bool
	alpha         float32
	life          float32
	order         int
	maxAngle      float32
	unlit         bool
	permanent     bool
}

var decalOptions = map[string]decalOption{
	"size": {
		"how wide and tall the mark is, in world units: a number for a square, or a vec for its width and height",
		func(o *decalOpts, v flang.Value) error {
			if p, ok := toVec3(v); ok {
				if p.X() <= 0 || p.Y() <= 0 {
					return fmt.Errorf("decal size needs a positive width and height, not %v", p)
				}
				o.width, o.height = p.X(), p.Y()
				return nil
			}
			f, ok := toFloat32(v)
			if !ok {
				return fmt.Errorf("decal size takes a number or a vec, not a %s", flang.TypeOf(v))
			}
			if f <= 0 {
				return fmt.Errorf("decal size needs to be above zero, not %v", f)
			}
			o.width, o.height = f, f
			return nil
		},
	},
	"depth": {
		"how far along its facing the projection reaches, either side of the point; keep it small or the mark prints on what is behind the wall too",
		func(o *decalOpts, v flang.Value) error {
			f, ok := toFloat32(v)
			if !ok {
				return fmt.Errorf("decal depth takes a number, not a %s", flang.TypeOf(v))
			}
			if f <= 0 {
				return fmt.Errorf("decal depth needs to be above zero, not %v", f)
			}
			o.depth, o.hasDepth = f, true
			return nil
		},
	},
	"normal": {
		"the direction the mark faces out of the surface, as a vec; without it the surface at the point is found and used",
		func(o *decalOpts, v flang.Value) error {
			p, ok := toVec3(v)
			if !ok {
				return fmt.Errorf("decal normal takes a vec, not a %s", flang.TypeOf(v))
			}
			if p.LenSqr() < 1e-12 {
				return fmt.Errorf("decal normal cannot be the zero vector")
			}
			o.normal, o.hasNormal = p.Normalize(), true
			return nil
		},
	},
	"roll": {
		"a spin of the mark about its own facing, in degrees; without it a spin is derived from the point, so two holes in one wall are not identical",
		func(o *decalOpts, v flang.Value) error {
			f, ok := toFloat32(v)
			if !ok {
				return fmt.Errorf("decal roll takes a number, not a %s", flang.TypeOf(v))
			}
			o.roll, o.hasRoll = mgl32.DegToRad(f), true
			return nil
		},
	},
	"material": {
		"the .fmat the mark wears, by name",
		func(o *decalOpts, v flang.Value) error {
			s, ok := toStr(v)
			if !ok {
				return fmt.Errorf("decal material takes a str, not a %s", flang.TypeOf(v))
			}
			o.material = s
			return nil
		},
	},
	"blend": {
		`how it combines with the surface: "alpha" (the default), "multiply" for scorch and dirt, or "additive"`,
		func(o *decalOpts, v flang.Value) error {
			s, ok := toStr(v)
			if !ok {
				return fmt.Errorf("decal blend takes a str, not a %s", flang.TypeOf(v))
			}
			for _, n := range gfx.DecalBlendNames {
				if n == s {
					o.blend, o.hasBlend = gfx.DecalBlendFromName(s), true
					return nil
				}
			}
			return fmt.Errorf("decal blend %q is not one of %v", s, gfx.DecalBlendNames)
		},
	},
	"tint": {
		"a colour multiplier as a vec of r, g, b — one grey scorch texture tinted eight ways is eight marks",
		func(o *decalOpts, v flang.Value) error {
			p, ok := toVec3(v)
			if !ok {
				return fmt.Errorf("decal tint takes a vec, not a %s", flang.TypeOf(v))
			}
			o.tint, o.hasTint = p, true
			return nil
		},
	},
	"alpha": {
		"opacity, 0 to 1",
		func(o *decalOpts, v flang.Value) error {
			f, ok := toFloat32(v)
			if !ok {
				return fmt.Errorf("decal alpha takes a number, not a %s", flang.TypeOf(v))
			}
			o.alpha = clamp01f(f)
			return nil
		},
	},
	"life": {
		"seconds the mark lasts before fading out; without it the mark stays until the cap on spawned decals recycles it",
		func(o *decalOpts, v flang.Value) error {
			f, ok := toFloat32(v)
			if !ok {
				return fmt.Errorf("decal life takes a number, not a %s", flang.TypeOf(v))
			}
			if f <= 0 {
				return fmt.Errorf("decal life needs to be above zero, not %v", f)
			}
			o.life = f
			return nil
		},
	},
	"order": {
		"draw order among overlapping marks; higher lies on top",
		func(o *decalOpts, v flang.Value) error {
			f, ok := toFloat32(v)
			if !ok {
				return fmt.Errorf("decal order takes a number, not a %s", flang.TypeOf(v))
			}
			o.order = int(f)
			return nil
		},
	},
	"max_angle": {
		"how far, in degrees, a surface may lean from the mark's facing and still be painted",
		func(o *decalOpts, v flang.Value) error {
			f, ok := toFloat32(v)
			if !ok {
				return fmt.Errorf("decal max_angle takes a number, not a %s", flang.TypeOf(v))
			}
			if f <= 0 || f >= 180 {
				return fmt.Errorf("decal max_angle needs to be between 0 and 180, not %v", f)
			}
			o.maxAngle = f
			return nil
		},
	},
	"unlit": {
		"ignore the scene's lights and draw the texture's own colour — for a glowing rune, not for a bullet hole",
		func(o *decalOpts, v flang.Value) error {
			b, ok := toBool(v)
			if !ok {
				return fmt.Errorf("decal unlit takes a bool, not a %s", flang.TypeOf(v))
			}
			o.unlit = b
			return nil
		},
	},
	"permanent": {
		"keep the mark out of the cap on spawned decals, and save it with the game — for the few a script places deliberately rather than per impact",
		func(o *decalOpts, v flang.Value) error {
			b, ok := toBool(v)
			if !ok {
				return fmt.Errorf("decal permanent takes a bool, not a %s", flang.TypeOf(v))
			}
			o.permanent = b
			return nil
		},
	},
}

// DecalOptionSummaries is each option's one-line reference entry, for the script
// editor's completion and SCRIPTING.md.
func DecalOptionSummaries() map[string]string {
	out := make(map[string]string, len(decalOptions))
	for k, o := range decalOptions {
		out[k] = o.summary
	}
	return out
}

// decalVerbs is merged into scriptVerbs at init.
var decalVerbs = map[string]scriptVerb{
	"decal": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("decal takes a texture name and a place to put it")
		}
		tex, ok := toStr(args[0])
		if !ok {
			return nil, fmt.Errorf("decal takes a texture name first, not a %s", flang.TypeOf(args[0]))
		}
		at, ok := rt.positionOf(args[1])
		if !ok {
			return nil, fmt.Errorf("decal takes a vec or an object to mark, not a %s", flang.TypeOf(args[1]))
		}
		o := decalOpts{width: world.DefaultDecalSize, height: world.DefaultDecalSize, alpha: 1}
		if len(args) > 2 {
			if err := applyDecalOptions(&o, args[2]); err != nil {
				return nil, err
			}
		}
		if rt.lvl == nil {
			return flang.NullVal{}, nil
		}

		// The plane. An explicit normal wins; otherwise the surface under the
		// point is asked, and finding none means there is nothing here to mark —
		// which is an answer, not an error, so the verb returns null and a script
		// firing at the sky simply leaves no hole.
		normal := o.normal
		if !o.hasNormal {
			n, found := world.SurfaceNormalNear(rt.lvl, at, o.width*decalSearchRadius)
			if !found {
				return flang.NullVal{}, nil
			}
			normal = n
		}
		roll := o.roll
		if !o.hasRoll {
			roll = decalRollFor(at)
		}

		rt.markDirty()
		d := world.NewDecal(world.UniqueDecalID(rt.lvl, "decal"), at, normal)
		d.Rotation = world.DecalRotationFor(normal, roll)
		d.Texture = tex
		d.Width, d.Height = o.width, o.height
		if o.hasDepth {
			d.Depth = o.depth
		} else {
			// Scaled to the mark rather than left at the type's default: a
			// 2-metre poster wants a deeper box than a 5-centimetre bullet hole,
			// and the depth is what decides whether the mark wraps around a
			// corner or stops at it.
			d.Depth = minF(o.width, o.height)
		}
		d.Material = o.material
		if o.hasBlend {
			d.Blend = o.blend
		}
		if o.hasTint {
			d.Tint = o.tint
		}
		d.Alpha = o.alpha
		d.Unlit = o.unlit
		d.Order = o.order
		if o.maxAngle > 0 {
			d.MaxAngle = o.maxAngle
		}
		d.Lifetime = o.life
		// Transient unless the script asked otherwise: see the file comment.
		d.Transient = !o.permanent

		rt.lvl.Decals = append(rt.lvl.Decals, d)
		// The texture may not be resident, and a tick must not touch GL, so the
		// load is deferred exactly as a sprite's texture write is.
		rt.markTextureDirty(tex)
		if o.material != "" {
			rt.markTextureDirty(o.material)
		}
		// Capped here rather than in the tick, so the list can never be longer
		// than the cap even for one frame — and so that a script spawning a
		// hundred marks in one tick drops the first ninety-odd immediately
		// instead of building a hundred meshes and then throwing them away.
		if world.CapTransientDecals(rt.lvl) {
			// The cap dropped decals, so every index in the tick's cache is now
			// wrong. Rebuilt whole rather than patched: the cache is one map per
			// kind, and cacheAdded rebuilds it when the level it was built for is
			// not the one in hand — which is exactly what clearing it arranges.
			rt.cache = nil
		}
		rt.cacheAdded(func(c *refCache) { c.decal[d.ID] = len(rt.lvl.Decals) - 1 })
		return flang.RefVal{Kind: flang.RefDecal, A: d.ID}, nil
	},
}

// applyDecalOptions reads the options dict, naming an unknown key rather than
// ignoring it — a misspelled option that silently did nothing is the hardest kind
// of script bug to see.
func applyDecalOptions(o *decalOpts, v flang.Value) error {
	d, ok := v.(*flang.DictVal)
	if !ok || d == nil {
		return fmt.Errorf("decal takes a dict of options third, not a %s", flang.TypeOf(v))
	}
	for k, val := range d.M {
		opt, ok := decalOptions[k]
		if !ok {
			return fmt.Errorf("decal has no option called %q%s", k, didYouMeanDecalOption(k))
		}
		if err := opt.apply(o, val); err != nil {
			return err
		}
	}
	return nil
}

// didYouMeanDecalOption suggests the nearest real option name, or nothing —
// hitscanNearestOption's rule, through the same shared matcher.
func didYouMeanDecalOption(name string) string {
	names := make([]string, 0, len(decalOptions))
	for k := range decalOptions {
		names = append(names, k)
	}
	if best := world.NearestName(name, names); best != "" {
		return fmt.Sprintf("; did you mean %q?", best)
	}
	return ""
}

// decalRollFor is the spin a mark takes when the script did not ask for one: a
// value derived from where it landed, so that ten holes in one wall are ten
// different holes rather than ten copies of the same sticker.
//
// Derived from the position rather than drawn from a random source on purpose.
// The headless simulator replays a level to compare two runs (foundry sim), and a
// random roll would make every frame of decal geometry differ between them for no
// reason anyone could see.
func decalRollFor(p mgl32.Vec3) float32 {
	h := uint32(math.Float32bits(p.X()))*2654435761 ^
		uint32(math.Float32bits(p.Y()))*2246822519 ^
		uint32(math.Float32bits(p.Z()))*3266489917
	// Mixed once more so that nearby points — two rounds into the same plank —
	// do not land on nearby angles.
	h ^= h >> 15
	h *= 2246822519
	h ^= h >> 13
	return float32(h%3600) / 3600 * 2 * math.Pi
}

func minF(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}
