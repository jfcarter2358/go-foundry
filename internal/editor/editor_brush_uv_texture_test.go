package editor

// editor_brush_uv_texture_test.go — which faces a brush unwrap reaches, what it
// leaves behind, and what the texture editor's overlay is told.
//
// The texture itself is not made here: brushUnwrapToTexture writes a file and
// uploads to GL, and the part of it worth asserting on — which faces, laid out
// how, wearing what — is brushUnwrapFaces, which does neither.

import (
	"testing"

	"foundry/internal/geom"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// brushUnwrapFixture is brushTestLevel with the editor and its undo history put
// back afterwards: an unwrap pushes a snapshot, and a test should not leave one
// on the stack for the next.
func brushUnwrapFixture(t *testing.T) *world.Chunk {
	t.Helper()
	restore := saveEditorState()
	t.Cleanup(func() {
		restore()
		EditorClearUndoHistory()
	})
	ch := brushTestLevel(t)
	Editor.SelectedBrushes = nil
	Editor.EditContext = editorObjectMode
	EditorClearUndoHistory()
	return ch
}

// Object mode means whole brushes — but only the faces that are drawn, since a
// buried face would spend texels on nothing.
func TestBrushUnwrapTakesEveryDrawnFaceOfASelectedBrush(t *testing.T) {
	ch := brushUnwrapFixture(t)
	ch.Brushes[0].Faces[boxFaceBottom].NoDraw = true
	Editor.SelectedBrushes = []brushSel{{chunkIdx: 0, brushIdx: 0}}

	if got := len(brushUnwrapSelection()); got != 5 {
		t.Errorf("selection reaches %d faces, want the box's 5 drawn ones", got)
	}
}

// Edit mode means the picked faces, in level order however they were clicked,
// so a re-unwrap lays them out the same way.
func TestBrushUnwrapTakesPickedFacesInLevelOrder(t *testing.T) {
	brushUnwrapFixture(t)
	Editor.SelectedFaces = []faceSel{
		brushFaceSel(0, 1, boxFaceTop),
		brushFaceSel(0, 0, boxFaceTop),
		brushFaceSel(0, 1, boxFaceTop), // twice
	}
	got := brushUnwrapSelection()
	if len(got) != 2 {
		t.Fatalf("selection reaches %d faces, want 2", len(got))
	}
	if got[0].brushIdx != 0 || got[1].brushIdx != 1 {
		t.Errorf("faces came back as brushes %d, %d — want level order 0, 1", got[0].brushIdx, got[1].brushIdx)
	}
}

// A detailed brush is drawn from its detail mesh, so its hull faces are not
// surface anyone sees and must not take a region of the atlas.
func TestBrushUnwrapSkipsHullFacesUnderADetailMesh(t *testing.T) {
	ch := brushUnwrapFixture(t)
	ch.Brushes[0].SeedDetail()

	Editor.SelectedFaces = []faceSel{brushFaceSel(0, 0, boxFaceTop)}
	if got := len(brushUnwrapSelection()); got != 0 {
		t.Errorf("a hull face under a detail mesh was taken (%d faces)", got)
	}
	Editor.SelectedFaces = []faceSel{brushDetailFaceSel(0, 0, boxFaceTop)}
	if got := len(brushUnwrapSelection()); got != 1 {
		t.Errorf("the detail face drawn in its place was not taken (%d faces)", got)
	}
}

// The rule the whole feature rests on: faces already wearing the atlas are laid
// out WITH the selection, so the new layout cannot hand their texels to
// somebody else. Everything unrelated is left exactly as it was.
func TestBrushUnwrapLaysOutFacesAlreadyWearingTheAtlas(t *testing.T) {
	ch := brushUnwrapFixture(t)
	Editor.SelectedBrushes = []brushSel{{chunkIdx: 0, brushIdx: 0}}
	ch.Brushes[1].Faces[boxFaceTop].Texture = "room"

	n, lay, err := brushUnwrapFaces("room", 512, 60, 4)
	if err != nil {
		t.Fatalf("brushUnwrapFaces: %v", err)
	}
	if n != 7 || lay.Islands != 7 {
		t.Errorf("laid out %d faces in %d islands, want 7 of each — the selected box and the face wearing the atlas", n, lay.Islands)
	}
	for fi := range ch.Brushes[0].Faces {
		f := &ch.Brushes[0].Faces[fi]
		if f.Texture != "room" || !f.HasUVs() {
			t.Errorf("selected face %d: texture %q, laid out %v", fi, f.Texture, f.HasUVs())
		}
	}
	if !ch.Brushes[1].Faces[boxFaceTop].HasUVs() {
		t.Error("the face already wearing the atlas was not laid out with the selection")
	}
	if f := &ch.Brushes[1].Faces[boxFaceNegX]; f.Texture != "concrete" || f.UVs != nil {
		t.Errorf("an unrelated face was touched: texture %q, UVs %v", f.Texture, f.UVs)
	}
	if !EditorCanUndo() {
		t.Error("the unwrap left nothing to undo")
	}
}

// An unwrap with nothing to lay out says so, and leaves no empty undo step.
func TestBrushUnwrapWithNothingSelectedChangesNothing(t *testing.T) {
	brushUnwrapFixture(t)
	if _, _, err := brushUnwrapFaces("room", 512, 60, 4); err == nil {
		t.Error("an unwrap of nothing reported success")
	}
	if EditorCanUndo() {
		t.Error("a failed unwrap pushed an undo step")
	}
}

// The overlay outlines unwrapped faces wearing the texture, and nothing else: a
// face projecting that texture from its plane would draw its outline over the
// whole image and bury the layout.
func TestBrushUVLayoutOutlinesOnlyUnwrappedFacesWearingTheTexture(t *testing.T) {
	ch := brushUnwrapFixture(t)
	Editor.SelectedBrushes = []brushSel{{chunkIdx: 0, brushIdx: 0}}
	if _, _, err := brushUnwrapFaces("room", 512, 60, 4); err != nil {
		t.Fatalf("brushUnwrapFaces: %v", err)
	}
	ch.Brushes[1].Faces[boxFaceNegX].Texture = "room" // wearing it, never unwrapped

	if got := len(brushUVLayoutFor(Editor.level, "room")); got != 6 {
		t.Errorf("layout has %d loops, want the 6 unwrapped faces", got)
	}
	if got := brushUVLayoutFor(Editor.level, "concrete"); got != nil {
		t.Errorf("layout drew %d loops for a tiling texture", len(got))
	}
	if got := brushUVLayoutFor(Editor.level, ""); got != nil {
		t.Error("layout drew loops for no texture at all")
	}
}

// unwrapWholeBrush lays every face of b out on its own, as a test's starting
// point for a mesh operation.
func unwrapWholeBrush(t *testing.T, b *world.Brush) {
	t.Helper()
	var faces []world.AtlasFace
	for fi := range b.Faces {
		faces = append(faces, world.AtlasFace{Mesh: &b.BrushMesh, Face: fi})
	}
	lay, err := world.LayoutBrushAtlas(faces, geom.AtlasOptions{})
	if err != nil {
		t.Fatalf("LayoutBrushAtlas: %v", err)
	}
	world.ApplyBrushAtlas(faces, lay)
}

// Subdividing a painted face must keep every piece on the parent's island, and
// the neighbours that gain a corner along the split must keep theirs too — or a
// subdivide would quietly un-paint the faces around it.
func TestSubdivideKeepsAnUnwrappedFaceOnItsIsland(t *testing.T) {
	ch := brushUnwrapFixture(t)
	b := &ch.Brushes[0]
	unwrapWholeBrush(t, b)

	parent := b.Faces[boxFaceTop].UVs
	lo, hi := parent[0], parent[0]
	for _, uv := range parent {
		lo = mgl32.Vec2{min(lo.X(), uv.X()), min(lo.Y(), uv.Y())}
		hi = mgl32.Vec2{max(hi.X(), uv.X()), max(hi.Y(), uv.Y())}
	}

	kids := meshSubdivideFaces(&b.BrushMesh, []int{boxFaceTop})
	if len(kids) != 4 {
		t.Fatalf("subdivide made %d faces, want 4", len(kids))
	}
	const eps = 1e-5
	for _, k := range kids {
		f := &b.Faces[k]
		if !f.HasUVs() {
			t.Fatalf("piece %d lost its UVs", k)
		}
		for _, uv := range f.UVs {
			if uv.X() < lo.X()-eps || uv.Y() < lo.Y()-eps || uv.X() > hi.X()+eps || uv.Y() > hi.Y()+eps {
				t.Errorf("piece %d samples %v, off the parent's island %v..%v", k, uv, lo, hi)
			}
		}
	}
	for fi := range b.Faces {
		if !b.Faces[fi].HasUVs() {
			t.Errorf("face %d lost its UVs when a neighbour was subdivided", fi)
		}
	}
}

// An extrude's side walls are new surface: they project from their own plane
// until they are unwrapped, rather than inheriting coordinates that point into
// another face's region. The cap keeps its own.
func TestExtrudeSidesOfAnUnwrappedFaceProjectFromTheirPlane(t *testing.T) {
	ch := brushUnwrapFixture(t)
	b := &ch.Brushes[0]
	unwrapWholeBrush(t, b)
	before := len(b.Faces)

	caps := meshExtrudeFaces(&b.BrushMesh, []int{boxFaceTop}, 0.5)
	if len(caps) != 1 {
		t.Fatalf("extrude returned %d caps, want 1", len(caps))
	}
	if !b.Faces[caps[0]].HasUVs() {
		t.Error("the extruded cap lost its UVs")
	}
	if len(b.Faces) == before {
		t.Fatal("extrude added no side walls")
	}
	for fi := before; fi < len(b.Faces); fi++ {
		if b.Faces[fi].UVs != nil {
			t.Errorf("side wall %d carries UVs %v no unwrap wrote", fi, b.Faces[fi].UVs)
		}
	}
}

// The highlight is the selected faces that are laid out on the texture — a
// selected brush's unwrapped islands, not a neighbour's, and none of them on a
// texture they do not wear.
func TestBrushUVSelectionHighlightsOnlySelectedUnwrappedFaces(t *testing.T) {
	brushUnwrapFixture(t)
	Editor.SelectedBrushes = []brushSel{{chunkIdx: 0, brushIdx: 0}, {chunkIdx: 0, brushIdx: 1}}
	if _, _, err := brushUnwrapFaces("room", 512, 60, 4); err != nil {
		t.Fatalf("brushUnwrapFaces: %v", err)
	}

	Editor.SelectedBrushes = nil
	Editor.SelectedFaces = []faceSel{brushFaceSel(0, 1, boxFaceTop)}
	got := brushUVSelectionFor(&Editor.brushWorkspace, "room")
	if len(got) != 1 {
		t.Fatalf("selection loops = %d, want the one picked face", len(got))
	}
	_, _, m, f := brushAtlasFace{chunkIdx: 0, brushIdx: 1, faceIdx: boxFaceTop}.resolve(Editor.level)
	want := m.FaceUVs(f)
	for i := range want {
		if got[0][i] != want[i] {
			t.Errorf("corner %d = %v, want %v", i, got[0][i], want[i])
		}
	}
	if got := brushUVSelectionFor(&Editor.brushWorkspace, "concrete"); got != nil {
		t.Errorf("selection highlighted %d loops on a texture the face does not wear", len(got))
	}
}
