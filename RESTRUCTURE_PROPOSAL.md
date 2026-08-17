# Foundry — Structure Analysis & Restructuring Proposal

*Written by an automated pass over the repo on 2026-08-17. All file:line references were pulled from the code at that point in time — re-check line numbers if the referenced file has since changed.*

## 0. Executive summary

Foundry (SDL2 + OpenGL + Dear ImGui, Go 1.23) is 71 `.go` files, all sitting in a single `package main` at the repo root. The domain logic itself is in decent shape — there's essentially zero `TODO`/`FIXME` debt and the comments are unusually thorough — but the flat-file/global-state layout has let real duplication accumulate, produced several god-files/god-functions, and the repo was tracking generated build output as if it were source.

Highest-value, lowest-risk items, roughly in the order you'd want to tackle them:

1. **Repo hygiene** — `dist/` was fully committed (including the compiled binary and a runtime log) despite being pure build output; this is very likely why `dist/shaders/*.glsl` kept showing as modified. *(Done as part of this pass — see §7.)*
2. **~15 concrete duplication sites** with drop-in shared-helper fixes (§3) — none of these require a package split, they're pure in-`package main` cleanups.
3. **A handful of god-functions** (§2) that are all the same shape (long `switch`/`if` dispatch over an editor mode) and split the same way.
4. **A phased package-split roadmap** (§5) for when you want real module boundaries — sequenced by risk, not a one-shot rewrite.

## 1. Current state

- **71 files, all `package main`**, no internal package boundaries. Module is `foundry` (`go.mod`, Go 1.23.1). Built via `CGO_ENABLED=1 go build -o dist/foundry *.go` — note the explicit file glob rather than `go build .`, which already assumes the flat single-package layout.
- **No central `App`/`Engine` struct.** Runtime state lives in ~15 clusters of package-level `var`s:
  - window/GL lifecycle, shader uniform locations, `Running`, `Program` (`main.go`)
  - player/camera (`PlayerEntity`, `Cam`, `WorldUp` — `main.go`, `camera.go`)
  - input/gravity flags (`input.go`)
  - `Editor EditorState` — one big struct (~140 fields), good instinct, but every editor file reads/writes it directly with no accessor boundary
  - rendering subsystem singletons: `Shadows ShadowState`, `detail detailRenderer`, `biomes biomeRenderer`, `groundPatches groundRenderer`
  - texture editor/palette globals (`globalPalette`, `textureHasAlpha`, `pendingTextureUploads`, plus ~15 UI-state vars in `texture_editor_ui.go`)

  The clustering is already reasonable by subsystem — this isn't chaotic global soup — but there's no ownership boundary between clusters, and cross-file access is unrestricted (e.g. `debug.go` reads `PlayerEntity`/`Cam` directly from `main.go`/`camera.go`; `editor.go` reads/writes `GravityEnabled`/`IsOnGround`/`JumpRequested` from `input.go`).
- **CGO is not the obstacle it looks like.** Only **29 of 71 files** import `go-gl/gl`, `go-sdl2`, or `imgui-go`. The other **42** — `collision.go`, `level.go`/`chunk.go`/`sector.go`, `triangulate.go`, `extrusion.go`, `texture_doc.go`/`texture_palette.go`/`texture_file.go`, `detail_presets.go`, most of the biome data logic — are pure-Go domain code that happens to live in the cgo-tainted package only because of the flat layout, not because it needs to. CGO operates at file granularity, not package-tree granularity, so a multi-package split is structurally supported by the toolchain. The one real constraint is `runtime.LockOSThread()` (`main.go` `init()`) plus the fact that GL/SDL calls must stay pinned to the thread that created the context — that constraint travels with the *files that call SDL/GL*, not with whatever package happens to contain them today.
- **Docs**: `DESIGN.md` and `NEXT.md` are living and accurate. `plan.md` (32KB) targets an old `gl/v3.3-core` + CSV-format `level.go` that no longer exists — stale, candidate to archive. `TEXTURE_EDITOR_PLAN.md` (20KB) is a pre-implementation design doc for a feature that's now fully built (`texture_editor.go`, `texture_editor_ui.go`, `texture_palette.go`, `texture_file.go`, `texture_alpha.go` all present) — historical, candidate to archive. `WORLDGEN_HANDOFF.md` described a `WorldGrid` procedural-city feature with zero matches anywhere in `.go` source — confirmed stale, **archived to `docs/archive/` as part of this pass**.

## 2. Simplifications

- **Six one-off numeric helpers** scattered across the codebase instead of centralized: `max0` (`sector.go`), `min2`/`max2` (`biome_ground.go`), `min32`/`clamp01` (`detail_render.go`), `abs32` (`shadow.go`), `floor32` (`spatial.go`). Since the module targets Go 1.23, `max0`/`min2`/`max2`/`min32` are now redundant with the language's builtin generic `min`/`max` and can just be deleted; `abs32`/`floor32`/`clamp01` belong together in one `mathutil.go`.
- **`detail_render.go`'s three render passes** — `RenderDetailColor` (~654-727), `RenderDetailDirShadow` (~733-773), `RenderDetailCubeShadow` (~779-823) — all repeat the same shape: bind program → set matrix uniforms → `forEachDetailFace` filtered by displace/scatter → draw, differing only in which extra uniforms get set (color needs light/shadow upload + texture bind; shadow passes need `lightSpaceMatrix` (+ `lightPos`/`farPlane` for cube)). Collapse into one `renderDetailPass(level, cull, dispProg, scatProg, setExtra func(p *progUniforms))`.
- **`main.go` (704 lines)** mixes app bootstrap, the render loop, and inline player physics/collision orchestration with no `App`/`Game` struct. Even before any package split, wrapping this in a thin `Engine` struct would remove ~40 of the loose globals in one move.
- **`input.go`** conflates two responsibilities: window-sizing (`RefreshWindowSize`/`ToggleFullscreen`, ~29-52) and actual gameplay/global input handling (`HandleInput`, `HandleEvent`). Worth noting: `HandleEvent` is called unconditionally every frame regardless of editor state — it's not gameplay-only despite the filename; it owns all global hotkeys (fullscreen, debug overlay, editor toggle) and window resize/focus handling. Split window-sizing into `window.go` (where `UIWindow`/`RenderSecondaryWindows` already live) and keep `input.go` for actual input.
- **Twelve functions exceed 150 lines**, and all of them are the same shape — a long panel/dispatch `switch` over `Editor.Mode`/`Editor.ActiveSurface`:

  | Function | File | Lines |
  |---|---|---|
  | `Editor3DPanel` | `editor_3d.go` | 556 |
  | `loadRawChunk` | `level.go` | 571 (does ≥6 unrelated things: points, sectors, portal-gaps, primitives, biome-sectors, bounds) |
  | `EditorDrawUI` | `editor_ui.go` | 327 |
  | `mergeSectorsAtPortal` | `editor_2d.go` | 197 |
  | `Editor2DHandleClick` | `editor_2d.go` | 214 |
  | `EditorHandleEvent` | `editor.go` | 204 |
  | `chunkToRaw` | `editor_save.go` | 190 |
  | `SceneryEditorPanel` | `editor_scenery.go` | 187 |
  | `Editor3DHandleClick` | `editor_3d.go` | 186 |
  | `EditorLightPanel` | `editor_lights.go` | 184 |
  | `biomePanelSection` | `editor_biome.go` | 173 |
  | `Surface2DPanel` / `Editor2DPanel` | `editor_surface2d.go` / `editor_ui.go` | ~160 each |

  Recommended pattern: one function per mode/surface case (`Editor3DPanelForChunk`, `Editor3DPanelForEntity`, `Editor3DPanelForPrimitive`, `Editor3DPanelForSector`, etc.), with the top-level function reduced to a dispatch switch.
- **One genuinely dead function**: `indexSize` (`editor_imgui.go:411`) is unreachable — shadowed by a local `:=` of the same name inside `imguiRenderDrawData` (`editor_imgui.go:326`).

## 3. De-duplication

This is the highest-value, lowest-risk category — every item below is a same-package helper extraction, no architecture change required.

| # | Duplication | Locations | Fix |
|---|---|---|---|
| 1 | Byte-identical shoelace-formula polygon area, two names | `level.go:1190` `signedArea2D`, `extrusion.go:276` `signedArea2D_UV` | Delete one, repoint the ~4 callers (`editor_2d.go:1325`, `editor_biome.go:50`, `world_biome_test.go:55`, etc.) |
| 2 | Portal-gap-quad wall generation reimplemented twice | `chunk.go:358-406` `Chunk.RebuildGaps` vs. inlined in `level.go`'s `loadRawChunk` (~786-830) | Have `loadRawChunk` call `RebuildGaps()` instead of re-deriving the same ~40 lines |
| 3 | Identical sun-direction/color fallback block, copy-pasted verbatim (same magic literals) | `grass.go:514-523`, `biome_ground.go:376-385` | `defaultSunDirColor(lights LightSet) (dir, col mgl32.Vec3)` |
| 4 | Two grid-clip mesh builders that clip a polygon the same way (the water one's own doc comment admits it) | `biome_ground.go:218-286` `buildGroundPatchMesh`, `biome_water.go:113-169` `buildBiomeWaterMesh` | `buildClippedGridMesh(min, max, nx, nz, polygon, emit func(...))` |
| 5 | Four independent hand-rolled VAO/VBO upload sequences instead of extending the shared type | `wall.go:107-135` `Geometry.Load`, `grass.go:223-246` `uploadBiomeMesh`, `biome_ground.go` inline in `ensureGroundPatch` (~301-317), `detail_render.go:98-110` in `DetailInit` | Configurable-stride variant of `Geometry` |
| 6 | Four separate normal/cross-product implementations of "normalize(cross(edge1, edge2))" (two different vec3 types in play) | `biome_ground.go:250-262` `normalAt`, `grass.go:340-348`, `detail_render.go:298-322` `computeFaceBasis`, `smooth.go:130-138` | Consolidate onto one `mgl32.Vec3`-based helper (note: `smooth.go`'s local `vec3` type is the odd one out and blocks trivial merge) |
| 7 | Byte-identical helper functions, plus 20+ ad hoc copies of the same guard pattern | `editor_biome.go:24-30` `activeChunk()`, `editor_translate_gizmo.go:102-108` `gizmoChunk()`, and inline `&lvl.Chunks[Editor.ActiveChunkIdx]` across `editor.go`, `editor_2d.go`, `editor_3d.go`, `editor_scenery.go`, `editor_surface2d.go`, `editor_extrusion.go`, `editor_ui.go` | One `Editor.activeChunk() *Chunk` method |
| 8 | Four directory-scan-by-extension functions with the same skeleton; two are byte-identical | `editor_save.go:28-44` `ScanLevelFiles`, `editor_scenery.go:669-687` `ScanSceneryFiles` (identical to the former), `editor_3d.go:38-59` `ScanEntityDefs`, `texture_file.go:357-370` `ScanFtexFiles` | One `scanFilesByExt(dir, ext string) []string` |
| 9 | Undo/redo bounded-stack logic duplicated wholesale between the level editor and scenery editor | `editor_undo.go:32-82` (level) vs. `editor_undo.go:151-212` (scenery) | Generic `boundedStack[T any]` with `Push/Pop/Clear/Len` |
| 10 | Raw-primitive serialization sharing ~35 lines of kind→string switch + scale-defaulting — **and silently dropping fields** | `editor_save.go:405-457` `primitiveToRaw` vs. `editor_scenery.go:518-559` `sceneryPrimToRaw` | Share the common core; **`sceneryPrimToRaw` drops `Smooth`/`SmoothAngle`** — likely a real bug where scenery primitives lose their smoothing settings on save. Worth verifying against `rawToSceneryPrimitive` (`editor_scenery.go:584`) independent of the broader cleanup |
| 11 | Screen↔world ortho-transform quartet copy-pasted between the 2D map view and the surface UV view, differing only in which pan/zoom fields they close over | `editor_2d.go:214-251` (`editor2DOrtho`/`worldToScreen`/`screenToWorld`/`screenToWorldSize`), `editor_surface2d.go:42-74` (`surfaceOrtho`/...) | One `orthoView{pan, zoom mgl32.Vec2/float32}` type with methods |
| 12 | The "open-flag → `BeginPopupModalV` → body → Cancel → `EndPopup`" idiom, ~9 times | `editor_scenery.go` (`sceneryPickerPopup`, `scenerySaveAsPopup`, `importSceneryPopup`), `editor_ui.go` (level picker/save-as blocks), `texture_editor_ui.go` (`drawTexEditorNewDialog`, `drawTexEditorOpenDialog`, `drawTexEditorImportDialog`, `drawTexEditorSaveAsDialog`, `drawTexEditorMismatchDialog`) | One `withModalPopup(title string, show *bool, body func())` |
| 13 | Generic file-utility living inside a level-specific file, consumed from unrelated files | `writeFileAtomic` (`editor_save.go:76-114`), called from `texture_file.go:70,185,310` and `detail_presets.go:130` | Move to a neutral `fileutil.go` |
| 14 | Palette-management functions with no dependency on the interactive editing session they're filed under | `SetPaletteColor`, `RemapDocToGlobalPalette`, `AdoptDocPaletteGlobally`, `RebuildAllTextures` (`texture_editor.go:485-542`) | Move to `texture_palette.go`/`texture_file.go` |
| 15 | Struct-shape utilities with no session dependency, filed with session logic | `docByteSize`, `entrySize`, `cloneTexDoc` (`texture_editor.go:49-76`) | Move `cloneTexDoc` beside `TexDoc`'s definition in `texture_doc.go` |

## 4. Optimizations

- **`detailBasisCache`/`detailBoundaryCache`** (`detail_render.go:340,514`) are keyed by raw `*Geometry` pointer with no eviction hook found tied to geometry rebuild/deletion. Worth verifying whether a freed/reallocated `Geometry` can alias a stale cache entry — flagged as a possible stale-cache/memory-growth risk, not confirmed as an active bug.
- Consolidating the four independent VBO-upload paths (§3.5) and the three detail render passes (§2) reduces per-frame branching and duplicated GL state changes, not just source duplication.
- **`cmd/gentextures`** isn't wired into the Makefile, so generated textures in `resources/textures/` can silently drift from what the generator would currently produce. Add a `make textures` target (`go run ./cmd/gentextures`).
- The Makefile builds via an explicit `*.go` glob rather than `go build .` — this already assumes a flat single-package layout and would need to change before any package split, not something to fix standalone.

## 5. Structural improvements & target module layout

This is a **phased roadmap**, not a prescription to do it all at once — package-splitting a 71-file solo project mid-development is realistically several sessions of work, best sliced into incremental PRs.

**Phase A — prerequisites (in-package, do first)**
- Land the de-duplication in §3.
- Decompose the `EditorState` god-struct (~140 fields) into embedded per-subsystem structs: `GizmoState`, `SurfaceEditorState`, `SceneryEditorState`, `BiomeLayerState`, `UndoState`. Every editor file currently reads/writes `Editor.*` directly with no boundary — this decomposition is a **hard prerequisite** for splitting editor code into packages, not optional polish.
- Split `level.go` into `level_schema.go` (the ~20 `Raw*` types) and `level_loader.go` (with `loadRawChunk` broken into `loadChunkPoints`/`loadChunkSectors`/`loadChunkPrimitives`/`loadChunkBiomeSectors`, and its portal-gap block deleted in favor of #2 above).
- Move `Geometry` out of `wall.go` into its own `geometry.go`; move `Extrusion`/`TerrainPoint`/`ExtrusionPoint` type definitions beside their builders in `extrusion.go`.

**Phase B — lowest-risk extractions once Phase A lands**
- `internal/collision` — `collision.go` (682 lines) is already clean and self-contained; its only outside dependencies are the `Chunk`/`Sector`/`Entity`/`Primitive` types. Best first extraction.
- `internal/texdoc` — `texture_doc.go` + `texture_palette.go` have zero ImGui/SDL/GL imports today.
- `editor_imgui.go` as a standalone `imguibackend` package — grep confirms it never touches `Editor.*` state at all, making it the cleanest editor-cluster file to extract.

**Phase C — target package tree (longer-term)**
```
cmd/gentextures/        (already standalone; add more CLI tools here as internal/ packages emerge)
internal/geom/          Geometry, triangulate, smooth, extrusion types   (mesh core, GL-free)
internal/level/         chunk, sector, portal, spatial, level schema/loader
internal/collision/     collision.go as-is
internal/biome/         biome_sector, biome_ground, biome_water, grass
internal/detail/        detail.go, detail_presets.go, detail_render.go
internal/texture/       texture_doc, texture_palette, texture_file (pure-Go)
                         — keep texture.go/texture_alpha.go (GL-coupled) with render or main
internal/render/        camera, graphics, light, culling, entity, primitive, obj, shadow
editor/{core,view2d,view3d,surface,scenery,lights,save,imguibackend}/
                         (gated on the EditorState decomposition in Phase A)
main package             bootstrap, render-loop wiring, window/GL/uniform globals only
```

Suggested sub-groupings within the editor cluster specifically, once Phase A's `EditorState` decomposition lands:

```
editor/core/       editor.go (trimmed), editor_undo.go, editor_ui.go (shared widgets only)
editor/view2d/      editor_2d.go, editor_biome.go
editor/view3d/      editor_3d.go, editor_3d_pick.go, editor_gizmo.go, editor_translate_gizmo.go
editor/surface/     editor_surface2d.go, editor_extrusion.go
editor/scenery/     editor_scenery.go
editor/lights/      editor_lights.go
editor/save/        editor_save.go (minus writeFileAtomic → shared fileutil)
editor/imguibackend/ editor_imgui.go
```

And within the texture cluster:

```
texture/doc/       texture_doc.go
texture/palette/   texture_palette.go (+ palette funcs moved from texture_editor.go)
texture/file/      texture_file.go (binary format I/O — depends on doc + palette)
texture/runtime/   texture.go, texture_alpha.go (GPU upload; consumed by wall.go outside this cluster)
texture/editor/    texture_editor.go (trimmed of palette funcs)
texture/editorui/  texture_editor_ui.go
```

## 6. Appendix — file responsibility tables

### Editor cluster (15 files, ~10,700 lines + 1,248 test lines)

| File | Lines | Responsibility |
|---|---|---|
| `editor.go` | 822 | `EditorState` struct, init/toggle/tick/render lifecycle, top-level input dispatch |
| `editor_2d.go` | 1762 | 2D top-down map editor: rendering, click/drag handling, sector split/merge/create |
| `editor_3d.go` | 1331 | 3D mode: entity/ray picking dispatch, chunk create/duplicate/delete, `Editor3DPanel` |
| `editor_3d_pick.go` | 645 | Ray-geometry intersection math, vertex-handle picking/selection state |
| `editor_biome.go` | 518 | Biome-sector polygon drawing/editing (2D layer) + property panel |
| `editor_extrusion.go` | 516 | Legacy wall-extrusion placement, panel, gizmo rendering |
| `editor_gizmo.go` | 192 | Low-level immediate-mode line-gizmo renderer |
| `editor_imgui.go` | 411 | ImGui/SDL/OpenGL backend plumbing — zero `Editor.*` coupling |
| `editor_lights.go` | 290 | Light panel UI + 3D direction indicator overlay |
| `editor_save.go` | 811 | Level/chunk YAML serialization, atomic file writes, chunk import |
| `editor_scenery.go` | 845 | Standalone scenery-prefab editor |
| `editor_surface2d.go` | 736 | Per-surface UV-space 2D editor |
| `editor_translate_gizmo.go` | 515 | 3-axis translate-gizmo interaction |
| `editor_ui.go` | 1193 | Shared ImGui widget helpers + master `EditorDrawUI` dispatcher + 2D-mode panel |
| `editor_undo.go` | 212 | Undo/redo stacks for both the level editor and scenery editor |

Test coverage is thin and lopsided: only 5 of 15 files have a direct `_test.go` sibling. `Editor3DPanel` (556 lines), all of `editor_ui.go`'s widgets, biome, extrusion, scenery, lights, undo, and gizmo code have zero direct tests.

### Texture cluster (7 files, ~2,724 lines + 1,028 test lines)

| File | Lines | Responsibility |
|---|---|---|
| `texture.go` | 164 | GPU texture upload/registration, thread-safe upload queue |
| `texture_alpha.go` | 56 | Per-texture alpha-presence cache |
| `texture_doc.go` | 287 | `TexDoc`/`TexLayer`/`TexPalette` data model, layer ops, blend compositing |
| `texture_editor.go` | 542 | Editing session: brush/fill/line/rect tools, per-layer undo/redo (plus misplaced palette funcs, see §3.14) |
| `texture_editor_ui.go` | 842 | ImGui window: toolbar, dialogs, tool/palette/layers/canvas panels |
| `texture_file.go` | 370 | `.ftex`/`.pal`/`.png` binary I/O, path helpers |
| `texture_palette.go` | 193 | Global palette default data, nearest-color quantization |

Missing direct tests: `texture.go`, `texture_editor_ui.go` — the two most side-effect-heavy/GL-coupled files.

### Rendering/geometry cluster

| File | Responsibility |
|---|---|
| `chunk.go` | Chunk type (point pool + sectors), point add/remove, bounds, portal inference, gap-wall rebuild |
| `sector.go` | Floor/Ceiling/Sector types, per-vertex height math, AABB, `Sector.Load` |
| `primitive.go` | Primitive type (box/sphere/cyl/plane/water), water-param (de)serialization |
| `extrusion.go` | Wall/floor/ceiling mesh building with extrusion holes |
| `triangulate.go` | 2D polygon triangulation: ear-clipping, hole-bridging, Delaunay |
| `wall.go` | Core `Geometry` GPU-buffer type, `Wall`/`Extrusion`/`TerrainPoint` type defs, UV transform |
| `smooth.go` | Vertex-welding smooth-normal computation |
| `shadow.go` | Shadow map/cascade/cubemap setup, shadow-pass rendering |
| `culling.go` | Frustum extraction/AABB test, portal BFS visibility |
| `grass.go` | GPU-instanced biome scatter rendering, height-texture cache |
| `biome_sector.go` | BiomeBrush/BiomeSector data model + kind constants |
| `biome_ground.go` | Procedural ground-patch mesh (displaced terrain under biome paint) |
| `biome_water.go` | Auto-manages a companion water Primitive per Water-kind BiomeSector |
| `detail.go` | DetailSpec type, built-in preset table |
| `detail_presets.go` | User preset persistence |
| `detail_render.go` | Tessellation/scatter shader programs + face-detail rendering |
| `light.go` | Light types, direction↔yaw/pitch conversion |
| `camera.go` | Camera type, mouse-look, movement |
| `graphics.go` | Shader program compile/link |
| `obj.go` | `.obj` mesh loader |
| `portal.go` | `Portal` type |
| `spatial.go` | Spatial hash for chunk/sector lookup |
| `entity.go` | Entity type, model matrix, AABB |
| `level.go` | Raw YAML schema (~20 structs) + level/chunk loader — see §2's `loadRawChunk` breakdown |
| `collision.go` | Spatial queries, geometry primitives, surface-height interpolation, collision resolution (slope/wall/floor/entity/primitive) — already clean, best Phase B extraction candidate |

`level.go` (1198 lines) is a genuine god-file mixing four concerns: YAML schema definition, deserialization/conversion, top-level load orchestration, and pure geometry helpers (`buildGapQuad`, `edgeHeights`, `signedArea2D`) that belong in `chunk.go`/`sector.go` instead.

## 7. What's already been done as part of this pass

- `.gitignore` now includes `dist` — it was fully tracked despite being regenerated (`rm -rf dist && mkdir dist && cp -r resources/* dist && go build -o dist/foundry`) by every `make linux`/`make windows`/`make sdl` run, including the compiled binary and a runtime log file. Untracked via `git rm -r --cached dist` (files remain on disk).
- `WORLDGEN_HANDOFF.md` moved to `docs/archive/WORLDGEN_HANDOFF.md` — confirmed stale (describes a `WorldGrid` feature with zero references anywhere in `.go` source).
- `plan.md` and `TEXTURE_EDITOR_PLAN.md` were **not** moved — flagged above as archive candidates, left for you to action since they weren't explicitly confirmed stale the way `WORLDGEN_HANDOFF.md` was.

## 8. Suggested next step

Nothing above has touched application code yet — this document plus the two hygiene fixes in §7 are the full scope of this pass. When you're ready to act on §3 (de-duplication) or §5 (structural split), those are naturally separable follow-up tasks: §3 is safe to do file-by-file with no behavior change, §5 Phase A is a prerequisite for anything in Phase B/C.
