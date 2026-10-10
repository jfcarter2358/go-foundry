package world

// effect_test.go — the explosion's timeline, its flash, and its level-file
// form. All CPU-side: the renderer and the flash both read ExplosionState, so
// what it answers is most of what an explosion looks like.

import (
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func TestExplosionGrowsCoolsAndFades(t *testing.T) {
	x := DefaultExplosion()
	at := mgl32.Vec3{1, 2, 3}

	var prev ExplosionState
	for i, age := range []float32{0, 0.1, 0.3, 0.6, 0.95} {
		s, ok := x.StateAt(age, at)
		if !ok {
			t.Fatalf("age %v is inside the run but reported not live", age)
		}
		if s.Radius <= 0 || s.Radius > x.Radius+1e-4 {
			t.Errorf("age %v: radius %v outside (0, %v]", age, s.Radius, x.Radius)
		}
		if i > 0 {
			// Radiating outward: it only ever grows, and only ever cools.
			if s.Radius <= prev.Radius {
				t.Errorf("age %v: radius %v did not grow past %v", age, s.Radius, prev.Radius)
			}
			if s.Heat > prev.Heat {
				t.Errorf("age %v: heat %v rose from %v", age, s.Heat, prev.Heat)
			}
			if s.Center.Y() < prev.Center.Y() {
				t.Errorf("age %v: a positive rise sank the ball", age)
			}
		}
		prev = s
	}

	early, _ := x.StateAt(0.05, at)
	late, _ := x.StateAt(0.95, at)
	if early.Heat < 0.5 || late.Heat > 0.05 {
		t.Errorf("heat %v early and %v late; it should start hot and end as smoke", early.Heat, late.Heat)
	}
	if late.Fade > 0.1 {
		t.Errorf("the smoke is still at %v of its density at the very end", late.Fade)
	}
	// The fireball reaches most of its size quickly: a blast, not a balloon.
	if s, _ := x.StateAt(0.3, at); s.Radius < 0.8*x.Radius {
		t.Errorf("a third of the way through it is only %v of %v across", s.Radius, x.Radius)
	}
	for _, age := range []float32{-0.01, 1, 1.5} {
		if _, ok := x.StateAt(age, at); ok {
			t.Errorf("age %v is outside the run but reported live", age)
		}
	}
}

func TestExplosionFlashPeaksEarlyAndDies(t *testing.T) {
	x := DefaultExplosion()
	peak, _ := x.StateAt(0.02, mgl32.Vec3{})
	mid, _ := x.StateAt(0.5, mgl32.Vec3{})
	if peak.Flash < 0.8 {
		t.Errorf("the flash is %v just after detonation; it should be near full", peak.Flash)
	}
	if mid.Flash > 0.1 {
		t.Errorf("the flash is still %v halfway through; it should be gone before the smoke", mid.Flash)
	}
}

func TestEffectLoopsAndStops(t *testing.T) {
	e := NewEffect("boom", EffectExplosion, mgl32.Vec3{})
	e.Explosion.Duration = 1
	e.LoopDelay = 0.5

	if !e.Live() {
		t.Fatal("a freshly placed effect is not live")
	}
	if e.Step(1.1) {
		t.Error("a looping effect reported finishing")
	}
	if e.Live() {
		t.Errorf("at time %v it should be waiting out its loop delay", e.Time)
	}
	e.Step(0.5)
	if !e.Live() {
		t.Errorf("at time %v it should have gone off again", e.Time)
	}

	e.Loop = false
	e.Time = 0.9
	if !e.Step(0.2) {
		t.Error("a non-looping effect did not report finishing")
	}
	if e.Playing || e.Live() {
		t.Error("a finished non-looping effect is still running")
	}
	e.Detonate()
	if !e.Live() || e.Time != 0 {
		t.Error("Detonate did not set it off from the start")
	}

	// A hitch longer than a whole cycle must not leave it behind and firing.
	e.Loop = true
	e.Step(10)
	if e.Time > 0 {
		t.Errorf("after a long hitch the clock sits at %v, past the start of the next run", e.Time)
	}
}

func TestEffectUpdateReapsOneShotsAndReportsLights(t *testing.T) {
	lvl := &Level{}
	keep := NewEffect("placed", EffectExplosion, mgl32.Vec3{})
	keep.Loop = false
	shot := NewEffect("shot", EffectExplosion, mgl32.Vec3{})
	shot.Loop, shot.OneShot = false, true
	lvl.Effects = []Effect{keep, shot}
	effectLightsWereLit = false

	if !EffectUpdate(lvl, 0.01, true) {
		t.Error("two flashes going off did not ask for a light upload")
	}
	// The frame the last flash goes out still uploads, to clear it.
	if !EffectUpdate(lvl, 100, true) {
		t.Error("the frame the flashes went out did not ask for the upload that clears them")
	}
	if len(lvl.Effects) != 1 || lvl.Effects[0].ID != "placed" {
		t.Fatalf("after the run the level holds %d effects; the one-shot should be gone and the placement kept", len(lvl.Effects))
	}
	if EffectUpdate(lvl, 0.01, true) {
		t.Error("with nothing lit for two frames it still asks for uploads every frame")
	}

	// A pause freezes the clock.
	lvl.Effects[0].Detonate()
	EffectUpdate(lvl, 5, false)
	if lvl.Effects[0].Time != 0 {
		t.Errorf("a paused update advanced the clock to %v", lvl.Effects[0].Time)
	}
}

func TestSceneLightsKeepsLevelLightsFirst(t *testing.T) {
	lvl := &Level{}
	lvl.Lights.Points = []PointLight{{ID: "lamp", CastShadows: true, Radius: 5, Brightness: 1}}
	lvl.Effects = []Effect{NewEffect("boom", EffectExplosion, mgl32.Vec3{0, 3, 0})}
	lvl.Effects[0].Time = 0.03 // just after detonation: the flash is up

	ls := SceneLights(lvl)
	if len(ls.Points) != 2 {
		t.Fatalf("scene has %d point lights, want the lamp and the flash", len(ls.Points))
	}
	if ls.Points[0].ID != "lamp" {
		t.Error("the flash was slotted ahead of the level's lamp, which would hand it the lamp's shadow map")
	}
	flash := ls.Points[1]
	if !flash.Dynamic || flash.CastShadows || flash.Brightness <= 0 {
		t.Errorf("flash light %+v: want dynamic, shadowless and lit", flash)
	}
	if len(lvl.Lights.Points) != 1 {
		t.Error("SceneLights appended into the level's own light list")
	}

	// A level already at the limit keeps every slot for its own lights.
	lvl.Lights.Points = make([]PointLight, MaxPointLights)
	if n := len(SceneLights(lvl).Points); n != MaxPointLights {
		t.Errorf("a full level's scene holds %d point lights", n)
	}

	lvl.Lights.Points = nil
	lvl.Effects[0].Enabled = false
	if n := len(SceneLights(lvl).Points); n != 0 {
		t.Errorf("a disabled explosion still lights the scene (%d lights)", n)
	}
}

func TestEffectRoundTripsEveryField(t *testing.T) {
	want := Effect{
		ID:        "boom",
		Kind:      EffectExplosion,
		Position:  mgl32.Vec3{1, 2, 3},
		Rotation:  mgl32.Vec3{0, 0.5, 0},
		Enabled:   false,
		Playing:   false,
		Loop:      false,
		LoopDelay: 4,
		Attach:    Attach{Entity: "tank", Model: "body", Bone: "turret"},
		Explosion: Explosion{
			Radius: 9, Duration: 5, Brightness: 3,
			FireTint: mgl32.Vec3{0.5, 1, 0.5}, SmokeColor: mgl32.Vec3{0.1, 0.1, 0.1},
			Density: 3, Turbulence: 0.2, Rise: 0,
			LightColor: mgl32.Vec3{0, 1, 0}, LightBrightness: 0, LightRadius: 40,
		},
	}
	got, err := EffectFromRaw(EffectToRaw(&want))
	if err != nil {
		t.Fatal(err)
	}
	got.ScriptData, want.ScriptData = ScriptData{}, ScriptData{}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effect changed across a round trip:\n got %+v\nwant %+v", got, want)
	}

	def := NewEffect("boom", EffectExplosion, mgl32.Vec3{})
	raw := EffectToRaw(&def)
	if raw.Explosion != nil || raw.Enabled != nil || raw.Playing != nil || raw.Loop != nil || raw.LoopDelay != nil {
		t.Errorf("a default effect wrote optional keys: %+v", raw)
	}
	back, _ := EffectFromRaw(raw)
	if back.Explosion != DefaultExplosion() {
		t.Errorf("a default effect read back as %+v", back.Explosion)
	}
}

func TestEffectIDsShareTheLevelNamespace(t *testing.T) {
	lvl := &Level{Sounds: []Sound{{ID: "boom"}}}
	if id := UniqueEffectID(lvl, "boom"); id == "boom" {
		t.Error("an effect was allowed the name a sound already has")
	}
	lvl.Effects = []Effect{{ID: "blast"}}
	if !LevelIDTaken(lvl, "blast") {
		t.Error("LevelIDTaken does not see effects")
	}
}
