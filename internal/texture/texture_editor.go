package texture

// texture_editor.go — texture editor state, tools, and undo/redo.
//
// This file only touches the texdoc.TexDoc model plus the map/slice bookkeeping in
// go (RegisterTexture, QueueTextureUpload) and texture_alpha.go
// (UpdateTextureHasAlpha) — none of which call into GL themselves, so every
// function here is unit-testable without a display. The actual GL upload
// happens later, when the main loop drains the queue (go). Window
// creation and imgui drawing live in texture_editor_ui.go.

import (
	"fmt"
	"image"
	"image/draw"
	"math/rand"
	"path/filepath"
	"strings"

	"foundry/internal/gfx"
	"foundry/internal/texdoc"
)

// TexTool selects the active painting tool.
type TexTool int

const (
	ToolBrush TexTool = iota
	ToolEraser
	ToolFill
	ToolLine
	ToolRect
	ToolEyedropper
	ToolSpray
	ToolSelectRect
	ToolSelectLasso
)

// IsSelectTool reports whether t draws selections (and drags their contents)
// rather than painting.
func (t TexTool) IsSelectTool() bool { return t == ToolSelectRect || t == ToolSelectLasso }

// texUndoByteBudget bounds undo history by bytes, not entry count — a 128MB
// budget is roughly 32 single-layer paint steps at the 2048x2048 cap, or
// thousands at a typical 256x256.
const texUndoByteBudget = 128 * 1024 * 1024

// texUndoEntry is one undo/redo step. Paint ops touch a single layer's
// indices; structural ops (add/delete/reorder/palette-remap) snapshot the
// whole document. Applying an entry always means "write this state", so the
// same struct shape works for both the undo and redo stacks.
//
// A paint entry records which frame and which channel it was painted on, not
// just which layer: "layer 2" means nothing on its own once a document has an
// animation stack and a bump channel, and applying a stroke's indices to
// whatever happens to be checked out now would write colour art into a height
// field. Applying one checks its frame and channel back out first, so an undo
// taken after switching away goes back to where the edit was, and the user
// sees what was undone.
type texUndoEntry struct {
	Structural bool
	Frame      int               // paint ops only: the frame the stroke was painted on
	Channel    texdoc.TexChannel // paint ops only: the channel it was painted on
	LayerIdx   int               // paint ops only
	Before     []uint8           // paint ops only: the indices to write when applied
	Doc        *texdoc.TexDoc    // structural ops only: the doc to swap in when applied

	// HasSel marks a paint op that moved the selection (a move or a paste):
	// applying it puts Sel back as the selection too, nil meaning none.
	HasSel bool
	Sel    []bool
}

func entrySize(e texUndoEntry) int {
	if e.Structural {
		return docByteSize(e.Doc)
	}
	return len(e.Before) + len(e.Sel)
}

func docByteSize(d *texdoc.TexDoc) int {
	n := 0
	for _, f := range d.Frames() {
		for _, l := range f.Layers {
			n += len(l.Idx)
		}
		for _, l := range f.Bump {
			n += len(l.Idx)
		}
	}
	return n
}

// TexEditorState is one open document plus its editing state. The plan's UI
// hosts a single texture editor window with one active document at a time —
// see activeTexEditor in texture_editor_ui.go.
type TexEditorState struct {
	Doc          *texdoc.TexDoc
	BoundTexture string // texture name this doc live-updates; "" until bound
	Tool         TexTool
	PrimaryIdx   uint8
	SecondaryIdx uint8
	BumpIdx      uint8 // height the tools write on the bump channel, 0..255
	BrushSize    int   // radius in texels; 1 = single texel
	SprayDensity int   // ToolSpray only: random dabs applied per painted frame

	undoStack []texUndoEntry
	redoStack []texUndoEntry
	undoBytes int

	strokeActive  bool
	strokeLayer   int
	strokeFrame   int
	strokeChannel texdoc.TexChannel
	strokeBefore  []uint8

	// strokeSelRecorded marks a stroke opened by beginSelStroke, which also
	// records the selection it started with — see texture_selection.go.
	strokeSelRecorded bool
	strokeSelBefore   []bool

	// Selection state (texture_selection.go). sel is nil for no selection;
	// selGen counts changes to it. float is content lifted by a move or
	// paste; moveFromX/Y is where the current move found it, and scaleFrom
	// the edges (left, top, right, bottom) the current scale found it with.
	sel                  []bool
	selGen               int
	float                *texFloat
	moveFromX, moveFromY int
	scaleFrom            [4]int

	// BumpDirty is PreviewDirty's counterpart for the height field: set by
	// anything that could have changed it, cleared by the UI once it has
	// re-derived and re-uploaded the normal map. Separate from PreviewDirty
	// because deriving a normal map is a Sobel over the whole canvas, and a
	// brush drag on the colour channel must not pay for it — it is the same
	// reasoning that gates the flatten, one level down.
	BumpDirty bool

	// PreviewDirty is set by any texel/layer mutation and cleared by the UI
	// once it has re-flattened and re-uploaded — see texture_editor_ui.go.
	// Gating the (relatively expensive) Flatten call on this rather than
	// running it unconditionally every frame is what keeps a brush drag to
	// one upload per frame without also burning a full flatten on frames
	// where nothing changed (e.g. while just panning/zooming the canvas).
	PreviewDirty bool
}

// NewTexEditor wraps doc in fresh editing state with no undo history.
func NewTexEditor(doc *texdoc.TexDoc) *TexEditorState {
	return &TexEditorState{Doc: doc, Tool: ToolBrush, PrimaryIdx: 1, BumpIdx: 255, BrushSize: 1, SprayDensity: 8, strokeLayer: -1, PreviewDirty: true, BumpDirty: true}
}

func (e *TexEditorState) activeLayer() *texdoc.TexLayer {
	if e.Doc == nil || e.Doc.Active < 0 || e.Doc.Active >= len(e.Doc.Layers) {
		return nil
	}
	return &e.Doc.Layers[e.Doc.Active]
}

// ---------- painting ----------

// BeginStroke snapshots the active layer's indices before a paint gesture
// (brush drag, single fill/line/rect op) so EndStroke can diff and commit a
// single undo entry regardless of how many texels the gesture touched.
func (e *TexEditorState) BeginStroke() {
	layer := e.activeLayer()
	if layer == nil {
		return
	}
	e.strokeActive = true
	e.strokeLayer = e.Doc.Active
	e.strokeFrame = e.Doc.ActiveFrameIndex()
	e.strokeChannel = e.Doc.Channel()
	e.strokeBefore = append([]uint8(nil), layer.Idx...)
	e.strokeSelRecorded = false
	e.strokeSelBefore = nil
}

// EndStroke commits the accumulated diff as one undo entry, if anything
// actually changed.
func (e *TexEditorState) EndStroke() {
	if !e.strokeActive {
		return
	}
	e.strokeActive = false
	layer := &e.Doc.Layers[e.strokeLayer]
	changed := false
	for i, v := range e.strokeBefore {
		if layer.Idx[i] != v {
			changed = true
			break
		}
	}
	before := e.strokeBefore
	e.strokeBefore = nil
	selMoved := e.strokeSelRecorded && !boolsEqual(e.strokeSelBefore, e.SelectionMask())
	if changed || selMoved {
		entry := texUndoEntry{
			Frame:    e.strokeFrame,
			Channel:  e.strokeChannel,
			LayerIdx: e.strokeLayer,
			Before:   before,
		}
		if e.strokeSelRecorded {
			entry.HasSel, entry.Sel = true, e.strokeSelBefore
		}
		e.pushUndo(entry)
	}
	if changed {
		e.Doc.MarkDirty()
	}
	e.strokeSelRecorded = false
	e.strokeSelBefore = nil
}

func boolsEqual(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (e *TexEditorState) setTexel(x, y int, idx uint8) {
	if x < 0 || y < 0 || x >= e.Doc.W || y >= e.Doc.H {
		return
	}
	layer := e.activeLayer()
	if layer == nil {
		return
	}
	i := y*e.Doc.W + x
	if e.HasSelection() && !e.sel[i] {
		return
	}
	layer.Idx[i] = idx
	e.PreviewDirty = true
	if e.Doc.Channel() == texdoc.ChanBump {
		e.BumpDirty = true
	}
}

// StampBrush writes idx into every texel within BrushSize-1 texels of
// (cx, cy) (a circular brush), clamped to the canvas.
func (e *TexEditorState) StampBrush(cx, cy int, idx uint8) {
	r := e.BrushSize - 1
	if r < 0 {
		r = 0
	}
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			if dx*dx+dy*dy > r*r {
				continue
			}
			e.setTexel(cx+dx, cy+dy, idx)
		}
	}
}

// StampSpray scatters idx randomly within BrushSize-1 texels of (cx, cy),
// approximating the classic MS Paint spray can: unlike StampBrush's solid
// fill, this is meant to be called once per painted frame while the button
// stays down, so density builds up gradually the longer the cursor lingers
// rather than filling solidly on first contact.
func (e *TexEditorState) StampSpray(cx, cy int, idx uint8) {
	r := e.BrushSize - 1
	if r < 0 {
		r = 0
	}
	density := e.SprayDensity
	if density < 1 {
		density = 1
	}
	span := 2*r + 1
	for i := 0; i < density; i++ {
		dx := rand.Intn(span) - r
		dy := rand.Intn(span) - r
		if dx*dx+dy*dy > r*r {
			continue
		}
		e.setTexel(cx+dx, cy+dy, idx)
	}
}

// StampLine draws idx along a Bresenham line from (x0,y0) to (x1,y1),
// stamping the current brush at each step.
func (e *TexEditorState) StampLine(x0, y0, x1, y1 int, idx uint8) {
	dx := texdoc.AbsInt(x1 - x0)
	dy := -texdoc.AbsInt(y1 - y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	errv := dx + dy
	x, y := x0, y0
	for {
		e.StampBrush(x, y, idx)
		if x == x1 && y == y1 {
			break
		}
		e2 := 2 * errv
		if e2 >= dy {
			errv += dy
			x += sx
		}
		if e2 <= dx {
			errv += dx
			y += sy
		}
	}
}

// StampRect draws a rectangle from (x0,y0) to (x1,y1): outline only, or
// filled.
func (e *TexEditorState) StampRect(x0, y0, x1, y1 int, idx uint8, filled bool) {
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	if filled {
		for y := y0; y <= y1; y++ {
			for x := x0; x <= x1; x++ {
				e.setTexel(x, y, idx)
			}
		}
		return
	}
	for x := x0; x <= x1; x++ {
		e.setTexel(x, y0, idx)
		e.setTexel(x, y1, idx)
	}
	for y := y0; y <= y1; y++ {
		e.setTexel(x0, y, idx)
		e.setTexel(x1, y, idx)
	}
}

// Fill flood-fills the 4-connected region matching the texel at (x,y) with
// idx, on the active layer.
func (e *TexEditorState) Fill(x, y int, idx uint8) {
	layer := e.activeLayer()
	if layer == nil || x < 0 || y < 0 || x >= e.Doc.W || y >= e.Doc.H {
		return
	}
	w, h := e.Doc.W, e.Doc.H
	target := layer.Idx[y*w+x]
	if target == idx {
		return
	}
	stack := [][2]int{{x, y}}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		px, py := p[0], p[1]
		if px < 0 || py < 0 || px >= w || py >= h {
			continue
		}
		i := py*w + px
		// An unselected texel is a wall like a different colour is: setTexel
		// would refuse it, leaving it matching target and re-pushed forever.
		if layer.Idx[i] != target || !e.Selected(px, py) {
			continue
		}
		e.setTexel(px, py, idx)
		stack = append(stack, [2]int{px + 1, py}, [2]int{px - 1, py}, [2]int{px, py + 1}, [2]int{px, py - 1})
	}
}

// Eyedropper returns the palette index at (x,y) on the active layer.
func (e *TexEditorState) Eyedropper(x, y int) (uint8, bool) {
	layer := e.activeLayer()
	if layer == nil || x < 0 || y < 0 || x >= e.Doc.W || y >= e.Doc.H {
		return 0, false
	}
	return layer.Idx[y*e.Doc.W+x], true
}

// PaintIndex is the byte the tools write on the checked-out channel: a
// palette index on the colour channel, a height on the bump channel. Two
// separate values rather than one, because they mean unrelated things —
// switching to the bump channel and back must not have changed which colour
// the brush is loaded with.
func (e *TexEditorState) PaintIndex() uint8 {
	if e.Doc != nil && e.Doc.Channel() == texdoc.ChanBump {
		return e.BumpIdx
	}
	return e.PrimaryIdx
}

// SetPaintIndex loads the tools with v on the checked-out channel — what the
// eyedropper and the palette/height pickers write through.
func (e *TexEditorState) SetPaintIndex(v uint8) {
	if e.Doc != nil && e.Doc.Channel() == texdoc.ChanBump {
		e.BumpIdx = v
		return
	}
	e.PrimaryIdx = v
}

// ---------- undo/redo ----------

// checkoutFor checks out the frame and channel a paint entry was recorded
// against and returns the layer it touched, or nil if that layer no longer
// exists (or is the wrong size, which a canvas the entry predates would be).
func (e *TexEditorState) checkoutFor(entry texUndoEntry) *texdoc.TexLayer {
	if entry.Frame >= 0 && entry.Frame < e.Doc.FrameCount() {
		_ = e.Doc.SetActiveFrame(entry.Frame)
	}
	if entry.Channel == texdoc.ChanBump && !e.Doc.HasBump() {
		return nil
	}
	e.Doc.SetChannel(entry.Channel)
	if entry.LayerIdx < 0 || entry.LayerIdx >= len(e.Doc.Layers) {
		return nil
	}
	layer := &e.Doc.Layers[entry.LayerIdx]
	if len(layer.Idx) != len(entry.Before) {
		return nil
	}
	return layer
}

// applyEntrySelection restores the selection a move/paste entry recorded,
// recording the current one on its inverse so the step can be taken back.
// The float is left alone: the layer no longer holds what it stamped (or, on
// a redo, holds it again), which floatValid works out for itself.
func (e *TexEditorState) applyEntrySelection(entry texUndoEntry, inverse *texUndoEntry) {
	if !entry.HasSel {
		return
	}
	inverse.HasSel, inverse.Sel = true, e.SelectionMask()
	e.setSelection(entry.Sel)
}

// pushUndo records a completed action: clears redo history (a new action
// invalidates it) and appends to the undo stack, evicting the oldest entries
// while over the byte budget.
func (e *TexEditorState) pushUndo(entry texUndoEntry) {
	e.redoStack = nil
	e.undoStack = append(e.undoStack, entry)
	e.undoBytes += entrySize(entry)
	for e.undoBytes > texUndoByteBudget && len(e.undoStack) > 0 {
		e.undoBytes -= entrySize(e.undoStack[0])
		e.undoStack = e.undoStack[1:]
	}
}

func (e *TexEditorState) CanUndo() bool { return len(e.undoStack) > 0 }
func (e *TexEditorState) CanRedo() bool { return len(e.redoStack) > 0 }

// Undo applies the most recent undo entry and pushes its inverse onto the
// redo stack.
func (e *TexEditorState) Undo() {
	if len(e.undoStack) == 0 {
		return
	}
	n := len(e.undoStack) - 1
	entry := e.undoStack[n]
	e.undoStack = e.undoStack[:n]
	e.undoBytes -= entrySize(entry)

	var redoEntry texUndoEntry
	if entry.Structural {
		redoEntry = texUndoEntry{Structural: true, Doc: texdoc.CloneTexDoc(e.Doc)}
		e.Doc = entry.Doc
	} else {
		layer := e.checkoutFor(entry)
		if layer == nil {
			// The layer the stroke was on has since been deleted; the
			// structural entry for that deletion is the one that brings it
			// back, and it is deeper in the stack. Drop this step rather than
			// writing the indices somewhere they don't belong.
			return
		}
		redoEntry = texUndoEntry{
			Frame: entry.Frame, Channel: entry.Channel, LayerIdx: entry.LayerIdx,
			Before: append([]uint8(nil), layer.Idx...),
		}
		copy(layer.Idx, entry.Before)
		e.applyEntrySelection(entry, &redoEntry)
	}
	e.redoStack = append(e.redoStack, redoEntry)
	e.Doc.MarkDirty()
	e.PreviewDirty = true
	e.BumpDirty = true
}

// Redo re-applies the most recently undone entry and pushes its inverse back
// onto the undo stack.
func (e *TexEditorState) Redo() {
	if len(e.redoStack) == 0 {
		return
	}
	n := len(e.redoStack) - 1
	entry := e.redoStack[n]
	e.redoStack = e.redoStack[:n]

	var undoEntry texUndoEntry
	if entry.Structural {
		undoEntry = texUndoEntry{Structural: true, Doc: texdoc.CloneTexDoc(e.Doc)}
		e.Doc = entry.Doc
	} else {
		layer := e.checkoutFor(entry)
		if layer == nil {
			return
		}
		undoEntry = texUndoEntry{
			Frame: entry.Frame, Channel: entry.Channel, LayerIdx: entry.LayerIdx,
			Before: append([]uint8(nil), layer.Idx...),
		}
		copy(layer.Idx, entry.Before)
		e.applyEntrySelection(entry, &undoEntry)
	}
	e.undoStack = append(e.undoStack, undoEntry)
	e.undoBytes += entrySize(undoEntry)
	e.Doc.MarkDirty()
	e.PreviewDirty = true
	e.BumpDirty = true
}

// ---------- structural layer ops ----------
//
// Each wrapper snapshots the doc before attempting the mutation and only
// records the undo entry if the mutation actually succeeded, so a rejected
// op (e.g. hitting the 8-layer cap) never leaves a no-op undo step behind.

func (e *TexEditorState) AddLayer(name string) error {
	before := texdoc.CloneTexDoc(e.Doc)
	if err := e.Doc.AddLayer(name); err != nil {
		return err
	}
	e.pushUndo(texUndoEntry{Structural: true, Doc: before})
	e.PreviewDirty = true
	e.BumpDirty = true
	return nil
}

func (e *TexEditorState) DuplicateLayer(i int) error {
	before := texdoc.CloneTexDoc(e.Doc)
	if err := e.Doc.DuplicateLayer(i); err != nil {
		return err
	}
	e.pushUndo(texUndoEntry{Structural: true, Doc: before})
	e.PreviewDirty = true
	e.BumpDirty = true
	return nil
}

func (e *TexEditorState) DeleteLayer(i int) error {
	before := texdoc.CloneTexDoc(e.Doc)
	if err := e.Doc.DeleteLayer(i); err != nil {
		return err
	}
	e.pushUndo(texUndoEntry{Structural: true, Doc: before})
	e.PreviewDirty = true
	e.BumpDirty = true
	return nil
}

func (e *TexEditorState) MergeLayerDown(i int) error {
	before := texdoc.CloneTexDoc(e.Doc)
	if err := e.Doc.MergeLayerDown(i); err != nil {
		return err
	}
	e.pushUndo(texUndoEntry{Structural: true, Doc: before})
	e.PreviewDirty = true
	e.BumpDirty = true
	return nil
}

func (e *TexEditorState) MoveLayer(i int, up bool) error {
	before := texdoc.CloneTexDoc(e.Doc)
	if err := e.Doc.MoveLayer(i, up); err != nil {
		return err
	}
	e.pushUndo(texUndoEntry{Structural: true, Doc: before})
	e.PreviewDirty = true
	e.BumpDirty = true
	return nil
}

// ImportLayerImage decodes the image at path and imports it into the
// checked-out channel of the current frame — as a new layer above the active
// one, or with replace over the active layer — as one undoable step. See
// texdoc.ImportImageLayer.
func (e *TexEditorState) ImportLayerImage(path string, fit texdoc.ImportFit, replace bool) error {
	img, err := LoadImageFile(path)
	if err != nil {
		return err
	}
	b := img.Bounds()
	if err := texdoc.ValidTexSize(b.Dx(), b.Dy()); err != nil {
		return fmt.Errorf("import %s: %w", filepath.Base(path), err)
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	before := texdoc.CloneTexDoc(e.Doc)
	if err := e.Doc.ImportImageLayer(img, name, fit, replace); err != nil {
		return err
	}
	e.pushUndo(texUndoEntry{Structural: true, Doc: before})
	e.PreviewDirty = true
	e.BumpDirty = true
	return nil
}

// ---------- structural frame (animation stack) ops ----------
//
// Same undo shape as the layer ops above: snapshot, attempt, record only on
// success. SetActiveFrame is deliberately not here — switching frames is not
// undoable, matching how switching the active layer within a frame isn't.

func (e *TexEditorState) AddFrame() error {
	before := texdoc.CloneTexDoc(e.Doc)
	if err := e.Doc.AddFrame(); err != nil {
		return err
	}
	e.pushUndo(texUndoEntry{Structural: true, Doc: before})
	e.PreviewDirty = true
	e.BumpDirty = true
	return nil
}

func (e *TexEditorState) DuplicateFrame(i int) error {
	before := texdoc.CloneTexDoc(e.Doc)
	if err := e.Doc.DuplicateFrame(i); err != nil {
		return err
	}
	e.pushUndo(texUndoEntry{Structural: true, Doc: before})
	e.PreviewDirty = true
	e.BumpDirty = true
	return nil
}

func (e *TexEditorState) DeleteFrame(i int) error {
	before := texdoc.CloneTexDoc(e.Doc)
	if err := e.Doc.DeleteFrame(i); err != nil {
		return err
	}
	e.pushUndo(texUndoEntry{Structural: true, Doc: before})
	e.PreviewDirty = true
	e.BumpDirty = true
	return nil
}

func (e *TexEditorState) MoveFrame(i int, up bool) error {
	before := texdoc.CloneTexDoc(e.Doc)
	if err := e.Doc.MoveFrame(i, up); err != nil {
		return err
	}
	e.pushUndo(texUndoEntry{Structural: true, Doc: before})
	e.PreviewDirty = true
	e.BumpDirty = true
	return nil
}

// SetActiveFrame checks out frame i for editing. Not undoable; see above.
func (e *TexEditorState) SetActiveFrame(i int) error {
	if err := e.Doc.SetActiveFrame(i); err != nil {
		return err
	}
	e.PreviewDirty = true
	e.BumpDirty = true
	return nil
}

// ---------- bump channel ----------

// SetChannel checks out a channel for editing. Switching is a view change and
// not undoable, matching SetActiveFrame — except when it is the switch that
// *creates* the bump channel, which is a change to the document and gets the
// same structural snapshot every other one does.
func (e *TexEditorState) SetChannel(c texdoc.TexChannel) {
	if c == texdoc.ChanBump && !e.Doc.HasBump() {
		before := texdoc.CloneTexDoc(e.Doc)
		e.Doc.SetChannel(c)
		e.pushUndo(texUndoEntry{Structural: true, Doc: before})
	} else {
		e.Doc.SetChannel(c)
	}
	e.PreviewDirty = true
	e.BumpDirty = true
}

// DisableBump deletes the document's bump channel, undoably — it throws away
// every frame's height layers at once, which is the most destructive single
// action in the editor and the one most in need of a way back.
func (e *TexEditorState) DisableBump() {
	if !e.Doc.HasBump() {
		return
	}
	before := texdoc.CloneTexDoc(e.Doc)
	e.Doc.DisableBump()
	e.pushUndo(texUndoEntry{Structural: true, Doc: before})
	e.PreviewDirty = true
	e.BumpDirty = true
}

// SetBumpStrength re-bakes the derived normal map at a new strength. Not
// undoable, matching the layer opacity and blend-mode controls beside it: a
// slider drag would otherwise push a whole-document snapshot per frame it
// moved, and the value is one drag away from wherever it was.
func (e *TexEditorState) SetBumpStrength(s float32) {
	e.Doc.SetBumpStrength(s)
	e.PreviewDirty = true
	e.BumpDirty = true
}

// ---------- GPU sync + persistence ----------

// RgbaFromPaletted expands a flattened, on-palette image to plain RGBA for
// upload — the GPU side stays plain RGBA8 (see go).
func RgbaFromPaletted(p *image.Paletted) *image.RGBA {
	b := p.Bounds()
	out := image.NewRGBA(b)
	draw.Draw(out, b, p, b.Min, draw.Src)
	return out
}

// SyncLiveTexture flattens the checked-out frame and queues a GPU upload if
// it's bound to a texture name, so every keystroke/brush stroke updates the
// world live — requirement 6 covers editing, not just saving. Touches the
// name with gfx (see TouchAnimatedTexture) so an animated texture's own
// playback clock doesn't race this upload and overwrite the frame being
// painted with a stale cached one.
func (e *TexEditorState) SyncLiveTexture() {
	if e.BoundTexture == "" {
		return
	}
	flat := e.Doc.Flatten()
	gfx.UpdateTextureHasAlpha(e.BoundTexture, &e.Doc.Pal, flat)
	gfx.QueueTextureUpload(e.BoundTexture, RgbaFromPaletted(flat))
	gfx.TouchAnimatedTexture(e.BoundTexture)
	SyncLiveNormalMap(e.Doc, e.BoundTexture)
}

// SyncLiveNormalMap pushes the normal map derived from d's bump channel to
// the GPU under name, or takes the texture off the normal-mapped list when
// the document has no bump channel — so painting a height stroke lights every
// surface wearing the texture on the next frame, exactly the way painting a
// colour stroke recolours them.
//
// Deriving the whole map per edited frame is a Sobel over the canvas, on the
// same once-per-frame budget as the flatten that produced the albedo beside
// it. Shared by the live path above and RebuildAllTextures.
func SyncLiveNormalMap(d *texdoc.TexDoc, name string) {
	if !d.HasBump() {
		gfx.ClearNormalMap(name)
		return
	}
	gfx.SetNormalMap(name, d.NormalMap())
	gfx.TouchAnimatedTexture(gfx.NormalMapName(name))
}

// SyncAnimatedFrames registers name's frame stack with gfx so
// AdvanceAnimatedTextures can cycle through it, or un-registers it if d is
// back down to a single frame. Shared by Save (below) and
// RebuildAllTextures (texture_palette.go), the two places a document's
// frame count can change on disk.
//
// has-alpha is folded in here too, ORed across every frame rather than just
// the checked-out one: Geometry.IsTransparent routes a whole surface into
// the sorted blended pass or not, so it needs to know about transparency in
// any frame the surface might be showing, not only the one last saved from.
func SyncAnimatedFrames(d *texdoc.TexDoc, name string) {
	if d.FrameCount() <= 1 {
		gfx.UnregisterAnimatedTexture(name)
		gfx.UnregisterAnimatedTexture(gfx.NormalMapName(name))
		flat := d.Flatten()
		gfx.UpdateTextureHasAlpha(name, &d.Pal, flat)
		return
	}
	frames := make([]*image.RGBA, d.FrameCount())
	hasAlpha := false
	for i := range frames {
		flat := d.FlattenFrame(i)
		if gfx.ComputeHasAlpha(&d.Pal, flat) {
			hasAlpha = true
		}
		frames[i] = RgbaFromPaletted(flat)
	}
	gfx.SetTextureHasAlpha(name, hasAlpha)
	gfx.RegisterAnimatedTexture(name, frames)

	// The bump channel is per frame like everything else, so an animated
	// texture gets an animated normal map: registered under the derived name,
	// it rides the same playback clock as the albedo and stays in step with it
	// frame for frame.
	if !d.HasBump() {
		return
	}
	normals := make([]*image.RGBA, d.FrameCount())
	for i := range normals {
		normals[i] = d.NormalMapFrame(i)
	}
	gfx.SetNormalMap(name, normals[0])
	gfx.RegisterAnimatedTexture(gfx.NormalMapName(name), normals)
}

// Save writes the document to <exe>/textures/<name>.{ftex,png}, registers it
// so the 2D/3D editor's texture dropdowns can find it immediately
// (requirement 7), registers/unregisters its animation stack to match its
// current frame count, and queues the flattened image for GPU upload.
func (e *TexEditorState) Save(name string) error {
	if err := SaveTexDoc(e.Doc, name); err != nil {
		return err
	}
	gfx.RegisterTexture(name)
	e.BoundTexture = name
	e.SyncLiveTexture()
	// After SyncLiveTexture, not before: that call's own has-alpha update
	// only looks at the checked-out frame, and would otherwise immediately
	// overwrite the flag this ORs across every frame in the stack.
	SyncAnimatedFrames(e.Doc, name)
	return nil
}

// RevertToSaved throws away the document's unsaved changes: it reads the
// texture it is bound to back off disk and pushes that to the live texture, so
// every model and brush wearing it goes back to what was last saved instead of
// keeping strokes that were never written. Returns the saved document, to
// carry on from, or nil for one never saved — nothing wears it, so there is
// nothing to put back and nothing to keep.
func (e *TexEditorState) RevertToSaved() (*TexEditorState, error) {
	if e.BoundTexture == "" {
		return nil, nil
	}
	saved, err := OpenTexEditorDoc(e.BoundTexture)
	if err != nil {
		return nil, err
	}
	saved.SyncLiveTexture()
	// Last, as in Save: the frame count or the transparency may be exactly
	// what the discarded edits changed.
	SyncAnimatedFrames(saved.Doc, saved.BoundTexture)
	return saved, nil
}

// ---------- new/open/import ----------

func NewTexEditorDoc(name string, w, h int) (*TexEditorState, error) {
	d, err := texdoc.NewTexDoc(name, w, h, texdoc.GlobalPalette)
	if err != nil {
		return nil, err
	}
	return NewTexEditor(d), nil
}

func OpenTexEditorDoc(name string) (*TexEditorState, error) {
	d, err := ReadTexDoc(FtexPath(name))
	if err != nil {
		return nil, err
	}
	ed := NewTexEditor(d)
	ed.BoundTexture = name
	return ed, nil
}

func ImportTexEditorDoc(pngPath string) (*TexEditorState, error) {
	d, err := ImportPNG(pngPath)
	if err != nil {
		return nil, err
	}
	return NewTexEditor(d), nil
}
