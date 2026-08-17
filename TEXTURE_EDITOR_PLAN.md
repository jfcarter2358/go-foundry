# Texture Editor — Implementation Plan

A layered texture editor that opens in its own OS window, saves a re-openable
layered source file plus a flattened PNG, and pushes edits into the live 2D/3D
editor the moment they happen.

## Requirements

1. Opens in a **new window** when selected from the editor.
2. Create and update textures.
3. **Transparency** support (per-texel alpha).
4. **Up to 8 layers**.
5. On save: a **custom layered format** (re-openable) *and* a flattened texture
   the engine uses.
6. Updating a texture **immediately updates every instance** in the 2D/3D editor.
7. Saving a new texture makes it **immediately available** to the 2D/3D editor.
8. Canvases are capped at **1024×1024**.
9. All art is **indexed to a 256-colour palette** — no free RGB painting.

## What the existing code already gives us

These four facts decide most of the design — worth reading before the phases.

- **Nothing caches a texture handle.** [wall.go:181](wall.go#L181) and
  [wall.go:277](wall.go#L277) do `gl.BindTexture(gl.TEXTURE_2D, Textures[g.Texture])`
  at draw time; `detailAlbedo` ([detail_render.go:639](detail_render.go#L639)) does the
  same name→handle lookup. So requirement 6 is nearly free: re-upload pixels into
  the *same* GL texture object and every wall, floor, primitive, scenery item and
  detail-scatter instance shows the new image on the next frame. No level walk,
  no geometry rebuild.
- **The texture list is enumerated fresh every frame.** `textureCombo`
  ([editor_ui.go:187](editor_ui.go#L187)) rebuilds its list from `textureNames`
  each call, and `ensureTextureLoaded` ([texture.go:60](texture.go#L60)) resolves
  through the same map. Requirement 7 is one map insert.
- **Alpha cutout already works; alpha blending does not.**
  [fragment.glsl:298-299](resources/shaders/fragment.glsl#L298-L299) computes
  `finalAlpha = texColor.a * alpha` and discards below 0.001, so fully
  transparent texels vanish today. But a surface only enters the blended
  back-to-front pass ([main.go:459-476](main.go#L459-L476),
  [main.go:596-633](main.go#L596-L633)) when `Geometry.IsTransparent()` is true,
  and that is purely the per-geometry `Alpha` scalar
  ([wall.go:160-162](wall.go#L160-L162)). Semi-transparent *texels* currently render
  as opaque. Phase 5 fixes that.
- **`ScanTextures` only picks up `.png` and skips directories**
  ([main.go:24-43](main.go#L24-L43)), so a `.ftex` source file can live right
  beside its `.png` in `textures/` and be invisible to the loader.

Also confirmed present in the pinned deps: `imgui.Context.SetCurrent()` (imgui-go
v4.7.0 `Context.go:55`, contexts share a font atlas by default),
`DrawList.AddImageV`, `sdl.GL_SHARE_WITH_CURRENT_CONTEXT`, `Window.GLMakeCurrent`,
`Window.GetID`, `sdl.GetGlobalMouseState`.

## The palette (cross-cutting)

Everything below assumes **one global 256-entry palette shared by every texture**,
which is the classic Doom/Build arrangement and makes palette-swap effects and a
coherent art style possible. Each entry is RGBA8, so a *slot* carries its own
alpha — that is how requirement 3 and requirement 9 coexist. By convention slot 0
is fully transparent.

> If you'd rather each texture carried its own private palette, say so — it's a
> small change (drop the global file, keep the embedded copy) but it changes the
> palette UI and kills palette-swapping, so it's worth settling before Phase 1.

- **Storage:** `<exe>/textures/palette.pal` — magic `FPAL`, `uint16` version,
  then 256 × RGBA8 (1024 bytes). Loaded at startup beside `LoadDetailPresets()`
  ([main.go:144](main.go#L144)). If the file is missing, a compiled-in default
  palette is used and written out on first save, so a fresh checkout works.
- **Every `.ftex` embeds a copy of the palette it was authored against.** On open,
  if the embedded palette differs from the current global one, the editor asks:
  *adopt the file's palette globally* or *remap the art to the current palette*
  (nearest-colour per index). Without this, editing the global palette would
  silently recolour every old source file with no way to tell.
- **Layers store 1-byte indices**, not RGBA. Compositing happens in RGBA (so
  opacity and blend modes still work), and the result is **quantised back to the
  palette** at flatten time — the engine therefore never sees an off-palette
  colour.
- **Quantisation** is nearest-colour in RGB with alpha matched first, memoised by
  a `map[uint32]uint8` cache. Exact matches must return that entry; ties resolve
  to the lowest index so results are deterministic. No dithering in v1 — worth
  adding later as an import option, not as a paint behaviour.
- **Editing a palette slot is a live global operation:** indices don't move, so
  every open document just re-flattens and re-uploads. Already-saved PNGs on disk
  hold quantised RGBA and won't follow, so the palette panel gets a **"Rebuild all
  textures"** action that walks `textures/*.ftex`, re-flattens each against the new
  palette and rewrites its PNG.

## Phase 0 — Multi-window plumbing (`window.go`, refactor `editor_imgui.go`)

This is the only genuinely fiddly part. It is also reusable: NEXT.md already
wants "brush settings should pop out into new windows".

New type:

```go
type UIWindow struct {
    Win           *sdl.Window
    GL            sdl.GLContext
    Ctx           *imgui.Context
    VAO, VBO, EBO uint32 // VAOs are NOT shared across GL contexts — one set per window
    ID            uint32
    W, H          int32
    Draw          func()  // per-frame UI body
    OnClose       func()
}
```

- Create with `sdl.GLSetAttribute(sdl.GL_SHARE_WITH_CURRENT_CONTEXT, 1)` *before*
  `Win.GLCreateContext()`. Programs, buffers and **textures** are then shared with
  the main context; VAOs are not, hence the per-window VAO.
- Refactor [editor_imgui.go](editor_imgui.go): move `imguiVAO/VBO/EBO` out of
  package globals into the `UIWindow`, and pass display size into
  `ImGuiNewFrame`/`imguiRenderDrawData` instead of reading `WinWidth`/`WinHeight`.
  `imguiShaderProgram` and `imguiFontTexture` stay shared (they are shared GL
  objects, and the font atlas is shared across imgui contexts).
- `ImGuiNewFrame` currently reads `sdl.GetMouseState()`
  ([editor_imgui.go:91](editor_imgui.go#L91)), which is relative to the *focused*
  window. Replace with `sdl.GetGlobalMouseState()` minus `Win.GetPosition()` so each
  window gets correct local coordinates regardless of focus.
- **Event routing** in the main loop ([main.go:321-326](main.go#L321-L326)): read
  the `WindowID` off mouse/keyboard/window events; if it belongs to a secondary
  window, feed only that window's imgui context and skip
  `EditorHandleEvent`/`HandleEvent` entirely.
- **Gate the existing window handler.** [input.go:144-158](input.go#L144-L158)
  reacts to `WINDOWEVENT_SIZE_CHANGED` / `FOCUS_LOST` / `FOCUS_GAINED` with no
  window check — resizing the texture window would otherwise call
  `RefreshWindowSize()` and stomp the 3D viewport. Add `t.WindowID == mainWindowID`.
  Handle `WINDOWEVENT_CLOSE` on the secondary ID by closing just that window.
- **Frame order:** render main window → `Window.GLSwap()`; then for each open
  secondary window `Win.GLMakeCurrent(w.GL)`, `w.Ctx.SetCurrent()`, clear, new
  frame, `w.Draw()`, render, `GLSwap()`; finally restore the main GL context and
  main imgui context. Anything that touches world GL state must run while the
  main context is current — see the upload queue in Phase 3.
- **Add `runtime.LockOSThread()` in an `init()`.** It is missing today; the
  program survives on luck, and juggling two GL contexts from a goroutine the
  scheduler may migrate will not.

*Fallback:* if shared contexts misbehave on WSL2/X11, the entire Phase 4 UI is
plain imgui calls and can be hosted in a floating window inside the main window
with no changes to Phases 1–3. Build it so the UI body doesn't know which window
it's in.

## Phase 1 — Document model (`texture_doc.go`) — pure Go, no GL

```go
const (
    texMaxLayers = 8
    texMaxDim    = 1024
    texPaletteSz = 256
)

type TexBlend uint8 // BlendNormal, BlendMultiply, BlendAdd, BlendScreen

// TexPalette is the 256-entry colour table. Straight (non-premultiplied) alpha;
// slot 0 is transparent by convention.
type TexPalette [texPaletteSz]color.RGBA

type TexLayer struct {
    Name    string
    Visible bool
    Opacity float32 // 0..1
    Blend   TexBlend
    Idx     []uint8 // one palette index per texel, len == W*H
}

type TexDoc struct {
    Name    string
    W, H    int
    Pal     TexPalette // copy of the palette this document was authored against
    Layers  []TexLayer // index 0 = bottom
    Active  int
    Path    string     // .ftex path; "" until first save
    dirty   bool
}
```

- **`texMaxDim = 1024`.** Canvases are capped at 1024×1024, enforced in one place
  — a `validTexSize(w, h int) error` that `NewTexDoc`, `ReadTexDoc` and PNG import
  all call. The UI offers 64/128/256/512/1024 and nothing else, but the check
  lives in the model so a hand-edited or oversized `.ftex` can't smuggle a larger
  canvas past it. Import of a PNG larger than 1024² is rejected with a clear
  message rather than silently downscaled — a rescale changes the user's art and
  should be an explicit choice.
- `func (d *TexDoc) Flatten() *image.Paletted` — resolve each layer's indices
  through `d.Pal`, composite bottom-to-top source-over with per-layer opacity and
  blend mode in RGBA, then quantise each output texel back to the palette. Returns
  a paletted image so the PNG carries a real PLTE chunk and the on-disk texture is
  provably on-palette. This is the one piece of math worth testing hard.
  (`LoadTexture` already `draw.Draw`s whatever it decodes into an RGBA buffer
  ([texture.go:23-27](texture.go#L23-L27)), so a paletted PNG needs no loader change.)
- Because compositing quantises, a 50%-opacity layer over another colour lands on
  the *nearest palette entry*, not a true midpoint. That's inherent to indexed art
  and should be called out in the UI, not engineered around.
- **Undo** follows the `editor_undo.go` snapshot idea but scoped. At one byte per
  texel a full 8-layer snapshot at 1024² is 8 MB and a single-layer paint entry is
  1 MB. Paint ops record `{layerIdx, beforeIdx}` for the affected layer only;
  structural ops (add/delete/reorder/resize/palette-remap) record a full doc copy.
  Bound the ring by **bytes, not entries** — a 32 MB budget dropping oldest first,
  which is ~32 paint steps at the maximum canvas size and hundreds at 256².

## Phase 2 — File format (`texture_file.go`)

`<exe>/textures/<name>.ftex` — little-endian, `encoding/binary`:

```
0    [4]byte  magic "FTEX"
4    uint16   version = 1
6    uint16   flags (reserved, 0)
8    uint32   width   (1..1024)
12   uint32   height  (1..1024)
16   uint8    layerCount (1..8)
17   [3]byte  padding
20   [1024]   palette: 256 × RGBA8, the table this document was authored against
1044 ...      layers, bottom first
     per layer:
       uint16  nameLen, then name bytes (UTF-8)
       uint8   blend
       uint8   visible
       float32 opacity
       uint32  compressedLen
       bytes   zlib(palette indices, rows top-to-bottom, W*H bytes)
```

And `<exe>/textures/palette.pal` for the global table — magic `FPAL`, `uint16`
version, 256 × RGBA8.

- `WriteTexDoc` / `ReadTexDoc` / `WritePalette` / `ReadPalette`, all going through
  the existing `writeFileAtomic` ([editor_save.go:76](editor_save.go#L76)) so saves
  are atomic and keep one `.bak` generation for free.
- Reads validate magic, version, `layerCount <= 8`, `width`/`height` within
  `texMaxDim` and non-zero (checked *before* allocating, so a corrupt header
  can't provoke a huge allocation), and that each layer decompresses to exactly
  `W*H`. Reject a short read rather than pad it — a truncated file is a bug, not a
  partial image. Every index is inherently valid (a `uint8` always addresses a
  256-entry table), which is one class of corruption we get to not worry about.
- **Save writes two files:** the `.ftex` above *and* `<exe>/textures/<name>.png`,
  the flattened image via `png.Encode` on an `image.Paletted` (main.go currently
  blank-imports `image/png` for decode only; the encoder is in the same package).
  `.ftex` sits beside the `.png` and `ScanTextures` ignores it.

## Phase 3 — GPU sync (`texture.go` additions)

- `UpdateTextureFromRGBA(name string, img *image.RGBA)` — if `Textures[name]`
  exists, bind that handle and `gl.TexImage2D` into it (re-spec handles a size
  change too). Same handle ⇒ every existing instance updates. If absent,
  `gl.GenTextures`, set the same LINEAR/REPEAT params as
  [texture.go:33-36](texture.go#L33-L36), and insert into `Textures`.
  The GPU side stays plain RGBA8 — the flattened `image.Paletted` is expanded on
  the way up. An indexed upload plus a palette-LUT shader would be the retro-pure
  version, but it would mean touching the fragment shader and every sampler path
  for no visual gain.
- `RegisterTexture(name string)` — sets `textureNames[name] = "textures/"+name+".png"`,
  which is all requirement 7 needs.
- **Uploads are queued, not immediate.** The editor window pushes
  `{name, *image.RGBA}` onto `pendingTextureUploads`; the main loop drains it at
  the top of the frame while the main GL context is current. Shared contexts make
  a direct upload legal, but queuing also throttles a brush drag to one upload
  per frame and keeps all world-GL mutation in one place.
- **Live preview before save:** the same path runs on every stroke when the
  document is bound to a texture name, so painting updates the world in real
  time — requirement 6 covers editing, not just saving.

## Phase 4 — The editor UI (`texture_editor.go`, `texture_editor_ui.go`)

Entry point: a **"Textures"** button in the toolbar beside "Scenery"
([editor_ui.go:644](editor_ui.go#L644)). Opening creates the window if it isn't
open, otherwise raises it. Closing hides the window and keeps the document in
memory.

Panels inside the window:

- **Canvas** — one GL texture holding the flattened preview, drawn with
  `WindowDrawList().AddImageV` over an `InvisibleButton` region so hit-testing and
  drag work. Checkerboard behind it so alpha is visible. Zoom/pan with wheel and
  middle-drag.
- **Layers** — list of ≤8, bottom-first, each with visible toggle, opacity slider,
  blend combo, rename; buttons for add / duplicate / delete / move up / move down.
  Add is disabled at 8.
- **Palette** — a 16×16 swatch grid, which replaces the usual colour picker.
  Left-click sets the primary index, right-click the secondary; the selected slot
  and its index are shown numerically. Double-click opens `ColorEdit4` for that
  slot (**alpha included** — that's requirement 3 at authoring time), and the edit
  applies globally and live. Also here: load/save `.pal`, and the **"Rebuild all
  textures"** action described above.
- **Tools** — brush, eraser (paints the transparent slot), fill, line, rect,
  eyedropper (picks the index under the cursor). Brush size only — **no hardness or
  soft edges**, since an anti-aliased edge would quantise into speckle. Every tool
  writes a palette index, so nothing can introduce an off-palette colour in the
  first place.
- **File** — New (sizes 64/128/256/512/1024, default 512; 1024 is the hard cap),
  Open (picker listing `*.ftex`), **Import PNG** (one-layer doc from any existing
  texture, quantised to the palette on the way in, so the 12 PNGs already in
  `resources/textures/` are editable), Save, Save As.
- Painting converts mouse→texel through zoom/pan, stamps a circular brush of the
  active index into the active layer, marks the doc dirty, and re-flattens +
  uploads once at end of frame.

## Phase 5 — Transparency in the engine

- Cutout: already works, no change.
- Semi-transparent texels: the palette makes this cheap — build a 256-entry
  used-index histogram of the flattened image and check whether any *used* slot
  has `0 < a < 250`, rather than testing every texel. Record
  `textureHasAlpha[name] = true`. Extend
  `Geometry.IsTransparent()` ([wall.go:160](wall.go#L160)) to
  `(g.Alpha > 0 && g.Alpha < 1) || textureHasAlpha[g.Texture]`. That routes those
  surfaces into the existing sorted, blended pass with **no shader change**.
- Known limitation to note, not fix here: the shadow pass is depth-only, so a
  glass texture still casts a solid shadow. Worth its own NEXT.md entry.
- Ticks the existing `- [ ] Transparency in textures` item in
  [NEXT.md](NEXT.md).

## Phase 6 — Tests

Matching the existing pure-logic style (no GL in tests — see
[detail_presets_test.go](detail_presets_test.go), which redirects `executablePath`
to `t.TempDir()`):

- `texture_doc_test.go` — flatten math: opaque over opaque, 50% over, hidden layer
  skipped, layer opacity, each blend mode, single-layer passthrough, empty doc,
  and **every output texel is a palette colour** (the invariant that matters most).
- `texture_palette_test.go` — quantisation: an exact palette colour maps to its own
  index; nearest-colour picks the expected neighbour; ties resolve to the lowest
  index (determinism); the memo cache returns the same answers as the uncached
  path; alpha is matched before RGB; remapping a document between two palettes
  produces the expected indices.
- `texture_file_test.go` — round-trip 8 layers with awkward names/opacities *and*
  the embedded palette; `.pal` round-trip; reject bad magic, wrong version,
  `layerCount > 8`, a header claiming dimensions above `texMaxDim` or zero,
  truncated payload, wrong decompressed length, a short palette block.
- `texture_editor_test.go` — brush stamps the expected indices, clamps at canvas
  edges, eraser writes the transparent slot, undo restores prior indices, undo ring
  evicts oldest once past its byte budget, layer add/delete/reorder invariants,
  8-layer cap enforced, `NewTexDoc` and PNG import both reject sizes above 1024².
- `texture_alpha_test.go` — `hasAlpha` derived from used indices vs palette alpha,
  including the case where a transparent slot exists in the palette but is unused,
  and `IsTransparent` routing.

## Risks and decisions needed

1. **Save location.** Saves go to `<exe>/textures/`, same as every other asset.
   On the Windows target that directory persists across builds, so no Makefile
   change is needed. One consequence to be aware of: `make windows` copies
   `resources/*` over the deployed tree, so an edit to a texture that also exists
   in `resources/textures/` (e.g. `brick.png`) reverts on the next build, while
   newly created textures are untouched. Copy a texture back into
   `resources/textures/` by hand when you want an edit to become the repo version.
2. **Second window on WSL2/X11.** Shared GL contexts usually work, but if
   `GL_SHARE_WITH_CURRENT_CONTEXT` is refused we fall back to an in-main-window
   panel (Phases 1–3 unaffected).
3. **Missing `runtime.LockOSThread`** — should be added regardless of this feature.
4. **Memory.** Bounded by the 1024² cap and one byte per texel: worst case is
   8 layers × 1 MB = 8 MB of indices, a 4 MB flatten buffer, a 4 MB GPU preview and
   a 32 MB undo budget — ~48 MB for a maxed-out document, under 3 MB for a typical
   512² four-layer one. No warning needed; the cap does the work.
5. **Global vs per-texture palette** — see the palette section. Worth confirming
   before Phase 1; everything else in the plan is unaffected either way.
6. **The existing 12 PNGs are off-palette.** They'll be quantised on import,
   which will shift their colours. Deciding the default palette *from* those
   textures (sample them, cluster to 256) rather than inventing a ramp set would
   keep the current art looking like itself — worth doing as part of Phase 1's
   compiled-in default.

## Suggested order

Palette model + quantiser first (it's the base every other phase builds on), then
Phase 1 + 2 + 6 (pure Go, fully testable, no window needed) → Phase 3
(single-texture live update, verified by hot-reloading an existing PNG) → Phase 0
(window plumbing) → Phase 4 (UI) → Phase 5 (transparency routing).

## Out of scope

Mipmaps/filtering controls, normal/roughness map authoring, procedural
generators, tiling-seam tools, per-layer masks or transforms, texture atlasing,
dithered quantisation, palette-cycling animation, and indexed upload with a
palette-LUT shader.
