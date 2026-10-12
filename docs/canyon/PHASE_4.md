# Phase 4: the Poly Haven asset library — what was built

Phase 4 makes downloaded Poly Haven CC0 scans the canyon's default source
of rocks, cliffs, plants and ground textures. It adds:

- a Poly Haven API client;
- an importer, glTF + JPG to cooked Nanite meshes and engine PNGs in the
  placement frame;
- three generator stages, `polyhaven_rocks`, `polyhaven_plants` and
  `polyhaven_textures`;
- a Poly Haven browser window, and an id-list widget in the Generator;
- `foundry polyhaven`;
- BC7/BC5 texture compression with a disk cache, a skip-mips setting and
  texture VRAM in F3;
- classic-path geometry sharing for cooked placements.

Verifying it against the live library found two engine bugs that only real
scans show: the glTF importer flipped V, and the engine's normal-map green
convention is the opposite of `nor_gl`.

The user-facing chapters are
[docs/guide/34-polyhaven.md](../guide/34-polyhaven.md) and the three new
stage sections of [docs/guide/33-generators.md](../guide/33-generators.md).

## Packages and files

| Package | Files | What |
| --- | --- | --- |
| `internal/polyhaven` (new) | client.go, download.go, search.go, thumbs.go; client_test.go | The API client. Pure Go over net/http (layering-asserted: no gfx/model/nanitecook/world/procgen). |
| `internal/polyhaven/phimport` (new) | phimport.go, model.go, textures.go, frame.go, decimate.go, texset.go, provenance.go; phimport_test.go | The importer. |
| `internal/polyhaven/phtest` (new) | phtest.go | A fake Poly Haven on httptest, shared by the tests of phimport, the stages and the editor. |
| `internal/polyhaven/phcli` (new) | phcli.go | `foundry polyhaven search / info / import / cache`. |
| `internal/procgen/stages` | polyhaven.go, polyhaven_textures.go, polyhaven_test.go; place_common.go, scatter.go, rocks_run.go, scatter_test.go edited | The three stages; `ScatterSources`; `previewRock` takes a manifest without a mesh; `RocksArtifact.ModelPath` honours `VariantMeta.Model`. |
| `internal/procgen` | params.go | `ParamDef.AssetList` (additive). |
| `internal/procgen/rocks` | meta.go | `VariantMeta.Model` (optional, additive; manifest version unchanged). |
| `internal/model` | model_gltf.go, model_gltf_test.go | UVs taken as stored (see "The V flip"). |
| `internal/nanite` | mesh.go | `CookVersion` 5 → 6. |
| `internal/texcompress` (new, sub-agent) | texcompress.go, mip.go, bc7.go, bc5.go, encode.go, cache.go; tests | BC7 mode-6 and BC5 encoders, CPU mips, the disk cache. |
| `internal/gfx` (sub-agent) | texture_compress_settings.go, texture_compress.go, texture_vram.go (new); texture.go, texture_prefetch.go, texture_normal.go, texture_ui.go, texture_filter.go edited; tests | Compressed uploads, skip-mips, VRAM accounting. |
| `internal/world` (sub-agent) | scenery_share.go (new), scenery.go; scenery_share_test.go | Classic-path sharing. |
| `internal/render` | nanite_cutout_gl_test.go (new); debug.go (F3 line, sub-agent); scenery_share_gl_test.go, normal_map_bc5_gl_test.go (sub-agents) | |
| `resources/shaders` | fragment.glsl | `hasNormalMap == 2` reconstructs a BC5 normal's z (3 lines, sub-agent). |
| `internal/app` | bench.go, graphics_settings.go (+ test) | Bench prints texture VRAM; the two settings persist. |
| `internal/script` | script_props_level.go | `@level.prop.texture_compression`, `texture_skip_mips`. |
| `internal/editor` | editor_polyhaven_window.go, editor_polyhaven_jobs.go, editor_polyhaven_stage.go, editor_polyhaven_test.go (new); editor_generator_params.go, editor_generator_window.go edited | The browser and the id-list widget. |
| `internal/pack` | unused.go | Imports are never offered as unused. |
| docs | this file, guide 33 (three sections, scatter's `rocks` row), guide 34 (new), README row | |

### Shared files touched (additive only)

| File | Edit |
| --- | --- |
| internal/editor/editor_menubar.go | `editorKindPolyHaven` after `editorKindGenerator`, and a "Poly Haven" switcher entry at the end. The diff also shows the Generator const and entry, which are phase 0's and were never committed. |
| internal/app/devtools_dev.go | `phcli` import, one `case phcli.Command:` |
| docs/guide/README.md | one row after Generators |
| internal/editor/editor_claude_context.go | one primer paragraph after the generators line (not on the shared list) |

Not touched: editor_save_state.go (the browser has no document),
editor_claude_reference.go, pack/refs.go (`.fnm` and PNG are already content).

## The client (internal/polyhaven)

- **User-Agent.** Every request sends `Foundry-Engine/<polyhaven.Version>
  (+procedural canyon generator)`. A test asserts that it is the only agent
  seen.
- **Listings.** These are `Assets(type, category)`, `Info(id)` and
  `Files(id)`.
  - They are cached as JSON in `<cache>/api/<key>.json` for `TTL`, 24 h by
    default.
  - A failed request falls back to a stale cache entry.
  - `Offline` answers from the cache however old, and returns `ErrOffline`
    for anything not cached.
  - `/files` is parsed per entry, and an entry without the map → resolution
    → format shape is skipped (the API grows keys).
- **Files** are content-addressed by md5 in `<cache>/files/<md5>`.
  - `Fetch` serves a stored file even when offline. Otherwise it downloads
    into `<md5>.part` through the client's semaphore (`MaxConcurrent = 4`).
  - An interrupted download resumes with a `Range` request; a 200 reply
    restarts the part file.
  - Failures are retried 3 times with doubling back-off, but never a
    cancelled context or a 4xx.
  - The md5 is verified before the rename; a mismatch discards the part
    file.
  - Concurrent asks for one md5 share one transfer.
  - `FetchTo` hard-links (or copies) into place; `FetchAll` runs a batch,
    reports its bytes and cancels the rest on the first error.
- **Thumbnails**: `<cdn>/asset_img/thumbs/<id>.png?width=256`, kept as
  `<cache>/thumbs/<id>_<w>.png`.
- **Search.**
  - `Query{Text, Category, Tags, MaxPolycount}.Match` and `Filter` sort by
    ID.
  - `Select` takes a stage's IDs in order, then up to `count` query matches
    in ID order, so a rerun picks the same assets. Download counts change
    daily and are never used to choose.
  - `Sort` orders the browser's grid.
- **Where:** `os.UserCacheDir()/foundry/polyhaven`, or
  `FOUNDRY_POLYHAVEN_CACHE`. `FOUNDRY_POLYHAVEN_OFFLINE=1` makes `New()`
  offline.
- **Credit:** `polyhaven.Credit` = "Assets from Poly Haven (polyhaven.com),
  CC0".

## The importer (internal/polyhaven/phimport)

### `ImportModel(ctx, client, id, ModelOptions)`

1. **Choose the files.** Read the info and file listing. Take the glTF at
   `Resolution` (default 2k; otherwise the largest offered below it, else
   the smallest above). Add every include, an alpha map per cutout
   material, and a displacement map per material when the asset has one.
2. **Fetch** everything (four at a time; cached files are not counted).
3. **Textures.** Each material's albedo is `ph_<id>[_<part>]`, where part is
   the material name less the ID, used when there are several materials.
   - The albedo's normal map and ARM are named after the ALBEDO
     (`<albedo>_normal` is how the engine finds a normal map). A normal
     image two materials share is written once per albedo.
   - Materials sharing one diffuse image (an atlas: the rooibos bush's twigs
     and leaves) share one albedo.
   - Conversions run four at a time at `png.BestSpeed`. They are skipped
     when the output exists and its sources' md5s match the previous
     provenance.
   - `MaxSize` halves an image (box filter) until it fits.
4. **The glTF** is rewritten with every image pointing at
   `../../../textures/<name>.png`, beside its `.bin`, in
   `models/polyhaven/<id>/<id>.gltf`.
5. **Load and check.** `model.LoadGLTF` the rewritten file. Compare the whole
   mesh's sorted extents with the API's `dimensions`; a mismatch over 15%
   becomes a warning in the log and the provenance.
6. **Parts.** When the scene has more than one root node with a mesh (Poly
   Haven's "set of variations": the rooibos bush is five bushes in a row),
   each root becomes its own part, `<id>_<node>`. `NoSplit` turns this off.
7. **Per part:**
   - `Normalise`: the back estimate and turn (below), the base on y = 0, the
     footprint centred, and a back on z = 0.
   - Variants: the main mesh, decimated to `MaxTriangles` when set, plus
     `<id>_lite` at `LiteTriangles`.
   - Cook (`meshout.Cook`) with collision by `collisionFor`: plants none;
     auto gives hulls up to `HullSize` (4 m) and the mesh above, cooked
     mesh-only so the decomposition is skipped.
   - `Measure` fills a `rocks.VariantMeta`.
8. **`polyhaven.yaml`**: source, id, name, type, page, licence, credit,
   authors, categories, tags, `dimensions_mm`, polycount, resolution,
   published and downloaded dates, every file (name, url, md5, size), and
   `import:` (triangles, extent, back, back confidence, yaw, textures,
   cutout materials, meshes with triangles and collision, warnings). It also
   records `texture_sources` (each texture's source key).

**Cook file names** are `<id>[_<part>][_t<n>][_b<back>].fnm`, so different
budgets and explicit backs coexist. An unchanged mesh is not recooked, because
the `.fnm` carries its hash.

### The back

The **back estimate** is the area-weighted horizontal normal:

- `front = normalize(Σ A·n_h)` and `confidence = |Σ A·n_h| / Σ A·|n_h|`.
- The direction comes from the triangle's vertex normals, because a scan's
  winding can be either way round; the weight is its area.
- A piece has a back when the confidence is at least 0.25, and is then
  turned so the back faces −Z.

Measured on the live set:

| Asset | Confidence | Yaw | Result |
| --- | --- | --- | --- |
| namaqualand_cliff_01 | 0.59 | −6° | back |
| namaqualand_cliff_02 | 0.73 | 6° | back |
| rock_face_01 | 0.89 | 1° | back |
| boulders | 0.00–0.01 | | none |

Override with `back: none | -z | +z | -x | +x | <degrees>`.

### `ImportTextures(ctx, client, id, TextureOptions)`

From Diffuse, nor_gl, arm and Displacement (Rough and AO when an asset has
no ARM) it writes:

- `ph_<id>.png`, `_normal`, `_arm`;
- `_rough` (the ARM's G, grey) and `_ao` (R, grey);
- `_height` (grey);
- `textures/polyhaven/<id>/polyhaven.yaml`, with `texture_set: {size_m,
  textures}`.

### Two conventions found by looking

- **The V flip.** `model.ParseGLTF` and `EncodeGLB` flipped V ("the engine's V
  runs up the image, OBJ's convention"). The engine's 2D textures are
  uploaded top row first, so v = 0 is the image's TOP, which is glTF's own
  convention. texdoc's `NormalMapFromHeight` already documents this.
  - The flip mapped every UV island of a scan onto the mirrored part of its
    atlas. Cliffs came out a mosaic of wrong texture patches
    (`shots4/gcliff2_lod0` and `gcliff2_fwd`: the same at full detail and on
    the forward path). Bushes lost most of their twigs, whose cards sampled
    empty atlas.
  - Both directions are fixed, so a GLB round trip is unchanged.
    `nanite.CookVersion` is bumped to 6: the same `.gltf` bytes now cook to
    different UVs, and `HashMesh` (the GLB bytes of an in-memory mesh)
    cannot see the difference.
  - Every cooked mesh, terrain tiles and generated rocks included, re-cooks
    once on its next run. Their geometry is identical; generated content
    never went through a file.
  - The repo ships no glTF content, so nothing in it changes.
- **Normal-map green.** `fragment.glsl`'s cotangent frame takes the bitangent
  along +V, which runs down the image. So the engine's normal maps are what
  DCC tools call DirectX-convention, which is how texdoc authors them.
  `nor_gl` is converted with its green turned over (`chNormal`). This is
  pinned by the importer test.

## The stages

Parameter tables (generated from the schema) are in the guide. In summary:

- **polyhaven_rocks**
  - Sources: `assets` (list: `id`, `back`, `max_triangles`; `AssetList:
    models`), or a query (`category`, `tags`, `search`, `count`).
  - Import: `resolution` 1k/2k/4k/8k (default 2k), `max_size`,
    `max_triangles`, `lite_triangles`, `back` (auto).
  - Collision and cooking: `collision` (auto/hulls/mesh/none), `hull_size` 4,
    `cook`, `cook_memory` 2000 MB.
  - Also `offline`, `gallery`, `gallery_origin`, and the preview-only
    `preview_asset`.
  - Defaults: namaqualand_cliff_01, namaqualand_boulder_02, rock_face_01.
- **polyhaven_plants**: the same without `back`, `collision` and `hull_size`;
  plants are always without a back and without collision. Defaults:
  quiver_tree_01, shrub_02.
- **polyhaven_textures**
  - `layers` (list: `name`, `id`, `scale`; `AssetList: textures`).
    Defaults: sand=red_sand, cracked=cracked_red_ground,
    gravel=red_laterite_soil_stones, rock=red_sandstone_wall,
    cliff=rock_face, wash=dry_riverbed_rock.
  - Also `resolution`, `max_size`, `offline`.

**Artifacts.**

- The rock and plant stages write `rocks.yaml`: `rocks.Manifest`, version 1,
  preset `polyhaven` or `polyhaven_plant`, lithology `scan`.
  - Each variant has every phase-2 field plus `model:`, the
    resources-relative mesh under `models/polyhaven/<id>/`.
    `RocksArtifact.ModelPath` returns it.
  - `Load` is `loadRocks`, so a skipped stage feeds scatter and cliff_wall
    as before.
  - They also write `credits.yaml` (`credit`, then each asset's id, name,
    authors, page, licence).
- `polyhaven_textures` writes `textures.yaml`, which is
  `stages.TextureSetManifest`: `version: 1`, `credit`, and `layers: [{name,
  id, resolution, size, scale, albedo, normal, arm, rough, ao, height}]`.
  - The artifact is the manifest, and `Layer(name)` looks one up.
  - `LoadTextureSet(dir)` is its Load.

**Consumers.**

- `RockSources` (cliff_wall, and scatter by way of it) already named
  `polyhaven_rocks`.
- `ScatterSources = RockSources + polyhaven_plants` is what a scatter rule's
  `rocks` accepts, so **plants are scattered as scenery** by ordinary
  scatter rules (upright with `align: 0, tilt: 0`). This was the smaller
  change: vegetation stays biome volumes.

**Runs and previews.**

- Imports run side by side, as many as `cook_memory` allows given the
  largest asset's API polycount, at most 4.
- Downloads happen inside the run (cached across runs); a forced rerun makes
  no requests (tested).
- The preview never touches the network. It reads the imports' provenance,
  lists them, turns the chosen asset's cooked fallback mesh, and hands a
  manifest of what is imported to downstream previews.

## Plants: cutout, double-sided, through Nanite

The engine already drew alpha-cutout textures alpha-tested in the opaque pass
(`alphaSharpen`, alpha-to-coverage). Nanite's visibility buffer decides
coverage from the albedo's alpha in `nanite_vis_frag.glsl`, and a cooked slot
marked `DoubleSided` drops its clusters' normal cones.

What was missing was the alpha. Poly Haven's glTFs reference opaque JPG
diffuse maps and ship the cutout as a separate `Alpha` / `<part>_alpha` map.
The importer merges it into the albedo's alpha for `MASK` materials, and for
`BLEND` ones when such a map exists. A `BLEND` material with no alpha map, such
as the quiver tree's solid rosettes, stays opaque.

`TestNaniteCutoutShowsThroughOnEveryPath` (GL) draws a 2 m card, red and
opaque on the left and alpha 0 on the right, in front of a brick card. It
checks the visibility buffer (page and flat pools), the forward draw and the
classic path, from the front and from behind:

- the opaque half is red;
- the cut half shows the brick (or the clear colour), never the texture's
  hidden green.

Seen from behind, the visibility-buffer frame was observed to shade through
the forward fallback (0 material bins) while cutting correctly. The test pins
the path from the front only.

In the scene (`shots4/bush_paths.png`), the bushes cut out on both the Nanite
and classic paths, and cast cutout shadows.

Wind (`Scenery.Wind`) is not set on plants (optional per the spec; it would
be an additive placement field).

## The editor

**The Poly Haven browser** (switcher entry "Poly Haven"; window title "Poly
Haven"):

- **Toolbar:** Models/Textures, search, a category combo (by frequency) and a
  sort combo, all through `imguibackend`.
- **Grid:** thumbnails fetched only for on-screen cells, by 4 workers from a
  64-deep queue, decoded off-thread to 128 px, uploaded at most 8 a frame by
  `phStepJobs`, and kept for the session.
- **Details:** authors, categories, tags, triangles, size, date, licence,
  and "imported" status.
- **Import settings:** the resolution (from the file listing, fetched async),
  max texture px, and for models Plant (defaulted from the categories),
  Back, Max triangles and Lite triangles.
- **Download & Import** runs on a goroutine with recover. It shows a progress
  bar (the importer's step and fraction), a log and Cancel (context). On
  success it runs a texture rescan on the UI thread, so pickers see `ph_*`
  at once.
- **Add to stage:** the Browse… target, else the selected stage's first list
  of that type. One Generator undo step; refuses a duplicate; names a texture
  layer after the ID.
- **Credit:** the footer always shows `polyhaven.Credit`.

**The Generator's id-list widget:** a list param with `AssetList` shows each
item's 40 px thumbnail and its ID (or `name: id`) as the item header, plus
**Browse...** (`OpenPolyHavenBrowserFor(type, stage, path)`).

## Texture memory (sub-agent; numbers re-measured in the level below)

- **Compression**
  - Setting: `gfx.TextureCompression`, default **off**. It persists as
    `texture_compression` in graphics.yaml, is the
    `@level.prop.texture_compression` script prop, and
    `FOUNDRY_TEXTURE_COMPRESSION=1|0` overrides it.
  - Which textures: world textures loaded from files, at least 256 px a side
    and multiples of 4. `<name>_normal` maps become BC5
    (`COMPRESSED_RG_RGTC2`); everything else BC7 (`COMPRESSED_RGBA_BPTC_UNORM`,
    a mode-6 encoder: PCA endpoints, p-bit search, two least-squares passes).
  - Mips are built on the CPU, every level is encoded, and the work runs in
    parallel over block rows.
  - The texture editor's live re-uploads stay RGBA8.
- **Disk cache:** `~/.cache/foundry/texcache/<sha256(source bytes, format,
  encoder version)[:32]>.ftxc`, written atomically. A corrupt file counts as
  a miss. Skip-mips is not in the key.
- **Fallback:** after each compressed upload there is one `glGetError`. On an
  error the texture re-uploads as RGBA8, the format is marked refused, and it
  is logged once. (Mesa d3d12 lists neither format in
  `GL_COMPRESSED_TEXTURE_FORMATS` but supports both, so the list is only
  logged.)
- **BC5 normals:** `BindNormalMap` passes `hasNormalMap = 2`, and
  fragment.glsl reconstructs z. There is no new sampler or derivative call,
  so the Nanite material pass is unaffected (`TestVisMaterialProgramBuilds`
  passes). Uncompressed maps keep `hasNormalMap = 1` and render exactly as
  before.
- **Skip mips:** `gfx.TextureSkipMips` 0–3, default 0. It persists as
  `texture_skip_mips` (prop and `FOUNDRY_TEXTURE_SKIP_MIPS` too).
  - It applies to textures with a short side of at least 512 px, and stops
    at a 256 px short side.
  - The big levels are never allocated: the uncompressed path halves on the
    CPU, and the compressed path uploads `Levels[skip:]`.
- **F3 line:** `Textures: N MB (T textures, a BC7, b BC5), compression on/off,
  skip K mips`. This phase also prints it in `foundry` bench reports.
- **API:** `gfx.TextureVRAM()`, `TextureVRAMBytes()`, `TextureFormatOf(name)`.
- **Where the setting lives in the UI:** the "Graphics tab" is the game's own
  `.fui` screen, so the settings appear to it as the two script props. No
  repo screen was edited.

## Classic-path geometry sharing (sub-agent)

`internal/world/scenery_share.go` holds the shared builds.

- **Key:** a cache keyed by `(*nanite.Mesh, gfx.IndexedModels,
  NaniteClassicShadowLOD)`. `nanite.Load` caches by path and checks the
  file stamp, so a re-cooked file gets a new key.
- **What is shared:** CPU-only `gfx.Geometry` templates per slot and their
  ShadowLOD templates, plus a `gfx.MeshShare` each. This is the scheme the
  entity attachments' `model_mesh_cache.go` already used.
- **What stays per placement:** each placement's submeshes are value copies,
  with their own texture (from `SlotTexture`, so material overrides still
  work), alpha and VAO. They `UploadShared` the buffers.
- **Lifetime:** reference-counted claims, released by `Scenery.Delete` (which
  `Load` calls first, and chunk unload uses), at most once per claim.
- **Also shared:** non-cooked `.fmdl`/`.obj` scenery now uses
  `pristineModelDocument` and the shared model mesh.
- **Readers checked:** the instanced and GPU-cull scenery draws, picking,
  point-shadow signatures, GI sources, collision, nav and chunk streaming.
  None writes into shared slices.

Memory (`foundry profile-load`, `/usr/bin/time -v`), on phase 3's scratch
canyon:

| Level | Before | After |
| --- | --- | --- |
| canyon_w100 | 3.54 GB, 8.6 s | |
| canyon_w200 | 6.00 GB, 13.9 s | 2.95 GB, 6.9 s |
| canyon_w, 1,266 placements | ~32 GB by extrapolation (+24.5 MB a placement); not run | 2.82 GB, 6.3 s |
| canyon, 15,521 placements, 87 models | OOM (phase 3's report) | 2.76 GB, 7.4 s (2.14 GB at `GOGC=20`) |

The phase-4 scratch level loads in 2.29 s at 0.49 GB peak with
`profile-load` (1,503 placements).

## Verification on the live library

**Scratch project:** `$S/p4` (`$S` = the session scratchpad,
`/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/8b1c0d12-038d-4935-9ad4-2bf2380ff5b6/scratchpad`).
Shaders, biomes and fonts are symlinked to the repo; textures are a real
directory with the repo's linked in. Nothing was written to the repo's
resources/.

**What was downloaded (each file once):**

- 8 models at 2k: namaqualand_cliff_01 and _02, namaqualand_boulder_02, _04
  and _05, rock_face_01, quiver_tree_01, wild_rooibos_bush.
- 6 texture sets at 2k: red_sand, cracked_red_ground,
  red_laterite_soil_stones, red_sandstone_wall, rock_face,
  dry_riverbed_rock.
- The catalog, info, file listings and thumbnails, plus 3 glTF files fetched
  by hand to read their materials.

**The generator:** `p4/resources/generators/canyon.fgen`.

- terrain: 256 m at 0.5 m, a canyon with 2 tributaries, painted with
  `ph_red_sand` at 3 m a repeat.
- `cliffs`: polyhaven_rocks with the two namaqualand cliffs and rock_face_01.
- `boulders`: polyhaven_rocks with 3 namaqualand boulders, `back: none`,
  `lite_triangles` 5000.
- `trees` (quiver_tree_01) and `bushes` (wild_rooibos_bush): polyhaven_plants.
- walls: cliff_wall over `cliffs`, `max_chunks` 300.
- scatter, four rules:
  - talus (boulders, max 6000 triangles, so the lite copies);
  - boulder_field;
  - trees (density 0.15, spacing 4, upright);
  - bushes (density 3).

It is spliced into `levels/canyon.yaml`: 1,503 placements, 486 KB.
`levels/gallery.yaml` (from `p4tools/gallery.py`) stands every import in a
row on flat ground, facing the sun.

### Numbers (dev box: 20 threads, WSL2, GTX 1070 through Mesa d3d12)

**Downloads.** 98.5 MB of model files and 66.3 MB of textures (the cache:
165 MB).

| Model | Download | Time |
| --- | --- | --- |
| namaqualand_cliff_01 | 10.7 MB | 0.5 s |
| namaqualand_cliff_02 | 15.4 MB | 1.1 s |
| namaqualand_boulder_02 | 13.3 MB | 0.7 s |
| namaqualand_boulder_04 | 9.7 MB | 0.6 s |
| namaqualand_boulder_05 | 11.5 MB | 0.7 s |
| rock_face_01 | 9.7 MB | 0.3 s |
| quiver_tree_01 | 22.2 MB | 1.1 s |
| wild_rooibos_bush | 6.0 MB | 0.3 s |

The six texture sets took 0.3–0.8 s each.

**Imports.**

| Step | Time |
| --- | --- |
| Texture conversion (per model, 3–6 maps) | 0.4–1.3 s |
| Texture conversion (per texture set) | 0.5–0.9 s |
| Cook: cliff_01, 94k triangles, mesh | 1.2–1.3 s |
| Cook: cliff_02, 194k triangles | 2.7 s |
| Cook: boulders, 59–98k, hulls | 2.2–4.0 s |
| Cook: quiver tree, 150k | 3.6 s |
| Cook: lite copies at 5k | 0.1–0.4 s each |

- All 8 models from nothing took 43 s (34 s and 8.8 s in two CLI runs), at
  0.87 GB peak RSS. The 6 texture sets took 8 s at 0.27 GB.
- A re-import with everything cached runs in 0.4–0.7 s per asset (textures
  skipped by their sources' md5s, cooks skipped by hash).
- **Generator run:** the first run took 27.2 s at 0.96 GB peak (the terrain
  plus the three import stages, most of it the boulders' hull cooks). A rerun
  that changes only scatter takes 0.2–0.3 s.

**Disk.**

- `models/polyhaven/`: 111 MB for 8 assets (the `.gltf`/`.bin` plus cooks;
  cliff_02 at 24 MB including its lite copy).
  - The `.fnm` sizes: 4.2 MB (cliff_01), 8.5 MB (cliff_02), 2.6–4.4 MB
    (boulders), 6.4 MB (quiver tree), 0.1–0.9 MB (bush parts), 0.3 MB (each
    5k lite copy).
- Model textures: 184 MB. Texture sets: 162 MB. PNG at BestSpeed is about
  twice the JPG.
- The texture cache: 113 MB for 22 compressed 2k textures.

**Load.** The bench (`FOUNDRY_BENCH`, yaml, GPU path) loads in 3.6–3.9 s, and
4.4 s with compression on from the cache (8.0 s on the first, encoding run).

**Texture VRAM.** The overview pose, 23 world textures resident:

| Configuration | VRAM |
| --- | --- |
| uncompressed (default) | 469.3 MB |
| BC7/BC5 | 117.3 MB (11 BC7, 11 BC5) |
| BC7/BC5 + skip 1 | 29.3 MB |
| uncompressed + skip 1 | 117.3 MB |

The compressed and uncompressed pictures are indistinguishable
(`shots4/vram_cmp.png`).

**Frame cost.** 1920×1080, TAA, visibility buffer, VSM, p50 on WSLg:

| View | p50 |
| --- | --- |
| quiver-tree view | 48 ms |
| wall view | 56 ms |
| canyon overview | 86 ms |
| gallery level | 24 ms |

Compression did not change frame time on WSLg (85.5 against 86.3 ms). GPU
timer queries read 0 on Mesa d3d12, as in phase 3. Judge on the 1070 under
Windows.

### Pictures inspected (`$S/shots4/`, each `<name>/pose_0.png`)

- `tree1`: the first tree render. The bushes were **black silhouettes**:
  two materials share one diffuse image under two image indices, and the
  second kept its original JPG name, so its texture was missing. Fixed: every
  image index now gets its engine name.
- `tree2`, `close1.png`, `rooibos_tex.png`: then the bushes were sparse twigs.
  That was the V flip sampling empty atlas, not the cutout.
- `gallery1.png`, `gcliffs.png`: cliff_02 was a **mosaic** of texture patches.
  `gcliff2_ab.png` (LOD off, forward path) shows the same picture, so it was
  not LOD. `gcliff2_unflip_crop.png`, with V un-flipped, is photographic.
  That led to the V-flip fix and the CookVersion bump.
- `sheet2.png`, after the fixes:
  - the wall view (namaqualand cliffs stacked by cliff_wall, scan-textured);
  - the boulder field (namaqualand boulders grounded with shadows, lichen
    colour right);
  - the quiver tree;
  - cliff_01 alone, which matches its Poly Haven thumbnail.
- `gtree3_s.png`: the quiver tree close up. Its aloe rosettes are double-sided
  and correct. The trunk is slimmer than the Poly Haven thumbnail suggests,
  but that is the model at its true 2.7 m.
- `grf_cmp.png`: rock_face_01 beside its thumbnail; a match.
- `bush_paths.png`: the rooibos bushes, Nanite against the classic path. Both
  cut out, with dense needle tufts and cutout shadows.
- `over1_s.png`: the canyon overview. Cliff scans clad the mesa walls.
- `vram_cmp.png`: uncompressed, BC7/BC5, and BC7/BC5 with skip 1.

**Scale, orientation and floating:** sizes match the API's except
rock_face_01. That glTF is 0.70× the listed dimensions on every axis, which
looks like the listing's (warned, left as scanned). Bases sit on the ground;
backs face into walls. In the wall views a few stacked pieces stand proud of
the heightfield wall with a dark gap behind them. This is cliff_wall's
seating of irregular, open-backed scans by their bounding box (phase 3's
rule), not the import.

## Tests

| Package | What |
| --- | --- |
| polyhaven | the User-Agent on every request; listings cached for the TTL, refetched after it, a category its own listing; info and authors; bad IDs refused; /files keeps three-level entries and skips odd ones; offline from the cache only and no requests; a download cut mid-way resumes with one Range request; 500s retried, 404 not retried; a bad md5 refused and not stored; FetchTo; at most 4 concurrent with 12+1 jobs and a duplicate fetched once, progress reaching the total; cancellation; Select deterministic (IDs, then the query in ID order), Filter and Sort |
| polyhaven/phimport | end to end on the fake asset: textures named per material; the cutout's alpha merged (left opaque, right cut); the normal map's green turned over; the glTF re-pointed and loading with the engine names and double-sided; the frame (a face looking +X turned so its back is −Z on z = 0, centred, base on 0, in the cooked .fnm too); the lite copy; model paths; provenance with md5s; a re-import downloading nothing and rewriting no texture; a plant with no back and no collision; root nodes split into parts (and not with NoSplit); a texture set (names, max size, rough = ARM green, provenance; a texture refused as a model); Normalise's back choices; part names; resolution choice |
| procgen/stages | polyhaven_rocks feeding cliff_wall and a scatter rule (its `max_triangles` picking the lite copies), polyhaven_plants by query feeding an upright, collisionless scatter rule, polyhaven_textures' layer; models under models/polyhaven/; credits.yaml; Load; a forced rerun making no requests; offline with an empty cache failing cleanly; validation; every kind has an asset list; the preview without and with an import (turntable mesh, manifest artifact); scatter's sources include plants |
| editor | the browser drawn with no GL against the fake: catalog, thumbnails for visible cells, a selection's listing and the plant default, Download & Import (one at a time) writing the files, imported status, Add to stage (refusing a duplicate, a texture not targeting a models list, Browse's target kept, one undo step), the Generator drawing an asset list with thumbnails and Browse…; textures, search and a failed catalog |
| render (GL) | `TestNaniteCutoutShowsThroughOnEveryPath` (above); sub-agents: `TestScenerySharedClassicPlacementsDrawAtTheirOwnTransforms`, `TestBC5NormalMap…` |
| model | the UV test now expects UVs as stored |
| texcompress, gfx, world | the sub-agents' encoder, cache, settings, VRAM, and sharing tests (their reports are summarised above) |
| internal | layering: `polyhaven` must not reach gfx/model/nanitecook/world/procgen; `texcompress` must not reach gfx |

### Results

- `go build ./...` and `go vet ./...` are clean.
- `go test` passes for polyhaven/..., procgen/... (all), model, nanite,
  nanitecook, pack, app, gfx, texcompress, world/..., render/..., biome,
  levelio and internal (layering).
- editor: everything passes except `TestScoreSibeliusKeymap` and
  `TestScoreRebinding`, the score session's work in progress (as in phase 3).
- GL (`FOUNDRY_GL_TEST=1`): gfx passes; editor's generator and Poly Haven
  tests pass; render: every test passes, `TestNaniteCutoutShowsThroughOnEveryPath`
  included, except `TestNaniteFoliageDrawsTheClassicNearField`, the failure
  recorded since phase 0 (415 s run).

## Known gaps

- **Lite copies in walls.** cliff_wall uses every variant of its source, so
  give a cliff stage no `lite_triangles` (documented); a wall-side
  `max_triangles` would be a small addition.
- **Plant spacing** works off the trunk or stem footprint (the seating needs
  it); use `spacing` 3–4 for trees.
- **`back` tags** make a separate cook per explicit back (`_bnone` …), even
  when `none` gives the same mesh as `auto`.
- **Atlases.** The `alphaCutoff` value is not read (the engine's cutoff is
  0.5 ± its bias), and the ARM, rough and height maps are written but nothing
  uses them until phase 5.
- **PNG disk.** BestSpeed PNG is about twice the JPG: 2k model textures run
  15–25 MB per asset.
- **Wind** on imported plants is not set.
- **Browser.** It shows no per-file sizes before import; the
  Generator id-list widget does not validate IDs against the catalog, only
  their spelling.
- **Classic path:** the first placement of each mesh still pays one
  de-index/weld; the template duplicates the decoded pool (see the sharing
  section).
- **Texture compression:** mode 6 only; no cache eviction; a first-time encode
  outside the prefetch stalls the main thread (~0.2 s per 2k map); settings
  apply to textures loaded afterwards. Cubemaps, UI textures and texture
  arrays are neither compressed nor counted.
- **Visibility buffer from behind.** A lone double-sided card seen from
  behind shaded through the forward fallback in the GL test (the picture is
  right). This was not investigated further.
- **Floating pieces** in cliff walls (above) are phase 3's seating, made more
  visible by open-backed scans.
- The phase-0 `list_assets` gap still stands.
