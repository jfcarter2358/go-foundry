# Shaping surfaces

A brush is a mesh — a vertex list and a face list — so it is shaped with the same
gestures a model is. There are two places to do that, and the difference between
them is the whole chapter:

| | | |
| --- | --- | --- |
| **Edit mode** | `Tab` | shapes the **solid**. Everything you do here moves where the player stops |
| **Detail mode** | `Ctrl+Shift+click` | shapes the **surface drawn over** the solid. Nothing you do here touches collision |

Use edit mode for the shape of the room. Use detail mode for the ledge running
along the wall, the reveal around the window, the panelling, the moulding, the
lip on the step — everything a player should see and nothing they should catch
on.

## Edit mode: shaping the solid

`Tab` goes in, `1` `2` `3` pick points, edges and faces, and `Tab` comes back out
with the brush selected. Three tools act on the face selection:

| Key | |
| --- | --- |
| `E` | **extrude** — slide the face along its own normal (`Shift+E` pulls it back in) |
| `I` | **inset** — draw the face's corners toward its centre, in its own plane (`Shift+I` widens) |
| `B` | **subdivide** — split the face into quadrants at its edge midpoints |
| `J` | **join** — make the selected faces one face, along the outline they share |
| `X` | **dissolve** — take the selected points out of the lines they sit on |
| `M` | **merge** the selected points into one at their centre (`Shift+M`: onto the first picked) |
| `G` `R` `C` | move, turn, scale whatever is selected |

All three keep the brush **convex**, which it must be
([Building levels](02-building-levels.md) says why). That is also why the list is
short: extrude here slides the face's plane rather than growing a bump off it,
because a bump is concave, and inset here draws the face's own corners in rather
than building a border inside them, because a border is a face you would then
push out of plane. In an id-lineage editor a bump on a wall is a second brush,
and it still is here — or it is detail.

Subdivide is the one tool that is the *same* in both modes, because splitting a
face at its edge midpoints changes no shape at all: the quadrants are still in
the face's own plane and still on the hull. A neighbour that shared a cut edge
takes the new midpoint into its own outline as it goes, so a subdivided wall
grows no cracks along its seams.

**Join, dissolve and merge** are the same in both modes too, and undo what
subdivide does. `J` makes the selected faces one face: they must meet along
edges, leave no hole, and — because a brush face is flat and convex — lie flat
together as a convex shape. The points the join leaves along its edges are
dissolved with `X`, which takes out any selected point where only two edges meet,
so the two segments either side become one; corners stay. In edge mode, select a
run of edges and `X` makes it one edge. Subdivide a wall, join the quarters, and
dissolve the midpoints, and it is the box it started as. `M` merges the selected
points into one, at their centre or (`Shift+M`) onto the first one picked. Points
on two different brushes each stay their own brush's but land on the same spot,
which is how two solids are made to meet exactly.

Dragging a single corner, or merging two, is the edit that can break convexity. The editor
warns rather than refuses, in the status line and in the brush's properties, so
you can pass through an invalid shape on the way to a valid one; compiling
refuses outright and names the brush.

## Detail mode: shaping what you see

**`Ctrl+Shift+click` a brush.** The first time, its detail mesh is seeded as a
copy of the solid's own faces, so nothing changes on screen. From then on the
brush is *drawn* from that mesh, and two overlays come up with it:

- **every face of the detail mesh is outlined in black**, the way the model
  editor outlines a mesh's edges. Four coplanar quarters of a subdivided wall
  are one flat surface to look at, so without the seams there is no telling what
  a click is about to land on. They are depth-tested, so you see the near side
  of the mesh and not through it;
- **the solid underneath is drawn as a dim wire cage**, so you can always see
  where the player will actually stop.

Clicks in here reach that one brush and nothing else — you cannot move a solid by
mis-aiming — and the tools are the **model editor's**, not edit mode's:

| Key | |
| --- | --- |
| `E` | **extrude** — the real one: the face is duplicated and the gap walled in with sides (`Shift+E` presses it in instead) |
| `I` | **inset** — a mitred border round the face, in its own plane, leaving the middle selected |
| `B` | **subdivide** the selected faces at their edge midpoints |
| `Shift+B` | **smooth** the whole detail mesh — one round of Catmull-Clark |
| `J` | **join** the selected faces into one, if they lie flat together |
| `M` / `Shift+M` | **merge** the selected points at their centre / onto the first picked |
| `X` | with points selected, **dissolve** them out of their lines |
| `Delete` / `X` | delete the selected faces |
| `1` `2` `3` | points, edges, faces of the detail mesh |
| `G` `R` `C` | move, turn, scale the selection, anywhere, in any direction |
| `Tab` / `Esc` | leave, with the brush selected |

`Ctrl+Shift+click` another brush to go straight to it, or the same one again to
come out. The entity editor's viewport works the same way, on a type's own
brushes.

There is no convexity rule in here, no warning, and no limit on what the mesh can
become: it may be open, concave, disconnected, or a single floating quad. It is a
surface, not a solid.

### How to make things

**A ledge along a wall.** `Ctrl+Shift+click` the wall, `3`, click the inward
face, `B` to subdivide it, click the strip you want, `E` to pull it out.

**A window reveal.** Click the face, `I` for a border, then `Shift+E` to press
the middle in — the walls of the reveal are the sides the extrude leaves behind.
The window *opening* is a hole in the solid, which is edit mode's job or a second
brush's; detail cannot make a hole you can walk through, because it cannot make a
hole in collision at all.

**Panelling.** Subdivide a wall into a grid, select alternating faces with
`Ctrl+click`, `I` then `Shift+E` to press each one in a few centimetres behind
its own border.

**A raised frame.** `I`, then `E` on the border rather than the middle: select
the ring quads and push them out.

**A rounded pillar.** `Shift+B` on a cylinder brush's detail mesh, once or twice.
The solid stays a twelve-sided prism — cheap to collide against — and what you
see is round.

### What it costs, and what it does not

- **Collision, always the solid.** A body meets the hull. So does a particle, so
  does the level's seal, and so do the cells the compiler cuts the world into.
- **Drawing, always the detail.** Shadows, picking and raycasts follow what is
  drawn, so a crosshair lands on the ledge it is pointing at even though a
  shoulder would pass through it.
- **The compiler treats it as detail proper.** Detail faces are drawn and lit
  like any others, and take no part in choosing the BSP's splitting planes or in
  computing visibility — the partition of a level is exactly the partition it
  would have had with no detail in it at all. This is the same bargain Quake's
  detail brushes make: detail costs draws, not tree.
- **Nothing is free.** Every face you add is a face to draw. A wall subdivided
  four times is 256 of them.

### Keeping the two in step

The detail mesh does not follow later edits to the solid. Move the brush as a
whole — in object mode, with the gizmo — and the detail goes with it; reshape the
*solid's* faces in edit mode and the detail stays where it was, because there is
no way to guess which of your carved geometry was meant to follow.

The brush's **Detail** properties group is where that is fixed:

| | |
| --- | --- |
| **Shape detail...** | the same thing `Ctrl+Shift+click` does |
| **Re-seed from solid** | throw the detail away and copy the solid's faces again |
| **Remove detail** | back to being drawn as the solid it is |

So the order of work is: get the solid right first, then carve it.

## Smooth shading

Both meshes take the per-face **Smooth** flag, which welds normals along shared
edges so a run of faces reads as one curved surface. It is a shading trick and
changes no geometry — where `Shift+B` in detail mode actually moves the corners.
See [Smooth shading](05-smoothing.md).
