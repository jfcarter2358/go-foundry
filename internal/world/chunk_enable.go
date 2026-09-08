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
// Disabled means three things at once, and all three have to hold or the flag
// is a lie:
//
//   - Not drawn. Every per-chunk loop — the main pass, the shadow passes, the
//     detail and biome passes, the water sync — skips it.
//   - Not there. It is left out of the spatial hash, which is what collision,
//     entity location and the script location verbs all resolve through, so a
//     player walks through where it was rather than along it.
//   - Not resident. Its vertex buffers are released, which is the part that
//     makes this worth having at all. See ReleaseGeometry.
//
// The flag is stored as Disabled rather than Enabled so the zero value is "on":
// every chunk built by a test, an import or an older level file predates this
// and none of them meant to be invisible. Scripts and the editor still say
// `enabled`, which is the way round anyone actually thinks about it.
//
// Geometry follows the flag through SyncChunkGeometry, a reconcile rather than
// a callback: writing the flag is cheap and can happen anywhere — mid-tick, in
// a script handler, from an undo — while releasing and rebuilding buffers is
// gl.* work that must happen on the main thread between frames. Separating them
// is the same split DrainScriptWork exists for.

// EvictBiomeCaches releases the GPU resources the renderer derives from a
// biome scatter volume — its scatter height texture, its ground patch mesh
// and its cached triangulation. Installed by the render package (GrassInit),
// because those caches live there and this package sits below it. The
// default does nothing, which is correct headless and in every test.
var EvictBiomeCaches = func(volumeID string) {}

// IsEnabled reports whether the chunk takes part in the world at all.
func (ch *Chunk) IsEnabled() bool { return !ch.Disabled }

// SetChunkEnabled writes the flag and brings the chunk's geometry into line
// with it straight away.
//
// Callers that are already on the main thread with the GL context current — the
// editor's panel, the level loader — want this. A script writing
// `@chunk.prop.enabled` sets the field alone and lets the frame loop's
// SyncChunkGeometry do the buffers, because a script tick is not a place gl.*
// may be called from.
//
// Reports whether anything changed, which is the caller's cue to rebuild the
// level indexes: the spatial hash leaves disabled chunks out, so a chunk that
// has just come back is not yet in any cell.
func SetChunkEnabled(ch *Chunk, on bool) bool {
	if ch == nil || ch.IsEnabled() == on {
		return false
	}
	ch.Disabled = !on
	return SyncChunkGeometryFor(ch)
}

// SyncChunkGeometry brings every chunk's residency into line with its flag, and
// reports whether it had to change any of them.
//
// Called once a frame from the main loop, where the GL context is current. A
// true return means the set of chunks that exist has changed, so the caller
// rebuilds the level indexes.
func SyncChunkGeometry(lvl *Level) bool {
	if lvl == nil {
		return false
	}
	changed := false
	for ci := range lvl.Chunks {
		if SyncChunkGeometryFor(&lvl.Chunks[ci]) {
			changed = true
		}
	}
	return changed
}

// SyncChunkGeometryFor is the single-chunk half of SyncChunkGeometry.
func SyncChunkGeometryFor(ch *Chunk) bool {
	switch {
	case ch.Disabled && !ch.Unloaded:
		ch.ReleaseGeometry()
		return true
	case !ch.Disabled && ch.Unloaded:
		ch.ReloadGeometry()
		return true
	}
	return false
}

// ReleaseGeometry frees every GPU and CPU buffer the chunk owns and marks it
// unloaded. The chunk itself stays in the level: its brushes, textures, tags
// and script are what ReloadGeometry rebuilds from, and they are also what
// the editor draws, so a disabled chunk is still visible to author and still
// addressable by name.
//
// Safe to call twice; the second call finds nothing to free.
func (ch *Chunk) ReleaseGeometry() {
	for bi := range ch.Brushes {
		b := &ch.Brushes[bi]
		for fi := range b.Faces {
			f := &b.Faces[fi]
			f.Geometry.Unload()
			for si := range f.SubmeshGeometries {
				f.SubmeshGeometries[si].Unload()
			}
			f.SubmeshGeometries = nil
		}
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
		_ = ch.Brushes[bi].Load()
	}
	ch.RecomputeBounds()
	ch.Unloaded = false
}

// ReleaseLevelGeometry frees everything the level's chunks own on the GPU, plus
// the spawn marker and the placed scenery.
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
}
