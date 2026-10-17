package render

import (
	"os"
	"testing"

	"foundry/internal/gfx"
)

// withoutFrameLighting switches off, for the test, the lighting passes whose
// answer depends on the frames before (ao.go's per-frame turn, the
// reflections' captures, envmap.go): for a test that compares two frames of
// something else and wants them identical where that something is absent.
func withoutFrameLighting(t *testing.T) {
	t.Helper()
	ao, contact, refl, chosen := gfx.AmbientOcclusion, gfx.ContactShadows, gfx.Reflections, gfx.ReflectionsChosen
	gfx.AmbientOcclusion, gfx.ContactShadows, gfx.Reflections, gfx.ReflectionsChosen = false, false, gfx.ReflectionsOff, true
	t.Cleanup(func() {
		gfx.AmbientOcclusion, gfx.ContactShadows, gfx.Reflections, gfx.ReflectionsChosen = ao, contact, refl, chosen
	})
}

// TestMain runs the package's tests with a level that names no tone curve
// shading LDR, as every test here was written for: HDR by default
// (gfx.HDRByDefault) brings automatic exposure, which re-meters between
// two frames a test compares and brightens away the very difference it is
// looking for. A test about HDR asks for it with a level's Tonemap, which
// still applies.
//
// The lighting pass's other additions (gfx/lighting_settings.go) are off too:
// the screen-space occlusion turns its samples every frame, the reflections
// and highlights add light the tests' expected colours predate, and two
// frames a test compares would differ by them. A test about one of them
// switches it on (envTestSetup, for one).
func TestMain(m *testing.M) {
	gfx.HDRByDefault = false
	gfx.SpecularLighting = false
	gfx.Reflections, gfx.ReflectionsChosen = gfx.ReflectionsOff, true
	gfx.AmbientOcclusion, gfx.AmbientOcclusionChosen = false, true
	gfx.ContactShadows, gfx.ContactShadowsChosen = false, true
	gfx.SoftShadows = false
	gfx.Bloom = false
	os.Exit(m.Run())
}
