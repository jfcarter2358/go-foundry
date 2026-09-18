package world

// entity_waypoints.go — the waypoint animations an entity carries.
//
// A ship's blast door sliding shut, a turret's barrel swinging out of its
// housing, a lift car inside a moving platform: paths that belong to a *thing*
// rather than to a level, and so have to go wherever it goes. A level's own
// animation is written in world space, which is exactly wrong for them — the
// door's arrows would stay on the landing pad while the ship lifted off.
//
// It is the arrangement entity_emitter.go and entity_volume.go already use. A
// definition holds animations written in the ENTITY's local space, the space its
// brushes are authored in; a level materialises one live WaypointAnimation per
// placement, with ID `<entity>.<name>` and FromEntity naming the entity. That
// FromEntity is the whole contract:
//
//   - WaypointAnimation.SpaceMatrix reads it as the space the points are in, so
//     every follower is carried to the arrow where the entity is NOW, tick by
//     tick (waypoint_follow.go);
//   - LevelToRaw skips an animation that has it, and DropEntityWaypointAnimations
//     sweeps them, so the level never holds a stale copy of the type's path.
//
// Because the live animation is an ordinary level one, `play_waypoints`, the
// `:waypoint:` events, `@obj.prop.waypoints` and the level editor's preview all
// work on it unchanged. A script names it through the placement —
// `play_waypoints(@self.hatch, @self.door_close)` — the way it names the
// placement's brushes.
//
// No per-instance overrides: a path is one coherent shape, and an instance that
// wanted a different one would be a different type. A typeless entity, which has
// no definition to inherit from, writes its list whole (EntityToRaw).

import (
	"fmt"
	"reflect"
)

// EntityWaypointAnimationID is the level-unique ID one of an entity's waypoint
// animations carries once materialised: the entity's ID and the animation's
// name, joined — EntityBrushID's rule, so `@ship.door_close` reads the way
// `@ship.hatch` does.
func EntityWaypointAnimationID(entityID, name string) string {
	if entityID == "" {
		return name
	}
	return entityID + "." + name
}

// HasWaypointAnimations reports whether the entity carries any path at all.
func (e *Entity) HasWaypointAnimations() bool { return e != nil && len(e.WaypointAnimations) > 0 }

// WaypointAnimationNamed returns the definition's animation with that name, or
// nil. The uniqueness test the entity editor's rename runs.
func (d *EntityDef) WaypointAnimationNamed(name string) *WaypointAnimation {
	if d == nil {
		return nil
	}
	for i := range d.WaypointAnimations {
		if d.WaypointAnimations[i].ID == name {
			return &d.WaypointAnimations[i]
		}
	}
	return nil
}

// UniqueEntityWaypointAnimationName returns base, or base with a numeric suffix
// no animation in the list is using. What the entity editor's "Add Animation"
// names a new one.
func UniqueEntityWaypointAnimationName(anims []WaypointAnimation, base string) string {
	if base == "" {
		base = "waypoints"
	}
	taken := func(name string) bool {
		for i := range anims {
			if anims[i].ID == name {
				return true
			}
		}
		return false
	}
	if !taken(base) {
		return base
	}
	for n := 2; ; n++ {
		name := fmt.Sprintf("%s_%d", base, n)
		if !taken(name) {
			return name
		}
	}
}

// CloneWaypointAnimations deep-copies an animation list, so a definition and
// every instance stamped from it own their points outright. Empty stays nil, so
// an entity with no paths compares equal to one that never had any.
func CloneWaypointAnimations(anims []WaypointAnimation) []WaypointAnimation {
	if len(anims) == 0 {
		return nil
	}
	out := make([]WaypointAnimation, len(anims))
	for i := range anims {
		out[i] = anims[i].Clone()
	}
	return out
}

// WaypointAnimationsEqual reports whether two lists would be written out the
// same — the test EntityToRaw uses to decide whether an instance restates its
// type's paths.
func WaypointAnimationsEqual(a, b []WaypointAnimation) bool {
	return reflect.DeepEqual(entityWaypointAnimationsToRaw(a), entityWaypointAnimationsToRaw(b))
}

// ---------- serialization ----------

// entityWaypointAnimationsFromRaw converts a definition's (or a typeless
// instance's) list, naming anything that arrived without a name — an unnamed
// animation could not be addressed — and refusing a name used twice.
func entityWaypointAnimationsFromRaw(raw []RawWaypointAnimation, owner string) ([]WaypointAnimation, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]WaypointAnimation, 0, len(raw))
	seen := map[string]bool{}
	for i, r := range raw {
		a := WaypointAnimationFromRaw(r)
		if a.ID == "" {
			a.ID = fmt.Sprintf("waypoints_%d", i+1)
		}
		if seen[a.ID] {
			return nil, fmt.Errorf("%s: duplicate waypoint animation %q", owner, a.ID)
		}
		seen[a.ID] = true
		out = append(out, a)
	}
	return out, nil
}

// entityWaypointAnimationsToRaw is entityWaypointAnimationsFromRaw's inverse.
func entityWaypointAnimationsToRaw(anims []WaypointAnimation) []RawWaypointAnimation {
	if len(anims) == 0 {
		return nil
	}
	out := make([]RawWaypointAnimation, 0, len(anims))
	for i := range anims {
		out = append(out, WaypointAnimationToRaw(&anims[i]))
	}
	return out
}

// ---------- materialising into a level ----------
//
// Beside the emitters' and the volumes', and for their reason: nothing here
// uploads, so the script runtime can build and drop an entity's paths around
// `spawn` and `destroy` without importing the loader.

// EntityWaypointAnimationInstance converts one template into a level animation
// riding the named entity. entityID may be empty, which is what the entity
// editor's preview passes: the animation is then unqualified and in no space but
// its own, which in that window is the entity's.
func EntityWaypointAnimationInstance(entityID string, a *WaypointAnimation) WaypointAnimation {
	cp := a.Clone()
	cp.ID = EntityWaypointAnimationID(entityID, a.ID)
	cp.FromEntity = entityID
	return cp
}

// MaterializeEntityWaypointAnimations builds e's animations into level
// animations riding it and appends them to lvl, reporting how many it added.
func MaterializeEntityWaypointAnimations(lvl *Level, e *Entity) int {
	if lvl == nil || e == nil || !e.HasWaypointAnimations() {
		return 0
	}
	for i := range e.WaypointAnimations {
		a := EntityWaypointAnimationInstance(e.ID, &e.WaypointAnimations[i])
		// Qualified by the entity already, so this only fires when something
		// else in the level happens to have that exact name.
		a.ID = UniqueWaypointAnimationID(lvl, a.ID)
		lvl.WaypointAnimations = append(lvl.WaypointAnimations, a)
	}
	return len(e.WaypointAnimations)
}

// DropEntityWaypointAnimations removes every animation materialised from the
// named entity, and reports how many went.
//
// A sweep rather than a lookup, for DropEntityEmitters' reason: a rename leaves
// the old ID's animations behind, and this is what has to find them. An object
// still following one simply stops where it has got to on its next tick — the
// rule for any animation deleted out from under a follower — unless the same ID
// is materialised again first, as a re-stamp does, when it carries on.
func DropEntityWaypointAnimations(lvl *Level, entityID string) int {
	if lvl == nil || entityID == "" {
		return 0
	}
	kept := lvl.WaypointAnimations[:0]
	dropped := 0
	for i := range lvl.WaypointAnimations {
		if lvl.WaypointAnimations[i].FromEntity == entityID {
			dropped++
			continue
		}
		kept = append(kept, lvl.WaypointAnimations[i])
	}
	lvl.WaypointAnimations = kept
	return dropped
}

// FindCarriedWaypointAnimationNamed returns an animation some entity carries
// under the name its type gave it, or nil — for a check that does not know
// which placement will be asking, such as the script linter reading a type's
// script against a level.
func FindCarriedWaypointAnimationNamed(lvl *Level, name string) *WaypointAnimation {
	if lvl == nil || name == "" {
		return nil
	}
	for i := range lvl.WaypointAnimations {
		a := &lvl.WaypointAnimations[i]
		if a.FromEntity != "" && a.ID == EntityWaypointAnimationID(a.FromEntity, name) {
			return a
		}
	}
	return nil
}

// WaypointFollowerCarrier is the entity f is part of — the entity whose type put
// this brush, emitter or volume in the level — or "" for anything the level
// placed itself.
func WaypointFollowerCarrier(f WaypointFollower) string {
	switch o := f.(type) {
	case brushFollower:
		if o.ch != nil {
			return o.ch.FromEntity
		}
	case *Chunk:
		return o.FromEntity
	case *Emitter:
		return o.FromEntity
	case *Volume:
		return o.FromEntity
	}
	return ""
}

// WaypointFollowerCarriesAnimation reports whether f is the very entity anim is
// carried by. Such an object cannot follow it: every step would move the space
// the next step is measured in, and the entity would chase its own path off
// into the distance. The script verb and the editor's preview both refuse it.
func WaypointFollowerCarriesAnimation(f WaypointFollower, anim *WaypointAnimation) bool {
	if anim == nil || anim.FromEntity == "" {
		return false
	}
	e, ok := f.(*Entity)
	return ok && e != nil && e.ID == anim.FromEntity
}
