# Foundry Nanite implementation spec

Source of truth: the design doc "Nanite for Foundry: Virtualized Geometry Proposal"
(https://claude.ai/code/artifact/ac8c9f5b-b160-41dd-b3f6-eb9543512d61). This file is
the condensed spec for implementing agents. Repo: /home/jfcarter2358/personal/code/main/foundry
(Go, OpenGL 4.3 core via go-gl v4.3-core, SDL2, shaders in resources/shaders, `#version 430`).

Decisions already made: build every phase (the doc's measurement gates are waived), and
phase 4 switches anti-aliasing to TAA (MSAA kept as a fallback setting until parity).

## Ground rules for every phase

- Phases run IN ORDER in the same working tree, one commit per phase. Earlier phases' work
  is already committed. Read what earlier phases added before starting (git log, and their
  notes in docs/nanite/PHASE_<n>.md).
- Implementing and verifying agents do NOT commit, push, or switch branches. A separate step
  commits each phase once it has passed verification.
- Every new render path sits behind a flag (env var FOUNDRY_NANITE_<THING>=0/1 and/or a
  gfx setting) with the old path intact as fallback, until captures match. Default ON only
  when tests and headless captures show parity.
- All GL calls stay on the main thread. GL 4.3 core is the baseline; anything beyond
  (64-bit atomics, ARB_buffer_storage, bindless) must be feature-detected with a fallback
  (internal/glext is the place for detection).
- Respect the package layering test (internal/layering_test.go,
  TestPackagesDoNotDependUpward): low-level packages such as internal/pack must not import
  internal/gfx, directly or through another package.
- Must pass at the end of each phase: `go build ./...`, `go vet ./...`, `go test ./...`.
  GL tests: `FOUNDRY_GL_TEST=1 go test ./internal/render/... ./internal/gfx/...` (shared
  GL 4.3 context, sequential; dev box is WSLg via Mesa d3d12 — if no GL context is
  available, say so in the report rather than claiming they passed).
  Known failure that predates this work: internal/harness
  TestAPlaySessionDrawsTheRoomItIsStandingIn (fails at clean HEAD c4009b6 too).
- GL tests that draw through the world program must bind the shadow samplers first
  (see existing *_gl_test.go files).
- Use the headless tools rather than scratch harnesses: `foundry check`, `foundry sim`,
  `foundry render model|screen|viewmodel`, `foundry profile-load`, `foundry nanite`, the
  FOUNDRY_BENCH env (internal/app/bench.go; phase 0 ran it natively on the user's GTX 1070 via
  a Windows exe in %LOCALAPPDATA%\Temp\foundry-bench — see docs/nanite/PHASE_0.md),
  cmd/foundrycompile, cmd/levelcheck. Compare captures old vs new path for visual parity
  where a phase changes rendering.
- Add F3 profiler sections (render/profile.go ProfileBegin/End) for every new pass.
- Match surrounding code style: long explanatory file-header comments, comment density
  like neighbouring files.
- Write docs/nanite/PHASE_<n>.md: what was built, flags, files, tests, measured numbers,
  known gaps. Update docs/guide where user-facing behaviour changes.
- Do not leave incidental noise in the tree (e.g. internal/editor/imgui.ini window moves
  from test runs, scratch bench output outside docs/nanite).
- Be honest: if something is stubbed or untested, report it as such.

## Engine facts (state before phase 0)

- Frame: render/world_frame.go DrawWorld(). Single-pass forward into the multisampled window
  framebuffer (render/antialias.go, 8/4/2x MSAA tiers + alpha-to-coverage). No G-buffer, no
  scene FBO. Order: chunk batch/terrain sync → shadows (3x2048 CSM in a depth array, cloud
  shadow, point cubemaps 1024 with static cache, live cascade array) → opaque (compiled static
  surfaces PVS→frustum→cluster cull, chunk brushes, terrain, entities, scenery, foliage,
  scatter, cutout sprites) → decals → sky → sorted transparents → volumetrics (ray-march,
  blits depth) → sprites → first-person viewmodel (clears depth).
- gfx.Geometry (internal/gfx/geometry.go): originally NON-indexed DrawArrays; pos+uv
  interleaved (5 floats), normals separate, baked light record gfx.LightStride = 7 floats
  (direct RGB + sun share + indirect RGB; AttribLight=5, AttribLightIndirect=6), skin
  idx/wt. Phase 0 added an indexed path (gfx/indexed_mesh.go, FOUNDRY_NANITE_INDEXED).
- Brushes: CPUOnly, batched per chunk+material (render/chunk_batch.go, gfx/batch.go),
  glMultiDrawArrays. Compiled levels: internal/compile (CSG → BSP → portals/PVS → per-vertex
  light bake incl. Face.Indirect) → .fbsp gob; runtime internal/bsp + world.StaticGeometry,
  drawn by render/static_batch.go.
- Foliage (render/foliage*.go): procedural meshes (foliage_mesh.go), near/far/clump-card or
  octahedral impostor tiers, rank thinning, dithered crossfade, one instance pool, GPU
  compute cull foliage_cull_comp.glsl with Hi-Z and glMultiDrawElementsIndirect
  (render/foliage_gpu.go). Wind = per-vertex sway weight.
- Live GI (render/gi*.go, shaders gi_*.glsl): SDF clipmap (4 levels of 128^3, 8^3 bricks)
  from sources in gi_sources.go + DDGI-style probe cascades; every lit pass calls
  ambientLight() in light_model.glsl; GI atlases on texture units 20–22, SDF 24–31.
- Models: .fmdl YAML (internal/model), ModelDoc per-face slices, OBJ import (model_obj.go).
  Scenery (world/scenery.go): model path, pos/rot/scale, Solid.
- Collision (internal/collision): brushes = GJK/EPA vs convex pieces (Chunk.ConvexBrushPieces,
  support-function Shape interface in shape.go); terrain = ray probes from inside the body
  (terrain.go pushOutOfTerrain/terrainProbe); scenery = one OBB proxy, linear scan per body
  (scenery_collision.go); raycasts via BVHs in raycast_accel.go (sceneryHit = sphere+box);
  SweepSteps/SweepHit in collide_sweep.go; nav heightfield (internal/nav/heightfield.go)
  rasterises brushes only.

## The 10 Nanite components

1 Clustering (offline, ~128-tri clusters w/ bounds + normal cone) · 2 Cluster DAG
(group → simplify to half → re-split, locked group borders, monotonic error) ·
3 Compression & paging (quantised, fixed-size pages, root pages resident) ·
4 Fallback mesh (coarse proxy for collision/raycasts/bake) · 5 GPU LOD selection
(BVH over cluster groups, pick cut where own error < ~1px and parent's > ~1px) ·
6 Two-pass occlusion culling (draw last frame's visible, build HZB, test rest) ·
7 Hybrid rasteriser (compute raster small tris, 64-bit atomic max depth|id) ·
8 Visibility buffer + deferred materials · 9 Streaming (GPU feedback of missing pages,
async CPU load, coarse parent until arrival) · 10 Virtual shadow maps.

## Phases

### Phase 0 — Baseline and indexed meshes (DONE, verified)
- Baseline bench recorded (docs/nanite/PHASE_0.md); models load and draw indexed.

### Phase 1 — Clusters and GPU occlusion culling (components 1, 6, part of 5)
- Package internal/cluster: meshlet building (~128 tris), per-cluster bounds + normal cone.
- Run from internal/compile for .fbsp static surfaces (versioned addition to .fbsp) and at
  model load/cook.
- Nanite mesh asset v0: glTF 2.0 importer beside OBJ (normals, material slots), cook to a
  binary cooked file (.fnm) keyed on source hash, via editor action and `foundry nanite`.
  Scenery referencing a cooked mesh uses the Nanite path: shared cluster pool, per-placement
  instance records in an SSBO, material overrides.
- GPU-driven pipeline: compute instance cull → cluster cull (frustum, backface cone,
  two-pass Hi-Z generalised from foliage's HZB) → indirect draws per material slot. PVS stays
  as a first filter for compiled surfaces. Depth prepass for the culled draws (forward
  overdraw), until phase 4.
- Shadow passes cull per cluster too.
- Nanite meshes are GI sources: mesh SDF baked at cook time (internal/meshsdf), fed into
  gi_sources.go.
- Done when: level1 opaque GPU time drops; shadow passes cull per cluster; captures match.

### Phase 2 — Cluster DAG and continuous LOD (components 2, 4, 5)
- DAG build in internal/cluster: group adjacent clusters (graph partition), merge, simplify
  to ~half with locked group borders, re-split, repeat to root; store per-node error and
  parent error, bounds. Simplification weights the 7-float baked light + normals + UVs.
- Fallback mesh = a fixed mid-level cut, used by raycast BVHs and baking.
- GPU LOD selection in compute: hierarchical traversal of the cluster-group BVH, choose cut by
  projected error ~1px (setting), crack-free.
- Debug views: cluster colours, LOD level, overdraw (see render.SetView debug views).
- Done when: no cracks or popping in captures; a dense test mesh costs by screen size.

### Phase 2b — Collision for Nanite meshes (CPU-only Go)
- Modes per asset (placement override): box (today), hulls, mesh, none. Default hulls under a
  size threshold, mesh above.
- Cook: fixed collision cut at a project world-space error (few cm), clean (weld, drop
  degenerates, record open edges); mesh mode → SAH BVH in mesh-local space with quantised
  verts + per-tri surface tags; hulls mode → convex decomposition (CoACD or V-HACD via cgo,
  or a Go implementation), capped hulls/verts, stored like ConvexBrushPieces; cached on
  source hash + settings.
- Runtime: per-chunk placement BVH broadphase replacing ResolveSceneryCollisions' linear
  scan; Hull Shape (support = farthest transformed vertex) into GJK/EPA/resolveEntityOverlap/
  SweepHit as a compound proxy; extract terrain.go's probes into a surface interface
  (down/up/wall probes → distance+normal) implemented by terrain and the mesh BVH (rays in
  mesh-local space); raycast_accel integration replacing sceneryHit; ParticleCollide;
  collision resident by proximity to active bodies; attached chunks via matrix.
- Nav heightfield rasterises mesh tris + hulls. Editor collision tab + debug draw; F3 rows;
  foundry check warnings (hull cap hits, open edges in hulls mode, solid with none).
- Tests mirroring terrain_test.go, step_up_test.go, entity_collision_slide_test.go,
  raycast_oracle_test.go (BVH vs brute force), GJK hull cases, a foundry sim run with AI
  across a Nanite cliff.

### Phase 3 — Compression, paging and streaming (components 3, 9)
- Quantised positions/attributes per cluster, bit-packed; cluster groups in fixed-size pages;
  root pages always resident. Versioned cooked format.
- GPU page pool; GPU writes feedback of requested-but-missing pages; CPU reads it back
  (async, fenced), loads pages on goroutines, uploads on main thread through a staging ring
  (ARB_buffer_storage persistent mapping if available, else orphaned buffers); coarser
  parent drawn until arrival. Collision pages resident by body proximity (phase 2b).
- Done when: a level over the VRAM budget plays without stalls (test with a budget cap).

### Phase 4 — Visibility buffer, deferred materials, and TAA (component 8)
- TAA: jittered projection, motion vectors, history reprojection + neighbourhood clamping,
  sharpen. MSAA path stays as a fallback setting until TAA reaches parity; alpha-to-coverage
  cutouts need a TAA-friendly replacement (dithered/stochastic alpha resolved by TAA).
- Scene FBO: depth + 32-bit visibility ID (cluster/triangle) for Nanite geometry; full-screen
  (or tiled, binned by material) material pass that reconstructs attributes from the triangle
  and shades with the existing light model, shadows, cloud shadow and live GI
  (ambientLight with GI atlases bound). Non-Nanite passes (terrain, skinned, foliage,
  decals, transparents, volumetrics, viewmodel) composite correctly with the scene depth.
  Replaces phase 1's depth prepass on this path.
- Done when: captures match the forward path; shading cost stays flat with overdraw.

### Phase 5 — Software raster and virtual shadow maps (components 7, 10)
- Compute rasteriser for small triangles writing 64-bit atomic max (depth<<32 | id) into a
  visibility SSBO/image when 64-bit atomics are available (feature-detect via internal/glext);
  hardware raster for large tris; full hardware fallback otherwise. Must be optional.
- Virtual shadow maps: paged sun shadow atlas (large virtual resolution), pages marked by
  receivers each frame, drawn through the cluster pipeline, static pages cached; replaces the
  3 cascades + live cascade array behind a flag. Point cubemaps stay.
- Done when: dense scenes beat hardware-only raster; shadows stay sharp at all ranges.

### Phase 6 — Nanite foliage (UE 5.7 style)
- Nanite Voxels: build-time per-cluster voxel bricks (≤128 bricks of 4x4x4) as a DAG node kind
  chosen when voxel error < triangle error; voxels store coverage, albedo/tone/accent/part,
  a normal distribution (stochastic per-pixel normal); rasterised in their own bin (software
  raster when available, else ray-marched boxes).
- Nanite Assemblies: foliage generator (render/foliage_mesh.go) emits part templates +
  transforms instead of merged meshes; cluster cull applies part transforms.
- Nanite Skinning: procedural bones from the branch hierarchy; compute pass poses bones into
  an SSBO each frame from wind; replaces per-vertex sway on this path.
- Optional: voxel coverage feeds the GI SDF so canopies shade the ground; trunk capsules for
  collision from assemblies.
- Existing foliage tiers remain the fallback behind a flag.
- Done when: forests draw to the horizon with no crossfade bands or impostor pops.
