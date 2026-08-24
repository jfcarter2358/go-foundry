package main

import (
	"foundry/internal/leveltest"
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// faceTestLevel is two square sectors sharing an edge, each with a floor, a
// ceiling and four walls — enough faces of every kind to select across.
func faceTestLevel(t *testing.T) *world.Chunk {
	t.Helper()
	lvl := &world.Level{Chunks: []world.Chunk{*leveltest.SplitChunk()}}
	Editor.level = lvl
	Editor.Mode = Mode3D
	Editor.ActiveChunkIdx = 0
	Editor.ActiveSectorIdx = -1
	Editor.SelectedFaces = nil
	Editor.SelectedVertices = nil
	Editor.SpawnSelected = false
	Editor.ActiveBiomeSectorIdx = -1
	clearFaceSelection()
	return &lvl.Chunks[0]
}

func wallFace(si, idx int) faceSel { return edgeFaceSel(0, si, "wall", idx) }
func floorFace(si int) faceSel     { return surfaceFaceSel(0, si, "floor") }
func ceilingFace(si int) faceSel   { return surfaceFaceSel(0, si, "ceiling") }

// ---------- the mirror ----------

// The four Active* fields are derived from the selection's first entry. Every
// panel and outline in the editor was written against them, so if they drift
// out of step with the list the whole side panel starts describing a face that
// is not the one being edited.
func TestSetFaceSelectionMirrorsThePrimary(t *testing.T) {
	defer saveEditorSelection()()
	faceTestLevel(t)

	setFaceSelection([]faceSel{wallFace(1, 2), floorFace(0)})
	if Editor.ActiveSurface != "wall" || Editor.ActiveEdgeKind != "wall" || Editor.ActiveEdgeIdx != 2 {
		t.Errorf("wall primary mirrored as %q/%q #%d, want wall/wall #2",
			Editor.ActiveSurface, Editor.ActiveEdgeKind, Editor.ActiveEdgeIdx)
	}
	if Editor.ActiveSectorIdx != 1 {
		t.Errorf("ActiveSectorIdx = %d, want the primary's sector 1", Editor.ActiveSectorIdx)
	}

	// A floor has no edge index, so the edge half of the mirror has to be
	// cleared or it would still name the wall that was selected before.
	setFaceSelection([]faceSel{floorFace(0)})
	if Editor.ActiveSurface != "floor" {
		t.Errorf("ActiveSurface = %q, want floor", Editor.ActiveSurface)
	}
	if Editor.ActiveEdgeKind != "" || Editor.ActiveEdgeIdx != -1 {
		t.Errorf("a floor left an edge in the mirror: %q #%d",
			Editor.ActiveEdgeKind, Editor.ActiveEdgeIdx)
	}
}

func TestClearFaceSelectionClearsTheMirror(t *testing.T) {
	defer saveEditorSelection()()
	faceTestLevel(t)
	setFaceSelection([]faceSel{wallFace(0, 1)})

	clearFaceSelection()
	if len(Editor.SelectedFaces) != 0 {
		t.Errorf("faces survived the clear: %v", Editor.SelectedFaces)
	}
	if Editor.ActiveSurface != "" || Editor.ActiveEdgeKind != "" || Editor.ActiveEdgeIdx != -1 {
		t.Errorf("the mirror survived the clear: %q/%q #%d",
			Editor.ActiveSurface, Editor.ActiveEdgeKind, Editor.ActiveEdgeIdx)
	}
}

// ---------- building a multi-selection ----------

// Ctrl+click adds a face, and adding one that is already in takes it back out —
// which is what lets a mis-aimed click be undone without starting the whole
// selection again.
func TestAddFaceToSelectionToggles(t *testing.T) {
	defer saveEditorSelection()()
	faceTestLevel(t)

	addFaceToSelection(wallFace(0, 0))
	addFaceToSelection(wallFace(0, 1))
	addFaceToSelection(floorFace(0))
	if selectedFaceCount() != 3 {
		t.Fatalf("selected %d faces, want 3", selectedFaceCount())
	}

	addFaceToSelection(wallFace(0, 1))
	if selectedFaceCount() != 2 {
		t.Fatalf("re-adding a face left %d selected, want it removed", selectedFaceCount())
	}
	if isFaceSelected(wallFace(0, 1)) {
		t.Error("re-adding a face did not deselect it")
	}
	if !isFaceSelected(wallFace(0, 0)) || !isFaceSelected(floorFace(0)) {
		t.Error("removing one face took another with it")
	}
}

// Removing the first entry has to promote the next one, or the panel would go
// on describing a face that is no longer selected.
func TestRemovingThePrimaryPromotesTheNext(t *testing.T) {
	defer saveEditorSelection()()
	faceTestLevel(t)

	setFaceSelection([]faceSel{wallFace(0, 0), floorFace(1)})
	addFaceToSelection(wallFace(0, 0)) // toggles the primary back out

	if Editor.ActiveSurface != "floor" || Editor.ActiveSectorIdx != 1 {
		t.Errorf("mirror = %q sector %d, want the promoted floor of sector 1",
			Editor.ActiveSurface, Editor.ActiveSectorIdx)
	}
}

// A face that does not exist must not enter the selection, or every action
// downstream has to re-check indices the selection should never have held.
func TestAddFaceRejectsSomethingThatIsNotThere(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceTestLevel(t)

	addFaceToSelection(wallFace(0, len(ch.Sectors[0].Walls)))
	addFaceToSelection(faceSel{chunkIdx: 0, sectorIdx: 99, kind: "floor", idx: -1})
	addFaceToSelection(faceSel{chunkIdx: 0, sectorIdx: 0, kind: "nonsense", idx: -1})
	if selectedFaceCount() != 0 {
		t.Errorf("selected %d impossible faces: %v", selectedFaceCount(), Editor.SelectedFaces)
	}
}

func TestFaceSelValid(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceTestLevel(t)

	cases := []struct {
		name string
		sel  faceSel
		want bool
	}{
		{"a wall that exists", wallFace(0, 0), true},
		{"a wall past the end", wallFace(0, len(ch.Sectors[0].Walls)), false},
		{"a floor", floorFace(1), true},
		{"a ceiling", ceilingFace(1), true},
		{"a sector past the end", floorFace(len(ch.Sectors)), false},
		{"a chunk past the end", faceSel{chunkIdx: 9, kind: "floor", idx: -1}, false},
		{"a portal on a sector with none", edgeFaceSel(0, 0, "portal", 0), false},
	}
	for _, c := range cases {
		if got := faceSelValid(c.sel); got != c.want {
			t.Errorf("%s: faceSelValid = %v, want %v", c.name, got, c.want)
		}
	}
}

// An edit that deletes a sector or renumbers its walls can strand entries.
// Pruning drops those and republishes the mirror from whatever is left.
func TestPruneFaceSelectionDropsStaleEntries(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceTestLevel(t)

	setFaceSelection([]faceSel{wallFace(0, 0), wallFace(1, 3), floorFace(1)})
	ch.Sectors = ch.Sectors[:1] // sector 1 is gone

	pruneFaceSelection()
	if selectedFaceCount() != 1 {
		t.Fatalf("pruned to %d faces, want the 1 still live: %v",
			selectedFaceCount(), Editor.SelectedFaces)
	}
	if Editor.ActiveSectorIdx != 0 || Editor.ActiveEdgeIdx != 0 {
		t.Errorf("the mirror was not republished: sector %d edge %d",
			Editor.ActiveSectorIdx, Editor.ActiveEdgeIdx)
	}
}

// ---------- mutual exclusivity with the other selection kinds ----------

// A face and a point are different units. Holding both at once is what makes a
// context menu ambiguous about what its rows are for, so each clears the other.
func TestFaceAndVertexSelectionsExcludeEachOther(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceTestLevel(t)

	setFaceSelection([]faceSel{wallFace(0, 0), wallFace(0, 1)})
	setVertexSelection([]vertexSel{vsel(ch, 0, 0, true)})
	if selectedFaceCount() != 0 {
		t.Errorf("selecting a point left %d faces selected", selectedFaceCount())
	}

	setFaceSelection([]faceSel{wallFace(0, 0)})
	if len(Editor.SelectedVertices) != 0 {
		t.Errorf("selecting a face left %d points selected", len(Editor.SelectedVertices))
	}
}

func TestFaceSelectionClearsTheSpawnMarker(t *testing.T) {
	defer saveEditorSelection()()
	faceTestLevel(t)
	Editor.level.Spawn = &world.SpawnPoint{Position: mgl32.Vec3{}}
	editorSelectSpawn()

	setFaceSelection([]faceSel{floorFace(0)})
	if Editor.SpawnSelected {
		t.Error("selecting a face left the spawn marker selected")
	}
}

// ---------- what a pick turns into ----------

func TestFaceSelFromHit(t *testing.T) {
	cases := []struct {
		name string
		hit  pickHit
		want faceSel
		Ok   bool
	}{
		{"a wall", pickHit{kind: pickWall, chunkIdx: 1, sectorIdx: 2, idx: 3},
			edgeFaceSel(1, 2, "wall", 3), true},
		{"a floor", pickHit{kind: pickFloor, chunkIdx: 0, sectorIdx: 4, idx: -1},
			surfaceFaceSel(0, 4, "floor"), true},
		{"a ceiling", pickHit{kind: pickCeiling, chunkIdx: 0, sectorIdx: 4, idx: -1},
			surfaceFaceSel(0, 4, "ceiling"), true},
		{"the spawn marker", pickHit{kind: pickSpawn}, faceSel{}, false},
		{"a water plane", pickHit{kind: pickWater}, faceSel{}, false},
		{"a miss", pickHit{kind: pickNone}, faceSel{}, false},
	}
	for _, c := range cases {
		got, ok := faceSelFromHit(c.hit)
		if ok != c.Ok {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.Ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

// ---------- the fields an action reaches ----------

func TestSurfaceRefsForResolvesEachKind(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceTestLevel(t)

	r, ok := editor3DSurfaceRefsFor(floorFace(0))
	if !ok || r.Texture != &ch.Sectors[0].Floor.Texture {
		t.Error("a floor did not resolve to its own texture field")
	}
	if r.Collision != nil {
		t.Error("a floor offered a collision flag it does not have")
	}

	r, ok = editor3DSurfaceRefsFor(wallFace(1, 2))
	if !ok || r.Texture != &ch.Sectors[1].Walls[2].Texture {
		t.Error("a wall did not resolve to its own texture field")
	}
	if r.Collision != &ch.Sectors[1].Walls[2].Collision {
		t.Error("a wall did not resolve to its own collision flag")
	}

	if _, ok := editor3DSurfaceRefsFor(wallFace(0, 99)); ok {
		t.Error("a wall past the end resolved to refs")
	}
}

// The primary is the first entry: what the panel shows, and what a toggle takes
// its "before" value from.
func TestSurfaceRefsFollowsThePrimary(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceTestLevel(t)

	setFaceSelection([]faceSel{wallFace(1, 2), floorFace(0)})
	r, ok := editor3DSurfaceRefs()
	if !ok || r.Texture != &ch.Sectors[1].Walls[2].Texture {
		t.Error("the refs did not follow the first selected face")
	}

	clearFaceSelection()
	if _, ok := editor3DSurfaceRefs(); ok {
		t.Error("refs resolved with no face selected")
	}
}

// ---------- editing them all ----------

// Setting a texture reaches every selected face, which is the whole reason
// faces became multi-selectable. (The rebuild that normally follows needs a GL
// context, so the edit half is driven directly — see editor3DForEachSelectedFace.)
func TestApplyingATextureReachesEverySelectedFace(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceTestLevel(t)

	setFaceSelection([]faceSel{wallFace(0, 0), wallFace(0, 2), floorFace(1)})
	n := applySelectedFaceEdits(func(r surface3DRefs) {
		if r.Texture != nil {
			*r.Texture = "marble"
		}
	})
	if n != 3 {
		t.Fatalf("reached %d faces, want 3", n)
	}
	if ch.Sectors[0].Walls[0].Texture != "marble" || ch.Sectors[0].Walls[2].Texture != "marble" {
		t.Error("a selected wall was not retextured")
	}
	if ch.Sectors[1].Floor.Texture != "marble" {
		t.Error("the selected floor was not retextured")
	}
	// And nothing else was touched.
	if ch.Sectors[0].Walls[1].Texture == "marble" {
		t.Error("an unselected wall was retextured")
	}
}

// A toggle sets every selected face to the same value — the opposite of the
// primary's — rather than flipping each independently, which would leave a
// mixed selection exactly as mixed as it started.
func TestToggleSetsEverySelectedFaceToMatchThePrimary(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceTestLevel(t)

	ch.Sectors[0].Walls[0].Collision = true // the primary
	ch.Sectors[0].Walls[1].Collision = false
	ch.Sectors[0].Walls[2].Collision = true
	setFaceSelection([]faceSel{wallFace(0, 0), wallFace(0, 1), wallFace(0, 2)})

	pick := func(r surface3DRefs) *bool { return r.Collision }
	want := !*pick(*mustRefs(t, wallFace(0, 0)))
	applySelectedFaceEdits(func(r surface3DRefs) {
		if f := pick(r); f != nil {
			*f = want
		}
	})

	for i := 0; i < 3; i++ {
		if got := ch.Sectors[0].Walls[i].Collision; got != want {
			t.Errorf("wall %d collision = %v, want %v — all three should match", i, got, want)
		}
	}
}

// A selection that mixes kinds must not be blocked by the members that have no
// such field: a floor has no collision flag, and skipping it is right, but it
// must not stop the walls beside it from being set.
func TestApplyingSkipsFacesWithoutTheField(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceTestLevel(t)

	ch.Sectors[0].Walls[0].Collision = false
	setFaceSelection([]faceSel{floorFace(0), wallFace(0, 0)})
	applySelectedFaceEdits(func(r surface3DRefs) {
		if r.Collision != nil {
			*r.Collision = true
		}
	})
	if !ch.Sectors[0].Walls[0].Collision {
		t.Error("a floor in the selection stopped the wall beside it from being set")
	}
}

// mustRefs resolves a face's refs or fails the test.
func mustRefs(t *testing.T, sel faceSel) *surface3DRefs {
	t.Helper()
	r, ok := editor3DSurfaceRefsFor(sel)
	if !ok {
		t.Fatalf("no refs for %+v", sel)
	}
	return &r
}

// ---------- selecting a whole sector ----------

func TestSelectSectorWallsTakesTheWholeRing(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceTestLevel(t)
	setFaceSelection([]faceSel{wallFace(0, 0)})

	editor3DSelectSectorWalls()
	if got, want := selectedFaceCount(), len(ch.Sectors[0].Walls); got != want {
		t.Fatalf("selected %d walls, want %d", got, want)
	}
	for i := range ch.Sectors[0].Walls {
		if !isFaceSelected(wallFace(0, i)) {
			t.Errorf("wall %d was left out", i)
		}
	}
}

func TestSelectSectorFacesTakesTheSurfacesToo(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceTestLevel(t)
	setFaceSelection([]faceSel{wallFace(1, 0)})

	editor3DSelectSectorFaces()
	want := len(ch.Sectors[1].Walls) + len(ch.Sectors[1].Portals) + 2
	if got := selectedFaceCount(); got != want {
		t.Fatalf("selected %d faces, want %d", got, want)
	}
	if !isFaceSelected(floorFace(1)) || !isFaceSelected(ceilingFace(1)) {
		t.Error("the floor and ceiling were left out")
	}
}
