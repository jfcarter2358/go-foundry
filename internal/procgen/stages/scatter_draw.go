package stages

// scatter_draw.go — how far a scatter rule's placements are drawn, and the
// far impostors of its trees (docs/regions/PRESETS_5.md; the engine side is
// world/scenery_draw.go and render/nanite_draw_distance.go).
//
// In a level generation doc's regions (Env.Region: the environment presets'
// scatters), every placement a rule makes is written with a draw distance, a
// shadow distance and — for a tree — an impostor, unless the rule says
// otherwise. A classic document's rules write none unless they set them: its
// output is pinned to the byte (region_place_test.go, regions_test.go), and
// an existing level regenerated must draw as it did. The automatic values
// come from the placement's own size, its bounding sphere's radius r (half
// its box's diagonal, scaled):
//
//	draw distance      clamp(DrawPerMetre·r, DrawMin, DrawMax)
//	                   (a 1 m bush ~220 m, a 0.3 m stone 120 m, a boulder
//	                   300-400 m); a tree with an impostor at least
//	                   TreeDrawMin, out to DrawMax
//	shadow distance    ShadowShare of it; a tree with an impostor casts
//	                   only while its MESH is drawn — out to its impostor
//	                   distance — and its impostor casts none (PRESETS_6:
//	                   the cards' coverage-preserving mips are near opaque
//	                   at the coarse virtual-shadow-map pages a far caster
//	                   lands on, so each impostor cast a dark square)
//	impostor distance  clamp(ImpostorPerMetre·r, ImpostorMin, ImpostorMax)
//
// A rule's draw_distance, shadow_distance and impostor_distance override the
// automatic values (a negative draw distance is no limit, a negative shadow
// distance as far as drawn). impostor: auto bakes impostors for a Poly Haven
// plant source's TREES — a variant at least TreeHeight tall at the rule's
// largest scale — on bakes them for every variant, off for none.
//
// An impostor is baked once per cooked variant (internal/impostor), beside
// it — models/.../<stem>_imp.fnm and textures/ph_imp_<stem>_cov.png — and
// baked again only when the variant's cook, the bake's version or its
// options change (impostor.Current). A rule's variants that no placement
// uses are never baked.

import (
	"fmt"
	"math"
	"sync"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/impostor"
	"foundry/internal/nanite"
	"foundry/internal/nanitecook"
	"foundry/internal/procgen"
	"foundry/internal/procgen/place"
)

// The automatic distances (see the file comment), in metres.
const (
	DrawPerMetre     = 220
	DrawMin          = 120
	DrawMax          = 1500
	TreeDrawMin      = 800
	ShadowShare      = 0.5
	TreeShadowShare  = 0.6
	ImpostorPerMetre = 25
	ImpostorMin      = 80
	ImpostorMax      = 300
	// TreeHeight is how tall a plant must stand to get an impostor under
	// impostor: auto.
	TreeHeight = 3.0
)

// drawRuleFields are a scatter rule's draw-distance parameters. Added: at
// their defaults (auto) they leave the stage hash alone, so no existing
// scatter goes stale for them — a region world picks the automatic values up
// when its scatters next run (`foundry gen run -stage r_<region>_scatter`
// forces them).
func drawRuleFields() []procgen.ParamDef {
	f := procgen.Float
	return []procgen.ParamDef{
		{Name: "draw_distance", Added: true, Type: procgen.ParamFloat, Label: "Draw distance (m)", Default: f(0), Min: -1, Max: 100000, Step: 10,
			Doc: "How far from the camera the rule's placements are drawn (they fade out over the last tenth). 0: by size — " +
				"about 220 m a metre of bounding radius, 120–1500 m (trees with impostors at least 800 m). Negative: no limit."},
		{Name: "shadow_distance", Added: true, Type: procgen.ParamFloat, Label: "Shadow distance (m)", Default: f(0), Min: -1, Max: 100000, Step: 10,
			Doc: "How far they cast a shadow. 0: half the draw distance (a tree's impostor 0.6 of it). Negative: as far as drawn."},
		{Name: "impostor", Added: true, Type: procgen.ParamEnum, Default: procgen.Enum("auto"), Choices: []string{"auto", "on", "off"},
			Doc: "Far impostors: a few textured cards baked from each cooked variant, drawn instead of the mesh beyond the " +
				"impostor distance. auto: the trees of a Poly Haven plant source (3 m tall or more at the rule's largest scale)."},
		{Name: "impostor_distance", Added: true, Type: procgen.ParamFloat, Label: "Impostor distance (m)", Default: f(0), Min: 0, Max: 100000, Step: 10,
			Doc: "Where a placement's mesh hands over to its impostor (cross-faded over the last tenth before it). 0: by size, 80–300 m."},
	}
}

// drawRule is one rule's draw settings, read from its parameters.
type drawRule struct {
	draw, shadow, impDist float32
	impostor              string // auto, on or off
	plants                bool   // its source is a Poly Haven plant stage
	maxScale              float64
	// auto: the automatic values apply (a region's run); off, only what
	// the rule sets explicitly.
	auto bool
}

func drawRuleOf(it procgen.Params, a *RocksArtifact, r *place.Rule, regional bool) drawRule {
	d := drawRule{draw: float32(it.Float("draw_distance")), shadow: float32(it.Float("shadow_distance")),
		impDist: float32(it.Float("impostor_distance")), impostor: it.Str("impostor"), maxScale: max(r.Scale[0], r.Scale[1], 1e-6),
		auto: regional}
	if d.impostor == "" {
		d.impostor = "auto"
	}
	d.plants = a != nil && a.Manifest != nil && a.Manifest.Preset == polyHavenPlantPreset
	return d
}

// wantsImpostor reports whether the rule gives variant v an impostor.
func (d drawRule) wantsImpostor(v *place.Variant) bool {
	switch d.impostor {
	case "on":
		return nanite.IsCookedPath(v.Model)
	case "off":
		return false
	}
	return d.auto && d.plants && nanite.IsCookedPath(v.Model) && variantHeight(v)*d.maxScale >= TreeHeight
}

func variantHeight(v *place.Variant) float64 {
	if v.Max[1] > v.Min[1] {
		return v.Max[1] - v.Min[1]
	}
	return max(v.Extent[1], v.Height)
}

// variantRadius is v's bounding sphere's radius at scale 1: half its box's
// diagonal.
func variantRadius(v *place.Variant) float64 {
	e := v.Extent
	if v.Max != v.Min {
		e = [3]float64{v.Max[0] - v.Min[0], v.Max[1] - v.Min[1], v.Max[2] - v.Min[2]}
	}
	return 0.5 * math.Sqrt(e[0]*e[0]+e[1]*e[1]+e[2]*e[2])
}

// applyDraw writes the rule's distances onto a placement of variant v at
// scale, with its impostor (imp, "" for none).
func (d drawRule) applyDraw(pl *procgen.Placement, v *place.Variant, scale float64, imp string) {
	r := float32(variantRadius(v) * scale)
	clamp := func(x, lo, hi float32) float32 { return min(max(x, lo), hi) }
	draw := d.draw
	if draw == 0 && !d.auto {
		// A classic document's rule that set nothing: nothing written, as
		// before (an explicit impostor still takes its automatic distances).
		if imp == "" {
			return
		}
		draw = -1
	}
	if draw == 0 {
		draw = clamp(DrawPerMetre*r, DrawMin, DrawMax)
		if imp != "" {
			draw = clamp(DrawPerMetre*r, TreeDrawMin, DrawMax)
		}
	}
	if draw < 0 {
		draw = 0
	}
	at := d.impDist
	if imp != "" && at <= 0 {
		at = clamp(ImpostorPerMetre*r, ImpostorMin, ImpostorMax)
	}
	// far: the impostor takes over before the draw distance (else the mesh
	// is drawn all the way and there is no far tier).
	far := imp != "" && !(draw > 0 && at >= draw)
	shadow := d.shadow
	if shadow == 0 && draw > 0 {
		share := float32(ShadowShare)
		if imp != "" {
			share = TreeShadowShare
		}
		shadow = draw * share
		if far && d.auto {
			// Only the mesh casts (file comment).
			shadow = min(shadow, at)
		}
	}
	if shadow < 0 {
		shadow = 0
	}
	pl.DrawDistance, pl.ShadowDistance = round1(draw), round1(shadow)
	if !far {
		return
	}
	pl.Impostor, pl.ImpostorDistance = imp, round1(at)
}

func round1(x float32) float32 { return float32(math.Round(float64(x)*10) / 10) }

// impostorLocks serialises the bakes of one variant across stages (two
// regions' scatters can share a plant source).
var impostorLocks sync.Map

// ensureImpostor bakes (if it is not current) the impostor of the cooked
// variant model (resources-relative) and returns its cards' path.
func ensureImpostor(logf func(string, ...any), model string) (string, error) {
	paths := impostor.PathsFor(model)
	mu, _ := impostorLocks.LoadOrStore(paths.Model, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	m, err := nanite.Load(fileutil.ResolveAssetPath(model))
	if err != nil {
		return "", err
	}
	var opt impostor.Options
	if impostor.Current(paths, m, opt) {
		return paths.Model, nil
	}
	t0 := time.Now()
	res, err := impostor.Bake(m, impostor.TexturesFrom(), opt)
	if err != nil {
		return "", fmt.Errorf("impostor of %s: %w", model, err)
	}
	res.SetTexture(paths.Texture)
	if err := impostor.WriteTextures(res, paths); err != nil {
		return "", err
	}
	out := fileutil.ResolveAssetPath(paths.Model)
	cooked, err := nanitecook.CookMesh(res.Mesh, out, nanitecook.Options{})
	if err != nil {
		return "", fmt.Errorf("impostor of %s: cook: %w", model, err)
	}
	// Each card drawn only from the views near its own (impostor/cones.go).
	impostor.SetViewCones(cooked, res)
	if err := cooked.WriteNoBackup(out); err != nil {
		return "", fmt.Errorf("impostor of %s: %w", model, err)
	}
	if err := impostor.WriteProvenance(paths, m, opt); err != nil {
		return "", err
	}
	logf("impostor %s: %d cards baked in %s", paths.Model, res.Cards, time.Since(t0).Round(time.Millisecond))
	return paths.Model, nil
}
