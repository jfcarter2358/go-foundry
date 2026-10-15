# Environment regions — phase 4: biome-weighted placement

Status: built and tested, 2026-10-12. Two terrain defects found while
reviewing phase 3 were fixed first (part A). Then the placement stages
became per-region stages (part B). Every gate has a test. The scratch
world from phase 3 was regenerated with both presets' placements,
rendered through the engine on WSLg, and the renders were looked at.

`$S` below is this session's scratchpad,
`/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/9afc65f5-e4b2-4b3e-9788-2e06870a0755/scratchpad`.
The pictures named here are in `$S/p4/`.

## Part A — terrain defects

### A1. A shading line along every tile edge

**Cause.** `meshout.HeightGrid.Build` gave each vertex the area-weighted
normal of the tile's own triangles. At a tile's rim that normal sees only
one side of the seam. The two tiles sharing a rim vertex therefore
disagreed, and phase 3's render showed a straight line along the 128 m
tile edge. It showed even with effects, LOD and the visibility buffer all
off.

**Fix, at the source.**

- `HeightGrid.Normal func(i, j) Vec3` is new and optional.
- `meshout.FieldNormal` computes normals by central differences over the
  WHOLE field, one-sided only at the field's own edge.
- Every terrain stage sets it, through `tileGridAt` (`gridNormal`):
  - the classic `terrain` stage;
  - `region_terrain`, via the same `writeTiles`;
  - `drift`'s recooked tiles.
- The far ring tiles use the lattice's central differences
  (`farLattice.normal`).
- In the stitch strips, the inner edge takes the map tile's rim normal and
  the outer edge the lattice's. A strip therefore shades into both
  neighbours.
- Normals are now set for all vertices, not only the rim. The Nanite cook
  keeps non-zero normals.

**Goldens: changed deliberately, and only for the tile meshes.**

- `TestTerrainStageGolden`'s `desert_ground` hash was split into two:
  `desert_ground_fields` (heightmap, masks, albedo, relief, terrain.yaml)
  and `desert_ground_tiles` (the `.glb` tiles). Both were recorded again.
- Before re-recording, the old single hash
  (`b4cf8a…536d`, kept in a comment) was checked to still match with the
  field normals switched off. So the fields are unchanged, and only the
  tile normals moved.
- `far_small` and `far_meshes` (positions and indices) are unchanged.
- `TestGenerateGolden` (terrain heights and masks) is unchanged, and so
  is `TestGateOneRegionIsTheClassicTerrain` (region terrain equals
  classic, tiles included).

**Tests.**

- `TestNeighbouringTilesShareRimNormals`: three neighbouring pairs, with
  mesh_step 1 and 2. Every shared top vertex has an identical normal. The
  old per-tile normals are checked to differ there, so the fixture can
  tell the two apart.
- `TestStitchStripShadesLikeTheTileAndTheRing`.

**Render.**

- `a1_tile_edge_normals_before.png` (phase 3's binary) shows the vertical
  seam line down the centre.
- `a1_tile_edge_normals_after.png` is the same pose with FX, LOD and the
  vis buffer off. The line is gone.

### A2. A sawtooth along a ridge crest in `seam_east.png`

**Reproduced.** I cast a synthetic silhouette from the heightmap at the
phase-3 pose (0, 60), yaw 0. It shows the same teeth, one per 2–2.5 m of
crest, so the cause is in the heights, not in the mesh, LOD or tile
quantisation.

**The heightmap.** Behind the canyon extension's rim (x ≈ 25–35,
z ≈ 47) there is a **one-sample-wide levee**. The profile runs
1.85 → 3.58 → 1.80 m: a 1.7 m knife edge with 31° flanks. A one-sample
crest that runs diagonal to a 1 m grid is drawn as a sawtooth.

**Phase by phase** (experiments in the scratch project, editing the
preset):

| Experiment | Result |
| --- | --- |
| droplets off | the levee stays |
| talus off | the levee is a 4.5 m wall, which talus then shaves to the knife |
| drainage, droplets, talus, terrace and detail off | a smooth 20 m plateau with a clean canyon wall: no levee |

**Cause: the drainage pass.**

- Stream power runs on a 4 m coarse grid. Its change is spread back onto
  the 1 m field by a B-spline, by distance only.
- In the band, the savannah's soft rock lowers the blended plateau by
  about 18 m. The coarse samples on the canyon floor are barely lowered.
- So the lowering fades out over the last coarse cell before the rim. The
  rim, a fine sample at the plateau's height, keeps half its height.
- The classic canyon never sees this, because its caprock is too hard to
  lower the plateau that far.

**Fix** (`terrain/fluvial.go`, `edgeAwareChange`): a **guided
upsampling** of the coarse change.

- Over the 4×4 coarse samples the B-spline reads, it fits a weighted
  least-squares line, change ≈ a·height + b, with the B-spline weights and
  a regulariser of 4 m².
- It reads the line at the fine sample's own height, clamped to the coarse
  samples' range. The rim takes the plateau's change, the floor the
  floor's, and the wall a share by its height.
- On gentle ground it equals the B-spline: the test allows 1 cm, and
  measures less.
- A height-weighted (bilateral) version was tried first. It beaded the rim
  at the coarse grid's pitch (`a2_relief_before_vs_bilateral.png`), so it
  was dropped.
- It is used only in unions of more than one provider
  (`knobs.edgeAware`, set by `GenerateUnion`). The classic field and the
  one-region identity are unchanged, so their goldens pass untouched.

**Result.**

- The silhouette's reversals in the left half of the view went from 12 to
  2, and the summed upward steps from 2.15° to 0.14°.
- Rendered at the same pose: `a2_seam_east_after.png`, against
  `a2_seam_east_before.png`. The crest is a soft natural ridge, with no
  staircase.
- `a2_relief_guided_eps4_vs_16.png` compares regulariser values; 4 was
  kept.
- Side effect, seen in relief: the slopes above the desert canyon's rim
  next to the band now carry fine rills (droplet gullies on ground that is
  now steeper). They read as erosion, not as a grid. Recorded here.

**Tests.**

- `TestGuidedChangeKeepsTheRimWithThePlateau`: rim −18 within 0.6 where
  the B-spline gives −12…−16; floor −2; mid-wall between.
- `TestGuidedChangeIsTheBSplineOnSmoothGround`.
- The union gates still pass: river, tiled erosion, seam slopes
  (band 82.3°/80.8° p99, as before), and the one-region identity.

**Not changed.** The desert mesa field ends in a straight N–S face at
x ≈ 27.5 in this world. It is there before any erosion, and it is a mesa
joint line, which the joints feature makes straight by design. It was left
alone.

## Part B — phase 4

### 1. Placement stages per region

**`procgen` core** (regional.go, registry.go).

- New `StageKind.OnDemand`. An asset stage (`rocks`, `polyhaven_rocks`,
  `polyhaven_plants`, `polyhaven_textures`) is expanded for a region only
  when a kept expanded stage of that region refers to it
  (`pruneOnDemand`). Without it, a level doc with no region terrain
  downloaded Poly Haven scans for nothing; a test caught it.
- A preset stage whose Shared stand-in the level doc lacks (a scatter that
  needs a `region_material`) is now **skipped with a note** rather than
  refusing the plan. `TestRegionsNeedASharedStandIn` was updated to say
  so.
- `procgen.RegionStages(d, region)` (by key or volume ID) is the planned
  expanded stage IDs.

**Kinds now `RegionalPerRegion`:** scatter, vegetation, cliff_wall,
wall_sheet, rocks, polyhaven_rocks, polyhaven_plants and
polyhaven_textures. drift stays RegionalNone and is skipped with a log: it
recooks the shared terrain's tiles, which no region owns. No kind's
`Version` or params changed, so no existing hash moved
(`TestDesertCanyonHashesUnchangedByRegions` passes).

**`place` package** (region.go; scatter.go, vegetation.go, wall.go):
`place.Region{Weight}`. The hooks are below.

| Kind | What it does with the region |
| --- | --- |
| Scatter (`ScatterSettings.Region`) | Each cell's and satellite's weight × the region's weight. 32 m blocks are counted from the world origin; the block RNG and the keep order's tie-break are keyed by world block coordinates (`Hash64(seed, "world", wbx, wbz)`). |
| Patches (`PatchSettings.Region`) | Cell weight × the region weight, on a grid from a world multiple of `cell`. |
| Walls (`WallSettings.Keep`) | The stage keeps columns where weight ≥ ½. |
| Wall sheet (`wallsheet.Settings.Weight`) | The shell's stand-out (`steep·(Depth−R)`) × the weight. The mask, the band and the sections stay unweighted, so a shell never ends in a cut face. Sections with no share are not meshed. |

**Deviation: no clip to the band rectangle.**

- The spec says to restrict candidates to the band bounds. The band test
  showed why not.
- The regions package hands union padding and gaps to the nearest regions
  smoothly, so a region's weight can be about 0.1 well past its band.
- Clipping there left 17–21 rocks a bin and then 0: a hard line.
- So the weight alone decides; it is 0 where the region has no share.

**Stages** (place_region.go, place_region_ground.go).

- `cliff_wall`'s `paths`, `region_min`/`region_max` and `focus` are preset
  coordinates, moved by the region's anchor.
- A region's `match_ground` aims at its OWN ground. `regionGround`
  replaces `GroundMean` with the mean of the region material's ground map,
  weighed by the region's weight.
- `rocks` per region: variants are seeded by the preset stage, not the
  region, and made once into `generated/<doc>/_shared/rocks_<hash>/`. The
  hash covers the kind, its version, that seed and the resolved params.
  Every region of that preset points its manifest at the shared files.
- Poly Haven stages: imports were already one folder per asset
  (md5 download cache, `.fnm` hash skip). `importModelShared` also reuses,
  within a process, an import a region's stage made, keyed by asset and
  settings and validated by the model files' mtimes. In the scratch run
  the savannah's shrubs, boulders and stones were "the import another
  region's stage made this run".
- Galleries are not placed for regions.
- `polyhaven_textures` now serialises imports of one asset id. Two layers
  naming the same texture raced on the `.tmp` rename; a new test hit this
  in the classic stage.

### 2. Union terrain and region material as inputs

- `GroundSources = {terrain_material, region_material}` is the `ground`
  param of scatter, cliff_wall and wall_sheet. `TerrainSources` already
  covered `region_terrain`.
- **wall_sheet over eight layers.** `rockLayerIndex` picks the region's
  environment's role-`rock` layer from the region material's `layers`.
  `rock_weights.png` and `rock_weights2.png` hold pure rock in that
  channel, and the material sets both `weights` and `weights2`. The
  four-layer path writes the same single file as before.
- **drift** refuses an eight-layer ground with a clear error. It writes
  sand into channel 1 of a four-layer splat. It stays non-regional.

### 3. Region-keyed IDs and the region-scoped splice

- IDs are `gen.<doc>.r_<region>_<stage>.<id>`, as in phase 1.
- `genlevel.Options.Scope` holds prefixes. When it is set, only entries
  under them are removed or added; nil means everything, and empty means
  nothing.
- `genlevel.RegionScope(d, res, regions)` gives the StagePrefix of each
  region's planned expanded stages, plus the doc's own stages that ran.
  It uses whole stage IDs, not `gen.<doc>.r_a_`, which would also match
  region `a_b`.
- **CLI:** `foundry gen run <doc> -region <id>` (repeatable) runs that
  region's STALE expanded stages (all of them with `-force`) plus any
  stale shared stage they need, then does a scoped splice. If the region
  is up to date it says so.
- **Editor:** Generator > Run > **Run Region** › id (`genBeginRegionRun`).
  The pending splice carries the regions, and both splice paths (open
  level, file) use the scope.

### 4. The savannah's placement (first pass; phase 6 tunes)

Ids were checked against the live catalogue (`foundry polyhaven search -c
plants`).

| Stage | Contents |
| --- | --- |
| trees | `searsia_lucida` (to 250k triangles) and `quiver_tree_01` (150k) |
| shrubs | `wild_rooibos_bush`, `shrub_04` and `didelta_spinosa` (120k) |
| boulders and stones | namaqualand_boulder_04/05 and namaqualand_stones_01: the desert's granite, so one import serves both |
| scatter | tree clumps (0.05, cluster 1 at 70 m), boulders, shrubs with satellites, stones in the washes |
| grass | vegetation with the savannah biome, **disabled** (the WSLg freeze) |

An existing project's `savannah.fgen` is never overwritten. Delete it and
run `foundry gen env write savannah` to get these.

## Tests (new)

**procgen/stages:**

- **GATE** `TestGateRerunningOneRegionLeavesTheOtherAlone`: two regions,
  each with fake Poly Haven scans and plants, generated pebbles, a scatter
  over the region material and vegetation, all uncooked. Region B's preset
  scatter density is edited, then:
  1. a plain run: none of A's stages rerun;
  2. a forced run: A's stages are regenerated;
  3. a run of `RegionStages(plain)` alone.

  After each, every file of A's `r_hills_*` folders is byte-identical
  (placements.yaml, footprints.yaml, rocks.yaml, credits.yaml,
  materials). A scoped splice of run 3 leaves every one of A's placements
  in the level unchanged, and B's count matches the run.
- `TestRegionScatterThinsAcrossTheBand`: equal densities, flat ground,
  10 m bins. Region A's share goes 1.00 → 0.97 (−35) → 0.72 (−15) →
  0.42 (5) → 0.11 (25) → 0.00 (45+). The combined count stays within
  ±30% of the mean in every bin (it measured within about ±15%).
- `TestRegionRocksAreMadeOnce`: two regions of one preset make the rocks
  once and both point at `/_shared/`. The second region reuses the scan
  import.
- `TestRegionWallsWearTheirOwnRock`: on an eight-layer region material,
  each region's rock weights are pure on its own rock layer, and the
  material reads `rock_weights2`.
- `TestClassicPlacementsPinned`: a classic doc's scatter and vegetation
  placements.yaml hashes. **Recorded after the change.** Classic identity
  rests on every region path sitting behind a nil check, and on the
  existing placement tests passing unchanged. No pre-change recording was
  available.

**Other packages:**

| Package | Tests |
| --- | --- |
| place | `TestRegionScatterIsKeyedByTheWorld` (growing the ground from 256 m to 384 m moves no rock in the shared square), `TestRegionPatchesFollowTheWeight`, `TestWallKeepDropsColumns` |
| wallsheet | `TestWeightSinksTheShell` |
| genlevel | `TestScopedSpliceKeepsTheOtherRegions` (`r_a_b` is not region `a`; an empty scope changes nothing) |
| gencli | `TestGenRunOneRegionSplicesOnlyIt` (the real CLI on a level file) |
| procgen | `TestRegionsNeedASharedStandIn` (updated) |

## Commands and results

```
export GOFLAGS=-p=6 GOMEMLIMIT=6GiB
go build ./...                                               clean
go vet ./internal/procgen/... ./internal/editor/             clean
go test ./internal/procgen/...                               all ok (stages 63 s)
go test ./internal/ -run TestPackagesDoNotDependUpward       ok
go test ./internal/world/... ./internal/levelio/...         ok
go test ./internal/editor/...     fails only TestScoreSibeliusKeymap, TestScoreRebinding (the score session's)
```

No render/gfx/shader code was touched, so no GL tests were run for this
phase. The renders below went through the real engine.

## The scratch world (`$S/proj3`, level `seam`)

**The run.** Presets were rewritten from the built-ins, with cook_memory
2000 for rockface and rockfall. Then:

```
foundry gen run seam_world -workers 8
```

took **3 min 17 s** wall at a **peak RSS of 861 MB**.

| Stage | Time | Output |
| --- | --- | --- |
| rockface | 1 min 16 s | 159 sections, 0.9 M triangles (10.7 M meshed) |
| rockfall (shared cache) | 18 s | |
| savannah trees (searsia decimation) | 63 s | |
| the union terrain (from earlier) | ~13 s | |

**Placements.**

- canyon_west: **7,656** (the scatter's 7,497 plus 159 wall sections).
  - talus 994
  - boulder_field 712
  - wash_stones 3,000 (at its cap)
  - trees 134
  - deadwood 112
  - bushes 2,545
- savannah_east: **2,889**.
  - tree_clumps 74
  - boulders 221
  - shrubs 1,605
  - stones 989
- Shared: 120 (tiles, far ring, strips).

**Scatter placements per 40 m x bin (|z| < 250), west/east:**

| x from (m) | west | east |
| --- | --- | --- |
| −120 | 559 | 3 |
| −80 | 436 | 18 |
| −40 | 469 | 54 |
| 0 | 235 | 85 |
| 40 | 107 | 157 |
| 80 | 25 | 229 |
| 120 | 21 | 202 |
| 160 | 4 | 205 |
| 200 | 0 | 241 |

**One region again.** The savannah's shrub density was edited, then:

```
foundry gen run seam_world -region savannah_east
```

took **2.4 s** wall at 363 MB. 1 stage ran, and the scoped splice was
"scenery −3207 +2889". canyon_west's 7,656 placements in the level file
were deep-equal before and after. Before stale-only filtering, the forced
version reran the region's 5 stages in 11.4 s.

**Renders.**

- Bench poses, 1600×900, TAA, vis buffer, VSM: `place_pose_0.png` and
  `place_pose_1.png` (ground at the seam, east and west),
  `place_pose_2.png` (28 m up, looking east across the band) and
  `place_pose_3.png` (30 m up, looking along the seam).
- Desert boulders and stones thin out going east and the first shrubs
  appear. From 30 m along the seam the desert's walls and rocks give way
  to sparse shrubs over the band. There is no line where rocks stop.
- **Load** 8.7 s (2.3 s for terrain alone). **Frame p50 on WSLg:**
  111–113 ms on the ground and 192–194 ms at 30 m, against about 23 ms for
  terrain alone in phase 3.
- That cost is the 16.8 k placements: 3,000 wash stones, 2,545 bushes of
  9 k triangles, 134 trees of 116 k. It is not the region code. It has not
  been judged on the GTX 1070.

## Gaps

- **Regions do not see each other's rocks.** Each region's Poisson keep
  sees only its own, so in the band two regions' rocks can overlap. This
  is rare at the presets' densities and was not seen in the renders.
- **Footprint edits rerun every region.** Expanded stages hash in the
  regions stage (phase 1's decision: the weights depend on every region).
  Only preset edits are region-local.
- **The shared rocks cache is never pruned.** An edited rocks stage
  leaves its old `_shared/rocks_<hash>` folder behind.
- **`RockMean`** (cliff_wall's `match_to: rock`) is still the lead
  environment's.
- **drift** does not run per region, and refuses an eight-layer ground.
- **The editor's Run Region was not clicked by hand.** It compiles and
  shares the CLI's tested path (`RegionStages` and `RegionScope`).
- **Frame cost with full placement** is high on WSLg and unjudged on the
  1070. The savannah's density and triangle counts are phase 6's to tune.
- **Pre-change classic outputs were not recorded.** The placement pin is
  post-change; see the tests section.
