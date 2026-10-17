package rocks

// meta.go — what later stages know about a variant without opening its mesh:
// the manifest (rocks.yaml) the stage writes beside the cooked variants.
//
// Phase 3's scatter and cliff_wall place variants from it: scatter needs each
// rock's footprint (overlap rejection, Poisson spacing), height and size (the
// power-law size bias, the sink depth as a fraction of size) and the
// collision to give it; cliff_wall needs a chunk's back (which way is "into
// the wall"), its width and height (tiers, spacing) and where its top is (to
// stack the next tier and to lay a ledge). docs/canyon/PHASE_2.md documents
// every field.
//
// THE FRAME. Every variant's mesh is in the same placement frame: +Y up, the
// lowest point on y = 0 (a placement at a ground point puts the rock ON it —
// sink it by moving it down), its footprint's bounding box centred on the Y
// axis. A variant with a flat back (cliff_chunk) has the back on the plane
// z = 0, facing −Z, the rock in front along +Z, centred in X: place it at a
// point on the wall with +Z turned out of the wall.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v2"
)

// ManifestFile is the manifest's name in the stage's output directory.
const ManifestFile = "rocks.yaml"

// ManifestVersion is its format's version.
const ManifestVersion = 1

// Manifest is a rocks stage's rocks.yaml.
type Manifest struct {
	Version   int    `yaml:"version"`
	Preset    string `yaml:"preset"`
	Lithology string `yaml:"lithology"`
	Palette   string `yaml:"palette"`
	// Frame restates the placement frame, for people reading the file.
	Frame    string        `yaml:"frame"`
	Variants []VariantMeta `yaml:"variants"`
	// Material is the .fmat every variant wears unless the stage placing it
	// writes its own (which then builds on this one): a generated rock's
	// triplanar Poly Haven texture (docs/canyon/PHASE_5.md). Empty: none.
	Material string `yaml:"material,omitempty"`
	// MatchGround is the colour match toward the ground the placing stages
	// apply by default (their match_ground -1): a Poly Haven rocks stage's
	// match_ground.
	MatchGround float64 `yaml:"match_ground,omitempty"`
}

// FrameDoc is Manifest.Frame's text.
const FrameDoc = "+Y up; lowest point at y=0; footprint centred on the Y axis; a flat back (back: set) on z=0 facing -Z, the rock along +Z"

// VariantMeta is one variant.
type VariantMeta struct {
	// Name is the variant's stem (boulder_03) and the placement ID suffix;
	// File its cooked mesh (or .glb), relative to the manifest.
	Name string `yaml:"name"`
	File string `yaml:"file"`
	// Model, when set, is the mesh's resources-relative path, overriding
	// File: a manifest describing meshes kept elsewhere — the Poly Haven
	// stages' imports, shared by every generator under
	// models/polyhaven/<id>/ (docs/canyon/PHASE_4.md). File then names the
	// same mesh for people reading the manifest.
	Model string `yaml:"model,omitempty"`
	Seed  uint64 `yaml:"seed"`
	// Size is the preset's main dimension as this variant came out (its
	// length, height or width — Manifest's preset says which).
	Size float64 `yaml:"size"`
	// Min and Max bound the mesh; Extent is Max − Min.
	Min    [3]float32 `yaml:"min,flow"`
	Max    [3]float32 `yaml:"max,flow"`
	Extent [3]float32 `yaml:"extent,flow"`
	// Height is Max.y (the base is at 0).
	Height float32 `yaml:"height"`
	// Radius is the bounding cylinder about +Y; FootprintRadius the
	// cylinder of what lies within BaseBand of the ground — what touches it,
	// which is what spacing on the ground cares about.
	Radius          float32 `yaml:"radius"`
	FootprintRadius float32 `yaml:"footprint_radius"`
	BaseBand        float32 `yaml:"base_band"`
	// SphereCenter and SphereRadius bound the mesh (overlap tests).
	SphereCenter [3]float32 `yaml:"sphere_center,flow"`
	SphereRadius float32    `yaml:"sphere_radius"`
	// Top is the height of its upper surface (the 90th percentile of the
	// up-facing vertices): where a ledge is walked, where the next tier of a
	// wall sits.
	Top float32 `yaml:"top"`
	// Back is the flat back's outward direction (0, 0, −1), for a variant
	// that has one (the back is on z = 0).
	Back *[3]float32 `yaml:"back,omitempty,flow"`
	// FlatBase says the base was cut flat (it sits without sinking).
	FlatBase bool    `yaml:"flat_base"`
	Volume   float32 `yaml:"volume"`
	// Triangles and Vertices are the mesh's before the cook (the cook's
	// level 0 is the same triangles).
	Triangles int `yaml:"triangles"`
	Vertices  int `yaml:"vertices"`
	// Collision is what the cook built (hulls: a convex decomposition,
	// mesh: the mesh alone); PlaceCollision what a placement should ask
	// for (hulls, mesh, or none for a pebble).
	Collision      string `yaml:"collision"`
	PlaceCollision string `yaml:"place_collision"`
	// The times, milliseconds: meshing, decimation, bake, cook.
	MeshMS     float64 `yaml:"mesh_ms"`
	SimplifyMS float64 `yaml:"simplify_ms"`
	BakeMS     float64 `yaml:"bake_ms"`
	CookMS     float64 `yaml:"cook_ms"`
	// MeshTriangles and Cell are what the mesher made, at what cell.
	MeshTriangles int     `yaml:"mesh_triangles"`
	Cell          float64 `yaml:"cell"`
}

// measure is a finished mesh's metadata (the stage fills File, Collision
// and the cook's time).
func measure(m *Mesh, s Settings, pr *preset, seed uint64) VariantMeta {
	v := VariantMeta{Seed: seed, Triangles: m.TriangleCount(), Vertices: len(m.Positions), FlatBase: pr.flat,
		PlaceCollision: pr.collision}
	first := true
	for _, p := range m.Positions {
		if first {
			v.Min, v.Max, first = p, p, false
			continue
		}
		for k := 0; k < 3; k++ {
			v.Min[k] = min(v.Min[k], p[k])
			v.Max[k] = max(v.Max[k], p[k])
		}
	}
	for k := 0; k < 3; k++ {
		v.Extent[k] = v.Max[k] - v.Min[k]
	}
	v.Height = v.Max[1]
	v.Radius = float32(radiusXZ(m, -1, v.Max[1]+1))
	v.BaseBand = float32(math.Min(0.12*float64(v.Height), 0.25*float64(max(v.Extent[0], v.Extent[2]))))
	v.FootprintRadius = float32(radiusXZ(m, -1, v.BaseBand))
	for k := 0; k < 3; k++ {
		v.SphereCenter[k] = (v.Min[k] + v.Max[k]) / 2
	}
	for _, p := range m.Positions {
		d := 0.0
		for k := 0; k < 3; k++ {
			d += float64(p[k]-v.SphereCenter[k]) * float64(p[k]-v.SphereCenter[k])
		}
		v.SphereRadius = max(v.SphereRadius, float32(math.Sqrt(d)))
	}
	// The top: the up-facing vertices' heights, high percentile.
	var ups []float32
	for i, n := range m.Normals {
		if n[1] > 0.8 {
			ups = append(ups, m.Positions[i][1])
		}
	}
	if len(ups) > 0 {
		sort.Slice(ups, func(a, b int) bool { return ups[a] < ups[b] })
		v.Top = ups[min(len(ups)-1, len(ups)*9/10)]
	} else {
		v.Top = v.Height
	}
	if pr.back {
		v.Back = &[3]float32{0, 0, -1}
	}
	// Volume, from the triangles about the origin.
	vol := 0.0
	for t := 0; t+2 < len(m.Indices); t += 3 {
		a, b, c := m.Positions[m.Indices[t]], m.Positions[m.Indices[t+1]], m.Positions[m.Indices[t+2]]
		ax, ay, az := float64(a[0]), float64(a[1]), float64(a[2])
		bx, by, bz := float64(b[0]), float64(b[1]), float64(b[2])
		cx, cy, cz := float64(c[0]), float64(c[1]), float64(c[2])
		vol += (ax*(by*cz-bz*cy) + ay*(bz*cx-bx*cz) + az*(bx*cy-by*cx)) / 6
	}
	v.Volume = float32(vol)
	return v
}

// LoadManifest reads a rocks stage's manifest from its output directory.
func LoadManifest(dir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := yaml.UnmarshalStrict(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	if m.Version != ManifestVersion {
		return nil, fmt.Errorf("%s: version %d, this build reads %d (re-run the stage)", ManifestFile, m.Version, ManifestVersion)
	}
	return &m, nil
}

// Encode is the manifest as YAML.
func (m *Manifest) Encode() ([]byte, error) { return yaml.Marshal(m) }
