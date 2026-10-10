package editor

// editor_3d_pick_cycle.go — repeated clicks cycle through what is under the
// cursor.
//
// One click can only ever mean one thing, but a pixel routinely covers several:
// a light fitting standing against the wall it is bolted to, a trigger volume
// wrapped around the doorway it watches, the far side of a box you are looking
// into. Picking the nearest is the right answer for the FIRST click; clicking
// again in the same place now takes the next candidate along the ray instead of
// re-selecting the one already selected, and the last wraps back to the first.
//
// The cycle keeps no state of its own. The anchor is the current selection: the
// picker lists everything under the cursor in order, and the click takes the
// entry after whichever one is selected, or the first when none of them is.
// That is how the vertex and edge handles have always cycled
// (pickVertexHandle), and it costs nothing to be right about a camera that
// moved, a level that was reloaded or an object that was deleted between two
// clicks — there is no remembered index to go stale.

// pickCycleNext returns the candidate a fresh click should select: the one
// after the currently selected entry when the selection is under the cursor,
// and the nearest otherwise. selected reports whether a candidate is what is
// selected now, which differs by context — object mode compares whole brushes
// where face mode compares faces.
func pickCycleNext(cands []pickHit, selected func(pickHit) bool) pickHit {
	if len(cands) == 0 {
		return pickNoHit()
	}
	for i := range cands {
		if selected(cands[i]) {
			return cands[(i+1)%len(cands)]
		}
	}
	return cands[0]
}

// pickObjectCandidates is pick3DCandidates as OBJECT mode sees it: a brush is
// one thing, not the several faces of it the ray crossed, so the faces collapse
// to the brush they belong to at the distance of the nearest one. Without that
// a click on a box would cycle through its near face, its far face and its
// sides while the selection never visibly changed.
func pickObjectCandidates(mx, my int32) []pickHit {
	cands := pick3DCandidates(mx, my)
	out := make([]pickHit, 0, len(cands))
	seen := make(map[brushSel]bool)
	for _, c := range cands {
		if c.kind != pickBrushFace {
			out = append(out, c)
			continue
		}
		s := brushSel{chunkIdx: c.chunkIdx, brushIdx: c.brushIdx}
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, c)
	}
	return out
}

// pickFaceClick resolves a FACE-mode click: the next face (or object) under the
// cursor after the one selected now, or the nearest one when ctrl is held.
//
// Ctrl does not cycle because it is a different gesture: it is adding to a
// multi-selection, and every face it adds is already selected by the time the
// next ctrl+click lands, so a cycle would walk the ray every click and never
// let the author build the set they were pointing at.
func pickFaceClick(mx, my int32, ctrlHeld bool) pickHit {
	cands := pick3DCandidates(mx, my)
	if ctrlHeld {
		return pickNearest(cands)
	}
	return pickCycleNext(cands, func(h pickHit) bool {
		if f, ok := faceSelFromHit(h); ok {
			return f == primaryFaceSel()
		}
		return pickHitIsSelectedObject(h)
	})
}

// pickObjectClick is pickFaceClick for OBJECT mode, where the unit is a whole
// brush rather than one of its faces.
func pickObjectClick(mx, my int32, ctrlHeld bool) pickHit {
	cands := pickObjectCandidates(mx, my)
	if ctrlHeld {
		return pickNearest(cands)
	}
	return pickCycleNext(cands, func(h pickHit) bool {
		if h.kind == pickBrushFace {
			return brushSel{chunkIdx: h.chunkIdx, brushIdx: h.brushIdx} == primaryBrushSel()
		}
		return pickHitIsSelectedObject(h)
	})
}

// pickNearest is the first candidate — what a click meant before any of this,
// and still what ctrl+click means.
func pickNearest(cands []pickHit) pickHit {
	if len(cands) == 0 {
		return pickNoHit()
	}
	return cands[0]
}

// pickHitIsSelectedObject reports whether h names the object that is selected
// right now — the PRIMARY one, where a ctrl-built multi-selection has several.
// The primary is what the panel is showing, so it is what "click again to go
// past this one" means.
//
// Brush geometry is not here: what a face hit counts as depends on the context
// the click was made in, and each caller above answers that for itself.
//
// These are Editor fields rather than ws() ones because they are the LEVEL
// editor's — the entity editor's workspace holds a scratch level of brushes and
// nothing else (entityRebuildGeometry), so none of these kinds ever reaches
// this function from that window.
func pickHitIsSelectedObject(h pickHit) bool {
	switch h.kind {
	case pickSpawn:
		return Editor.SpawnSelected
	case pickSeal:
		return Editor.SealSelected
	case pickEntity:
		return Editor.ActiveEntityIdx == h.idx
	case pickVolume:
		return Editor.ActiveVolumeIdx == h.idx
	case pickSprite:
		return Editor.ActiveSpriteIdx == h.idx
	case pickDecal:
		return Editor.ActiveDecalIdx == h.idx
	case pickScenery:
		return Editor.ActiveSceneryIdx == h.idx
	case pickEmitter:
		return Editor.ActiveEmitterIdx == h.idx
	case pickSound:
		return Editor.ActiveSoundIdx == h.idx
	case pickEffect:
		return Editor.ActiveEffectIdx == h.idx
	case pickTree:
		return Editor.ActiveTreeIdx == h.idx
	case pickWaypoint:
		return Editor.ActiveWaypointAnimIdx == h.idx && Editor.ActiveWaypointIdx == h.sub
	case pickLight:
		return Editor.SelectedLightKind == lightKindPoint && Editor.SelectedLightIdx == h.idx
	}
	return false
}
