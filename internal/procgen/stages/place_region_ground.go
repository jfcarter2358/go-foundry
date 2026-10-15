package stages

// place_region_ground.go — a ground material as one environment region's
// placement stages read it (docs/regions/PHASE_4.md).
//
// The placement stages take a `ground` stage for three things: the colour
// their rocks are matched toward (GroundMean, match_ground), the maps their
// feet blend into (ground_blend reads the ground and height maps by world
// position, so the union's maps serve every region as they are), and — for
// wall_sheet — the splat whose rock layer the walls wear.
//
// Over a level generation doc that ground is the shared region_material:
// one splat for the whole union, of up to eight layers. Two of the three
// need the region's own answer:
//
//   - GroundMean: the union's mean colour mixes every biome (a savannah's
//     boulders matched toward red desert sand); regionGround replaces it
//     with the mean of the ground map weighed by the region's weight;
//   - the rock layer: a terrain_material's is always its fourth layer, and
//     wall_sheet built a weights map of pure channel 4. In an eight-layer
//     region material the region's rock is whichever layer is its
//     environment's role "rock" (rockLayerIndex), in either weights map.

import (
	"image"
	"os"
	"path/filepath"

	"foundry/internal/fileutil"
	"foundry/internal/procgen"
)

// regionGround is g for env's region: a copy with GroundMean the ground
// map's colour weighed by the region's weight. g itself for a classic run,
// or when the map cannot be read.
func regionGround(env *procgen.Env, g *TerrainMaterialArtifact) *TerrainMaterialArtifact {
	if env.Region == nil || g == nil || env.Region.Weight == nil || g.Ground == "" {
		return g
	}
	f, err := os.Open(filepath.Join(fileutil.ResourcesPath, filepath.FromSlash(g.Ground)+".png"))
	if err != nil {
		return g
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return g
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return g
	}
	sx, sz := (g.Max[0]-g.Min[0])/float64(w), (g.Max[1]-g.Min[1])/float64(h)
	var sum [3]float64
	total := 0.0
	// Every other texel: a mean colour, not a picture.
	for j := 0; j < h; j += 2 {
		z := g.Min[1] + (float64(j)+0.5)*sz
		for i := 0; i < w; i += 2 {
			x := g.Min[0] + (float64(i)+0.5)*sx
			wt := env.Region.Weight(x, z)
			if wt <= 0 {
				continue
			}
			r, gg, bb, _ := img.At(b.Min.X+i, b.Min.Y+j).RGBA()
			sum[0] += wt * float64(r) / 65535
			sum[1] += wt * float64(gg) / 65535
			sum[2] += wt * float64(bb) / 65535
			total += wt
		}
	}
	if total <= 0 {
		return g
	}
	c := *g
	for k := range c.GroundMean {
		c.GroundMean[k] = round3(sum[k] / total)
	}
	return &c
}

// rockLayerIndex is the splat layer (0..7) a wall in env's region wears: 3
// — a terrain_material's rock — unless g is an eight-layer region
// material, whose layers name their role and environments; then the rock
// layer of the region's environment (or, with no region, the first rock
// layer listed).
func rockLayerIndex(env *procgen.Env, g *TerrainMaterialArtifact) int {
	if g == nil || (g.Weights2 == "" && g.Palette == 0) || len(g.Layers) == 0 {
		return 3
	}
	want := ""
	if env.Region != nil {
		want = env.Region.Environment
	}
	first := -1
	for i, l := range g.Layers {
		if l.Role != splatRoleNames[3] {
			continue
		}
		if first < 0 {
			first = i
		}
		if want == "" || containsString(l.Environments, want) {
			return i
		}
	}
	if first >= 0 {
		return first
	}
	return 3
}
