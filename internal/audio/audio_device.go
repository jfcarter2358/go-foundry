package audio

// audio_device.go — opening the output, and the goroutine that feeds it.
//
// The engine already brings the SDL audio subsystem up: app.Run calls
// sdl.Init(sdl.INIT_EVERYTHING), which includes SDL_INIT_AUDIO. All that is
// left is to open a device and keep a queue full.
//
// Two decisions are worth stating outright.
//
// First, this pushes with QueueAudio from a goroutine rather than being driven
// by SDL's audio callback. go-sdl2 declares AudioSpec.Callback as a raw C
// function pointer with no Go wrapper, so a callback device would need an
// //export'ed cgo shim and would then run the entire mixer on a thread the Go
// scheduler does not own. Pushing costs a little latency and buys ordinary Go
// code.
//
// Second, a failure to open is not fatal. The engine log.Fatalf's on GL
// failures because there is nothing to draw without GL; a game with no sound
// still runs, and a CI container with no ALSA device still has to run the
// whole test suite. Init logs and returns; Available() then reports false and
// every public call becomes a no-op.

import (
	"log"
	"runtime"
	"sync/atomic"
	"time"
)

// Config is what Init is asked for. The zero value is usable — see withDefaults.
type Config struct {
	// Rate is the preferred sample rate. The device may hand back another.
	Rate int
	// ChunkMS is how much audio one render produces.
	ChunkMS int
	// TargetMS is the queue depth the mixer tries to hold ahead of the device.
	TargetMS int
	// DeviceName selects an output; empty means the system default.
	DeviceName string
}

func (c Config) withDefaults() Config {
	if c.Rate <= 0 {
		c.Rate = 48000
	}
	if c.ChunkMS <= 0 {
		c.ChunkMS = 10
	}
	if c.TargetMS <= 0 {
		c.TargetMS = defaultTargetMS
	}
	return c
}

// device is the whole mutable lifecycle, kept in one struct so Shutdown has
// exactly one thing to tear down.
type device struct {
	sink Sink
	mix  *mixer
	stop chan struct{}
	done chan struct{}
	cfg  Config
}

// active is the open device, or nil.
//
// This is the audio equivalent of gfx.UpdateCamMatrix's `if Program == 0`
// guard, and it exists for the same reason: it is what lets a headless test
// call the real API rather than a mock. Every public function loads it once
// into a local — checking it and then using it as two separate reads is a nil
// dereference waiting for a Shutdown to land in between.
var active atomic.Pointer[device]

// Available reports whether there is an output. False in CI, in a headless
// container, and on a machine with no sound card.
func Available() bool { return active.Load() != nil }

// Rate is the device's actual sample rate, or 0 when there is none.
func Rate() int {
	d := active.Load()
	if d == nil {
		return 0
	}
	return d.sink.Rate()
}

// Init opens the output and starts the mixer.
//
// Calling it twice is a no-op rather than an error, so a test that opens a
// device does not have to care whether something else already did.
func Init(cfg Config) error {
	if active.Load() != nil {
		return nil
	}
	cfg = cfg.withDefaults()

	sink, err := OpenSink(cfg)
	if err != nil {
		log.Printf("audio: no output device (%v); the game runs silent", err)
		return err
	}

	m := newMixer(sink.Rate(), 2)
	d := &device{
		sink: sink,
		mix:  m,
		stop: make(chan struct{}),
		done: make(chan struct{}),
		cfg:  cfg,
	}
	active.Store(d)
	go mixLoop(d)
	log.Printf("audio: %d Hz stereo, %d ms chunks, %d ms target", sink.Rate(), cfg.ChunkMS, cfg.TargetMS)
	return nil
}

// Shutdown stops the mixer and closes the device, in that order.
//
// The ordering is the whole point. A QueueAudio still in flight when SDL_Quit
// runs is a use-after-free down in C, so this waits for the mixer to
// acknowledge rather than merely signalling it. The command channel is never
// closed: a game-thread Play racing this would panic on a send to a closed
// channel, and clearing the device pointer is a sufficient stop signal.
func Shutdown() {
	d := active.Swap(nil)
	if d == nil {
		return
	}
	close(d.stop)
	select {
	case <-d.done:
	case <-time.After(500 * time.Millisecond):
		log.Printf("audio: mixer did not stop in time; closing the device anyway")
	}
	d.sink.Close()
}

// mixLoop keeps the device's queue topped up.
func mixLoop(d *device) {
	// Locked for the life of the goroutine and never unlocked. Every write to
	// the sink is a cgo call; on an unlocked goroutine the scheduler is free
	// to run it on the same OS thread SDL pumps events on, where a write that
	// blocks on the device lock would stall input.
	runtime.LockOSThread()
	defer close(d.done)

	rate := d.sink.Rate()
	chunk := rate * d.cfg.ChunkMS / 1000
	if chunk < 64 {
		chunk = 64
	}
	target := rate * d.cfg.TargetMS / 1000
	block := make([]float32, chunk*2)

	// Half a chunk, so one missed wakeup cannot drain the queue. A plain
	// sleep rather than a ticker: a ticker that falls behind coalesces and
	// the loop then writes a burst, whereas the queue-depth check below is
	// already self-correcting.
	sleep := time.Duration(d.cfg.ChunkMS/2) * time.Millisecond
	if sleep < 2*time.Millisecond {
		sleep = 2 * time.Millisecond
	}

	// started guards the underrun count. The queue is legitimately empty
	// before the first block is ever written, and counting that as an
	// underrun would put a 1 in the stats of every session that never had one.
	started := false

	for {
		select {
		case <-d.stop:
			return
		default:
		}

		d.mix.drainCommands()

		queued := d.sink.QueuedFrames()
		if queued == 0 && started {
			d.mix.underruns.Add(1)
		}
		for queued < target {
			d.mix.render(block, chunk)
			if err := d.sink.Write(block); err != nil {
				log.Printf("audio: write failed: %v", err)
				return
			}
			queued += chunk
			started = true
		}

		time.Sleep(sleep)
	}
}

// ---------- stats ----------

// Stats is what the debug overlay reads.
type Stats struct {
	Voices    int
	Underruns uint64
	Dropped   uint64
	QueuedMS  float32
	// Peak is the last block's loudest sample, which doubles as a level meter
	// telling an author their clip is far hotter than everything else.
	Peak float32
}

// Snapshot reports the mixer's current state. Safe to call from the game
// thread; the numbers are at most one block stale.
func Snapshot() Stats {
	d := active.Load()
	if d == nil {
		return Stats{}
	}
	var s Stats
	s.Underruns = d.mix.underruns.Load()
	s.Dropped = d.mix.dropped.Load()
	s.Voices = d.mix.liveApprox()
	s.Peak = d.mix.peakApprox()
	if r := d.sink.Rate(); r > 0 {
		s.QueuedMS = float32(d.sink.QueuedFrames()) * 1000 / float32(r)
	}
	return s
}

// sleepMillisecond exists so tests can wait on the mixer without importing
// time for a single call.
func sleepMillisecond() { time.Sleep(time.Millisecond) }
