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
	"image"
	"image/draw"
	"math/rand"

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
)

// texUndoByteBudget bounds undo history by bytes, not entry count — a 32MB
// budget is roughly 32 single-layer paint steps at the 1024x1024 cap, or
// hundreds at a typical 256x256.
const texUndoByteBudget = 32 * 1024 * 1024

// texUndoEntry is one undo/redo step. Paint ops touch a single layer's
// indices; structural ops (add/delete/reorder/palette-remap) snapshot the
// whole document. Applying an entry always means "write this state", so the
// same struct shape works for both the undo and redo stacks.
type texUndoEntry struct {
	Structural bool
	LayerIdx   int            // paint ops only
	Before     []uint8        // paint ops only: the indices to write when applied
	Doc        *texdoc.TexDoc // structural ops only: the doc to swap in when applied
}

func entrySize(e texUndoEntry) int {
	if e.Structural {
		return docByteSize(e.Doc)
	}
	return len(e.Before)
}

func docByteSize(d *texdoc.TexDoc) int {
	n := 0
	for _, l := range d.Layers {
		n += len(l.Idx)
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
	BrushSize    int // radius in texels; 1 = single texel
	SprayDensity int // ToolSpray only: random dabs applied per painted frame

	undoStack []texUndoEntry
	redoStack []texUndoEntry
	undoBytes int

	strokeActive bool
	strokeLayer  int
	strokeBefore []uint8

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
	return &TexEditorState{Doc: doc, Tool: ToolBrush, PrimaryIdx: 1, BrushSize: 1, SprayDensity: 8, strokeLayer: -1, PreviewDirty: true}
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
	e.strokeBefore = append([]uint8(nil), layer.Idx...)
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
	if changed {
		e.pushUndo(texUndoEntry{LayerIdx: e.strokeLayer, Before: before})
		e.Doc.MarkDirty()
	}
}

func (e *TexEditorState) setTexel(x, y int, idx uint8) {
	if x < 0 || y < 0 || x >= e.Doc.W || y >= e.Doc.H {
		return
	}
	layer := e.activeLayer()
	if layer == nil {
		return
	}
	layer.Idx[y*e.Doc.W+x] = idx
	e.PreviewDirty = true
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
		if layer.Idx[i] != target {
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

// ---------- undo/redo ----------

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
		layer := &e.Doc.Layers[entry.LayerIdx]
		redoEntry = texUndoEntry{LayerIdx: entry.LayerIdx, Before: append([]uint8(nil), layer.Idx...)}
		copy(layer.Idx, entry.Before)
	}
	e.redoStack = append(e.redoStack, redoEntry)
	e.Doc.MarkDirty()
	e.PreviewDirty = true
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
		layer := &e.Doc.Layers[entry.LayerIdx]
		undoEntry = texUndoEntry{LayerIdx: entry.LayerIdx, Before: append([]uint8(nil), layer.Idx...)}
		copy(layer.Idx, entry.Before)
	}
	e.undoStack = append(e.undoStack, undoEntry)
	e.undoBytes += entrySize(undoEntry)
	e.Doc.MarkDirty()
	e.PreviewDirty = true
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
	return nil
}

func (e *TexEditorState) DuplicateLayer(i int) error {
	before := texdoc.CloneTexDoc(e.Doc)
	if err := e.Doc.DuplicateLayer(i); err != nil {
		return err
	}
	e.pushUndo(texUndoEntry{Structural: true, Doc: before})
	e.PreviewDirty = true
	return nil
}

func (e *TexEditorState) DeleteLayer(i int) error {
	before := texdoc.CloneTexDoc(e.Doc)
	if err := e.Doc.DeleteLayer(i); err != nil {
		return err
	}
	e.pushUndo(texUndoEntry{Structural: true, Doc: before})
	e.PreviewDirty = true
	return nil
}

func (e *TexEditorState) MoveLayer(i int, up bool) error {
	before := texdoc.CloneTexDoc(e.Doc)
	if err := e.Doc.MoveLayer(i, up); err != nil {
		return err
	}
	e.pushUndo(texUndoEntry{Structural: true, Doc: before})
	e.PreviewDirty = true
	return nil
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

// SyncLiveTexture flattens the document and queues a GPU upload if it's
// bound to a texture name, so every keystroke/brush stroke updates the world
// live — requirement 6 covers editing, not just saving.
func (e *TexEditorState) SyncLiveTexture() {
	if e.BoundTexture == "" {
		return
	}
	flat := e.Doc.Flatten()
	gfx.UpdateTextureHasAlpha(e.BoundTexture, &e.Doc.Pal, flat)
	gfx.QueueTextureUpload(e.BoundTexture, RgbaFromPaletted(flat))
}

// Save writes the document to <exe>/textures/<name>.{ftex,png}, registers it
// so the 2D/3D editor's texture dropdowns can find it immediately
// (requirement 7), and queues the flattened image for GPU upload.
func (e *TexEditorState) Save(name string) error {
	if err := SaveTexDoc(e.Doc, name); err != nil {
		return err
	}
	gfx.RegisterTexture(name)
	e.BoundTexture = name
	e.SyncLiveTexture()
	return nil
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
