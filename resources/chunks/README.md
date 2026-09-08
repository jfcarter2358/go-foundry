# Chunks

A `.yaml` file here is one chunk on its own: a point pool, its sectors, and the
floors, ceilings, walls and portals they carry — the same block a level file
holds inline, saved out where more than one thing can use it.

Two ways in and two ways out.

**Into a level.** Explorer → Chunks → Import drops a copy into the level being
edited, at the camera. From then on it is that level's own geometry; editing the
file here does not change the level, and editing the level does not change the
file.

**Into an entity type.** The entity editor's Contents → Chunks → Import attaches
one to a definition under `entities/`, and every entity placed from that type
materialises it as a chunk bound to the entity's frame — geometry carried around
by the thing that owns it, which is how a drivable ship gets a hold you can walk
around in while it moves. That binding is by *reference*: the level file never
stores the geometry, the definition names this file, and a fix to the hull
reaches every ship in the game. See `internal/world/entity_chunk.go`.

**Out of a level.** Explorer → Chunks → Export writes the selected chunk here.

**Out of the entity editor.** Its import dialog also lists the chunks of the
level open in the main window, and picking one exports it here and attaches it
in the same click — build the hull where hulls are easy to build, then lift it
into the type.

One chunk ships: `dropship_hold.yaml`, the inside of the shuttle in
`entities/dropship.yaml` — the second case above, and the reason it exists. Its
header is the place to read about the one thing that is not obvious in an
entity's chunk: how a doorway out of a moving space is authored, and why its two
halves must overlap rather than meet.

Nothing else has to ship here. A level that never wanted to reuse a piece of
itself never needs a file in this directory.
