package main

import (
	"fmt"
	"log"
	"math"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

const MaxDirLights   = 4
const MaxPointLights = 8

// GlobalLight is a uniform ambient light applied equally to all surfaces.
type GlobalLight struct {
	Color      mgl32.Vec3
	Brightness float32
}

// DirectionalLight is a light with a fixed direction and infinite range (like the sun).
//
// Yaw/Pitch (degrees) are the authored rotation; Direction is always derived
// from them by SyncDirection. Editing the vector components directly used to
// let the light reach {0,0,0}, whose Normalize() is NaN — which poisons the
// direction uniform and the shadow matrix permanently. Angles cannot express a
// zero-length vector, so the light stays valid no matter what is typed.
type DirectionalLight struct {
	Direction      mgl32.Vec3 // derived unit vector — set via SyncDirection, never edited directly
	Yaw            float32    // degrees about +Y; 0 = toward +X, 90 = toward +Z
	Pitch          float32    // degrees of elevation; -90 = straight down, +90 = straight up
	Color          mgl32.Vec3
	Brightness     float32
	CastShadows    bool
	ShadowStrength float32 // 0.0 = no shadow darkening, 1.0 = fully dark shadows
}

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
	dl.Direction = DirectionFromAngles(mgl32.DegToRad(dl.Yaw), mgl32.DegToRad(dl.Pitch))
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
	Position       mgl32.Vec3
	Color          mgl32.Vec3
	Brightness     float32
	Radius         float32
	CastShadows    bool
	ShadowStrength float32 // 0.0 = no shadow darkening, 1.0 = fully dark shadows
}

// LightSet holds all lights in the scene.
type LightSet struct {
	Global      GlobalLight
	Directional []DirectionalLight
	Points      []PointLight
}

// uniformLoc fetches a uniform location and logs a warning if it is missing
// (-1), which means the name does not exist in the linked shader program.
// [SCENARIO 5] Storing the name in a local variable also ensures the backing
// array is not collected before gl.Str returns the pointer to the driver.
func uniformLoc(name string) int32 {
	return progUniformLoc(Program, name)
}

// progUniformLoc fetches a uniform location from an arbitrary program. Unlike
// uniformLoc it does not log misses — secondary programs (e.g. the detail
// tessellation program) legitimately omit some uniforms.
func progUniformLoc(prog uint32, name string) int32 {
	return gl.GetUniformLocation(prog, gl.Str(name))
}

// UploadLights sends all light uniforms to the main shader program.
func UploadLights(ls LightSet) { UploadLightsTo(Program, ls, true) }

// UploadLightsTo sends all light uniforms to prog. prog must already be linked;
// this binds it as the active program. verbose enables the informational log
// (used only for the main program's initial upload).
func UploadLightsTo(prog uint32, ls LightSet, verbose bool) {
	gl.UseProgram(prog)
	uniformLoc := func(name string) int32 { return progUniformLoc(prog, name) }
	if verbose {
		log.Printf("uploading lights: global=1, directional=%d, point=%d",
			len(ls.Directional), len(ls.Points))
	}

	gl.Uniform3f(uniformLoc("globalLightColor\x00"),
		ls.Global.Color[0], ls.Global.Color[1], ls.Global.Color[2])
	gl.Uniform1f(uniformLoc("globalLightBrightness\x00"), ls.Global.Brightness)

	numDir := len(ls.Directional)
	if numDir > MaxDirLights {
		numDir = MaxDirLights
	}
	gl.Uniform1i(uniformLoc("numDirLights\x00"), int32(numDir))
	for i := 0; i < numDir; i++ {
		d := ls.Directional[i]
		// [SCENARIO 5] Keep Sprintf result in a named variable so the string
		// backing array stays alive for the duration of the gl call.
		dirName := fmt.Sprintf("dirLightDir[%d]\x00", i)
		gl.Uniform3f(uniformLoc(dirName), d.Direction[0], d.Direction[1], d.Direction[2])
		colorName := fmt.Sprintf("dirLightColor[%d]\x00", i)
		gl.Uniform3f(uniformLoc(colorName), d.Color[0], d.Color[1], d.Color[2])
		brightnessName := fmt.Sprintf("dirLightBrightness[%d]\x00", i)
		gl.Uniform1f(uniformLoc(brightnessName), d.Brightness)
	}

	numPoint := len(ls.Points)
	if numPoint > MaxPointLights {
		numPoint = MaxPointLights
	}
	gl.Uniform1i(uniformLoc("numPointLights\x00"), int32(numPoint))
	for i := 0; i < numPoint; i++ {
		p := ls.Points[i]
		posName := fmt.Sprintf("pointLightPos[%d]\x00", i)
		gl.Uniform3f(uniformLoc(posName), p.Position[0], p.Position[1], p.Position[2])
		colorName := fmt.Sprintf("pointLightColor[%d]\x00", i)
		gl.Uniform3f(uniformLoc(colorName), p.Color[0], p.Color[1], p.Color[2])
		brightnessName := fmt.Sprintf("pointLightBrightness[%d]\x00", i)
		gl.Uniform1f(uniformLoc(brightnessName), p.Brightness)
		radiusName := fmt.Sprintf("pointLightRadius[%d]\x00", i)
		gl.Uniform1f(uniformLoc(radiusName), p.Radius)
	}

	if verbose {
		log.Printf("light uniforms uploaded")
	}
}
