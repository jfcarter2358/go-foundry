package app

// graphics_settings.go — the player's graphics settings, on disk.
//
// controls_settings.go's arrangement, for the graphics choices a game's
// settings screen can offer: Smooth Lighting (gfx.SmoothLighting), the
// foliage distance and shadows (gfx.FoliageDistance, gfx.FoliageShadows), the
// Nanite LOD threshold (gfx.NaniteLODError), the Nanite page pool's size
// (gfx.NaniteStreamPoolMB), the live GI tier (gfx.GIQuality) and the
// anti-aliasing mode and TAA sharpening (gfx.AntiAliasMode, gfx.TAASharpen),
// and the texture memory settings (gfx.TextureCompression,
// gfx.TextureSkipMips). The
// file is graphics.yaml in the writable state directory, a missing file means
// defaults, a malformed one is logged and ignored, and a script's change is
// saved once it has settled (saveSettingsWhenSettled).

import (
	"log"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"

	"gopkg.in/yaml.v2"
)

const graphicsFileName = "graphics.yaml"

// rawGraphics is the on-disk shape; pointers so a field missing from an older
// file keeps its default.
type rawGraphics struct {
	SmoothLighting  *bool    `yaml:"smooth_lighting,omitempty"`
	FoliageDistance *float32 `yaml:"foliage_distance,omitempty"`
	FoliageShadows  *bool    `yaml:"foliage_shadows,omitempty"`
	// NaniteLODError is gfx.NaniteLODError, pixels.
	NaniteLODError *float32 `yaml:"nanite_lod_error,omitempty"`
	// NaniteStreamPoolMB is gfx.NaniteStreamPoolMB, megabytes.
	NaniteStreamPoolMB *int `yaml:"nanite_stream_pool_mb,omitempty"`
	// GIQuality is gfx.GIQuality by name: off, low, medium, high. Absent lets
	// the engine pick by GPU (gfx.PickDefaultGIQuality).
	GIQuality *string `yaml:"gi_quality,omitempty"`
	// AntiAliasing is gfx.AntiAliasMode by name: taa, msaa, off.
	AntiAliasing *string `yaml:"anti_aliasing,omitempty"`
	// TAASharpen is gfx.TAASharpen, 0-1.
	TAASharpen *float32 `yaml:"taa_sharpen,omitempty"`
	// TextureCompression is gfx.TextureCompression; TextureSkipMips is
	// gfx.TextureSkipMips, 0-3. FOUNDRY_TEXTURE_COMPRESSION and
	// FOUNDRY_TEXTURE_SKIP_MIPS, when set, outrank them for the run.
	TextureCompression *bool `yaml:"texture_compression,omitempty"`
	TextureSkipMips    *int  `yaml:"texture_skip_mips,omitempty"`
	// GrassDensity, GrassDistance and GrassShadowDistance are the Poly Haven
	// grass instancer's (gfx/grass_settings.go).
	GrassDensity        *float32 `yaml:"grass_density,omitempty"`
	GrassDistance       *float32 `yaml:"grass_distance,omitempty"`
	GrassShadowDistance *float32 `yaml:"grass_shadow_distance,omitempty"`
	// The lighting pass's settings (gfx/lighting_settings.go). Reflections
	// is a tier by name (off, low, high); absent ones keep the GPU's pick.
	Specular         *bool   `yaml:"specular,omitempty"`
	Reflections      *string `yaml:"reflections,omitempty"`
	AmbientOcclusion *bool   `yaml:"ambient_occlusion,omitempty"`
	Bloom            *bool   `yaml:"bloom,omitempty"`
	ContactShadows   *bool   `yaml:"contact_shadows,omitempty"`
	SoftShadows      *bool   `yaml:"soft_shadows,omitempty"`
	// WaterQuality is gfx.WaterQuality by name: low, medium, high.
	WaterQuality *string `yaml:"water_quality,omitempty"`
	// The render scale and dynamic resolution (gfx/render_scale.go).
	RenderScale               *float32 `yaml:"render_scale,omitempty"`
	DynamicResolution         *bool    `yaml:"dynamic_resolution,omitempty"`
	DynamicResolutionTargetMs *float32 `yaml:"dynamic_resolution_target_ms,omitempty"`
	DynamicResolutionMin      *float32 `yaml:"dynamic_resolution_min,omitempty"`
	// LightShafts is gfx.LightShaftsQuality by name: off, low, high.
	LightShafts *string `yaml:"light_shafts,omitempty"`
}

// loadGraphicsSettings reads the saved graphics settings over the defaults.
// Before the first level loads, so the first batches are built with the
// player's choice rather than rebuilt a frame later.
func loadGraphicsSettings() {
	data, err := fileutil.ReadState(graphicsFileName)
	if err != nil {
		return
	}
	if err := applyGraphicsSettings(data); err != nil {
		log.Printf("graphics: %s: %v (using defaults)", graphicsFileName, err)
	}
}

func applyGraphicsSettings(data []byte) error {
	var raw rawGraphics
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.SmoothLighting != nil {
		gfx.SmoothLighting = *raw.SmoothLighting
	}
	if raw.FoliageDistance != nil {
		gfx.FoliageDistance = min(max(*raw.FoliageDistance, gfx.FoliageDistanceMin), gfx.FoliageDistanceMax)
	}
	if raw.FoliageShadows != nil {
		gfx.FoliageShadows = *raw.FoliageShadows
	}
	if raw.NaniteLODError != nil {
		gfx.NaniteLODError = min(max(*raw.NaniteLODError, gfx.NaniteLODErrorMin), gfx.NaniteLODErrorMax)
	}
	if raw.NaniteStreamPoolMB != nil {
		gfx.NaniteStreamPoolMB = min(max(*raw.NaniteStreamPoolMB, gfx.NaniteStreamPoolMBMin), gfx.NaniteStreamPoolMBMax)
		gfx.NaniteStreamPoolChosen = true
	}
	if raw.GIQuality != nil {
		if q, ok := gfx.ParseGIQuality(*raw.GIQuality); ok {
			gfx.GIQuality, gfx.GIQualityChosen = q, true
		} else {
			log.Printf("graphics: unknown gi_quality %q (want off, low, medium or high)", *raw.GIQuality)
		}
	}
	// FOUNDRY_AA, when set, outranks the saved mode for the run.
	if raw.AntiAliasing != nil && !gfx.AntiAliasModeForced {
		if m, ok := gfx.ParseAAMode(*raw.AntiAliasing); ok {
			gfx.AntiAliasMode, gfx.AntiAliasModeChosen = m, true
		} else {
			log.Printf("graphics: unknown anti_aliasing %q (want taa, msaa or off)", *raw.AntiAliasing)
		}
	}
	if raw.TAASharpen != nil {
		gfx.TAASharpen = min(max(*raw.TAASharpen, 0), 1)
	}
	if raw.TextureCompression != nil && !gfx.TextureCompressionForced {
		gfx.TextureCompression = *raw.TextureCompression
	}
	if raw.TextureSkipMips != nil && !gfx.TextureSkipMipsForced {
		gfx.TextureSkipMips = gfx.ClampTextureSkipMips(*raw.TextureSkipMips)
	}
	if raw.GrassDensity != nil {
		gfx.GrassDensity = gfx.ClampGrassDensity(*raw.GrassDensity)
	}
	if raw.GrassDistance != nil {
		gfx.GrassDistance = gfx.ClampGrassDistance(*raw.GrassDistance)
	}
	if raw.GrassShadowDistance != nil {
		gfx.GrassShadowDistance = gfx.ClampGrassShadowDistance(*raw.GrassShadowDistance)
	}
	applyLightingSettings(&raw)
	applyRenderScaleSettings(&raw)
	if raw.WaterQuality != nil {
		if q, ok := gfx.ParseWaterQuality(*raw.WaterQuality); ok {
			gfx.WaterQuality, gfx.WaterQualityChosen = q, true
		} else {
			log.Printf("graphics: unknown water_quality %q (want low, medium or high)", *raw.WaterQuality)
		}
	}
	return nil
}

func marshalGraphicsSettings() ([]byte, error) {
	smooth := gfx.SmoothLighting
	dist := gfx.FoliageDistance
	shadows := gfx.FoliageShadows
	lod := gfx.NaniteLODError
	pool := gfx.NaniteStreamPoolMB
	sharpen := gfx.TAASharpen
	raw := rawGraphics{SmoothLighting: &smooth, FoliageDistance: &dist, FoliageShadows: &shadows, NaniteLODError: &lod,
		NaniteStreamPoolMB: &pool, TAASharpen: &sharpen}
	texCompress, skipMips := gfx.TextureCompression, gfx.TextureSkipMips
	raw.TextureCompression, raw.TextureSkipMips = &texCompress, &skipMips
	grassDensity, grassDistance, grassShadow := gfx.GrassDensity, gfx.GrassDistance, gfx.GrassShadowDistance
	raw.GrassDensity, raw.GrassDistance, raw.GrassShadowDistance = &grassDensity, &grassDistance, &grassShadow
	marshalLightingSettings(&raw)
	marshalRenderScaleSettings(&raw)
	// The mode likewise, only once chosen.
	if gfx.AntiAliasModeChosen {
		aa := gfx.AntiAliasMode.String()
		raw.AntiAliasing = &aa
	}
	// Only a tier someone chose is written: one the engine picked by GPU
	// stays a pick, and is picked again on whatever GPU runs it next.
	if gfx.GIQualityChosen {
		gi := gfx.GIQuality.String()
		raw.GIQuality = &gi
	}
	if gfx.WaterQualityChosen {
		wq := gfx.WaterQuality.String()
		raw.WaterQuality = &wq
	}
	return yaml.Marshal(raw)
}

var graphicsSave settledSave

// saveGraphicsWhenSettled writes graphics.yaml once a change has settled.
func saveGraphicsWhenSettled(dt float32) {
	if !graphicsSave.step(&gfx.GraphicsSettingsChanged, dt) {
		return
	}
	data, err := marshalGraphicsSettings()
	if err == nil {
		err = fileutil.WriteFileAtomic(fileutil.StatePath(graphicsFileName), data)
	}
	if err != nil {
		log.Printf("graphics: saving %s: %v", graphicsFileName, err)
	}
}

// applyLightingSettings reads the lighting pass's settings over the defaults.
func applyLightingSettings(raw *rawGraphics) {
	if raw.Specular != nil {
		gfx.SpecularLighting = *raw.Specular
	}
	if raw.Reflections != nil {
		if q, ok := gfx.ParseReflectionQuality(*raw.Reflections); ok {
			gfx.Reflections, gfx.ReflectionsChosen = q, true
		} else {
			log.Printf("graphics: unknown reflections %q (want off, low or high)", *raw.Reflections)
		}
	}
	if raw.AmbientOcclusion != nil {
		gfx.AmbientOcclusion, gfx.AmbientOcclusionChosen = *raw.AmbientOcclusion, true
	}
	if raw.Bloom != nil {
		gfx.Bloom = *raw.Bloom
	}
	if raw.ContactShadows != nil {
		gfx.ContactShadows, gfx.ContactShadowsChosen = *raw.ContactShadows, true
	}
	if raw.SoftShadows != nil {
		gfx.SoftShadows = *raw.SoftShadows
	}
}

// marshalLightingSettings writes them; the GPU-picked tiers only once chosen,
// as gi_quality is.
func marshalLightingSettings(raw *rawGraphics) {
	spec, bloom, soft := gfx.SpecularLighting, gfx.Bloom, gfx.SoftShadows
	raw.Specular, raw.Bloom, raw.SoftShadows = &spec, &bloom, &soft
	if gfx.ReflectionsChosen {
		r := gfx.Reflections.String()
		raw.Reflections = &r
	}
	if gfx.AmbientOcclusionChosen {
		ao := gfx.AmbientOcclusion
		raw.AmbientOcclusion = &ao
	}
	if gfx.ContactShadowsChosen {
		cs := gfx.ContactShadows
		raw.ContactShadows = &cs
	}
}

// applyRenderScaleSettings reads the render scale and dynamic resolution
// (gfx/render_scale.go); FOUNDRY_RENDER_SCALE / FOUNDRY_DYNAMIC_RES outrank
// them for the run.
func applyRenderScaleSettings(raw *rawGraphics) {
	if raw.RenderScale != nil && !gfx.RenderScaleForced {
		gfx.RenderScale = gfx.ClampRenderScale(*raw.RenderScale)
	}
	if raw.DynamicResolution != nil && !gfx.DynamicResolutionForced {
		gfx.DynamicResolution = *raw.DynamicResolution
	}
	if raw.DynamicResolutionTargetMs != nil {
		gfx.DynamicResolutionTargetMs = gfx.ClampDynamicResolutionTarget(*raw.DynamicResolutionTargetMs)
	}
	if raw.DynamicResolutionMin != nil {
		gfx.DynamicResolutionMin = gfx.ClampRenderScale(*raw.DynamicResolutionMin)
	}
	if raw.LightShafts != nil {
		if q, ok := gfx.ParseShaftsQuality(*raw.LightShafts); ok {
			gfx.LightShaftsQuality = q
		} else {
			log.Printf("graphics: unknown light_shafts %q (want off, low or high)", *raw.LightShafts)
		}
	}
}

// marshalRenderScaleSettings writes them.
func marshalRenderScaleSettings(raw *rawGraphics) {
	scale, dyn, target, lo := gfx.RenderScale, gfx.DynamicResolution, gfx.DynamicResolutionTargetMs, gfx.DynamicResolutionMin
	raw.RenderScale, raw.DynamicResolution = &scale, &dyn
	raw.DynamicResolutionTargetMs, raw.DynamicResolutionMin = &target, &lo
	shafts := gfx.LightShaftsQuality.String()
	raw.LightShafts = &shafts
}
