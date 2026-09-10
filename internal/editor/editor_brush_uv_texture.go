package editor

// editor_brush_uv_texture.go — "unwrap these brushes onto a texture I can paint".
//
// The level's counterpart of the model editor's Unwrap & Paint
// (editor_model_uv_texture.go), and most of it IS that feature: the same form,
// the same guide-and-paint-layer texture saved on the spot, the same deferred
// hand-off to the texture editor, and the same live update for the same reason
// — a face resolves its texture by name when it is drawn, so a stroke shows up
// on the brushes the moment the texture editor re-uploads.
//
// What differs is what a texture belongs to. A model wears one, so the faces an
// atlas lays out are simply all of them. A brush face wears its own, and a level
// has hundreds wearing tiling materials no unwrap should touch. So the faces are
//
//   - the selection — whole brushes in object mode, the picked faces in edit and
//     detail mode — and
//   - every other drawn face in the workspace already wearing the texture being
//     unwrapped onto.
//
// The second half is the model editor's "never a partial atlas" rule, restated
// for brushes. Unwrapping one new brush onto a room's atlas lays the whole room
// out again, rather than packing the brush into texels the room's faces are
// still sampling. The form says how many such faces there are before it runs,
// because a name that happens to belong to a tiling material would sweep up
// every face wearing it.
//
// Only DRAWN faces take part: not a face with Drawn off, and not a hull face
// hidden under a detail mesh. Both would spend texels on surface nobody sees.
//
// Everything reads ws(), so the entity editor's brushes get this too.

import (
	"fmt"
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"

	"foundry/internal/geom"
	"foundry/internal/imguibackend"
	"foundry/internal/texdoc"
	"foundry/internal/textureui"
	"foundry/internal/world"
)

// brushAtlasFace names one face an unwrap lays out. detail says which of the
// brush's two meshes faceIdx indexes, as it does on a faceSel.
type brushAtlasFace struct {
	chunkIdx, brushIdx, faceIdx int
	detail                      bool
}

// resolve returns the face's chunk, brush, mesh and face — or all nil when the
// indices no longer name a face that is drawn.
func (a brushAtlasFace) resolve(lvl *world.Level) (*world.Chunk, *world.Brush, *world.BrushMesh, *world.BrushFace) {
	if lvl == nil || a.chunkIdx < 0 || a.chunkIdx >= len(lvl.Chunks) {
		return nil, nil, nil, nil
	}
	ch := &lvl.Chunks[a.chunkIdx]
	if a.brushIdx < 0 || a.brushIdx >= len(ch.Brushes) {
		return nil, nil, nil, nil
	}
	b := &ch.Brushes[a.brushIdx]
	if a.detail != b.HasDetail() {
		return nil, nil, nil, nil // a face of the mesh that is not on screen
	}
	m := b.DrawMesh()
	if a.faceIdx < 0 || a.faceIdx >= len(m.Faces) || m.Faces[a.faceIdx].NoDraw {
		return nil, nil, nil, nil
	}
	return ch, b, m, &m.Faces[a.faceIdx]
}

// sortBrushAtlasFaces orders faces the way the level stores them and drops
// repeats. Level order, not selection order, so unwrapping the same faces twice
// lays them out the same way however they were clicked.
func sortBrushAtlasFaces(faces []brushAtlasFace) []brushAtlasFace {
	sort.Slice(faces, func(i, j int) bool {
		a, b := faces[i], faces[j]
		if a.chunkIdx != b.chunkIdx {
			return a.chunkIdx < b.chunkIdx
		}
		if a.brushIdx != b.brushIdx {
			return a.brushIdx < b.brushIdx
		}
		return a.faceIdx < b.faceIdx
	})
	out := faces[:0]
	for i, f := range faces {
		if i > 0 && f == faces[i-1] {
			continue
		}
		out = append(out, f)
	}
	return out
}

// brushUnwrapSelection is the drawn faces the selection covers: every face of
// each selected brush in object mode, or the selected faces otherwise.
func brushUnwrapSelection() []brushAtlasFace {
	lvl := ws().level
	if lvl == nil {
		return nil
	}
	var out []brushAtlasFace
	if len(ws().SelectedBrushes) > 0 {
		for _, s := range ws().SelectedBrushes {
			if !brushSelValid(s) {
				continue
			}
			b := &lvl.Chunks[s.chunkIdx].Brushes[s.brushIdx]
			for fi := range b.DrawFaces() {
				a := brushAtlasFace{chunkIdx: s.chunkIdx, brushIdx: s.brushIdx, faceIdx: fi, detail: b.HasDetail()}
				if _, _, _, f := a.resolve(lvl); f != nil {
					out = append(out, a)
				}
			}
		}
	} else {
		for _, sel := range ws().SelectedFaces {
			a := brushAtlasFace{chunkIdx: sel.chunkIdx, brushIdx: sel.brushIdx, faceIdx: sel.faceIdx, detail: sel.detail}
			if _, _, _, f := a.resolve(lvl); f != nil {
				out = append(out, a)
			}
		}
	}
	return sortBrushAtlasFaces(out)
}

// brushFacesWearing lists every drawn face in lvl whose texture is tex, in level
// order.
func brushFacesWearing(lvl *world.Level, tex string) []brushAtlasFace {
	if lvl == nil || tex == "" {
		return nil
	}
	var out []brushAtlasFace
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		for bi := range ch.Brushes {
			b := &ch.Brushes[bi]
			faces := b.DrawFaces()
			for fi := range faces {
				if !faces[fi].NoDraw && faces[fi].Texture == tex {
					out = append(out, brushAtlasFace{chunkIdx: ci, brushIdx: bi, faceIdx: fi, detail: b.HasDetail()})
				}
			}
		}
	}
	return out
}

// brushUnwrapTargets is every face an unwrap onto tex lays out: the selection,
// and every face already wearing tex. See the file comment for why both.
func brushUnwrapTargets(tex string) []brushAtlasFace {
	faces := append(brushUnwrapSelection(), brushFacesWearing(ws().level, tex)...)
	return sortBrushAtlasFaces(faces)
}

// ---------- the action ----------

// brushUnwrapFaces lays the targets out on one atlas and points every one of
// them at tex, returning how many faces that was and the layout. It makes no
// texture — brushUnwrapToTexture does — so it runs with no disk and no GL.
//
// The layout is worked out BEFORE the undo snapshot. An unwrap that cannot fit
// then leaves nothing on the stack, which matters most in the entity editor,
// whose stack has no way to take an entry back off.
func brushUnwrapFaces(tex string, size int, angleDeg, padTexels float32) (int, geom.AtlasLayout, error) {
	lvl := ws().level
	targets := brushUnwrapTargets(tex)
	if len(targets) == 0 {
		return 0, geom.AtlasLayout{}, fmt.Errorf("nothing to unwrap: select brushes, or the faces to paint in edit mode")
	}

	faces := make([]world.AtlasFace, 0, len(targets))
	for _, a := range targets {
		ch, _, m, _ := a.resolve(lvl)
		faces = append(faces, world.AtlasFace{Mesh: m, Face: a.faceIdx, Xform: ch.ModelMatrix()})
	}
	layout, err := world.LayoutBrushAtlas(faces, geom.AtlasOptions{
		AngleLimit: angleDeg * math.Pi / 180,
		PaddingUV:  atlasPadUV(padTexels, size),
		Aspect:     1, // the atlas is square; see the size options
	})
	if err != nil {
		return 0, geom.AtlasLayout{}, err
	}

	// One undo entry for the layout and the retexture together: undoing half
	// would leave faces laid out for an atlas they no longer wear, or wearing
	// one they are not laid out for.
	ws().pushUndo()
	world.ApplyBrushAtlas(faces, layout)
	touched := map[*world.Brush]bool{}
	for _, a := range targets {
		_, b, _, f := a.resolve(lvl)
		f.Texture = tex
		touched[b] = true
	}
	for b := range touched {
		_ = b.Load()
	}
	ws().geometryChanged()
	return len(targets), layout, nil
}

// brushUnwrapToTexture is the whole feature in one call: lay the faces out,
// build and save the texture they were laid out for, and hand it to the texture
// editor.
func brushUnwrapToTexture(name string, size int, angleDeg, padTexels float32) error {
	name = sanitizeTextureName(name)
	if name == "" {
		return fmt.Errorf("texture name is empty")
	}
	if err := texdoc.ValidTexSize(size, size); err != nil {
		return err
	}
	n, layout, err := brushUnwrapFaces(name, size, angleDeg, padTexels)
	if err != nil {
		return err
	}
	ed, err := buildAtlasTexture(name, size)
	if err != nil {
		return err
	}
	requestTexEditor(ed)
	// Texel density is uniform across the atlas, so one number describes all of
	// it — and it is the number that says whether the texture is big enough for
	// the room it has been stretched over.
	ws().setStatus("Unwrapped %d faces into %d islands on %s (%d², %.1f texels per unit)",
		n, layout.Islands, name, size, layout.Scale*float32(size))
	return nil
}

// ---------- the form ----------

// brushOpenUnwrapDialog arms this window's copy of the form.
func brushOpenUnwrapDialog() {
	ws().UnwrapDialog.open(brushUnwrapSuggestedName())
}

// brushUnwrapSuggestedName offers the atlas a selected face already wears —
// re-unwrapping onto it is the common case once a surface has been through this
// once — or else a name from the brush, or from the chunk when the selection
// spans several brushes.
func brushUnwrapSuggestedName() string {
	lvl := ws().level
	sel := brushUnwrapSelection()
	for _, a := range sel {
		if _, _, _, f := a.resolve(lvl); f.HasUVs() && f.Texture != "" {
			return f.Texture
		}
	}
	if len(sel) > 0 {
		ch, b, _, _ := sel[0].resolve(lvl)
		id := b.ID
		for _, a := range sel[1:] {
			if a.chunkIdx != sel[0].chunkIdx || a.brushIdx != sel[0].brushIdx {
				id = ch.ID
				break
			}
		}
		if name := sanitizeTextureName(id); name != "" {
			return name
		}
	}
	return "brush_atlas"
}

// drawBrushUnwrapDialog draws this window's copy of the form. Called with the
// window's other modals, after its panel has ended.
func drawBrushUnwrapDialog() {
	d := &ws().UnwrapDialog
	imguibackend.WithModalPopup("Unwrap & Paint##brushunwrapdlg", &d.Show, func() {
		d.body("brush", "Lay the selection out on one texture and paint it.",
			brushUnwrapNote, brushUnwrapToTexture)
	})
}

// brushUnwrapNote says what the unwrap will reach before it runs — above all,
// the faces it will take that are not selected.
func brushUnwrapNote(name string) {
	sel := brushUnwrapSelection()
	imgui.Text(fmt.Sprintf("%d selected face(s).", len(sel)))
	chosen := make(map[brushAtlasFace]bool, len(sel))
	for _, a := range sel {
		chosen[a] = true
	}
	others := 0
	for _, a := range brushFacesWearing(ws().level, name) {
		if !chosen[a] {
			others++
		}
	}
	if others > 0 {
		editorTextMutedWrapped(fmt.Sprintf(
			"%d more face(s) already wear %q and will be laid out with them, "+
				"so that every face keeps a region of its own.", others, name))
	}
}

// ---------- the panel ----------

// brushPaintPanel is the brush's way into painting: lay the selection out on an
// atlas, or — once that atlas is open in the texture editor — go back to it.
func brushPaintPanel() {
	editorPropGroup("Paint##brushpaint", 0, func() {
		editorTextMutedWrapped("Lay the selection out on a texture of its own and paint it. " +
			"The level updates as you paint.")
		if imgui.Button("Unwrap && Paint...##brushunwrap") {
			brushOpenUnwrapDialog()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Give every selected face its own region of one new texture,\n" +
				"point them at it, and open it in the texture editor.\n" +
				"Faces already wearing that texture are laid out too.")
		}
		// Shown only once the two are linked, as the model editor's is: a
		// disabled button with no explanation reads as broken.
		if brushSelectionIsBoundForPainting() {
			imgui.SameLine()
			if imgui.Button("Paint##brushpaintgo") {
				requestTexEditor(nil)
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip("Raise the texture editor on this atlas.")
			}
		}
	})
}

// brushSelectionIsBoundForPainting reports whether the texture editor has open
// an atlas a selected face is laid out on — the state in which painting updates
// the level live.
func brushSelectionIsBoundForPainting() bool {
	ed := textureui.ActiveTexEditor
	if ed == nil || ed.BoundTexture == "" {
		return false
	}
	for _, a := range brushUnwrapSelection() {
		if _, _, _, f := a.resolve(ws().level); f.HasUVs() && f.Texture == ed.BoundTexture {
			return true
		}
	}
	return false
}

// ---------- the UV layout overlay ----------

// brushUVLayoutFor returns the UV outline of every drawn face in lvl that wears
// tex and has been laid out by an unwrap — what the texture editor draws over
// its canvas.
//
// Unwrapped faces only. A face projecting a tiling texture from its plane covers
// the image many times over, and outlining it would bury the layout the overlay
// exists to show. Like the model's, this is derived on demand, so dragging a
// corner in the viewport moves its outline on the canvas in the same frame.
func brushUVLayoutFor(lvl *world.Level, tex string) [][]mgl32.Vec2 {
	var out [][]mgl32.Vec2
	for _, a := range brushFacesWearing(lvl, tex) {
		if _, _, m, f := a.resolve(lvl); f.HasUVs() {
			out = append(out, m.FaceUVs(f))
		}
	}
	return out
}

// uvLayoutFor is what the texture editor's overlay asks for: every open
// document's layout on tex — the model editor's model, the level's brushes, and
// the brushes of the type open in the entity editor.
func uvLayoutFor(tex string) [][]mgl32.Vec2 {
	out := modelUVLayoutFor(tex)
	out = append(out, brushUVLayoutFor(Editor.level, tex)...)
	if entityDef != nil {
		out = append(out, brushUVLayoutFor(entityWS.level, tex)...)
	}
	return out
}
