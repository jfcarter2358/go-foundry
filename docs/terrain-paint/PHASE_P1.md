# Phase P1 — paint core

Built 2026-10-15. Spec: docs/terrain-paint/SPEC.md ("Paint layers",
"Consumers", "Editor"). The user-facing page is
docs/guide/36-terrain-paint.md.

## What was built

**Paint layers in the document.**

- A `.fgen` declares its layers at the top level:
  `paint: [{name, kind, resolution}]`. Kinds are density, exclude, material,
  water, height and direction. Resolution is metres per texel (0.125–8, must
  divide 64), default 0.5, and is not written when it is the default.
- `procgen.Doc.Paint` holds the declarations. `Doc.PaintData`
  (`*paint.Set`) holds the pixels. It is not in the file and not in
  `Fingerprint`.
- `ReadDoc` loads the pixels from `<doc>.paint/` beside the file. `WriteDoc`
  saves what changed.
- `Doc.Clone` snapshots the pixels copy-on-write. A run or a preview reads a
  frozen snapshot while the editor paints on.
- New param type **`ParamMask`** (`"mask"` in docs, `procgen.Mask(name)`): a
  layer's name, or empty. It is a plain string in YAML.
- `Validate` reports bad declarations (name, kind, resolution, duplicates)
  and masks that name an undeclared layer.
- The editor's widget is a combo of the declared layers with each one's kind
  (`genMaskCombo`).

**`internal/procgen/paint`**, a pure package with a layering `upward` entry
like procgen/terrain's.

- **Tiles.** Fixed 64 m world tiles counted from the origin, keyed `(i, j)`.
  Learned from nothing: they are world-space, so a layer is independent of
  the terrain under it.
  - 64 m divides the terrain stages' 64 and 128 m tiles. A world-aligned
    128 m terrain tile is 2×2 paint tiles, and a centred terrain with an odd
    tile count still has edges on multiples of 64.
  - `TilesOverlapping(rect)` answers the general case, for P2's per-tile
    hashes.
- **Values** are quantised to the file's precision on every write, so memory
  equals disk exactly and hashes survive a save/load.
- **Sampling.** `Sample` / `SampleChannel` / `Direction`: bilinear between
  texel centres, across tile edges. A missing tile reads 0, and a nil layer
  reads 0.
- **Strokes.** `Stroke{X, Z, Radius, Strength, Falloff, Mode, Value, Dir}`:
  a circle stamp, weight 1 in the core with a smoothstep over the outer
  Falloff fraction.
  - Modes: add, erase, smooth (3×3 mean of the pre-stamp layer), set.
  - Height's add is in metres. Direction moves toward `Dir`.
  - A sub-quantum erase or smooth step still moves one quantum, so erasing
    always reaches 0.
  - `ApplyLine` stamps every quarter radius.
  - `TouchedBy` gives the tiles a stamp reaches, for undo snapshots.
  - Tiles left all zero are deleted.
- **Hashes.**
  - Per tile: sha256 over the kind, the size and the file-encoded samples,
    cached atomically.
  - Per layer: the declaration plus every (key, tile hash) in key order.
  - `Set.Fingerprint` feeds the editor's save chip.
- **Copy-on-write.** `Layer.Clone`, `Snapshot` and `Restore` freeze shared
  tiles, and the next write copies a frozen tile first.

**Storage**: `<fgen dir>/<doc>.paint/<layer>/<i>_<j>.png`

| Kind | PNG |
| --- | --- |
| density, exclude, material, water | 8-bit grey (0–255 = 0–1) |
| height | 16-bit grey (32768 = 0 m, 1/256 m steps) |
| direction | RGB (R = x, G = z, 128 = 0, ±127 = ±1, B = 128 unused) |

- Writes are atomic and keep no `.bak`.
- `Save` writes only tiles whose hash differs from what that folder holds,
  or that are missing. It deletes files of tiles no longer stored, then
  empty layer and paint folders.
- A layer dropped from `paint:` keeps its folder.
- Save As into another folder writes every tile.

**Hashing.** `foldPaint` (procgen/paint_doc.go) runs in `planWithNotes`, in
the region expansion and in the preview's `lenientPlan`.

- A stage whose resolved params name layers (`MaskRefs`, list items
  included) gets `"paint:" + name=layerHash,...` appended after its refs and
  HashExtra.
- A stage naming none gets nothing, so every existing document hashes
  byte-identically. The golden test passes.
- Painting a layer reruns exactly its readers.
- Erasing back to nothing restores the old hash.
- An expanded region stage reads the LEVEL doc's layers (world-level paint).

**Consumers** (all fields `Added`, defaults off):

- `place.Rule.Mask func(x, z) float64`.
  - `Rule.Weight` multiplies by it. A nil Mask is exactly the old code path.
  - Scatter drops a candidate whose mask is 0 after drawing its randomness,
    so the RNG stream is unchanged and edges are sharp to the candidate.
  - Grass gets the mask through its band rule's `Weight` for free.
- scatter and grass_field rules get `paint` (mask), `paint_band` (vec2, a
  ramp: 0 at or below lo, 1 at or above hi, default 0..1 = the value) and
  `paint_invert`. The stages get `exclude: [{layer}]`, which multiplies
  every rule by 1 − v. This lives in stages/paint_bands.go, applied by
  `maskScatterRules` / `maskGrassRules` after the existing rule builders
  (run and preview).
- terrain_material and region_material get a `paint` list:
  `{layer, target, mode: override|band, band, invert, strength}`
  (stages/terrain_material_paint.go).
  - Applied to the per-sample weights before quantisation, after rules and
    covers.
  - terrain_material targets are roles (with covers when present).
  - region_material targets are a role (shared by that role's layers by
    their weights at the sample, or all to the first) or one layer by Poly
    Haven id or albedo name.
  - The far backdrop is not painted.
- **Deviation from the spec**: SPEC.md lists `material` as a "splat override
  index". It is a 0..1 weight instead, and the STAGE names the target
  layer. The task statement asked for that ("painted weight forces a named
  layer"). It blends and smooths properly, where an index would not.

**Editor** (editor_generator_paint.go):

- A **Paint** toggle in the 2D view's bar, and a second row:
  - the layer combo;
  - **New layer…** (name, kind, resolution; an undoable document edit);
  - radius, strength and falloff;
  - mode (add, erase, smooth);
  - undo and redo.
- `[` and `]` resize the brush.
- A left drag paints live: a stamp every quarter radius, written straight
  into the layer.
- **Overlay**: one RGBA texture per stored tile, tinted by kind, alpha from
  the value. It is drawn in the view's draw list with `genUploadTexture`:
  editor-only, no world or material passes, no texture units claimed.
  Tiles a stroke wrote are re-uploaded. Undo and switching layers rebuild
  all of them.
- **Undo**: the brush has its own stack of `{layer, before, after}`
  frozen-tile snapshots, one step per stroke. Ctrl+Z / Ctrl+Y act on it
  while the tool is on.
- `genApplySnapshot` keeps `PaintData`, so a document undo never touches
  paint.
- Mouse-up commits: undo step, `genAfterEdit` (dirty chip and preview
  rerun).
- The save chip's fingerprint is `genFingerprint` = doc ^ paint.
- Save and Save All go through `genSave` → `WriteDoc`, which writes the
  tiles. editor_save_state.go was not touched.
- Running uses `genDoc.Clone()` (a pixel snapshot), so it is a full rerun of
  the reading stages.

**CLI**: `foundry gen paint <doc> -layer L [-kind K] [-res R]
[-mode add|erase|smooth|set] [-strength] [-falloff] [-value] [-dir x,z]
[-disc x,z,r]... [-line x0,z0,x1,z1,r]... [-clear]`, and `-list`
(gencli_paint.go). `foundry gen run` needed no change: `ReadDoc` loads the
paint.

## Files

New:

- internal/procgen/paint/:
  - paint.go
  - layer.go
  - stroke.go
  - storage.go
  - paint_test.go
- internal/procgen/paint_doc.go and paint_doc_test.go
- internal/procgen/stages/:
  - paint_bands.go
  - terrain_material_paint.go
  - paint_bands_test.go
- internal/procgen/gencli/gencli_paint.go and gencli_paint_test.go
- internal/editor/editor_generator_paint.go and editor_generator_paint_test.go
- docs/guide/36-terrain-paint.md
- this file

Edited (small, targeted):

- procgen:
  - params.go (ParamMask, Mask)
  - doc.go (Paint, PaintData, Clone)
  - fgen_file.go (paint key, mask decode and encode, ReadDoc / WriteDoc
    load and save)
  - validate.go
  - run.go, regional.go, preview.go (foldPaint)
- procgen/place/scatter.go (Rule.Mask)
- procgen/stages:
  - scatter.go, scatter_run.go
  - grass_field.go, grass_field_run.go
  - terrain_material.go (param list and one call after
    coverGroundWeights, reaching the Env through the pool argument both
    callers pass)
  - region_material.go, region_material_run.go
- procgen/gencli/gencli.go (the verb and usage)
- editor:
  - editor_generator_view2d.go (toggle, canvas hook, splines off while
    painting)
  - editor_generator_window.go (reset, close, fingerprint, keys)
  - editor_generator_undo.go (keep pixels)
  - editor_generator_params.go (mask widget)
- internal/layering_test.go (the upward entry)
- docs: guide/33-generators.md (file example, mask type, scatter /
  grass_field / terrain_material / region_material paint, CLI line) and
  guide/README.md (one row)

## Tests

- `go test ./internal/procgen/paint/`: storage round trip for every kind,
  exact samples and hashes; sparse storage and deletion down to no folder;
  bilinear sampling across a tile edge; stroke modes; hashes follow content
  and Snapshot/Restore; copy-on-write clones; declaration validation;
  TilesOverlapping.
- `go test ./internal/procgen/`:
  - `paint:` and mask round trip, stable bytes, no `paint` key without
    layers;
  - validation;
  - ReadDoc / WriteDoc carry pixels, and a clone is a snapshot;
  - hashing: stages naming no layer keep their hash, painting reruns only
    the readers, erasing restores the hash, the preview hash follows.
- `go test ./internal/procgen/stages/`:
  - a scatter rule restricted to a painted disc (every rock inside it);
  - a grass rule restricted to a disc, with an exclude strip through it (no
    clump on the strip), and a further stroke making only `grass` stale;
  - defaults off (no mask, unchanged hashes with an unread painted layer);
  - the ramp;
  - terrain_material override and band (sums stay 1, unknown target is an
    error);
  - region_material role and named-layer targets;
  - TestTerrainStageGolden passes.
- `go test ./internal/procgen/gencli/`: paint declares and writes tiles, run,
  a second stroke leaves `a run would run rubble` only, `-list`.
- `go test ./internal/editor/`: TestGeneratorPaintBrush covers New layer,
  a simulated drag paints, one undo step, dirty chip, overlay textures, a
  document undo keeps pixels, brush undo and redo, Save writes tiles and
  clears dirty, and the mask widget draws.
- Full suites at the end:
  - `go build ./...` passes.
  - `go vet` passes on procgen, paint, place, stages, gencli, editor and
    internal.
  - `go test ./internal/procgen/...` passes in every package: stages 237 s,
    rocks 39 s, terrain 25 s, gencli 21 s.
  - `go test ./internal/` (layering) passes.
  - `go test ./internal/editor/...` passes every generator test, the new
    one included, with three failures that are not this phase's:
    TestScoreSibeliusKeymap and TestScoreRebinding (the score session's
    keymap, in progress) and TestYAMLFieldDocsUpToDate (the level schema's
    comments changed in internal/world, which this phase did not touch; not
    regenerated here, since that is the other session's change to make).

## End-to-end check

Scratch project `$S/pproj` (`$S` = this session's scratchpad). Its
resources/ are real folders for generators, levels, models, textures and
materials (the shipped files symlinked one by one inside them), so imports
and generated output never reach the repo. Shaders and fonts are symlinked.

The generator `paint_demo.fgen` holds:

- a 256 m rolling terrain;
- leafy_grass / withered_grass / rock_boulder_dry textures (offline, from
  the Poly Haven cache);
- a terrain_material with `paint: [{layer: trail, target: cracked}]`;
- grass_medium_01;
- a grass_field with a sparse rule (0.15/m²), a `meadow` rule (4/m²,
  `paint: meadow`, `paint_band: [0, 0.5]`) and `exclude: [{layer: path}]`.

It was painted with `foundry gen paint`:

- meadow: disc at (0, 15), r 28, falloff 0.3;
- path (exclude): a line along z = 0, r 3;
- trail (material): the same line, r 3.5.

It ran with `foundry gen run -workers 8`: 5 stages, 5.9 s, 16,648 clumps
against about 9,800 for the sparse rule alone.

The log attributes every clump to "sparse": both rules share the same kit
kinds, and the per-rule count is by kind. That is a logging quirk of
grass_field, not paint.

Shots with `FOUNDRY_NANITE_VSM=0 foundry play run` (no hang). What they show:

- overview.png and top.png: a dense round patch of grass south of a straight
  lighter strip. The strip is the withered-grass layer forced by the trail
  override. No clump stands on it, and the patch is cut cleanly by it. The
  rest of the ground has sparse clumps.
- path.png, a low view west along the strip: thick tall grass on the
  meadow side, the bare trail, thinner grass at the disc's northern edge
  across it.

## Known gaps (carried to P2 and later)

- **Whole-stage reruns.** A stroke reruns every stage reading the layer in
  full. Per-tile hashing via `TileHash` and `TilesOverlapping`, tile-scoped
  Supersede for scatter, and per-tile splat rewrites are P2.
- **No 3D-viewport brush and no projected overlay.** That is P2.
- **Brush presets** (`.fpb`) are P3. There is no layer delete or rename in
  the editor (edit the YAML).
- **Unread kinds.** `height`, `water` and `direction` are stored, sampled
  and hashed, but nothing reads them yet (W2 ground_edit, P4 paths).
  `water` holds only an area weight; a painted water level needs a second
  channel or a height layer in W2.
- **The far backdrop is not painted.** A preset's own `terrain_material`
  paint list is not read by region_material (world-level paint only).
- **The overlay** re-uploads a touched tile's texture on every stamp of a
  drag. That is fine at 128² tiles, but a 2 cm-resolution layer under a
  large brush would be slow. Not measured.
- **The preview** reruns after each stroke through the normal debounce.
  Stage previews (scatter dots, grass dots) follow the paint, but a terrain
  preview does not show other layers' effects.
- **grass_field's per-rule counts** in the log merge rules that share kit
  kinds (pre-existing).
- **No GL test of the overlay.** The editor test stubs the upload. The
  overlay was not looked at in a real window, only through the editor test
  and the rendered game view.
