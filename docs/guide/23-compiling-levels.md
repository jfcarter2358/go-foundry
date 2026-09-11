# Compiling levels

A level you author is a list of brushes. That is the right thing to *edit*, and
the wrong thing to *ship*: it contains surfaces nothing can ever see, it has no
structure to cull against, and every pixel of it recomputes lighting that never
changes.

**Compiling** turns it into the form a released game loads. It runs four passes,
each of which throws away work the game would otherwise do every frame:

| | |
| --- | --- |
| **CSG** | Removes the surfaces buried inside other brushes — the underside of a floor slab, the back of a shared wall, the disc a pillar stands on. |
| **BSP** | Cuts the level into convex cells, so that "where is the camera" is a handful of comparisons. |
| **Visibility** | Works out which cells can see which, through the openings between them. A room you are not in then costs nothing at all. |
| **Lighting** | Bakes the light onto the vertices, once, instead of onto the pixels, every frame. |

A brush's **detail mesh** ([Shaping surfaces](03-shaping-surfaces.md)) is drawn
and lit like anything else and takes no part in the second and third passes: the
tree is cut with the solids' own planes and the detail is dropped into the cells
they made. So a level compiles to the same partition, the same portals and the
same visibility whether or not you have carved its walls — detail costs draws,
not tree — and a wall you have detailed still seals the level exactly as the flat
one did.

You compile from the toolbar's **Compile** button, or from the command line:

```
go run ./cmd/foundrycompile              # every level under resources/levels
go run ./cmd/foundrycompile canyon       # one, by name
```

Either way the result is written beside the level: `canyon.yaml` gets a
`canyon.fbsp`. Recompile whenever the geometry changes; nothing rebuilds it for
you.

The command works on a project: `-root` is the directory holding `resources/`,
and defaults to the current one, so run it from the repository root or a game's
project directory.

## Looking at what it made

The numbers say whether your level is sealed. They say nothing about whether it
*looks* right, and a compiled level can be wrong in ways only the eye catches: a
seam where a texture restarts, a surface CSG took that it should not have, a
shadow baked into the wrong place.

The toolbar's **Preview** button next to Compile is for that. It compiles into
memory and draws the result the way a release build would — compiled surfaces,
culled by the compiled visibility set — so what you are looking at is the
shipped thing rather than an impression of it. `F3`'s overlay grows a
`Compiled: N surfaces / M drawn` line while it is up, which is the culling in
one number.

**Any edit turns it off.** A preview is a picture of the level as it was when it
was built, and an edit is the moment it stops being one.

## What actually uses it

Nothing, while you are editing. Play in the editor evaluates the brushes live,
every frame, so what you see is the level and not the last compile of it — an
editor whose view lagged a compile behind your edits would be worse than a slow
one. (Preview, above, is the deliberate exception, and it is the reason it turns
itself off.)

A **release build** is the other way round: it loads a `.fbsp` and nothing else.
That is where the four passes above are spent —

- CSG's removed surfaces are never submitted, in any pass.
- The visibility set skips whole rooms before a single bounding box is tested.
  `F3`'s overlay shows it as `Compiled: N surfaces / M drawn`; in a level built
  out of rooms M should be a fraction of N.
- Baked lighting replaces the per-fragment light loop, its cascade selection and
  its shadow-map samples, for every pixel of static geometry.

— which means the shipped game is the *faster* of the two, and that the editor
is the pessimistic case rather than the optimistic one.

`make release-linux` compiles every level first, with `-strict`, so a leaking
level fails the build. See [Shipping a game](20-shipping.md).

## What stays a brush

A compiled level keeps its brushes; they are just no longer what gets drawn.
Collision resolves a body against a brush's own shape, a script addresses one by
name, and a ray reports the brush it hit — none of which changes, and none of
which would be better against a soup of clipped fragments than against the
solids you placed.

So a compiled level behaves exactly like the level it came from. If it does
not, that is a bug rather than a trade-off.

## Your level has to be sealed

This is the one rule the compiler imposes, and it is worth understanding rather
than working around.

Visibility is computed over the *inside* of the level. To find the inside, the
compiler floods in from far outside — well beyond anything you built — and
everything that flood reaches is declared **outside**: the sky above your roof,
the dirt under your floor, the far side of every wall. What is left is your
level, and that is what gets cells, portals and a visibility set.

If there is a **gap** in the shell — two brushes that do not quite meet, a
missing ceiling, a wall someone deleted — the flood pours in through it and
everything it touches becomes outside. Your level, from the compiler's point of
view, stops existing.

That is a **leak**, and the compiler tells you:

```
canyon: faces 42 -> 345, 29 nodes, 30 cells (7 solid, 23 void, 0 interior), 0 portals
canyon: LEAK — the player spawn at [-15 1 0] is not sealed in: the void outside the level reaches it through a gap near [-12 4 0]
```

The giveaway is `0 interior` cells. A sealed level has some.

The spawn and the entities are only what gets *named*. Whatever they happen to
be standing in, the point after "a gap near" is the thing to go and look at: it
is where the void comes in on its way to them — the middle of an opening that
stands onto nothing, like a hangar door open to the sky or a room with no
ceiling. It is roughly placed rather than exact, so look around it.

A dynamic brush does not seal (a door is a hole as far as this is concerned),
and neither does a face marked no-draw on a detailed brush, so a gap one of
those is covering in the editor is still a gap here.

A leak does **not** fail the compile. The output is still correct — it is just
that nothing is culled, so you have paid for the pass and got nothing. (For a
release build that must not ship one, `foundrycompile -strict` makes it an
error.)

### The seal box

Every level has one, and it is what normally does the sealing. It is an
invisible box around the level — **Seal Box** in the Explorer, a thin wireframe
in the editor, and nothing at all in play: nothing collides with it and nothing
draws it. The compiler wraps it in solid walls of its own, so the flood stops
there, and everything inside it counts as inside the level — a hangar door open
to space, a courtyard, a canyon.

A level that has never had one gets one fitted when it opens: around every brush
that compiles, the spawn and the entities, with 4 units to spare. After that it
is yours. Select it from the Explorer (or click it from outside, where there is
nothing else under the cursor), then move it with **G** and resize it with **C**
— the scale gizmo drags its half-extents — or type the numbers into Properties.
**Fit To Level** refits it after the level has grown.

What follows from it:

- **Something placed outside the box is a leak** — `entity ship at [...] is
  outside the level's seal box` — because it is outside the level. Move it in,
  or make the box bigger.
- **The part of a brush outside the box is never drawn from inside the level.**
  The compile names any brush that reaches past it, in a warning.
- **Sunlight is not blocked by it.** Past the box is sky.

Untick **Seals the level** in its Properties to switch it off. The level then
has to seal itself with its own walls, the old way, and every gap in them is a
leak — which is what you want for an indoor level that is meant to be closed and
should tell you when it is not.

### Two things that do not count as sealing

- **Disabled chunks.** A chunk switched off is not in the world at compile time,
  so if part of your shell is one, the level leaks through the hole it leaves.
- **Frame-owned chunks** — a vehicle's interior. It moves, so it cannot be part
  of a static shell, and it is not compiled at all (see below).

## What does not get compiled, and what does not get linked

Two kinds of chunk stay as authored brushes and are built the ordinary way at
load:

- **Frame-owned chunks**, whose geometry rides a vehicle. A moving room cannot
  be baked into a fixed list of world-space polygons.
- **Disabled chunks**, which a script can switch on later. Baking one in would
  quietly make it permanent.

Everything else about a level — entities, volumes, lights, the spawn, scripts,
UI screens — is carried through unchanged. Only the static geometry is
transformed.

**Prefabs are the exception: they are resolved.** A prefab's link exists so that
editing one file updates twenty placements, which is an authoring convenience;
by the time you compile, it has done its job. A compiled level holds what the
placements made, not the placements, so that a build cannot come out different
from what you compiled because someone edited a prefab in between.

An **entity definition's own geometry** — a ship's hold — is the opposite case
and stays linked. It is frame-owned, so it was never baked, and it is what makes
an entity type one thing rather than twenty frozen copies.

## Reading the report

```
canyon: faces 172 -> 891, 96 nodes, 121 cells (58 solid, 58 void, 5 interior),
        14 portals, 2.4 cells visible on average
```

- **faces N -> M.** N is what you authored; M is what came out. It usually goes
  *up*, because faces are cut on a grid before lighting so that baked shadows
  have somewhere to land — `-grid` controls how fine (default 4 units; smaller
  means sharper shadows and more geometry).
- **cells.** *Solid* is inside brush material, *void* is outside the level,
  *interior* is space you can stand in. Interior is the number that matters.
- **cells visible on average.** How many cells a typical one can see. Close to
  the interior count means visibility is buying you nothing — either the level
  is one big open room, or it leaks.

## A concave brush is an error here

The editor lets you drag a vertex until a brush is no longer convex, and warns
you rather than refusing — an editor you cannot pass through an invalid shape in
is one you cannot shape anything in. The compiler is the other end of that
bargain: convexity is what makes "inside this brush" a local test, and CSG,
collision and the cell labelling all depend on it. Compiling reports every
concave brush by name and stops.

Fix it by moving the vertex back, or by splitting the shape into two brushes —
which is what an id-lineage editor would have made you do all along, and is why
extrude only ever pushes a face along its own normal.
