package levelio

// entity_emitters.go — turning an entity's effects into level emitters.
//
// A definition says "this type has a thruster plume here" (world/
// entity_emitter.go). This is what makes that true in a level: one runtime
// Emitter per attachment, its document loaded, attached to the entity, and
// tagged with the entity it came from so saving knows not to write it back out.
//
// It is entity_geometry.go's twin, deliberately down to the shape of the
// functions, because it is the same arrangement applied to the other kind of
// carried content:
//
//	brushes   → one frame-owned Chunk   (EntityGeometryChunk)
//	emitters  → one Emitter each        (EntityEmitterInstances)
//
// The two ends that matter here:
//
//   - The emitter's Attach names the entity, so EmitterUpdate resolves its
//     origin through the entity's own transform every frame and a plume follows
//     the ship with no code of its own. With a bone named it resolves through
//     that bone's posed placement instead, which is what puts a flame in a
//     hand that is swinging.
//   - The emitter's FromEntity is the entity's ID, and is what LevelToRaw
//     checks before writing an emitter out. A materialised emitter is the
//     *entity's* content; a level that saved a copy would write the same
//     placement twice and the two would drift.
//
// # Why the level's list and not the entity's own simulation
//
// An Emitter in lvl.Emitters is already simulated by EmitterUpdate, already
// gathered by the instanced sprite pass, already culled, already addressable by
// a script as `@ship.thruster`, and already visible in the editor's Explorer
// and its bounds overlay. Simulating one inside Entity instead would mean a
// second copy of every one of those. The cost is that these entries have to be
// kept in step with the entities that own them, which is exactly the bargain
// the materialised chunk already makes and is why both are swept by the same
// callers.

// The functions themselves live in world/entity_emitter.go rather than here,
// which is the one place this twin diverges from entity_geometry.go.
// Materialising brushes uploads vertex buffers and resolves textures, so it has
// to be on the loader's side of the split; materialising an effect uploads
// nothing, so it can sit beside the description it reads — and the script
// runtime, which cannot import this package, can then drop an entity's emitters
// when a `destroy` takes the entity away.
//
//	world.EntityEmitterInstance    one attachment  → one Emitter
//	world.EntityEmitterInstances   an entity's     → its Emitters, documents read
//	world.MaterializeEntityEmitters                → appended to a level
//	world.DropEntityEmitters       the sweep that takes them back out
