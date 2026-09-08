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

You compile from the toolbar's **Compile** button, or from the command line:

```
go run ./cmd/foundrycompile              # every level under resources/levels
go run ./cmd/foundrycompile canyon       # one, by name
```

Either way the result is written beside the level: `canyon.yaml` gets a
`canyon.fbsp`. Recompile whenever the geometry changes; nothing rebuilds it for
you.

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
canyon: LEAK — the player spawn is at [-15 1 0], which is outside the level.
```

The giveaway is `0 interior` cells. A sealed level has some.

A leak does **not** fail the compile. The output is still correct — it is just
that nothing is culled, so you have paid for the pass and got nothing. (For a
release build that must not ship one, `foundrycompile -strict` makes it an
error.)

### Sealing an outdoor level

The same rule applies to a level with sky in it: the flood does not care that
you meant it to be open. An outdoor level is sealed the way one always has been
— with a shell of brushes around the whole thing, textured with your sky. The
player never reaches them, and they are what keeps the void out.

### Two things that do not count as sealing

- **Disabled chunks.** A chunk switched off is not in the world at compile time,
  so if part of your shell is one, the level leaks through the hole it leaves.
- **Frame-owned chunks** — a vehicle's interior. It moves, so it cannot be part
  of a static shell, and it is not compiled at all (see below).

## What does not get compiled

Two kinds of chunk stay as authored brushes and are built the ordinary way at
load:

- **Frame-owned chunks**, whose geometry rides a vehicle. A moving room cannot
  be baked into a fixed list of world-space polygons.
- **Disabled chunks**, which a script can switch on later. Baking one in would
  quietly make it permanent.

Everything else about a level — entities, volumes, lights, the spawn, scripts,
UI screens, prefab placements — is carried through unchanged. Only the static
geometry is transformed.

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
