package harness

// play.go — the session as something that plays.
//
// Session already knew how to walk a player and read a value; what it did not
// have was a way to be *driven* — to be told "look left, walk to the door,
// press use, take a picture" and to spend that over the ticks it takes. That
// is internal/play, and this file is the half of it a headless session owns:
// the Target it drives (the level, the log, a watch path and a picture) and
// the loop that runs a script to its end.
//
// The same script text drives the editor's Play mode, which is the whole
// reason the vocabulary lives in its own package rather than in here.

import (
	"fmt"

	"foundry/internal/play"
	"foundry/internal/world"
)

// playTarget is the session seen as something a script can drive.
//
// A separate type rather than methods on Session, for a dull reason: Level is
// a field on Session and play.Target wants a Level() method, and a Go type
// cannot have both. Keeping the field is worth a four-line adapter — reaching
// straight for sess.Level is what every caller and every test does.
type playTarget struct{ s *Session }

func (t playTarget) Level() *world.Level { return t.s.Level }

func (t playTarget) Emit(kind, name, value string) {
	t.s.emit(Event{Kind: kind, Name: name, Value: value})
}

func (t playTarget) Read(path string) string { return read(t.s.Level, path) }

func (t playTarget) Shot(name string) (string, error) { return t.s.Shot(name) }

// ReadPath answers one watch path — the exported half of read.go, so the
// editor's play driver can answer `read` the same way a headless run does.
func ReadPath(lvl *world.Level, path string) string { return read(lvl, path) }

// Play runs a script to its end and answers the pictures it took.
//
// The script is spent a tick at a time against the same sim.Step a played
// frame runs: `forward 2` really walks for two seconds of game time, into
// whatever is in the way. limit is how long the run may take in game seconds,
// after which the script is stopped where it stands — a `to` walking into a
// wall forever must not hang the tool driving it.
func (s *Session) Play(text string, limit float64) ([]string, error) {
	cmds, err := play.Parse(text)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 120
	}
	r := play.New(playTarget{s}, cmds)
	end := s.T + limit
	for !r.Done() {
		if s.T >= end {
			s.emit(Event{Kind: "TIMEOUT", Value: fmt.Sprintf("stopped after %.0fs of game time", limit)})
			r.Stop()
			break
		}
		s.StepWith(r.Tick(s.dt))
	}
	return r.Shots(), nil
}

// State is the player's situation in words: where they stand, what they are
// holding, what the crosshair is on, what is near them and what is on screen.
func (s *Session) State() string { return play.State(s.Level, s.T) }
