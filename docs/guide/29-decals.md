# Decals

A decal is a picture laid **on** a surface the level already has — a poster, a
stencilled bay number, a scorch mark, a bullet hole, a blood splatter.

Every other way of getting a picture onto a wall in Foundry changes the wall: its
texture, a second texture blended over it, an atlas island painted into the
face's own UVs. That is right for what a surface *is*, and wrong for what
happens to it. A bullet hole has nothing to do with how the wall was textured, it
is a few centimetres across where the wall is eight metres, and it often lands
right on the seam between two faces.

So a decal is not a surface property. It is a **box placed in the world**, and
what it draws is the level's own geometry clipped to that box and re-textured by
projecting it along the box's axis.

That is the one thing worth understanding before you place any, because it is
where every good and bad behaviour of decals comes from:

- A decal **follows the shape it lands on**. On the corner where two walls meet
  it bends around the corner. On a displaced terrain face it follows the bumps.
  On a staircase it steps.
- A decal is **clipped by everything in front of it**, for free. A bullet hole
  behind a crate is hidden by the crate.
- A decal on a **lift, a door or a dropship's hull rides it**, because its mesh is
  built out of that brush's own triangles and drawn under that brush's own
  matrix. A decal on a chunk that streams out goes with it.
- And a decal **only exists where there is geometry**. Place one in mid-air and
  there is simply nothing to see.

## Placing one

**Explorer → Add → Decal → Place By Clicking**, then click a surface.

The click is doing more than picking a position. The decal is **oriented by the
face you clicked**, so it lies flat on that face. This is the only placement in
the editor that reads the surface as well as the point, and it is why placing a
decal is usually the whole job — the rotation you would otherwise have to dial in
by hand is already right.

Set the ID and texture in the Add menu before you click, if you want them; both
can be changed afterwards.

Three textures ship for the three blend modes:
`decal_bullet_hole`, `decal_scorch` and `decal_splatter`.

## When a decal is invisible

This is the one object in the editor that can be perfectly well authored and
still draw nothing at all, so it is worth knowing the three causes up front. The
Explorer says **(projects onto nothing)** on the row whenever one of them
applies, and the Projection group in the panel says the same thing at more
length.

**It is facing the wrong way.** A decal projects along its local **+Z**, which
points *out* of the surface it lies on. One facing into its wall projects onto
the back of that wall's near face, which is culled, and you see nothing. Fix:
line the view up with the surface you want it on and press **Face The Camera**
in the Transform group.

**It is in mid-air.** There is no geometry inside the box. Drag it onto a
surface; the selection cage shows you where the box actually is.

**Max angle is too low for the surface.** See below.

## The projection group

Three fields, collapsed by default because a decal placed by clicking rarely
needs any of them. Each is the answer to one specific way a decal can look wrong.

**Depth** — how far along its facing the projection reaches, measured either side
of the position. Keep it small. It is the field that decides whether a decal
aimed at a wall *also* prints itself on the crate standing a metre behind the
wall, which reads as a rendering fault rather than a mistake. Raise it
deliberately to wrap a mark around a corner or over a step.

**Max angle** — how far a surface's normal may lean away from the decal's facing
and still be painted, in degrees (75 by default). Past that angle the projection
is so oblique that the texture stretches into streaks metres long. A decal that
stops at the corner reads as a decal; a smear does not. Lower this to stop a wall
decal creeping onto the floor; raise it to let one wrap further around a curve.

**Push** — lifts the mesh off its surface along the surface's own normal. The
renderer already offsets decals in depth, which handles every surface you see
face-on; raise this only if a decal still shimmers where it is seen almost
edge-on. Negative sinks it in.

## Blend modes

The mode decides how the decal's texels combine with the surface under them, and
picking wrong is the commonest way a decal ends up looking like a sticker.

**alpha** (the default) lays the texture over the surface, its own alpha deciding
how much of each shows. Right for posters, signs, bullet holes, wet splatter —
anything that is genuinely a picture *on top of* the wall.

**multiply** darkens the surface by the texture instead of covering it. This is
what most of the marks a game leaves actually want. Scorch, soot, oil, dirt,
dried blood and tyre rub are all a *darkening*: an alpha-blended grey blob is the
same grey blob on a white wall and on a black floor, where a multiplied one is a
smudge on both. One texture, every surface — which is most of the economy of
decals.

In a multiply texture, **white leaves the surface untouched** and black blacks it
out, so the art is a mask that fades to white at its edges. Its alpha still works
as you would expect — a half-transparent texel darkens half as much, and lowering
a multiply decal's `alpha` or letting its lifetime run out fades the mark away
rather than dimming it to nothing.

**additive** adds light and never darkens. For a glowing rune, a holographic
stencil, a hot ember scar.

## Tint, alpha and order

**Tint** multiplies the texture. It earns more on a decal than anywhere else: one
grey scorch texture tinted eight ways is eight different marks, where eight
copies of the texture is eight textures to load.

**Alpha** scales the texture's own alpha, and is what a script should fade
through.

**Order** breaks the tie between two decals that overlap — higher lies on top.
Equal orders draw in the order they were placed, so "the one I put down last is
on top" is what you get without thinking about it.

**Unlit** should stay off for almost everything. A decal *is* part of the surface
it lies on; one that stayed bright in a dark room reads as a light source. Turn it
on for the emissive few.

## Materials

A decal wears a **surface** `.fmat` (see [Materials](28-materials.md)), not a
sprite one, because its mesh is ordinary world geometry. Every surface effect
therefore works on a decal — a scroll, a normal map, a cutout threshold — and a
material naming a texture supplies the decal's.

There is also `poster.fmat`, which is *not* a decal: it is a sprite that does not
billboard. Use it for a picture that hangs in space, like a floating panel; use a
decal for anything lying on a surface. (It was called `decal.fmat` before decals
were real.)

## From a script

`decal` places one at a point and works out the plane itself:

```
decal("decal_bullet_hole", at)
decal("decal_bullet_hole", at, {"size": 0.08})
decal("decal_scorch", @crate, {"blend": "multiply", "size": 1.2, "life": 20})
```

The verb takes a **point**, not an orientation, because a point is what a script
has. `hitscan`'s `at` is where the round landed; the engine finds the surface
there and orients the mark flat against it. Pass `normal` yourself if you already
have one, and it skips the search.

It returns a ref to the decal, or `null` when there was no surface at that point
— so a script firing at the sky simply leaves no hole, with nothing to check.

A typical impact handler, beside the effect it already spawns:

```
def on_fire(:primary_fire:) -> null
    hitscan(:shot:)
end

def on_shot(:shot:, hit, at vec) -> null
    if hit != null
        hit <- (:damage:, @self, 10)
    else
        spawn_effect("bullet_impact", at)
        decal("decal_bullet_hole", at, {"size": 0.07, "life": 45})
    end
end
```

The options: `size`, `depth`, `normal`, `roll`, `material`, `blend`, `tint`,
`alpha`, `life`, `order`, `max_angle`, `unlit` and `permanent`. `F1` in the
editor lists them with their one-line summaries, generated from the engine's own
table.

Two of them are worth a note.

`life` is how many seconds the mark lasts; it fades out over its last second and
a half rather than blinking off. Without it the mark stays until the cap
recycles it.

`permanent` opts a mark out of that cap and into the save game. Use it for the
few a script places deliberately — a stain that is part of the level's story —
not for per-impact marks.

### The cap

Script-placed decals are capped (192), oldest recycled. A decal costs a mesh
clipped out of the world's triangles, and a level that kept every bullet hole a
long firefight produced would grow without bound. Decals you placed in the editor
are never touched by the cap, however many there are.

### Properties

`@id.prop.` reaches `position`, `rotation`, `size`, `texture`, `material`,
`tint`, `alpha`, `blend`, `order`, `lifetime`, `visible`, `unlit` and the
read-only `faces`.

One thing to know about cost. Writing `position`, `rotation` or `size`
**re-clips the mesh** out of the world on the next frame. Writing `alpha`,
`tint`, `texture`, `material` or `visible` does not, and is free. So fade a decal
through `alpha`, and hide one through `visible` rather than by moving it away.

## In a level file

```yaml
decals:
  - id: bay_4
    position: {x: 12, y: 2.5, z: -8}
    rotation: {x: 0, y: 1.5708, z: 0}
    texture: sf_stencil_4
    width: 1.2
    height: 0.6
  - id: blast_mark
    position: {x: 12, y: 0.02, z: -6}
    rotation: {x: -1.5708, y: 0, z: 0}
    texture: decal_scorch
    blend: multiply
    width: 3
    height: 3
    depth: 0.4
    tint: {r: 0.8, g: 0.75, b: 0.7}
```

`rotation` is in radians and aims the projection: the decal's local +Z is the way
it faces out of its surface. You will rarely type one — place the decal by
clicking and the editor writes it.

Everything else is optional and defaults as the panel shows, so a decal nobody
has fiddled with stays a five-line entry.

## Cost

A decal is one draw call per **space** it touches: one for the static brushes of
each chunk it reaches, one more for each dynamic brush, one for each chunk of a
compiled level's surfaces. A decal on a single wall is one call. They are blended
and not batched, so they are cheap in the hundreds and not in the thousands —
which is exactly what the script cap is sized for.

Rebuilding is what costs, not drawing: a rebuild walks every triangle whose brush
overlaps the box. It happens when the decal moves or the geometry under it is
edited, and the engine spreads a queue of them over a few frames, so a level
opening with two hundred decals does not stall on the first frame.

In a compiled level a decal takes the **baked light** of the surface under it,
interpolated, so it is lit exactly as that wall is rather than sitting on it as a
brightly lit patch.

## See also

- [Materials](28-materials.md) — the `.fmat` a decal wears
- [Sprites and particles](15-sprites-and-particles.md) — for a picture that is
  not on a surface
- [Textures](04-textures.md) — painting the art, including its alpha
- [Scripting](18-scripting.md) — flang, events and properties
