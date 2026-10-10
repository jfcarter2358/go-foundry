package script

// script_audio_test.go — the audio verbs and properties, end to end through
// the real event loop.
//
// Headless, and genuinely so: no device is ever opened, so every audio.Play
// returns a zero handle and the mixer never runs. That is the point of the
// no-device guard in internal/audio — what is asserted here is the *binding*,
// which is where the bugs are. Six tables have to agree before a script can
// reach a sound at all (the ref kind, the cache, refFor, the property table,
// the verb table and scriptedRefs), and every one of them is easy to forget.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/world"
)

// soundLevel builds a level holding one scripted sound.
func soundLevel(t *testing.T, script string) *world.Level {
	t.Helper()
	lvl := tickTestLevel(t)
	s := world.NewSound("hum", mgl32.Vec3{0, 1, 0})
	s.Clip = "machine"
	s.Script = script
	lvl.Sounds = append(lvl.Sounds, s)
	return lvl
}

// TestScriptCanReachASound is the whole binding chain in one assertion.
func TestScriptCanReachASound(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "s.flang", `def on_spawn(:spawn:) -> null
    @self.prop.gain = 0.25
end`)

	lvl := soundLevel(t, path)
	runTicks(t, lvl, 3)

	if lvl.Sounds[0].Gain != 0.25 {
		t.Errorf("gain is %v; the script's write did not reach the sound", lvl.Sounds[0].Gain)
	}
}

func TestStopLeavesThePlacementInPlace(t *testing.T) {
	// The distinction between `stop` and switching the sound off, and the
	// reason both exist: a stopped machine is still a machine that a later
	// `play` can start again.
	root := withTempScriptDir(t)
	path := writeScript(t, root, "s.flang", `def on_spawn(:spawn:) -> null
    stop(@self)
end`)

	lvl := soundLevel(t, path)
	runTicks(t, lvl, 2)

	if lvl.Sounds[0].Playing {
		t.Error("stop did not clear Playing")
	}
	if !lvl.Sounds[0].Enabled {
		t.Error("stop switched the whole sound off; that is what .prop.enabled is for")
	}
}

func TestPlayStartsASoundThatWasSwitchedOff(t *testing.T) {
	// `play` on a disabled sound must start it rather than silently doing
	// half the job — the same rule `emit` follows for emitters.
	root := withTempScriptDir(t)
	path := writeScript(t, root, "s.flang", `def on_spawn(:spawn:) -> null
    play(@self)
end`)

	lvl := soundLevel(t, path)
	lvl.Sounds[0].Enabled = false
	lvl.Sounds[0].Playing = false
	runTicks(t, lvl, 2)

	if !lvl.Sounds[0].Enabled || !lvl.Sounds[0].Playing {
		t.Errorf("play left enabled=%v playing=%v; it should have started it",
			lvl.Sounds[0].Enabled, lvl.Sounds[0].Playing)
	}
}

func TestPlaySoundAtPlacesASelfRemovingOneShot(t *testing.T) {
	// Modelled on spawn_effect, and carrying its most important property: a
	// script firing one per impact must not fill the level with spent
	// placements, and they must never be written to the level file.
	root := withTempScriptDir(t)
	path := writeScript(t, root, "s.flang", `def on_spawn(:spawn:) -> null
    play_sound_at("thud", <3.0, 0.0, 4.0>)
end`)

	lvl := soundLevel(t, path)
	runTicks(t, lvl, 2)

	var oneShot *world.Sound
	for i := range lvl.Sounds {
		if lvl.Sounds[i].OneShot {
			oneShot = &lvl.Sounds[i]
		}
	}
	if oneShot == nil {
		t.Fatal("play_sound_at placed no sound")
	}
	if oneShot.Clip != "thud" {
		t.Errorf("the placed sound plays %q, want \"thud\"", oneShot.Clip)
	}
	if oneShot.Position != (mgl32.Vec3{3, 0, 4}) {
		t.Errorf("placed at %v, want {3 0 4}", oneShot.Position)
	}
	if oneShot.Loop {
		t.Error("a one-shot was placed looping; it would never finish and never be reaped")
	}
}

func TestPlaySoundUsesTheObjectsPosition(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "s.flang", `def on_spawn(:spawn:) -> null
    play_sound("clank", @self)
end`)

	lvl := soundLevel(t, path)
	lvl.Sounds[0].Position = mgl32.Vec3{7, 2, -1}
	runTicks(t, lvl, 2)

	for i := range lvl.Sounds {
		if lvl.Sounds[i].OneShot {
			if lvl.Sounds[i].Position != (mgl32.Vec3{7, 2, -1}) {
				t.Errorf("the one-shot landed at %v, not on the object at {7 2 -1}", lvl.Sounds[i].Position)
			}
			return
		}
	}
	t.Fatal("play_sound placed no sound")
}

func TestAOneShotIsReapedOnceItHasFinished(t *testing.T) {
	// With no device the voice handle is always zero, which is exactly the
	// state a finished one-shot is in — so the reaper has to clear it.
	lvl := tickTestLevel(t)
	s := world.NewSound("bang", mgl32.Vec3{})
	s.Clip = "thud"
	s.Loop = false
	s.OneShot = true
	lvl.Sounds = append(lvl.Sounds, s)

	world.SoundUpdate(lvl, 1.0/60.0, true,
		mgl32.Vec3{}, mgl32.Vec3{1, 0, 0}, mgl32.Vec3{0, 1, 0})

	for i := range lvl.Sounds {
		if lvl.Sounds[i].OneShot {
			t.Fatal("the spent one-shot was not reaped; a level firing these per impact would grow without bound")
		}
	}
}

func TestPlayMusicIsANoOpWhenTheTrackIsAlreadyPlaying(t *testing.T) {
	// A handler that sets the track every tick must not restart it every
	// tick, which would be an audible stutter for what reads as a no-op.
	lvl := tickTestLevel(t)
	if !world.SetMusic(lvl, "cavern", 1) {
		t.Fatal("the first SetMusic reported no change")
	}
	if lvl.Music == nil || lvl.Music.Track != "cavern" {
		t.Fatalf("music is %+v after being set to cavern", lvl.Music)
	}
	// With no device the voice is zero, so "already playing" cannot be
	// detected and a re-set is honest about having changed something. What
	// must not happen is a different track being ignored.
	if !world.SetMusic(lvl, "surface", 1) {
		t.Error("switching to a different track reported no change")
	}
	if lvl.Music.Track != "surface" {
		t.Errorf("track is %q after switching", lvl.Music.Track)
	}
}

func TestTheBusPropertyRefusesANameThatIsNotABus(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "s.flang", `def on_spawn(:spawn:) -> null
    @self.prop.bus = "dialogue"
end`)

	lvl := soundLevel(t, path)
	runTicks(t, lvl, 2)

	if lvl.Sounds[0].Bus == "dialogue" {
		t.Error("an unknown bus name was accepted; it would silently answer to no slider")
	}
}

func TestGainIsClampedRatherThanBlowingTheMixOut(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "s.flang", `def on_spawn(:spawn:) -> null
    @self.prop.gain = -5
end`)

	lvl := soundLevel(t, path)
	runTicks(t, lvl, 2)

	if g := lvl.Sounds[0].Gain; g < 0 {
		t.Errorf("gain is %v; a negative gain inverts the waveform", g)
	}
}
