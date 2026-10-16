package world

import (
	"foundry/internal/gfx"
	"log"
	"math"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

const MaxDirLights = 4
const MaxPointLights = 8

// GlobalLight is a uniform ambient light applied equally to all surfaces.
type GlobalLight struct {
	Color      mgl32.Vec3
	Brightness float32
	// Disabled switches the light off without losing what it was set to, which
	// is what makes it answerable to a checkbox in the editor. Stored inverted
	// so the zero value is "on": every level authored before the flag existed
	// has one, and none of them meant it to be dark.
	Disabled bool

	// Wrap and Hemisphere shape how every light in the level falls across a
	// surface — see LightWrap and AmbientAt. Level-wide rather than per light
	// because they are the lighting MODEL, and a bake and a live surface have
	// to agree on it. A loaded level without them gets DefaultLightWrap and
	// DefaultAmbientHemisphere; zero is the hard, flat look.
	Wrap       float32
	Hemisphere float32

	// GI is the level's live global illumination (render/gi.go): bounce light
	// worked out every frame from the lights as they are, in place of the
	// flat ambient and the bake's own bounce. Whether it runs at all is the
	// player's (gfx.GIQuality); this is what the level asks of it when it does.
	GI GISettings

	// Bounces is how many times the light bake carries light on from one
	// surface to the next (compile/bounce.go): 0 bakes direct light only, 1
	// lets a sunlit floor light the wall beside it, 2 lets that wall light the
	// ceiling in turn. BounceScale multiplies what each bounce carries. Both
	// act on the bake alone — nothing lit live receives bounced light — so a
	// dev build, which lights everything live, never shows them. A loaded
	// level without them gets DefaultBounces and DefaultBounceScale; the zero
	// value (a level built in code) bakes no bounce at all.
	Bounces     int
	BounceScale float32
}

// GISettings is a level's live GI. The zero value is not what a loaded level
// gets: levelio fills DefaultGISettings first, as it does Wrap's default.
type GISettings struct {
	// Disabled turns GI off for this level whatever the player's setting:
	// the flat ambient and the bake's bounce, as before GI existed.
	Disabled bool
	// Intensity multiplies the bounce light GI finds. 1 is physical.
	Intensity float32
	// ProbeSpacing is how far apart the finest probes sit, in world units;
	// each coarser cascade doubles it. Finer catches thinner walls and
	// smaller rooms, at the cost of covering less ground.
	ProbeSpacing float32
	// AmbientFloor is how much of the flat ambient is kept under GI, 0 to 1:
	// a minimum, so a sealed room with no light in it is dim rather than
	// black.
	AmbientFloor float32
}

// DefaultGISettings is what a level that says nothing about GI gets.
func DefaultGISettings() GISettings {
	return GISettings{Intensity: 1, ProbeSpacing: 2}
}

// GIProbeSpacingMin and GIProbeSpacingMax bound GISettings.ProbeSpacing.
const (
	GIProbeSpacingMin = float32(0.5)
	GIProbeSpacingMax = float32(16)
)

// DefaultLightWrap and DefaultAmbientHemisphere are what a level that never set
// Wrap or Hemisphere gets (levelio's loader).
const (
	DefaultLightWrap         = float32(0.5)
	DefaultAmbientHemisphere = float32(0.5)
)

// DefaultBounces and DefaultBounceScale are what a loaded level that never set
// `bounce:` gets; MaxBounces is the most the bake will do however many are
// asked for, each one costing as much again as the first.
const (
	DefaultBounces     = 1
	DefaultBounceScale = float32(1)
	MaxBounces         = 4
)

// LightWrap is how much of a light a surface at cosine ndl to it takes:
// Lambert, wrapped. At Wrap 0 it is plain max(N·L, 0), falling to nothing
// exactly side-on — the hard terminator. Wrap w lets the light carry on round
// the curve until N·L = -w, easing to zero instead of stopping, which is what
// a real surface's bounce and roughness look like and what keeps a smoothed
// cylinder from having a sharp dark line down it. MUST stay identical to
// lightWrapTerm in light_model.glsl — a baked surface and a live one have to
// agree.
func (g GlobalLight) LightWrap(ndl float32) float32 {
	w := g.Wrap
	if w < 0 {
		w = 0
	}
	d := (ndl + w) / (1 + w)
	if d < 0 {
		return 0
	}
	return d
}

// AmbientAt is the ambient light a surface facing n receives: the global
// colour, brighter facing up at the sky and darker facing down at the ground,
// by Hemisphere (0 = the same from every side, 1 = from half to one and a half
// times). The average over every direction is the colour itself, so turning it
// up reshapes a level's ambient rather than brightening it. MUST stay
// identical to ambientAt in light_model.glsl.
func (g GlobalLight) AmbientAt(n mgl32.Vec3) mgl32.Vec3 {
	h := min(max(g.Hemisphere, 0), 1)
	return g.Color.Mul(g.EffectiveBrightness() * (1 + 0.5*h*n.Y()))
}

// EffectiveBrightness is the brightness actually uploaded — zero while the
// light is switched off.
//
// Every light kind switches off this way rather than by being left out of the
// uniform arrays. The arrays are indexed, and a shadow-casting directional
// light's cascade slots are allocated by that index (see RenderDirShadows), so
// compacting the list to skip one would slide every light after it onto another
// light's shadow map.
func (g GlobalLight) EffectiveBrightness() float32 {
	if g.Disabled {
		return 0
	}
	return g.Brightness
}

// DirectionalLight is a light with a fixed direction and infinite range (like the sun).
//
// Yaw/Pitch (degrees) are the authored rotation; Direction is always derived
// from them by SyncDirection. Editing the vector components directly used to
// let the light reach {0,0,0}, whose Normalize() is NaN — which poisons the
// direction uniform and the shadow matrix permanently. Angles cannot express a
// zero-length vector, so the light stays valid no matter what is typed.
type DirectionalLight struct {
	ID             string     // stable identity; how a script addresses this light
	ScriptData                // tags, attributes and the attached script
	Direction      mgl32.Vec3 // derived unit vector — set via SyncDirection, never edited directly
	Yaw            float32    // degrees about +Y; 0 = toward +X, 90 = toward +Z
	Pitch          float32    // degrees of elevation; -90 = straight down, +90 = straight up
	Color          mgl32.Vec3
	Brightness     float32
	CastShadows    bool
	ShadowStrength float32 // 0.0 = no shadow darkening, 1.0 = fully dark shadows
	Disabled       bool    // switched off; see GlobalLight.EffectiveBrightness
	Dynamic        bool    // lit live rather than baked; see the note on PointLight.Dynamic
	NoBake         bool    // static, but left out of the bake; see the note on PointLight.NoBake
	NoGI           bool    // lights directly but is not bounced by live GI; see PointLight.NoGI

	// BakeExclude is chunk IDs this light's bake skips: it still shines on
	// them live (fragment.glsl reads every enabled light regardless of this
	// list — it is compile.BakeLight, and only the bake, that consults it),
	// but its contribution is never frozen into their compiled surfaces.
	//
	// For a light that belongs where it is switched on but has no lasting
	// business being baked into somewhere else — the sun over an outdoor level
	// whose hangar chunk gets disabled for good once the ship has left it, say.
	// Baking the sun into the hangar's own walls would light it for nobody: the
	// chunk is either drawn live (while the hangar exists, lit correctly by
	// this same light's live contribution) or gone. Unlike NoBake, the light
	// itself keeps baking normally everywhere else — this trims one chunk's
	// share of it, not the whole bake.
	BakeExclude []string

	// Waypoints is the waypoint animation turning this light, or nil: it
	// shines the way each arrow points. Runtime-only; see waypoint_follow.go.
	Waypoints *WaypointPlayback
}

// EffectiveBrightness is zero while the light is switched off.
func (dl DirectionalLight) EffectiveBrightness() float32 {
	if dl.Disabled {
		return 0
	}
	return dl.Brightness
}

// CastsShadows reports whether this light should be given shadow-map layers.
// A switched-off light contributes no light, so rendering its cascades would
// be pure cost for a shadow nothing casts.
func (dl DirectionalLight) CastsShadows() bool { return dl.CastShadows && !dl.Disabled }

// Default sun angles for a newly added directional light. Deliberately not
// straight down (pitch -90), which flattens all shading and leaves yaw with no
// visible effect.
const (
	DefaultDirLightYaw   float32 = 45
	DefaultDirLightPitch float32 = -50
)

// SyncDirection recomputes Direction from Yaw/Pitch. Pitch is clamped to
// [-90, 90]; yaw is left unwrapped so dragging past ±180 doesn't jump.
func (dl *DirectionalLight) SyncDirection() {
	if dl.Pitch > 90 {
		dl.Pitch = 90
	} else if dl.Pitch < -90 {
		dl.Pitch = -90
	}
	dl.Direction = gfx.DirectionFromAngles(mgl32.DegToRad(dl.Yaw), mgl32.DegToRad(dl.Pitch))
}

// DirLightYawPitch derives editor angles (degrees) from a direction vector, for
// levels authored before the angles were stored. A zero-length or non-finite
// vector — including a NaN one saved by the old vector editor — falls back to
// the defaults rather than propagating the bad value.
func DirLightYawPitch(d mgl32.Vec3) (yaw, pitch float32) {
	for i := 0; i < 3; i++ {
		if math.IsNaN(float64(d[i])) || math.IsInf(float64(d[i]), 0) {
			return DefaultDirLightYaw, DefaultDirLightPitch
		}
	}
	if d.Len() < 1e-6 {
		return DefaultDirLightYaw, DefaultDirLightPitch
	}
	n := d.Normalize()
	y := n.Y()
	if y > 1 {
		y = 1
	} else if y < -1 {
		y = -1
	}
	pitch = mgl32.RadToDeg(float32(math.Asin(float64(y))))
	yaw = mgl32.RadToDeg(float32(math.Atan2(float64(n.Z()), float64(n.X()))))
	return yaw, pitch
}

// PointLight is a positional light with a quadratic radius-based falloff.
type PointLight struct {
	ID             string // stable identity; how a script addresses this light
	ScriptData            // tags, attributes and the attached script
	Position       mgl32.Vec3
	Color          mgl32.Vec3
	Brightness     float32
	Radius         float32
	CastShadows    bool
	ShadowStrength float32 // 0.0 = no shadow darkening, 1.0 = fully dark shadows
	Disabled       bool    // switched off; see GlobalLight.EffectiveBrightness

	// Dynamic marks a light that may change in game — moved, re-aimed, dimmed,
	// sent along a path. A static light (the zero value, and so every light
	// authored before the flag existed) is baked into a compiled level's static
	// geometry by the light bake and never summed per fragment on it again; a
	// dynamic one is left out of the bake and lit live on everything, the baked
	// surfaces included, with its shadow maps.
	//
	// It is also why a script may only change a dynamic light: a static light's
	// contribution to the world is frozen into the vertices at compile time, so
	// changing it would move the light on the props and not on the walls. The
	// refusal is made in every build, not only a compiled one, so a level that
	// runs in the editor runs the same once shipped. Same contract as
	// Brush.Dynamic.
	Dynamic bool

	// NoBake leaves a static light out of the light bake without making it
	// dynamic: it is lit live on everything, like a dynamic light, but a script
	// may do no more to it than switch it on and off. For a light that belongs
	// to geometry the compiler does not bake — a hangar that flies away, marked
	// Chunk.Moves — whose light would otherwise be baked onto whatever it
	// happens to shine on and stay there after the hangar has gone.
	//
	// Switching it is allowed where nothing else is because nothing of it is in
	// the vertices: off is off on the walls as well as the props.
	NoBake bool

	// NoGI leaves the light out of live GI (render/gi.go): it still lights
	// what it shines on, but nothing it lights hands any of it on. For a fill
	// light that is there to light one thing and should not brighten the room
	// around it. Live GI only — the bake's bounce does not read it.
	NoGI bool

	// Attach names what the light rides — a frame, an entity, or a bone inside
	// one of its models: a lantern in a hand, a headlight on a truck. Set,
	// Position is an offset in that carrier's space. An attached light always
	// moves, so it is always Dynamic; the loader and the editor both keep the
	// two in step, and the bake leaves an attached light out regardless.
	Attach Attach

	// BakeExclude is chunk IDs this light's bake skips; see
	// DirectionalLight.BakeExclude.
	BakeExclude []string

	// FromPrefab is runtime-only, never serialized: the prefab instance whose
	// file put this here (prefab.go). Empty for one the level file authored.
	// It is what LevelToRaw checks before writing — a level that saved a copy
	// of a prefab's content would freeze it and stop tracking the file.
	FromPrefab string

	// Waypoints is the waypoint animation carrying this light, or nil.
	// Runtime-only; see waypoint_follow.go.
	Waypoints *WaypointPlayback
}

// LitLive reports whether the light is summed per fragment on compiled geometry
// rather than baked into it: a dynamic light, one left out of the bake, and an
// attached one (which is always dynamic).
func (pl PointLight) LitLive() bool { return pl.Dynamic || pl.NoBake || !pl.Attach.IsZero() }

// LitLive is PointLight.LitLive for a directional light.
func (dl DirectionalLight) LitLive() bool { return dl.Dynamic || dl.NoBake }

// EffectiveBrightness is zero while the light is switched off.
func (pl PointLight) EffectiveBrightness() float32 {
	if pl.Disabled {
		return 0
	}
	return pl.Brightness
}

// CastsShadows reports whether this light should be given a shadow cubemap.
func (pl PointLight) CastsShadows() bool { return pl.CastShadows && !pl.Disabled }

// WorldPosition resolves the light's position through its attachment. This is
// what the shader and the shadow cubemaps are handed (SceneLights).
func (pl *PointLight) WorldPosition(lvl *Level) mgl32.Vec3 {
	if pl.Attach.IsZero() {
		return pl.Position
	}
	return mulPoint(pl.Attach.Matrix(lvl), pl.Position)
}

// SetWorldPosition puts the light at world point p, storing it in its carrier's
// space when it is attached.
func (pl *PointLight) SetWorldPosition(lvl *Level, p mgl32.Vec3) {
	pl.Position = fromAttached(lvl, pl.Attach, p)
}

// HasAttachedLights reports whether any point light rides something. Their
// carriers can move every frame, so a level with one uploads its lights every
// frame; a level without keeps uploading only on change.
func HasAttachedLights(lvl *Level) bool {
	if lvl == nil {
		return false
	}
	for i := range lvl.Lights.Points {
		if !lvl.Lights.Points[i].Attach.IsZero() {
			return true
		}
	}
	return false
}

// LightSet holds all lights in the scene.
type LightSet struct {
	Global      GlobalLight
	Directional []DirectionalLight
	Points      []PointLight
}

// UniformLoc fetches a uniform location and logs a warning if it is missing
// (-1), which means the name does not exist in the linked shader program.
// [SCENARIO 5] Storing the name in a local variable also ensures the backing
// array is not collected before gl.Str returns the pointer to the driver.
func UniformLoc(name string) int32 {
	return ProgUniformLoc(gfx.Program, name)
}

// ProgUniformLoc fetches a uniform location from an arbitrary program. Unlike
// UniformLoc it does not log misses — secondary programs (e.g. the detail
// tessellation program) legitimately omit some uniforms.
func ProgUniformLoc(prog uint32, name string) int32 {
	return gfx.UniformLoc(prog, name)
}

// UploadLights sends all light uniforms to the main shader program.
func UploadLights(ls LightSet) { UploadLightsTo(gfx.Program, ls, true) }

// UploadLightsTo hands the lights to every program that reads the scene block
// (gfx/scene_block.go) — or, for a program pointed at another block (the
// material preview's), to that one. prog must already be linked; this binds it
// as the active program, as it always has, for callers that draw through it
// straight after. verbose enables the informational log (used only for the
// main program's initial upload).
//
// Once these were prog's own uniforms and every lit program needed its own
// call every frame. They are one buffer now, so the call for the second
// program in a frame finds nothing changed and costs a comparison.
func UploadLightsTo(prog uint32, ls LightSet, verbose bool) {
	// No program, nothing to upload to: a headless load, an undo restore in a
	// test, where a GL call with no context current is a crash in cgo rather
	// than a no-op.
	if prog == 0 {
		return
	}
	gl.UseProgram(prog)
	if verbose {
		log.Printf("uploading lights: global=1, directional=%d, point=%d",
			len(ls.Directional), len(ls.Points))
	}
	s := gfx.SceneFor(prog)
	PackLights(&s.Block, ls)
	s.Flush()
	if verbose {
		log.Printf("light uniforms uploaded")
	}
}

// PackLights writes ls into the light members of the scene block b: what the
// shader reads under globalLight*, lightWrap, ambientHemisphere, dirLight* and
// pointLight* (all but the shadow switches, which are the shadow passes'). No
// GL. Slots past the light counts are zeroed: the shader never reads them, and
// a block that holds nothing stale compares equal frame to frame.
func PackLights(b *gfx.SceneBlock, ls LightSet) {
	b.GlobalLightColor = ls.Global.Color
	b.GlobalLightBrightness = ls.Global.EffectiveBrightness()
	b.LightWrap = max(ls.Global.Wrap, 0)
	b.AmbientHemisphere = min(max(ls.Global.Hemisphere, 0), 1)
	// The player's specular setting (gfx/lighting_settings.go): the GGX
	// highlight every lit pass adds (specular.glsl).
	b.SpecularOn = boolInt(gfx.SpecularLighting)
	// And soft shadows (vsm.glsl's PCSS): the sun's disc, half a degree
	// across as the real one is (a touch more, so a shadow's widening reads).
	b.SoftShadowsOn = boolInt(gfx.SoftShadows)
	b.SunAngularRadius = SunAngularRadius

	numDir := min(len(ls.Directional), MaxDirLights)
	b.NumDirLights = int32(numDir)
	for i := 0; i < MaxDirLights; i++ {
		if i >= numDir {
			b.DirLightDir[i], b.DirLightColor[i], b.DirLightBrightness[i], b.DirLightDynamic[i] = [4]float32{}, [4]float32{}, [4]float32{}, [4]int32{}
			continue
		}
		d := &ls.Directional[i]
		b.DirLightDir[i] = vec3Std140(d.Direction)
		b.DirLightColor[i] = vec3Std140(d.Color)
		b.DirLightBrightness[i] = [4]float32{d.EffectiveBrightness()}
		b.DirLightDynamic[i] = [4]int32{boolInt(d.LitLive())}
	}

	numPoint := min(len(ls.Points), MaxPointLights)
	b.NumPointLights = int32(numPoint)
	for i := 0; i < MaxPointLights; i++ {
		if i >= numPoint {
			b.PointLightPos[i], b.PointLightColor[i] = [4]float32{}, [4]float32{}
			b.PointLightBrightness[i], b.PointLightRadius[i], b.PointLightDynamic[i] = [4]float32{}, [4]float32{}, [4]int32{}
			continue
		}
		p := &ls.Points[i]
		b.PointLightPos[i] = vec3Std140(p.Position)
		b.PointLightColor[i] = vec3Std140(p.Color)
		b.PointLightBrightness[i] = [4]float32{p.EffectiveBrightness()}
		b.PointLightRadius[i] = [4]float32{p.Radius}
		b.PointLightDynamic[i] = [4]int32{boolInt(p.LitLive())}
	}
}

// SunAngularRadius is the sun's angular radius, in radians, its soft shadows
// widen by (vsm.glsl): 0.35°, a little over the real sun's 0.27°.
const SunAngularRadius = 0.0061

// vec3Std140 is v as one std140 array element: a vec3 padded to 16 bytes.
func vec3Std140(v mgl32.Vec3) [4]float32 { return [4]float32{v[0], v[1], v[2], 0} }

// boolInt is a bool as the 0/1 a GLSL int uniform takes.
func boolInt(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// BakedSun is the directional light whose baked share a compiled surface
// carries per vertex, so the renderer can take it back out where something
// the bake never saw — an entity, a chunk switched on later — stands in its
// way: the first shadow-casting directional light that is baked at all. -1
// when there is none. The compiler (compile/light.go) and the renderer
// (render.UploadShadowUniformsTo) both ask here, which is what keeps the
// share they agree on the same light.
func (ls LightSet) BakedSun() int {
	for i := range ls.Directional {
		if i >= MaxDirLights {
			break
		}
		dl := &ls.Directional[i]
		if dl.EffectiveBrightness() > 0 && dl.CastsShadows() && !dl.LitLive() && dl.Direction.Len() > 1e-6 {
			return i
		}
	}
	return -1
}
