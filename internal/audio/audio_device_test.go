package audio

// audio_device_test.go — the device seam.
//
// The test this file exists for is
// TestEveryPublicCallIsSafeWithNoDeviceOpen. It is the audio equivalent of
// gfx.UpdateCamMatrix's `if Program == 0` guard, and it is what lets every
// other package's tests — world's, script's, the editor's — call the real
// audio API with no mock and no sound card. CI has no audio device; the whole
// suite has to run there anyway.

import (
	"sync"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// withRecordingSink installs a sink that remembers what it was handed, and
// puts the real one back afterwards. Package globals restored via t.Cleanup,
// per house style.
func withRecordingSink(t *testing.T) *RecordingSink {
	t.Helper()
	sink := NewRecordingSink(testRate)
	prev := OpenSink
	OpenSink = func(Config) (Sink, error) { return sink, nil }
	t.Cleanup(func() {
		Shutdown()
		OpenSink = prev
	})
	if err := Init(Config{}); err != nil {
		t.Fatalf("Init with a recording sink: %v", err)
	}
	return sink
}

func TestEveryPublicCallIsSafeWithNoDeviceOpen(t *testing.T) {
	if Available() {
		t.Fatal("a device is open; this test asserts the silent path")
	}
	// None of these may panic, and Play must hand back a handle that is safe
	// to pass everywhere else.
	id := Play(PlayOptions{Clip: "nothing-of-the-sort"})
	if id != 0 {
		t.Errorf("Play with no device returned handle %d, want 0", id)
	}
	PlaySound("nope", mgl32.Vec3{1, 2, 3}, 1)
	PlayGlobal("nope", BusMusic, 1, true)
	Stop(id)
	FadeOut(id, 0.5)
	SetLoop(id, true)
	StopAll()
	Publish(Frame{ListenerForward: mgl32.Vec3{1, 0, 0}, ListenerUp: mgl32.Vec3{0, 1, 0}})
	Preload("nope")
	if r := Rate(); r != 0 {
		t.Errorf("Rate with no device is %d, want 0", r)
	}
	if s := Snapshot(); s.Voices != 0 {
		t.Errorf("Snapshot with no device reports %d voices", s.Voices)
	}
}

func TestInitReportsAFailureWithoutTakingTheProcessDown(t *testing.T) {
	// A game with no sound still runs, and a CI container with no ALSA device
	// still has to run the test suite. This is why app.Run must not Fatalf on
	// the error Init returns.
	prev := OpenSink
	OpenSink = func(Config) (Sink, error) { return nil, errNoDeviceForTest{} }
	t.Cleanup(func() { OpenSink = prev })

	if err := Init(Config{}); err == nil {
		t.Error("Init reported success with no device available")
	}
	if Available() {
		t.Error("Available() is true after a failed Init")
	}
}

type errNoDeviceForTest struct{}

func (errNoDeviceForTest) Error() string { return "no device, for the test" }

func TestTheMixerFillsTheQueueAndKeepsItThere(t *testing.T) {
	sink := withRecordingSink(t)
	waitFor(t, func() bool { return sink.QueuedFrames() > 0 })

	target := testRate * defaultTargetMS / 1000
	waitFor(t, func() bool { return sink.QueuedFrames() >= target/2 })
}

func TestShutdownStopsTheMixerBeforeClosingTheSink(t *testing.T) {
	// A write in flight while SDL_Quit runs is a use-after-free down in C.
	// RecordingSink fails a write after close, so a mixer that outlived
	// Shutdown shows up as a recorded error rather than as a rare crash.
	sink := NewRecordingSink(testRate)
	sink.Drain = 1 << 20 // keep it hungry, so the mixer is always writing
	prev := OpenSink
	OpenSink = func(Config) (Sink, error) { return sink, nil }
	t.Cleanup(func() { OpenSink = prev })

	if err := Init(Config{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return blockCount(sink) > 0 })
	Shutdown()

	if Available() {
		t.Error("Available() is still true after Shutdown")
	}
	// If the mixer were still running it would now be writing into a closed
	// sink; give it several of its own wakeup intervals to prove it is not.
	before := blockCount(sink)
	for i := 0; i < 50; i++ {
		sleepMillisecond()
	}
	if after := blockCount(sink); after != before {
		t.Errorf("the mixer wrote %d more blocks after Shutdown returned", after-before)
	}
}

func blockCount(s *RecordingSink) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.Blocks)
}

func TestPlayNeverBlocksItsCaller(t *testing.T) {
	// The command channel takes a non-blocking send: a full queue drops the
	// command and counts it rather than parking the render loop. Hammering it
	// from several goroutines must finish rather than deadlock, and must be
	// clean under -race.
	withRecordingSink(t)

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				id := Play(PlayOptions{Clip: "does-not-exist"})
				Stop(id)
				Publish(Frame{
					ListenerForward: mgl32.Vec3{1, 0, 0},
					ListenerUp:      mgl32.Vec3{0, 1, 0},
				})
			}
		}()
	}
	wg.Wait() // a hang here is the failure
}

func TestInitTwiceIsANoOpRatherThanASecondDevice(t *testing.T) {
	withRecordingSink(t)
	if err := Init(Config{}); err != nil {
		t.Errorf("a second Init returned %v; it should be a no-op", err)
	}
	if !Available() {
		t.Error("the device went away")
	}
}

// waitFor spins briefly on a condition, so the tests do not depend on the
// mixer's sleep interval.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if cond() {
			return
		}
		sleepMillisecond()
	}
	t.Fatal("timed out waiting for the mixer")
}
