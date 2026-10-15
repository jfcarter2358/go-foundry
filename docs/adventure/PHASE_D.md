# Phase D: the ruins stage

The spec asked for ancient sandstone architecture as SDF → Nanite, laid out
along an authored path down a slope, textured with Poly Haven masonry, with
mesh collision and draw distances. User docs: the `ruins` section of
[docs/guide/33-generators.md](../guide/33-generators.md).

`$S` is the session scratchpad; the scratch project is `$S/phd/proj`
(its own resources, the repo's shaders and fonts linked in; never the
OneDrive project), the test document `generators/ruintest.fgen`, shots in
`$S/phd/shots1` … `shots5`, the walk `$S/phd/walk.play`.

## What was built

**Code**

- `internal/procgen/ruins/` (pure computation on sdf + noise; layering rule
  added):
  - `profile.go` — plans (Chebyshev rect, stepped cross, circle, ribbed,
    octagon) and **profiles**: a moulding outline in (plan distance, y) swept
    round a plan (`Lathe`). One profile is a tower's whole stack of plinth,
    walls, string courses, cornices and set-back tiers; mitred corners.
  - `compose.go` — `Model` (union with box culling, cutters), face folding,
    repetition along a face, 2D arches (round/pointed).
  - `vocab.go` — plinth/cornice mouldings, niche and blind-arch rows, dentil
    rows, crow-step merlons, corner turrets (miniature shrines), crowns
    (ribbed amalaka, pot, finial), columns (square base, octagonal shaft,
    fluted shaft, cushion capital, cross bracket).
  - `library.go` — 14 variants: `tower_a/b/c` (b with an open arched
    pavilion + ribbed dome), `tower_ruin` (top broken off block by block),
    `shrine`, `pavilion`/`pavilion_ruin` (plinth, front steps, 4×4 columns,
    roof tiers, dome), `gate` (gopuram with a walkable passage), `wall` /
    `wall_broken` (crenellated, blind arcade), `column`, `column_fallen`,
    `rubble_0/1`. Weathering: low swells, grain, eroded pits; bites; the
    mesher's edge rounding.
  - `layout.go` — terraces and stairs as chords of the path (unbroken way
    down), floors over the walkway's ground, sides trimmed where the hillside
    rises above the floor, foundations under the lowest ground; buildings
    facing the walkway, gates, colonnades, walls, slope towers on plinths,
    debris, free spots.
  - `site.go` — the terraces (battered, coursed 0.78 × 1.55 m blocks,
    buttresses, coping, parapet with merlons opened for the walkway),
    stairs (steps + sloped cheeks), plinths, bites; a plan grid.
- `internal/procgen/stages/ruins.go` — the stage (params, validate, run,
  preview, load), Version 1.

**Bugs found on the way** (kept as rules in the code): a cutter answered
with a *bound* next to the surface let the weathering displacement turn it
into surface (cutters are now always read inside their box); every
component capping its field at ~2 m made the mesher evaluate every
subcube (caps raised to 8 m: site mesh 2× faster); floors following the
uphill side of a wide terrace floated tens of metres (now the walkway's
ground, sides trimmed); stairs on slopes near their own angle maxed every
flight (terraces shorten on steep ground; zigzag the path).

## Verification

**Tests** (pass): `procgen/ruins` — `TestVariantsMesh` (every variant
meshes closed within its box and to its height), `TestLayoutStandsOnTheGround`
(floors over the ground, terrace/stair/plinth bodies reach below it, every
building on a floor or plinth), `TestStairsAreWalkable` (each stair joins
two terraces at their floors, riser ≤ limit, tread ≥ 0.25, the walking
height never jumps more than one riser, a cliff band forces a tall wall),
`TestLayoutIsDeterministic`, `TestSiteFieldMatchesTheLayout`; `stages` —
`TestRuinsStageRegisters`, `TestRuinsStageEndToEnd` (fake Poly Haven, real
cooks: every placement mesh collision + draw distance + material, a ray down
onto every terrace and sampled treads lands on the cooked site within 8 cm of
the layout height and over the terrain, manifest loads). Layering test and
`go test -short ./internal/procgen/...` pass.

**End to end** (`ruintest.fgen`, 512 m terrain at 0.5 m: a broad ramp from a
100 m summit platform between massifs; a 5-point zigzag path; `outliers: 30`):

| | |
| --- | --- |
| layout | 8 terraces, 7 flights of 58 steps (0.18 × 0.33 m), 30 plinths, 66 instances |
| library (12 variants used) | 0.63 M triangles kept (5.6 M meshed), 27.6 MB; ~2 min |
| site | 61 sections, 2.61 M triangles kept (13.1 M meshed), 112.6 MB; ~5.5 min |
| stage total | 7 min 47 s, peak RSS 2.5 GB, 141 MB on disk |

Per variant: towers 95–141 k triangles (4.3–6.0 MB), pavilion_ruin 55 k,
walls 10–14 k, rubble 15–29 k, column 5 k.

`foundry play run -gi off` shots (`$S/phd/shots5`): the vista from the top
terrace, a tower close up (cornices, dentils, niches, turrets, crown) and a
carved-detail close-up, an aerial overview of the terraces stepping down
with towers on plinths beside them, the open-pavilion tower in silhouette,
looking down a flight between cheek walls. **Walk** (`walk.play`): `to`
moves from the top terrace down all 7 flights, 99.4 m → 26.7 m, no stall.

## For a level doc

See the guide's YAML. Use a `polyhaven_textures` stage with
`large_sandstone_blocks` (temple), `rabdentse_ruins_wall` (masonry),
`sandstone_cracks` (terrace). Phase E reads `ruins.yaml` (`spots`, terrace
`corners`/`floor`) to scatter props and plants on the floors.

## Gaps

- **Site cost**: the terraces dominate time and disk (113 MB for 8
  terraces; the coursing grooves resist simplification). A bigger level
  should raise `site_cell` toward 0.15 or `simplify`; or the coursing could
  move to the texture. The 300 MB budget fits about two such sites.
- The intact `pavilion` and the `gate` rarely fit the test's trimmed
  terraces (need wider, flatter terraces); nothing yet places buildings
  *into* cliff faces (outliers stand on plinths beside the path).
- Tower colour reads greyer than the reference at a distance; tune
  `stone_color`/`brightness`/`match` against phase B's lighting. Triplanar
  shows faint green/yellow bands on some moulding slopes (from the scan).
- Steep straight paths make long flights and tall foundations: author
  zigzags. Uphill legs work but are untested in a level.
- The 3D preview shows the site and building boxes only.
- No native GTX 1070 measurement (phase F). Instances are few (~70); the
  cost is the site's 2.6 M triangles + the towers' clusters.
