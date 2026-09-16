package script

// script_actions_hitscan.go — the verb that asks what the player is looking at.
//
// `hitscan` casts one ray from the reticle — the centre of the screen, which is
// the camera's own position and direction — and posts what it met back to the
// caller as a message it names. It answers with a message rather than a return
// value for the reason every other engine answer is a message: a shot is a
// thing that happened, and the handler for "I hit something" is the same shape
// as the handler for `:collide:` or `:interact:` whether the cause was a script
// or the world.
//
// The ray itself is internal/collision's (raycast.go). Everything here is the
// language boundary: the options dict in, the atom and refs out.
//
// One design note worth keeping. Bodies do not stop the ray — only the level's
// own shape does — so `hitscan(:shot:)` reads the nearest thing hit and
// `hitscan(:shot:, {"all": true})` reads everything in the line of fire. A cast
// that stopped at the first entity could answer the first question and not the
// second, and a penetrating shot is the one nobody can write around.

import (
	"fmt"
	"math"
	"sort"

	"foundry/internal/collision"
	"foundry/internal/flang"
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// hitscanDefaultRange is how far a cast reaches when the options say nothing,
// in world units. Chosen to cross any level anyone has built rather than to
// model a weapon: a script that cares about falloff says so.
const hitscanDefaultRange = 1000.0

// hitscanOption is one switch, its default, and how to read it. A table rather
// than a switch for the same reason the property tables are one: the "no option
// called X, did you mean Y" message enumerates it.
type hitscanOption struct {
	// summary is the one-line reference entry, shown by the script editor and
	// repeated in SCRIPTING.md.
	summary string
	apply   func(o *hitscanOpts, v flang.Value) error
}

// hitscanOpts is a parsed options dict.
type hitscanOpts struct {
	all    bool
	points bool
	// spread is the full width, in degrees, of the cone the shot may land in.
	// Zero is a ray straight down the reticle, which is what every cast was
	// before this existed.
	spread float32
	filter collision.RayFilter
}

var hitscanOptions = map[string]hitscanOption{
	"all": {
		"report every object in the line of fire instead of the nearest one",
		func(o *hitscanOpts, v flang.Value) error {
			b, err := hitscanBool("all", v)
			o.all = b
			return err
		},
	},
	"points": {
		"add a third argument: where each hit landed — a vec (or null) for the nearest, a list of vecs beside the hits for all",
		func(o *hitscanOpts, v flang.Value) error {
			b, err := hitscanBool("points", v)
			o.points = b
			return err
		},
	},
	"range": {
		"how far the ray travels, in world units",
		func(o *hitscanOpts, v flang.Value) error {
			n, ok := numericValue(v)
			if !ok {
				return fmt.Errorf("hitscan's \"range\" is a number, not a %s", flang.TypeOf(v))
			}
			o.filter.Range = float32(n)
			return nil
		},
	},
	"spread": {
		"the full width, in degrees, of the cone the shot scatters inside (0 — dead centre — by default)",
		func(o *hitscanOpts, v flang.Value) error {
			n, ok := numericValue(v)
			if !ok {
				return fmt.Errorf("hitscan's \"spread\" is a number of degrees, not a %s", flang.TypeOf(v))
			}
			if n < 0 {
				return fmt.Errorf("hitscan's \"spread\" is a cone width in degrees and cannot be negative")
			}
			o.spread = float32(n)
			return nil
		},
	},
	"entities": {
		"entities can be hit (on by default)",
		func(o *hitscanOpts, v flang.Value) error {
			b, err := hitscanBool("entities", v)
			o.filter.Entities = b
			return err
		},
	},
	"volumes": {
		"trigger volumes can be hit (off by default)",
		func(o *hitscanOpts, v flang.Value) error {
			b, err := hitscanBool("volumes", v)
			o.filter.Volumes = b
			return err
		},
	},
	"geometry": {
		"walls, floors and ceilings stop the ray and can be hit (on by default)",
		func(o *hitscanOpts, v flang.Value) error {
			b, err := hitscanBool("geometry", v)
			o.filter.Geometry = b
			return err
		},
	},
	"visible_only": {
		"report only what is drawn — entities and walls whose visible flag is set",
		func(o *hitscanOpts, v flang.Value) error {
			b, err := hitscanBool("visible_only", v)
			o.filter.VisibleOnly = b
			return err
		},
	},
	"tag": {
		"report only objects carrying this tag",
		func(o *hitscanOpts, v flang.Value) error {
			s, ok := toStr(v)
			if !ok {
				return fmt.Errorf("hitscan's \"tag\" is a string, not a %s", flang.TypeOf(v))
			}
			o.filter.Tag = s
			return nil
		},
	},
	"ignore": {
		"a ref, or a list of them, the ray passes straight through",
		func(o *hitscanOpts, v flang.Value) error {
			return hitscanIgnore(o, v)
		},
	},
}

// HitscanOptionNames returns the option keys, sorted — what the script editor's
// reference panel lists and what a misspelling is measured against.
func HitscanOptionNames() []string {
	out := make([]string, 0, len(hitscanOptions))
	for k := range hitscanOptions {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// HitscanOptionSummary returns one option's reference line.
func HitscanOptionSummary(name string) string { return hitscanOptions[name].summary }

var hitscanVerbs = map[string]scriptVerb{
	"hitscan": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) < 1 || len(args) > 2 {
			return nil, fmt.Errorf("hitscan takes an atom to answer with, and optionally a dict of options")
		}
		atom, ok := args[0].(flang.AtomVal)
		if !ok {
			return nil, fmt.Errorf("hitscan's first argument is the atom it answers with, not a %s", flang.TypeOf(args[0]))
		}
		opts, err := parseHitscanOpts(rt, args, pos)
		if err != nil {
			return nil, err
		}

		// The reticle is the camera: the centre of the screen is exactly where
		// the camera looks, so there is no unprojection to do. Both are already
		// world-space, including for a player riding in a frame — the vehicle
		// camera writes them that way (app/vehicle_input.go).
		origin, dir := gfx.Cam.Position, gfx.Cam.Direction
		if world.PlayerEntity == nil || dir.Len() < 1e-6 {
			rt.warnAt(pos, "hitscan did nothing: there is no player to cast from, which is the case in the editor and before the level finishes loading")
			return flang.NullVal{}, nil
		}

		dir = scatter(dir, opts.spread, rt.randomUnit)
		res := collision.Raycast(rt.lvl, origin, dir, opts.filter)
		payload := []flang.Value{
			hitscanPayload(res, opts.all),
			vecValue(res.Stop),
		}
		if opts.points {
			payload = append(payload, hitscanPoints(res, opts.all))
		}
		_ = rt.post(rt.self, string(atom), payload, pos)
		return flang.NullVal{}, nil
	},
}

// scatter tilts a shot off the reticle by a random angle inside a cone of that
// full width, in degrees. A spread of zero is returned untouched, so the cost
// of this existing is one comparison for every cast that does not want it.
//
// The landing point is uniform over the cone's disc rather than over its
// angle: drawing the angle straight would pile most shots into the middle few
// degrees and leave the rim nearly empty, which reads as a gun that is far
// more accurate than its number says. The square root is what spreads a
// uniform draw over area instead of over radius.
//
// The cone is built on any two axes square to the aim — which of them is which
// does not matter, since the second draw spins the offset all the way round.
func scatter(dir mgl32.Vec3, spreadDeg float32, draw func() float64) mgl32.Vec3 {
	if spreadDeg <= 0 {
		return dir
	}
	dir = dir.Normalize()
	half := float64(spreadDeg) * math.Pi / 360 // degrees of full width to radians of half-angle
	theta := math.Atan(math.Tan(half) * math.Sqrt(draw()))
	phi := 2 * math.Pi * draw()

	// An axis the aim is not parallel to, so the cross product below is a real
	// direction rather than a zero vector.
	up := mgl32.Vec3{0, 1, 0}
	if math.Abs(float64(dir.Y())) > 0.99 {
		up = mgl32.Vec3{1, 0, 0}
	}
	right := dir.Cross(up).Normalize()
	up = right.Cross(dir).Normalize()

	off := right.Mul(float32(math.Cos(phi))).Add(up.Mul(float32(math.Sin(phi))))
	return dir.Mul(float32(math.Cos(theta))).Add(off.Mul(float32(math.Sin(theta)))).Normalize()
}

// hitscanPayload is the first argument the answering message carries: the
// nearest hit, or the whole line of fire.
//
// The two shapes are a ref-or-null and a list, never one standing in for the
// other, so a handler's clause head says which mode it was written for.
func hitscanPayload(res collision.RayResult, all bool) flang.Value {
	if !all {
		h, ok := res.First()
		if !ok {
			return flang.NullVal{}
		}
		return hitscanRef(h)
	}
	elems := make([]flang.Value, 0, len(res.Hits))
	for _, h := range res.Hits {
		elems = append(elems, hitscanRef(h))
	}
	return flang.NewList(elems...)
}

// hitscanPoints is the third argument `"points": true` adds: where each hit
// in the payload landed, shaped the same way the payload is — a vec or null
// beside a ref or null, a list of vecs index-for-index beside a list of refs.
//
// Opt-in rather than always sent because it changes the message's arity, and
// every handler written before it takes exactly two arguments. `at` alone
// cannot stand in for it: bodies do not stop the ray, so `at` is the wall
// behind an enemy, not the place on the enemy a shot met.
func hitscanPoints(res collision.RayResult, all bool) flang.Value {
	if !all {
		h, ok := res.First()
		if !ok {
			return flang.NullVal{}
		}
		return vecValue(h.Point)
	}
	elems := make([]flang.Value, 0, len(res.Hits))
	for _, h := range res.Hits {
		elems = append(elems, vecValue(h.Point))
	}
	return flang.NewList(elems...)
}

// hitscanRef names a hit the way a script addresses it.
func hitscanRef(h collision.RayHit) flang.RefVal {
	switch h.Kind {
	case collision.RayHitEntity:
		return flang.RefVal{Kind: flang.RefEntity, A: h.ID}
	case collision.RayHitVolume:
		return flang.RefVal{Kind: flang.RefVolume, A: h.ID}
	case collision.RayHitBrush:
		// A brush ID is level-unique, so the chunk it sits in is not part of
		// the address the way a sector's chunk used to be.
		return flang.RefVal{Kind: flang.RefBrush, A: h.ID}
	}
	return flang.RefVal{}
}

// ---------- options ----------

func parseHitscanOpts(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (hitscanOpts, error) {
	o := hitscanOpts{filter: collision.DefaultRayFilter(hitscanDefaultRange)}
	if len(args) < 2 {
		return o, nil
	}
	d, ok := args[1].(*flang.DictVal)
	if !ok {
		if _, isNull := args[1].(flang.NullVal); isNull {
			return o, nil
		}
		return o, fmt.Errorf("hitscan's options are a dict, not a %s", flang.TypeOf(args[1]))
	}
	// Sorted, so a dict with two bad keys reports the same one every run.
	keys := make([]string, 0, len(d.M))
	for k := range d.M {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		opt, known := hitscanOptions[k]
		if !known {
			// An unknown option is a typo rather than a thing to ignore, the
			// same reading an unknown property gets: the set is fixed and
			// enumerable, so nothing outside it was meant.
			return o, fmt.Errorf("hitscan has no option called %q%s", k, hitscanNearestOption(k))
		}
		if err := opt.apply(&o, d.M[k]); err != nil {
			return o, err
		}
	}
	if o.filter.Range <= 0 {
		rt.warnAt(pos, "hitscan's range is %g, so the ray travels no distance and hits nothing", o.filter.Range)
	}
	return o, nil
}

func hitscanNearestOption(k string) string {
	if best := world.NearestName(k, HitscanOptionNames()); best != "" {
		return fmt.Sprintf("; did you mean %q?", best)
	}
	return ""
}

func hitscanBool(name string, v flang.Value) (bool, error) {
	b, ok := v.(flang.BoolVal)
	if !ok {
		return false, fmt.Errorf("hitscan's %q is true or false, not a %s", name, flang.TypeOf(v))
	}
	return bool(b), nil
}

// hitscanIgnore reads the one option that takes refs: a single ref or a list of
// them. A null is accepted and means nothing is ignored, so
// `{"ignore": @maybe_gone}` does not have to be guarded at the call site.
func hitscanIgnore(o *hitscanOpts, v flang.Value) error {
	add := func(x flang.Value) error {
		switch t := x.(type) {
		case flang.NullVal:
			return nil
		case flang.RefVal:
			if t.A == "" {
				return nil
			}
			if o.filter.Ignore == nil {
				o.filter.Ignore = map[string]bool{}
			}
			// Refs are paths of names; for everything a ray can meet, the last
			// name is the one the level indexes by.
			o.filter.Ignore[hitscanIgnoreKey(t)] = true
			return nil
		default:
			return fmt.Errorf("hitscan's \"ignore\" takes a ref or a list of refs, not a %s", flang.TypeOf(x))
		}
	}
	if l, ok := v.(*flang.ListVal); ok {
		for _, e := range l.Elems {
			if err := add(e); err != nil {
				return err
			}
		}
		return nil
	}
	return add(v)
}

// hitscanIgnoreKey is the name a ray-castable object is known by, which for
// every kind a cast can meet — entity, volume, scenery, brush — is the ref's
// leading name. Ignoring a brush ignores all of its faces, since a face has
// no address of its own.
func hitscanIgnoreKey(r flang.RefVal) string {
	return r.A
}

// numericValue reads an int or a float, which is what every numeric option
// accepts — `1` and `1.0` are different types and both are a range.
func numericValue(v flang.Value) (float64, bool) {
	switch t := v.(type) {
	case flang.IntVal:
		return float64(t), true
	case flang.FloatVal:
		return float64(t), true
	}
	return 0, false
}
