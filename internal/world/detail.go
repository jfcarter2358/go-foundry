package world

// go — per-face procedural detail (GPU-generated rock surfaces & scatter).
//
// A face (Floor/Ceiling/Wall) may carry a DetailSpec. When enabled it is no
// longer drawn as a flat textured surface: instead the GPU generates detail from
// the face's own triangles, deterministically from a per-face Seed.
//
//   - Displace: the face is drawn as GL_PATCHES through a tessellation program
//     that subdivides each triangle and pushes the generated vertices out along
//     the face normal — turning a flat quad into a shaped surface. The offset is
//     the sum of up to three sine waves (each with its own amplitude, frequency
//     and tessellation) plus, optionally, fractal value-noise for roughness. It
//     reuses the main fragment shader, so lighting and shadow *receiving* are
//     identical to normal geometry, and it is drawn in the shadow depth passes
//     too so it *casts* accurate shadows.
//   - Scatter: small rock meshes are instanced across the face plane (mirrors the
//     grass.go biome scatter), placed by a hash seeded from Seed, and dropped onto
//     the (optionally displaced) surface. Also reuses the main fragment/shadow
//     shaders for accurate cast/receive.
//
// Collision is unaffected — the player still walks on the original flat face.
// Rendering lives in detail_render.go; this file is the data model + presets.

// DetailMaxWaves is the number of displacement wave slots per face. Must match
// DETAIL_MAX_WAVES in detail_common.glsl.
const DetailMaxWaves = 3

// DetailWave is one displacement layer: a sine wave running across the face
// plane. Enabled waves are summed to form the surface shape.
type DetailWave struct {
	Enabled   bool
	Amplitude float32 // peak displacement along the face normal, world units
	Frequency float32 // wave cycles per world unit (0 = flat)
	Rotation  float32 // travel direction within the face plane, degrees. 0 runs
	//                   along the face's reference tangent (horizontally on a
	//                   wall, along world Z on a floor/ceiling); 90 is across it.
	TessLevel float32 // subdivision density this wave needs, 1..64
}

// DetailSpec is the per-face procedural-detail configuration. The zero value
// (Enabled=false) makes the face behave exactly as an ordinary flat face.
type DetailSpec struct {
	Enabled bool   // face is designated for procedural detail
	Preset  string // named preset the params were seeded from ("rock", ...)
	Seed    int64  // deterministic seed for this face's noise/scatter

	// --- Surface displacement (tessellation) ---
	Displace bool
	Waves    [DetailMaxWaves]DetailWave // summed sine layers

	// Optional fractal value-noise added on top of the summed waves, for rocky
	// roughness. Off by default so waves alone stay smooth.
	Noise          bool
	NoiseAmplitude float32 // max noise displacement along the normal, world units
	NoiseFrequency float32 // noise features per world unit
	NoiseOctaves   int     // fractal octaves, 1..6
	NoiseTessLevel float32 // subdivision density the noise needs, 1..64

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

// DetailPresets holds ready-made parameter sets. Selecting a preset in the
// editor copies its params in; every field stays individually editable after.
var DetailPresets = map[string]DetailSpec{
	"rock": {
		Preset: "rock", Displace: true,
		Waves: [DetailMaxWaves]DetailWave{
			{Enabled: true, Amplitude: 0.25, Frequency: 0.5, Rotation: 0, TessLevel: 24},
			{Enabled: true, Amplitude: 0.10, Frequency: 1.3, Rotation: 65, TessLevel: 24},
			{},
		},
		Noise: true, NoiseAmplitude: 0.15, NoiseFrequency: 1.2, NoiseOctaves: 4, NoiseTessLevel: 24,
		EdgeTaper: 0.4,
		Scatter:   false, ScatterMesh: "rock", ScatterTex: "rock",
		ScatterDensity: 0.5, ScatterSpacing: 1.4, ScatterScale: 0.5,
		ScatterJitter: 0.8, ScatterRange: 60,
	},
	"cliff": {
		Preset: "cliff", Displace: true,
		Waves: [DetailMaxWaves]DetailWave{
			{Enabled: true, Amplitude: 0.60, Frequency: 0.20, Rotation: 0, TessLevel: 32},
			{Enabled: true, Amplitude: 0.25, Frequency: 0.55, Rotation: 55, TessLevel: 32},
			{Enabled: true, Amplitude: 0.10, Frequency: 1.40, Rotation: 115, TessLevel: 32},
		},
		Noise: true, NoiseAmplitude: 0.3, NoiseFrequency: 0.7, NoiseOctaves: 5, NoiseTessLevel: 32,
		EdgeTaper: 0.6,
		Scatter:   true, ScatterMesh: "rock", ScatterTex: "rock",
		ScatterDensity: 0.3, ScatterSpacing: 2.0, ScatterScale: 0.7,
		ScatterJitter: 0.9, ScatterRange: 80,
	},
	"gravel": {
		Preset: "gravel", Displace: false,
		Waves: [DetailMaxWaves]DetailWave{
			{Enabled: true, Amplitude: 0.05, Frequency: 1.5, Rotation: 0, TessLevel: 12},
			{}, {},
		},
		Noise: true, NoiseAmplitude: 0.06, NoiseFrequency: 2.5, NoiseOctaves: 3, NoiseTessLevel: 12,
		EdgeTaper: 0.2,
		Scatter:   true, ScatterMesh: "rock", ScatterTex: "rock",
		ScatterDensity: 0.9, ScatterSpacing: 0.5, ScatterScale: 0.18,
		ScatterJitter: 1.0, ScatterRange: 40,
	},
	"waves": {
		Preset: "waves", Displace: true,
		Waves: [DetailMaxWaves]DetailWave{
			{Enabled: true, Amplitude: 0.20, Frequency: 0.25, Rotation: 0, TessLevel: 32},
			{Enabled: true, Amplitude: 0.08, Frequency: 0.70, Rotation: 25, TessLevel: 32},
			{},
		},
		Noise: false, NoiseAmplitude: 0.05, NoiseFrequency: 2.0, NoiseOctaves: 3, NoiseTessLevel: 24,
		EdgeTaper: 0.5,
		Scatter:   false, ScatterMesh: "rock", ScatterTex: "rock",
		ScatterDensity: 0.4, ScatterSpacing: 1.4, ScatterScale: 0.4,
		ScatterJitter: 0.8, ScatterRange: 60,
	},
}

// DetailPresetNames is a stable, sorted-ish list for the UI combo.
var DetailPresetNames = []string{"rock", "cliff", "gravel", "waves"}

// DetailDefaultWave returns the wave a freshly-enabled slot starts from, so the
// face visibly changes right away. Each slot gets a different rotation: waves
// that all travel the same way just stack into one ridge family.
func DetailDefaultWave(slot int) DetailWave {
	return DetailWave{
		Enabled:   true,
		Amplitude: 0.25,
		Frequency: 0.5,
		Rotation:  float32(slot) * 55,
		TessLevel: 16,
	}
}

// ensureNoiseDefaults fills the noise params when noise is switched on for a face
// that has never had them set.
func (d *DetailSpec) EnsureNoiseDefaults() {
	if d.NoiseAmplitude == 0 {
		d.NoiseAmplitude = 0.12
	}
	if d.NoiseFrequency == 0 {
		d.NoiseFrequency = 1.5
	}
	if d.NoiseOctaves == 0 {
		d.NoiseOctaves = 3
	}
	if d.NoiseTessLevel == 0 {
		d.NoiseTessLevel = 16
	}
}

// ensureDisplaceDefaults gives a face at least one active displacement layer, so
// enabling "Displace surface" on a fresh face is not a no-op.
func (d *DetailSpec) EnsureDisplaceDefaults() {
	if d.Noise {
		return
	}
	for i := range d.Waves {
		if d.Waves[i].Enabled {
			return
		}
	}
	d.Waves[0] = DetailDefaultWave(0)
}

// tessLevelFor returns the subdivision level to run the patch at: the highest
// level requested by any active displacement layer (tessellation is per-patch,
// so the layers share one level).
func (d *DetailSpec) TessLevelFor() float32 {
	tess := float32(1)
	for i := range d.Waves {
		if d.Waves[i].Enabled && d.Waves[i].TessLevel > tess {
			tess = d.Waves[i].TessLevel
		}
	}
	if d.Noise && d.NoiseTessLevel > tess {
		tess = d.NoiseTessLevel
	}
	return tess
}

// ApplyDetailPreset fills d with the named preset's parameters while preserving
// the face's existing Seed (or assigning one if unset). Enabled is turned on.
func ApplyDetailPreset(d *DetailSpec, name string) {
	p, ok := DetailPresets[name]
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
func (d *DetailSpec) Clamp() {
	for i := range d.Waves {
		w := &d.Waves[i]
		if !w.Enabled {
			continue // leave untouched, so ensureDisplaceDefaults still sees "unset"
		}
		if w.Frequency < 0 {
			w.Frequency = 0
		}
		if w.Frequency > 20 {
			w.Frequency = 20
		}
		if w.TessLevel < 1 {
			w.TessLevel = 1
		}
		if w.TessLevel > 64 {
			w.TessLevel = 64
		}
	}
	// Only clamped while active, so an untouched noise layer stays all-zero and
	// ensureNoiseDefaults can tell it has never been configured.
	if d.Noise {
		if d.NoiseOctaves < 1 {
			d.NoiseOctaves = 1
		}
		if d.NoiseOctaves > 6 {
			d.NoiseOctaves = 6
		}
		if d.NoiseFrequency < 0.01 {
			d.NoiseFrequency = 0.01
		}
		if d.NoiseTessLevel < 1 {
			d.NoiseTessLevel = 1
		}
		if d.NoiseTessLevel > 64 {
			d.NoiseTessLevel = 64
		}
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

// DetailToRaw lives here rather than with the rest of the level serialiser
// because the user-preset store (detail_presets.go) writes DetailSpecs of its
// own, with no level involved.

// DetailToRaw converts a face's DetailSpec to an optional RawDetail. A disabled
// spec returns nil so the "detail" key is omitted from YAML entirely.
func DetailToRaw(d DetailSpec) *RawDetail {
	if !d.Enabled {
		return nil
	}
	waves := make([]RawDetailWave, 0, DetailMaxWaves)
	for _, w := range d.Waves {
		waves = append(waves, RawDetailWave{
			Enabled:   w.Enabled,
			Amplitude: w.Amplitude,
			Frequency: w.Frequency,
			Rotation:  w.Rotation,
			TessLevel: w.TessLevel,
		})
	}
	return &RawDetail{
		Preset:         d.Preset,
		Seed:           d.Seed,
		Displace:       d.Displace,
		Waves:          waves,
		Noise:          d.Noise,
		NoiseAmplitude: d.NoiseAmplitude,
		NoiseFrequency: d.NoiseFrequency,
		NoiseOctaves:   d.NoiseOctaves,
		NoiseTessLevel: d.NoiseTessLevel,
		EdgeTaper:      d.EdgeTaper,
		Scatter:        d.Scatter,
		ScatterMesh:    d.ScatterMesh,
		ScatterTex:     d.ScatterTex,
		ScatterDensity: d.ScatterDensity,
		ScatterSpacing: d.ScatterSpacing,
		ScatterScale:   d.ScatterScale,
		ScatterJitter:  d.ScatterJitter,
		ScatterRange:   d.ScatterRange,
	}
}
