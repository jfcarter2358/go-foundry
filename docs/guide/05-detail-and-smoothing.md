# Procedural detail

Detail is per-face roughness the GPU generates: displacement that breaks a flat
plane into rock, plus optional scattered stones on top. It costs nothing in the
level file and nothing to author beyond a few sliders, and it is the right tool
for cliff faces, rubble and any large surface that would otherwise read as a
plane.

It is not a replacement for [surface meshes](03-shaping-surfaces.md). Detail is
noise you tune; a surface mesh is a shape you make. Use detail for texture,
meshes for anything the player has to navigate.

## Turning it on

Select a wall, floor or ceiling, open the **Detail** group in its properties, and
tick **Enabled**.

Start from a preset: `rock`, `cliff`, `gravel`, `waves`. Selecting one copies its
numbers in and every field stays editable afterwards. Type a name and press
**Save Preset** to keep your own; user presets are written beside the executable
and appear in the same list. Built-ins cannot be deleted.

**Seed** fixes the random draw. Two faces with the same settings and different
seeds look different; the same seed reproduces exactly. **Randomize** rolls a new
one.

## Displacement

Three **wave** layers plus a **noise** layer, each independently switchable.

| Field | |
| --- | --- |
| Amplitude | how far the surface moves, in world units |
| Frequency | how many ripples per unit |
| Rotation | which way the wave runs, in degrees |
| Tessellation | how finely that layer subdivides the patch |
| Octaves | noise only — how many scales are summed |

Tessellation is per-patch, so the active layers share the highest level any of
them asks for. The panel reports the **effective tessellation** so you can see
what you are actually paying for.

**Edge Taper** fades the displacement to zero at the face's boundary. Without it,
a displaced floor and the wall meeting it come apart at the seam. Raise it until
the join closes.

## Scatter

**Scatter meshes** stamps small rocks over the face, instanced on the GPU.

| Field | |
| --- | --- |
| Rock Texture | which texture the stones take |
| Density | how many |
| Spacing | minimum distance between them |
| Scale | how big |
| Jitter | how irregularly they are placed |
| Draw Range | how far from the camera they are drawn |

Scattered rock casts and receives shadows.

## What collides

**Nothing.** Detail is visual. Collision on a displaced floor is still the flat
floor, and a displaced wall stops you at its 2D line. That is deliberate: it
keeps the collision cost of a cliff face the same as a wall, and it means you can
turn detail up without changing how the level plays.

If the player has to walk on it, shape a surface mesh instead.

## Smooth shading

Its own group, on the same face.

**Smooth Shading** welds normals along shared edges so neighbouring faces read as
one curved surface rather than as facets. **Crease Angle** is the cutoff — edges
sharper than it stay sharp.

On a displaced face the engine uses the analytic derivative of the displacement
rather than welded normals, so a detailed cliff smooths correctly without you
subdividing anything.
