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

// DefaultStepHeight is the allowance a level that says nothing gets, quoted
// against the engine's own player: what EffectiveStepHeight works out to for an
// unmodified body. It exists as a function of its own so the editor can show
// the number an author is departing from without building a player to ask.
func DefaultStepHeight() float32 { return playerHalfHeight * 2 }

// SuggestedStepHeight is what the editor seeds a step height with when an
// author first sets one, in world units. About a stair riser: far enough below
// the default (a whole body height) that the change is obvious the first time
// the level is walked, and tall enough that no floor seam or shallow ramp joint
// trips over it.
const SuggestedStepHeight = float32(0.5)

// MaxStepHeight is the ceiling the editor's field offers. Nothing enforces it
// at load — a hand-written level may ask for more — it only keeps the drag from
// reaching values where the player steps up onto the roof of the room it is
// standing in, which is what the resolver exists to prevent in the first place.
const MaxStepHeight = float32(16)

// PlayerSettings is a level's tuning for the player it starts.
type PlayerSettings struct {
	// StepHeight is the tallest riser the player may walk up onto rather than
	// be stopped by — see ResolveStepEntry in the collision package, which is
	// what actually enforces it.
	//
	// Zero (an absent `player:` block, or one that does not mention it) means
	// the body's own height, which is the threshold the engine used before this
	// was configurable and the only figure it had to hand. Anything that is not
	// positive reads as that same default rather than as "no stepping at all":
	// a body that could not step at all would be stopped by the seam between
	// two sectors whose floors differ by a rounding error, so zero clearance is
	// never the thing an author meant.
	StepHeight float32
}

// EffectiveStepHeight resolves StepHeight against the body it applies to,
// which is where the "zero means the body's own height" rule is actually
// spent. A nil entity is not an error: there is no body to measure, so it falls
// back to the engine's own player, which is the body this setting is about in
// every case that reaches here without one.
func (p PlayerSettings) EffectiveStepHeight(e *Entity) float32 {
	if p.StepHeight > 0 {
		return p.StepHeight
	}
	if e == nil {
		return DefaultStepHeight()
	}
	return e.BoundHalfY * 2
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
