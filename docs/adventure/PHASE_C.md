# Phase C: authored terrain and the cave stage

The spec (docs/adventure/SPEC.md) asked for two procgen pieces: terrain
**stamps** so the adventure level's layout is designed rather than left to
noise, and a **cave** stage — an SDF cave along a 3D path, cut through the
terrain, dark inside, open at its mouths. Both are documented for users in
[docs/guide/33-generators.md](../guide/33-generators.md) ("Authored
landforms (stamps)" under terrain, and "cave (Placement)").

`$S` below is the session scratchpad,
`/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/ec789bcb-0346-4ea6-a32e-b00173193e5a/scratchpad/phc`:
a scratch project (`$S/proj`, its own resources with the repo's shaders and
fonts linked in; never the OneDrive project) and the shots.

## 1. Terrain stamps and hold

**Code:** `internal/procgen/terrain/stamps.go`; the `stamps` list and the
canyons' `hold`/`hold_margin` in `stages/terrain_landform.go` and
`stages/terrain.go`; the preview draws each stamp point's radius and falloff
(`terrain_preview.go`). The editor needs no change: every spline in a list is
already editable in the 2D view.

- Kinds `mountain` (crest polyline, concave profile, ridged spurs, rough
  outline; segments combine by max), `plateau` (flat top, rim over
  `falloff`), `platform` (discs flattened to an absolute height), `ramp`
  (flattened path at absolute heights along it), `cut` (lowered path/pit).
- Applied by the landform shaper after the other landforms, so erosion and
  the far backdrop treat them as base shape. `Landform.Any` includes them.
- **Hold**: a per-sample weight (stamp footprints × `hold`; canyons within
  their width plus `hold_margin`). After drainage, terracing, droplets and
  talus — before detail — the heights are blended back toward the post-carve
  ones (`field.applyHold`). Without it a 6 m slot 35 m deep at 0.5 m filled
  with 11 m of talus (`floor 8 → 19`); with `hold: 1` the floor stays within
  the detail noise of the carved one.
- All parameters `Added` and off at default: the terrain golden and hash
  tests pass unchanged.

## 2. The cave stage

**Code:** `internal/procgen/cave/` (void, rock, cut, sky bake; pure
computation, layering rule added) and `internal/procgen/stages/cave.go`.
`meshout.HeightGrid` gained `Skip` (quads left out). `wall_sheet` gained
`caves` (its shell has the void cut out, `wallsheet.Field.Carve`).

- **Void**: Catmull-Rom path of floor points (width, height), wander,
  nearest-segment local frame via plan bins; section = vertical walls +
  elliptic vault + flat sand floor; roughness; `sdf.Strata` ledges; drapery
  fins (ridged-noise crest lines hanging from the vault, patchy); breakdown
  blocks on two lattices (rotated rounded boxes, half buried; a clear lane
  whose rubble is ≤ 0.28 m proud). Where the ground lies up to 2.5 m over
  the floor (min-filtered over 1.5 m so a cliff face does not count) the
  floor lifts to just under it: out of a mouth the ground is the floor.
- **Cut**: quads whose corners, centre or (on cliffs) a column up their
  middle lie in the void dilated by `cut_margin`, more than 0.5 m over its
  floor. Every touched tile (a hole, or the cave under it) is recooked from
  the terrain stage's own plan/quant/base, open (no skirts — they would wall
  the tunnel), unused vertices removed (otherwise coarse DAG levels reached
  across the hole), and supersedes the terrain stage's tile.
- **Rock** = terrain solid (first-order distance, top 12 cm under, or `lip`
  over round holes on steep ground) minus the void shrunk 2 cm (the cave is
  in front where wall_sheet also meshes the void). 32 m sections, boxes
  overlapping half a cell; box faces dropped only where all three corners
  are deep in the unbounded field; simplify 1.2 cm with open edges locked;
  DC sharpness 0.6.
- **Bake**: normals from the field; alpha = (0.25 + 0.75·fieldAO) ×
  (1 − darkness·(1 − (sky·gain)^0.75)); sky from a 1 m voxel grid (48
  hemisphere rays, DDA through solid voxels, the open terrain past it).
- **Parts**: `sand_*` (sand layer), `rock_*` (rock layer + ground blend into
  the cave floor's own height/colour maps), `ground_*` (tile material);
  mesh collision, draw 700 m / shadow 250 m.
- **Rocks**: optional rocks/polyhaven_rocks stage strewn on the floor, a
  darkened material copy per step of sky darkness, small and collision-free
  in the lane.
- **cave.yaml / artifact**: settings, bounds, mouths, interior boxes (16 m
  runs under the ground), samples every 6 m with sky visibility and
  under-ground flag; `Load` rebuilds it; `VoidOn(grid)` gives the exact void.

### For a level doc

```yaml
- stage: cave
  id: cave
  params:
    terrain: ground
    ground: ground_mat
    path:                       # floor points; start and end outside the ground
    - {x: -51, "y": 36.2, z: 1.6, width: 6, height: 6}
    - {x: 0, "y": 48, z: -8, width: 16, height: 13}     # a chamber
    - {x: 37, "y": 94.9, z: -75, width: 7, height: 6}   # out onto the ledge
    rocks: stones               # optional
- stage: wall_sheet
  id: rockface
  params: {terrain: ground, ground: ground_mat, caves: cave}
```

Phase E: put an ambient-darkening volume on each `interior` box and lights
at low-`sky` `samples` (phase B's volume type); place more Poly Haven rocks
via `rocks`. Quote `"y"` in the path (YAML 1.1 reads `y` as true).

## 3. Verification

**Tests** (all pass):

| Package | Test |
| --- | --- |
| procgen/cave | `TestTheVoidIsOpenInsideAndRockOutside`, `TestTheCutOpensTheMouthAndOnlyTheMouth` (cliff cut, flat ground in front untouched, no kept quad through the void), `TestTheRockIsTheGroundLessTheVoid`, `TestTheSkyDarkensAwayFromTheMouth`, `TestTheRockMeshes` |
| procgen/stages | `TestCaveStageRegisters`; `TestCaveCutsTheTerrainAndOpensItsMouth` (fake Poly Haven, real cooks: tiles superseded, no level-0 tile triangle in the void, a ray from outside to inside hits neither terrain nor rock, rays from the chamber hit rock in every direction away from the mouth, the manifest loads back to the same void); existing terrain/hash/golden tests unchanged |

**End to end** (`$S/proj/resources/generators/advtest.fgen`, 512 m at
0.5 m): a 40 m plateau stamp, a slot canyon cut into it (`profile: slot`,
6–10 m wide, 34–42 m deep, `hold: 1`), a mountain massif (270/220 m crest
heights, 192 m after erosion), a platform ledge at 95 m with `hold: 1`, a
ramp down the far side, and a 14-point cave from the slot's head up to the
ledge (~180 m, climbing 59 m, two chambers), wall_sheet with `caves`.

| | |
| --- | --- |
| terrain (1025², 16 tiles) | 21 s |
| cave: cut + 4 tile recooks | 5 s (302 quads cut) |
| cave: sky bake (168×165×141 voxels) | 0.7 s |
| cave: 12 sections | 24 s total (1.9 M triangles meshed, 0.16 M kept; summed: mesh 69 s, simplify 9 s, bake 9 s, cook 5 s) |
| cave output | 39 files, ~9 MB of meshes + 24 MB of recooked tiles; 152 rocks |
| peak RSS (cave run) | 0.45 GB |

`foundry play run` (headless GL, `-gi off`), shots in `$S/shots8` …
`$S/shots12`: the slot canyon (sheer layered walls, light from the top),
inside looking back at the bright slot mouth, the dark first chamber (sand,
blocks, drapery), the upper mouth from the ledge (drapery fins hanging in the
opening, strata — the reference's look). A scripted walk (`$S/walk3.play`)
goes from the slot through the mouth and the first chamber 75 m into the
cave without being blocked.

Bugs found by the shots and the ray test, fixed: section seams (lattice
points on a shared box face counted outside by both sections → a crack;
fixed by half-cell overlap and corner-based face dropping), simplification
moving open borders, coarse LODs reaching across a tile hole (unused
vertices), pits where the void floor ran under ledge ground (floor lift), a
lip sill on gentle ground (lip only on steep ground).

## Known gaps

- **Lighting of the interior is vertex AO only.** Scanned rocks are darkened
  through their material; phase B's ambient volume should replace both.
- **Nav**: recooked tiles are open, so the nav build reads them per
  triangle (heavier; not measured). No agents were tested in the cave.
- **drift** recooking a tile the cave cut would reseal it (keep drift off
  the cave's tiles; not detected).
- **Slot heads**: a slot canyon whose head runs into rising ground grades a
  steep floor (25–35° in the test: the scripted walk could not climb it);
  and wall_sheet in a slot narrower than ~2× its `depth` leaves a jagged
  seam between its shells and the terrain floor (pre-existing).
- Steep cave floors (> ~30°) stop the walker; the designer keeps path
  gradients gentle. The test's last climb to the ledge is too steep.
- **The test's lower mouth** (`$S/shots13/slot_mouth_outside.png`), on the
  slot's steep head slope, still shows a few pale triangles of terrain /
  rock-top over the cave's sand floor where the floor lift, the cut and the
  steep ground meet. The ledge mouth (gentle ground) is clean. Phase E should
  put each mouth's floor on level ground at the ground's height.
- The region (union) terrain does not apply `hold`.
- Performance on the GTX 1070 is not measured (phase F).
