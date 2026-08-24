package main

// editor_model_uv_texture.go — "unwrap this model onto a texture I can paint".
//
// This is the seam between the two editors. The model editor owns a mesh, the
// texture editor owns an image, and neither knew about the other: you could
// point a model at a texture that already existed, but there was no way to get
// a texture *shaped like* a particular model. Unwrap & Paint is that step —
// atlas-unwrap the mesh (model_uv_atlas.go), create a texture whose layout
// matches the result, point the model at it, and open it for painting.
//
// The live update needs no machinery at all, which is worth being explicit
// about because it looks like it should. Three facts do the work:
//
//  1. Every draw call resolves its texture as Textures[name] at draw time —
//     nothing caches a GL handle — so re-specifying that texture object shows
//     up everywhere the name is used, with no walk and no rebuild.
//  2. The texture editor already re-uploads its bound texture whenever a stroke
//     dirties the document (texEditorRefreshPreview), and the main loop drains
//     that queue each frame before anything draws.
//  3. Secondary windows share a GL context group (NewUIWindow asks for
//     GL_SHARE_WITH_CURRENT_CONTEXT), and texture objects — unlike VAOs — are
//     shared across it. So an upload made under the main context is the same
//     object the model editor's window samples.
//
// All this file has to do is make sure the model's texture name and the
// document's BoundTexture are the same string.

import (
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"

	"foundry/internal/texdoc"
)

// modelUnwrapDialogState is the Unwrap & Paint form. It lives across frames
// because imgui is immediate-mode: the widgets are rebuilt every frame and the
// values they edit cannot be.
var modelUnwrapDialog struct {
	Show      bool
	Name      string
	SizeIdx   int32   // index into texEditorSizeOptions
	AngleDeg  float32 // AtlasUnwrapOptions.AngleLimit, in degrees
	PadTexels float32 // gutter width, in texels of the chosen size
	Err       string  // last failure, shown in the form until it succeeds
}

// modelUnwrapDefaults seeds the form. The angle matches
// defaultAtlasAngleLimit; the padding matches defaultAtlasPaddingUV at 512².
const (
	modelUnwrapDefaultAngleDeg  = float32(60)
	modelUnwrapDefaultPadTexels = float32(4)
)

// modelOpenUnwrapDialog arms the form, seeding the texture name from whatever
// the model already wears — re-unwrapping onto the same texture is the common
// case once a model has been through this once.
func modelOpenUnwrapDialog() {
	if modelDoc == nil {
		return
	}
	name := modelDoc.Texture
	if name == "" || name == defaultModelTexture {
		name = modelUnwrapSuggestedName()
	}
	modelUnwrapDialog.Show = true
	modelUnwrapDialog.Name = name
	modelUnwrapDialog.SizeIdx = 3 // 512, same default as the texture editor's New
	modelUnwrapDialog.AngleDeg = modelUnwrapDefaultAngleDeg
	modelUnwrapDialog.PadTexels = modelUnwrapDefaultPadTexels
	modelUnwrapDialog.Err = ""
}

// modelUnwrapSuggestedName derives a texture name from the document's ID, so an
// unwrap of "crate" offers "crate" rather than "untitled".
func modelUnwrapSuggestedName() string {
	if modelDoc == nil {
		return "model_atlas"
	}
	name := sanitizeTextureName(modelDoc.ID)
	if name == "" {
		return "model_atlas"
	}
	return name
}

// sanitizeTextureName reduces s to something safe to use as a file stem: a
// texture name becomes textures/<name>.ftex and textures/<name>.png, so a name
// carrying a separator or a traversal would write outside the textures
// directory. Anything not a letter, digit, dash or underscore becomes an
// underscore.
func sanitizeTextureName(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('_')
		}
	}
	return b.String()
}

// ---------- the panel section ----------

// modelTextureSection is the document's texture: which one it wears, and the
// button that makes a new one shaped like the mesh.
//
// It is a whole-document section rather than part of the Face section it used
// to live in, because the texture is no longer a property of a selection — see
// the note on ModelDoc.Texture.
func modelTextureSection() {
	if !imgui.CollapsingHeaderV("Texture##modeltex", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}

	tex := modelTexture()
	if textureCombo("Texture##modeldoctex", &tex, false) {
		modelSetTexture(tex)
	}

	if imgui.Button("Unwrap && Paint...##modelunwrapatlas") {
		modelOpenUnwrapDialog()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Flatten the whole mesh into one texture, point the model\n" +
			"at it, and open it in the texture editor.\n" +
			"The model updates as you paint.")
	}

	// The button is only useful once the two are already linked, so it is shown
	// rather than greyed when they are not — a disabled button with no
	// explanation reads as broken.
	if modelTextureIsBoundForPainting() {
		imgui.SameLine()
		if imgui.Button("Paint##modelpaintatlas") {
			modelRequestTexEditor(nil)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Raise the texture editor on this model's texture.")
		}
	}
}

// modelTextureIsBoundForPainting reports whether the open texture document is
// the one this model wears — the state in which painting updates the mesh live.
func modelTextureIsBoundForPainting() bool {
	return modelDoc != nil && activeTexEditor != nil &&
		activeTexEditor.BoundTexture != "" &&
		activeTexEditor.BoundTexture == modelDoc.TextureName()
}

// ---------- the dialog ----------

// drawModelUnwrapDialog draws the form. Called once per frame from the model
// editor's panel, like the editor's other modals.
func drawModelUnwrapDialog() {
	withModalPopup("Unwrap & Paint##modelunwrapdlg", &modelUnwrapDialog.Show, func() {
		d := &modelUnwrapDialog

		imgui.Text("Flatten the mesh into one texture and paint it.")
		imgui.Separator()

		imgui.Text("Texture name:")
		imgui.SetNextItemWidth(200)
		imgui.InputText("##modelunwrapname", &d.Name)

		imgui.Text("Size (square):")
		sizeLabels := make([]string, len(texEditorSizeOptions))
		for i, s := range texEditorSizeOptions {
			sizeLabels[i] = fmt.Sprintf("%dx%d", s, s)
		}
		imgui.SetNextItemWidth(150)
		imgui.Combo("##modelunwrapsize", &d.SizeIdx, sizeLabels)

		// Degrees and texels drag by whole units; Editor.StepSize is a world
		// distance and would make both of these crawl.
		editorDragFloatStep("Seam angle (deg)##modelunwrapangle", &d.AngleDeg, 1, 179, "%.0f", 1)
		if imgui.IsItemHovered() {
			imgui.SetTooltip("How far two neighbouring faces may differ before the\n" +
				"unwrap cuts a seam between them. Lower makes more,\n" +
				"flatter islands; higher makes fewer, more distorted ones.")
		}

		editorDragFloatStep("Gutter (texels)##modelunwrappad", &d.PadTexels, 0, 32, "%.0f", 1)
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Blank space kept around each island so filtering and\n" +
				"mipmaps cannot bleed one island's colour into another.")
		}

		name := sanitizeTextureName(d.Name)
		if name != "" && texDocExistsOnDisk(name) {
			imgui.Separator()
			imgui.Text(fmt.Sprintf("Note: overwrites the existing texture %q.", name))
		}
		if d.Err != "" {
			imgui.Separator()
			imgui.Text("Failed: " + d.Err)
		}

		imgui.Separator()
		editorPushDisabled(name == "")
		if imgui.Button("Unwrap##modelunwrapok") {
			size := int(texEditorSizeOptions[d.SizeIdx])
			err := modelUnwrapToTexture(name, size, d.AngleDeg, d.PadTexels)
			if err != nil {
				d.Err = err.Error()
			} else {
				d.Err = ""
				imgui.CloseCurrentPopup()
			}
		}
		editorPopDisabled()
		imgui.SameLine()
		if imgui.Button("Cancel##modelunwrapcancel") {
			d.Err = ""
			imgui.CloseCurrentPopup()
		}
	})
}

// texDocExistsOnDisk reports whether a texture of this name is already saved.
func texDocExistsOnDisk(name string) bool {
	_, err := os.Stat(ftexPath(name))
	return err == nil
}

// ---------- the action ----------

// modelUnwrapToTexture is the whole feature in one call: atlas-unwrap the mesh,
// build a texture document laid out for the result, point the model at it, save
// it so the model is valid on disk, and hand it to the texture editor.
//
// The unwrap covers every face, never the selection. A partial atlas would
// leave the unselected faces mapped into whatever region of the new texture
// their old UVs happened to name — silently sharing texels with faces that were
// unwrapped, which is the exact overlap the atlas exists to avoid. Per-face UV
// work is still available in the UV section; this button means "lay out the
// model".
func modelUnwrapToTexture(name string, size int, angleDeg, padTexels float32) error {
	if modelDoc == nil {
		return fmt.Errorf("no model open")
	}
	name = sanitizeTextureName(name)
	if name == "" {
		return fmt.Errorf("texture name is empty")
	}
	if err := texdoc.ValidTexSize(size, size); err != nil {
		return err
	}

	// Padding is authored in texels because that is the unit the bleed happens
	// in — it is a property of the filter, not of the model.
	pad := padTexels / float32(size)
	if padTexels <= 0 {
		// A deliberate zero, which resolve() would otherwise read as "unset".
		pad = -1
	}

	// One undo entry for the whole thing: the unwrap and the retexture are one
	// action from the user's side, and undoing half of it would leave the model
	// wearing an atlas it is no longer laid out for.
	ModelPushUndo()

	islands, err := modelDoc.AtlasUnwrap(modelAllFaceIndices(), AtlasUnwrapOptions{
		AngleLimit: angleDeg * math.Pi / 180,
		PaddingUV:  pad,
		Aspect:     1, // the atlas is square; see the size options
	})
	if err != nil {
		// AtlasUnwrap writes no UVs unless the whole layout succeeded, so the
		// document is untouched — drop the entry rather than undoing it, which
		// would clear the selection over an action that did nothing.
		ModelDiscardUndo()
		return err
	}
	modelDoc.SetTexture(name)
	// The UV panel caches the selection's UVs to transform against, and every
	// one of those caches now describes UVs that no longer exist.
	modelUVInvalidate()

	ed, err := modelBuildAtlasTexture(name, size)
	if err != nil {
		return err
	}

	modelRequestTexEditor(ed)
	modelSetStatus("Unwrapped %d faces into %d islands on %s (%d²)",
		len(modelDoc.Faces), islands, name, size)
	return nil
}

// modelBuildAtlasTexture creates the texture the unwrap was laid out for,
// saves it, and returns it wrapped in editor state bound to the name.
//
// Two layers rather than one. The bottom is the checker guide
// (model_uv_guide.go): a fresh TexDoc is entirely palette slot 0, which is
// transparent and which the shader discards, so without a fill the model would
// vanish rather than look unpainted — and a guide is far more use than the flat
// grey that first occupied this slot, because it says which part of the image
// each face is showing.
//
// The top layer is empty and active, so the first brush stroke lands somewhere
// that can be hidden or thrown away without taking the guide with it — and,
// just as importantly, so the guide itself can be hidden once the painting no
// longer needs it, without disturbing the paint.
func modelBuildAtlasTexture(name string, size int) (*texEditorState, error) {
	doc, err := texdoc.NewTexDoc(name, size, size, texdoc.GlobalPalette)
	if err != nil {
		return nil, err
	}
	doc.Layers[0].Name = "UV Guide"
	fillAtlasGuide(doc, 0)
	if err := doc.AddLayer("Paint"); err != nil {
		return nil, err
	}
	doc.Active = len(doc.Layers) - 1

	ed := NewTexEditor(doc)
	// Save immediately. The model now names this texture, and a model that
	// names a texture with no file behind it renders black on the next load —
	// so "unwrap onto a new texture" has to actually produce the texture, not
	// an unsaved document that might never be written. Save also registers the
	// name and sets BoundTexture, which is what makes painting live.
	if err := ed.Save(name); err != nil {
		return nil, fmt.Errorf("save texture %q: %w", name, err)
	}

	// Upload synchronously rather than through the frame queue. The queue
	// drains under the main context next frame, but the model editor draws
	// this frame and would call ensureTextureLoaded on a name whose .png was
	// only just written — a wasted disk read at best, one black frame and a log
	// line at worst. Texture objects are shared across the window contexts (see
	// the file comment), so specifying it here is visible everywhere.
	flat := doc.Flatten()
	UpdateTextureHasAlpha(name, &doc.Pal, flat)
	if err := UpdateTextureFromRGBA(name, rgbaFromPaletted(flat)); err != nil {
		return nil, fmt.Errorf("upload texture %q: %w", name, err)
	}
	return ed, nil
}

// ---------- deferred hand-off to the texture editor ----------

// modelPendingTexEditor is an open request waiting for the main loop.
//
// Opening the texture editor cannot happen where the request is made. The model
// editor's panel runs inside RenderSecondaryWindows' range over the open
// windows, and creating one there would insert into the map being ranged over
// and — worse — leave the *new* window's GL context current for the rest of the
// model editor's frame. So the request is parked here and drained from the main
// loop, where neither is true.
var modelPendingTexEditor struct {
	Armed bool
	Doc   *texEditorState // nil means "just raise it on what is already open"
}

// modelRequestTexEditor asks the main loop to open the texture editor, adopting
// ed if it is non-nil.
func modelRequestTexEditor(ed *texEditorState) {
	modelPendingTexEditor.Armed = true
	modelPendingTexEditor.Doc = ed
}

// DrainModelTexEditorRequest opens the texture editor if one was requested.
// Called once per frame from the main loop with the main GL context current —
// see modelPendingTexEditor for why it cannot happen at the call site.
func DrainModelTexEditorRequest() {
	if !modelPendingTexEditor.Armed {
		return
	}
	ed := modelPendingTexEditor.Doc
	modelPendingTexEditor.Armed = false
	modelPendingTexEditor.Doc = nil

	// Adopt before opening: OpenTextureEditor creates a blank starting document
	// when nothing is open, and building one only to replace it would leave the
	// user's window flashing a document they never asked for.
	if ed != nil {
		texEditorAdoptDoc(ed)
	}
	OpenTextureEditor()
}

// ---------- the UV layout overlay ----------

// modelUVLayoutFor returns the UV outline of every face the open model maps
// onto texture tex, as closed loops in UV space — what the texture editor draws
// over its canvas so the painter can see which region belongs to which face.
//
// It is derived from the document on demand rather than baked into the texture,
// for two reasons. Baked guide lines would be *in* the image, so they would show
// up on the model and get painted over. And derived-on-demand means the overlay
// follows the mesh: move a vertex in the model editor and the outline moves in
// the texture editor on the same frame.
//
// Returns nil when the open model does not wear tex, which is what makes the
// overlay silently correct while editing an unrelated texture.
func modelUVLayoutFor(tex string) [][]mgl32.Vec2 {
	if modelDoc == nil || tex == "" || modelDoc.TextureName() != tex {
		return nil
	}
	out := make([][]mgl32.Vec2, 0, len(modelDoc.Faces))
	for fi := range modelDoc.Faces {
		f := &modelDoc.Faces[fi]
		if len(f.Verts) < 3 {
			continue
		}
		loop := make([]mgl32.Vec2, len(f.Verts))
		for i := range f.Verts {
			loop[i] = f.uvAt(i)
		}
		out = append(out, loop)
	}
	return out
}
