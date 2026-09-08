# Biomes

A biome sector is a polygon you paint over a chunk's floors. It clothes them:
GPU-instanced grass, rocks, wildflowers, cattails or moss, a textured ground
patch that conforms to whatever is underneath, and — for the Water kind — an
animated water surface filling the polygon.

Biome paint is decoration. It never collides, and the player walks on the floor
under it.

## Painting one

1. Open the **2D Window** and tick **Edit Biome Layer** under Map. The map now
   selects and edits biome polygons instead of geometry.
2. `Add... > Biome Sector` in the Explorer.
3. Click the corners on the map. Click the first point again, or press `Enter`,
   to close it. Right-click undoes the last point; `Esc` cancels.
4. Select it and pick a **Brush**.

With the layer on and nothing being drawn: click selects a polygon, dragging a
vertex of the selected one moves it, right-click deletes the polygon under the
cursor.

Polygons may overlap — that is how you layer looks, grass under wildflowers under
scattered rock.

## The brushes

Switching brush resets to that kind's defaults rather than carrying stale fields
over.

| Brush | What it does | Its own fields |
| --- | --- | --- |
| **Grass** | scattered blades | Grass Height, Blade Width, Clumping |
| **Rock** | scattered stones | Rock Sides, Rock Size |
| **Wildflower** | stems with blooms | Stem Height/Width, Bloom Size, Leaves, Spike Bloom, Random Bloom Colors, Bloom Center |
| **Cattail** | reeds with seed heads | Reed Height, Reed Width, Seed Head Size |
| **Sand** | displaced dunes, no scatter | Dune Height |
| **Moss** | a ground patch, optionally with low clumps | Scatter Clumps, Clump Size |
| **Water** | an animated water surface | Depth, Wave Amplitude/Length/Speed, Reflectivity, Reflect Color |

Every land brush also lays a **ground patch** underneath it — a textured,
lightly-displaced surface that conforms to the floor — so the painted area reads
as terrain rather than as a flat cutout. Water's patch is the lakebed.

**Clumping** on grass is worth knowing: 0 scatters evenly, higher values gather
blades into tufts, which is what makes a lawn look like a field.

## Shared fields

| Field | |
| --- | --- |
| **Density** | how much scatter, for kinds that scatter |
| **Draw Dist** | the camera ring the scatter is drawn within |
| **Color** | tint; every kind but Sand and Water |

### Ground

Every kind, Water included.

| Field | |
| --- | --- |
| Ground Texture | resolved against `textures/` |
| Texture Scale U / V | repeats per world unit, same units as a floor's UV scale |
| Roughness | how much the patch is displaced (absent for Sand, whose amplitude is Dune Height) |
| Detail Frequency / Octaves | the shape of that displacement |

### Slope range

For kinds that follow displacement — Grass, Rock, Sand, Moss. Scatter only
appears where the ground's angle is between **Min Angle** and **Max Angle**.
That is how you get grass on the flats and bare rock on the steep parts of the
same painted polygon, with no second polygon.

Sloped floors, shaped surface meshes and GPU-displaced detail all count as slope.

### Level of detail

Mesh detail drops Near → Mid → Far with distance.

| Field | |
| --- | --- |
| Near Dist | where the near tier ends |
| Mid Dist | where the mid tier ends |
| (Far) | Mid Dist out to Draw Dist |

Only the near tier casts real per-instance shadows by default.

### Soft edges

Hard cutoffs read as circles and straight lines, which is the usual way a biome
looks wrong.

| Field | |
| --- | --- |
| Range Fade | fade at the outer camera ring; 0 is a hard cutoff at Draw Dist |
| Edge Fade Dist | fade at the polygon's own boundary; 0 is a hard cutoff |
| Boundary Jitter | bulges the draw and LOD rings so they do not read as circles |

## Water

Painting Water auto-manages a water plane filling the polygon. Click the plane in
the 3D view to select the biome sector behind it.

**Depth** is how far *below* the sampled floor the surface sits — so increasing
it lowers the water, and a **negative** value lifts it above the floor and floods
flat ground.

`lakebed` turns off the ground patch drawn underneath, for water lying on a floor
that is already textured and detailed.

Scripts read `water_depth` (the authored number) and `water_level` (its
world-space companion). Animate against `water_level`; see
[Scripting](18-scripting.md).

## Shadows and cost

Biome scatter takes the full scene lighting and shadows. Because that is the
expensive part, it is on function keys and in the **Rendering** group of the
editor settings:

| Key | |
| --- | --- |
| `F5` | scatter receives shadows |
| `F6` | rock / moss cast shadows |
| `F7` | foliage casts shadows (expensive — every blade is re-instanced into every cascade) |
| `F8` | how many cascades scatter casts into |
| `F9` | how many LOD tiers cast at all |

## Limits

Biome paint and procedural detail build in world coordinates, so a chunk carried
by a vehicle frame cannot have them — the grass would stay where the ship used to
be. The editor refuses to paint one.

If you move geometry under existing paint, press **Refresh Heights** on the map
to re-drape it.
