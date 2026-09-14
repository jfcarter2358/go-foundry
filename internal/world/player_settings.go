package world

import "fmt"

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
	// Models is the first-person viewmodel this level starts the player with —
	// hands and whatever they hold. Nil is no viewmodel at all, not "keep
	// whatever the last level had": a level's player block is read the same
	// unconditional way StepHeight is, so a level that wants the player armed
	// says so itself rather than inheriting it. render.RenderViewmodel draws
	// these; nothing else in the frame will, since PlayerEntity is not in
	// Level.Entities (see entity.go's header on why) and so is invisible to
	// every walk that is.
	Models []EntityModel

	// Script, Attributes and ScriptParams are the player's behaviour, the
	// same three things an entity type carries: which .flang file hears what
	// happens to the player (`:primary_fire:`, `:damage:`, `:tick:`), the
	// values it starts each play session with (health, most likely), and the
	// level's answers to that script's `param`s.
	//
	// These are the authored copies. ApplyPlayerScript puts them onto
	// PlayerEntity — attributes cloned, since a script writes those during
	// play and the level must still hold what the author wrote when play ends.
	Script       string
	Attributes   AttrSet
	ScriptParams AttrSet
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
//
// The error is a mistyped attribute or param, which stops the load for the
// reason it stops an entity type's: a script finding health missing later is
// a worse place to learn about it.
func PlayerSettingsFromRaw(r *RawPlayer) (PlayerSettings, error) {
	var p PlayerSettings
	if r == nil {
		return p, nil
	}
	if r.StepHeight != nil {
		p.StepHeight = *r.StepHeight
	}
	for _, rm := range r.Models {
		p.Models = append(p.Models, entityModelFromRaw(rm))
	}
	p.Script = r.Script
	attrs, err := attrsFromRaw(r.Attributes)
	if err != nil {
		return PlayerSettings{}, fmt.Errorf("player attributes: %w", err)
	}
	params, err := attrsFromRaw(r.Params)
	if err != nil {
		return PlayerSettings{}, fmt.Errorf("player params: %w", err)
	}
	p.Attributes, p.ScriptParams = attrs, params
	return p, nil
}

// PlayerSettingsToRaw writes the block back, returning nil for settings that
// are still the default. That nil is what keeps every level authored before
// this block existed byte-identical when the editor saves it.
//
// Models can't join the plain equality Empty used to be: a struct holding a
// slice cannot be compared with ==, and an empty-but-non-nil slice would have
// to count as "unset" anyway, the same way StepHeight's own zero does.
func PlayerSettingsToRaw(p PlayerSettings) *RawPlayer {
	if p.StepHeight == 0 && len(p.Models) == 0 && p.Script == "" &&
		len(p.Attributes) == 0 && len(p.ScriptParams) == 0 {
		return nil
	}
	r := &RawPlayer{}
	if p.StepHeight != 0 {
		sh := p.StepHeight
		r.StepHeight = &sh
	}
	for i := range p.Models {
		r.Models = append(r.Models, entityModelToRaw(&p.Models[i]))
	}
	r.Script = p.Script
	r.Attributes = attrsToRaw(p.Attributes)
	r.Params = attrsToRaw(p.ScriptParams)
	return r
}

// ApplyPlayerScript puts a level's player behaviour onto the engine-owned
// player: its script, its params, and a fresh copy of its starting attributes.
//
// Called as a play session's scripts load (script.ScriptRuntimeLoad), which is
// every entry into play, every level change and every release boot — the
// moments a player should come back to full health. Not at level load: the
// editor loads a level long before anyone plays it, and a second play session
// must not start with the health the first one ended on, which the level load
// alone would leave in place.
//
// The player is a no-op when nil, which it is in tests and tools that never ran
// the app's startup.
func ApplyPlayerScript(lvl *Level) {
	if PlayerEntity == nil || lvl == nil {
		return
	}
	PlayerEntity.Script = lvl.Player.Script
	PlayerEntity.Attributes = lvl.Player.Attributes.Clone()
	PlayerEntity.ScriptParams = lvl.Player.ScriptParams.Clone()
}
