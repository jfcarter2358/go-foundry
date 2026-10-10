# Smooth shading

A flat-shaded face has one plane and one normal, and the faces around it meet
it at visible creases. That is right for a wall and wrong for a barrel, a dome or
a hillside built out of a dozen small faces, which should read as one curved
surface.

**Smooth shading** makes them read that way, and it is on by default. Every
face of a brush, and every model, blends its lighting into its neighbours
across edges shallower than its **Crease Angle**. Edges sharper than that stay
sharp. The default of 60° leaves a box exactly as it was, because its edges are
all 90°, but rounds an eight-sided cylinder (45°).

## The player's setting

Smooth Lighting is a player setting: the game's settings screen can turn it off
with `@level.prop.smooth_lighting` (see [UI screens](17-ui-screens.md#a-settings-screen)).
The engine saves the choice to `graphics.yaml`. It applies to compiled
levels in a release build too, because the compiler bakes both answers.

## Per-face controls

Select a face (Tab into edit mode, press `3`, click it) and open **Shading** in
its properties.

**Crease Angle** is the cutoff. Neighbouring faces meeting at a shallower angle
than this have their normals welded together and shade as one surface; edges
sharper than it stay sharp. Raise it to smooth harder corners, lower it to keep
more of them crisp. The setting is per face, so a cylinder can have its curved
sides smoothed and its flat caps left alone.

**Always Smooth** keeps a face smooth even when the player turns Smooth Lighting
off, for surfaces that should never look faceted. To do a run of faces at once,
select them all and use **Smooth** from the right-click menu.

## What it does and does not change

Smoothing is shading only. It welds normals; it moves no geometry. The
silhouette of a smoothed eight-sided cylinder is still an octagon, collision
still resolves against the real faces, and the level file is one flag bigger.

If you want the shape itself to be rounder, add geometry: select the faces and
subdivide (`B`), then move the new corners. See
[Shaping surfaces](03-shaping-surfaces.md).

## There is no procedural detail

Earlier versions of the engine could give a face GPU-generated roughness —
tessellated displacement plus scattered stones, tuned with a panel of sliders.
That is gone.

It was a layer riding on top of a flat face, which made sense when a level was
built from sectors whose faces were flat by construction. A brush is a solid you
shape directly: roughness in a cliff is corners you moved, and the collision, the
compiled geometry and the silhouette all agree about them because there is only
one shape rather than a real one and a drawn one.

For ground cover — grass, rock, flowers — see [Scatter](06-scatter.md), which is
a volume you place rather than a property of a surface.
