# Foundry Environment Regions: Seamless Multi-Biome Generation

Oct 12, 2026 

## Summary

Build seamless multi-biome worlds by blending continuous fields across environment regions, not by wavefunction collapse. A designer places a region volume, picks an environment such as "desert canyon", places a "savannah" region beside it, and one level-wide generation pass produces both with a natural transition band between them.

- **Recommendation:** each region contributes height shape, masks, ground materials and placement rules, weighted by distance to its edge. Erosion then runs once over the union so drainage crosses the seam. WFC-style constraints apply only to which biomes may touch, and to modular built content.
- **Starting point:** the desert canyon toolset is complete (phases 0–7 in `docs/canyon/`). It covers `.fgen` generator documents, the Generator editor, eroded heightfield terrain cooked to Nanite tiles, a Poly Haven asset library, rule-based scatter and cliff walls, splat materials, atmosphere and a `desert_canyon` template. It generates one square region per generator.
- **Size:** about six phases, comparable to the canyon project. The first deliverable is a canyon-to-savannah join that shows no seam from the ground.

## Why not wavefunction collapse for terrain

WFC solves a grid of discrete tiles under adjacency rules, so it suits modular, man-made content and fits natural ground poorly.

- **Terrain is continuous.** Heightfields, erosion and drainage are fields, not tiles. A tile set would quantise the land, and WFC output reads as gridded and repetitive.
- **Seams are about shared processes.** A believable join needs a river to leave the canyon and keep flowing across the savannah, and talus and sediment to cross the boundary. Only a field pass that spans both regions produces that.
- **Industry practice agrees.** Gaea, World Machine, Houdini and Unreal's PCG framework all blend fields with masks for biome transitions.

WFC-style constraints still belong in three places:

1. **Biome adjacency rules.** Each pair of environments has a transition width or is forbidden. A forbidden pair, such as desert next to glacier, raises a warning or auto-inserts an intermediate biome.
2. **Fill regions.** An optional volume can assign biomes to unassigned coarse cells by constraint solving over the adjacency table.
3. **Built content.** Ruins, villages, cave modules and road networks are true tile problems, and real WFC fits them well.

## Design

One level-wide generation pass composes every region onto a shared world grid, then runs erosion, materials and placement once over the whole union.

### Region volumes

- New level volume `kind: region`: a polygon footprint, an `environment` preset name, an optional `.fgen` override, a priority, and a `transition` width in metres (default 150 m).
- An environment preset is a `.fgen` template with its terrain, material, scatter and vegetation stages, but no fixed size or origin. The region supplies bounds at run time. Presets are written to the project directory only when absent, so a user's edited copy is kept.
- A level generation document (`level.fgen`, created on demand) lists the level's regions, shared stages (environment, backdrop) and global settings: seed, resolution, tile size.

### Field providers

- Each environment exposes a pure function set over world XZ: base height, erodibility and strata, plus mask hints. Today's `terrain` stage is split into a provider (mesas, canyon carving, terracing) and shared passes (drainage, droplet and thermal erosion, mask derivation).
- Providers evaluate only inside their region plus its transition band, so cost scales with the area covered.

### Transition weights

- Each region's weight is a smoothstep of the signed distance to its edge across the transition width, with domain-warped noise so the boundary meanders instead of following the volume's straight sides.
- Weights normalise to 1 everywhere. Overlaps resolve by priority, then by distance.
- Heights blend in a pre-erosion space: base shapes and erodibility blend, then erosion runs on the result. A canyon wall therefore degrades into savannah slopes through erosion rather than a linear ramp.

### Erosion over the union

- Drainage, droplet and thermal erosion run once over the bounding grid of all regions, so rivers and sediment cross seams.
- For incremental edits, erosion runs in tiles with an overlap margin and a fixed per-tile seed. Changing one region regenerates its tiles plus a margin and leaves distant tiles byte-identical.

### Shared tile grid

- Terrain tiles live on one world grid with one quantisation step (already true inside a field since phase 1), so tiles from different regions never crack.
- The far-field backdrop from phase 7 becomes a level-wide stage that continues the dominant biome at each edge.

### Biome-weighted materials

- Each region contributes its ground texture set and splat rules. The terrain material blends biome-weighted splat stacks, height-blended within each biome and weight-blended between them.
- This exceeds today's 4-layer, 32-sampler splat, so layers move into texture arrays (one albedo-plus-height array, one normal-plus-AO array) indexed per layer.

### Biome-weighted placement

- Every scatter, cliff-wall and vegetation rule carries its biome. Density is multiplied by the biome's weight at the candidate point, so ecotones mix naturally: sparse acacia among the last desert boulders.
- Placement IDs keep the `gen.<doc>.<stage>.` prefix plus a region key, so re-running one region replaces only its own placements.

### Adjacency rules

- An adjacency table lists each environment pair with its transition width, or marks it forbidden with a suggested intermediate biome.
- The editor checks regions against the table when they are moved and offers "insert intermediate region" for forbidden pairs.

### Incremental regeneration

- Stage hashing moves from per-stage to per-stage-per-tile. A region edit marks dirty only the tiles its footprint and transition band touch, plus the erosion margin.
- The editor previews region weights as a biome-colour overlay in the 2D view before any heavy run.

## Engine changes

Most of the work reshapes existing canyon code into region-aware pieces; three items are genuinely new engine work: texture-array splats, the vegetation GPU freeze, and per-tile hashing.

| Area | Today | Change |
| --- | --- | --- |
| Level schema (`internal/world`, `levelio`) | Volumes include `biome`, `scatter`, `objective` | Add `kind: region` with environment, priority, transition, footprint; editor gizmo and inspector |
| Generator core (`internal/procgen`) | One `.fgen` per square field; per-stage hashing and manifest | Level generation doc; region-scoped run context with bounds and weight lookup; per-tile hashes and dirty tracking |
| Terrain (`procgen/terrain`, `stages/terrain.go`) | Monolithic stage: shape, canyon, strata, erosion, masks, tiles | Split into field providers plus shared passes; erosion in overlapping tiles with fixed seeds; global tile grid |
| Backdrop (`terrain.FarField`) | Continues one terrain past its edge | Level-wide stage continuing the dominant edge biome |
| Materials (`gfx/material_fx*`, `surface_fx.glsl`) | `splat` with 4 layers in 2×2 atlases; material pass uses 30 of 32 samplers | Texture-array layers (8–16 per material); per-pixel biome weights; keep the visibility-buffer rewrite rules (no new derivatives) |
| Placement (`procgen/place`, scatter, cliff\_wall, vegetation) | Mask-filtered rules over one terrain | Each rule carries a biome; density multiplied by biome weight; region-keyed IDs |
| Vegetation rendering (`render/foliage_volumes.go`, Nanite foliage) | Biome volumes froze the WSLg GPU in two canyon views; ships disabled; about 9 ms per frame | Find and fix the freeze; savannah grass depends on it |
| Splice (`procgen/genlevel`) | Replaces entries by `gen.<doc>.<stage>.` prefix | Add region key to the prefix; region-scoped replacement |
| Editor (`editor_generator_*`) | Generator window with 2D/3D preview and spline editing | Region list, biome-colour overlay, adjacency warnings, "insert intermediate region" |
| Poly Haven (`internal/polyhaven`, stages) | rocks, plants, textures stages | Per-environment asset presets, such as savannah grasses, searsia and acacia stand-ins |

## Implementation plan

Seven phases run in order, each closed by a gate; the canyon toolset's phase notes are the model for how each is specified and verified.

&#91;embedded content: implementation roadmap · 7 phases, each with a gate\]

Phase 0 is the performance work already under way. Phases 1–5 are engine and tooling, and the savannah environment in phase 6 proves the whole design. Each phase follows the canyon ground rules: additive edits to shared files, resource limits, GL tests on both render paths, and a `PHASE_n.md` note.

## Proposed environments

Build savannah first, alongside the region system, then pine forest and coast. Coverage reflects a scan of the Poly Haven catalog on 2026-10-11.

| Priority | Environment | Poly Haven coverage | New generation work |
| --- | --- | --- | --- |
| Done | Desert canyon | Strong: Namaqualand cliffs, boulders, stones; quiver trees; red sand and rock textures | None |
| 1 | Savannah / dry grassland | Good: searsia trees, quiver trees, shrubs, grasses (engine has a built-in acacia shape) | Dense grass at scale, wide rolling plains, scattered tree clumps |
| 2 | Temperate / pine forest | Strong: pine\_forest and verdant\_trail collections, moss and forest-floor textures | Forest-floor layering, tree density and LOD (rainforest Nanite foliage helps) |
| 3 | Rocky coast / sea cliffs | Strong: coast\_rocks, coastal\_cliff, smugglers\_cove collection, beach sands | Shoreline and water edge, wave-cut cliff profile |
| 4 | Alpine mountains / scree | Medium: rock faces and cliffs; snow as textures only | Ridged and glacial erosion, snow by altitude and slope |
| 5 | Dunes / badlands | Medium: sand textures; reuses desert assets | Wind-shaped dune fields, ripple detail |
| 6 | Volcanic / basalt fields | Thin: textures only | Columnar basalt from the SDF rock generator, lava flows |
| 7 | Tundra / snowfield | Thin | Mostly material and shader work |
| 8 | Wetland / marsh | Thin: textures only | Water surfaces, reeds |
| 9 | Tallgrass prairie | To check: shares grasses with savannah; tall prairie grasses and wildflowers unconfirmed | Tall grass (1–2 m) at high density with wind motion, wildflower mixes, gently rolling plains |
| 10 | Oak savannah | To check: oak models unconfirmed; ground cover reuses prairie and savannah grasses | Open-grown oak clumps with wide crowns over prairie grass; reuses the prairie grass stage |
| Deferred | Jungle; caves and karst | Very thin; caves need volumetric terrain, not a heightfield | Defer |

Natural transition chains run desert ↔ savannah ↔ forest ↔ alpine, with coast able to border any of them.

### Adjacency rules

| Pair | Transition | Notes |
| --- | --- | --- |
| Desert canyon ↔ savannah | 200 m | Canyon walls erode into grassy slopes; sand gives way to dry grass |
| Desert canyon ↔ dunes | 150 m | Sand deposition fills canyon floors toward the dunes |
| Savannah ↔ pine forest | 300 m | Tree density ramps; grass thins under canopy |
| Pine forest ↔ alpine | 250 m | Treeline by altitude rather than distance where both overlap |
| Coast ↔ any | 100 m | Beach or cliff edge chosen by the inland biome's relief |
| Desert ↔ alpine or tundra | Forbidden | Insert savannah or steppe between |
| Volcanic ↔ forest | 150 m | Ash soils, burnt deadwood in the band |
| Tallgrass prairie ↔ oak savannah | 300 m | Oak clumps thin out into open prairie; same grass stage on both sides |
| Oak savannah ↔ pine forest | 300 m | Tree density ramps; oaks give way to pines, grass thins under canopy |
| Tallgrass prairie ↔ savannah | 200 m | Grass height and colour blend from tall green to short dry |

## Risks and open questions

- **Frame cost.** The canyon alone ran about 100 ms GPU in the editor and 20–40 FPS in play on the GTX 1070. That investigation is finished and the fix has landed, so region work is no longer gated on it. Biome-blended materials and grass still add cost to measure per phase.
- **Vegetation freeze.** Dense biome volumes froze the WSLg GPU. Retest on native Windows instead of WSL to resolve it. Savannah depends on grass at scale, so phase 6 waits on that Windows test passing.
- **Erosion locality.** Tiled erosion with margins approximates a global run. Long rivers may change course when a far region is edited; accepted, as long as seams stay invisible.
- **Sampler and memory budget.** Texture arrays need every layer at one size and format. Imports already default to 2k, and BC7 compression exists. Accepted.
- **Generation time and memory.** A 2 km × 2 km world at 0.5 m is 16 times the canyon's area. Per-tile incremental runs and the memory guard keep this practical; a first full run taking minutes is accepted.
- **Decided:** region footprints are polygons from day one.
- **Decided:** environment presets ship as `.fgen` files, written to the project directory only when no file of that name exists, so user edits are never overwritten.

## References

- Canyon toolset spec and phase notes: `docs/canyon/SPEC.md`, `docs/canyon/PHASE_0.md` to `PHASE_7.md`
- Generator guide: `docs/guide/33-generators.md`; Poly Haven guide: `docs/guide/34-polyhaven.md`
- [Poly Haven API](https://github.com/Poly-Haven/Public-API) and its [terms of service](https://github.com/Poly-Haven/Public-API/blob/master/ToS.md): unique User-Agent and a visible credit required
