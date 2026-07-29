package main

// detail.go — per-face procedural detail (GPU-generated rock surfaces & scatter).
//
// A face (Floor/Ceiling/Wall) may carry a DetailSpec. When enabled it is no
// longer drawn as a flat textured surface: instead the GPU generates detail from
// the face's own triangles, deterministically from a per-face Seed.
//
//   - Displace: the face is drawn as GL_PATCHES through a tessellation program
//     that subdivides each triangle and pushes the generated vertices out along
//     the face normal by fractal value-noise — turning a flat quad into a bumpy
//     rocky surface. It reuses the main fragment shader, so lighting and shadow
//     *receiving* are identical to normal geometry, and it is drawn in the shadow
//     depth passes too so it *casts* accurate shadows.
//   - Scatter: small rock meshes are instanced across the face plane (mirrors the
//     grass.go biome scatter), placed by a hash seeded from Seed, and dropped onto
//     the (optionally displaced) surface. Also reuses the main fragment/shadow
//     shaders for accurate cast/receive.
//
// Collision is unaffected — the player still walks on the original flat face.
// Rendering lives in detail_render.go; this file is the data model + presets.

// DetailSpec is the per-face procedural-detail configuration. The zero value
// (Enabled=false) makes the face behave exactly as an ordinary flat face.
type DetailSpec struct {
	Enabled bool   // face is designated for procedural detail
	Preset  string // named preset the params were seeded from ("rock", ...)
	Seed    int64  // deterministic seed for this face's noise/scatter

	// --- Surface displacement (tessellation) ---
	Displace  bool
	Amplitude float32 // max displacement along the face normal, world units
	Frequency float32 // noise features per world unit
	Octaves   int     // fractal octaves (surface roughness), 1..6
	TessLevel float32 // subdivision density per patch edge, 1..64
	EdgeTaper float32 // band (world units) over which displacement fades to 0 at
	//                   the face's outer boundary, so it meets adjacent walls
	//                   seamlessly. 0 = no taper (hard seams).

	// --- Scatter (instanced meshes) ---
	Scatter        bool
	ScatterMesh    string  // template mesh key ("rock", ...)
	ScatterTex     string  // texture applied to scattered instances
	ScatterDensity float32 // coverage 0..1
	ScatterSpacing float32 // lattice spacing on the face, world units
	ScatterScale   float32 // world size of one instance
	ScatterJitter  float32 // positional jitter 0..1
	ScatterRange   float32 // camera-ring draw radius (bounds instance count)
}

// HasDisplace reports whether this face should render displaced surface detail.
func (d *DetailSpec) HasDisplace() bool { return d.Enabled && d.Displace }

// HasScatter reports whether this face should render scattered instance detail.
func (d *DetailSpec) HasScatter() bool { return d.Enabled && d.Scatter }

// detailPresets holds ready-made parameter sets. Selecting a preset in the
// editor copies its params in; every field stays individually editable after.
var detailPresets = map[string]DetailSpec{
	"rock": {
		Preset: "rock", Displace: true,
		Amplitude: 0.35, Frequency: 0.6, Octaves: 4, TessLevel: 24, EdgeTaper: 0.4,
		Scatter: false, ScatterMesh: "rock", ScatterTex: "rock",
		ScatterDensity: 0.5, ScatterSpacing: 1.4, ScatterScale: 0.5,
		ScatterJitter: 0.8, ScatterRange: 60,
	},
	"cliff": {
		Preset: "cliff", Displace: true,
		Amplitude: 0.9, Frequency: 0.35, Octaves: 5, TessLevel: 32, EdgeTaper: 0.6,
		Scatter: true, ScatterMesh: "rock", ScatterTex: "rock",
		ScatterDensity: 0.3, ScatterSpacing: 2.0, ScatterScale: 0.7,
		ScatterJitter: 0.9, ScatterRange: 80,
	},
	"gravel": {
		Preset: "gravel", Displace: false,
		Amplitude: 0.08, Frequency: 2.0, Octaves: 3, TessLevel: 12, EdgeTaper: 0.2,
		Scatter: true, ScatterMesh: "rock", ScatterTex: "rock",
		ScatterDensity: 0.9, ScatterSpacing: 0.5, ScatterScale: 0.18,
		ScatterJitter: 1.0, ScatterRange: 40,
	},
}

// detailPresetNames is a stable, sorted-ish list for the UI combo.
var detailPresetNames = []string{"rock", "cliff", "gravel"}

// applyDetailPreset fills d with the named preset's parameters while preserving
// the face's existing Seed (or assigning one if unset). Enabled is turned on.
func applyDetailPreset(d *DetailSpec, name string) {
	p, ok := detailPresets[name]
	if !ok {
		return
	}
	seed := d.Seed
	*d = p
	d.Enabled = true
	if seed == 0 {
		seed = 1
	}
	d.Seed = seed
}

// clampDetail normalizes out-of-range params so the shaders stay well-defined.
func (d *DetailSpec) clamp() {
	if d.Octaves < 1 {
		d.Octaves = 1
	}
	if d.Octaves > 6 {
		d.Octaves = 6
	}
	if d.TessLevel < 1 {
		d.TessLevel = 1
	}
	if d.TessLevel > 64 {
		d.TessLevel = 64
	}
	if d.EdgeTaper < 0 {
		d.EdgeTaper = 0
	}
	if d.ScatterSpacing < 0.05 {
		d.ScatterSpacing = 0.05
	}
	if d.ScatterRange < 1 {
		d.ScatterRange = 1
	}
	if d.Seed == 0 {
		d.Seed = 1
	}
}
