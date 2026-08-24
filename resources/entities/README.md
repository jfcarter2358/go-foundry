# Entity definitions

A `.yaml` file here describes one *type* of entity: its models, its bounds, its
collision flags, its default attributes and tags, and the script that drives it.
A level then places instances of it, storing only where each one is and whatever
it overrides — see `entity_def.go`.

Nothing ships here, and nothing has to. The editor's Entities panel places a
**blank entity**: a box with an ID, no model, and defaults filled in on the
spot, written out in full in the level file. That is the right shape for a thing
one level needs once.

Write a definition when a *second* level wants the same thing, or when "every
barrel gains 10 health" should be one edit rather than a search across every
level that placed one. Definitions found here show up in the panel's Type
picker, under the blank entry.
