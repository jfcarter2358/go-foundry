package script

// script_actions_bone.go — where a bone is, in the world.
//
// A socket is a bone (docs/guide/11-entities.md), and a muzzle flash, a spark
// off a blade or a shell ejected from a breech all want the same answer: the
// point that bone is at right now, posed and carried by its entity. Attaching
// an emitter answers it continuously; this answers it once, for a one-shot
// `spawn_effect` or `play_sound_at`.

import (
	"fmt"

	"foundry/internal/flang"
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

var boneVerbs = map[string]scriptVerb{
	"bone_position": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		// Two spellings, the pair play_anim offers: an attachment ref and a
		// bone, or an entity, an attachment name and a bone.
		var entityRef flang.RefVal
		var model, bone string
		switch len(args) {
		case 2:
			r, ok := args[0].(flang.RefVal)
			if !ok || r.Kind != flang.RefModel {
				return nil, fmt.Errorf("bone_position takes (@entity.model.name, bone) or (entity, model, bone)")
			}
			entityRef = flang.RefVal{Kind: flang.RefEntity, A: r.A}
			model = r.B
			bone, _ = verbStrAt(args, 1)
		case 3:
			r, ok := args[0].(flang.RefVal)
			if !ok {
				return nil, fmt.Errorf("bone_position's first argument is an entity, not a %s", flang.TypeOf(args[0]))
			}
			entityRef = r
			model, _ = verbStrAt(args, 1)
			bone, _ = verbStrAt(args, 2)
		default:
			return nil, fmt.Errorf("bone_position takes (@entity.model.name, bone) or (entity, model, bone)")
		}
		e := rt.host.entityFor(entityRef)
		if e == nil {
			return nil, fmt.Errorf("bone_position: %s is not an entity in the level", entityRef)
		}
		p, err := bonePosition(rt.lvl, e, model, bone)
		if err != nil {
			return nil, err
		}
		return vecValue(p), nil
	},
}

// bonePosition is the world-space point one bone of one attachment is at.
//
// The player's attachments are the viewmodel, which RenderViewmodel draws with
// the camera as their base rather than the body's own matrix, so the point has
// to be carried out the same way or a flash would appear where the player's
// feet are instead of at the gun on screen.
func bonePosition(lvl *world.Level, e *world.Entity, model, bone string) (mgl32.Vec3, error) {
	m := e.Model(model)
	if m == nil {
		return mgl32.Vec3{}, fmt.Errorf("bone_position: @%s has no attachment called %q", e.ID, model)
	}
	bm, ok := world.EntityBoneMatrix(e, model, bone)
	if !ok {
		return mgl32.Vec3{}, fmt.Errorf("bone_position: @%s.model.%s has no bone %q, or its model has not loaded", e.ID, model, bone)
	}
	var base mgl32.Mat4
	switch {
	case e == world.PlayerEntity:
		base = gfx.Cam.Matrix.Inv()
	case lvl != nil:
		base = lvl.Frames.FrameFor(e).WorldMatrix().Mul4(e.ModelMatrix())
	default:
		base = e.ModelMatrix()
	}
	p := base.Mul4(bm).Mul4x1(mgl32.Vec4{0, 0, 0, 1})
	return mgl32.Vec3{p.X(), p.Y(), p.Z()}, nil
}
