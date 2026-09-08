package editor

// editor_slab_sel_test.go — a slab's far face as a selection of its own.
//
// The point of giving a floor a thickness is being able to texture what you see
// from underneath separately from what you walk on, and that only works if the
// underside is a face the editor can hold on its own: pickable, valid, pointing
// the panel at its own section and greying out the actions that make no sense
// for it.

import (
	"foundry/internal/testenv"
	"foundry/internal/world"
	"strings"
	"testing"
)

func floorSlabFace(si int) faceSel   { return surfaceFaceSel(0, si, "floor_slab") }
func ceilingSlabFace(si int) faceSel { return surfaceFaceSel(0, si, "ceiling_slab") }

// A pick on a slab has to become a slab face, not the surface it hangs off:
// resolving it to the floor would make the underside untexturable, which is the
// state the feature exists to fix.
func TestFaceSelFromSlabHit(t *testing.T) {
	cases := []struct {
		name string
		hit  pickHit
		want faceSel
	}{
		{"a floor's underside", pickHit{kind: pickFloorSlab, chunkIdx: 0, sectorIdx: 4, idx: -1},
			surfaceFaceSel(0, 4, "floor_slab")},
		{"a ceiling's top side", pickHit{kind: pickCeilingSlab, chunkIdx: 0, sectorIdx: 4, idx: -1},
			surfaceFaceSel(0, 4, "ceiling_slab")},
	}
	for _, c := range cases {
		got, ok := faceSelFromHit(c.hit)
		if !ok {
			t.Errorf("%s: not resolved to a face at all", c.name)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

// A slab that has been thinned back to nothing has no geometry in the viewport,
// so a selection naming it is stale — the same way a wall index past the end of
// the slice is. Left valid, it would outline a face that is not there and open
// a panel for a solid that no longer exists.
func TestSlabFaceIsValidOnlyWithThickness(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceTestLevel(t)

	if faceSelValid(floorSlabFace(0)) {
		t.Error("a floor with no thickness has a valid underside face")
	}
	ch.Sectors[0].Floor.Slab.Thickness = 0.25
	if !faceSelValid(floorSlabFace(0)) {
		t.Error("a floor with a thickness has no valid underside face")
	}
	ch.Sectors[0].Ceiling.Slab.Thickness = 0.25
	if !faceSelValid(ceilingSlabFace(0)) {
		t.Error("a ceiling with a thickness has no valid top-side face")
	}
}

// Clicking the underside opens the Underside group, not merely the Floor group
// it is nested in — opening the outer one alone leaves the controls folded away,
// which is exactly as useful as opening nothing.
func TestSlabFacePointsThePanelAtItsOwnSection(t *testing.T) {
	if got := faceSelPanelFocus(floorSlabFace(0)); got != panelFocusFloorSlab {
		t.Errorf("floor underside focus = %q, want %q", got, panelFocusFloorSlab)
	}
	if got := faceSelPanelFocus(ceilingSlabFace(0)); got != panelFocusCeilingSlab {
		t.Errorf("ceiling top side focus = %q, want %q", got, panelFocusCeilingSlab)
	}
	// The surface's own group has to open too, since the slab's lives inside it.
	if got := slabPanelFocus("floor"); got != panelFocusFloorSlab {
		t.Errorf("slabPanelFocus(floor) = %q, want %q", got, panelFocusFloorSlab)
	}
	if got := slabPanelFocus("wall"); got != "" {
		t.Errorf("a wall has no slab, but slabPanelFocus said %q", got)
	}
}

// The right-click menu's texture row acts on whatever is selected, so it has to
// reach the slab's own texture rather than the floor's — otherwise retexturing
// a selected underside silently retextures the thing above it.
func TestSlabRefsPointAtTheSlabsOwnFields(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceTestLevel(t)
	s := &ch.Sectors[0]
	s.Floor.Texture = "stone_floor"
	s.Floor.Slab.Thickness = 0.25

	r, ok := editor3DSurfaceRefsFor(floorSlabFace(0))
	if !ok {
		t.Fatal("a slab face resolved to no editable fields")
	}
	if r.Texture == &s.Floor.Texture {
		t.Fatal("the underside's texture row points at the floor's own texture")
	}
	*r.Texture = "concrete"
	if s.Floor.Slab.Texture != "concrete" {
		t.Errorf("slab texture = %q, want %q", s.Floor.Slab.Texture, "concrete")
	}
	if s.Floor.Texture != "stone_floor" {
		t.Errorf("retexturing the underside changed the floor to %q", s.Floor.Texture)
	}
	// A slab is switched on by having a thickness and inherits its smoothing,
	// so neither row applies and the menu has to grey both out.
	if r.On != nil {
		t.Error("a slab offered an Enabled toggle; thickness is its only switch")
	}
	if r.Smooth != nil {
		t.Error("a slab offered a Smooth toggle; smoothing is inherited from its surface")
	}
}

// A slab face's gizmo handles are the surface's, so grabbing an underside and
// dragging moves the floor and takes the slab with it. It has no corners of its
// own to move independently.
func TestSlabFaceTakesItsSurfacesHandles(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceTestLevel(t)
	ch.Sectors[0].Floor.Slab.Thickness = 0.25

	slab := faceVertexSels(floorSlabFace(0))
	floor := faceVertexSels(floorFace(0))
	if len(slab) == 0 {
		t.Fatal("a slab face produced no gizmo handles")
	}
	if len(slab) != len(floor) {
		t.Fatalf("%d handles on the underside, %d on the floor; they must be the same set",
			len(slab), len(floor))
	}
	for i := range slab {
		if slab[i] != floor[i] {
			t.Errorf("handle %d differs: %+v vs the floor's %+v", i, slab[i], floor[i])
		}
	}
}

// The kind strings are a schema detail; the menu heading and the panel's
// "Selected face" line are read by a person.
func TestSlabKindLabelsReadAsSides(t *testing.T) {
	if got := editor3DKindLabel("floor_slab"); got != "Floor underside" {
		t.Errorf("floor_slab label = %q", got)
	}
	if got := editor3DKindLabel("ceiling_slab"); got != "Ceiling top side" {
		t.Errorf("ceiling_slab label = %q", got)
	}
	if got := editor3DKindLabel("wall"); got != "Wall" {
		t.Errorf("wall label = %q, want the plain capitalisation to be untouched", got)
	}
}

// ---------- shaping the far face ----------

// Shaping goes through the same two resolvers every other face does, so a slab
// face that does not answer them is a face surface mode cannot open at all.
func TestSlabFaceResolvesForShaping(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceTestLevel(t)
	s := &ch.Sectors[0]
	s.Floor.Slab.Thickness = 0.5
	pts := world.SectorPoints(ch, s)

	mesh := s.SurfaceMeshFor("floor_slab", -1)
	if mesh != &s.Floor.Slab.Mesh {
		t.Error("the underside does not resolve to the slab's own mesh")
	}
	frame, ok := s.SurfaceFrameFor(pts, "floor_slab", -1)
	if !ok {
		t.Fatal("the underside resolves to no frame")
	}
	pf, isPlanar := frame.(world.PlanarSurfaceFrame)
	if !isPlanar {
		t.Fatalf("the underside's frame is %T, want a planar one", frame)
	}
	if want := s.Floor.Height - 0.5; pf.BaseHeight != want {
		t.Errorf("underside frame base at %v, want %v", pf.BaseHeight, want)
	}
	// The hole through a floor is a hole through the whole solid.
	s.Floor.Openings = []world.Opening{{ID: "well"}}
	if got := s.SurfaceOpeningsFor("floor_slab"); len(got) != 1 {
		t.Errorf("the underside is cut by %d openings, want the floor's 1", len(got))
	}
}

// Entering shaping seeds the mesh; leaving it "following" again empties it.
// Both go through the panel, so the round trip is what the button pair means.
func TestShapingASlabDetachesItAndFollowingReattaches(t *testing.T) {
	// EnterSurfaceEdit seeds the mesh and rebuilds the sector, which uploads.
	testenv.WithoutGeometryUploads(t)
	defer saveEditorSelection()()
	ch := faceTestLevel(t)
	s := &ch.Sectors[0]
	s.Floor.Slab.Thickness = 0.5

	EnterSurfaceEdit(floorSlabFace(0))
	if !surfaceEditActive() {
		t.Fatal("surface mode did not open on the underside")
	}
	if !s.Floor.Slab.Shaped() {
		t.Error("entering shaping left the slab still following the floor")
	}
	if Editor.SurfaceFace != floorSlabFace(0) {
		t.Errorf("shaping %+v, want the underside", Editor.SurfaceFace)
	}

	// The panel's way back: empty the mesh and the mirror resumes.
	s.Floor.Slab.Mesh = world.SurfaceMesh{}
	if s.Floor.Slab.Shaped() {
		t.Error("emptying the mesh left the slab reporting as shaped")
	}
	if got := s.Floor.Slab.ShapeMesh(&s.Floor.Mesh); got != &s.Floor.Mesh {
		t.Error("a re-following slab does not build from the floor's mesh again")
	}
}

// The menu's Shape row acts on the primary selection, so it has to be offered
// for a slab face rather than greyed out — that row is the only way in from the
// viewport.
func TestShapeRowIsOfferedForASlabFace(t *testing.T) {
	defer saveEditorSelection()()
	ch := faceTestLevel(t)
	ch.Sectors[0].Floor.Slab.Thickness = 0.5
	setFaceSelection([]faceSel{floorSlabFace(0)})

	var shape *ctxMenuItem
	for i, it := range editor3DSurfaceMenuItems() {
		if strings.HasPrefix(it.Label, "Shape ") {
			shape = &editor3DSurfaceMenuItems()[i]
			break
		}
	}
	if shape == nil {
		t.Fatal("no Shape row for a selected underside")
	}
	if shape.Disabled {
		t.Error("the Shape row is greyed out for an underside")
	}
	if !strings.Contains(shape.Label, "underside") {
		t.Errorf("Shape row reads %q; it should name the side, not the kind string", shape.Label)
	}
}
