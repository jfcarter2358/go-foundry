# Ground trails: sand and snow underfoot

Walkers press footprints into the sand and snow of generated terrain. Their steps there make the ground's own sound and kick up a puff of it, and the ground drags at them. This applies to **procgen terrain only**. Brush ground and block terrain are being retired and never carry a splat.

## What decides "soft"

A splat layer's `layerN_soft` param, one of `none` (the default), `sand` or `snow`. An optional `layerN_soft_depth` (metres) overrides the kind's own print depth. Both are on the splat effect (`material_kinds_terrain.go`), so any `.fmat` can set them by hand.

The generator sets them by **texture**, not by role (`procgen/stages/soft_ground.go`). A role is only a slot in the ground rules: the pine forest's "sand" slot holds needles and the prairie's holds grass. The rule:

- A Poly Haven id containing `snow` is snow.
- One containing `sand` (but not `sandstone`) is sand.
- A triplanar layer (cliff rock) is never soft.

Today this covers:

| Preset | Texture | Kind |
|---|---|---|
| Desert, dunes | `red_sand` | sand |
| Coast | `coast_sand_01`, `damp_beach_sand` | sand |
| Volcanic (cinders) | `coast_sand_05` | sand |
| Alpine, tundra | `snow_02` | snow |

`terrain_material` and `region_material` are now at **Version 2**. An existing world picks the soft layers up on its next generator run, which also reruns the stages downstream of the material.

## The kinds

| | sand | snow |
|---|---|---|
| print depth | 3.5 cm | 8 cm |
| fill (share of a print that refills in about 45 s) | 0.85 | 0.08 |
| drag on the stride | 12 % | 25 % |
| eye sink | 3 cm | 7 cm |
| step sound / puff | `step_sand` | `step_snow` |

Depth and fill are `material.SoftKinds`, which the shader needs. The rest is `world.SoftGroundKinds`. Sounds and puffs are named `material.SoftStepAsset(kind)`. The release packer ships them with any reachable `.fmat` that has a layer of that kind (`pack/refs.go`), because no content file names them.

## Pieces

1. **CPU lookup** (`world/soft_ground.go`). The sim casts a 1.5 m ray down from just above the feet (`sim.groundUnder`). A scenery hit answers through its own material with `SoftGroundOn(lvl, id, x, z)`, so a cave floor wins over the terrain above it and a rock lying on the sand over the sand. Any other hit (a brush, block terrain) is firm. Only a ray that hits nothing falls back to `SoftGroundAt`, which takes the smallest splat tile whose box holds the point. Either way, the lookup bilinearly reads that tile's weights PNG, decoded on the CPU and cached per material generation. It returns the soft share, the dominant kind, and depth, drag and sink weighted by share. It also returns a colour: each soft texture's average, tinted. Those averages are computed in a goroutine, so the first step doesn't hitch.
2. **Steps** (`sim/footsteps.go`, called once per frame after the entity pass).
   - **Stride.** The player on foot and every AI walker count the ground covered. Stride is `0.45 + 0.17·speed` (0.55 to 1.4 m); feet alternate, 11 cm either side of the line walked.
   - **What a step does on soft ground.** It queues a `world.TrailStamp`, a 15 cm capsule along the foot. It plays the kind's sound with pitch varied ±10% and spawns the kind's puff as a one-shot emitter tinted the ground's colour. A landing presses both feet at once.
   - **Drag.** The player's intent is scaled by `1 − drag`. An AI walker's drag goes to `world.WalkerDrag`, which `ai.steer` takes off the speed it asks for. That makes the drag independent of frame rate and acceleration.
   - **Sink.** `world.PlayerGroundSink` eases over 0.2 s, and `PlacePlayerCamera` lowers the first-person eye by it.
3. **Trail map** (`render/ground_trails.go`, `ground_trail_comp.glsl`).
   - **Format.** A 2048² RG16F texture (16 MB) over 48 m, so 2.3 cm texels, centred on `world.TrailFocus` (the player's feet). It moves when the focus strays 4 m. At 1024² (4.7 cm) a print was 2–3 texels across: a blurred bump with no floor, steep everywhere and black from above.
   - **Toroidal.** World cell `c` lives in texel `c mod 2048`. As the window moves, the texels that scroll in are cleared, so nothing is copied.
   - **Values.** A positive value is a dent, as a share of the soft depth; a negative value is a raised rim.
   - **Print shape.** The shape is in metres: a flat floor, a 1.8 cm wall, and a rim 2.5 cm outside 2 cm wide.
   - **Decay.** Red decays with τ = 45 s, green with τ = 30 min. Each channel banks its time and decays only once the factor is one RG16F can hold: red every 0.25 s, green every 20 s. Near 1.0 a half float steps by 1/2048, so a per-frame factor of 0.9999 rounds a whole step down, and the slow channel faded about four times too fast before this.
   - **Update.** The update in `DrawWorld` runs a full-window pass (scrolled-in texels cleared, decay, the first 32 stamps) four times a second, on a move and on a clear. On other frames it dispatches only over the rectangle each batch of 32 stamps reaches, which for a footstep is a few hundred texels.
   - **Publishing.** The map is published as `gfx.GroundTrail`. `FOUNDRY_TRAILS=0` turns it off; steps still sound and puff.
4. **Shading** (`surface_fx.glsl`, `fxTrailAt` inside `fxSplatSurface`).
   - **Gating.** This runs behind `FX_TRAIL` (256), which `gfx.applyTrail` sets for a splat with a soft layer while a trail map exists. The Nanite material variants compile it out of every bin without one (`visKeyTrail`).
   - **Depth and fill.** From the splat weights already read, the pixel's soft depth is `Σ w·layerDepth / Σ w` and its fill is the soft layers' average.
   - **Parallax march.** The view ray goes down into the print in six steps plus a secant refine, with the slope capped at V·N ≥ 0.25. Every layer is then sampled at the hit point, so a print is a hole, not a stain. Ground with no print nearby costs two trail reads: one at the pixel and one at the march's far end.
   - **Normal and colour.** The normal tilts along the dent's slope (central differences, capped at 1.5). The floor darkens by up to 12% with up to 30% AO, a rim lightens by 5%, and sand ripples are trodden flat inside a print.
   - **Edge.** The effect fades over the window's last 3 m.

## Budgets

- **Samplers.** `fxTrail` (unit 47) is one of the material pass's fragment samplers (`TestMaterialFxSamplersFitTheMaterialPass` wants 9 fx samplers). With it in, the pass reached 32 of 32 until the lighting work folded the eight point-shadow cubes into one cube array. It is now at 25 of 32, and the lighting work has claimed 2–3 of the free slots.
- **GPU.** The update is a full 2048² compute pass four times a second, plus a few hundred texels per footstep. Shading adds 2 reads per soft pixel inside the window, and up to 12 inside a print.

## Not done

- **Shadows.** Prints are shading only and cast no shadow.
- **Geometry.** Nanite would need the same offset in the hardware and software rasters, the material pass and the VSM passes. Deep drifts don't need it at these depths.
- **Vehicles.** Wheels and tracks don't stamp yet. A capsule per wheel along its motion is the obvious next step.
- **Saves.** Trails aren't saved and clear on a level change.
