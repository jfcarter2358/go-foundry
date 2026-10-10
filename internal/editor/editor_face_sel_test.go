package editor

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// faceTestLevel is the two-box fixture — twelve faces to select across, six of
// them on each of two brushes, which is what the "which brush does this face
// belong to" assertions need.
func faceTestLevel(t *testing.T) *world.Chunk {
	t.Helper()
	ch := brushTestLevel(t)
	clearFaceSelection()
	return ch
}

// bface names one face of one brush of the fixture's single chunk.
func bface(bi, fi int) faceSel { return brushFaceSel(0, bi, fi) }

// ---------- the mirror ----------

// ActiveBrushIdx/ActiveFaceIdx are derived from the selection's first entry.
// The panel and the outline are written against them, so if they drift out of
// step with the list the side panel starts describing a face that is not the
// one being edited.
func TestSetFaceSelectionMirrorsThePrimary(t *testing.T) {
	defer saveEditorSelection()()
	faceTestLevel(t)

	setFaceSelection([]faceSel{bface(1, boxFaceNegZ), bface(0, boxFaceTop)})
	if Editor.ActiveBrushIdx != 1 || Editor.ActiveFaceIdx != boxFaceNegZ {
		t.Errorf("primary mirrored as brush %d face %d, want 1/%d",
			Editor.ActiveBrushIdx, Editor.ActiveFaceIdx, boxFaceNegZ)
	}
	if Editor.ActiveChunkIdx != 0 {
		t.Errorf("ActiveChunkIdx = %d, want the primary's chunk 0", Editor.ActiveChunkIdx)
	}

	setFaceSelection([]faceSel{bface(0, boxFaceTop)})
	if Editor.ActiveBrushIdx != 0 || Editor.ActiveFaceIdx != boxFaceTop {
		t.Errorf("primary mirrored as brush %d face %d, want 0/%d",
			Editor.ActiveBrushIdx, Editor.ActiveFaceIdx, boxFaceTop)
	}
}

func TestClearFaceSelectionClearsTheMirror(t *testing.T) {
	defer saveEditorSelection()()
	faceTestLevel(t)

	setFaceSelection([]faceSel{bface(0, boxFaceNegX)})
	clearFaceSelection()

	if selectedFaceCount() != 0 {
		t.Errorf("%d faces still selected", selectedFaceCount())
	}
	if Editor.ActiveBrushIdx != -1 || Editor.ActiveFaceIdx != -1 {
		t.Errorf("the mirror still names brush %d face %d",
			Editor.ActiveBrushIdx, Editor.ActiveFaceIdx)
	}
	if Editor.GizmoKind == gizmoKindFace {
		t.Error("the face gizmo survived the deselection")
	}
}

// ---------- building a multi-selection ----------

// Ctrl+click toggles rather than only adding, so a mis-aimed click can be taken
// back without starting the whole selection again.
func TestAddFaceToSelectionToggles(t *testing.T) {
	defer saveEditorSelection()()
	faceTestLevel(t)

	addFaceToSelection(bface(0, boxFaceNegX))
	addFaceToSelection(bface(0, boxFacePosX))
	if selectedFaceCount() != 2 {
		t.Fatalf("selected %d faces, want 2", selectedFaceCount())
	}

	addFaceToSelection(bface(0, boxFacePosX))
	if selectedFaceCount() != 1 || !isFaceSelected(bface(0, boxFaceNegX)) {
		t.Errorf("toggling off left %v", Editor.SelectedFaces)
	}
}

// Removing the primary promotes the next entry, so the panel goes on describing
// something that is actually selected.
func TestRemovingThePrimaryPromotesTheNext(t *testing.T) {
	defer saveEditorSelection()()
	faceTestLevel(t)

	setFaceSelection([]faceSel{bface(0, boxFaceNegX), bface(1, boxFaceTop)})
	addFaceToSelection(bface(0, boxFaceNegX)) // toggles the primary off

	if Editor.ActiveBrushIdx != 1 || Editor.ActiveFaceIdx != boxFaceTop {
		t.Errorf("the mirror names brush %d face %d, want the promoted 1/%d",
			Editor.ActiveBrushIdx, Editor.ActiveFaceIdx, boxFaceTop)
	}
}

// A face that does not exist must not enter the selection, or every action
// downstream has to re-check indices the selection should never have held.
func TestAddFaceRejectsSomethingThatIsNotThere(t *testing.T) {
	defer saveEditorSelection()()
	faceTestLevel(t)

	addFaceToSelection(bface(0, 99))
	addFaceToSelection(brushFaceSel(0, 99, 0))
	addFaceToSelection(brushFaceSel(9, 0, 0))
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
		{"a face that exists", bface(0, boxFaceTop), true},
		{"a face past the end", bface(0, len(ch.Brushes[0].Faces)), false},
		{"a face of the second brush", bface(1, boxFaceBottom), true},
		{"a brush past the end", brushFaceSel(0, len(ch.Brushes), 0), false},
		{"a chunk past the end", brushFaceSel(9, 0, 0), false},
		{"a negative face index", bface(0, -1), false},
	}
	for _, c := range cases {
		if got := faceSelValid(c.sel); got != c.want {
			t.Errorf("%s: faceSelValid = %v, want %v", c.name, got, c.want)
		}
	}
}

// An edit that deletes a brush can strand entries. Pruning drops those and
// republishes the mirror from whatever is left.
func TestPruneFaceSelectionDropsStaleEntries(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceTestLevel(t)

	setFaceSelection([]faceSel{bface(0, boxFaceNegX), bface(1, boxFaceTop), bface(1, boxFaceNegZ)})
	ch.Brushes = ch.Brushes[:1] // brush 1 is gone

	pruneFaceSelection()
	if selectedFaceCount() != 1 {
		t.Fatalf("pruned to %d faces, want the 1 still live: %v",
			selectedFaceCount(), Editor.SelectedFaces)
	}
	if Editor.ActiveBrushIdx != 0 || Editor.ActiveFaceIdx != boxFaceNegX {
		t.Errorf("the mirror was not republished: brush %d face %d",
			Editor.ActiveBrushIdx, Editor.ActiveFaceIdx)
	}
}

// ---------- mutual exclusivity with the other selection kinds ----------

// A face and a point are different units. Holding both at once is what makes a
// context menu ambiguous about what its rows are for, so each clears the other.
func TestFaceAndVertexSelectionsExcludeEachOther(t *testing.T) {
	defer saveEditorSelection()()
	faceTestLevel(t)

	setFaceSelection([]faceSel{bface(0, boxFaceNegX), bface(0, boxFacePosX)})
	setVertexSelection([]vertexSel{{chunkIdx: 0, brushIdx: 0, vertIdx: 0}})
	if selectedFaceCount() != 0 {
		t.Errorf("selecting a point left %d faces selected", selectedFaceCount())
	}

	setFaceSelection([]faceSel{bface(0, boxFaceNegX)})
	if len(Editor.SelectedVertices) != 0 {
		t.Errorf("selecting a face left %d points selected", len(Editor.SelectedVertices))
	}
}

func TestFaceSelectionClearsTheSpawnMarker(t *testing.T) {
	defer saveEditorSelection()()
	faceTestLevel(t)
	Editor.level.Spawn = &world.SpawnPoint{Position: mgl32.Vec3{}}
	editorSelectSpawn()

	setFaceSelection([]faceSel{bface(0, boxFaceTop)})
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
		{"a brush face", pickHit{kind: pickBrushFace, chunkIdx: 1, brushIdx: 2, faceIdx: 3},
			brushFaceSel(1, 2, 3), true},
		{"the spawn marker", pickHit{kind: pickSpawn}, faceSel{}, false},
		{"an entity", pickHit{kind: pickEntity, idx: 2}, faceSel{}, false},
		{"a volume", pickHit{kind: pickVolume, idx: 0}, faceSel{}, false},
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

// Every brush face has every field, so every pointer in the refs is non-nil —
// the per-kind nils a sector's six kinds of face needed are gone with them.
func TestSurfaceRefsResolveEveryField(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceTestLevel(t)

	r, ok := editor3DSurfaceRefsFor(bface(1, boxFaceTop))
	if !ok {
		t.Fatal("a face of the second brush did not resolve")
	}
	if r.Texture != &ch.Brushes[1].Faces[boxFaceTop].Texture {
		t.Error("the refs did not point at the face's own texture field")
	}
	if r.Smooth == nil || r.DoubleSided == nil {
		t.Error("a brush face carries every flag, so none of the refs may be nil")
	}
	if r.b != &ch.Brushes[1] || r.f != &ch.Brushes[1].Faces[boxFaceTop] {
		t.Error("the refs did not point at the named brush and face")
	}

	if _, ok := editor3DSurfaceRefsFor(bface(0, 99)); ok {
		t.Error("a face past the end resolved to refs")
	}
}

// The primary is the first entry: what the panel shows, and what a toggle takes
// its "before" value from.
func TestSurfaceRefsFollowsThePrimary(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceTestLevel(t)

	setFaceSelection([]faceSel{bface(1, boxFaceNegZ), bface(0, boxFaceTop)})
	r, ok := editor3DSurfaceRefs()
	if !ok || r.Texture != &ch.Brushes[1].Faces[boxFaceNegZ].Texture {
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

	setFaceSelection([]faceSel{bface(0, boxFaceNegX), bface(0, boxFaceTop), bface(1, boxFacePosZ)})
	n := applySelectedFaceEdits(func(r surface3DRefs) { *r.Texture = "marble" })
	if n != 3 {
		t.Fatalf("reached %d faces, want 3", n)
	}
	for _, at := range []struct {
		bi, fi int
	}{{0, boxFaceNegX}, {0, boxFaceTop}, {1, boxFacePosZ}} {
		if got := ch.Brushes[at.bi].Faces[at.fi].Texture; got != "marble" {
			t.Errorf("brush %d face %d texture = %q, want marble", at.bi, at.fi, got)
		}
	}
	// And nothing else was touched.
	if ch.Brushes[0].Faces[boxFacePosX].Texture == "marble" {
		t.Error("an unselected face was retextured")
	}
}

// A toggle sets every selected face to the same value — the opposite of the
// primary's — rather than flipping each independently, which would leave a
// mixed selection exactly as mixed as it started.
func TestToggleSetsEverySelectedFaceToMatchThePrimary(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceTestLevel(t)

	// NoCollide is stored the way round that makes the zero value solid, so a
	// mixed selection here is "two solid, one not".
	ch.Brushes[0].Faces[boxFaceNegX].NoCollide = false // the primary
	ch.Brushes[0].Faces[boxFacePosX].NoCollide = true
	ch.Brushes[0].Faces[boxFaceTop].NoCollide = false
	faces := []int{boxFaceNegX, boxFacePosX, boxFaceTop}
	sels := make([]faceSel, len(faces))
	for i, fi := range faces {
		sels[i] = bface(0, fi)
	}
	setFaceSelection(sels)

	want := !faceSolid(&ch.Brushes[0].Faces[boxFaceNegX])
	applySelectedFaceEdits(func(r surface3DRefs) { setFaceSolid(r.f, want) })

	for _, fi := range faces {
		if got := faceSolid(&ch.Brushes[0].Faces[fi]); got != want {
			t.Errorf("face %d solid = %v, want %v — all three should match", fi, got, want)
		}
	}
}

// ---------- selecting a whole brush ----------

func TestSelectBrushFacesTakesEverySide(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceTestLevel(t)
	setFaceSelection([]faceSel{bface(1, boxFaceNegX)})

	editor3DSelectBrushFaces()
	if got, want := selectedFaceCount(), len(ch.Brushes[1].Faces); got != want {
		t.Fatalf("selected %d faces, want %d", got, want)
	}
	for fi := range ch.Brushes[1].Faces {
		if !isFaceSelected(bface(1, fi)) {
			t.Errorf("face %d was left out", fi)
		}
	}
}

// "Sides" is a threshold on the face normal rather than an authored kind,
// because a brush face has no kind: a box's four upright faces qualify and its
// top and bottom do not.
func TestSelectBrushSideFacesDropsTheHorizontalOnes(t *testing.T) {
	defer saveEditorSelection()()
	faceTestLevel(t)
	setFaceSelection([]faceSel{bface(0, boxFaceNegX)})

	editor3DSelectBrushSideFaces()
	if got := selectedFaceCount(); got != 4 {
		t.Fatalf("selected %d faces, want the box's 4 upright sides", got)
	}
	if isFaceSelected(bface(0, boxFaceTop)) || isFaceSelected(bface(0, boxFaceBottom)) {
		t.Error("a horizontal face was taken as a side")
	}
}
