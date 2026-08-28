package script

// script_actions_attach.go — the attach verbs.
//
//	attach @flame @guard                  ride an entity
//	attach @flame @guard "hand.R"         ride a bone, when the entity has one model
//	attach @flame @tank "turret" "muzzle" ride a bone in a named attachment
//	attach @smoke "ship"                  ride a frame — a vehicle's local space
//	detach @flame                         back to world space
//
// This is the runtime half of what the editor's Attach panel authors
// (editor_attach_panel.go): both write the same world.Attach, so an emitter
// bound by a script and one bound in the editor are the same emitter, and the
// level format grew nothing to support this.
//
// Only sprites and emitters, because Attach is the binding those two have. An
// entity rides a vehicle through `mount`, which is a different idea with a
// different answer to collision, seats and dismounting, and conflating the two
// would give an entity two ways to be somewhere else.
//
// The asymmetry between the pair is deliberate, and it is the one thing to know:
//
//   - `attach` leaves Position alone, because a position authored as an offset
//     is what you want on the far side. `spawn_effect(fx, <0,0,0>)` then
//     `attach(e, @guard, "hand.R")` puts the effect *on the hand*, which is the
//     whole reason anyone reaches for this. Rebasing it to "wherever it already
//     was" would strand every such effect at its spawn point.
//   - `detach` rebases Position to where the object currently is in the world,
//     because the local offset it was carrying means nothing once there is
//     nothing to be local to — a plume released from a sinking ship should stay
//     over the water, not snap back to the level origin. Rotation is left as
//     authored; there is no way to express "the carrier's heading, frozen" in
//     Euler angles that survives the carrier turning, so an emitter aimed by its
//     carrier keeps aiming the way it was authored.

import (
	"fmt"
	"strings"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
	"foundry/internal/world"
)

// attachTarget is the writable half of one attachable object: the binding, the
// position whose meaning the binding changes, and where it currently is.
type attachTarget struct {
	id    string
	at    *world.Attach
	pos   *mgl32.Vec3
	world func(lvl *world.Level) mgl32.Vec3
}

// verbAttachable resolves an argument to a sprite or an emitter.
//
// The pointers it hands back are into the level's slices, so they are good for
// exactly as long as nothing appends to one. Every caller here writes
// immediately, which is the same contract verbEntity works under.
func (rt *ScriptRuntime) verbAttachable(args []flang.Value, i int, name string) (attachTarget, error) {
	if i >= len(args) {
		return attachTarget{}, fmt.Errorf("%s needs a sprite or an emitter", name)
	}
	r, ok := args[i].(flang.RefVal)
	if !ok {
		if _, isNull := args[i].(flang.NullVal); isNull {
			return attachTarget{}, fmt.Errorf("%s was given null — an `@name` that matches nothing in the level resolves to null, so check the object's ID", name)
		}
		return attachTarget{}, fmt.Errorf("%s takes a sprite or an emitter, not a %s", name, flang.TypeOf(args[i]))
	}
	switch r.Kind {
	case flang.RefSprite:
		sp := rt.cache.spriteFor(r)
		if sp == nil {
			return attachTarget{}, &scriptMissingError{r.String()}
		}
		return attachTarget{id: sp.ID, at: &sp.Attach, pos: &sp.Position, world: sp.WorldPosition}, nil
	case flang.RefEmitter:
		e := rt.cache.emitterFor(r)
		if e == nil {
			return attachTarget{}, &scriptMissingError{r.String()}
		}
		return attachTarget{id: e.ID, at: &e.Attach, pos: &e.Position, world: e.WorldPosition}, nil
	}
	return attachTarget{}, fmt.Errorf(
		"%s takes a sprite or an emitter; %s is %s — an entity rides a vehicle with mount, not attach",
		name, r.String(), aKind(r.Kind))
}

// attachVerbs is merged into scriptVerbs at init, alongside the particle ones.
var attachVerbs = map[string]scriptVerb{
	"attach": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		t, err := rt.verbAttachable(args, 0, "attach")
		if err != nil {
			return nil, err
		}
		if len(args) < 2 || len(args) > 4 {
			return nil, fmt.Errorf(
				"attach takes (sprite or emitter, entity), (…, entity, bone), (…, entity, model, bone) or (…, frame name)")
		}

		// A string in the carrier slot names a frame rather than an entity: the
		// two are different spaces even when a vehicle owns both, and the frame
		// is the one that exists without the entity's own model matrix in it.
		if frame, isStr := verbStrAt(args, 1); isStr {
			if len(args) != 2 {
				return nil, fmt.Errorf("attach to a frame takes just the frame's name; a bone hangs off an entity, not off a frame")
			}
			return rt.attachToFrame(t, frame, pos), nil
		}

		e, err := rt.verbEntity(args, 1, "attach")
		if err != nil {
			return nil, err
		}

		// Two spellings for a bone: (obj, entity, bone) reads the entity's only
		// attachment, which is how nearly every entity is authored, and
		// (obj, entity, model, bone) names one of several — the same pair
		// play_anim and set_texture offer, for the same reason.
		model, bone := "", ""
		switch len(args) {
		case 2:
		case 3:
			if bone, err = verbStr(args, 2, "attach"); err != nil {
				return nil, err
			}
		case 4:
			if model, err = verbStr(args, 2, "attach"); err != nil {
				return nil, err
			}
			if bone, err = verbStr(args, 3, "attach"); err != nil {
				return nil, err
			}
		}
		if bone != "" {
			if why := e.BoneProblem(model, bone); why != "" {
				rt.warnAt(pos, "attach did nothing: %s", why)
				return flang.BoolVal(false), nil
			}
			model = e.BoneModel(model)
		}

		rt.markDirty()
		*t.at = world.Attach{Entity: e.ID, Model: model, Bone: bone}
		return flang.BoolVal(true), nil
	},

	"detach": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		t, err := rt.verbAttachable(args, 0, "detach")
		if err != nil {
			return nil, err
		}
		if t.at.IsZero() {
			// Not a warning: asking something already loose to come loose is a
			// reasonable line to write, and `false` says it did nothing.
			return flang.BoolVal(false), nil
		}
		rt.markDirty()
		// Read the world position through the binding that is still in place,
		// then clear it — the other order would resolve against the identity and
		// leave the object exactly where the local offset happened to point.
		here := t.world(rt.lvl)
		*t.at = world.Attach{}
		*t.pos = here
		return flang.BoolVal(true), nil
	},
}

// attachToFrame binds to a moving coordinate system by name.
//
// A frame exists because something is parented to that vehicle, not because an
// entity carries a VehicleSpec — so "there is no frame called that" is a real
// and common answer, and worth saying rather than binding to a name that will
// resolve to the identity forever.
func (rt *ScriptRuntime) attachToFrame(t attachTarget, frame string, pos flang.Pos) flang.Value {
	if frame == "" {
		rt.warnAt(pos, "attach: a frame needs a name; detach is how you go back to world space")
		return flang.BoolVal(false)
	}
	if rt.lvl == nil || rt.lvl.Frames.Frame(frame) == nil {
		msg := fmt.Sprintf("attach did nothing: there is no frame called %q", frame)
		if e := world.FindEntity(rt.lvl, frame); e != nil {
			msg += fmt.Sprintf(" — %q is an entity, so write `attach(@%s, @%s)` to ride the entity itself, or park something on the vehicle first to give it a frame",
				frame, t.id, frame)
		}
		rt.warnAt(pos, "%s", msg)
		return flang.BoolVal(false)
	}
	rt.markDirty()
	*t.at = world.Attach{Frame: frame}
	return flang.BoolVal(true)
}

// aKind renders a ref kind with the article that reads: three of the kinds
// begin with a vowel, and "a entity" in a message is a stumble in the middle of
// a sentence someone is trying to act on.
func aKind(k flang.Kind_) string {
	s := k.String()
	if s != "" && strings.ContainsRune("aeiou", rune(s[0])) {
		return "an " + s
	}
	return "a " + s
}
