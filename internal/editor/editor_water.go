package editor

// editor_water.go — selecting a water plane in the 3D view, and dragging its
// height with the move gizmo.
//
// A water plane is not a placed object: it is derived, every frame, from the
// Water-kind BiomeSector that painted it (biome_water.go), and the one thing
// about it that is not fixed by that polygon is how far the surface sits from
// the floor underneath — BiomeBrush.WaterDepth. So the gizmo here is a single
// green arm that writes that one number back, and clicking the plane selects
// the sector rather than the surface (editor_3d.go's pickWater case), which is
// what opens the Depth slider the arm is a second way of driving.
//
// Everything else a translate gizmo normally offers is deliberately absent.
// The plane's XZ is the painted polygon's centroid and its outline is that
// polygon, so an X or Z arm would be claiming to move something only the map
// layer can move; rotate and scale have no branch for this kind at all.

import (
	"foundry/internal/biome"
	"foundry/internal/world"
)

// editorSelectWater makes the water plane at surfaceIdx in chunkIdx the
// selection: the sector that produced it opens in the panel, and the move
// gizmo attaches to the plane's height.
//
// A plane whose sector has already been deleted but which the next sync has
// not swept up yet selects nothing — there is no depth to drag and no
// properties to show.
func editorSelectWater(chunkIdx, surfaceIdx int) {
	bsIdx := editorWaterSectorFor(chunkIdx, surfaceIdx)
	if bsIdx < 0 {
		Editor.PanelFocus = panelFocusNone
		return
	}
	Editor.ActiveChunkIdx = chunkIdx
	Editor.ActiveSectorIdx = -1
	Editor.ActiveBiomeSectorIdx = bsIdx
	Editor.PanelFocus = panelFocusWater
	Editor.GizmoKind = gizmoKindWater
}

// editorClearWaterSelection takes the gizmo off a water plane. The biome
// sector selection itself is left alone: it is the same selection the 2D map
// layer works in, and callers that want it gone clear ActiveBiomeSectorIdx.
func editorClearWaterSelection() {
	if Editor.GizmoKind == gizmoKindWater {
		Editor.GizmoKind = gizmoKindNone
	}
}

// activeGizmoWater returns the chunk, the Water-kind biome sector and the
// companion surface the gizmo is attached to, or nils.
//
// The gizmo kind is checked as well as the selection — the same guard
// activeGizmoPointLight applies — so a biome sector still selected from the 2D
// layer cannot be dragged by a gizmo that was never attached to it. The kind
// is re-checked too: switching the brush away from Water drops the surface on
// the next sync, and the lookup below then finds nothing.
func activeGizmoWater() (*world.Chunk, *world.BiomeSector, *world.WaterSurface) {
	if Editor.GizmoKind != gizmoKindWater {
		return nil, nil, nil
	}
	ch := activeChunk()
	if ch == nil {
		return nil, nil, nil
	}
	idx := Editor.ActiveBiomeSectorIdx
	if idx < 0 || idx >= len(ch.BiomeSectors) {
		return nil, nil, nil
	}
	bs := &ch.BiomeSectors[idx]
	if bs.Brush.Kind != world.BiomeWater {
		return nil, nil, nil
	}
	for wi := range ch.WaterSurfaces {
		if ch.WaterSurfaces[wi].ID == bs.ID {
			return ch, bs, &ch.WaterSurfaces[wi]
		}
	}
	return nil, nil, nil
}

// gizmoWaterMovesYOnly reports whether the current selection is a water plane
// — the one kind whose only movable coordinate is its height. The X and Z arms
// would have nothing to write back to, so they are neither drawn nor
// clickable, the same way gizmoChunkRotates hides the rings a chunk has no
// rotation for.
func gizmoWaterMovesYOnly() bool {
	return gizmoEffectiveKind() == gizmoKindWater
}

// dragWaterLevel applies a Y-arm drag to the selected plane, converting the
// world height the cursor lands on back into the sector's WaterDepth.
//
// The whole round trip is needed rather than just adding delta to the depth:
// the drag is anchored on a world-space snapshot so a long drag stays exact
// (see the file header of editor_translate_gizmo.go), the grid snap applies in
// world space like every other axis drag, and the chunk may be placed with an
// origin and a scale that the plane's chunk-local Position is expressed
// against. Depth is measured DOWN from the floor, hence the subtraction —
// dragging the arm up shrinks it, and past the floor it goes negative and the
// water stands proud of the ground.
func dragWaterLevel(delta float32) {
	ch, bs, w := activeGizmoWater()
	if w == nil {
		return
	}
	// The same floor sample rebuildBiomeWaterSurface derives the plane's Y
	// from, so the depth written here reproduces exactly the height dragged to.
	_, floorY := biome.BiomeWaterCentroidAndFloor(Editor.level, ch, bs)

	wantWorld := axisMoved(Editor.GizmoDragStartPos, 1, delta)
	wantLocal := ch.WorldToLocal(wantWorld)

	bs.Brush.WaterDepth = world.NormalizeWaterDepth(floorY - wantLocal.Y())
	// The cheap flag, not GroundDirty: a drag writes this every frame it
	// moves, and the triangles are unaffected by the height they sit at.
	bs.WaterLevelDirty = true
}
