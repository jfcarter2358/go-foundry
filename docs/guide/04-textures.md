# Textures

A world texture is a `.png` in `resources/textures/`. Drop one in and it appears
in every texture picker in the editor — nothing has to be registered.

The **texture editor** (toolbar: `Textures`) edits `.ftex` documents: layered,
palette-indexed, optionally animated, with a height channel for relief. Saving
one writes both the `.ftex` and the `.png` the engine actually samples.

## The palette is global

There is one 256-colour palette for the whole project. A layer stores a palette
index per texel, not a colour. Editing slot 12 recolours every texel on slot 12
in every document and in the running world, live, as you drag.

- Click a swatch to set the primary colour, right-click for the secondary,
  double-click to edit that slot.
- Slot 0 is transparent. It is what lets a layer below show through.
- **Rebuild All Textures** re-exports every `.ftex` on disk with the current
  palette. Art already saved keeps its old colours until you do this.
- Opening a `.ftex` whose embedded palette differs from the current one asks
  whether to adopt the file's palette globally or remap the art onto the current
  one.

## Tools

`Brush`, `Eraser`, `Fill`, `Line`, `Rect`, `Eyedropper`, `Spray`.

Brush size is a pixel count with no hardness — a soft edge quantised into 256
colours turns into speckle. Spray has a density (dabs per frame while held).

| | |
| --- | --- |
| Left click / drag | paint |
| Middle-drag | pan |
| Wheel | zoom |
| `Ctrl+Z` / `Ctrl+Y` | undo / redo |

## Layers

Up to 8 per document. Each has a name, a visibility toggle, an opacity and a
blend mode: `Normal`, `Multiply`, `Add`, `Screen`. Reorder with Up/Down.

## Frames — animated textures

Up to 32. **Add Frame**, **Duplicate Frame**, **Delete Frame**, and `< Move` /
`Move >` to reorder. Each frame has its own layer stack.

An animated texture plays one full cycle per second, so the frame rate is however
many frames you gave it. Anything that samples the texture animates — walls,
floors, models, detail patches — and so does its normal map.

## Bump maps

The **Channel** switch at the top of the tools panel flips the whole editor
between the colour channel and the **bump** channel. The bump channel is a second
layer stack painted with the same tools, but each texel holds a *height* rather
than a palette index: lighter is higher.

The engine derives a tangent-space normal map from the height field at save time
and writes it beside the albedo as `<name>_normal.png`. From then on, anything
using that texture has relief — walls, floors, ceilings, surface meshes, models,
detail patches. There is no per-surface toggle and nothing in a level file
mentions it. Paint height into `brick.ftex` once and every brick in the game
lights.

Notes:

- Height 0 means "nothing here" and lets the layers below show through, the same
  way palette slot 0 does on the colour channel. The eraser writes it.
- **Strength** is baked into the derived map, not sent to the shader.
- **Show normal map** previews what the engine will actually sample.
- Gradients are taken with wrapped sampling, so tiling textures do not grow a
  false ridge down the seam.
- Water, biome foliage, ground patches, sprites and particles do not sample
  normal maps — they get their normals another way.

## Sizes and importing

New documents are square: 64, 128, 256, 512 or 1024.

**Import PNG...** brings an existing image in as a single-layer document,
quantised to the current palette.

## The UV layout overlay

With a model open in the model editor, the texture canvas can draw that model's
UV layout over the art (`UV layout (n faces)` under the canvas). That is how you
paint to a specific unwrap rather than guessing.

## Naming

Textures are referenced by name without an extension. `hull_plate.png` is
`hull_plate` everywhere — in a level file, in a picker, in
`@wall.prop.texture = "hull_plate"`. A `_normal` suffix is reserved: those files
never show up as textures in their own right.
