# fonts

`.ttf` files the UI loads by name: a widget asking for font `inter` reads
`inter.ttf` from this folder.

Nothing is checked in yet, because which typeface ships is a licensing decision
rather than an engineering one. Until one is added, every label falls back to
the compiled-in bitmap face in `internal/uifont/uifont_fallback.go` — which is
deliberately crude, so a missing font reads as a missing font rather than as a
rendering bug.

Dropping a `.ttf` in `internal/uifont/testdata/` additionally switches on the
opentype tests in that package, which are skipped while it is empty.
