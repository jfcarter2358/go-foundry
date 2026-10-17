package terrain

// river_meta.go — terrain.yaml's `rivers:` section and the river masks a
// ground_edit with rivers writes (docs/terrain-paint/PHASE_W3.md).
//
// The masks are the rivers' fields (river_carve.go) on the grid, 8-bit
// like every mask, written only when a ground_edit carved at least one
// river (so every terrain before W3 is unchanged):
//
//	river_flow_x, river_flow_z  the current along X and Z: 0.5 + 0.5·v/RiverFlowMax
//	                            (0.5 still) — the FLOW MAP, its two channels
//	                            split over two masks (the ribbon meshes bake it
//	                            into their vertex colours; render reads no
//	                            texture: the water pass has no sampler free)
//	river_foam                  the rivers' foam, 0..1
//	river_bed                   the channel: 1 in the bed, fading over the banks
//	river_bar                   the point bars on the inside of bends
//
// The section lists the rivers (name, source, length, drop, ends), every
// waterfall (lip, foot, heading, width — what W4 builds its falls mesh and
// mist from) and every confluence (where W4 puts its confluence foam).
// Terrain.RectKey folds the masks in, so a downstream piece that reads any
// of them is rebuilt when a river changes under it.

// River mask names.
const (
	MaskRiverFlowX = "river_flow_x"
	MaskRiverFlowZ = "river_flow_z"
	MaskRiverFoam  = "river_foam"
	MaskRiverBed   = "river_bed"
	MaskRiverBar   = "river_bar"
)

// RiverMaskNames is every river mask, in the order terrain.yaml lists them.
var RiverMaskNames = []string{MaskRiverFlowX, MaskRiverFlowZ, MaskRiverFoam, MaskRiverBed, MaskRiverBar}

// RiverMaskDocs describe each river mask.
var RiverMaskDocs = map[string]string{
	MaskRiverFlowX: "river current along X: 0.5 + 0.5 v/4 m/s (0.5 still)",
	MaskRiverFlowZ: "river current along Z: 0.5 + 0.5 v/4 m/s (0.5 still)",
	MaskRiverFoam:  "river foam, 0..1",
	MaskRiverBed:   "river channel share: 1 in the bed, fading over the banks",
	MaskRiverBar:   "point bars on the inside of river bends, 0..1",
}

// RiverFlowMax is the speed (m/s) a flow channel's full range encodes; the
// water shader's vertex flow uses the same (resources/shaders/water.glsl
// WATER_FLOW_MAX).
const RiverFlowMax = 4.0

// RiversMeta is terrain.yaml's `rivers:`.
type RiversMeta struct {
	Rivers      []RiverMeta      `yaml:"rivers"`
	Falls       []WaterfallMeta  `yaml:"falls,omitempty"`
	Confluences []ConfluenceMeta `yaml:"confluences,omitempty"`
}

// RiverMeta is one river's summary.
type RiverMeta struct {
	Name     string  `yaml:"name"`
	Drainage bool    `yaml:"drainage,omitempty"`
	Material string  `yaml:"material"`
	Length   float64 `yaml:"length"`
	Source   float64 `yaml:"source"`
	Mouth    float64 `yaml:"mouth"`
	Into     string  `yaml:"into,omitempty"`
}

// WaterfallMeta is one waterfall: its lip's centre (x, z), the surface
// above and below, the heading over the lip and the width.
type WaterfallMeta struct {
	River string     `yaml:"river"`
	At    [2]float64 `yaml:"at,flow"`
	Lip   float64    `yaml:"lip"`
	Foot  float64    `yaml:"foot"`
	Dir   [2]float64 `yaml:"dir,flow"`
	Width float64    `yaml:"width"`
}

// ConfluenceMeta is one confluence: the tributary's mouth and its level.
type ConfluenceMeta struct {
	Main      string     `yaml:"main"`
	Tributary string     `yaml:"tributary"`
	At        [2]float64 `yaml:"at,flow"`
	Level     float64    `yaml:"level"`
}
