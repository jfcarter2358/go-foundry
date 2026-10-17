package world

// volume_reflection.go — reflection volumes (docs/lighting/PLAN.md, phase 2).
//
// Every lit surface reflects an environment (render/envmap.go): the sky's
// cube, brought down to the light the GI probes see in the reflected
// direction, so a corridor does not mirror a blue sky. A REFLECTION volume
// gives a region an environment of its own: a cube captured from the
// volume's centre — traced through the GI distance field, so it is the room
// as lit right now — and looked up through the volume's box (parallax-
// corrected), so a reflection slides across the floor as the eye moves, as a
// room's would.
//
// A field on Volume, as Ambient and Fog are: the shape, the gizmos, the
// `enabled` a script flips are all there already. Only the box's
// world-axis-aligned bounds are used as the parallax proxy, so a reflection
// volume is best left unrotated and sized to the room's walls.

import (
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// MaxReflectionVolumes is how many the shader carries at once, the nearest
// the camera winning. MUST match MAX_REFL_VOLUMES in scene_block.glsl.
const MaxReflectionVolumes = 8

// VolumeReflection is a volume's environment capture.
type VolumeReflection struct {
	// Enabled switches it off and keeps the numbers.
	Enabled bool
	// Intensity scales what the capture reflects.
	Intensity float32
	// Fade is how far in from the box's walls, in metres, the volume's
	// reflection takes over from what is outside it.
	Fade float32
}

// VolumeReflection defaults.
const (
	VolumeReflectionDefaultFade float32 = 1
)

// DefaultVolumeReflection is what ticking the box gives.
func DefaultVolumeReflection() VolumeReflection {
	return VolumeReflection{Enabled: true, Intensity: 1, Fade: VolumeReflectionDefaultFade}
}

// Clamp puts every field in range.
func (r *VolumeReflection) Clamp() {
	if r == nil {
		return
	}
	r.Intensity = clampF(r.Intensity, 0, 4)
	r.Fade = clampF(r.Fade, 0, 100)
}

// HasReflection reports whether the volume captures a reflection: switched
// on, the volume enabled, and a box to capture in.
func (v *Volume) HasReflection() bool {
	return v != nil && v.Reflection != nil && v.Reflection.Enabled && v.Enabled && v.HasBounds()
}

// ReflectionVolumes returns the volumes whose capture is in effect, nearest
// camPos first (by their boxes), at most MaxReflectionVolumes — with their
// world boxes.
func ReflectionVolumes(vols []Volume, lvl *Level, camPos mgl32.Vec3) []ReflectionBox {
	var out []ReflectionBox
	for i := range vols {
		v := &vols[i]
		if !v.HasReflection() {
			continue
		}
		lo, hi, ok := v.WorldAABB(lvl)
		if !ok {
			continue
		}
		out = append(out, ReflectionBox{Volume: v, Min: lo, Max: hi, Center: v.WorldPosition(lvl),
			dist: boxDistance(camPos, lo, hi)})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].dist < out[b].dist })
	if len(out) > MaxReflectionVolumes {
		out = out[:MaxReflectionVolumes]
	}
	return out
}

// ReflectionBox is a reflection volume in effect: its world box, the parallax
// proxy, and its centre, where it is captured from.
type ReflectionBox struct {
	Volume           *Volume
	Min, Max, Center mgl32.Vec3
	dist             float32
}

// ---------- YAML ----------

// RawVolumeReflection is the `reflection:` block.
type RawVolumeReflection struct {
	// Disabled keeps the numbers with the capture switched off.
	Disabled bool `yaml:"disabled,omitempty"`
	// Intensity scales what is reflected. Default 1.
	Intensity *float32 `yaml:"intensity,omitempty"`
	// Fade is how far in from the box's walls, in metres, its reflection
	// takes over. Default 1.
	Fade *float32 `yaml:"fade,omitempty"`
}

// VolumeReflectionFromRaw reads the block; nil stays nil.
func VolumeReflectionFromRaw(r *RawVolumeReflection) *VolumeReflection {
	if r == nil {
		return nil
	}
	v := DefaultVolumeReflection()
	v.Enabled = !r.Disabled
	if r.Intensity != nil {
		v.Intensity = *r.Intensity
	}
	if r.Fade != nil {
		v.Fade = *r.Fade
	}
	v.Clamp()
	return &v
}

// VolumeReflectionToRaw is the inverse, defaults left out.
func VolumeReflectionToRaw(v *VolumeReflection) *RawVolumeReflection {
	if v == nil {
		return nil
	}
	c := *v
	c.Clamp()
	r := &RawVolumeReflection{Disabled: !c.Enabled}
	if c.Intensity != 1 {
		r.Intensity = ptrF(c.Intensity)
	}
	if c.Fade != VolumeReflectionDefaultFade {
		r.Fade = ptrF(c.Fade)
	}
	return r
}
