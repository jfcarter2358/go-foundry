package world

// chunk_enable.go — turning a chunk off, and its geometry out of memory.
//
// A level big enough to want streaming cannot afford to hold all of itself on
// the GPU at once, and the unit it wants to stream is the chunk: it is already
// the thing sectors, biome paint and portal gaps belong to, already the thing
// the spatial hash buckets and the renderer culls, and already the thing a
// script can address by name. So "load the next area" is `@chunk.prop.enabled
// = true` rather than a second, parallel notion of a region.
//
// Disabled means two things at once, and both have to hold or the flag is a
// lie:
//
//   - Not drawn. Every per-chunk loop — the main pass, the shadow passes, the
//     detail and biome passes, the water sync — skips it.
//   - Not there. It is left out of the spatial hash, which is what collision,
//     entity location and the script location verbs all resolve through, so a
//     player walks through where it was rather than along it. Raycasts, nav
//     and the compiler check the flag themselves.
//
// What it deliberately does NOT mean is "not resident". Building a chunk's
// meshes is the expensive part — thousands of GL objects for a big area — and
// doing it the frame a script switches an area on stalls the game for seconds.
// So every chunk is built at load, behind the loading screen, and toggling is a
// flag write plus an index rebuild. A level that would rather have the memory
// back sets UnloadWhenDisabled on the chunk, which restores the release on
// disable and the rebuild on enable; see ReleaseGeometry.
//
// The flag is stored as Disabled rather than Enabled so the zero value is "on":
// every chunk built by a test, an import or an older level file predates this
// and none of them meant to be invisible. Scripts and the editor still say
// `enabled`, which is the way round anyone actually thinks about it.
//
// Everything follows the flag through SyncChunkGeometry, a reconcile rather
// than a callback: writing the flag is cheap and can happen anywhere — mid-tick,
// in a script handler, from an undo — while releasing and rebuilding buffers is
// gl.* work that must happen on the main thread between frames, and the index
// rebuild must happen before anything resolves through the hash again.
// Separating them is the same split DrainScriptWork exists for.

// EvictBiomeCaches releases the GPU resources the renderer derives from a
// biome scatter volume — its scatter height texture, its ground patch mesh
// and its cached triangulation. Installed by the render package (GrassInit),
// because those caches live there and this package sits below it. The
// default does nothing, which is correct headless and in every test.
var EvictBiomeCaches = func(volumeID string) {}

// IsEnabled reports whether the chunk takes part in the world at all.
func (ch *Chunk) IsEnabled() bool { return !ch.Disabled }

// SetChunkEnabled writes the flag and brings the chunk's residency into line
// with it straight away.
//
// Callers that are already on the main thread with the GL context current — the
// editor's panel, the level loader — want this. A script writing
// `@chunk.prop.enabled` sets the field alone and lets the frame loop's
// SyncChunkGeometry do the rest, because a script tick is not a place gl.*
// may be called from.
//
// Reports whether the flag changed, which is the caller's cue to rebuild the
// level indexes: the spatial hash leaves disabled chunks out, so a chunk that
// has just come back is not yet in any cell, and one that has just gone is
// still in some.
func SetChunkEnabled(ch *Chunk, on bool) bool {
	if ch == nil || ch.IsEnabled() == on {
		return false
	}
	ch.Disabled = !on
	SyncChunkGeometryFor(ch)
	return true
}

// SyncChunkGeometry brings every chunk's residency into line with its flags,
// and reports whether any chunk's enabled state changed since the last call
// reported it (or its residency had to change).
//
// Called once a frame from the main loop, where the GL context is current. A
// true return means the set of chunks that exist has changed, so the caller
// rebuilds the level indexes — and must, because this is the only thing that
// notices a script's toggle.
func SyncChunkGeometry(lvl *Level) bool {
	if lvl == nil {
		return false
	}
	changed := false
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if SyncChunkGeometryFor(ch) {
			changed = true
		}
		// A compiled chunk's surfaces are not its own any more, so its flag
		// reaches nothing on screen by itself — the static list has to be told
		// separately, or a switched-off area would stay drawn. Written every
		// frame rather than on the transition, because the flag can be set from
		// anywhere (a script tick, an undo) and this is the reconcile that owns
		// the consequence.
		if lvl.Static != nil && ch.Compiled {
			lvl.Static.SetChunkHidden(ch.ID, ch.Disabled)
		}
	}
	return changed
}

// SyncChunkGeometryFor is the single-chunk half of SyncChunkGeometry: it
// releases or rebuilds the chunk's geometry if its flags now ask for the other
// state, and reports whether the enabled flag moved since it was last
// reported, or residency changed.
func SyncChunkGeometryFor(ch *Chunk) bool {
	changed := ch.Disabled != ch.indexedDisabled
	ch.indexedDisabled = ch.Disabled
	switch want := ch.WantsGeometry(); {
	case !want && !ch.Unloaded:
		ch.ReleaseGeometry()
		changed = true
	case want && ch.Unloaded:
		ch.ReloadGeometry()
		changed = true
	}
	return changed
}

// WantsGeometry reports whether the chunk's flags ask for its geometry to be
// built: always, unless it is switched off AND has opted into unloading when
// it is. The loaders read it to decide what to build, and SyncChunkGeometryFor
// to decide what to release — one rule, so the chunk a script has toggled off
// and on is the chunk the loader would have produced.
func (ch *Chunk) WantsGeometry() bool { return !(ch.Disabled && ch.UnloadWhenDisabled) }

// ReleaseGeometry frees every GPU and CPU buffer the chunk owns and marks it
// unloaded. Switching a chunk off only reaches this when the chunk opts in with
// UnloadWhenDisabled; a level swap reaches it for every chunk. The chunk itself stays in the level: its brushes, textures, tags
// and script are what ReloadGeometry rebuilds from, and they are also what
// the editor draws, so a disabled chunk is still visible to author and still
// addressable by name.
//
// Safe to call twice; the second call finds nothing to free.
func (ch *Chunk) ReleaseGeometry() {
	for bi := range ch.Brushes {
		ch.Brushes[bi].Unload()
	}
	ch.Unloaded = true
}

// ReloadGeometry rebuilds everything ReleaseGeometry threw away, from the
// chunk's own authored data.
//
// This is the same work the loader does after it has finished reading a
// chunk's YAML, in the same order and through the same functions — Brush.Load
// per brush, then the chunk-level passes that span more than one — so a
// chunk that has been off and on again is the chunk the loader would have
// produced.
func (ch *Chunk) ReloadGeometry() {
	for bi := range ch.Brushes {
		if !ch.DrawsBrushLive(&ch.Brushes[bi]) {
			continue
		}
		_ = ch.Brushes[bi].Load()
	}
	ch.RecomputeBounds()
	ch.Unloaded = false
}

// DrawsBrushLive reports whether b needs a mesh of its own in this chunk.
//
// It always does in an uncompiled chunk. In a compiled one the static brushes
// do not — their surfaces are in the compiled list, and building meshes here
// would draw them twice, once from each (see static.go) — but a DYNAMIC brush
// still does, because the compiler deliberately left it out of that list. The
// one place that rule is written; the loader, the reload above and the editor's
// compiled preview all read it, and a disagreement between them is a brush
// drawn twice or not at all.
func (ch *Chunk) DrawsBrushLive(b *Brush) bool { return !ch.Compiled || b.Dynamic }

// ReleaseBakedGeometry frees the meshes of the brushes a compile has taken
// over, leaving the dynamic ones — which it has not — loaded.
//
// For the editor's compiled preview, which turns a live chunk into a compiled
// one in place. ReleaseGeometry is the other case and deliberately releases
// everything: an unloaded chunk has nothing resident, dynamic brushes included.
func (ch *Chunk) ReleaseBakedGeometry() {
	for bi := range ch.Brushes {
		b := &ch.Brushes[bi]
		if ch.DrawsBrushLive(b) {
			continue
		}
		b.Unload()
	}
}

// ReleaseLevelGeometry frees everything the level's chunks own on the GPU, plus
// the spawn marker, the placed scenery and the projected decals.
//
// For replacing a running level with another — a level trigger firing, the
// editor opening a file. Overwriting the Level struct drops the handles rather
// than freeing them, so without this every level change leaks the whole
// outgoing level's world geometry: walls, floors, ceilings, portal gaps,
// skirts, water planes and the biome caches derived from them.
//
// Entities and volumes are released by their own passes beside this one; they
// are held per-object rather than per-chunk, and the packages that own them sit
// above this one. Scenery is held per-object too, but this package owns it, so
// it is freed here rather than by a fourth call every level swap has to
// remember to make.
func ReleaseLevelGeometry(lvl *Level) {
	if lvl == nil {
		return
	}
	for ci := range lvl.Chunks {
		lvl.Chunks[ci].ReleaseGeometry()
	}
	if lvl.Spawn != nil {
		lvl.Spawn.Delete()
	}
	// Scenery is uploaded per placement rather than per surface, so it has to
	// be released explicitly — overwriting the level struct would drop the
	// handles rather than free them.
	DeleteScenery(lvl)
	// Decals, for the same reason again: a decal's mesh is clipped out of the
	// level's geometry and uploaded per decal, so it belongs to no chunk.
	ReleaseDecals(lvl)
	// A compiled level's surfaces belong to no chunk, so nothing above reaches
	// them; they are the largest single thing a level owns on the GPU.
	lvl.Static.Release()
}
