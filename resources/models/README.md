# Models

A `.fmdl` here is one model document: a vertex pool, n-gon faces, one texture,
and optionally an armature and its animation clips. It is what the model editor
opens and saves, what an entity's model attachments name (`path: models/x.fmdl`),
and what a piece of scenery places. See `internal/model/model_file.go`.

## What ships

`dropship.fmdl` and `dropship_glass.fmdl` — the hull and canopy of the shuttle
in `entities/dropship.yaml`. Two documents rather than one because a model wears
a single texture, and glass is not plating; the entity draws the second at low
alpha over the first.

Both are **generated**: `go run ./cmd/genmodels`, or `make models`. Edit the
generator, not the YAML — a hull assembled out of thirty tapered boxes is a
thing a program never gets wrong and a hand never gets right twice, and the
generator's own test checks that every face still winds outwards. Opening one in
the model editor and saving is fine, but the next generator run overwrites it.

Anything authored by hand in the editor is ordinary content and belongs here
beside them, generator or no generator.
