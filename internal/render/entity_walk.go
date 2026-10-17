package render

// entity_walk.go — the entities the world passes draw: the level's own, and
// the player's third-person body.
//
// The body (world.PlayerBodyEntity) is engine-owned and not in Level.Entities,
// for PlayerEntity's reason. Every walk that draws entities — the colour pass,
// its depth prepass, the cascades, the cube shadows and the VSM movers — goes
// through these two so the body is drawn, shadowed and shadow-cached exactly
// like a placed character, and cannot be in one pass and missing from another.
// It is appended after the level's entities, so its index is
// len(lvl.Entities): stable while the level's list is, and when that list
// changes the caches keyed by index see a new signature anyway.

import "foundry/internal/world"

// drawnEntityCount is how many entities the world passes walk.
func drawnEntityCount(lvl *world.Level) int {
	if lvl == nil {
		return 0
	}
	n := len(lvl.Entities)
	if world.DrawnPlayerBody() != nil {
		n++
	}
	return n
}

// drawnEntity is the i'th of them, for i < drawnEntityCount(lvl).
func drawnEntity(lvl *world.Level, i int) *world.Entity {
	if i < len(lvl.Entities) {
		return &lvl.Entities[i]
	}
	return world.PlayerBodyEntity
}
