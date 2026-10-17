package play

// library.go — a score by name, playing in the game.
//
// The game's side of the scheduler, the score's tracker.Play: load a score
// from sounds/, prepare it, put the play head where the level (or a save
// game) asked, and start it as one voice on the music bus. world.Music
// holds the player that comes back, and scripts steer it the way they
// steer a song.

import (
	"log"

	"foundry/internal/audio"
	"foundry/internal/score"
)

// PlayOptions say where a score starts and how it comes in.
type PlayOptions struct {
	// Section starts at a rehearsal mark by name ("" or unknown: the top).
	Section string
	Gain    float32
	FadeIn  float32
	// Params are set before the first note.
	Params map[string]float32
	// Resume starts at Tick instead of Section (save games).
	Resume bool
	Tick   int64
}

// Prepared loads a score by name and prepares it, ready to start. A line
// whose instrument will not load plays nothing and is logged; err is a
// score that will not load or perform at all.
func Prepared(name string) (*Player, error) {
	s, err := score.Load(name)
	if err != nil {
		return nil, err
	}
	// The cached score is shared; the player only reads it.
	return newLogged(name, s)
}

// newLogged is New with the instruments that would not load logged.
func newLogged(name string, s *score.Score) (*Player, error) {
	p, errs := New(s)
	if p == nil {
		// New's last error is the one that stopped it.
		return nil, errs[len(errs)-1]
	}
	for _, e := range errs {
		log.Printf("score %s: %v", name, e)
	}
	return p, nil
}

// Play loads a score and starts it on the music bus. The player is the
// handle scripts steer; the voice is the mixer's. The voice is zero when
// there is no audio device; both are when the score will not load.
func Play(name string, o PlayOptions) (*Player, audio.VoiceID, error) {
	p, err := Prepared(name)
	if err != nil {
		return nil, 0, err
	}
	if o.Resume {
		p.StartAt(o.Tick)
	} else {
		p.Start(o.Section)
	}
	for k, v := range o.Params {
		p.SetParamNow(k, v)
	}
	id := audio.PlayGenerator(p, audio.PlayOptions{Bus: audio.BusMusic, Gain: o.Gain, FadeIn: o.FadeIn, Priority: audio.PriorityHigh})
	return p, id, nil
}

// NewStinger prepares a score to play once over the level's music, from
// the top: it does not loop, and its sections neither loop nor move on
// (a stinger that repeated a section would never end). The player is
// started, ready for Player.Stinger or tracker.Player.StingerGenerator.
func NewStinger(name string) (*Player, error) {
	s, err := score.Load(name)
	if err != nil {
		return nil, err
	}
	c := s.Clone()
	c.Loop = false
	for i := range c.Sections {
		c.Sections[i].Loop, c.Sections[i].Next = false, ""
	}
	p, err := newLogged(name, c)
	if err != nil {
		return nil, err
	}
	p.Start("")
	return p, nil
}

// Preload reads a score and every instrument its lines play into their
// caches, so a level's score starts without touching the disk — what a
// level load does for its music.
func Preload(name string) error {
	s, err := score.Load(name)
	if err != nil {
		return err
	}
	for _, ref := range score.InstrumentRefs(s) {
		LoadInstrument(ref)
	}
	return nil
}
