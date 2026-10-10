package world

// emitter_light_test.go — the flash an effect's `light:` block casts.

import (
	"testing"

	"foundry/internal/particle"

	"github.com/go-gl/mathgl/mgl32"
)

// flashEmitter is one emitter at pos running a one-layer effect that lights.
func flashEmitter(id string, pos mgl32.Vec3, brightness float32) Emitter {
	e := NewEmitter(id, pos)
	e.Effect = "flash"
	doc := &particle.Doc{
		ID:      "flash",
		Gravity: particle.DefaultGravity,
		Light:   particle.Flash{Color: mgl32.Vec3{1, 0.8, 0.5}, Brightness: brightness, Radius: 8, Duration: 0.08},
		Layers: []particle.Layer{{
			Name: "flash", Duration: 0.2, MaxParticles: 8, LifeMin: 0.05, LifeMax: 0.05,
			Bursts: []particle.Burst{{Time: 0, Count: 2}}, Texture: "default",
			Tint: mgl32.Vec3{1, 1, 1}, BoxHalf: mgl32.Vec3{0.5, 0.5, 0.5}, Direction: mgl32.Vec3{0, 1, 0},
		}},
	}
	e.SetDoc(doc)
	return e
}

// The whole of the feature from the level's side: an effect that lights is
// dark before its first step, lit as it goes off, and dark again once the
// flash is over — and it asks for the upload that puts it out.
func TestEmitterFlashLightsWhileTheEffectGoesOff(t *testing.T) {
	lvl := &Level{Emitters: []Emitter{flashEmitter("muzzle", mgl32.Vec3{2, 1, 0}, 5)}}
	emitterFlashesWereLit = false

	if _, ok := lvl.Emitters[0].FlashLight(lvl); ok {
		t.Error("the flash was lit before the effect had taken a single step")
	}
	if !EmitterUpdate(lvl, 1.0/60.0, true) {
		t.Error("an effect going off did not ask for a light upload")
	}
	l, ok := lvl.Emitters[0].FlashLight(lvl)
	if !ok {
		t.Fatal("the effect is a frame into its cycle and is not lighting")
	}
	if !l.Dynamic || l.CastShadows {
		t.Errorf("flash light %+v: a flash is dynamic and casts no shadow", l)
	}
	if l.ID != "muzzle" || l.Position != (mgl32.Vec3{2, 1, 0}) || l.Radius != 8 {
		t.Errorf("flash light %+v: want the emitter's id, place and reach", l)
	}
	if l.Brightness <= 0 || l.Brightness > 5 {
		t.Errorf("flash brightness %v is outside (0, 5]", l.Brightness)
	}

	// Past the flash's own duration, with the layer still running: the light
	// is out, and the frame it goes out still asks for an upload to clear it.
	if !EmitterUpdate(lvl, 0.1, true) {
		t.Error("the frame the flash went out did not ask for the upload that clears it")
	}
	if _, ok := lvl.Emitters[0].FlashLight(lvl); ok {
		t.Error("the flash is still lit past its duration")
	}
	if EmitterUpdate(lvl, 0.01, true) {
		t.Error("with nothing lit for two frames it still asks for an upload every frame")
	}
}

// An effect with no `light:` is exactly what it was before the feature, and a
// stopped or switched-off one does not light either.
func TestOnlyALitRunningEmitterCastsAFlash(t *testing.T) {
	dark := flashEmitter("plain", mgl32.Vec3{}, 0)
	lvl := &Level{Emitters: []Emitter{dark}}
	EmitterUpdate(lvl, 1.0/60.0, true)
	if _, ok := lvl.Emitters[0].FlashLight(lvl); ok {
		t.Error("an effect with no light in its document lit one anyway")
	}

	lvl = &Level{Emitters: []Emitter{flashEmitter("muzzle", mgl32.Vec3{}, 5)}}
	EmitterUpdate(lvl, 1.0/60.0, true)
	for _, c := range []struct {
		name string
		set  func(e *Emitter)
	}{
		{"stop_emit", func(e *Emitter) { e.Emitting = false }},
		{"disabled", func(e *Emitter) { e.Enabled = false }},
	} {
		e := lvl.Emitters[0]
		c.set(&e)
		if _, ok := e.FlashLight(lvl); ok {
			t.Errorf("a %s emitter still cast its flash", c.name)
		}
	}
}

// The eight slots, contested: a shadow-casting lamp keeps its own, and what is
// left goes to the flashes the camera can actually see rather than to the ones
// that happen to come first.
func TestFlashesWinSlotsByWhatTheCameraSees(t *testing.T) {
	lvl := &Level{}
	lvl.Lights.Points = []PointLight{{ID: "lamp", CastShadows: true, Radius: 5, Brightness: 1}}
	lvl.Effects = []Effect{NewEffect("boom", EffectExplosion, mgl32.Vec3{0, 3, 0})}
	lvl.Effects[0].Time = 0.03
	// Seven rifles: one beside the camera (which is at the origin in a test)
	// and six hundreds of metres off.
	for i, at := range []mgl32.Vec3{{200, 0, 0}, {300, 0, 0}, {1, 0, 0}, {400, 0, 0}, {500, 0, 0}, {600, 0, 0}, {700, 0, 0}} {
		lvl.Emitters = append(lvl.Emitters, flashEmitter(string(rune('a'+i)), at, 5))
	}
	EmitterUpdate(lvl, 1.0/60.0, true)

	ls := SceneLights(lvl)
	if len(ls.Points) != MaxPointLights {
		t.Fatalf("scene holds %d point lights, want all %d slots filled", len(ls.Points), MaxPointLights)
	}
	if ls.Points[0].ID != "lamp" {
		t.Error("a flash took the slot of the level's own shadow-casting lamp")
	}
	got := map[string]bool{}
	for _, p := range ls.Points {
		got[p.ID] = true
	}
	if !got["c"] {
		t.Error("the flash going off beside the camera lost its slot to one hundreds of metres away")
	}
	if !got["boom"] {
		t.Error("the explosion's flash lost its slot to a rifle a quarter of a mile away")
	}
	if len(lvl.Lights.Points) != 1 {
		t.Error("SceneLights appended into the level's own light list")
	}
}

// A level with more point lights than the shader carries — one a tool
// generated, which is the only way to get one — lights what is near the camera
// rather than whatever came first in the file.
func TestTooManyLevelLightsKeepTheOnesInReach(t *testing.T) {
	lvl := &Level{}
	for i := 0; i < MaxPointLights+4; i++ {
		lvl.Lights.Points = append(lvl.Lights.Points, PointLight{
			ID: string(rune('a' + i)), Position: mgl32.Vec3{float32(500 - 40*i), 0, 0},
			Brightness: 1, Radius: 7,
		})
	}
	// The shadow caster is the last in the file and the furthest away: it
	// keeps its slot regardless, because its cubemap is found by its index.
	lvl.Lights.Points[0].CastShadows = true
	lvl.Lights.Points[0].ShadowStrength = 1

	ls := SceneLights(lvl)
	if len(ls.Points) != MaxPointLights {
		t.Fatalf("scene holds %d point lights, want %d", len(ls.Points), MaxPointLights)
	}
	if ls.Points[0].ID != "a" {
		t.Errorf("the shadow-casting light lost its slot (first is %q)", ls.Points[0].ID)
	}
	near := lvl.Lights.Points[len(lvl.Lights.Points)-1].ID
	found := false
	for _, p := range ls.Points {
		if p.ID == near {
			found = true
		}
	}
	if !found {
		t.Errorf("the light nearest the camera (%q) was cut for one far behind it", near)
	}
	if len(lvl.Lights.Points) != MaxPointLights+4 {
		t.Error("fitting the scene's lights cut the level's own list")
	}
}
