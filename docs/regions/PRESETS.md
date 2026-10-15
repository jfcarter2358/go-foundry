# Environment presets and the biome showcase world

User request (2026-10-13): build out the remaining environment presets ENVIRONMENT.md
proposes, then build one world from generated regions that shows all of them, in the
user's OneDrive project (`/mnt/c/Users/jcart/OneDrive/Documents/Foundry`).

Everything in docs/regions/SPEC.md "Ground rules" applies (no commits, resource limits,
house style, Poly Haven first, honest phase notes, defaults OFF for existing content —
desert_canyon / savannah / canyon_savannah hashes and outputs must not change).

## The presets to build

From ENVIRONMENT.md's "Proposed environments" table (jungle and caves stay deferred):

| Preset | Landform | Ground | Life |
| --- | --- | --- | --- |
| `pine_forest` | rolling hills, moderate relief | forest floor, moss, needles, rock | Poly Haven pine_forest / verdant_trail assets; `pine`/`forest` foliage biomes; dense trees with LOD |
| `coast` | shoreline at a sea level, wave-cut cliff profile, beach | beach sand, wet sand, coast rocks | coast_rocks / coastal_cliff / smugglers_cove assets; `beach` biome; a WATER surface at sea level |
| `alpine` | ridged mountains, glacial valleys, scree aprons | rock faces, scree, SNOW by altitude and slope | rock/cliff scans; `scree`/`glacier` biomes |
| `dunes` | wind-shaped dune field with ripples, interdune flats | sand variants (reuse desert) | sparse; `desert` biome |
| `volcanic` | cones/craters, lava-flow lobes, columnar basalt from the SDF rocks | basalt, ash, cinders | `volcanic` biome; burnt deadwood |
| `tundra` | low rolling, frost-heave hummocks, ponds | snow patches, frozen ground, lichen | `tundra`/`ice_plains` biomes |
| `wetland` | flat, shallow water channels and ponds at a water level | mud, wet grass, reeds | `wetland` biome; WATER surfaces |
| `tallgrass_prairie` | gently rolling plains | green-gold grass ground | tall grass 1–2 m with wind, wildflowers (`meadow`-like) |
| `oak_savannah` | gently rolling | prairie grass ground | open-grown oak clumps with wide crowns over prairie grass |

Each preset gets an overlay colour, sensible adjacency rows (ENVIRONMENT.md's table +
plausible additions), Poly Haven ids VERIFIED against the live catalog, and is written to a
project only when absent.

## Engine work the presets need (step 1)

1. **More than 8 ground layers per world.** The union material is capped at 8 distinct
   layers. Lift the cap: a world-wide layer PALETTE (up to 32) in one shared atlas
   (tile size drops to 512 when > 8 layers; BC7 path), and per-tile materials become
   mandatory when the palette exceeds 8 — each tile (with its margin) references at most
   8 palette entries by atlas index (the `atlasN` params from the per-tile work). A tile
   that would need more than 8 is a validation error naming the tile and layers (the
   layout or presets must share layers). Worlds with ≤ 8 layers are unchanged.
2. **Material rules beyond the canyon's four roles.** terrain_material's roles are
   sand/cracked/gravel/rock by slope/flow/deposition/cliff. Add optional cover rules so
   presets can express: snow (altitude band + slope limit + noise), wet ground near a water
   level, ash/cinder by distance from a vent, forest floor under canopy if cheap. Keep the
   canyon's outputs byte-identical.
3. **Landforms.** New provider shape options in procgen/terrain (each off by default):
   ridged mountains (+ glacial U-valley carving if cheap), dune field (wind direction,
   wavelength, asymmetric stoss/lee, ripples as detail), coastline (sea level, shoreline
   along the region edge facing a chosen side or a spline, beach profile, wave-cut cliff
   option), volcanic cones/craters + lava lobes, hummocks/ponds (tundra, wetland).
4. **Water.** A world/region water level that places water surfaces (the existing
   world water surface) over the parts of the union below it (sea for coast, ponds and
   channels for wetland/tundra), as level placements/volumes the splice can own.
5. **Height compatibility.** Regions at very different elevations meet as escarpments.
   Give presets a base elevation and let alpine rise from the shared base inside its
   footprint (relief faded across the band) so neighbours meet at the same ground level.

## The showcase world (step 3)

- A world doc `biome_showcase_world` in the OneDrive project with ~11 regions (the two
  existing presets + the nine new), laid out so every adjacent pair is allowed by the
  adjacency table (no forbidden-pair warnings; insert intermediates where the
  natural chains need them: desert ↔ savannah ↔ forest ↔ alpine, coast along one edge,
  dunes beside the desert, tundra beyond the alpine, wetland/prairie/oak savannah in the
  temperate middle, volcanic next to forest).
- Size about 3 km × 2 km at 1 m (or what the budget allows), tiled erosion, per-tile
  materials, level `biome_showcase` with a spawn, a camera tour or several named spawn
  points, environment stage (sky/fog/sun) that suits the mix.
- Generated end to end with `foundry gen run`, renders from every region and every seam,
  looked at; native 1070 numbers as docs/canyon/PERF.md measures them.
