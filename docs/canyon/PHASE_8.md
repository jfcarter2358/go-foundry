# Phase 8: sand that answers the rocks, textures that do not repeat, walls that are rock

The user's verdict on the phase-7 canyon (in the OneDrive project) was:

- the sand met the scattered rocks in a flat cut instead of piling against
  them;
- the wall texture and the wash texture visibly repeated;
- the scanned rocks stuck on the walls looked fake, and the walls themselves
  had no interesting geometry.

The plan agreed with the user was A (textures), then B (sand), then C
(walls). This file is what was built, what was tried and dropped, and what
is left. The user-facing documentation is in:

- [docs/guide/33-generators.md](../guide/33-generators.md): the template's
  table, `terrain_material`'s new parameters, and the `wall_sheet` and
  `drift` sections;
- [docs/guide/28-materials.md](../guide/28-materials.md): the splat's
  `layerN_anti_tile` and macro variation's `vertical`.

`$S` below is this session's scratchpad,
`/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/ef90bc64-8ba6-455a-ab2d-1316953ce265/scratchpad`.
The renders are bench runs of the OneDrive project through `$S/proj`, a
directory that symlinks the project's resources and the repo's shaders
(the project ships no shaders of its own; the editor build supplies them).
They are named `$S/shots/<run>_<pose>/pose_0.png`.

## A. Textures that do not repeat

### Anti-tiling in the splat (surface_fx.glsl `fxSplatTap`, `fxSplatPatch`)

- A per-layer uniform, `fxSplatAnti`, comes from the splat's new
  `layerN_anti_tile`.
- The plane is cut into patches by the bands of a slow value noise: an
  eighth of its range per band, about a repeat across.
- Each band lays the texture shifted by its own hash. Top-down layers are
  also turned, by the golden angle times the band index, with the tangent
  frame turned with them. Triplanar layers are shifted only, so a rock's
  bedding stays level.
- Two neighbouring patches are blended by their height maps, not
  cross-faded. Outside the middle 52% of a band, only one patch is read.
- It follows the other agent's "no runtime indexing" rewrite: one call per
  layer and axis, with constant arguments.
- `terrain_material` sets it on every layer with `anti_tile: true`. The far
  backdrop's material never takes it: a kilometre off, the extra reads buy
  nothing.

### Macro variation up walls

`macro_variation` gained `vertical`: height sheared into the noise's plane.
`terrain_material` passes it through as `macro_vertical`.

### Wash bars (terrain_material `washBars`)

- The wash's gravel bed is broken into channels and sand bars drawn out
  along the flow.
- Noise blobs a bar wide are averaged along the downhill streamline through
  each sample, with a line integral convolution weighted as a tent.
- The averaging flattens the noise's spread, so the result is restretched by
  its own spread over the washes. About a third of the wash becomes bar.
- A bar's share of the gravel goes straight to sand (never cracked flat).
- Noise in a frame turned to the flow would be cheaper, but it compresses
  and tears a few hundred metres from the origin.

### The wall texture: tried and kept

- `rock_face_03` at 4 m (`$S/shots/a_WALL`, `b_WALL`) was tried as a
  featureless detail rock. It read as a pale, flat cream at distance. Its
  mean colour was the same as `cliff_side`'s, (158, 119, 93) against
  (145, 109, 84) on screen, but it had none of the contrast.
- `cliff_side` with anti-tiling (`c_WALL`, `c_SPAWN`) kept the layered red
  look. Its baked bands now shift between patches like natural lenses
  instead of repeating every 10 m. The template keeps it.

### Dropped from the plan

The plan's per-layer *variant* textures needed a 3×3 atlas, which means
reworking the atlas packing, the exact-halving skip-mips and the BC7 cache.
Anti-tiling plus vertical macro variation removed the repeat without them.

## B. Sand that answers the rocks: the `drift` stage

### The method (place/drift.go)

- Each rock of the chosen scatter rules is rasterised from its cooked
  fallback mesh onto the terrain grid (`RasterColumns`). Each sample under
  it gets the rock's underside and top.
- The contact height is `fillet` × the rock's exposed height, capped, and
  varied ±15% around the rock.
- The footprint is where the rock meets the sand's new surface; its boundary
  samples are the contact line.
- The apron is the largest of the contact samples' profiles,
  h·(1 − d/L)² with L = spread·h. Distance is measured in a frame stretched
  by `tail` downwind and shortened by `windward` upwind. In a wash the tail
  turns downhill.
- Rocks combine by the larger rise, never the sum.
- Drifts fade out on steep ground and are zero at the grid's edge, where the
  backdrop's stitch strips meet the tiles.

### The stage (stages/drift.go)

- It recooks the touched tiles with the terrain stage's own tile plan,
  quantisation step and base, so they meet the untouched tiles.
- It writes the terrain material's maps with the sand in them (weights,
  ground colour, height) and a copy of the material reading them.
- It re-places the scatter's rocks wearing copies of their materials whose
  ground blend reads the new height. It lifts the pebbles and plants that
  stand on a drift.

### Supersede, in the procgen core

`RunContext.Supersede(stage, ids...)` lets a stage replace an earlier
stage's placements:

- they are left out of `Result.Placements`;
- the list is persisted in `placements.yaml` as `supersedes:`, so a skipped
  stage still replaces them.

### Result

- 996 of 1,833 rocks drifted; the other 766 were under `min_height`.
- 1,060 m³ of sand, deepest 0.77 m.
- All 16 tiles touched; about 20 s, nearly all of it the tile cook.
- `$S/shots/d_WASH` against `b_WASH`: the boulder in the wash now sits in
  sand that climbs it and trails downstream. The gravel gives way round it
  instead of meeting a flat cut.

## C. Walls that are rock: the `wall_sheet` stage

### The method (procgen/wallsheet)

The shell's surface is the terrain's own surface, pushed **out** along its
normal by up to `depth` on steep ground, less a recess function:

- **beds** of irregular thickness, in formations whose bend keeps them in
  order;
- **soft beds** hollowed back under **hard** ones, which stand as ledges with
  an eased under-lip;
- **joints**: Voronoi panels in plan, each set back its own amount. A joint
  is open through runs of 2–6 beds;
- **alcoves** now and then;
- a **caprock**: the 2.5–5 m under each local rim (the highest ground within
  12 m) is hard and unjointed;
- **roughness**.

Because the shell only ever stands in front of the terrain, the terrain
never changes:

- no hole is cut in the tiles;
- `drift`'s recooked tiles are unaffected.

On flat ground the shell lies 12 cm under the terrain. A first try at 5 cm
risked the terrain's triangles and the shell's bilinear field disagreeing by
more than that on slopes.

### The run (stages/wall_sheet.go)

- The band is cut into 32 m sections on a world-aligned grid, each meshed
  alone with its lattice snapped to the world's.
- Each section is simplified within 4 cm, with its side vertices locked so
  it still meets its neighbours.
- Normals come from the unbounded field, except on a section's box, so the
  seams do not shade.
- Vertex colour: AO from the field in alpha, a small tint per bed and panel
  in RGB.
- Cooked with mesh collision and `AttrRadiusCap` 1 m (the perf agent's cap
  for big meshes).
- The material is the ground material's splat over a pure-rock weights map,
  with bedding turned down to laminations and a ground blend at the foot.

### Iterations

- **First run** (`$S/shots/e_WALL`, `e_SPAWN`):
  - up close, real ledges, overhangs and joints;
  - from a distance, the joints notched every rim into battlements;
  - joints at one pitch crossed beds at one pitch, and read as masonry;
  - 532 MB of sections.
- **Fixes:**
  - the caprock under each local rim;
  - joints open through runs of beds, fewer of them (40%), 17 m apart;
  - bed formations;
  - simplification, 532 MB → 211 MB.
- **Second run** (`f_WALL`, `f_SPAWN`): clean rims, and joints that read as
  fractures.
- **Rockfall.** The generated sandstone blocks took the Entrada palette
  over the cliff texture and glowed orange against the walls. The template
  now gives them a near-white custom palette, warmed like the rock tint.

### The template

- `rockface` (wall_sheet) and `rockfall` (rocks, preset boulder, the cliff
  layer's texture) are added.
- `cliffs` and `walls` (the phase-7 scan cladding) are **off**; `scatter` no
  longer avoids them.
- The talus rule places `rockfall`, with `match_ground` 0.35.

## Final run

On the OneDrive project, with 6 workers on WSL2:

| | Phase 7 template | Phase 8 template |
| --- | --- | --- |
| Full regenerate after the template change | — | 3 min 30 s, peak RSS 0.7 GB |
| `rockface` | — | 2 min 32 s: 189 sections, 15.1 M triangles meshed → 1.3 M kept, 84 MB |
| `drift` | — | ~20 s (the 16 tile cooks) |
| Wall geometry | 580 scan chunks, 61 M triangles at full detail | 1.3 M triangles of wall shell + 1,321 rockfall blocks of 6k |
| Placements | 6,645 | 7,133 |
| Nanite pages resident at the poses | 3,374 (580 MB) | 4,274 (801 MB) |

How the walls' cost came down:

| Version | Wall time | Size | Cut |
| --- | --- | --- | --- |
| first | ~4 min | 532 MB | — |
| simplified within 4 cm | 9 min 31 s | 211 MB | 41 min of the summed time was simplification |
| buried triangles dropped first | 2 min 32 s | 84 MB | the shell under the floor, box bottoms, rock inside the mesas |

The last fix drops every triangle more than half of `Hidden` under the
terrain before simplifying.

**Frame times on WSLg** (p50, 1600×900, not the 1070's), against phase 7's
1920×1080:

| Pose | Phase 7 | Phase 8 |
| --- | --- | --- |
| WALL | 114 ms | 73 ms |
| SPAWN | 147 ms | 72 ms |
| OVER | 280 ms | 161 ms |

These run at different resolutions and are not comparable. The perf agent's
work landed in between, and **only the GTX 1070 bench can judge**.

### The rim clamp

The overview after the buried-triangle drop showed one more defect. Pushed
out along a rim's upturned normal, the shell stood up over mesa tops as a
parapet.

- **The fix:** the shell may rise at most `RimLip` (25 cm) over the ground
  1.2·`depth` uphill of it. "Uphill" comes from a gradient blurred over
  `depth`, so at a wall's foot it still points into the wall.
- **The test:** `TestWallShellStandsInFrontOfTheTerrain` checks every
  section for a vertex over the top. It fails without the clamp: 1.25 m out
  from the cliff face, the shell stood 0.95 m over the top.
- **Not a defect:** the S-shaped slot in the foreground mesa of `h_OVER` is
  in the terrain itself. It is in phase 7's `fin_OVER` too.

Final renders: `$S/shots/g_WALL`, `g_SPAWN`, `g_WASH`, `h_OVER`, `h_WALL`.

## Tests

| Package | New |
| --- | --- |
| procgen | `TestSupersededPlacementsAreReplaced` (also when the superseding stage is skipped) |
| procgen/place | `TestRasterColumnsSeesTheBoxTopAndUnderside`, `TestDriftFilletsTheRockAndTrailsDownwind`, `TestDriftsCombineByTheLargerNotTheSum` |
| procgen/wallsheet | `TestWallShellStandsInFrontOfTheTerrain`: the steep mask, `min_wall` excluding a bank, sections only at the cliff, no face vertex behind the terrain, relief between ledge and recess |
| procgen/stages | `TestWashBarsRunAlongTheFlow`; the template test lists the new kinds |
| material | the resolve test covers `anti_tile` and `vertical` |
| render (GL) | `TestMaterialFxOnEveryPath` gained an anti-tiled splat (every path, variant against generic) |

## Known gaps

- **Performance is unjudged on the GTX 1070.**
  - Anti-tiling adds about half again the splat's texture reads.
  - The walls add millions of triangles where the scans were.
  - The bench needs the Windows build (docs/canyon/PERF.md, "Method").
- **The walls take 2.5 minutes to generate.** Meshing is most of that now
  (11 min summed over 6 workers). The field's per-sample cost — cellular
  joints, FBM, beds — is the next thing to cut, for example by caching the
  recess per column of beds.
- **Drift does not reach the walls.** Sand ramps against cliff feet would
  want the wall shell rasterised too. Its hidden flat parts would put sand
  at the band's edge, so it needs a footprint test that ignores them.
- **The editor's generator preview** shows a superseding stage's placements
  beside the ones they replace.

## Follow-up: sand against the cliffs, pebbles in relief

The user then asked for two more things: sand piling against the cliffs, and
pebbles that do not look 2D.

### Sand against the cliffs

`drift` gained `walls` (a wall_sheet stage) and `wall_*` parameters.

How it works:

- Every wall section is an obstacle, rasterised from its **full** cooked
  mesh. It carries its own `place.DriftShape`: a fixed ramp height varying
  along the wall over 15 m, its own spread, tail and slope fade.
- The walls are re-placed wearing materials whose ground blend reads the
  drifted height.

Four defects found and fixed on the way:

1. **Zero-direction distance bug.** An obstacle with no tail direction made
   the stretched distance zero everywhere in reach: full sand height for
   metres. Real runs always pass the wind, and an earlier test passed for
   the wrong reason. Fixed: no direction now means plain distance. The
   per-obstacle tail and windward factors are locals; the first fix wrote
   to the shared settings from a worker.
2. **Sand over the rim.** Aprons climbed onto higher ground (the mesa top
   behind a rim) because a rise is relative to each sample's own ground.
   Fixed: a contact's sand surface is absolute — ground + h·t² — and the
   rise is capped by it.
3. **Rim contacts.** For cliffs, a contact counts only where the rock stands
   at least half the ramp over the ground. A rim barely clears the top, and
   would otherwise heap a ridge along every rim.
4. **Sand through the rock.** Under a cliff's rock the ground is never
   raised. It is the heightfield's wall behind a face full of joints, and
   raised, it showed through them as sand spikes and blocks
   (`$S/shots/i_*`, `j_*`).
   - `wall_slope_fade` is (25, 40): no ramp where the terrain is steep enough
     for the shell to stand in front of it.
   - Final run: `k_SPAWN`, `k_WALL`. A smooth ramp banks against the cliff
     foot; 8,200 m³ of sand, deepest 2.7 m. `drift` is ~30 s.

`TestDriftRampsACliffFootNotItsRim` covers all four: ~2 m at the foot,
nothing behind the rim, nothing under the rock.

### Pebbles in relief

The gravel's normal map was there; the stones still lay painted on the
plane. The splat gained `layerN_relief` (`fxSplatRelief`, `fxSplatRead`):

- **Parallax:** iterative offset mapping on top-down layers. The height map
  is the albedo atlas's alpha, centred on the mesh's plane: three height
  reads, u = u0 − d·(0.5 − h(u)).
- **Cavity:** the gaps darken in the AO by the same height.
- **Fades out** once a pixel covers 2–12% of a repeat; the view slope is
  capped at V·N ≥ 0.3.
- **Never applied** to triplanar layers or the far backdrop.
- **Template:** `terrain_material` sets it through `gravel_relief` (7 cm)
  and `cracked_relief` (1.5 cm).

`$S/crop/wash.png` puts the gravel before (left) and after (right) side by
side: the stones overlap and their gaps go dark. The effect is subtle in a
still frame and stronger in motion. The GL every-path test's anti-tiled
splat also carries relief.

### Cliff sand, second pass: no dip, no spikes

**The user's report:** the cliff sand dipped right beside the cliff, rose,
fell again, and was spiky.

**What was rebuilt.** The boulders' method — a cone from every contact
sample, the largest winning — was replaced for cliffs by `cliffRise`, one
field over the whole map. Every step was found by a render and pinned by a
test:

| Render | Seen | Cause | Fix |
| --- | --- | --- | --- |
| `l_*` | sand up the whole face, in sawteeth | "where the rock stands over the ground" held all the way up the wall; every terrain sample there raised its own ramp | the foot is read off the terrain: gentle ground within `depth` (the wall stage's) of steep, rock-covered ground above it |
| `m_*`, `n_*` | a sawtooth along the top of the sand | the diagnostic transect (`stages/zz_diag_test.go`, deleted): the foot undulates by a metre, and each sample's own foot ground made the sand rise over a hump and fall into the hollow by the wall | an absolute surface: the foot's ground averaged along the wall over 4 m, plus the ramp; hollows filled, humps buried |
| `o_*` | vertical walls of sand | the nearest foot's ramp switched between a bench and the floor below | the highest of every foot point's ramp, bucketed on a 4 m grid |
| `p_*` | (none, after the fix) | each ramp ended at its own base level, not the ground | each ramp's rise allowance, H·t² + 1.5·H·t, runs out with it |
| `p_*` | streaks up the face | ledges and chute floors under the rock started ramps | foot points must be open ground; flat ground under the rock (its flare) takes the full ramp height but starts none |

**Also changed:**

- No slope fade for cliffs (`wall_slope_fade` is gone). The absolute
  surface keeps the sand off the terrain's wall behind the rock, which is
  never raised.
- The ramp height and the tail direction come from the foot point. A
  gradient of the distance field flipped between cells.

**Tests:** five tests in procgen/place, each checked to fail on the
behaviour it replaced where that applies:

- `TestDriftRampsACliffFootNotItsRim`: no dip, no spikes along the foot,
  nothing on the terrain's wall, the mesa top or the rim;
- `TestCliffSandBuriesABumpyFoot`;
- `TestCliffSandHasNoStepsWhereTheFootChangesLevel`;
- `TestCliffSandLeavesLedgesUpTheFaceBare`;
- the boulder tests.

**Final run:** `q_FOOT` (a close view of the foot), `q_SPAWN`, `q_WALL`. A
smooth ramp meets the rock along the whole foot. 56,900 m³ of sand; `drift`
takes 25 s.
