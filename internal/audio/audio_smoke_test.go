package audio

// audio_smoke_test.go — the only test here that opens real hardware.
//
// Everything else in this package runs against a RecordingSink, which proves
// the mixer but not that SDL accepts what it produces. A wrong AudioSpec, a
// format the driver silently refuses, or a QueueAudio issued from a thread the
// driver dislikes all show up here and nowhere else — and the symptom in the
// running engine is silence, which no amount of `go build` can see.
//
// It needs a sound device, so it is opt-in, in the same arrangement
// gltest.Context makes for a GL context.
//
//	FOUNDRY_AUDIO_TEST=1 go test ./internal/audio/...

import (
	"math"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

func TestARealDeviceAcceptsARealMix(t *testing.T) {
	if os.Getenv("FOUNDRY_AUDIO_TEST") == "" {
		t.Skip("set FOUNDRY_AUDIO_TEST=1 to run (needs a sound device)")
	}
	// Nothing is torn down on the way out, for gltest.Context's reason: a Go
	// test does not necessarily run on the process's main OS thread, and
	// tearing an SDL subsystem down from another one segfaults in the driver.
	runtime.LockOSThread()
	if err := sdl.Init(sdl.INIT_AUDIO); err != nil {
		t.Fatalf("sdl.Init(INIT_AUDIO): %v", err)
	}

	if err := Init(Config{}); err != nil {
		t.Fatalf("Init against real hardware: %v", err)
	}
	defer Shutdown()

	if !Available() {
		t.Fatal("Init succeeded but Available() is false")
	}
	t.Logf("device opened at %d Hz", Rate())

	// A 440 Hz tone, installed directly into the cache so the test does not
	// need an asset on disk.
	rate := Rate()
	frames := rate / 2
	s := make([]float32, frames)
	for i := range s {
		s[i] = 0.4 * float32(math.Sin(2*math.Pi*440*float64(i)/float64(rate)))
	}
	clipMu.Lock()
	clipCache["smoke-tone"] = clipEntry{clip: &Clip{
		Name: "smoke-tone", Samples: s, Rate: rate, Channels: 1,
	}}
	clipMu.Unlock()
	t.Cleanup(ReloadClips)

	if id := PlayGlobal("smoke-tone", BusEffects, 1, true); id == 0 {
		t.Fatal("Play returned no handle with a device open")
	}

	time.Sleep(250 * time.Millisecond)
	st := Snapshot()
	t.Logf("voices=%d queued=%.1fms peak=%.3f underruns=%d dropped=%d",
		st.Voices, st.QueuedMS, st.Peak, st.Underruns, st.Dropped)

	if st.Voices == 0 {
		t.Error("the voice is not playing")
	}
	// Peak is what proves samples genuinely reached the device rather than
	// the mixer having quietly produced silence.
	if st.Peak <= 0.1 || st.Peak > 1.0 {
		t.Errorf("block peak is %.4f; expected a clearly audible tone inside unity", st.Peak)
	}
	if st.QueuedMS < 1 {
		t.Errorf("only %.2f ms queued; the mixer is not keeping ahead of the device", st.QueuedMS)
	}
}
