package render

// water_interact.go — water that answers what happens in it
// (docs/terrain-paint/PHASE_W5.md, items 2 and 4).
//
// RIPPLES. The simulation pushes a world.WaterRipple for every disturbance
// of a surface (sim/water_wade.go's steps, wakes and splashes, the hitscan's
// shots into water). Each frame drains them into a short list of live rings
// stamped with the frame clock; the water shader is handed the nearest
// waterMaxRipples to the camera as a uniform array (centre X/Z, birth time,
// strength and reach packed in one float) and draws each as a damped wave
// packet spreading from its centre, added to the ripple slopes of the
// surface normal (water.glsl waterInteractSlope). Rain is not a list: the
// level's rain_ripples (0..1) rides in waterRippleInfo.y and the shader
// draws a procedural field of drop rings over every surface. No texture and
// no sampler: the water draws through the world program, which has none to
// spare (PHASE_W1.md), so no texture unit is claimed.
//
// UNDER WATER. With the camera below a water surface (world.WaterAt at the
// eye, with a small hysteresis band so a wave passing the lens does not
// flip it every frame):
//
//   - the water FILLS with its own murk: a fog volume (a box from the
//     surface down, round the camera) is added to the frame's fog volumes,
//     its density the mean extinction of the material's absorption
//     (−absorption · ln shallow_color), its colour the scatter colour lit
//     by the level's sun and sky. A volume rather than the distance fog
//     because a volume fogs only the part of each view ray that is IN the
//     water: a tree on the bank seen up through the surface is fogged by the
//     water between, not by its whole distance. Every program that fogs
//     reads it from the scene block, so terrain, grass, foliage, sprites and
//     the sky sink into the same murk; frameVolumes hands it to each upload
//     site in place of the level's list (the level is never written);
//   - CAUSTICS come back on whatever lies under the surface: the bed-side
//     path water_caustics.go retired for frames above water lists this one
//     body again, so fragment.glsl's waterCaustics lights the bed, rocks and
//     anything else below the surface around the camera. The visibility
//     buffer's material variants fold that path out when the list is empty;
//     warmMaterialVariants builds the caustic variants too for a level with
//     water, so the first dive does not stall on a program build;
//   - the surface seen from BELOW is water.glsl's (Snell's window, total
//     internal reflection outside it);
//   - the world's SOUND is muffled: audio.SetMuffle low-passes the effects
//     bus (audio/audio_muffle.go).
//
// The state is the render thread's own; the level is only read.

import (
	"log"
	"math"

	"foundry/internal/audio"
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// waterMaxRipples is WATER_MAX_RIPPLES in water.glsl.
const waterMaxRipples = 16

// waterRipple is one live ring.
type waterRipple struct {
	at       mgl32.Vec3
	born     float64
	strength float32
	reach    float32
}

// life is how long the ring shows, seconds: until its packet has run its
// reach and faded (water.glsl's waterRingSlope: speed 0.5 + 0.25·reach m/s).
func (r *waterRipple) life() float64 {
	return float64(r.reach/(0.5+0.25*r.reach)) + 0.8
}

var waterInteract struct {
	ripples []waterRipple
	drain   []world.WaterRipple
	// The frame's upload: centres/birth/packed strength+reach, the count,
	// the rain.
	packed [waterMaxRipples * 4]float32
	count  int32
	rain   float32
	clock  float64

	// Under water: the hysteresis state, the body, and the frame's fog
	// volumes (the water's first, then the level's).
	under   bool
	sample  world.WaterSample
	vols    []world.Volume
	lastLvl *world.Level
	// near: the eye is within waterNearReach of a water surface, so the
	// caustic material variants should be ready (nanite_vis_variants.go).
	near bool
}

// waterNearReach is how close to a surface (metres above it) the eye is
// "near" water.
const waterNearReach = 8

// WaterUnderwater reports whether the last frame's camera was under water
// (for the tests and the debug overlay).
func WaterUnderwater() bool { return waterInteract.under }

// WaterLogTransitions logs each time the camera goes under or comes out of
// the water (FOUNDRY_WATER_LOG=1), for a headless run's log.
var WaterLogTransitions = gfx.EnvFlag("FOUNDRY_WATER_LOG", false)

// waterUnderBand is the hysteresis half-band, metres: under once the eye is
// this far below the still surface, back above once it is this far over it.
const waterUnderBand = 0.04

// beginWaterFrame updates the ripples and the under-water state for a frame
// drawn at now from the camera's position (the file comment).
func beginWaterFrame(lvl *world.Level, now float64) {
	wi := &waterInteract
	if wi.lastLvl != lvl {
		wi.lastLvl = lvl
		wi.ripples = wi.ripples[:0]
		wi.under = false
	}
	wi.clock = now
	eye := gfx.Cam.Position

	// --- ripples ---
	wi.drain = world.DrainWaterRipples(wi.drain[:0])
	for _, r := range wi.drain {
		reach := r.Reach
		if reach <= 0 {
			reach = 0.8 + 2.2*r.Strength
		}
		wi.ripples = append(wi.ripples, waterRipple{at: r.At, born: now, strength: r.Strength, reach: reach})
	}
	kept := wi.ripples[:0]
	for i := range wi.ripples {
		r := &wi.ripples[i]
		if age := now - r.born; age >= 0 && age < r.life() {
			kept = append(kept, *r)
		}
	}
	wi.ripples = kept
	// The nearest to the eye, when there are more than the shader takes: a
	// partial selection sort over a list of a few dozen.
	n := min(len(wi.ripples), waterMaxRipples)
	for i := 0; i < n; i++ {
		best := i
		bd := wi.ripples[i].at.Sub(eye).LenSqr()
		for j := i + 1; j < len(wi.ripples); j++ {
			if d := wi.ripples[j].at.Sub(eye).LenSqr(); d < bd {
				best, bd = j, d
			}
		}
		wi.ripples[i], wi.ripples[best] = wi.ripples[best], wi.ripples[i]
	}
	for i := 0; i < n; i++ {
		r := &wi.ripples[i]
		wi.packed[4*i] = r.at.X()
		wi.packed[4*i+1] = r.at.Z()
		// Relative to a clock the shader shares, as a float32 seconds
		// value: the frame clock is a float64, the shader's `time` a float.
		wi.packed[4*i+2] = float32(r.born)
		wi.packed[4*i+3] = packRipple(r.strength, r.reach)
	}
	wi.count = int32(n)
	wi.rain = 0
	if lvl != nil {
		wi.rain = world.ClampRainRipples(lvl.RainRipples)
	}

	// --- under water ---
	ws := world.WaterAt(lvl, eye)
	band := float32(waterUnderBand)
	if ws.Found {
		band += min(ws.Water.WaveAmplitude, 0.3)
	}
	wi.near = ws.Found && ws.Depth > -waterNearReach
	was := wi.under
	if wi.under {
		wi.under = ws.Found && ws.Depth > -band && ws.Depth <= world.WaterQueryMaxDepth
	} else {
		wi.under = ws.Found && ws.In && ws.Depth > band
	}
	if wi.under != was && WaterLogTransitions {
		log.Printf("water: camera %s (%s, %.2f m under the surface at %.2f)",
			map[bool]string{true: "under water", false: "out of the water"}[wi.under], ws.Material, ws.Depth, ws.Surface)
	}
	if wi.under {
		wi.sample = ws
		wi.vols = append(wi.vols[:0], underwaterVolume(lvl, ws, eye))
		if lvl != nil {
			wi.vols = append(wi.vols, lvl.Volumes...)
		}
		audio.SetMuffle(1)
	} else {
		audio.SetMuffle(0)
	}
}

// packRipple packs a ring's strength (0..1, to 1/1000) and reach (metres,
// to 1/100) into one float, exactly: floor(reach·100) + strength·0.999.
func packRipple(strength, reach float32) float32 {
	s := min(max(strength, 0), 1) * 0.999
	return float32(math.Floor(float64(min(max(reach, 0.1), 80)*100))) + s
}

// frameFog is the distance fog this frame is drawn in: the level's (under
// water too: the water's murk is a volume, frameVolumes).
func frameFog(lvl *world.Level) *world.Fog {
	if lvl == nil {
		return nil
	}
	return lvl.Fog
}

// frameVolumes is the volumes this frame's fog is packed from: under water
// the water's own murk first, then the level's (the file comment).
func frameVolumes(lvl *world.Level) []world.Volume {
	if waterInteract.under && waterInteract.lastLvl == lvl && len(waterInteract.vols) > 0 {
		return waterInteract.vols
	}
	if lvl == nil {
		return nil
	}
	return lvl.Volumes
}

// underwaterReach and underwaterDepth size the murk's box round the eye,
// metres: wider than any fog lets the eye see, and deep enough for any bed.
const (
	underwaterReach = 400
	underwaterDepth = 80
)

// underwaterVolume is the fog volume of being inside water ws, round eye.
func underwaterVolume(lvl *world.Level, ws world.WaterSample, eye mgl32.Vec3) world.Volume {
	w := ws.Water
	abs := w.Absorption
	if abs <= 0 {
		abs = 0.6
	}
	sh := w.ShallowColor
	if sh == (mgl32.Vec3{}) {
		sh = mgl32.Vec3{0.55, 0.8, 0.82}
	}
	sc := w.ScatterColor
	if sc == (mgl32.Vec3{}) {
		sc = mgl32.Vec3{0.025, 0.11, 0.14}
	}
	// Extinction per metre, per channel: T = shallow^(d·absorption).
	ext := func(c float32) float32 {
		return -abs * float32(math.Log(float64(min(max(c, 0.01), 0.999))))
	}
	density := (ext(sh.X()) + ext(sh.Y()) + ext(sh.Z())) / 3
	// Even glass-clear water hides the far side of a lake.
	density = min(max(density, 0.06), 3)

	// The light the water's scatter colour is lit by: the sun through the
	// surface (more the higher it stands) and the sky's ambient.
	light := float32(0.35)
	if lvl != nil {
		g := lvl.Lights.Global
		if !g.Disabled {
			light = g.Brightness * (g.Color.X() + g.Color.Y() + g.Color.Z()) / 3
		}
		for i := range lvl.Lights.Directional {
			d := &lvl.Lights.Directional[i]
			if d.Disabled {
				continue
			}
			up := max(-d.Direction.Y(), 0)
			light += d.Brightness * up * (d.Color.X() + d.Color.Y() + d.Color.Z()) / 3
		}
	}
	// Deeper is darker: the light reaching the eye has come down through
	// the water above it. Gently (fixes after W5): the extinction is mostly
	// scattering, which turns the light rather than taking it, so the water
	// round a diver stays lit by the surface's glow. At half the extinction
	// a river two metres down (density ~1.6) drew its murk near black.
	depth := max(ws.Depth, 0)
	light *= float32(max(math.Exp(float64(-depth*density*0.12)), 0.35))
	// The scatter colours are small (the shader multiplies them by the lit
	// light at the surface, then tone maps): scaled so the murk reads as
	// the water's own colour, not black.
	col := sc.Mul(light * 2.2)
	top := ws.Surface
	return world.Volume{
		ID:       "\x00underwater",
		Position: mgl32.Vec3{eye.X(), top - underwaterDepth/2, eye.Z()},
		Shape:    world.VolumeBox,
		HalfX:    underwaterReach, HalfY: underwaterDepth / 2, HalfZ: underwaterReach,
		Enabled: true,
		Fog:     &world.VolumeFog{Enabled: true, Color: col, Density: density, MaxOpacity: 1},
	}
}

// uploadWaterInteract sends prog the ripples and the rain.
func uploadWaterInteract(prog uint32) {
	wi := &waterInteract
	loc := func(name string) int32 { return gfx.UniformLoc(prog, name+"\x00") }
	if l := loc("waterRipples"); l >= 0 && wi.count > 0 {
		gl.Uniform4fv(l, wi.count, &wi.packed[0])
	}
	// z: the camera is under water (water.glsl draws a surface from below
	// only then; fixes after W5).
	under := float32(0)
	if wi.under {
		under = 1
	}
	gl.Uniform4f(loc("waterRippleInfo"), float32(wi.count), wi.rain, under, 0)
}
