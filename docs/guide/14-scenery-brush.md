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
nothing to dial in — a prop dropped at an angle is solid at that angle.

The panel says when the mesh is flat or would not load, in which case it collides
with nothing.

## In the Explorer

Props are listed under **Scenery**, capped at 200 rows plus whatever is selected,
then `(N more — click one in the 3D view to select it)`. A brush makes hundreds,
and a tree that drew all of them would cost more per frame than the rest of the
panel put together.

A row reads `(no mesh)` when the model will not load, and `(not solid)` when
collision is off.

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

Collision is the mesh's bounding box, as for any prop. A cooked mesh draws
opaque: a slot whose texture has soft alpha is not sorted the way a classic
model's translucent parts are.

`FOUNDRY_NANITE_MESHES=0` draws cooked props the classic way, for comparison;
`FOUNDRY_NANITE_LOD=0` keeps the GPU path but draws every prop at full detail.
