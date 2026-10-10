package editor

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
//
// The form, the texture it makes and the hand-off to the texture editor are
// shared with a level's brushes, which have an Unwrap & Paint of their own
// (editor_brush_uv_texture.go). The texture editor's layout overlay shows both.

import (
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"

	"foundry/internal/gfx"
	"foundry/internal/imguibackend"
	"foundry/internal/model"
	"foundry/internal/texdoc"
	"foundry/internal/texture"
	"foundry/internal/textureui"
)

// atlasUnwrapForm is the Unwrap & Paint form. It lives across frames because
// imgui is immediate-mode: the widgets are rebuilt every frame and the values
// they edit cannot be.
//
// A type rather than one variable because three windows draw it — the model
// editor, and the level and entity editors for their brushes — and each has to
// own its copy, or a form opened in one would pop up in all of them.
type atlasUnwrapForm struct {
	Show      bool
	Name      string
	SizeIdx   int32   // index into texEditorSizeOptions
	AngleDeg  float32 // geom.AtlasOptions.AngleLimit, in degrees
	PadTexels float32 // gutter width, in texels of the chosen size
	Err       string  // last failure, shown in the form until it succeeds
}

// modelUnwrapDialog is the model editor's copy of the form.
var modelUnwrapDialog atlasUnwrapForm

// The form's defaults. The angle matches geom's default angle limit; the
// padding matches its default gutter at 512².
const (
	atlasUnwrapDefaultAngleDeg  = float32(60)
	atlasUnwrapDefaultPadTexels = float32(4)
)

// open arms the form on a suggested texture name, with everything else back at
// its defaults.
func (d *atlasUnwrapForm) open(name string) {
	d.Show = true
	d.Name = name
	d.SizeIdx = 3 // 512, same default as the texture editor's New
	d.AngleDeg = atlasUnwrapDefaultAngleDeg
	d.PadTexels = atlasUnwrapDefaultPadTexels
	d.Err = ""
}

// atlasPadUV turns the form's gutter into the UV units an unwrap takes. It is
// authored in texels because that is the unit the bleed happens in — a property
// of the filter, not of the mesh. A deliberate zero comes back as -1, which the
// options read as "no gutter" rather than as "unset".
func atlasPadUV(padTexels float32, size int) float32 {
	if padTexels <= 0 {
		return -1
	}
	return padTexels / float32(size)
}

// modelOpenUnwrapDialog arms the form, seeding the texture name from whatever
// the model already wears — re-unwrapping onto the same texture is the common
// case once a model has been through this once.
func modelOpenUnwrapDialog() {
	if modelDoc == nil {
		return
	}
	name := modelDoc.Texture
	if name == "" {
		name = modelUnwrapSuggestedName()
	}
	modelUnwrapDialog.open(name)
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
	if textureCombo("Texture##modeldoctex", &tex, true) {
		modelSetTexture(tex)
	}

	// A model already wearing a saved texture has been through this before, and
	// the texture is very likely painted: the button carries on painting it
	// rather than laying the mesh out again over a fresh checkerboard. Starting
	// over is still there, one button along, where it cannot be hit by habit.
	existing := modelExistingTexture()
	if imgui.Button("Unwrap && Paint...##modelunwrapatlas") {
		if existing != "" {
			if err := openTextureForPainting(existing); err != nil {
				modelSetStatus("Open %s: %v", existing, err)
			}
		} else {
			modelOpenUnwrapDialog()
		}
	}
	if imgui.IsItemHovered() {
		if existing != "" {
			imgui.SetTooltip(fmt.Sprintf("Open %s in the texture editor with the UV layout\n"+
				"over it, and keep painting. The model updates as you paint.", existing))
		} else {
			imgui.SetTooltip("Flatten the whole mesh into one texture, point the model\n" +
				"at it, and open it in the texture editor.\n" +
				"The model updates as you paint.")
		}
	}
	if existing != "" {
		imgui.SameLine()
		if imgui.Button("Re-unwrap...##modelreunwrapatlas") {
			modelOpenUnwrapDialog()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Lay the mesh out again from scratch. The texture is\n" +
				"replaced with a fresh guide, since the old paint would\n" +
				"no longer line up with the new layout.")
		}
	}
}

// modelExistingTexture is the texture the model wears when it is one saved on
// disk — the case in which Unwrap & Paint opens it rather than replacing it.
func modelExistingTexture() string {
	if modelDoc == nil || modelDoc.Texture == "" || !texDocExistsOnDisk(modelDoc.Texture) {
		return ""
	}
	return modelDoc.Texture
}

// openTextureForPainting raises the texture editor on the saved texture name,
// with the UV layout of whatever wears it drawn over the canvas.
//
// When the texture editor already has it open, it is raised as it is: reading
// the file again would throw away strokes not yet saved.
func openTextureForPainting(name string) error {
	if ed := textureui.ActiveTexEditor; ed != nil && ed.BoundTexture == name {
		requestTexEditor(nil)
		return nil
	}
	ed, err := texture.OpenTexEditorDoc(name)
	if err != nil {
		return err
	}
	requestTexEditor(ed)
	return nil
}

// ---------- the dialog ----------

// drawModelUnwrapDialog draws the form. Called once per frame from the model
// editor's panel, like the editor's other modals.
func drawModelUnwrapDialog() {
	imguibackend.WithModalPopup("Unwrap & Paint##modelunwrapdlg", &modelUnwrapDialog.Show, func() {
		modelUnwrapDialog.body("model", "Flatten the mesh into one texture and paint it.",
			nil, modelUnwrapToTexture)
	})
}

// body draws the form's contents inside whichever modal the caller opened.
//
// id keeps this copy's widget IDs apart from another's. note, when non-nil, is
// given the sanitised texture name to say what the unwrap is about to reach.
// run is the action; its error stays on the form until a later run succeeds.
func (d *atlasUnwrapForm) body(id, intro string, note func(name string),
	run func(name string, size int, angleDeg, padTexels float32) error) {
	imgui.Text(intro)
	imgui.Separator()

	imgui.Text("Texture name:")
	imgui.SetNextItemWidth(200)
	imgui.InputText("##"+id+"unwrapname", &d.Name)

	imgui.Text("Size (square):")
	sizeLabels := make([]string, len(textureui.TexEditorSizeOptions))
	for i, s := range textureui.TexEditorSizeOptions {
		sizeLabels[i] = fmt.Sprintf("%dx%d", s, s)
	}
	imgui.SetNextItemWidth(150)
	imguibackend.Combo("##"+id+"unwrapsize", &d.SizeIdx, sizeLabels)

	// Degrees and texels drag by whole units; Editor.StepSize is a world
	// distance and would make both of these crawl.
	editorDragFloatStep("Seam angle (deg)##"+id+"unwrapangle", &d.AngleDeg, 1, 179, "%.0f", 1)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How far two neighbouring faces may differ before the\n" +
			"unwrap cuts a seam between them. Lower makes more,\n" +
			"flatter islands; higher makes fewer, more distorted ones.")
	}

	editorDragFloatStep("Gutter (texels)##"+id+"unwrappad", &d.PadTexels, 0, 32, "%.0f", 1)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Blank space kept around each island so filtering and\n" +
			"mipmaps cannot bleed one island's colour into another.")
	}

	name := sanitizeTextureName(d.Name)
	if note != nil && name != "" {
		imgui.Separator()
		note(name)
	}
	if name != "" && texDocExistsOnDisk(name) {
		imgui.Separator()
		imgui.Text(fmt.Sprintf("Note: overwrites the existing texture %q.", name))
	}
	if d.Err != "" {
		imgui.Separator()
		imgui.Text("Failed: " + d.Err)
	}

	imgui.Separator()
	imguibackend.EditorPushDisabled(name == "")
	if imgui.Button("Unwrap##" + id + "unwrapok") {
		size := int(textureui.TexEditorSizeOptions[d.SizeIdx])
		if err := run(name, size, d.AngleDeg, d.PadTexels); err != nil {
			d.Err = err.Error()
		} else {
			d.Err = ""
			imgui.CloseCurrentPopup()
		}
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	if imgui.Button("Cancel##" + id + "unwrapcancel") {
		d.Err = ""
		imgui.CloseCurrentPopup()
	}
}

// texDocExistsOnDisk reports whether a texture of this name is already saved.
func texDocExistsOnDisk(name string) bool {
	_, err := os.Stat(texture.FtexPath(name))
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

	// One undo entry for the whole thing: the unwrap and the retexture are one
	// action from the user's side, and undoing half of it would leave the model
	// wearing an atlas it is no longer laid out for.
	ModelPushUndo()

	islands, err := modelDoc.AtlasUnwrap(modelAllFaceIndices(), model.AtlasUnwrapOptions{
		AngleLimit: angleDeg * math.Pi / 180,
		PaddingUV:  atlasPadUV(padTexels, size),
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

	ed, err := buildAtlasTexture(name, size)
	if err != nil {
		return err
	}

	requestTexEditor(ed)
	modelSetStatus("Unwrapped %d faces into %d islands on %s (%d²)",
		len(modelDoc.Faces), islands, name, size)
	return nil
}

// buildAtlasTexture creates the texture the unwrap was laid out for,
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
func buildAtlasTexture(name string, size int) (*texture.TexEditorState, error) {
	doc, err := texdoc.NewTexDoc(name, size, size, texdoc.GlobalPalette)
	if err != nil {
		return nil, err
	}
	doc.Layers[0].Name = "UV Guide"
	model.FillAtlasGuide(doc, 0)
	if err := doc.AddLayer("Paint"); err != nil {
		return nil, err
	}
	doc.Active = len(doc.Layers) - 1

	ed := texture.NewTexEditor(doc)
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
	// this frame and would call EnsureTextureLoaded on a name whose .png was
	// only just written — a wasted disk read at best, one black frame and a log
	// line at worst. Texture objects are shared across the window contexts (see
	// the file comment), so specifying it here is visible everywhere.
	flat := doc.Flatten()
	gfx.UpdateTextureHasAlpha(name, &doc.Pal, flat)
	if err := gfx.UpdateTextureFromRGBA(name, texture.RgbaFromPaletted(flat)); err != nil {
		return nil, fmt.Errorf("upload texture %q: %w", name, err)
	}
	return ed, nil
}

// ---------- deferred hand-off to the texture editor ----------

// pendingTexEditor is an open request waiting for the main loop.
//
// Opening the texture editor cannot happen where the request is made. The model
// editor's panel runs inside RenderSecondaryWindows' range over the open
// windows, and creating one there would insert into the map being ranged over
// and — worse — leave the *new* window's GL context current for the rest of the
// model editor's frame. So the request is parked here and drained from the main
// loop, where neither is true.
var pendingTexEditor struct {
	Armed bool
	Doc   *texture.TexEditorState // nil means "just raise it on what is already open"
}

// requestTexEditor asks the main loop to open the texture editor, adopting
// ed if it is non-nil.
func requestTexEditor(ed *texture.TexEditorState) {
	pendingTexEditor.Armed = true
	pendingTexEditor.Doc = ed
}

// DrainTexEditorRequest opens the texture editor if one was requested.
// Called once per frame from the main loop with the main GL context current —
// see pendingTexEditor for why it cannot happen at the call site.
func DrainTexEditorRequest() {
	if !pendingTexEditor.Armed {
		return
	}
	ed := pendingTexEditor.Doc
	pendingTexEditor.Armed = false
	pendingTexEditor.Doc = nil

	// Adopt before opening: OpenTextureEditor creates a blank starting document
	// when nothing is open, and building one only to replace it would leave the
	// user's window flashing a document they never asked for.
	if ed != nil {
		textureui.TexEditorAdoptDoc(ed)
	}
	textureui.OpenTextureEditor()
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
	if modelDoc == nil || tex == "" || modelDoc.Texture != tex {
		return nil
	}
	out := make([][]mgl32.Vec2, 0, len(modelDoc.Faces))
	for fi := range modelDoc.Faces {
		if loop := modelFaceUVLoop(fi); loop != nil {
			out = append(out, loop)
		}
	}
	return out
}

// modelUVSelectionFor is modelUVLayoutFor narrowed to the face selection: the
// islands the texture editor highlights so the painter can see where a face
// picked in the viewport lives on the canvas. Face mode only — a vertex or edge
// selection names no region to paint.
func modelUVSelectionFor(tex string) [][]mgl32.Vec2 {
	if modelDoc == nil || tex == "" || modelDoc.Texture != tex {
		return nil
	}
	faces := modelSelectedFaces()
	out := make([][]mgl32.Vec2, 0, len(faces))
	for _, fi := range faces {
		if loop := modelFaceUVLoop(fi); loop != nil {
			out = append(out, loop)
		}
	}
	return out
}

// modelFaceUVLoop is face fi's corners in UV space, or nil when fi does not
// name a face with an area.
func modelFaceUVLoop(fi int) []mgl32.Vec2 {
	if fi < 0 || fi >= len(modelDoc.Faces) {
		return nil
	}
	f := &modelDoc.Faces[fi]
	if len(f.Verts) < 3 {
		return nil
	}
	loop := make([]mgl32.Vec2, len(f.Verts))
	for i := range f.Verts {
		loop[i] = f.UvAt(i)
	}
	return loop
}
