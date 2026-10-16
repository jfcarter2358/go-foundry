package terrain

// meta.go — the terrain's files, and reading them back.
//
// A terrain stage's output folder holds (names are the constants below):
//
//	terrain.yaml        Meta: the bounds, the resolution, how each file is
//	                    encoded, the strata, the tiles and the canyon paths
//	heightmap.png       16-bit grey, (N+1)², value v → lo + v/65535·(hi-lo) m
//	mask_<name>.png     8-bit grey, (N+1)² each (MaskNames, masks.go)
//	albedo.png          8-bit RGB albedo hint
//	relief.png          8-bit RGB shaded relief (for people, not for stages)
//	tile_<i>_<j>.fnm    the cooked tiles (or .glb when not cooked)
//	placements.yaml     the run engine's
//
// Pixel (i, j) — column i, row j, row 0 first — is the SAMPLE at world
// (min_x + i·resolution, min_z + j·resolution): samples sit on the grid's
// corners, so the image is one pixel wider than size/resolution and its
// edge pixels are the map's edges.
//
// Terrain is the product a downstream stage reads: the heights in memory,
// the masks loaded on first use (a 0.5 m kilometre's masks are 4 M samples
// each), with height, normal and mask lookups at world positions. Load
// rebuilds one from a folder, so a scatter stage can run against a terrain
// stage a previous run made.

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v2"
)

// File names in a terrain output folder.
const (
	MetaFile      = "terrain.yaml"
	HeightmapFile = "heightmap.png"
	AlbedoFile    = "albedo.png"
	ReliefFile    = "relief.png"
	MetaVersion   = 1
)

// MaskFile is a mask's file name.
func MaskFile(name string) string { return "mask_" + name + ".png" }

// Meta is terrain.yaml.
type Meta struct {
	Version int `yaml:"version"`
	// Size is the square's side, Resolution the sample spacing (metres);
	// Samples is the images' side, Size/Resolution + 1.
	Size       float64    `yaml:"size"`
	Resolution float64    `yaml:"resolution"`
	Samples    int        `yaml:"samples"`
	Bounds     Bounds     `yaml:"bounds"`
	Height     Height     `yaml:"height"`
	Masks      []MaskMeta `yaml:"masks"`
	Albedo     string     `yaml:"albedo"`
	Relief     string     `yaml:"relief"`
	Strata     StrataMeta `yaml:"strata"`
	Tiles      TilesMeta  `yaml:"tiles"`
	Paths      []PathMeta `yaml:"paths,omitempty"`
	// Far is the backdrop round the square (far.go), when the stage made one.
	Far *FarMeta `yaml:"far,omitempty"`
	// Biomes is a union of environment regions' record (region_terrain):
	// which environments and regions made it, and their weight masks. Nil
	// for a classic terrain.
	Biomes *BiomesMeta `yaml:"biomes,omitempty"`
	// Vents are the volcanic vents the terrain's landforms placed (world XZ
	// and cone radius; landform.go), for the ground material's ash rule.
	// Absent without volcanoes.
	Vents []VentMeta `yaml:"vents,omitempty"`
	// Lobes are the vents' lava lobes (world XZ; landform.go), for the
	// ground material's lava rule (bare lava on the tongues, cinders round
	// them). Absent without volcanoes or lobes.
	Lobes []LobeMeta `yaml:"lobes,omitempty"`
	// Water is the standing water a region terrain filled in (seas at a
	// level, ponds in closed hollows; stages/region_terrain_water.go):
	// its mask (water depth) and every body. Absent without water.
	Water *WaterMeta `yaml:"water,omitempty"`
	// GroundEdit marks a terrain a ground_edit stage edited (edit.go): its
	// source and what moved. Absent for every terrain a terrain stage made.
	GroundEdit *GroundEditMeta `yaml:"ground_edit,omitempty"`
	// Rivers is what a ground_edit's rivers made (river_meta.go): the
	// waterfalls and confluences phase W4 dresses, and the river masks.
	// Absent for every terrain without rivers.
	Rivers *RiversMeta `yaml:"rivers,omitempty"`
	// Footpaths is what a ground_edit's paths laid (path_meta.go): each
	// path's style, length, crossings and stones, and the path masks.
	// Absent for every terrain without paths. (Paths, above, is a carved
	// canyon's centrelines.)
	Footpaths *FootpathsMeta `yaml:"footpaths,omitempty"`
	// Obstacles are the rocks a ground_edit placed itself — river boulders
	// and path stones — as discs [x, z, radius] (m): the scatter and grass
	// stages over this terrain keep out of them (phase W4,
	// stages/ground_obstacles.go). Absent when there are none.
	Obstacles [][3]float64 `yaml:"obstacles,omitempty"`
}

// VentMeta is one vent.
type VentMeta struct {
	X      float64 `yaml:"x"`
	Z      float64 `yaml:"z"`
	Radius float64 `yaml:"radius"`
}

// WaterMeta is a terrain's standing water.
type WaterMeta struct {
	// Mask is the depth mask's name (MaskWater: depth/WaterMaskDepth, 0..1).
	Mask string `yaml:"mask"`
	// Bodies are the water surfaces: each one level, its bounds and area.
	Bodies []WaterBodyMeta `yaml:"bodies"`
}

// WaterBodyMeta is one body of water.
type WaterBodyMeta struct {
	// Kind is sea (a region's water level) or pond (a filled hollow).
	Kind   string     `yaml:"kind"`
	Level  float64    `yaml:"level"`
	Bounds [4]float64 `yaml:"bounds,flow"`
	Area   float64    `yaml:"area"`
	// Region is the region whose preset asked for it.
	Region string `yaml:"region,omitempty"`
}

// MaskWater is the water depth mask a region terrain writes when it fills
// water in: depth below the surface over WaterMaskDepth metres, 0..1.
const MaskWater = "water"

// WaterMaskDepth is the depth the water mask's 1 stands for, metres.
const WaterMaskDepth = 8.0

// BiomesMeta is a region terrain's environments and regions. Each
// environment's weight — the sum of its regions' shares, 0..1 — is a mask
// like any other (`biome_<env>`, listed in Meta.Masks), so a downstream
// stage reads it with Terrain.Mask or BiomeAt.
type BiomesMeta struct {
	// Overlay is the people-only picture: every pixel the environments'
	// colours mixed by weight.
	Overlay      string            `yaml:"overlay"`
	Environments []BiomeEnvMeta    `yaml:"environments"`
	Regions      []BiomeRegionMeta `yaml:"regions"`
	// Erosion is global or tiled; Window and Margin (metres) the tiled
	// erosion's windows; Lead the region whose drainage/droplet/talus
	// numbers the global erosion ran with (and whose strata Meta.Strata
	// describes).
	Erosion string  `yaml:"erosion"`
	Window  float64 `yaml:"window,omitempty"`
	Margin  float64 `yaml:"margin,omitempty"`
	Lead    string  `yaml:"lead"`
	// Extent is what the union grid was fitted to: footprints or bands.
	Extent string `yaml:"extent"`
}

// BiomeEnvMeta is one environment of a region terrain.
type BiomeEnvMeta struct {
	Name  string     `yaml:"name"`
	Color [3]float64 `yaml:"color,flow"`
	// Mask is its weight mask's name (biome_<name>).
	Mask string `yaml:"mask"`
}

// BiomeRegionMeta is one region of a region terrain.
type BiomeRegionMeta struct {
	Key         string `yaml:"key"`
	Environment string `yaml:"environment"`
	// Preset is where its terrain came from ("built-in desert_canyon",
	// "environments/savannah.fgen"); Stage the preset's terrain stage.
	Preset string `yaml:"preset"`
	Stage  string `yaml:"stage"`
	// Seed is the provider's; Anchor its origin (world XZ).
	Seed   uint64     `yaml:"seed"`
	Anchor [2]float64 `yaml:"anchor,flow"`
	// Bounds is the footprint's [min_x, min_z, max_x, max_z].
	Bounds   [4]float64 `yaml:"bounds,flow"`
	Priority int        `yaml:"priority"`
	// Transition is the band width, metres.
	Transition float64 `yaml:"transition"`
	// Strata is the region's strata list (the strata mask indexes the
	// DOMINANT region's list at each sample).
	Strata []LayerMeta `yaml:"strata"`
}

// BiomeMaskPrefix starts an environment's weight mask's name.
const BiomeMaskPrefix = "biome_"

// BiomeMask is an environment's weight mask's name.
func BiomeMask(env string) string { return BiomeMaskPrefix + env }

// BiomesFile is a region terrain's overlay picture.
const BiomesFile = "biomes.png"

// FarMeta is the far backdrop: its heights on a coarse lattice over the
// whole far square (the map's own square included, coarsely), and the tiles
// it was cooked into.
type FarMeta struct {
	// Cell is the lattice spacing; Reach how far the backdrop runs past the
	// map's edge; Blend the easing width at the edge (metres).
	Cell  float64 `yaml:"cell"`
	Reach float64 `yaml:"reach"`
	Blend float64 `yaml:"blend"`
	// Bounds is the far square (texel centres on the lattice: sample (i, j)
	// is at min_x + i·cell, min_z + j·cell); Samples its side.
	Bounds  Bounds `yaml:"bounds"`
	Samples int    `yaml:"samples"`
	// Height is far_height.png's decoding, as Height's.
	Height Height `yaml:"height"`
	// Tiles are the cooked ring's files; Strips the stitch pieces between
	// the map's edge tiles and the ring.
	Tiles  []string `yaml:"tiles"`
	Strips []string `yaml:"strips"`
}

// FarHeightFile is the backdrop's heights.
const FarHeightFile = "far_height.png"

// FarHeights is the backdrop's lattice heights (row-major, Samples²), read
// from its file; an error when the terrain has no backdrop.
func (t *Terrain) FarHeights() ([]float32, error) {
	f := t.Meta.Far
	if f == nil {
		return nil, fmt.Errorf("the terrain has no far backdrop")
	}
	data, err := os.ReadFile(filepath.Join(t.Dir, f.Height.File))
	if err != nil {
		return nil, err
	}
	w, h, vals, err := decodeGray(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Height.File, err)
	}
	if w != f.Samples || h != f.Samples {
		return nil, fmt.Errorf("%s is %dx%d, want %d", f.Height.File, w, h, f.Samples)
	}
	span := f.Height.Hi - f.Height.Lo
	for i, v := range vals {
		vals[i] = f.Height.Lo + v*span
	}
	return vals, nil
}

// Bounds is the world XZ rectangle, and the height range.
type Bounds struct {
	MinX float64 `yaml:"min_x"`
	MinZ float64 `yaml:"min_z"`
	MaxX float64 `yaml:"max_x"`
	MaxZ float64 `yaml:"max_z"`
	MinY float32 `yaml:"min_y"`
	MaxY float32 `yaml:"max_y"`
}

// Height says how heightmap.png decodes.
type Height struct {
	File string  `yaml:"file"`
	Lo   float32 `yaml:"lo"`
	Hi   float32 `yaml:"hi"`
	// Encoding is "u16": metres = lo + v/65535·(hi-lo).
	Encoding string `yaml:"encoding"`
}

// MaskMeta is one mask's entry.
type MaskMeta struct {
	Name string `yaml:"name"`
	File string `yaml:"file"`
	// Encoding is "unit" (v/255 is the 0..1 value) or "index" (v is an
	// integer: the strata list index).
	Encoding string `yaml:"encoding"`
	Doc      string `yaml:"doc"`
}

// StrataMeta is the strata the masks and albedo were made with.
type StrataMeta struct {
	Base float64 `yaml:"base"`
	// Dip is metres of rise per 100 m along X and Z; Warp the noise
	// displacement in metres. A sample's strata coordinate is its height
	// plus both, which is why strata_height is written out.
	Dip    [2]float64  `yaml:"dip"`
	Warp   float64     `yaml:"warp"`
	Layers []LayerMeta `yaml:"layers"`
}

// LayerMeta is one layer, as listed.
type LayerMeta struct {
	Thickness float64    `yaml:"thickness"`
	Hardness  float64    `yaml:"hardness"`
	Color     [3]float64 `yaml:"color"`
}

// TilesMeta is the tile grid.
type TilesMeta struct {
	// Size is a tile's side (metres); Count the tiles a side. Tile (i, j)
	// spans X from min_x + i·size, Z from min_z + j·size, and is placed at
	// its centre with the mesh in tile-local coordinates.
	Size  float64 `yaml:"size"`
	Count int     `yaml:"count"`
	// Base is the closed tiles' bottom height; Step the shared
	// quantisation step every tile was cooked on (0: not cooked).
	Base  float32  `yaml:"base"`
	Step  float32  `yaml:"step"`
	Files []string `yaml:"files"`
	// Triangles is each tile's, before the cook.
	Triangles int `yaml:"triangles"`
}

// PathMeta is a carved canyon centreline, downstream, with its width and
// floor height per point.
type PathMeta struct {
	Tributary bool         `yaml:"tributary,omitempty"`
	Points    [][2]float64 `yaml:"points,flow"`
	Width     []float64    `yaml:"width,flow"`
	Floor     []float64    `yaml:"floor,flow"`
}

// NewMeta describes res made from s.
func NewMeta(s *Settings, res *Result) Meta {
	g := res.Grid
	lo, hi := g.Range()
	m := Meta{
		Version: MetaVersion, Size: g.Size(), Resolution: g.Cell, Samples: g.S(),
		Bounds: Bounds{MinX: g.MinX, MinZ: g.MinZ, MaxX: g.MinX + g.Size(), MaxZ: g.MinZ + g.Size(), MinY: lo, MaxY: hi},
		Height: Height{File: HeightmapFile, Lo: lo, Hi: hi, Encoding: "u16"},
		Albedo: AlbedoFile, Relief: ReliefFile,
		Strata: StrataMeta{Base: s.Strata.Base, Dip: s.Strata.Dip, Warp: s.Strata.Warp},
	}
	for _, name := range MaskNames {
		enc := "unit"
		if name == MaskStrata {
			enc = "index"
		}
		m.Masks = append(m.Masks, MaskMeta{Name: name, File: MaskFile(name), Encoding: enc, Doc: MaskDocs[name]})
	}
	for _, l := range s.Strata.Layers {
		m.Strata.Layers = append(m.Strata.Layers, LayerMeta{Thickness: l.Thickness, Hardness: l.Hardness, Color: l.Color})
	}
	for _, p := range res.Paths {
		pm := PathMeta{Tributary: p.Tributary}
		// Every few metres is plenty for anything that follows a canyon.
		step := max(len(p.Points)/400, 1)
		for k := 0; k < len(p.Points); k += step {
			pm.Points = append(pm.Points, [2]float64{round2(p.Points[k][0]), round2(p.Points[k][1])})
			pm.Width = append(pm.Width, round2(p.Width[k]))
			pm.Floor = append(pm.Floor, round2(p.Floor[k]))
		}
		m.Paths = append(m.Paths, pm)
	}
	for _, v := range res.Vents {
		m.Vents = append(m.Vents, VentMeta{X: round2(v.X), Z: round2(v.Z), Radius: round2(v.Radius)})
	}
	m.Lobes = append(m.Lobes, res.Lobes...)
	return m
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// Encode is res's image files by name (heightmap, masks, albedo, relief).
// Each encode is independent; pool runs them side by side.
func Encode(res *Result, meta Meta, pool Pool) (map[string][]byte, error) {
	g := res.Grid
	sz := g.S()
	type job struct {
		name string
		fn   func() ([]byte, error)
	}
	jobs := []job{
		{HeightmapFile, func() ([]byte, error) { return EncodeGray16(sz, sz, g.H, meta.Height.Lo, meta.Height.Hi) }},
		{AlbedoFile, func() ([]byte, error) { return EncodeRGB(sz, sz, res.Albedo) }},
		{ReliefFile, func() ([]byte, error) { return EncodeRGBA(sz, sz, Relief(g, res.Albedo)) }},
	}
	for _, name := range MaskNames {
		vals := res.Masks[name]
		jobs = append(jobs, job{MaskFile(name), func() ([]byte, error) { return EncodeGray8(sz, sz, vals) }})
	}
	out := make([][]byte, len(jobs))
	err := pool.Parallel(len(jobs), func(i int) error {
		b, err := jobs[i].fn()
		out[i] = b
		return err
	})
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for i, j := range jobs {
		files[j.name] = out[i]
	}
	return files, nil
}

// EncodeGray16 is a 16-bit grey PNG of vals mapped lo..hi to 0..65535.
func EncodeGray16(w, h int, vals []float32, lo, hi float32) ([]byte, error) {
	img := image.NewGray16(image.Rect(0, 0, w, h))
	span := hi - lo
	if span == 0 {
		span = 1
	}
	for k := 0; k < w*h; k++ {
		t := min(max((vals[k]-lo)/span, 0), 1)
		v := uint16(float64(t)*65535 + 0.5)
		img.Pix[2*k], img.Pix[2*k+1] = uint8(v>>8), uint8(v)
	}
	return encodePNG(img)
}

// EncodeGray8 is an 8-bit grey PNG of 0..1 values.
func EncodeGray8(w, h int, vals []float32) ([]byte, error) {
	img := image.NewGray(image.Rect(0, 0, w, h))
	for k := 0; k < w*h; k++ {
		img.Pix[k] = uint8(min(max(vals[k], 0), 1)*255 + 0.5)
	}
	return encodePNG(img)
}

// EncodeRGB is an 8-bit RGB PNG (written as opaque RGBA).
func EncodeRGB(w, h int, rgb []uint8) ([]byte, error) {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for k := 0; k < w*h; k++ {
		copy(img.Pix[4*k:4*k+3], rgb[3*k:3*k+3])
		img.Pix[4*k+3] = 255
	}
	return encodePNG(img)
}

// EncodeRGBA is an 8-bit RGBA PNG.
func EncodeRGBA(w, h int, rgba []uint8) ([]byte, error) {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	copy(img.Pix, rgba)
	return encodePNG(img)
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// decodeGray reads a grey PNG as 0..1 values (8- or 16-bit).
func decodeGray(data []byte) (w, h int, vals []float32, err error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return 0, 0, nil, err
	}
	b := img.Bounds()
	w, h = b.Dx(), b.Dy()
	vals = make([]float32, w*h)
	switch im := img.(type) {
	case *image.Gray16:
		for j := 0; j < h; j++ {
			for i := 0; i < w; i++ {
				o := j*im.Stride + 2*i
				vals[j*w+i] = float32(uint16(im.Pix[o])<<8|uint16(im.Pix[o+1])) / 65535
			}
		}
	case *image.Gray:
		for j := 0; j < h; j++ {
			for i := 0; i < w; i++ {
				vals[j*w+i] = float32(im.Pix[j*im.Stride+i]) / 255
			}
		}
	default:
		for j := 0; j < h; j++ {
			for i := 0; i < w; i++ {
				r, _, _, _ := img.At(b.Min.X+i, b.Min.Y+j).RGBA()
				vals[j*w+i] = float32(r) / 65535
			}
		}
	}
	return w, h, vals, nil
}

// Terrain is a generated terrain as a downstream stage sees it.
type Terrain struct {
	Meta Meta
	Grid *Grid
	// Dir is the folder the masks load from ("" for one held in memory).
	Dir string

	mu    sync.Mutex
	masks map[string][]float32
	// Albedo is the RGB hint when held in memory (nil after Load).
	Albedo []uint8
}

// FromResult is the in-memory Terrain of a run that just made res.
func FromResult(res *Result, meta Meta, dir string) *Terrain {
	return &Terrain{Meta: meta, Grid: res.Grid, Dir: dir, masks: res.Masks, Albedo: res.Albedo}
}

// Load reads a terrain output folder's metadata and heights.
func Load(dir string) (*Terrain, error) {
	data, err := os.ReadFile(filepath.Join(dir, MetaFile))
	if err != nil {
		return nil, err
	}
	var m Meta
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", MetaFile, err)
	}
	if m.Version != MetaVersion {
		return nil, fmt.Errorf("%s: version %d, want %d", MetaFile, m.Version, MetaVersion)
	}
	hd, err := os.ReadFile(filepath.Join(dir, m.Height.File))
	if err != nil {
		return nil, err
	}
	w, h, vals, err := decodeGray(hd)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", m.Height.File, err)
	}
	if w != m.Samples || h != m.Samples {
		return nil, fmt.Errorf("%s is %dx%d, %s says %d samples", m.Height.File, w, h, MetaFile, m.Samples)
	}
	g := &Grid{N: m.Samples - 1, Cell: m.Resolution, MinX: m.Bounds.MinX, MinZ: m.Bounds.MinZ, H: vals}
	span := m.Height.Hi - m.Height.Lo
	for i, v := range vals {
		g.H[i] = m.Height.Lo + v*span
	}
	return &Terrain{Meta: m, Grid: g, Dir: dir, masks: map[string][]float32{}}, nil
}

// Mask is a mask's values (0..1; the strata mask's are index/255), loaded on
// first use.
func (t *Terrain) Mask(name string) ([]float32, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if v, ok := t.masks[name]; ok {
		return v, nil
	}
	if t.Dir == "" {
		return nil, fmt.Errorf("no mask %q", name)
	}
	file := ""
	for _, mm := range t.Meta.Masks {
		if mm.Name == name {
			file = mm.File
		}
	}
	if file == "" {
		return nil, fmt.Errorf("no mask %q", name)
	}
	data, err := os.ReadFile(filepath.Join(t.Dir, file))
	if err != nil {
		return nil, err
	}
	w, h, vals, err := decodeGray(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if w != t.Meta.Samples || h != t.Meta.Samples {
		return nil, fmt.Errorf("%s is %dx%d, want %d", file, w, h, t.Meta.Samples)
	}
	if t.masks == nil {
		t.masks = map[string][]float32{}
	}
	t.masks[name] = vals
	return vals, nil
}

// MaskAt is a mask's bilinear value at world (x, z).
func (t *Terrain) MaskAt(name string, x, z float64) (float32, error) {
	v, err := t.Mask(name)
	if err != nil {
		return 0, err
	}
	g := t.Grid
	return bilinear(v, g.S(), (x-g.MinX)/g.Cell, (z-g.MinZ)/g.Cell), nil
}

// HeightAt is the bilinear height at world (x, z).
func (t *Terrain) HeightAt(x, z float64) float32 { return t.Grid.Sample(x, z) }

// NormalAt is the unit surface normal at world (x, z).
func (t *Terrain) NormalAt(x, z float64) [3]float32 {
	d := t.Grid.Cell
	dx := (t.Grid.Sample(x+d, z) - t.Grid.Sample(x-d, z)) / float32(2*d)
	dz := (t.Grid.Sample(x, z+d) - t.Grid.Sample(x, z-d)) / float32(2*d)
	l := float32(math.Sqrt(float64(dx*dx + 1 + dz*dz)))
	return [3]float32{-dx / l, 1 / l, -dz / l}
}

// Contains reports whether world (x, z) is on the terrain.
func (t *Terrain) Contains(x, z float64) bool {
	b := t.Meta.Bounds
	return x >= b.MinX && x <= b.MaxX && z >= b.MinZ && z <= b.MaxZ
}

// Biomes is the environments of a region terrain, in its metadata's order;
// none for a classic terrain.
func (t *Terrain) Biomes() []string {
	if t.Meta.Biomes == nil {
		return nil
	}
	var out []string
	for _, e := range t.Meta.Biomes.Environments {
		out = append(out, e.Name)
	}
	return out
}

// BiomeAt is environment env's weight at world (x, z), 0..1. A classic
// terrain has no biomes and answers an error.
func (t *Terrain) BiomeAt(env string, x, z float64) (float32, error) {
	return t.MaskAt(BiomeMask(env), x, z)
}
