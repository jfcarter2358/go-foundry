package audio

// audio_bus.go — the three gains a player actually adjusts.
//
// Named Gain rather than Volume throughout this package, and everywhere it
// surfaces in the editor and in scripts. `Volume` is already a level object
// type in this engine — a scriptable region — and a `Volume` field on an audio
// object next to world.Volume would be a genuinely confusing collision.

import "sync"

// Bus names which slider a sound answers to. A string rather than an iota
// because it is written into level files.
type Bus string

const (
	BusEffects Bus = "effects"
	BusMusic   Bus = "music"
)

// Gains are the three user-facing levels. Read on the mixer goroutine and
// written from the UI thread, so they sit behind a mutex rather than being
// bare globals like the rest of the engine's settings — a torn float32 is not
// a real hazard on any target here, but the race detector is right to object
// and `go test -race` runs in this repo.
var gains = struct {
	sync.RWMutex
	master  float32
	effects float32
	music   float32
}{master: 1, effects: 1, music: 1}

// SetMasterGain, SetEffectsGain and SetMusicGain clamp to [0,1] so a settings
// slider — or a script — cannot drive the mix negative or blow it out.
func SetMasterGain(g float32)  { gains.Lock(); gains.master = clamp01(g); gains.Unlock() }
func SetEffectsGain(g float32) { gains.Lock(); gains.effects = clamp01(g); gains.Unlock() }
func SetMusicGain(g float32)   { gains.Lock(); gains.music = clamp01(g); gains.Unlock() }

// MasterGain, EffectsGain and MusicGain read them back, for the settings panel.
func MasterGain() float32  { gains.RLock(); defer gains.RUnlock(); return gains.master }
func EffectsGain() float32 { gains.RLock(); defer gains.RUnlock(); return gains.effects }
func MusicGain() float32   { gains.RLock(); defer gains.RUnlock(); return gains.music }

// busGain is the master-multiplied level for one bus, taken once per mix block
// rather than per voice.
func busGains() (effects, music float32) {
	gains.RLock()
	defer gains.RUnlock()
	return gains.master * gains.effects, gains.master * gains.music
}
