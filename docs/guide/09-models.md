# Models

A model is a `.fmdl` in `resources/models/` — a mesh, optionally an armature and
skin weights, and any number of named animation clips, all in one file. `.obj`
files also load, for meshes with no rig.

Nothing is shared between models. One file is the whole thing, which costs some
duplication and means a rig and a mesh can never drift apart.

Open the **Model** window from the editor strip.

## Getting geometry in

**Add** section, which appends rather than replaces:

- Primitives: `Box`, `Plane`, `Cylinder`, `Cone`, `Sphere`. **Segments** sets the
  radial resolution of the round ones. New geometry lands at the orbit pivot, or
  a short way in front of the eye when flying, snapped to the grid if snapping is
  on.
- **Merge Model...** and **Merge OBJ...** append another file's geometry to what
  you already have.

**File > Import OBJ...** replaces the document instead. **File > Export OBJ...**
writes one out.

## Shaping

| Key | |
| --- | --- |
| `1` `2` `3` `4` | vertex / edge / face / bone select |
| `G` `R` `C` | move / rotate / scale (`C` again toggles local axes) |
| `E` | extrude the selected faces along their shared normal, then drag |
| `E` again | re-aim that drag along the face normal instead of the world axes |
| `Shift+E` | always stack another extrude |
| `Z` | wireframe |
| `V` | orbit / fly camera |
| `F` | frame the model |
| `I` | inset — a border round the selected faces (outside pose mode) |
| `J` | join the selected faces into one |
| `M` / `Shift+M` | merge the selected points at their centre / onto the first picked |
| `X` | dissolve the selected points out of the lines they sit on |
| `Delete` | delete the selected faces or bones |
| `Ctrl+Z` / `Ctrl+Y` | undo / redo |
| `Tab` | hand focus back to the main window |

Right-click for the same operations as a menu.

In the **Face** section: **Extrude**, **Inset**, **Subdivide** (midpoint split —
the surface does not move), **Join**, **Delete**, and a per-face **Smooth** flag.
With one face selected the panel reports its normal and side count.

**Join** (`J`) makes the selected faces one face along the outline they share.
They must meet along edges and leave no hole; a joined face may be any shape, and
keeps the corners' UVs when every face in it had them. The points left along its
edges can be cleared up in vertex mode with **Dissolve** (`X`), which takes out
each selected point where only two edges meet, so the segments either side
become one; corners stay. In edge mode, a selected run of edges becomes one edge.
**Merge** (`M`) collapses the selected points into one at their centre, or onto
the first one picked with `Shift+M`, blending their bone weights. Both are on the
right-click menu in vertex and edge mode.

**Inset** (`I`) puts a border round each selected face, in that face's own plane,
mitred so it is the same width all the way round however long and thin the face
is — and it leaves the *middle* selected, because what you almost always want
next is to extrude it. Inset then extrude inward is a recess; inset then extrude
the ring outward is a raised frame. The face's own outline does not move, so
nothing sharing its corners is dragged along.

In the **Mesh** section: **Smooth Subdivide** is Catmull-Clark over the whole
mesh — the surface gets rounder each time, and it renumbers everything so your
selection is dropped. **Weld Duplicate Points** collapses every set of points
sharing one spot into a single point, so a mesh built from parts that only
touch (or imported with its seams split) becomes one connected surface; any
face left with no area is removed. It is one undo step. You rarely *need* it
just to move things — a move, rotate or scale already drags the points stacked
under the selection along with it — but after a weld those points are one
point for extrude, merge and dissolve too. There is also a whole-mesh smooth
toggle with a crease angle.

## UVs

A model wears **one** texture, chosen in the **Texture** section — or `(none)`,
which is what a new document starts as: an untextured mesh is painted with its
own normals, in the editor and in the level alike. See
[Textures](04-textures.md#no-texture).

**Texture UV** section, with faces selected. The offset, scale and rotation
controls are a transform re-applied to a captured base, not a compounding edit,
so dragging back to zero puts you back where you started. **Reset transform**
does the same.

| | |
| --- | --- |
| **Unwrap** | project each face onto its own plane, keeping world scale so texel density matches across faces |
| **Unwrap to 0..1** | the same, stretched to fill the texture exactly once |
| **Flip U / Flip V** | |
| **Corners** | per-corner U and V numbers, for what the transform cannot express |

To paint against a specific unwrap, open the texture editor beside this and turn
on its **UV layout** overlay.

## Bones

Press `4` for bone select. Bones are added, parented and named in the bone panel;
each vertex carries up to four weights.

A rig needs three things before it can animate: an armature, at least one vertex
weighted to a bone, and at least one keyframe. Missing any one and nothing moves.

The armature can belong to the model or to a **skeleton** shared with other
models — see [Skeletons](10-animation.md#skeletons). The **Skeleton** section
above the bone panel says which, and switches between them.

See [Animation](10-animation.md) for pose mode and clips.

## Saving and using

**Save** writes `models/<name>.fmdl`, plus `skeletons/<name>.fskl` when the
model links a skeleton. `*` in the title means unsaved.

A model reaches the world as an **attachment** on an entity type, or as a
[scenery](14-scenery-brush.md) prop. Neither reads the `.fmdl` again until asked:
after editing a model, press **Reload Models** in the entity panel, or **Reload**
on a scenery prop, to pick the change up in the level open beside you.

A model loads **indexed**: corners that agree in everything (position, UV,
normal, bone weights) share one vertex, and the triangles index into that pool.
Corners that differ in anything stay separate, so UV seams and hard edges look
exactly as you made them. It is about half the vertex data of the old layout.
`FOUNDRY_NANITE_INDEXED=0` in the environment loads models the old way, for
comparing the two.

## Dense meshes: glTF and Nanite cooking

A scan, a sculpt or a rock with a hundred thousand triangles does not belong in
the model editor. Bring it in as **glTF** (`.gltf` with its `.bin`, or a single
`.glb`) under `resources/models/`, and **cook** it into a Nanite mesh:

- **Window > Cook Nanite Meshes** cooks every glTF under `resources/` that has
  changed since its last cook.
- `foundry nanite cook models/rock.glb` does the same from a terminal (`-all`
  for every glTF, `-force` to cook even an up-to-date one, `-o` to name the
  output). `foundry nanite inspect models/rock.fnm` says what a cooked mesh holds.

The cook writes `models/rock.fnm` beside the source. It keeps the file's own
normals and its **material slots** (each glTF material is a slot; its base-colour
texture, reduced to the bare name `rock_albedo`, is the slot's texture). It welds
the vertices, cuts the triangles into clusters of about 128 for the GPU to cull,
and builds every **level of detail** below that: groups of neighbouring clusters
simplified to half their triangles, again and again, down to a few hundred.
Simplifying keeps where two groups meet exactly where it was, so any mix of
levels joins without a crack, and it weighs the normals and UVs as well as the
shape, so a coarser level does not smear the shading. It also keeps one middle
level, the **fallback** mesh, for the editor's picking, and bakes the distance
field for live GI from that. It records a hash of the source, so a cook of an
unchanged source is skipped and a changed one is redone. `foundry nanite
inspect` lists the levels: how many triangles each holds and how far it strays
from the full detail.

A `.fmdl` or `.obj` can be cooked too: on a scenery prop that uses one, the
**Nanite** group's **Cook and use** button cooks it beside itself and points the
prop at the result. A cooked mesh is static — no rig, no animation — and is
placed as [scenery](14-scenery-brush.md#nanite-meshes).

glTF import reads triangles (strips and fans included), positions, normals, the
first UV set, node transforms and materials. Skins, morph targets, animations,
sparse accessors and extensions are not read.

## Model editor camera

| | |
| --- | --- |
| Middle- or right-drag | orbit (mouse-look in fly mode) |
| `Shift`+middle-drag | pan |
| Wheel | zoom |
| `V` | switch orbit / fly |
| `F` | frame |
