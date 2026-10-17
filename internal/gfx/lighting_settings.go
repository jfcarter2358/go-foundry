package gfx

// lighting_settings.go — the player's settings for the lighting quality pass
// (docs/lighting/PLAN.md): specular, reflections, ambient occlusion, bloom,
// contact shadows and soft shadows. Player settings like SmoothLighting, saved
// to graphics.yaml (app/graphics_settings.go) and changed through the Set*
// functions so the save hears about it.
//
// Every one of them degrades to exactly the picture the engine drew before it
// when off, and the scene block's switches (scene_block.glsl, "lighting
// quality") are 0 until something writes these into it, so a program or a
// test that never heard of them shades as before.

import (
	"fmt"
	"log"
	"os"
	"strings"
)

// FOUNDRY_LIGHTING, when set, is ApplyLightingSpec's list for the whole run —
// a benchmark's A/B, a test run with the lighting pass off — and outranks
// graphics.yaml as FOUNDRY_AA does (the settings it names count as chosen).
func init() {
	if spec := os.Getenv("FOUNDRY_LIGHTING"); spec != "" {
		if err := ApplyLightingSpec(spec); err != nil {
			log.Printf("FOUNDRY_LIGHTING: %v", err)
		}
	}
}

// SpecularLighting is the GGX highlight from every light (specular.glsl).
var SpecularLighting = true

// ReflectionQuality is the image-based specular tier (render/envmap.go).
type ReflectionQuality int

const (
	// ReflectionsOff: direct-light highlights only.
	ReflectionsOff ReflectionQuality = iota
	// ReflectionsLow: the sky's prefiltered cube and the reflection
	// volumes, scaled by the GI probes' light.
	ReflectionsLow
	// ReflectionsHigh: Low plus screen-space reflections.
	ReflectionsHigh
	reflectionQualityCount
)

var reflectionQualityNames = [reflectionQualityCount]string{"off", "low", "high"}

func (q ReflectionQuality) String() string {
	if q < 0 || q >= reflectionQualityCount {
		return reflectionQualityNames[ReflectionsOff]
	}
	return reflectionQualityNames[q]
}

// ParseReflectionQuality reads a tier's name, case-insensitively.
func ParseReflectionQuality(s string) (ReflectionQuality, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for i, n := range reflectionQualityNames {
		if n == s {
			return ReflectionQuality(i), true
		}
	}
	return ReflectionsOff, false
}

// ReflectionQualityNames lists the tiers, lowest first.
func ReflectionQualityNames() []string { return reflectionQualityNames[:] }

var (
	// Reflections is the player's reflection tier. Low until a choice or
	// PickDefaultLighting says otherwise.
	Reflections = ReflectionsLow
	// ReflectionsChosen is whether Reflections came from the player.
	ReflectionsChosen bool

	// AmbientOcclusion is GTAO plus the GI field's AO (render/ao.go).
	AmbientOcclusion = true
	// AmbientOcclusionChosen is whether it came from the player.
	AmbientOcclusionChosen bool

	// Bloom is the HDR glow round bright light (render/bloom.go).
	Bloom = true

	// ContactShadows is the sun's screen-space contact shadow (render/ao.go).
	ContactShadows = true
	// ContactShadowsChosen is whether it came from the player.
	ContactShadowsChosen bool

	// SoftShadows is the cascades' penumbra widening with the distance from
	// the caster (PCSS, fragment.glsl).
	SoftShadows = true
)

// SetSpecularLighting changes SpecularLighting, as the player's choice.
func SetSpecularLighting(on bool) { setLightingBool(&SpecularLighting, on, nil) }

// SetReflections changes the reflection tier, as the player's choice.
func SetReflections(q ReflectionQuality) {
	q = min(max(q, ReflectionsOff), ReflectionsHigh)
	ReflectionsChosen = true
	if Reflections != q {
		Reflections = q
		GraphicsSettingsChanged = true
	}
}

// SetAmbientOcclusion changes AmbientOcclusion, as the player's choice.
func SetAmbientOcclusion(on bool) { setLightingBool(&AmbientOcclusion, on, &AmbientOcclusionChosen) }

// SetBloom changes Bloom, as the player's choice.
func SetBloom(on bool) { setLightingBool(&Bloom, on, nil) }

// SetContactShadows changes ContactShadows, as the player's choice.
func SetContactShadows(on bool) { setLightingBool(&ContactShadows, on, &ContactShadowsChosen) }

// SetSoftShadows changes SoftShadows, as the player's choice.
func SetSoftShadows(on bool) { setLightingBool(&SoftShadows, on, nil) }

func setLightingBool(v *bool, on bool, chosen *bool) {
	if chosen != nil {
		*chosen = true
	}
	if *v != on {
		*v = on
		GraphicsSettingsChanged = true
	}
}

// PickDefaultLighting chooses the tiers for a player who has not, from the
// GPU's name — PickDefaultGIQuality's rule: a discrete NVIDIA or AMD card
// takes the high reflection tier, AO and contact shadows; anything else (an
// integrated GPU, a software renderer) the sky-only reflections and neither
// screen-space pass. A choice already made is left alone.
func PickDefaultLighting(renderer string) {
	discrete := DiscreteGPU(renderer)
	if !ReflectionsChosen {
		if discrete {
			Reflections = ReflectionsHigh
		} else {
			Reflections = ReflectionsLow
		}
	}
	if !AmbientOcclusionChosen {
		AmbientOcclusion = discrete
	}
	if !ContactShadowsChosen {
		ContactShadows = discrete
	}
}

// ApplyLightingSpec sets the lighting settings from a comma-separated list of
// name=value pairs — "specular=off,reflections=high" — as chosen settings, for
// a harness run or a benchmark. A bare name means on.
func ApplyLightingSpec(spec string) error {
	for _, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, val, hasVal := strings.Cut(item, "=")
		name = strings.ToLower(strings.TrimSpace(name))
		val = strings.ToLower(strings.TrimSpace(val))
		if name == "reflections" {
			q, ok := ParseReflectionQuality(val)
			if !ok {
				return fmt.Errorf("reflections=%q: want off, low or high", val)
			}
			Reflections, ReflectionsChosen = q, true
			continue
		}
		on := true
		if hasVal {
			switch val {
			case "on", "true", "1", "yes":
			case "off", "false", "0", "no":
				on = false
			default:
				return fmt.Errorf("%s=%q: want on or off", name, val)
			}
		}
		switch name {
		case "specular":
			SpecularLighting = on
		case "ambient_occlusion", "ao":
			AmbientOcclusion, AmbientOcclusionChosen = on, true
		case "bloom":
			Bloom = on
		case "contact_shadows":
			ContactShadows, ContactShadowsChosen = on, true
		case "soft_shadows":
			SoftShadows = on
		default:
			return fmt.Errorf("no lighting setting %q", name)
		}
	}
	return nil
}
