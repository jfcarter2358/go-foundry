# Handoff: detail brushes, baked hangar lights, cheaper point shadows

Three pieces of work that came out of a performance pass on level1 (the
dropship ran at 20 fps in the editor's compiled preview; it now runs at
128–150 fps). They are independent — do them in any order, or hand them to
different people — but read "Context" first, because all three are judged by
the same measurement tool and share the same traps.

1. **Detail brushes** — a per-brush flag that keeps a brush out of the BSP
   partition and visibility, the way Quake's detail brushes work.
2. **Baked lights that keep moving shadows** ("option 2") — bake the hangar's
   lights and still show characters' shadows on the baked floor.
3. **Cheaper live point shadows** ("option 3") — hardware-filtered PCF instead
   of 20 manual cubemap taps.

---

## Context

### State of the tree

Everything below refers to the working tree of `feature/jc/brush-editing`,
which has a large amount of **uncommitted** work in it (this perf pass, plus a
second session's lighting-model/compiler work). Nothing here is committed. Do
not stash or reset — other in-progress work lives in the same files
(`internal/render/static_batch.go`, `internal/gfx/graphics.go`,
`internal/app/app.go`, `resources/shaders/fragment.glsl`). Make targeted edits.

`TestABatchedChunkDrawsExactlyWhatPerFaceDrawingDid` (GL test) currently fails
with ~320 differing pixels; this was verified to be the other session's
in-progress batch/shader work, not the perf pass (it fails identically with
the perf changes reverted). Don't chase it as part of this work.

The compiler's format version moved 4 → 5 (other session). Every `.fbsp` in the
game project (`/mnt/c/Users/jcart/OneDrive/Documents/Foundry/resources/levels/`)
is version 4 and will not load in a release build until recompiled.

### What the perf pass added that you will build on

- **Per-pass profiler** — `internal/render/profile.go`. `render.ProfileBegin(name)`
  / `ProfileEnd()`; CPU (QPC on Windows) and GPU (timestamp queries) per
  section, shown in the F3 overlay.
- **Benchmark mode** — `internal/app/bench.go`. `FOUNDRY_BENCH='level=level1;
  compiled=1;frames=240;warm=90;at=x,y,z,yaw,pitch|spawn;out=bench.txt;
  cpuprofile=cpu.prof;hide=volumetrics'` loads a level, pins the camera per
  pose, writes a per-pass CPU/GPU table, exits.
- **Point-shadow cache** — `internal/render/point_shadow_cache.go`. Each
  shadowed point light has a signature of everything its six faces draw, split
  into a STATIC half (compiled surfaces, chunk brushes) and a MOVING half
  (entities, scenery, sprites, scatter). A light whose signature is unchanged
  is not re-rendered; a light with moving casters in reach keeps a second
  cubemap (`PointShadowMap.StaticCubemap`) holding only the static layer, and
  each face is a depth blit of that plus the moving casters drawn on top.
  **Rule: anything new a cube face draws must add its inputs to
  `pointShadowSignatures`, or its shadow freezes.**
- **Uniform-location memo** — `gfx.UniformLoc`. **Every `gl.LinkProgram` must
  be followed by `gfx.ForgetUniformLocations(prog)`**; a source test
  (`internal/gfx/uniform_cache_test.go`) enforces it.

### How to measure

The user's machine is Windows + GTX 1070; this repo is built from WSL. Measure
natively, not under WSLg:

```sh
B=/mnt/c/Users/jcart/AppData/Local/Temp/foundry-bench   # scratch copy of the game project
GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CXX=x86_64-w64-mingw32-g++ CC=x86_64-w64-mingw32-gcc \
  go build -tags release -o $B/foundry-bench.exe ./cmd/foundry
cp resources/shaders/* $B/resources/shaders/
go run ./cmd/foundrycompile -root $B level1          # ~1.5 min; needed after compiler changes
cd $B && FOUNDRY_BENCH='level=level1;at=1.5,102.7,-36.76,0.785,0|2,102.1,-1,1.711,0' \
  WSLENV=FOUNDRY_BENCH ./foundry-bench.exe; cat bench.txt
```

The scratch copy at `$B` is disposable; never compile into or write to the
real game project without the user asking. `-tags release` builds even when the
editor package is mid-edit and doesn't compile.

**Check the GPU is idle first**: `/mnt/c/Windows/System32/nvidia-smi.exe
--query-gpu=utilization.gpu --format=csv`. During the perf pass the user
started StarCraft II and every GPU number tripled; that cost an hour of
chasing a phantom regression. If the GPU is busy, only back-to-back A/B runs
mean anything.

Useful poses (eye position, yaw, pitch):

| Pose | at= |
|---|---|
| In the ship, facing forward (the original 20 fps spot) | `1.5,102.7,-36.76,0.785,0` |
| In the ship, facing aft | `1.5,102.7,-36.76,-2.356,0` |
| Hangar floor looking at the ship | `2,102.1,-1,1.711,0` |
| The game's own spawn camera | `spawn` |

The ground poses (canyon, basin, compound) are where detail brushes would
matter; pick some along the route in `tools/level1_ground_gen.py`. Ground
chunks are streamed and disabled at level start, so a ground pose needs them
switched on — see `cmd/levelsim`'s `-enable` for the mechanism.

GL tests are opt-in: `FOUNDRY_GL_TEST=1 go test -run <Name> ./internal/render/`
(WSLg provides the display).

---

## 1. Detail brushes

### The problem

level1 compiles to **63,610 portals**. The exact visibility flood only runs
below `coarseVisPortals = 16384` (`internal/compile/pvs.go`); above it the
compiler writes the coarse answer (orientation + connectivity only), and the
average cell sees **6,967 of 8,554** cells. Visibility culling is doing almost
nothing. The portal count is high because every face of every brush — terrain
cells, rocks, props — offers its plane to the BSP.

### What already exists

The compiler already implements the Quake detail bargain, but only for a
brush's **detail mesh** (`world.Brush.Detail *BrushMesh`, `HasDetail()`,
`world/brush_detail.go`) — the free-form surface drawn instead of the hull:

- `compile/csg.go` `gatherSolids`: a detailed brush emits its hull faces as
  `NoDraw` structure and its drawn faces with `Face.Detail = true`.
- `compile/bsp.go`: `BuildBSP` partitions only non-detail faces, then
  `attachDetailFaces` → `insertDetail` drops each detail face into every empty
  cell it reaches (splitting as it descends), and `attachInsetDetail` handles
  faces that land inside solid.
- `compile/light.go`: detail faces are lit like any other.
- Tests: `compile/detail_test.go` — read its header; it states the three
  invariants (drawn / doesn't shape the tree / doesn't break sealing).

A whole-brush flag reuses all of the insertion machinery. What is new is that
a whole-detail brush has **no structural hull at all**.

### Measured payoff (experiment, not committed)

A throwaway compiler that treated brushes as detail by ID prefix, on level1
with the hangar still `moves: true`:

| Detail | Portals | Cells | Surface fragments | Avg visible |
|---|---|---|---|---|
| none (today) | 63,610 | 15,157 | 71,377 | 81% |
| `ground_terrain`, `ground_rock`, `ground_cover`, `ground_rubble`, `dress_*`, `aa_roller`, `aa_conduit` | 47,112 | 10,726 | 68,099 | 86% |
| + `ground_cliff_corner`, `ground_cliff_upper`, `ground_cliff_fin` | 27,020 | 5,356 | 57,058 | 90% |

Set expectations accordingly: **detail brushes alone do not get level1 under
the exact-flood cutoff.** What they do buy is a third of the tree, faster
compiles, and ~20% fewer drawn surface fragments (fewer BSP splits chopping
the terrain). Getting exact visibility needs a follow-up — see "After this".
Also note the ground already streams ±2 zones (the canyon is nearly straight;
±1 leaked sightlines), so even exact PVS will cull modestly on the ground.

The experiment's first compile **leaked everywhere**: level1's terrain dips
below the seal box's floor (seal bottom ≈ y −14.2, leak found near y −15.3),
so with the terrain no longer structural the void poured in underneath.
Growing the seal 10 m down (`seal.position.y` 51.1391 → 41.1391,
`seal.half.y` 65.3609 → 75.3609) fixed it. A detail brush cannot seal
anything; authors and generators must know that.

### Design

**Name.** `Detail` is taken — `Brush.Detail` is the mesh, and the YAML key
`detail:` on a brush is the mesh (`world/level_schema.go` `RawBrush.Detail
*RawBrushMesh`; there is also an unrelated `Detail *float32` at line ~130).
Use something unambiguous, e.g. `Brush.VisDetail bool`, YAML `vis_detail:
true`, UI label "Detail (not structural)". Whatever you pick, grep for
`Detail` before settling on it.

**Compiler** (`compile/csg.go` `gatherSolids` and `clipPlanesFor`):

1. A `VisDetail` brush emits its drawn faces with `Face.Detail = true` and
   emits **no** `NoDraw` hull faces (unlike a detail mesh — there is no
   structure to keep).
2. Its solid(s) still go in the CSG solid list, flagged detail, and the clip
   rule becomes:
   - structural solid clips structural and detail faces (as today);
   - detail solid clips **detail faces only** — never a structural face.
     Otherwise a rock half-buried in a cliff deletes the cliff face behind it,
     and the structural shell (which the tree, the seal and the void fill are
     built from) gets a hole. This is the rule Quake's qbsp uses.
   - detail vs detail keeps the existing `sourceOrder` coplanar tie-break.
   `clipPlanesFor(f *Face, s *solid)` is where the rule goes; `solid` needs a
   `detail bool`.
3. Light bake: **baked shadows come from `b.tree.LineOfSight`** (the BSP's
   solidity), in `baker.sample` (`compile/light.go` ~line 180). A VisDetail
   brush is not in the tree, so without further work it **casts no baked
   shadow** — terrain would stop shadowing itself and props would float. Add a
   second occluder: a triangle BVH over the VisDetail faces (world space,
   built once per bake, before `SubdivideFaces`), and treat a shadow ray as
   blocked if either the tree or the BVH blocks it. `internal/collision/
   raycast_accel.go` has a BVH and slab-test code (`buildBVH`, `rayDir`) you
   can copy or lift into a shared package (check the import graph — compile
   must not pull in the runtime). Self-shadowing: set `cf.hull` on VisDetail
   faces to the brush's own hull planes so `clearOfHull` starts the ray
   outside its own solid, exactly as detail-mesh faces do.
4. Leak diagnostics: a leak whose only blocker was a VisDetail brush should
   say so ("…sealed only by detail brushes; detail does not seal"), or authors
   will be baffled. `compile/compile.go` `findLeaks` / `Leak.Describe`, and
   `compile/outside.go`.
5. `compile.Stats` / `Summary()`: report how many faces were detail, so the
   effect of flagging shows up in the compile line.

**Runtime**: nothing should change. Collision reads brushes directly; raycasts
use `StaticGeometry` surfaces; rendering draws leaf faces, and detail faces are
in leaves. Verify rather than assume — a compiled level with VisDetail brushes
must still collide with them (`collision` uses the chunk's brushes, which
compiled chunks keep).

**Data + editor**:
- `world.Brush.VisDetail`, a `RawBrush` field, `BrushFromRaw` / `BrushToRaw`
  and `CloneBrush` in `world/brush.go`.
- Checkbox in `internal/editor/editor_brush_panel.go`; structural edits must
  call `EditorPushUndo()` first (editor undo is RawLevel snapshots) and must
  drop the compiled preview (`dropCompiledPreview`, `editor_compile.go`) like
  every other structural edit does.
- Consider a view toggle that tints detail brushes, the way Hammer/TrenchBroom
  do; authors need to see what is structural.
- Document it in `docs/guide/` next to the detail-mesh docs, including "detail
  does not seal".

**Content** (`/mnt/c/Users/jcart/OneDrive/Documents/Foundry/tools/level1_ground_gen.py`,
OneDrive project, not this repo): mark `ground_terrain`, `ground_rock`,
`ground_cover`, `ground_rubble` and the cliff trim (`ground_cliff_corner`,
`ground_cliff_upper`, `ground_cliff_fin`) as `vis_detail`, keep the main
`ground_cliff` slabs structural (they are the occluders), and grow the seal
(the generator computes `grown_seal`) below the lowest terrain point with
margin. Hangar dressing (`dress_*`) lives in the hangar chunk, which is
hand-authored; flag those by hand or via `tools/level1_hangar_gen.py`.

### Tests to write (follow `compile/detail_test.go`'s style)

- A VisDetail brush adds no splitting plane (use a diagonal face, as the
  existing test does, so an axis-aligned coincidence can't pass it).
- A VisDetail brush does not clip a structural face it overlaps; a structural
  brush does clip a VisDetail face buried in it.
- A VisDetail brush casts a baked shadow (a light, a detail block, a floor —
  the floor texel behind the block is darker than one beside it).
- A level sealed only by a VisDetail brush reports a leak, and the message
  says why.
- Save/load round-trip of the flag; clone keeps it; editor toggle is undoable.
- Mutation-check each test (break the code it guards and see it fail).

### After this: getting exact visibility

Detail brushes get level1 to ~27k portals; the exact flood needs < 16,384.
Options, not decided:
- **Areas / clusters**: flood between groups of cells (a room, a canyon
  stretch) rather than individual cells — Quake 2/3's clusters. Biggest
  change, best scaling.
- **Raise `coarseVisPortals`**: `buildMightSee` is O(portals²) in bits
  (27k² bits ≈ 91 MB) and the flood's time is unmeasured at that size. Try it
  on the scratch copy with `-quiet` and time it before committing to it.
- More structural simplification (merge cliff slabs) in the generator.

---

## 2. Baked hangar lights that keep moving shadows

### Why the hangar costs what it does

- The hangar chunk is `moves: true` in `level1.yaml` (line ~23778). `compile.Bakes`
  (`compile/csg.go`) refuses any chunk that moves, so the hangar is never
  compiled or baked — in the editor preview **or** the release build. The flag
  is stale: it dates from when `ship.flang` flew the hangar upward (still
  visible in `scripts/ship.flang.bak`); the current `scripts/level1/ship.flang`
  only sets `@hangar.prop.enabled = false`, and a compiled chunk can be
  switched off (it is hidden via `StaticGeometry.SetChunkHidden`).
  `level1_ground_gen.py` text-splices the hangar through untouched, so a hand
  edit to `moves:` survives regeneration.
- Its six lights (`point_light_0..2`, shadowed; `hangar_work_light`,
  `hangar_bench_light`, `hangar_cargo_light`) are `no_bake: true`.
  `PointLight.LitLive()` is true for `no_bake`, and `fragment.glsl` sums every
  `LitLive` light per fragment **even on baked surfaces** (`pointLightDynamic[i]`
  in the `bakedLight != 0` branch). So flipping `moves` alone gains little:
  measured ~0.7 ms GPU in the ship (5.6 → 4.9 ms for the world passes), because
  the per-pixel lighting is the cost — three of those lights take 20 cubemap
  taps each (`calcPointShadow`).
- They are `no_bake` so a script can switch them off: `lightRefusesWrite`
  (`internal/script/script_props_geom.go`) allows `enabled` writes on a static
  light only if it is `no_bake`. `ship.flang`'s `:skybox:` handler switches
  them off when the hangar goes.

### Plain baking (content only) — and why it isn't enough

Set `moves: false`, remove `no_bake` from the six lights, and delete the
`@point_light_*.prop.enabled = false` / `@hangar_*_light.prop.enabled = false`
lines in `ship.flang` (the linter, `cmd/levelcheck`, will refuse them
otherwise). That is safe: the baked light lives only on hangar surfaces, which
vanish when the hangar is disabled, and the lights' reach stops well above the
ground (`point_light_2` at y 111.6, radius 25.6, reaches y ≈ 86; the ground is
below y ≈ 12). Verify with a compile that no ground surface picks up hangar
light.

What it loses: **characters stop casting shadows on the hangar floor** from
those lights. A baked surface only sums `LitLive` lights, and a baked light's
cube shadow is only ever sampled for live-lit fragments. Entities themselves
stay lit and self-shadowed (entities are always lit live from every light).
The squad and the four mechanics stand under the red lights; losing their
floor shadows is visible. Option 2 is how to bake and keep them.

### Option 2 design: subtract moving-caster shadows from the bake

For a **baked, shadow-casting** point light, on a **baked** fragment within its
radius:

```
L          = the light's live contribution, unshadowed (what pointLightContrib
             computes before its shadow term) — the bake already contains L
             wherever the static world didn't block it
combined   = shadow from the live cube (static + moving casters)   ∈ [0,1]
staticOnly = shadow from the static-layer cube                      ∈ [0,1]
moving     = max(combined - staticOnly, 0)    // shadowed ONLY because of a mover
lighting  -= L * moving * shadowStrength
```

The `combined − staticOnly` difference is the important part: subtracting
`L × combined` would double-darken everything the bake already shadowed.

Both cubes already exist per light: `PointShadowMap.Cubemap` (static + moving)
and `PointShadowMap.StaticCubemap` (static only), maintained by
`planPointShadow` / `RenderPointShadowPass` in `internal/render/shadow.go`.
Today the static cube is only created when a light has movers in reach;
that is exactly when this term is non-zero, so the shader needs a per-light
flag ("static cube valid") and can skip the term otherwise.

**The hard constraint is texture units.** GL 4.1 guarantees 16 fragment units
and the world shader uses 14: 0 albedo, 1 sun cascades (`CascadeTexUnit`),
2–9 the eight `pointShadowMap[]` cubes, 10 `BlendTexUnit`, 11
`BlendMaskUnit`, 12 `NormalMapUnit`, 13 `CloudShadowTexUnit`. Only 14 and 15
are free, so "a second samplerCube per light" does not fit. Options:
- **Cube map array** (`samplerCubeArray`, core since GL 4.0): allocate all
  point shadows as layers of one `GL_TEXTURE_CUBE_MAP_ARRAY` — live cube for
  light i at layer i, static cube at layer `MaxPointLights + i` — and render
  faces with `glFramebufferTextureLayer(…, layer*6 + face)`. One unit for all
  of them; frees seven units for everything else. The biggest change, and the
  right one. Touches `createDepthCube`, `EnsurePointShadowMaps`, the blit
  (`BlitFramebuffer` works per layer-face attachment), `UploadPointShadowUniformsTo`,
  the dummy cubemap, `fragment.glsl` and `biome_light.glsl` (it samples the
  same cubes — search for `pointShadowMap` in `resources/shaders/`).
- **Budget it**: units 14–15 give static cubes to at most two baked lights at a
  time (nearest the camera, say). Smaller change, a visible cap.

Also:
- `biome_light.glsl` duplicates the point-shadow sampling for scatter/ground
  patches; whatever the world shader does, it must do (see its header).
- The layered-shadow GL test (`internal/render/point_shadow_cache_gl_test.go`)
  proves the two cubes are right; add one that draws a baked quad under a
  baked shadowed light with an entity between them and reads back a darker
  pixel behind the entity and an unchanged one beside it (see
  `light_dynamic_gl_test.go` for the baked-quad probe).
- Then do the content changes from "Plain baking" above.

---

## 3. Cheaper live point shadows (hardware PCF)

Independent of 1 and 2, and it helps every level with shadowed point lights.

### Today

`calcPointShadow` in `fragment.glsl` (and its twin in `biome_light.glsl`)
takes **20** `texture(samplerCube)` taps in a disk (`sampleOffsets[20]`),
compares each by hand, and averages. The cube faces store linear distance /
far plane (written as `gl_FragDepth` by `shadow_cube_frag.glsl`), in
`DEPTH_COMPONENT24` cubemaps with LINEAR filtering (`createDepthCube` in
`internal/render/shadow.go`). Every lit fragment within a shadowed light's
radius pays 20 fetches per light; the hangar has three such lights.

### Change

- Set `GL_TEXTURE_COMPARE_MODE = GL_COMPARE_REF_TO_TEXTURE` and
  `GL_TEXTURE_COMPARE_FUNC = GL_LEQUAL` on every point-shadow cube (live,
  static layer, and the dummy cubemap — a `samplerCubeShadow` bound to a
  texture without compare mode is undefined).
- Declare `samplerCubeShadow pointShadowMap[]` and sample with
  `texture(pointShadowMap[i], vec4(dir, currentDist/farPlane - bias/farPlane))`.
  With LINEAR filtering each tap returns a bilinear 2×2 comparison, so 4–8
  taps give smoother edges than today's 20 point taps.
- `GL_TEXTURE_CUBE_MAP_SEAMLESS` is already enabled (context state, set in
  `internal/render/sky.go`'s init), so filtering across face edges is fine —
  but it is only set once the sky pass initialises; if a test harness draws
  point shadows without it, expect seams in the test, not the game.
- Both `fragment.glsl` and `biome_light.glsl` must change together (same
  textures).
- Keep the stored value as it is (linear distance / far) so nothing else
  changes; the reference just has to be in the same units.

Optional, measure first: `shadow_cube_frag.glsl` writing `gl_FragDepth`
disables early-z in the cube pass. With the point-shadow cache the cube pass
only redraws moving casters most frames, so this matters less than it did.

### Verify

- It changes how soft shadow edges look: screenshot before/after at the bench
  poses and show the user, don't just report timings.
- GL test: a shadowed point light, an occluder, a lit quad; read back a pixel
  well inside the shadow (dark), well outside (lit), and check the penumbra is
  monotonic across the edge.
- Bench the ship and hangar poses with the GPU idle.

---

## Where things are

| Thing | Where |
|---|---|
| Compile entry, stats line | `internal/compile/compile.go` (`Compile`, `Stats.Summary`) |
| CSG, face emission, clip rule | `internal/compile/csg.go` (`gatherSolids`, `clipPlanesFor`, `Bakes`, `BakesBrush`) |
| BSP build, detail insertion | `internal/compile/bsp.go` (`BuildBSP`, `attachDetailFaces`, `insertDetail`, `attachInsetDetail`) |
| Visibility | `internal/compile/pvs.go` (`BuildPVS`, `coarseVisPortals`) |
| Leaks, void fill | `internal/compile/outside.go`, `findLeaks` in compile.go |
| Seal box | `internal/compile/seal.go` (`FitSeal`, `sealSolids`) |
| Light bake | `internal/compile/light.go` (`BakeLight`, `baker.sample`, `clearOfHull`) |
| Detail mesh semantics | `internal/world/brush_detail.go` |
| Brush data, clone, raw | `internal/world/brush.go`, `internal/world/level_schema.go` |
| Brush panel | `internal/editor/editor_brush_panel.go` |
| Point shadow render + cache | `internal/render/shadow.go`, `internal/render/point_shadow_cache.go` |
| Shadow sampling | `resources/shaders/fragment.glsl` (`calcPointShadow`, `pointLightContrib`, `bakedLight` branch), `resources/shaders/biome_light.glsl` |
| Static-light script rule | `internal/script/script_props_geom.go` (`lightRefusesWrite`) |
| level1 hangar script | OneDrive `resources/scripts/level1/ship.flang` (`:skybox:` handler) |
| level1 ground generator | OneDrive `tools/level1_ground_gen.py` |
| Bench + profiler | `internal/app/bench.go`, `internal/render/profile.go` |
