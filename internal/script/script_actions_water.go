package script

// script_actions_water.go — the water verb (docs/terrain-paint/PHASE_W5.md).
//
// `water_at(place)` asks world.WaterAt about a vec or an object's position:
// whether it is under a water surface, how deep, the surface's height, the
// current there and the material the water wears. It answers null where no
// water surface lies over or under the point at all, so the common question
// — "is the player in water?" — reads `w = water_at(player())` then
// `if w != null and w["under"]`. Generated ponds, lakes and rivers answer (their
// meshes are the query's source), and so does a brush face wearing water.
//
// Read-only: asking costs one grid cell's point-in-triangle tests, so a
// script may ask every tick for every entity it cares about.

import (
	"fmt"

	"foundry/internal/flang"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// waterVerbs is merged into scriptVerbs at init.
var waterVerbs = map[string]scriptVerb{
	"water_at": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("water_at takes a vec or an object")
		}
		p, ok := rt.positionOf(args[0])
		if !ok {
			return nil, fmt.Errorf("water_at takes a vec or an object with a position, not a %s", flang.TypeOf(args[0]))
		}
		w := world.WaterAt(rt.lvl, p)
		if !w.Found {
			return flang.NullVal{}, nil
		}
		return waterSampleValue(w), nil
	},
}

// waterSampleValue is water_at's dict.
func waterSampleValue(w world.WaterSample) flang.Value {
	d := flang.NewDict()
	d.M["under"] = flang.BoolVal(w.In)
	d.M["depth"] = flang.FloatVal(float64(w.Depth))
	d.M["surface"] = flang.FloatVal(float64(w.Surface))
	d.M["flow"] = vecValue(mgl32.Vec3{w.Flow.X(), 0, w.Flow.Y()})
	d.M["material"] = flang.StrVal(w.Material)
	return d
}

// splashShot puts a splash where a shot from origin to stop crossed a
// water surface on its way down (hitscan; phase W5).
func splashShot(rt *ScriptRuntime, origin, stop mgl32.Vec3) {
	if rt.lvl == nil {
		return
	}
	x, ok := world.WaterCrossing(rt.lvl, origin, stop)
	if !ok {
		return
	}
	w := world.WaterAt(rt.lvl, x.Sub(mgl32.Vec3{0, 0.01, 0}))
	if world.SplashWater(rt.lvl, x, world.SplashImpact, 0.6, world.WaterSplashTint(w)) {
		rt.markDirty()
		rt.cacheAdded(flang.RefEmitter)
	}
}
