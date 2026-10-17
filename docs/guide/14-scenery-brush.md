# Scenery and the brush

Scenery is a model with a position, a rotation, a scale and a collision flag, and
nothing else. No script, no tags, no animation, no authored bounds. It is the
small type beside an entity, for the case where a level wants a thousand of them
— rocks, crates, tufts, debris.

A prop that needs behaviour is an [entity](11-entities.md), not scenery.

## The brush

`Add... > Scenery > Model`. Picking a model **is** arming the brush; there is no
"place by clicking" row, because the brush is not a one-shot.

A translucent ghost of the model follows the cursor onto whatever surface is
under it. Every left-click stamps a copy, and the brush stays armed — because the
thing to do after placing a rock is place another rock.

| Key | |
| --- | --- |
| Left click | place a copy where the ghost is |
| `[` / `]` | turn the ghost 15 degrees (5 with `Shift`) |
| `-` / `=` | shrink / grow the ghost |
| `Ctrl+Z` | take back the last copy placed |
| `Esc` | put the brush away |

The **Scenery Brush** block appears in the panel whenever the brush has a model.
It describes the *next* click rather than anything selected:

| Field | |
| --- | --- |
| **Model** | |
| **Start / Stop** | |
| **Yaw** | same as `[` and `]` |
| **Scale** | uniform, same as `-` and `=` |
| **Random yaw per copy** | each stamp gets its own facing; the ghost keeps the yaw above |
| **Solid** | the collision flag each copy takes |

It also reports how many props are in the level and how many the current view
drew.

**Use As Brush**, on a placed prop's right-click menu or in its panel, arms the
brush with that prop's model, facing and size — the way to carry on a run you
started earlier.

## A placed prop

| Field | |
| --- | --- |
| **ID** | committed on Enter |
| **Model** | `.fmdl` / `.obj` under `models/` |
| **Position / Rotation / Scale** | a real per-axis scale on the model |
| **Solid** | stops the player and any entity that collides with the world |
| **Reload** | re-read the model from disk after editing it |

Collision bounds are the model's own box, carried through the placement. There is
nothing to dial in — a prop dropped at an angle is solid at that angle. A cooked
Nanite mesh can collide as its own shape instead: see
[Nanite collision](#nanite-collision).

The panel says when the mesh is flat or would not load, in which case it collides
with nothing.

## In the Explorer

Props are listed under **Scenery**, capped at 200 rows plus whatever is selected,
then `(N more — click one in the 3D view to select it)`. A brush makes hundreds,
and a tree that drew all of them would cost more per frame than the rest of the
panel put together.

A row reads `(no mesh)` when the model will not load, and `(not solid)` when
collision is off.

## Trees

A tree is placed by hand like a prop, but it is a plant rather than a model.
You choose the species, the variant, the size and which way it faces, and it is
drawn exactly as the plants a biome grows are, with Nanite foliage, wind, shadows
and live GI. Biome paint is for undergrowth; the trees a scene is built around
are yours to place.

`Add... > Tree` lists the trees: the temperate rainforest's eight (Sitka spruce,
western hemlock, western redcedar, Douglas-fir, bigleaf maple, red alder, vine
maple, black cottonwood) and the other biomes' trees. **Other plants** has every
other plant shape, from a nurse log to a sword fern. Choosing one arms
placement, and every left-click plants one with its trunk's foot where you
clicked, turned at random. It stays armed until `Esc`. The combo at the top
picks the variant, or **Random variant**.

A selected tree is outlined as its crown's footprint and the box its trunk
collides as. The move gizmo carries it, the rotate gizmo turns it about its
trunk (only the yaw ring does anything, because a tree grows up), and the scale
gizmo sizes it.

| Field | |
| --- | --- |
| **ID** | committed on Enter |
| **Species / Variant** | any plant shape and any of its meshes |
| **Solid** | its trunk stops the player, entities and shots; the crown never does |
| **Position** | the foot of the trunk |
| **Turn** | degrees about the trunk |
| **Size** | against the species' own: at 1 a Sitka spruce is about 40 units tall |
| **Foliage / Moss & accent** | its colours; **Species colours** puts them back |

The right-click menu has **Move here**, **Plant more like this**, **Solid**,
**Duplicate** and **Delete**. In the Explorer, trees are listed under **Trees**.

In the level file:

```yaml
trees:
- id: spruce_1
  species: sitka_spruce   # any plant shape, as biome files name them
  variant: 1              # optional, from 0
  position: {x: 12, "y": 0, z: -40}
  yaw: 35                 # degrees, optional
  scale: 1.2              # optional, 1 is the species' own size
  color: [0.16, 0.27, 0.13]   # optional, the species' own when absent
  accent: [0.34, 0.44, 0.12]  # optional
  solid: false            # optional, solid when absent
```

A species name the engine does not know is reported when the level loads and
kept as written, so a typo never loses a tree when you save.

## Rendering

Props draw and cast shadows through the same passes entities do. They are solid
to the player and to any world-colliding entity, and they stop a
[hitscan](18-scripting.md) — reporting nothing, since nothing in the language
addresses a scenery placement by name. The impact point still comes back.

Placements of the same model are drawn together: every visible copy of a
model's opaque parts goes out in one instanced draw, in the colour pass and in
each shadow map. So a thousand copies of one rock cost about what a dozen do,
and a level's scenery cost grows with how many *different* models it uses rather
than how many it places. A translucent model is the exception. Its copies still
draw one by one, because blending has to be sorted back to front.

Setting `FOUNDRY_SCENERY_INSTANCING=0` before starting the engine draws every
placement on its own again, for comparing the two.

## Nanite meshes

A prop whose model is a cooked Nanite mesh (`.fnm` — see
[Models](09-models.md#dense-meshes-gltf-and-nanite-cooking)) is drawn by the
GPU instead: every placement of every cooked mesh shares one set of buffers,
and each frame a compute pass culls the meshes' clusters of about 128 triangles
against the view, against what faces away, and against what is hidden behind
geometry already drawn. Only the clusters that survive are drawn, one draw call
per material. So a field of rocks behind a hill costs almost nothing, and a
rock half out of view costs about half.

Each rock is also drawn at the **level of detail** its size on screen needs.
The cook stores every level, from the full detail down to a few hundred
triangles, and for every cluster the GPU checks how far its simplified surface
strays from the full detail, seen from the camera: the coarsest version that
strays by no more than a pixel is drawn. Near parts of one rock can be full
detail while its far side is coarse, and the levels meet without cracks. So a
dense rock costs by how big it is on screen: on the 244-rock bench a frame got
about a quarter cheaper. How many pixels it may stray is the player's
`@level.prop.nanite_lod_error` (1 by default, 0.25 to 8; see
[UI screens](17-ui-screens.md#a-settings-screen)): 2 or 4 trades a little
shimmer at silhouettes for fewer triangles. The debug views `clusters`, `lod`
and `overdraw` (`foundry play -view`, a play script's `view`) colour what is
drawn by cluster, by level, and by how much is drawn over itself.

Shadows are culled cluster by cluster too, against each shadow's own view
rather than the camera's (a rock you cannot see still casts), and draw the
level of detail the camera chose, so a surface is shadowed by exactly its own
shape. On a mesh that is a closed solid, the clusters facing wholly away from
the light are skipped as well, which never changes the shadow. The mesh's baked
distance field makes it block and bounce light for live GI.

The **Model** pickers list cooked meshes beside the models. A placed cooked
prop has a **Nanite** group in its panel: its triangle, cluster and level counts, and
one texture picker per material slot. A slot's picker overrides that slot's
texture on this prop only — the same rock in moss or in snow without a second
cook. In the level file the overrides are `materials: {stone: granite}`.

A cooked mesh draws opaque: a slot whose texture has soft alpha is not sorted
the way a classic model's translucent parts are.

### Wind

A cooked prop can sway in the wind: an imported tree whose trunk leans with
each gust and whose leaves flutter. Tick **Sway in the wind** in its panel's
**Wind** group, or give it `wind:` in the level file:

```yaml
scenery:
- id: oak_3
  model: models/oak.fnm
  position: {x: 4, "y": 0, z: -12}
  wind:
    bend: 0.02     # how far the top leans in a full gust, as a fraction of the height
    flutter: 0.08  # metres a branch tip at the crown's edge bobs and its leaves shake
```

Nothing in the mesh needs authoring or re-cooking for it. The motion comes from
where each vertex sits in the mesh. The trunk bends by the square of the height
up it, so its foot stays planted. The flutter is nothing on the vertical line
through the middle of the mesh's bounds and grows to the full amount at their
edge and top, so the trunk stays still and the outer branches move most. The
model should stand upright with its trunk roughly in the middle of its bounds,
as most tree models do.

The wind's direction, strength and gusts are the ones the biome scatter sways
in, so a tree moves with the grass round it. The bend and its shadow move
together in every pass. Collision, picking and shots keep the still mesh. The
wind shows only on the GPU path: a cooked prop drawn the classic way
(`FOUNDRY_NANITE_MESHES=0`, or a driver without the GPU cull) stands still.

A swaying prop costs a little more than a still one. Its shadow is drawn again
every frame rather than cached, and with TAA its leaves are smoothed using the
camera's motion only, not their own.

`FOUNDRY_NANITE_MESHES=0` draws cooked props the classic way, for comparison;
`FOUNDRY_NANITE_LOD=0` keeps the GPU path but draws every prop at full detail.
A cooked prop drawn the classic way still casts its shadow from the cook's
fallback mesh, a few percent of its triangles, rather than from the full detail
(`FOUNDRY_NANITE_CLASSIC_SHADOW_LOD=0` casts the full detail).

With TAA, the default anti-aliasing, cooked props and a compiled level's
surfaces are shaded through a visibility buffer: each pixel once, from the
triangle the GPU cull left nearest there (see
[Testing and debugging](19-testing-and-debugging.md#anti-aliasing)). Where the
graphics driver allows it, the parts of a cooked prop that are small on screen
are drawn into it by a compute rasteriser rather than the hardware's, which is
faster for triangles of a pixel or two and draws the same picture. A slot whose
texture has any transparent texel is always left to the hardware.

### Draw distance and impostors

A placement can say how far from the camera it is drawn. Four optional keys,
all absent by default (drawn at every distance, as before):

```yaml
- id: fir_00012
  model: models/polyhaven/fir_tree_01/fir_tree_01_a_lod0_c50000.fnm
  position: {x: 512, y: 3.1, z: -480}
  draw_distance: 1300        # metres; fades out over the last tenth
  shadow_distance: 780       # how far it casts (absent: as far as drawn)
  impostor: models/polyhaven/fir_tree_01/fir_tree_01_a_lod0_c50000_imp.fnm
  impostor_distance: 300     # where the mesh hands over to the impostor
```

- **The fade.** A placement does not pop out at its draw distance: over the
  last tenth before it, its pixels dissolve with a screen-space dither offset
  by a number of the placement's own (a hash of its position), which TAA
  smooths into a fade. Neighbours fade at their own pace, never as a wave
  front. Where a pixel dither is not possible — shadows, the forward path
  without the visibility buffer, the classic path — each placement is cut
  whole at its own distance inside that last tenth instead.
- **The impostor** is a cooked mesh of nine textured cards (eight vertical,
  back to back in four planes, and one through the crown) baked from the
  placement's mesh: what a generator writes for scanned trees (see
  [Generators](33-generators.md#draw-distances-and-impostors)). Beyond
  `impostor_distance` the cards are drawn instead of the mesh, out to
  `draw_distance`; in the tenth before `impostor_distance` the two dither
  complementary halves of the same pixels, so the hand-over shows no gap and
  no double. The cards are drawn by the same GPU cull, visibility buffer and
  material pass as the mesh — lit, shadowed and fogged by the same code — with
  a baked normal map, and cast shadows. A missing impostor file is a warning
  in `foundry check`; the mesh is then drawn all the way out.
- **Shadows** stop at `shadow_distance` (each placement at its own distance
  in the last tenth); the virtual shadow map redraws the pages of a caster
  that crosses it.

Distances are measured from the camera to the placement's position. Only
cooked meshes drawn by the GPU cull take impostors; a classic model honours
`draw_distance` and `shadow_distance` only. `FOUNDRY_DRAW_DISTANCE=0` ignores
all four keys, for an A/B comparison.

### Streaming and the memory budget

A cooked mesh's geometry stays in its file until it is needed. When the level
loads, only the small part is read — the clusters' bounds, the levels' errors,
the fallback mesh, the hulls — and the geometry goes to the GPU in **pages**,
into a pool sized by the player's `@level.prop.nanite_stream_pool_mb` (256 MB by
default, saved to `graphics.yaml`; see
[UI screens](17-ui-screens.md#a-settings-screen)). A level whose cooked meshes
fit is loaded whole and never streams. One that does not keeps the pool full
of what the view needs most: the GPU reports every frame which pages it drew
and which finer pages it wanted, and the engine reads the wanted ones from
disk on background threads and swaps them in for pages the view no longer
uses — or, when everything is in use, for pages whose loss shows least. Until
a page arrives the coarser version above it is drawn, which never cracks; a
frame never waits for the disk or the GPU, and at most 16 pages go up a frame.
The coarsest version of every mesh always stays loaded.

The `F3` overlay's **Nanite pages** line shows the pool: pages resident of
slots, what came and went this frame, what is queued and loading, and what was
**starved** — wanted, but every slot held something that mattered more. Many
starved pages mean the budget is too small for the view. The profiler's
`nanite: stream` row is the per-frame work.

A mesh-mode prop's collision cut is paged the same way: it is read on a
background thread when a body comes within about 16 m of the prop (or its
speed would bring it there within a second), and let go some seconds after
the last body leaves. A query that reaches one not yet read — a long shot at
a far cliff — reads it there and then; the overlay's **Prop collision** line
counts those as "waited for".

`FOUNDRY_NANITE_STREAM=0` decodes every cooked mesh whole at load into one
buffer, as before; `FOUNDRY_NANITE_STREAM_POOL_MB=n` overrides the budget (a
test of a small one); `FOUNDRY_NANITE_STREAM_PRELOAD=0` starts every level from
the coarsest pages alone, to watch it stream in.

## Nanite collision

A cooked mesh collides as its own shape, not its box. The cook keeps a
**collision cut**: the mesh simplified until it is 3 cm off the drawn surface
(bumps a foot could never feel are gone, the shape a body walks on is not),
welded, and split into up to 16 **convex hulls**. A prop picks how it collides
in its panel's **Collision** group, or with `collision:` in the level file:

| Mode | Collides as | Right for |
| --- | --- | --- |
| `auto` (the default) | `hulls` for a prop up to 4 m across, `mesh` for anything bigger | most props |
| `box` | the model's box, turned with the prop — how props always collided | a crate |
| `hulls` | the convex hulls, resolved exactly as brushes are | a rock a body walks round |
| `mesh` | the collision cut's triangles, probed like [terrain](30-terrain.md) | a cliff, a hillside, an arch |
| `none` | nothing | |

A prop in `hulls` or `mesh` is part of the same resolve the player and walkers
run against brushes, so they step up onto it, stand on its top, slide along
its sides and cannot climb a face steeper than a floor. Hitscans stop at the
same shape, particles land on it, and [AI](24-ai.md) navigation walks over and
round it — a watertight mesh's inside is solid to navigation, an open one is a
sheet. `box` props are resolved as they always were.

Hulls are cheap but bridge a hollow: a hull of a mesh with a hole in it, or of
an open sheet, fills the space behind. `mesh` follows the surface exactly and
costs more per body near it. Turn on **Show collision** in the group to draw
what a body meets over the selected prop. The group says what the cook made and
warns, as `foundry check` does, about a decomposition the hull cap cut short,
hulls of a mesh with open edges, and a solid prop that collides as nothing.

The cook's settings are project-wide, in `game.yaml`:

```yaml
nanite:
  collision_error: 0.03         # metres off the drawn surface
  collision_max_hulls: 16
  collision_max_hull_verts: 128
  collision_auto_size: 4        # metres: auto's hulls/mesh split
```

Changing them re-cooks every mesh at its next cook. The `F3` profiler shows
the props' collision as `props: resolve`, `props: rays`, `props: particles` and
`props: index` under `entity update`, and the overlay counts props by mode and
how many meshes have their collision in memory (only those a body or a ray has
come near lately do; see [Streaming](#streaming-and-the-memory-budget)).
