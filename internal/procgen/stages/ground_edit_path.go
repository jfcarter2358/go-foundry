package stages

// ground_edit_path.go — ground_edit's paths (docs/terrain-paint/
// PHASE_P4.md): the specs from the `paths` list, the laying
// (terrain.LayPaths: the flatten and the masks), the masks and the
// terrain.yaml section they leave, and paths.yaml. The stones are
// ground_edit_path_stones.go; the parameters and why paths live in this
// stage ground_edit_path_params.go.
//
// ORDER IN THE RUN. After the rivers (step 3c): a path must know where the
// water is — it fords a river and never flattens into its channel — and
// before the quantisation, so its flatten lands on the heightmap's 16 bits
// and the moved tiles are recooked by the one tile pass like any other
// edit. The stones are placed after the quantisation (step 9), on the
// ground the tiles are cooked from.
//
// WHAT IT WRITES (beside what ground_edit always writes), only when at
// least one path is laid, so a ground_edit without paths is unchanged:
//
//   - the path masks (terrain.PathMaskNames): each style's surface
//     (path_rock_path, path_stepping_stones, path_dirt_trail,
//     path_gravel_path, path_game_trail) for material entries to paint the
//     style's texture by, path_wear for grass to thin and shorten by and for
//     a dirt tint, path_clear for scatters to keep large rocks and logs off,
//     and path_dir_x / path_dir_z, the direction `orient_to` reads;
//   - terrain.yaml `footpaths:` — every path, its style, length, crossings
//     and stones;
//   - paths.yaml — the same for the editor's paths panel (always, once the
//     stage has a paths list, so the panel never shows a removed path).

import (
	"fmt"
	"os"

	"foundry/internal/procgen"
	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/terrain"

	"gopkg.in/yaml.v2"
)

// PathsFile is ground_edit's table of paths.
const PathsFile = "paths.yaml"

// groundPaths is what the paths laid.
type groundPaths struct {
	specs []terrain.PathSpec
	// entries is each spec's `paths` item.
	entries []int
	res     *terrain.PathResult
	// stones counts each path's placed stones (by kind) once placed.
	stones map[string]map[string]int
	// obstacles is the stones placed (ground_obstacles.go).
	obstacles [][3]float64
}

func (p *groundPaths) any() bool { return p != nil && p.res != nil && len(p.res.Paths) > 0 }

// layGroundPaths builds the specs and lays them on g (file comment).
func layGroundPaths(rc *procgen.RunContext, g *terrain.Grid, ws *groundWater, rv *groundRivers) *groundPaths {
	out := &groundPaths{stones: map[string]map[string]int{}}
	seen := map[string]bool{}
	for i, it := range rc.Params.Items("paths") {
		pts := it.Points("path")
		if len(pts) < 2 {
			continue
		}
		name := it.Str("name")
		if name == "" {
			name = fmt.Sprintf("path_%d", i+1)
		}
		name = safeName(name, "path")
		for base, n := name, 2; seen[name]; n++ {
			name = fmt.Sprintf("%s_%d", base, n)
		}
		seen[name] = true
		sp := terrain.PathSpec{Name: name, Style: it.Str("style"), Shape: pathShape(it), Seed: noise.Hash64(rc.Seed, 0x70617468, uint64(i))}
		for _, q := range pts {
			sp.Points = append(sp.Points, [2]float64{q[0], q[1]})
			w := 1.5
			if len(q) > 2 {
				w = q[2]
			}
			sp.Width = append(sp.Width, w)
		}
		out.specs = append(out.specs, sp)
		out.entries = append(out.entries, i)
	}
	if len(out.specs) == 0 {
		return out
	}
	var water *terrain.WaterResult
	if ws.wet() {
		water = ws.water
	}
	var rivers *terrain.RiverResult
	if rv.any() {
		rivers = rv.res
	}
	out.res = terrain.LayPaths(g, out.specs, water, rivers)
	cross := 0
	for _, p := range out.res.Paths {
		cross += p.Crossings
	}
	rc.Logf("paths: %d laid (%d samples flattened), %d water crossings", len(out.res.Paths), out.res.Changed, cross)
	return out
}

// masks is the path masks quantised as their files hold them.
func (p *groundPaths) masks() map[string][]float32 {
	out := map[string][]float32{}
	for _, name := range terrain.PathMaskNames {
		m := append([]float32(nil), p.res.Masks[name]...)
		terrain.QuantizeUnit(m)
		out[name] = m
	}
	return out
}

// meta is terrain.yaml's footpaths section.
func (p *groundPaths) meta() *terrain.FootpathsMeta {
	if !p.any() {
		return nil
	}
	m := &terrain.FootpathsMeta{}
	for _, q := range p.res.Paths {
		n := 0
		for _, c := range p.stones[q.Name] {
			n += c
		}
		m.Paths = append(m.Paths, terrain.FootpathMeta{Name: q.Name, Style: q.Style, Length: round2(q.Length),
			Crossings: q.Crossings, Stones: n})
	}
	return m
}

// GroundPath is one laid path as paths.yaml lists it.
type GroundPath struct {
	Name  string `yaml:"name"`
	Style string `yaml:"style"`
	// Entry is the `paths` item it came from.
	Entry     int     `yaml:"entry"`
	Length    float64 `yaml:"length"`
	Crossings int     `yaml:"crossings,omitempty"`
	// Stones counts its stones by kind (flag, step, ford).
	Stones map[string]int `yaml:"stones,omitempty"`
}

// GroundPaths is paths.yaml.
type GroundPaths struct {
	Paths []GroundPath `yaml:"paths"`
	// Note says why stones were not laid, when they were not.
	Note string `yaml:"note,omitempty"`
}

// writePathsFile writes paths.yaml (file comment).
func writePathsFile(rc *procgen.RunContext, p *groundPaths, note string) error {
	f := GroundPaths{Note: note}
	if p.any() {
		for _, q := range p.res.Paths {
			f.Paths = append(f.Paths, GroundPath{Name: q.Name, Style: q.Style, Entry: p.entries[q.Spec], Length: round2(q.Length),
				Crossings: q.Crossings, Stones: p.stones[q.Name]})
		}
	}
	data, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	return rc.WriteFile(PathsFile, data)
}

// ReadGroundPaths reads a ground_edit stage's paths.yaml.
func ReadGroundPaths(path string) (*GroundPaths, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f GroundPaths
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", PathsFile, err)
	}
	return &f, nil
}
