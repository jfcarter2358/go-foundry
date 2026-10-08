# Scatter

A scatter volume grows things: GPU-instanced grass, rocks, wildflowers,
cattails or moss, or a whole [biome](30-terrain.md) of trees and undergrowth,
planted on whatever geometry is underneath it.

It is decoration. It never collides, and the player walks on the brush faces
under it.

On [terrain](30-terrain.md), paint biomes instead: they grow on walls and
under overhangs as well as on open ground, and choose what grows where from
the shape of the land.

## Making one

1. `Add... > Volume > Place By Clicking`, and click where the vegetation goes.
2. Size it with the scale gizmo, or the half-extents under **Shape**.
3. Tick **This volume grows things** under **Scatter**.

That is the whole setup. The volume is a region, not a surface: put it over the
ground you want covered and make it tall enough to enclose the plants. What
actually decides where an instance lands is a ray cast straight down through
the level's brushes, so a hillside, a staircase and a displaced rock face all
work without being told about.

Volumes may overlap — that is how you layer looks, grass under wildflowers
under scattered rock.

## What grows

| Kind | What it is |
| --- | --- |
| `grass` | Tapered blades. The only kind that clumps into tufts. |
| `wildflower` | A stem with a bloom, optionally spiked, centred and leafed. |
| `cattail` | A tall reed with a seed head. Not height-capped — reeds read fine tall. |
| `rock` | Small faceted blobs. Sparse, and the only kind that reads correctly on a cliff. |
| `moss` | Low squashed clumps. Rock's shading, foliage's density. |
| `biome` | A terrain biome's species — trees, shrubs, ferns, logs, rocks. See [Biome volumes](#biome-volumes). |

**Density** thins the lattice out; it does not spread it. The spacing is fixed
per kind, so density is how many of the available slots survive.

**Draw range** is a ring that follows the *camera*, not a property of the
volume's size. A volume a hundred metres across with a forty-metre range draws
forty metres of grass around wherever you are standing, and costs the same
whatever the volume's own size.

## Where it will and will not grow

**Min/Max slope** (degrees from level) gate which parts of the surface take
anything: grass on the flatter tops of an outcrop, rock on its faces. The limits
are feathered over a few degrees, so vegetation thins out at the boundary rather
than stopping on a line.

Nothing grows where the ray finds no ground — under an overhang, or off the edge
of every brush. A volume floating clear of the floor still finds it: the cast
reaches a few metres past the volume in both directions, because a volume placed
a little high is the ordinary mistake and looking broken is a worse answer than
looking misplaced.

Only faces that could be walked on count: a face pointing downward or sideways
is not ground, a non-solid brush is not ground, and neither is a face with
**Drawn** or **Collision** switched off.

## Softening the edges

Three controls, all of which *shrink* instances toward their root rather than
fading them out — the pass draws opaque, so a fade would be a hard cut with
extra steps.

- **Range fade** — the fraction of the draw range the camera ring fades over.
- **Edge fade** — how far in from the volume's own boundary an instance takes
  to reach full size. Off by default: it costs a per-instance distance query,
  so it is worth paying for where the edge will be seen up close.
- **Boundary jitter** — bulges the circular boundaries in and out, so the
  transition reads as an organic patch rather than a circle centred on you.

## Cost

Instances are generated in the vertex shader from `gl_InstanceID`; there is no
CPU-side list of blades and nothing is placed as a scene object. Cost is bounded
by the draw range, not by world size.

Three LOD tiers (grass, wildflowers, cattails and moss) swap to cheaper meshes
with distance, the outermost being a pre-baked clump so a sparse far lattice
still reads as dense. Rock has one tier — sparse, individually read, with no
group mesh worth building.

**Shadows** are the expensive half. Rock and moss cast by default; foliage does
not, because a blade thinner than a shadow texel aliases into flickering speckle
rather than a shadow. F5/F6/F7 toggle them live against the frame time in the F3
overlay, which is the way to decide.

## Biome volumes

Kind `biome` grows a [terrain biome](30-terrain.md)'s species on brush ground:
pick the biome, and the volume fills with what the biome grows.

```yaml
volumes:
- id: woods
  position: {x: 0, "y": 5, z: 40}
  half: {x: 60, "y": 8, z: 40}
  scatter:
    kind: biome
    edge_fade_distance: 10
    biome:
      preset: forest
      density: 1.2
      amounts: {grass: 0, broadleaf_tree: 1.5}
```

These are real plant instances rather than a lattice. The volume places them
once, on the ground under it, and the foliage renderer draws them the same way
it draws a painted terrain's plants. Trees, shrubs, rocks and logs draw as
Nanite foliage, with wind and shadows; ground cover keeps its usual tiers. Each
species brings its own size, look and draw distance, so the lattice controls
(density, draw range, tint, size) are hidden for this kind.

- **Biome**, **Density**, **Tint** and **Amounts** work as on a terrain's biome
  slot. An amount of 0 removes that kind of plant.
- **Min/Max slope** narrows each species' own slope range. It never widens it.
- **Edge fade** thins the plants over that distance in from the boundary,
  frayed by noise. 0 is a hard edge.

Some of a biome's ecology needs terrain blocks to read, and is ignored here:

- Sky exposure, the foot of cliffs, moisture and headroom are ignored.
- Only species that grow on the ground take part. Wall and ceiling growers, and
  vines hung over a cliff's lip, have nothing to grow from.

A volume regrows when it moves, when its settings change, when the brushes
under it change, or when the biome file is saved. While you drag it in the
editor, it waits until you stop. Growing a very large volume is capped at
400,000 candidates. Past that cap, the densest species (grass, litter) are
spaced out and the trees are left alone.

## From a script

Scatter is a property of the volume, like fog:

```
@meadow.prop.scatter = true
@meadow.prop.scatter_kind = "wildflower"
@meadow.prop.scatter_density = 0.3
@meadow.prop.scatter_range = 60
@meadow.prop.scatter_color = vec(0.8, 0.9, 0.6)
```

Writing any of them to a volume that has no scatter block gives it one at the
defaults first, the same auto-vivify `fog_density` does.

## What this replaced

Scatter used to be *painted*: a polygon drawn on a sector's floor in a top-down
map, carrying a brush that said what grew there. Sectors are gone, and a brush
face has no floorplan to draw on.

Two other things went with the polygon and have not come back as they were:

- **Ground patches** — a conforming, displaced, textured mesh under the paint.
  Nothing drapes one now. A brush *is* the ground, and shape in it comes from
  subdividing the brush's own faces, so an author who wants rolling terrain
  builds it rather than switching it on.
- **Water** — a plane the Water kind auto-managed inside its polygon. Nothing
  replaces it yet; it needs a water volume, which is its own piece of work.
