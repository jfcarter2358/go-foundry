# materials/

A `.fmat` is a named bundle of shader effects and their settings, which anything
that can wear a texture can wear instead: a brush face, a compiled surface, a
model or entity attachment, a sprite, one layer of a particle effect, or the sky.

Naming one **overlays** it. A material that says nothing but `uv_scroll` slides
the texture of whatever wears it and leaves that surface's own texture, alpha,
tint and smoothing exactly as the level set them — which is what lets one
`conveyor.fmat` drive nine belts of five different art.

```yaml
kind: surface          # surface | sprite | particle | sky
texture: hull_glass    # optional: the albedo the material supplies
doc: A note for whoever opens this next.
effects:
  - effect: unlit
  - effect: uv_scroll
    rate: [0, 0.25]
```

A parameter left out reads as its default, so a file says what was changed and
nothing else. Saving from the **Material** editor writes it back the same way.

The files here are the engine's own effects, one per document, meant to be
opened and pulled around. Every one of them was a field on some object before
materials existed and still is underneath — see `docs/guide/28-materials.md`.
