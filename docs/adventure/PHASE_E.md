# Phase E: the `adventure` level

Status: built (uncommitted). The level `adventure` is in the OneDrive project
(`resources/generators/adventure.fgen` → `resources/levels/adventure.yaml`,
generated output under `resources/generated/adventure`). A third-person
mechanic starts in a slot canyon, walks up it into a cave, climbs the cave
through the massif to a ledge high on its east face, and walks down a
mountainside of ruins to a relic on an altar at the foot. Reaching the relic
wakes it and ends the level. A scripted walk goes from the spawn to the relic
without help (below).

`$S` is the session scratchpad,
`/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/ec789bcb-0346-4ea6-a32e-b00173193e5a/scratchpad/phe`:
the scratch project `$S/proj` (its own resources, the repo's shaders and
fonts linked, OneDrive skeletons/sounds/mechanic linked read-only), the tools
`$S/tools/*.py`, the play scripts `$S/*.play`, the shots `$S/shots*`. The
final shots are in `$S/final/`.

## Layout

World units are metres; +X east, +Z south (the relief image's down). The
map is 768 m square at 0.5 m (`x, z ∈ [-384, 384]`), 36 tiles of 128 m, and
a 3 km backdrop (`far: 3000`, 8 m cells).

```
 z
-160 ┤                                   ┌──── ruins chunk (x 96..384, z -160..220) ───┐
     │                         massif     │  south spur (170,-38) h145                  │
   0 ┤                     crest x≈20–35  │                                            │
     │                     peak 265 m     │ ledge (97,38) y 93.3 ─ terraces ─► relic    │
  38 ┤            cave ═══════════════════╪═► mouth (88,37)      (124…322, 20…55)  (314,35) y 11
     │           ╱ switchbacks y 20→93    │  north spur (165,112) h150                 │
 120 ┤ slot ═══╗╱  skylight chamber (-78,120)                                         │
     │ canyon  ║  lower mouth (-110,124) y 17.5                                       └────
 240 ┤ chunk (x -384..-104, z 20..240)                                                 
     └────────┬──────────────┬──────────────┬──────────────┬──────────────┬───────── x
           -384           -110            0             100            384
```

| Area | Where | Heights |
| --- | --- | --- |
| Spawn | (-335, 13.4, 131), facing +X | slot floor 13.4 |
| Slot canyon | centreline from the head (-112, 122) WSW to the map edge (-384, 128), ~270 m walked from the spawn; 5–10 m wide (meander 5) | floor 13.2 → 17.4, rims 50–55 (38 m deep) |
| Plateau | spine (-420…-60, 105…135), radius 80–100 | top 55 |
| Lower cave mouth | slot head (-110.4, 17.5, 124.2) | on the slot floor (level) |
| Skylight chamber | (-78, 20, 120), 20 m wide, 27.5 m tall: its vault breaks the plateau surface (a ~10 m oval skylight) | floor 20 |
| Rock-pile chamber | (-14, 37, 88), 22 × 15 m | floor 37 |
| Switchbacks | (18, 48, 48) → (8, 54, 26) → (-14, 60, 30) → (-22, 66, 52) → (-6, 72, 68) → … | 10–16° grades |
| Upper mouth | (88, 93.1, 37.3) facing +X | floor 92.4 → 93.3 over the last 12 m |
| Ledge | platform (97, 38) radius 16 | 93.3 |
| Ruins | path (114,38)→(142,54)→(168,22)→(198,52)→(228,20)→(258,50)→(290,24)→(322,40): 9 terraces, 8 flights (58 steps of 0.18 m, the last 22) | 93.4 → 10.0 |
| Relic | on the lowest terrace, end of the colonnade: altar at (314.2, 10.0, 34.8) (placed by `make_level.py` from ruins.yaml) | slab top 11.07 |
| Valley | ramp to (370, 30) | 3–10 |
| Backdrop | far mountain stamps at (1100–2300, -500…900) up to 650 m high; plateau stamps (mesas) 130–210 m; spurs flanking the ruins | |

The cave is 373 m of plan path with 20 control points, climbing 76 m
(average 12°, steepest 16°), from the slot's head to the ledge.

## Stages (adventure.fgen)

| Stage | Kind | Notes |
| --- | --- | --- |
| `tex` | polyhaven_textures | red_sand, cracked_red_ground, dry_river_pebbles, cliff_side (as desert_canyon) |
| `ground` | terrain | 768 m @ 0.5 m; `mesa_height: 0`, relief 4; slot canyon (`profile: slot`, `hold: 1`); stamps: plateau, massif (3-point mountain), two flanking spurs, far mountains and mesas, the ledge platform (`hold: 1`), the ruins ramp; **`chunk: none`**; far 3000 @ 8 m, **`far_terrace: 0.2`** |
| `ground_mat` | terrain_material | desert_canyon's, plus `rock_slope (27, 40)`, `cracked_cover 0.45`, **`sand_ripple 0.35`** (0.14 m, wind 80°), **`far_scale 6`** |
| `masonry` | polyhaven_textures | **old_sandstone_02** (temples), **medieval_blocks_02** (walls), sandstone_cracks (terraces) |
| `boulders`, `stones`, `patches` | polyhaven_rocks | namaqualand boulders 02–06 (lite 5k), stones/rocks sets (lite 2k), sand_rocks_small_01 (120k, lite 30k) |
| `dryplants`, `trees` | polyhaven_plants | wild_rooibos_bush, dead_quiver_branch_01/02, searsia_lucida; quiver_tree_01, dead_quiver_trunk |
| `props` | polyhaven_rocks | brass_vase_02 (the relic), brass_diya_lantern (cave lamps), wooden_ladder (imported, not placed) |
| `cave` | cave | the path above; `blocks 0.85`, `block_size 2.6`, `drapery 0.55`, rocks = boulders at 0.2/m² up to 2.8 m, **`ambient: true`** (22 volumes, dust 0.004), **`chunk: cave`**, `lip 0.15` |
| `rockface` | wall_sheet | `caves: cave` |
| `scatter` | scatter | slot only (**`near`** = the canyon path, radius 7–11): boulders, wall-foot talus (`cliff_base` 1 within 2.5 m), sand-and-rock patches, rubble (stone scans ×1.4–3.6), dry plants |
| `ruins` | ruins | `site_cell 0.15`, `simplify 0.012`, `rounding 0`, `outliers 24`, `spread 32`, `terrace_length (14, 26)` |
| `ruin_dressing` | scatter | after the ruins, **`avoid: ruins`**: boulders and dry plants within 40–48 m of the ruins path, quiver trees by the relic |
| `air` | environment | sun yaw 200 / pitch -72 (high, travelling WSW: it lies along the slot's head and backlights the ruins vista), HDR 1.5 sun, haze 1700 m, view 4500, **shafts** density 0.0008, **`shafts_ambient 0.15`**; spawn is the skeleton's |

Hand-made (`make_level.py`, kept by every splice): the player block (third
person, `models/mechanic.fmdl` + `mechanic_crew`, boom 4.2 m, left shoulder),
the three area chunks (`canyon` and `ruins` each footprinted by two tiny
buried solid nodraw corner brushes at y −150; `cave` brushless), the spawn,
the level script, the `slot_air` ambient volume (sky 0.55, ground 0.65, dust
0.006 — the slot's local dust for the shafts), the relic (altar brushes in
the ruins chunk, the vase scaled 2.2, a dynamic point light, `relic_motes` and
`relic_flare` emitters, the `relic_trigger` volume), three oil lamps with dim
warm lights in the cave's darkest stretches.

Scripts and UI (OneDrive `resources/`): `scripts/adventure/level.flang`
(streaming), `scripts/adventure/relic.flang` (the ending),
`scripts/adventure/end_screen.flang`, `ui/adventure_end.fui`,
`particles/relic_motes.fpar`, `particles/relic_flare.fpar`.

**Streaming** (level.flang, every 250 ms by the player's position):
`canyon` on while x < -30 and y < 50 (off past x > -5 or y > 56); `ruins` on
while y > 56 or x > 60 (off below y 50 west of x 40); `cave` on between
x -250 and 125. Placements: canyon 1206, cave 493 (the sections within 28 m
of a mouth stream by place, i.e. always in), ruins 517; terrain, backdrop and
the massif's cliffs always in. 2,680 placements in all.

**The ending** (relic.flang): the player entering `relic_trigger` bursts the
flare's swell and 90 sparks, swells the relic light from 3 to 25 over two
seconds, then puts up `adventure_end` and fades a warm white scrim in, then
the title ("The Valley of the Ancients"), then Again (restart_level) /
Leave (quit_game). No HUD at any point; no hint text.

## Engine changes (all Added params, defaults unchanged unless noted)

| Where | What |
| --- | --- |
| terrain stage | `chunk` (auto \| none): tiles stream with no chunk |
| terrain stage (far) | `far_terrace`: scales the backdrop's terracing |
| terrain_material | `sand_ripple`, `sand_ripple_wavelength`, `sand_ripple_wind`; `far_scale` (backdrop layer repeats) |
| material splat | `ripple`, `ripple_wavelength`, `ripple_angle`, `ripple_layer` → `fxSplatRipple`; `surface_fx.glsl` `fxRipple` (normal + trough AO on the rippling layer's blend share; level ground only; footprint-faded) |
| scatter / grass bands | `near` (spline x,z,radius) + `near_soft`: keep a rule to an area (`place.Rule.Near`) |
| scatter | `avoid` accepts a ruins stage (terraces, plinths, stairs as footprint discs) |
| cave | `chunk` + `chunk_mouth`; `ambient` + `ambient_sky/ground/fade/margin/dust` (ambient volumes on the interior boxes) |
| cave (Version 2) | segment choice by a centreline at most 2.5 m over the floor (a tall chamber's floor stepped up across it); the floor lifts to the ground only where the ground is level (a mouth at a steep bank's foot rose into a 1.5 m hump) |
| ruins (Version 2) | parapets are their own part weathered at 0.3, with bigger square merlons and a pyramid cap (they eroded to cushions); default grade warmer (`stone_color (0.8,0.56,0.37)`) and `saturation 1.25` |
| environment | `shafts_ambient`, `shafts_strength` |
| harness | a tick reconciles chunks a script switched (as DrawWorld does): a headless run's streamed area stayed out of collision until the next picture |

Tests: `place/near_test.go`, `stages/cave_phase_e_test.go`; suites below.

## What was tuned, from the shots

- **Haze.** Shafts at 0.012 fogged everything (cave chambers grey-white,
  vista washed out). Global dust 0.0008 with `shafts_ambient 0.15`; dust put
  where it belongs with ambient volumes (slot 0.006, cave 0.004).
- **Sunlit patches in the slot.** At pitch -55 no sun reached the 38 m deep
  floor; -72 at yaw 200 (along the head's reach) lights strips of floor and
  the lower walls, as 113107.
- **Slot floor** (was plain flat sand): ripples (engine), wash gravel and sand
  bars from the terrain material, Poly Haven rubble ×960, wall-foot talus,
  sand-rock patches, boulders, dry plants — all fenced to the slot by `near`.
- **Ruins colour/banding.** `large_sandstone_blocks` is a grey trim sheet:
  its green-stained band and yellow line were the "triplanar banding" on every
  moulding. old_sandstone_02 + medieval_blocks_02 and the warmer, saturated
  grade give the reference's warm sandstone.
- **Crenellations**: crown part + `rounding 0` → square merlons.
- **Backdrop stipple.** Far peaks showed a regular stipple (the backdrop
  material's 10 m rock repeat seen a kilometre off): `far_scale 6` turns it
  into strata. (`far_terrace` was tried first; it is kept, harmless.)
- **Cave floor steps** (scripted walk blocked): the chamber step and the
  mouth hump, fixed generically (cave Version 2); the mouth floor set level
  with the ledge.
- **Rocks on the ruins' walkways**: `avoid: ruins`.
- **Pale terrain triangles at mouths**: gone with the level mouths and the
  floor-lift change (not seen in the final shots).
- **Blotchy tower shadows**: not seen after the phase B VSM fix.

## Verification

- `foundry gen check` ok; `foundry check adventure`: the level loads (3
  chunks, 24 volumes, 5 lights, 2 emitters) and its screen and three scripts
  lint clean (the project's other scripts' problems are not this level's).
- Tests: `go test -short ./internal/procgen/... ./internal/material/...
  ./internal/harness/ ./internal/gfx/ ./internal/world/` ok; GL
  `internal/render -run 'MaterialFx|Splat|SceneBlock'` ok; `go vet` on the
  touched packages clean; `go build -tags release ./...` ok; terrain goldens
  and the desert-canyon hash pins unchanged.
- **Scripted walk** (`$S/walk.play`, from `tools/make_walk.py`: 100 `to`
  legs — down the slot's centreline, through all 60 cave samples, across the
  ledge, down every flight — then onto the relic): `foundry play run -draw=false`
  arrives at the altar in 163 s of game time and `relic_trigger.attr.woken`
  reads true — in the scratch project and again in the OneDrive project. Two momentary "blocked" lines in the cave (a corner, the walk
  continues).
- Shots (`foundry play run -gi off -size 1600x900`, WSLg): `$S/final/`.

| Shot | Reference |
| --- | --- |
| `01_slot_start.png` | the slot from the spawn: layered walls, gravel and sand floor, rubble, a sunlit wall strip |
| `02_slot_shafts.png` | 113107: the player in the slot's head with a sunlit, rippled sand patch and the shafted air |
| `03_cave_skylight.png` | the skylight chamber's shaft of light |
| `04_cave_rockpile.png` | 113117: the dark chamber floor of fallen blocks and scanned rocks in rippled sand |
| `05_cave_mouth.png` | 113107 background: drapery round the bright upper mouth, a tower beyond |
| `06_ledge_vista*.png` | 113157: from the top terrace over the stairs, towers and the strata spurs to the hazy valley |
| `07_tower.png` | a stepped tower close up (cornices, dentils, turrets, crown) |
| `08_stairs_down.png` | walking down the second flight, towers either side, the spur and the valley beyond |
| `08_terraces_down.png` | from the top flight over the terraces to the far strata wall |
| `09_goal.png` | 113549's glowing object: the relic on its altar at the end of the colonnade |
| `10_relic_wakes.png` | the player at the altar: the flare and the light swelling |
| `11_end.png` | the ending's fade and title |

## Sizes

OneDrive `resources/generated/adventure`: **522 MB** — ground 324 MB (tiles
209, far ring 100, maps 15), rockface 90 MB, ruins 65 MB (site 48, library
16), cave 35 MB, ground_mat 8.5 MB, scatter < 1 MB. Poly Haven imports and
textures written by this run: ~190 MB under `models/polyhaven` and ~205 MB of
`textures/ph_*` (shared with any generator naming the same assets). So about
0.9 GB on C:, under the 1.5 GB budget. A fresh OneDrive run took 11 min 49 s
(peak RSS 1.9 GB, `-workers 6`); the ruins site 4.7 min of it.

## Known gaps

- **Vista composition.** The ruins stand on a broad ramp between two spurs,
  so the ledge vista is a valley of terraces and towers under strata cliffs,
  not the reference's ruins growing out of cliff faces; nothing places
  buildings into cliffs (phase D gap). The top terrace sits on the ledge, so
  the first towers stand right beside the viewer.
- **Rock pile.** The cave's blocks are SDF blocks rounded by the mesher, plus
  scanned boulders; the reference's sunlit pile of sharp broken slabs at every
  scale is not matched, and the chamber is lit only by lamps and the sky's
  bounce (dark, as a cave is). Denser rocks (0.32/m²) blocked the lane.
- **Ripples** are a normal-map effect (no parallax); they fade beyond ~10 m.
- The far backdrop is coarse (8 m) and reads through haze; its strata repeat
  horizontally.
- No ropes, prayer flags or ladders placed (wooden_ladder is imported in
  `props`; Poly Haven's ladder is a step ladder, not a cliff ladder).
- The relic is a Poly Haven brass vase lit by a point light and additive
  sprites; there is no emissive surface path for Nanite scenery.
- WSLg shots (Mesa D3D12) are not the 1070's; performance is phase F's.
  Budgets kept: 2,680 placements, scatter draw distances 70–600 m, scanned
  rocks lite (2–6k triangles; patches 30k), ruins site 1.05 M triangles.
- `foundry play` cannot render against the OneDrive project directly (its
  `resources/shaders` is empty: the harness reads the project's shaders); the
  OneDrive level was checked with `foundry check` and the blind walk, and
  shot from the scratch project generated from the same document.
- The ripple shader, the cave/ruins fixes and the new params need the editor
  rebuilt from this tree to show in the user's installed editor.

## Phase E2: visual polish

From the lead's review of the phase E shots. `$S2` is
`/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/ec789bcb-0346-4ea6-a32e-b00173193e5a/scratchpad/phe2`
(play scripts `a.play` slot/cave, `b.play` ledge/ruins, `c.play` goal; the
earthy recolour tool `recolor/`); the scratch project is still `$S/proj`.
Final shots: `$S2/final/`.

### What changed

| Item | Fix |
| --- | --- |
| **Backdrop wall** | The far mountain stamps (four 260–380 m radius massifs, 380–650 m high, merged into one wall that filled the sky with tiled strata) are gone. The far ring now has: two `cut` stamps sinking a ~110 m deep desert **basin** east of the map (all beyond x 450: the map is untouched) — the ruins' valley ends on a scarp over it; four mesas and five low buttes (`plateau`, rough 0.8, talus falloff) standing in it; six lone **peaks** along the horizon at 1.5–3 km, each a jagged 7-point crest (`mountain`, ridges 1, rough 0.8, heights 210–340 m above the basin) — shark-fin silhouettes, not cones. Seen from the ledge they sit 3–8° over the horizon in aerial haze. |
| **Fog wall** | The default height fog (base 0, density 0.006, falloff 0.12: a 10 m thick opaque layer at the valley floor's level) was the flat beige band. Now `height_base -112` (the basin floor), `height_density 0.0012`, `height_falloff 0.014` (≈70 m scale height) and `aerial_distance 1300`, `aerial_scale_height 450`: haze thickens with depth and down into the basin. |
| **Floating rock at the upper mouth** (cave Version 3) | Rock drapery hangs from the analytic roof; where a mouth's roof opens to the sky the fin's foot is still under the ground's surface and was meshed as an island. `dropFloatingCave`: every connected piece of a section's raw mesh that touches none of the section box's faces is dropped (the raw mesh is the rock clipped to the box, so anything joined to the walls touches a face). Generic: also strata slivers and sealed air bubbles. |
| **Cave mouth ground material** (cave Version 3) | The ground round a mouth wore the *far backdrop's* material (the first terrain placement with a material is a far tile: six-times repeat). It now wears the ground stage's map material. |
| **Floating ruin fragments** (ruins Version 3) | A ruined gateway's cut left its arch stones floating over the colonnade (09_goal). `dropUngrounded`: a variant's pieces whose lowest point is over 0.5 m above its floor are dropped. |
| **Rock pile** (cave, new Added params, `cave_pile.go`) | `piles` (per 100 m², only where the cave is ≥ `pile_min_width` wide, off the clear lane): each pile a core (a huge slab from `pile_slab_rocks` — new `slabs` stage: Poly Haven rock_face_01/02, coast_rocks_05, coast_land_rocks_02 — or a big block), blocks round it (`pile_rocks`: the namaqualand boulders, lite), chips in a skirt (`pile_chip_rocks`: namaqualand stones/rocks, lite), sizes falling off outward. Every piece **rests on what is under it** — the floor (the void's own blocks included) or the pieces before it, each a dome of its `Top` (not bounding height) — sinking only `pile_sink` (8 %); blocks/slabs lean up to `pile_tilt`, chips any way up. 51 piles, ~6,000 pieces (chips 2/m²; draw/shadow distances 140/50 m, chips 56/20). Floor rocks 0.2→0.12/m², the void's fallen SDF blocks 0.85→0.55 (they were the "blobby mounds"). |
| **Cave light** | `darkness` 0.95→0.88; the rock-pile chamber gets three warm lamps (3.2/2.6/2.6, radius 13–15 m, two new lamp models against its walls) and the skylight chamber a dim fill (make_level.py `PILE_LIGHTS`). |
| **Ruins city** | `outliers` 24→110 (78 fit), `spread` 32→75, `max_plinth` 24→46 (towers on tall plinths out of the spurs' flanks), new Added `outlier_scale` (1.1–1.75: the slope towers bigger than the terrace ones), `buildings 1`, `walls 0.85`, `gates 0.5`. All library instances (10 variants). Site mesh 1.05 M → 2.40 M triangles (plinths), 48 → 115 MB. |
| **Slot** | Rubble density 60→140 (2,224 placed, lite 2k), plants spacing 1.5→0.9 (112), `shafts_strength 1.5`, `shafts_ambient 0.1`. Top-down splat layers on steep faces blend into their own triplanar projection (`surface_fx.glsl` `fxSplatSample`/`fxSplatTriSum`). Terrain material `streaks` 0.4→0.25. |
| **Player body** | `adventurer_khaki` (+ `_normal`, the mechanic's): the mechanic_crew texture with the orange suit recoloured to khaki canvas and the reflective stripes to brown leather, skin/boots untouched (one-off Go tool `$S2/recolor/`). |

Tests: `stages/cave_phase_e2_test.go` (floating piece dropped / clipped kept;
pile dome; ungrounded ruin piece dropped).

### Verification

- Scratch and OneDrive regenerated from the same document (OneDrive run 19 min 24 s, peak RSS 1.99 GB, `-workers 6`); `foundry check adventure` loads it (3 chunks, 24 volumes, 8 lights). OneDrive `generated/adventure` 522 → **599 MB**, + ~78 MB of new Poly Haven slab scans.
- **Walk**: `make_walk.py` now steps 3 m on along each flight before turning for the next (a straight line from a flight's foot cut its parapet's corner — the OneDrive site mesh differs from the scratch one by float noise and caught the walker there). Spawn → relic in 164 s of game time, `relic_trigger.attr.woken` true, in the OneDrive project (`-limit 400`: the default 120 s game-time limit stops it); momentary blocks only.
- `go test ./internal/procgen/stages -run 'TestCave|TestRuins|TestPileDome|TestEveryKindDocumentsItself'` ok; `go vet` stages/ruins clean.
- Shots: `$S2/final/` (01–11 as phase E, plus `03_cave_skylight_pile`, `04_cave_rockpile_b`, `06_ledge_city`), rendered with a binary built from the tree as it stood (`foundry2`).

### Still short of the references

- **02 slot smear**: the close right wall in the original 02 pose still
  shows a smeared, hatched texture. It is not the splat's projection (two
  shader attempts changed nothing; one reverted) nor the mouth material;
  walls framed at 2–3 m look right (`sf/b.png`, `sf/c.png`). It is the
  third-person boom pressed against the wall at < 1 m (magnified rock
  texture + varnish streaks). The final 02 is shot 1.5 m off that wall. A
  close-range detail layer on the rock (detail_normal on rockface) would be
  the real fix.
- **Rock pile**: piles of scanned blocks and chips now stack at the chamber
  walls, but the lane is still sand with scattered stones, and the chamber
  is lamp-lit and dim — no sunbeam (100 m of rock over it). The reference's
  floor is rock wall to wall.
- **Peaks/mesas** are 8 m backdrop cells: silhouettes read, faces are soft.
  Plinths are plain boxes; no ropes/flags.
- **Ruins render darker** (brown, not tan) in the final shots than in the
  mid-phase ones (`$S2/s3b/`), with the same data: it arrived with the
  parallel trails session's surface_fx/material changes landing in the tree
  (a binary from before them renders the same level bright). Not this
  phase's; re-shoot once that work settles.
- Placements 2,680 → ~9,900 (piles 6k, rubble 2.2k): phase F should check
  CPU cost of the pile instances (all small, lite, short draw distance).
