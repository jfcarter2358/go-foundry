package terrain

// path_meta.go — terrain.yaml's `footpaths:` section (docs/terrain-paint/
// PHASE_P4.md): one line per path a ground_edit laid. The path masks
// themselves are listed with the other masks (path.go, PathMaskNames) and
// folded into Terrain.RectKey, so a downstream piece reading any of them
// is rebuilt when a path changes under it.

// FootpathsMeta is terrain.yaml's `footpaths:` (Meta.Paths is a canyon's
// carved centrelines, an older and unrelated key).
type FootpathsMeta struct {
	Paths []FootpathMeta `yaml:"paths"`
}

// FootpathMeta is one path's summary.
type FootpathMeta struct {
	Name  string `yaml:"name"`
	Style string `yaml:"style"`
	// Length is along the centreline (m); Crossings the stretches under
	// water; Stones how many stones it laid (fords included).
	Length    float64 `yaml:"length"`
	Crossings int     `yaml:"crossings,omitempty"`
	Stones    int     `yaml:"stones,omitempty"`
}
