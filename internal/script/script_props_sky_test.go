package script

// script_props_sky_test.go — a script changing the level's sky, and easing
// between two of them.
//
// The interesting case, as with the volume fog properties beside these, is not
// "does a write land" but what a write does to a level that has no sky block
// yet — most levels won't, since sky_test.go's own DefaultSky is what the
// editor's checkbox gives one for the first time too. ease_sky is proven here
// only up to arming the blend and handing world.SkyUpdate something to
// advance; the blend actually landing on screen is render's own GL test.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
	"foundry/internal/testenv"
	"foundry/internal/world"
)

func skyTestLevel(t *testing.T) *world.Level {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	return &world.Level{}
}

// Reading any sky_* property on a level with no sky is a fair question with a
// dull answer — the default it would get — not an error, and must not itself
// give the level a sky.
func TestLevelWithoutSkyReadsAsDefaults(t *testing.T) {
	lvl := skyTestLevel(t)

	if got := runScript(t, lvl, "", `
def main() -> bool
    return @level.prop.sky
end
`); got != flang.BoolVal(false) {
		t.Errorf("sky read %v on a level with none, want false", got)
	}
	if got := runScript(t, lvl, "", `
def main() -> atom
    return @level.prop.sky_kind
end
`); got != flang.AtomVal(world.SkyKindName(world.DefaultSky().Kind)) {
		t.Errorf("sky_kind read %v, want the default kind", got)
	}
	if got := runScript(t, lvl, "", `
def main() -> float
    return @level.prop.sky_intensity
end
`); got != flang.FloatVal(float64(world.DefaultSky().Intensity)) {
		t.Errorf("sky_intensity read %v, want the default", got)
	}
	if lvl.Sky != nil {
		t.Error("merely reading gave the level a sky")
	}
}

// Writing any sky_* property on a level with no sky block gives it one at the
// defaults first, the same shortcut volume fog gives a plain trigger.
func TestScriptCanGiveTheLevelASkyByWritingIntoIt(t *testing.T) {
	lvl := skyTestLevel(t)

	runScript(t, lvl, "", `
def main() -> null
    @level.prop.sky_tint = <0.5, 0.6, 0.7>
    @level.prop.sky_intensity = 2
    return null
end
`)

	if lvl.Sky == nil {
		t.Fatal("the script's write did not give the level any sky")
	}
	if lvl.Sky.Tint != (mgl32.Vec3{0.5, 0.6, 0.7}) {
		t.Errorf("tint is %v, want {0.5 0.6 0.7}", lvl.Sky.Tint)
	}
	if lvl.Sky.Intensity != 2 {
		t.Errorf("intensity is %v, want 2", lvl.Sky.Intensity)
	}
	// Untouched fields keep the defaults rather than reading as zero.
	if lvl.Sky.Zenith != world.DefaultSky().Zenith {
		t.Errorf("zenith is %v, want the default (untouched by this write)", lvl.Sky.Zenith)
	}
}

// The "sky" property is the on/off switch itself — existence, not a field —
// matching the editor's own checkbox: off clears the block entirely rather
// than leaving a switched-off one behind for the next save to carry around.
func TestScriptTurnsTheSkyOffAndBackOn(t *testing.T) {
	lvl := skyTestLevel(t)
	s := world.DefaultSky()
	s.Intensity = 3
	lvl.Sky = &s

	runScript(t, lvl, "", `
def main() -> null
    @level.prop.sky = false
    return null
end
`)
	if lvl.Sky != nil {
		t.Fatal("sky = false should clear the level's sky, not merely disable it")
	}

	runScript(t, lvl, "", `
def main() -> null
    @level.prop.sky = true
    return null
end
`)
	if lvl.Sky == nil {
		t.Fatal("sky = true on a level with none should add one at the defaults")
	}
	if got, want := lvl.Sky.Intensity, world.DefaultSky().Intensity; got != want {
		t.Errorf("re-adding the sky kept intensity %v, want the fresh default %v", got, want)
	}
}

// sky_cubemap is the editor's "fill from base name" button as a property: one
// write names all six faces and switches the kind, and reading it back
// reconstructs the base name from the six it finds.
func TestScriptSetsTheCubemapFromABaseName(t *testing.T) {
	lvl := skyTestLevel(t)

	runScript(t, lvl, "", `
def main() -> null
    @level.prop.sky_cubemap = "dusk"
    return null
end
`)

	if lvl.Sky == nil {
		t.Fatal("the write did not give the level a sky")
	}
	if lvl.Sky.Kind != world.SkyCubemap {
		t.Errorf("kind is %v, want cubemap", lvl.Sky.Kind)
	}
	want := world.Sky{}
	want.SetFacesFromBase("dusk")
	if lvl.Sky.Faces != want.Faces {
		t.Errorf("faces are %v, want %v", lvl.Sky.Faces, want.Faces)
	}

	if got := runScript(t, lvl, "", `
def main() -> str
    return @level.prop.sky_cubemap
end
`); got != flang.StrVal("dusk") {
		t.Errorf("sky_cubemap read back %v, want \"dusk\"", got)
	}
}

// ---------- ease_sky ----------

// ease_sky arms a blend that the very next sky_* write becomes the target of,
// and reports whether it actually armed one.
func TestEaseSkyArmsATransitionForTheNextWrite(t *testing.T) {
	lvl := skyTestLevel(t)

	if got := runScript(t, lvl, "", `
def main() -> bool
    return ease_sky(2)
end
`); got != flang.BoolVal(true) {
		t.Errorf("ease_sky(2) returned %v, want true", got)
	}
	if lvl.Sky == nil || lvl.Sky.TransitionFrom == nil {
		t.Fatal("ease_sky did not arm a transition")
	}
	if lvl.Sky.TransitionSeconds != 2 {
		t.Errorf("transition duration is %v, want 2", lvl.Sky.TransitionSeconds)
	}

	// The snapshot is the state from BEFORE the write that follows.
	before := *lvl.Sky.TransitionFrom
	runScript(t, lvl, "", `
def main() -> null
    @level.prop.sky_tint = <0.1, 0.2, 0.3>
    return null
end
`)
	if lvl.Sky.Tint == before.Tint {
		t.Fatal("setup: the write did not change the tint")
	}
	if lvl.Sky.TransitionFrom == nil || lvl.Sky.TransitionFrom.Tint != before.Tint {
		t.Error("the transition's snapshot changed along with the live write; it should have stayed fixed at the pre-write state")
	}
}

// A non-positive duration cancels rather than arms, and reports false.
func TestEaseSkyZeroCancelsAndReportsFalse(t *testing.T) {
	lvl := skyTestLevel(t)
	s := world.DefaultSky()
	s.BeginTransition(5)
	lvl.Sky = &s

	if got := runScript(t, lvl, "", `
def main() -> bool
    return ease_sky(0)
end
`); got != flang.BoolVal(false) {
		t.Errorf("ease_sky(0) returned %v, want false", got)
	}
	if lvl.Sky.TransitionFrom != nil {
		t.Error("ease_sky(0) should have cancelled the transition that was running")
	}
}

// ease_sky on a level with no sky yet gives it the defaults to ease away from
// — the same auto-vivify the sky_* properties give, so a script adding a sky
// for the first time can ease it in with no extra ceremony.
func TestEaseSkyOnALevelWithNoSkyCreatesOneToEaseFrom(t *testing.T) {
	lvl := skyTestLevel(t)

	runScript(t, lvl, "", `
def main() -> null
    ease_sky(1)
    return null
end
`)
	if lvl.Sky == nil {
		t.Fatal("ease_sky did not create a sky to ease away from")
	}
	if lvl.Sky.TransitionFrom == nil {
		t.Error("ease_sky did not arm a transition on the sky it created")
	}
}
