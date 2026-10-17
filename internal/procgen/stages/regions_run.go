package stages

// regions_run.go — the regions stage's run, load and preview (regions.go).
//
// regions.yaml is the resolved set: what the run engine's expansion and the
// phase 2+ stages worked from, written so a run can be read back without the
// level (and so the output folder records which footprints it was made for):
//
//	warp: {amount: 40, scale: 300, seed: 1234567}
//	bounds: [-700, -400, 700, 400]          # every region plus its band
//	settings: {resolution: 0.5, tile_size: 128, margin: 64, erosion: global}
//	regions:
//	  - id: canyon_west
//	    key: canyon_west
//	    environment: desert_canyon
//	    priority: 0
//	    transition: 200
//	    anchor: [-300, 0]                    # resolved: the centroid unless anchored
//	    anchored: false
//	    bounds: [-600, -300, 0, 300]
//	    footprint: [[-600, -300], [0, -300], [0, 300], [-600, 300]]
//	    pair_transitions: {savannah: 200}    # the adjacency table's widths
//	                                         # (a default-transition region only)

import (
	"fmt"
	"math"

	"foundry/internal/procgen"
	"foundry/internal/procgen/regions"

	"gopkg.in/yaml.v2"
)

// RegionsFileName is the run's output.
const RegionsFileName = "regions.yaml"

type regionsFile struct {
	Warp     regionsWarpFile     `yaml:"warp"`
	Bounds   [4]float64          `yaml:"bounds"`
	Settings regionsSettingsFile `yaml:"settings"`
	Regions  []regionFile        `yaml:"regions"`
}

type regionsWarpFile struct {
	Amount float64 `yaml:"amount"`
	Scale  float64 `yaml:"scale"`
	Seed   uint64  `yaml:"seed"`
}

type regionsSettingsFile struct {
	Resolution float64 `yaml:"resolution"`
	TileSize   float64 `yaml:"tile_size"`
	Margin     float64 `yaml:"margin"`
	Erosion    string  `yaml:"erosion"`
}

type regionFile struct {
	ID          string       `yaml:"id"`
	Key         string       `yaml:"key"`
	Environment string       `yaml:"environment"`
	Generator   string       `yaml:"generator,omitempty"`
	Priority    int          `yaml:"priority"`
	Transition  float64      `yaml:"transition"`
	Anchor      [2]float64   `yaml:"anchor"`
	Anchored    bool         `yaml:"anchored"`
	Bounds      [4]float64   `yaml:"bounds"`
	Footprint   [][2]float64 `yaml:"footprint,flow"`
	// PairTransitions are the adjacency table's widths this region blends
	// over against neighbouring environments (only for a region left at the
	// default transition; regions/pairband.go).
	PairTransitions map[string]float64 `yaml:"pair_transitions,omitempty,flow"`
}

func rectArray(r regions.Rect) [4]float64 { return [4]float64{r.MinX, r.MinZ, r.MaxX, r.MaxZ} }

func runRegions(rc *procgen.RunContext) error {
	set, err := regionSet(rc.Params, rc.Seed)
	if err != nil {
		return err
	}
	f := regionsFile{
		Warp:   regionsWarpFile{Amount: set.Warp.Amount, Scale: set.Warp.Scale, Seed: set.Warp.Seed},
		Bounds: rectArray(set.Bounds()),
		Settings: regionsSettingsFile{Resolution: rc.Params.Float("resolution"), TileSize: rc.Params.Float("tile_size"),
			Margin: rc.Params.Float("margin"), Erosion: rc.Params.Str("erosion")},
	}
	for i := range set.Regions {
		r := &set.Regions[i]
		f.Regions = append(f.Regions, regionFile{ID: r.ID, Key: r.Key, Environment: r.Environment, Generator: r.Generator,
			Priority: r.Priority, Transition: r.Band(), Anchor: r.AnchorPoint(), Anchored: r.HasAnchor,
			Bounds: rectArray(set.RegionBounds(i)), Footprint: r.Footprint, PairTransitions: r.PairTransitions})
	}
	data, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	if err := rc.WriteFile(RegionsFileName, data); err != nil {
		return err
	}
	rc.Logf("%d regions over %.0f x %.0f m", set.Len(), f.Bounds[2]-f.Bounds[0], f.Bounds[3]-f.Bounds[1])
	rc.Artifact = set
	return nil
}

// loadRegions reads regions.yaml back into a *regions.Set.
func loadRegions(res *procgen.StageResult) (any, error) {
	data, err := readFile(res.File(RegionsFileName))
	if err != nil {
		return nil, err
	}
	return ParseRegionsFile(data)
}

// ParseRegionsFile is a regions.yaml as a Set.
func ParseRegionsFile(data []byte) (*regions.Set, error) {
	var f regionsFile
	if err := yaml.UnmarshalStrict(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", RegionsFileName, err)
	}
	rs := make([]regions.Region, len(f.Regions))
	for i, r := range f.Regions {
		rs[i] = regions.Region{ID: r.ID, Key: r.Key, Environment: r.Environment, Generator: r.Generator,
			Priority: r.Priority, Transition: r.Transition, Anchor: r.Anchor, HasAnchor: r.Anchored, Footprint: r.Footprint,
			PairTransitions: r.PairTransitions}
	}
	return regions.NewSet(rs, regions.Warp{Amount: f.Warp.Amount, Scale: f.Warp.Scale, Seed: f.Warp.Seed})
}

// ---------- preview ----------

// RegionsOverlayName is the preview's biome overlay image layer.
const RegionsOverlayName = "biomes"

func previewRegions(pc *procgen.PreviewContext) (*procgen.PreviewData, error) {
	set, err := regionSet(pc.Params, pc.Seed)
	if err != nil {
		return nil, err
	}
	out := &procgen.PreviewData{Artifact: set}
	if set.Len() == 0 {
		out.Notes = append(out.Notes, "No regions. Add region volumes to the level (a volume's Region block); "+
			"they are synced in before every run and preview.")
		return out, nil
	}
	b := set.Bounds()
	g := regions.GridOver(b, 1, pc.Params.Int("preview_size"))
	ras, err := set.Raster(pc.Ctx, g, pc.Workers)
	if err != nil {
		return nil, procgen.ErrCancelled
	}
	rect := procgen.Rect{MinX: g.MinX, MinZ: g.MinZ, MaxX: g.MinX + float64(g.W)*g.Cell, MaxZ: g.MinZ + float64(g.H)*g.Cell}
	out.Images = append(out.Images, BiomeOverlay(set, ras, rect))
	for k := range set.Regions {
		r := &set.Regions[k]
		out.Images = append(out.Images, procgen.GrayImage("weight_"+r.Key, g.W, g.H, ras.Planes[k], 0, 1, rect))
	}
	for k := range set.Regions {
		r := &set.Regions[k]
		c := environmentColor(r.Environment)
		line := procgen.LineLayer{Name: r.Key, Closed: true, Color: [4]float32{float32(c[0]), float32(c[1]), float32(c[2]), 1}}
		for _, p := range r.Footprint {
			line.Points = append(line.Points, [2]float64{p[0], p[1]})
		}
		out.Lines = append(out.Lines, line)
		note := fmt.Sprintf("%s: %s, priority %d, %.0f m band", r.ID, r.Environment, r.Priority, r.Band())
		if mb := set.MaxBand(k); mb != r.Band() {
			note += fmt.Sprintf(" (up to %.0f m against a neighbour, from the adjacency table)", mb)
		}
		out.Notes = append(out.Notes, note)
	}
	out.Bounds = rect
	out.Notes = append(out.Notes, fmt.Sprintf("%d regions over %.0f x %.0f m; overlay %dx%d at %.2g m", set.Len(),
		b.MaxX-b.MinX, b.MaxZ-b.MinZ, g.W, g.H, g.Cell))
	return out, nil
}

// BiomeOverlay is the overlay image: every pixel the regions' environment
// colours mixed by their weights, with the dominant share as the readout.
func BiomeOverlay(set *regions.Set, ras *regions.Raster, rect procgen.Rect) procgen.ImageLayer {
	g := ras.Grid
	n := g.W * g.H
	im := procgen.ImageLayer{Name: RegionsOverlayName, W: g.W, H: g.H, RGBA: make([]uint8, n*4),
		Values: make([]float32, n), Unit: "share", Bounds: rect}
	cols := make([][3]float64, set.Len())
	for k := range set.Regions {
		cols[k] = environmentColor(set.Regions[k].Environment)
	}
	for px := 0; px < n; px++ {
		var c [3]float64
		top := float32(0)
		for k := range cols {
			w := float64(ras.Planes[k][px])
			if w <= 0 {
				continue
			}
			c[0] += w * cols[k][0]
			c[1] += w * cols[k][1]
			c[2] += w * cols[k][2]
			top = max(top, float32(w))
		}
		for ch := 0; ch < 3; ch++ {
			im.RGBA[px*4+ch] = uint8(math.Round(math.Max(0, math.Min(1, c[ch])) * 255))
		}
		im.RGBA[px*4+3] = 255
		im.Values[px] = top
	}
	return im
}
