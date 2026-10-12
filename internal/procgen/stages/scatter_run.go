package stages

// scatter_run.go — the scatter stage's run, Load and preview.
//
// RUN: the terrain's ground (heights and the four masks the bands read),
// each rule's rocks stage's variants from its manifest, the avoid stage's
// footprints, place.Scatter, and every instance placed as scenery —
// `<rule>_<nnnnn>`, the variant's cooked mesh, Euler rotation, a uniform
// scale, collision by size, `chunk: auto`. It writes footprints.yaml (every
// rock's footprint disc, and each rule's counts) so a later stage can keep
// clear of the rocks and a later run can Load them without re-scattering.
//
// PREVIEW: the same scatter over the terrain's preview, each rule's rocks
// stood in for by its rocks stage's previewed variant (place_common.go):
// the terrain's image layers under a dot layer per rule (its colour, each
// dot its footprint), and in 3D the terrain's tiles with each rule's rocks
// as one instanced proxy.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"foundry/internal/procgen"
	"foundry/internal/procgen/place"

	"gopkg.in/yaml.v2"
)

// FootprintsFile is a placement stage's footprints file.
const FootprintsFile = "footprints.yaml"

// FootprintsArtifact is a scatter's (or cliff wall's) product downstream:
// the footprints it put on the ground.
type FootprintsArtifact struct {
	Discs []place.Disc
}

// footprintsFile is footprints.yaml.
type footprintsFile struct {
	Version int `yaml:"version"`
	// Discs are [x, z, r], metres.
	Discs [][3]float64     `yaml:"discs,flow"`
	Rules []footprintsRule `yaml:"rules,omitempty"`
}

type footprintsRule struct {
	Name       string  `yaml:"name"`
	Kept       int     `yaml:"kept"`
	Candidates int     `yaml:"candidates"`
	Area       float64 `yaml:"area_m2"`
}

func writeFootprints(rc *procgen.RunContext, discs []place.Disc, rules []footprintsRule) error {
	f := footprintsFile{Version: 1, Rules: rules}
	for _, d := range discs {
		f.Discs = append(f.Discs, [3]float64{round2(d.X), round2(d.Z), round2(d.R)})
	}
	data, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	return rc.WriteFile(FootprintsFile, data)
}

func loadFootprints(res *procgen.StageResult) (any, error) {
	data, err := os.ReadFile(filepath.Join(res.Dir, FootprintsFile))
	if err != nil {
		return nil, err
	}
	var f footprintsFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", FootprintsFile, err)
	}
	a := &FootprintsArtifact{}
	for _, d := range f.Discs {
		a.Discs = append(a.Discs, place.Disc{X: d[0], Z: d[1], R: d[2]})
	}
	return a, nil
}

func loadScatter(res *procgen.StageResult) (any, error) { return loadFootprints(res) }

// avoidDiscs is the avoid stage's footprints in a run.
func avoidDiscs(rc *procgen.RunContext, id string) ([]place.Disc, error) {
	if id == "" {
		return nil, nil
	}
	a, err := rc.UpstreamArtifact(id)
	if err != nil {
		return nil, err
	}
	if fa, ok := a.(*FootprintsArtifact); ok {
		return fa.Discs, nil
	}
	return nil, fmt.Errorf("stage %s has no footprints", id)
}

func runScatter(rc *procgen.RunContext) error {
	p := rc.Params
	t0 := time.Now()
	g, err := runGround(rc, p.Str("terrain"))
	if err != nil {
		return err
	}
	cache := map[string][]place.Variant{}
	arts := map[string]*RocksArtifact{}
	rules, idx, err := scatterRules(p, func(ref string) ([]place.Variant, error) {
		if v, ok := cache[ref]; ok {
			return v, nil
		}
		v, a, err := runVariants(rc, ref)
		cache[ref], arts[ref] = v, a
		return v, err
	})
	if err != nil {
		return err
	}
	ground, err := groundArtifact(rc, p.Str("ground"))
	if err != nil {
		return err
	}
	// Each rule's material: its source's base, plus the rule's ground blend,
	// match and varnish (plants take only the base).
	mats := make([]string, len(rules))
	items := p.Items("rules")
	for k := range rules {
		it := items[idx[k]]
		a := arts[it.Str("rocks")]
		base := ""
		if a != nil && a.Manifest != nil {
			base = a.Manifest.Material
		}
		fx := rockFxOf(rc, it, ground, a)
		if a != nil && a.Manifest != nil && a.Manifest.Preset == polyHavenPlantPreset {
			fx = rockFx{}
		}
		if mats[k], err = rockMaterial(rc, rules[k].Name, base, fx); err != nil {
			return err
		}
		if mats[k] != "" {
			rc.Logf("%s: material %s", rules[k].Name, mats[k])
		}
	}
	avoid, err := avoidDiscs(rc, p.Str("avoid"))
	if err != nil {
		return err
	}
	res, err := place.Scatter(g, place.ScatterSettings{Rules: rules, Seed: rc.Seed, CliffThreshold: p.Float("cliff_threshold"),
		Margin: p.Float("margin"), Avoid: avoid, Progress: func(f float32) { rc.Progress(0.9 * f) }}, &rc.Env)
	if err != nil {
		return err
	}
	scatterLog(&rc.Env, res, rules)
	counts := make([]int, len(rules))
	var discs []place.Disc
	pls := make([]procgen.Placement, 0, len(res.Instances))
	for k := range res.Instances {
		in := &res.Instances[k]
		r := &rules[in.Rule]
		counts[in.Rule]++
		pl := placementOf(fmt.Sprintf("%s_%05d", r.Name, counts[in.Rule]), in, &r.Variants[in.Variant])
		pl.Material = mats[in.Rule]
		pls = append(pls, pl)
		discs = append(discs, in.Disc())
	}
	rc.Place(pls...)
	var fr []footprintsRule
	for _, st := range res.Stats {
		fr = append(fr, footprintsRule{Name: st.Name, Kept: st.Kept, Candidates: st.Candidates, Area: round2(st.Area)})
	}
	if err := writeFootprints(rc, discs, fr); err != nil {
		return err
	}
	rc.Artifact = &FootprintsArtifact{Discs: discs}
	rc.Logf("%d rocks placed in %s", len(res.Instances), time.Since(t0).Round(time.Millisecond))
	rc.Progress(1)
	return nil
}

// scatterLog writes each rule's counts.
func scatterLog(env *procgen.Env, res *place.ScatterResult, rules []place.Rule) {
	for i, st := range res.Stats {
		tris := 0
		for _, v := range rules[i].Variants {
			tris += v.Triangles
		}
		tris /= max(len(rules[i].Variants), 1)
		env.Logf("%s: %d kept of %d candidates (%d satellites; %d too close, %d on rough ground, %d off the edge, %d over the cap), "+
			"weight over %.0f m², ~%d triangles a rock", st.Name, st.Kept, st.Candidates, st.Satellites, st.TooClose, st.TooRough,
			st.OffGround, st.OverTheCap, st.Area, tris)
	}
}

func previewScatter(pc *procgen.PreviewContext) (*procgen.PreviewData, error) {
	p := pc.Params
	t0 := time.Now()
	g, up, err := previewGround(pc, p.Str("terrain"))
	if err != nil {
		return nil, err
	}
	type stand struct {
		v    place.Variant
		mesh procgen.PreviewMesh
	}
	stands := map[string]*stand{}
	rules, idx, err := scatterRules(p, func(ref string) ([]place.Variant, error) {
		if s, ok := stands[ref]; ok {
			return []place.Variant{s.v}, nil
		}
		v, m := previewRock(pc, ref)
		s := &stand{v: v, mesh: proxyMesh(ref, m, p.Int("preview_triangles"))}
		stands[ref] = s
		return []place.Variant{v}, nil
	})
	if err != nil {
		return nil, err
	}
	for i := range rules {
		if rules[i].MaxInstances <= 0 || rules[i].MaxInstances > p.Int("preview_max") {
			rules[i].MaxInstances = p.Int("preview_max")
		}
	}
	res, err := place.Scatter(g, place.ScatterSettings{Rules: rules, Seed: pc.Seed, CliffThreshold: p.Float("cliff_threshold"),
		Margin: p.Float("margin"), Progress: pc.Progress}, &pc.Env)
	if err != nil {
		return nil, err
	}
	out := &procgen.PreviewData{Bounds: up.Bounds, Images: up.Images, Artifact: &FootprintsArtifact{}}
	out.Meshes = append(out.Meshes, terrainMeshes(up)...)
	items := p.Items("rules")
	layers := make([]procgen.PointLayer, len(rules))
	meshes := make([]procgen.PreviewMesh, len(rules))
	for k, r := range rules {
		layers[k] = procgen.PointLayer{Name: r.Name, Color: ruleColor(items[idx[k]], idx[k])}
		meshes[k] = stands[items[idx[k]].Str("rocks")].mesh
		meshes[k].Name = r.Name
		meshes[k].Instances = nil
	}
	fa := out.Artifact.(*FootprintsArtifact)
	for k := range res.Instances {
		in := &res.Instances[k]
		l := &layers[in.Rule]
		l.Points = append(l.Points, [2]float64{in.Pos[0], in.Pos[2]})
		l.Radii = append(l.Radii, in.Radius)
		if len(meshes[in.Rule].Indices) > 0 {
			meshes[in.Rule].Instances = append(meshes[in.Rule].Instances, instanceMatrix(in))
		}
		fa.Discs = append(fa.Discs, in.Disc())
	}
	out.Points = append(out.Points, layers...)
	for _, m := range meshes {
		if len(m.Instances) > 0 {
			out.Meshes = append(out.Meshes, m)
		}
	}
	for _, st := range res.Stats {
		out.Notes = append(out.Notes, fmt.Sprintf("%s: %d rocks (%d candidates, %d too close, %d rough), weight over %.0f m²",
			st.Name, st.Kept, st.Candidates, st.TooClose, st.TooRough, st.Area))
	}
	out.Notes = append(out.Notes, fmt.Sprintf("preview on the terrain's %.2f m preview grid, each rule's rocks one previewed variant; %s",
		g.Grid.Cell, time.Since(t0).Round(time.Millisecond)))
	return out, nil
}

// terrainMeshes is a terrain preview's tile meshes (shared, not copied: a
// preview is never modified once made).
func terrainMeshes(up *procgen.PreviewData) []procgen.PreviewMesh {
	if up == nil {
		return nil
	}
	var out []procgen.PreviewMesh
	for _, m := range up.Meshes {
		if !m.Turntable && len(m.Instances) == 0 {
			out = append(out, m)
		}
	}
	return out
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
