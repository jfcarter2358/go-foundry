package world

// player_settings.go — the level's tuning for the engine-owned player.
//
// The player is the one body in the level nothing authors: NewPlayer builds it,
// no `entities:` entry describes it, and nothing saves it (see entity.go). That
// left its movement tuning with nowhere to live but constants, which is fine
// for the numbers that are the same in every game built on this engine and
// wrong for the ones that are a design decision about a particular world — how
// tall a ledge you can walk up is the difference between a parkour game and a
// dungeon crawl, not a property of the engine.
//
// So a level may carry a top-level `player:` block. It is optional and every
// key in it is optional, and the zero value of this struct is exactly what the
// engine did before the block existed — which matters more than usual here,
// because a Level is routinely built as a struct literal in tests and by the
// editor, and the ones that never mention the player must keep walking the way
// they always did.

// DefaultStepHeight is the allowance a level that says nothing gets, in world
// units: about a stair riser. Tall enough that no floor seam, shallow ramp
// joint or curb trips the player up, and short enough that anything built to
// read as an obstacle is one.
//
// It used to be the body's own height — 1.8 units, inherited from the sector
// era, where a "step" was a height difference between two floors and could
// never lift the body anywhere a floor was not. Against solid brushes it can:
// an allowance that tall is most of a room, and the taller the lift the more
// of the level the resolver has to teleport the body through to try it. See
// StepUp in internal/collision, which refuses a lift that does not land in
// free space — a fix that is much less load-bearing now that the default lift
// is a curb rather than a body.
//
// A function rather than a constant so the editor can show the number an
// author is departing from, and so the one place it is written stays the one
// place it is read.
func DefaultStepHeight() float32 { return 0.5 }

// MaxStepHeight is the ceiling the editor's field offers. Nothing enforces it
// at load — a hand-written level may ask for more — it only keeps the drag
// away from values where "step height" stops describing steps at all.
const MaxStepHeight = float32(16)

// PlayerSettings is a level's tuning for the player it starts.
type PlayerSettings struct {
	// StepHeight is the tallest riser the player may walk up onto rather than
	// be stopped by — see ResolveStepEntry in the collision package, which is
	// what actually enforces it.
	//
	// Zero (an absent `player:` block, or one that does not mention it) means
	// DefaultStepHeight. Anything that is not positive reads as that same
	// default rather than as "no stepping at all":
	// a body that could not step at all would be stopped by the seam between
	// two sectors whose floors differ by a rounding error, so zero clearance is
	// never the thing an author meant.
	StepHeight float32
}

// EffectiveStepHeight is the allowance actually in force: what the level says,
// or the default when it says nothing.
//
// It used to take the body it applies to, because the default was a multiple of
// that body's height and a nil one had to fall back to the engine's player.
// The default is a flat distance now, so there is nothing left to measure and
// nothing left to be nil.
func (p PlayerSettings) EffectiveStepHeight() float32 {
	if p.StepHeight > 0 {
		return p.StepHeight
	}
	return DefaultStepHeight()
}

// PlayerSettingsFromRaw reads the optional `player:` block. A nil in is a level
// that never mentioned the player, and gives the zero value rather than an
// error — the same way an absent `fog:` is a level with no fog.
func PlayerSettingsFromRaw(r *RawPlayer) PlayerSettings {
	var p PlayerSettings
	if r == nil {
		return p
	}
	if r.StepHeight != nil {
		p.StepHeight = *r.StepHeight
	}
	return p
}

// PlayerSettingsToRaw writes the block back, returning nil for settings that
// are still the default. That nil is what keeps every level authored before
// this block existed byte-identical when the editor saves it.
func PlayerSettingsToRaw(p PlayerSettings) *RawPlayer {
	if p == (PlayerSettings{}) {
		return nil
	}
	sh := p.StepHeight
	return &RawPlayer{StepHeight: &sh}
}
