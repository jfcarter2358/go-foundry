# Shaping surfaces

Every wall, floor and ceiling is flat until you shape it. When you do, it grows a
**surface mesh** — a web of points and faces you edit with the same gestures the
model editor uses. This is how terrain, steps, pits, recesses, bevels and holes
are all made.

Select a face in the 3D view and press **Shape Surface** in its properties, or
pick it from the face's right-click menu. `Esc` leaves.

## The keys

While shaping, the select modes retarget from the sector to the surface itself.

| Key | |
| --- | --- |
| `1` `2` `3` | the surface's points, edges, faces |
| Left click | pick; `Ctrl+click` adds |
| Drag an arm | move — **green is depth**, red is U, blue is V |
| `E` | extrude the selected faces, then drag the depth arm |
| `I` | inset |
| `B` | subdivide |
| `C` | connect two selected points, splitting the face |
| `H` | open the selected faces as a hole, or close them again |
| `Ctrl+A` | select all |
| `Delete` | delete the selected faces |
| `Esc` | done |

Camera controls are unchanged — fly around while you shape.

## How to make things

**A hill.** Select the floor, **Shape Surface**, `3` for faces, `B` a couple of times to
subdivide, `1` for points, then drag points up the green arm. Turn on **Smooth
Shading** afterwards so it reads as a curve rather than facets.

**A recess.** Pick a face, `I` to inset, then `E` and drag inward. Because
extrude is a topological operation you can apply it to its own output — put a
bump inside the recess by picking its floor and doing it again.

**A hole in a wall.** Pick the face where the window goes, `I` to inset a frame
around it, then `H` on the inner face. That face stops drawing and the player can
walk or shoot through it.

**Steps.** Subdivide a floor into strips, then raise each strip with the depth
arm.

## What collides with what

This is the one rule to remember, and it is the same one procedural detail
follows:

- **Floors and ceilings collide with the mesh.** The height query reads the same
  triangles you can see, so a bump you can see is a bump you walk up.
- **A wall's collision stays flat.** What stops you is still the wall's line on
  the 2D map. The mesh moves what you *see*.
- **Holes are real on walls.** A `Hole` face is punched through the wall's
  collision as a pass-through window.

So: bumps you walk into, holes you walk through.

## Points ride the sector

A surface mesh point is stored as a position in the face's own space — along and
down for a wall, chunk-local X/Z for a floor — plus a depth. It is not a free 3D
position. That means the mesh rides the surface: raise the ceiling and a wall's
mesh stretches with it, drag a corner on the 2D map and the shape follows.

Points on the face's outline belong to the *sector*, not to the mesh. You cannot
drag them across the surface, and they are re-derived whenever the outline
changes. When you split a wall or insert a corner, the interior of your mesh is
carried across rather than thrown away.

## Smooth shading

In the same properties group. Ticking **Smooth Shading** welds normals along
shared edges so neighbouring faces read as one curved surface. **Crease Angle**
is the cutoff — edges sharper than it stay sharp.

It works on displaced procedural detail too, which uses the analytic derivative
of the displacement rather than welded normals.
