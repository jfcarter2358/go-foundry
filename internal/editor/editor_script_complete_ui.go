package editor

// editor_script_complete_ui.go — the completion popup.
//
// The list is drawn into ImGui's foreground draw list rather than as a window,
// and its keys are taken in the window's SDL event filter rather than from
// ImGui. Both choices come from the same constraint, and it is the one thing
// worth understanding about this file: **the text area must stay active**.
//
// InputTextMultiline gives up its selection, its caret and its undo history the
// moment it loses the keyboard focus, and the only way to reach its buffer is
// the callback it runs while it holds that focus (editor_script_edit.go says
// the same thing from the other side). A popup window would take the focus. A
// BeginPopup would take it twice over. So the list is *painted* over the top of
// everything, owns no widget, and is never hovered by ImGui at all — and its
// Up, Down, Tab and Enter are intercepted before ImGuiProcessEvent can feed
// them to the widget, exactly the way Escape already is.
//
// The consequence is that hit-testing and scrolling are this file's own work.
// That is a few dozen lines, and it buys a list that cannot disturb the buffer
// it is completing.

import (
	"strings"

	"foundry/internal/flang"
	"foundry/internal/script"
	"foundry/internal/world"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// scriptComplRows is how many rows are shown at once. Ten is about a third of
// the buffer's height at the default window size: enough that scrolling is rare
// and not so much that the list covers the code it is describing.
const scriptComplRows = 10

// scriptComplMaxDetail caps the reference column, which for a property is a
// whole sentence.
const scriptComplMaxDetail = 52

// scriptComplState is the popup. One list at a time: it belongs to whichever
// document is on screen, and switching tabs closes it.
var scriptComplState struct {
	Open  bool
	Ctx   scriptComplCtx
	Items []scriptComplItem
	Sel   int
	Top   int

	// docID, src and cursor are what the list was built from. The list is
	// rebuilt when any of them changes, which is what keeps it in step with the
	// buffer without rebuilding it every frame.
	docID  int
	src    string
	cursor int

	// dismissed is the word the author pressed Escape on. The list stays shut
	// until the word changes — otherwise the next keystroke would bring back
	// exactly the list that was just refused.
	dismissed string

	// force is Ctrl+Space: open the list even where it would not open itself,
	// which is the empty prefix at the start of a name.
	force bool

	// accept is the row parked for the text area's callback, or -1. Nothing can
	// be spliced here and now for the reason scriptEditorRequest gives: the
	// buffer belongs to the widget, and the callback is the only way in.
	accept int

	// anchor is where the caret was drawn, in screen coordinates, and rect is
	// where the list was painted — the second is what a click is tested
	// against, since ImGui knows nothing about it.
	anchor           imgui.Vec2
	rowH             float32
	rectMin, rectMax imgui.Vec2
	drawn            bool
}

// scriptComplLevelCacheType is the level-derived half of the list: the ids, the
// tags, the attributes and the properties.
//
// Cached against the *shape* of the context rather than against the text,
// because these are the expensive parts — resolving a name means indexing the
// whole level — and they cannot change while a single word is being typed.
// Typing `@do`, `@doo`, `@door` re-filters three times and indexes once.
type scriptComplLevelCacheType struct {
	shape string
	lvl   *world.Level
	// at is when it was gathered. The level is edited in another window while
	// this one is open, and a level pointer does not change when an entity is
	// added to it — so the cache is given a short life rather than trusted to
	// notice. Two seconds is long enough to cover typing one word and short
	// enough that a name created next door is offerable almost at once.
	at    uint32
	Refs  []script.CompletionRef
	Tags  []string
	Attrs []string
	Props []script.PropertyDoc
	Kind  flang.Kind_
}

// scriptComplLevelCache is the one gathered list, replaced wholesale rather
// than patched: every field in it belongs to the same context.
var scriptComplLevelCache scriptComplLevelCacheType

// scriptComplLevelCacheMS is how long a gathered list is trusted.
const scriptComplLevelCacheMS = 2000

// scriptComplClose puts the list away.
func scriptComplClose() {
	scriptComplState.Open = false
	scriptComplState.Items = nil
	scriptComplState.accept = -1
	scriptComplState.force = false
	scriptComplState.drawn = false
}

// scriptComplUpdate rebuilds the list for the caret's current position. Called
// once per frame from drawScriptEditorText, with the text area active.
func scriptComplUpdate(d *scriptDoc) {
	st := &scriptComplState
	if st.docID == d.id && st.src == d.Text && st.cursor == scriptBufCursor && !st.force {
		return
	}
	// Typing is the only thing that opens a list. A caret that arrived by arrow
	// key, by click or by a scroll is a reader moving about, and a list that
	// opened under one would take the arrow keys away from the very gesture
	// that opened it — every line moved past would cost an Escape.
	typed := st.docID == d.id && st.src != d.Text
	wasOpen := st.Open
	prevPrefix := st.Ctx.Prefix
	st.docID, st.src, st.cursor = d.id, d.Text, scriptBufCursor

	ctx := scriptComplContext(d.Text, scriptBufCursor)
	if ctx.Kind == complNone {
		st.Ctx = ctx
		st.dismissed = ""
		scriptComplClose()
		return
	}
	if !typed && !st.force {
		// Moving off a word also puts the list away: the rows it is showing
		// were filtered against a caret that has gone.
		st.Ctx = ctx
		st.dismissed = ""
		scriptComplClose()
		return
	}
	// A refusal holds only for the word it was made about. Typing on past it,
	// or moving to another one, is a new question.
	if st.dismissed != "" && !st.force {
		if strings.HasPrefix(ctx.Prefix, st.dismissed) {
			st.Ctx = ctx
			return
		}
		st.dismissed = ""
	}

	if !st.force && !scriptComplWanted(ctx) {
		scriptComplClose()
		st.Ctx = ctx
		return
	}

	scriptComplLoadLevel(ctx)
	items := scriptComplFilter(scriptComplItems(ctx, scriptComplSourceFor(d)), ctx.Prefix)
	st.Ctx = ctx
	st.Items = items
	st.Open = len(items) > 0
	st.force = false
	if !st.Open {
		return
	}
	// The selection is kept across a keystroke only while the word is growing
	// under it — an author narrowing `pos` to `posi` means the same row, and
	// resetting to the top under them would fight the typing.
	if !wasOpen || !strings.HasPrefix(ctx.Prefix, prevPrefix) || st.Sel >= len(items) {
		st.Sel, st.Top = 0, 0
	}
	scriptComplScrollToSel()
}

// scriptComplWanted decides whether a keystroke should open a list.
//
// Only ever asked about a keystroke — scriptComplUpdate has already refused
// every caret move by the time this runs. One typed character is the threshold
// for a name, and none at all for a sigil: `@` and `.` are unambiguous requests,
// since there is exactly one kind of thing that can follow either, while an
// empty identifier position is every name in the language and is not a question
// anybody asked. Ctrl+Space overrides both.
func scriptComplWanted(ctx scriptComplCtx) bool {
	if ctx.Sigil {
		return true
	}
	switch ctx.Kind {
	case complMember, complProp, complAttr, complAddr, complParam, complTag, complAtom:
		// A sigil context reached with something typed: the author is partway
		// through a name that can only be one of these.
		return true
	case complNone:
		return false
	}
	return len(ctx.Prefix) > 0
}

// scriptComplSourceFor gathers everything the item list is built from.
func scriptComplSourceFor(d *scriptDoc) scriptComplSource {
	scan := scriptComplScanFile(d.Text)
	return scriptComplSource{
		Scan:           scan,
		Locals:         scan.LocalsAt(scriptBufCursor),
		ImportedFuncs:  d.ScopeFuncs(),
		ImportedParams: d.ScopeParams(),
		Refs:           scriptComplLevelCache.Refs,
		Tags:           scriptComplLevelCache.Tags,
		Attrs:          scriptComplLevelCache.Attrs,
		Props:          scriptComplLevelCache.Props,
		Kind:           scriptComplLevelCache.Kind,
	}
}

// scriptComplLoadLevel fills the level-derived lists for one context, if the
// cache does not already hold them.
func scriptComplLoadLevel(ctx scriptComplCtx) {
	c := &scriptComplLevelCache
	shape := string(rune(ctx.Kind)) + "|" + string(ctx.BaseSigil) + ctx.Base + "|" +
		strings.Join(ctx.Path, ".")
	now := sdl.GetTicks()
	if c.shape == shape && c.lvl == Editor.level && now-c.at < scriptComplLevelCacheMS {
		return
	}
	*c = scriptComplLevelCacheType{shape: shape, lvl: Editor.level, at: now}

	switch ctx.Kind {
	case complAddr:
		c.Refs = script.CompletionRefs(Editor.level)
	case complTag:
		c.Tags = script.CompletionTags(Editor.level)
	case complAttr:
		if id, ok := scriptComplBaseID(ctx); ok {
			c.Attrs = script.CompletionAttrs(Editor.level, id)
		}
	case complMember, complProp:
		c.Kind = scriptComplPathKind(scriptComplBaseKind(ctx), ctx.Path)
		if ctx.Kind == complProp {
			c.Props = scriptComplPropDocs(c.Kind)
		}
	}
}

// scriptComplBaseID returns the level id a `.attr.` chain hangs off, if it is
// one this side can name. `@self` is not: which object is running the script is
// the level's answer and there may be many of them.
func scriptComplBaseID(ctx scriptComplCtx) (string, bool) {
	if ctx.BaseSigil != '@' || len(ctx.Path) > 0 {
		return "", false
	}
	switch ctx.Base {
	case "", "self", "player", "level":
		return "", false
	}
	return ctx.Base, true
}

// scriptComplBaseKind resolves what the address at the head of a chain points
// at, by the same rules the linter's kindOf uses — `@self` deliberately
// included among the ones it will not guess.
func scriptComplBaseKind(ctx scriptComplCtx) flang.Kind_ {
	if ctx.BaseSigil != '@' {
		return flang.RefNone
	}
	switch ctx.Base {
	case "level":
		return flang.RefLevel
	case "player":
		return flang.RefEntity
	case "self", "":
		return flang.RefNone
	}
	if k, ok := script.CompletionKindOf(Editor.level, ctx.Base); ok {
		return k
	}
	return flang.RefNone
}

// scriptComplPropDocs returns one kind's properties, or every kind's when the
// kind is not knowable.
//
// The union is offered rather than nothing for the reason
// scriptComplStructuralOf gives: most refs a script holds arrive as a handler
// parameter, whose kind no amount of reading the buffer will reveal, and a
// property list that went blank there would be blank most of the time. A
// property offered on the wrong kind is caught by the linter on the next parse,
// with a message naming the kind.
func scriptComplPropDocs(kind flang.Kind_) []script.PropertyDoc {
	if kind != flang.RefNone {
		return script.PropertyDocs(kind)
	}
	seen := map[string]bool{}
	var out []script.PropertyDoc
	for _, k := range script.PropertyKinds() {
		for _, p := range script.PropertyDocs(k) {
			if seen[p.Name] {
				continue
			}
			seen[p.Name] = true
			p.Doc = k.String() + " — " + p.Doc
			out = append(out, p)
		}
	}
	return out
}

// ---------- drawing ----------

// scriptComplGroupColor is the colour per row group, taken from the buffer's
// own palette so that a name reads the same in the list as it will once it is
// typed.
var scriptComplGroupColor = map[string]imgui.Vec4{
	"fn":      scriptHLPalette[flang.ClassBuiltin],
	"kw":      scriptHLPalette[flang.ClassKeyword],
	"type":    scriptHLPalette[flang.ClassType],
	"atom":    scriptHLPalette[flang.ClassAtom],
	"ref":     scriptHLPalette[flang.ClassAddress],
	"tag":     scriptHLPalette[flang.ClassAddress],
	"param":   scriptHLPalette[flang.ClassParam],
	"builtin": scriptHLPalette[flang.ClassAddress],
	"var":     {X: 0.88, Y: 0.90, Z: 0.94, W: 1},
	"prop":    {X: 0.72, Y: 0.93, Z: 0.82, W: 1},
	"attr":    {X: 0.72, Y: 0.93, Z: 0.82, W: 1},
	"member":  {X: 0.72, Y: 0.93, Z: 0.82, W: 1},
}

// drawScriptComplPopup paints the list. Called at the end of the frame, after
// every window, so nothing can be drawn over it.
func drawScriptComplPopup() {
	st := &scriptComplState
	st.drawn = false
	if !st.Open || len(st.Items) == 0 || !scriptBufTextValid || !scriptBufActive {
		return
	}
	// The caret has scrolled out of the buffer's view: the list has nothing to
	// point at, and one left floating over the file tree would be nonsense.
	if !vec2InRect(st.anchor, scriptBufRectMin, scriptBufRectMax) {
		return
	}

	lineH := imgui.TextLineHeight()
	pad := imgui.Vec2{X: 8, Y: 4}
	rowH := lineH + 2
	st.rowH = rowH

	n := min(len(st.Items), scriptComplRows)
	var labelW, detailW float32
	for i := st.Top; i < min(st.Top+n, len(st.Items)); i++ {
		labelW = max(labelW, imgui.CalcTextSize(st.Items[i].Label, false, 0).X)
		detailW = max(detailW, imgui.CalcTextSize(scriptComplTrim(st.Items[i].Detail), false, 0).X)
	}
	const gap = 18
	w := labelW + gap + detailW + pad.X*2
	h := float32(n)*rowH + pad.Y*2

	// Under the caret's line by preference, over it when there is no room —
	// the list must never cover the word being typed.
	pos := imgui.Vec2{X: st.anchor.X, Y: st.anchor.Y + lineH + 2}
	view := imgui.Vec2{X: float32(scriptEditorWindow.W), Y: float32(scriptEditorWindow.H)}
	if pos.Y+h > view.Y {
		pos.Y = st.anchor.Y - h - 2
	}
	if pos.X+w > view.X {
		pos.X = max(view.X-w-4, 0)
	}
	minP := pos
	maxP := imgui.Vec2{X: pos.X + w, Y: pos.Y + h}
	st.rectMin, st.rectMax, st.drawn = minP, maxP, true

	list := imgui.ForegroundDrawList()
	bg := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.13, Y: 0.14, Z: 0.17, W: 0.97})
	border := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.32, Y: 0.35, Z: 0.42, W: 1})
	selBg := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.24, Y: 0.34, Z: 0.50, W: 1})
	muted := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.55, Y: 0.59, Z: 0.66, W: 1})
	list.AddRectFilled(minP, maxP, bg)
	list.AddRect(minP, maxP, border)

	for i := 0; i < n; i++ {
		idx := st.Top + i
		if idx >= len(st.Items) {
			break
		}
		it := st.Items[idx]
		y := minP.Y + pad.Y + float32(i)*rowH
		if idx == st.Sel {
			list.AddRectFilled(imgui.Vec2{X: minP.X + 1, Y: y},
				imgui.Vec2{X: maxP.X - 1, Y: y + rowH}, selBg)
		}
		col := imgui.Vec4{X: 0.88, Y: 0.90, Z: 0.94, W: 1}
		if c, ok := scriptComplGroupColor[it.Group]; ok {
			col = c
		}
		list.AddText(imgui.Vec2{X: minP.X + pad.X, Y: y + 1},
			imgui.PackedColorFromVec4(col), it.Label)
		if d := scriptComplTrim(it.Detail); d != "" {
			list.AddText(imgui.Vec2{X: minP.X + pad.X + labelW + gap, Y: y + 1}, muted, d)
		}
	}

	// A thumb on the right edge when the list is longer than the window, so
	// "there are more" is visible rather than something to discover by holding
	// Down.
	if len(st.Items) > n {
		trackH := h - 4
		thumbH := max(trackH*float32(n)/float32(len(st.Items)), 8)
		off := trackH * float32(st.Top) / float32(len(st.Items))
		x := maxP.X - 3
		list.AddRectFilled(imgui.Vec2{X: x, Y: minP.Y + 2 + off},
			imgui.Vec2{X: x + 2, Y: minP.Y + 2 + off + thumbH}, muted)
	}
}

// scriptComplTrim shortens a reference column that would otherwise set the
// list's width from one long sentence.
func scriptComplTrim(s string) string {
	if len(s) <= scriptComplMaxDetail {
		return s
	}
	return s[:scriptComplMaxDetail-1] + "…"
}

// ---------- keyboard and mouse ----------

// scriptComplScrollToSel brings the selected row into the visible window.
func scriptComplScrollToSel() {
	st := &scriptComplState
	switch {
	case st.Sel < st.Top:
		st.Top = st.Sel
	case st.Sel >= st.Top+scriptComplRows:
		st.Top = st.Sel - scriptComplRows + 1
	}
	if st.Top > len(st.Items)-scriptComplRows {
		st.Top = len(st.Items) - scriptComplRows
	}
	if st.Top < 0 {
		st.Top = 0
	}
}

// scriptComplMove steps the selection by n rows, stopping at either end rather
// than wrapping: a list that wraps makes "am I at the bottom" a question.
func scriptComplMove(n int) {
	st := &scriptComplState
	st.Sel = min(max(st.Sel+n, 0), len(st.Items)-1)
	scriptComplScrollToSel()
}

// scriptComplRequestAccept parks a row for the text area's callback.
func scriptComplRequestAccept(i int) {
	if i < 0 || i >= len(scriptComplState.Items) {
		return
	}
	scriptComplState.accept = i
}

// scriptComplHandleKey takes the keys the list needs before ImGui can read them
// as edits.
//
// Every one of them is a key the text area also wants, which is why this is in
// the event filter and not in HandleEvent: by the time that runs the widget has
// already moved the caret or inserted a tab. Only while the list is actually
// up — with it shut, every one of these does what it always did.
func scriptComplHandleKey(t sdl.KeyboardEvent) bool {
	st := &scriptComplState
	ctrl := sdl.GetModState()&sdl.KMOD_CTRL != 0

	// Ctrl+Space asks for a list where one would not open itself.
	if ctrl && t.Keysym.Scancode == sdl.SCANCODE_SPACE {
		st.force = true
		st.dismissed = ""
		st.cursor = -1 // force a rebuild on the next frame
		return true
	}
	if !st.Open || ctrl {
		return false
	}
	switch t.Keysym.Scancode {
	case sdl.SCANCODE_DOWN:
		scriptComplMove(1)
	case sdl.SCANCODE_UP:
		scriptComplMove(-1)
	case sdl.SCANCODE_PAGEDOWN:
		scriptComplMove(scriptComplRows)
	case sdl.SCANCODE_PAGEUP:
		scriptComplMove(-scriptComplRows)
	case sdl.SCANCODE_TAB:
		scriptComplRequestAccept(st.Sel)
	case sdl.SCANCODE_RETURN, sdl.SCANCODE_KP_ENTER:
		// A selection that is already exactly what is typed is not worth an
		// Enter: at that point the author is finishing a line, not choosing a
		// name, and swallowing the newline would be the list getting in the way.
		if st.Sel < len(st.Items) && st.Items[st.Sel].Text == st.Ctx.Prefix {
			scriptComplClose()
			return false
		}
		scriptComplRequestAccept(st.Sel)
	case sdl.SCANCODE_ESCAPE:
		// Remembered, so that the next keystroke does not bring back the list
		// that was just refused. Escape is swallowed here for the same reason
		// scriptEditorFilterEvent swallows it: the widget reads it as "revert".
		st.dismissed = st.Ctx.Prefix
		scriptComplClose()
		return true
	default:
		return false
	}
	return true
}

// scriptComplHandleMouse takes a click or a wheel over the list.
//
// The list is painted rather than built out of widgets, so ImGui does not know
// it is there and would hand the click to the text area underneath — moving the
// caret to wherever the row happened to sit. Swallowing it here is what makes a
// click on a row mean the row.
func scriptComplHandleMouse(event sdl.Event) bool {
	st := &scriptComplState
	if !st.Open || !st.drawn {
		return false
	}
	switch t := event.(type) {
	case sdl.MouseButtonEvent:
		if t.Button != sdl.ButtonLeft {
			return false
		}
		row, ok := scriptComplRowAt(float32(t.X), float32(t.Y))
		if !ok {
			return false
		}
		// The release is swallowed with the press: a press consumed on its own
		// leaves ImGui holding a button that never came up.
		if t.Type == sdl.MOUSEBUTTONDOWN {
			st.Sel = row
			scriptComplRequestAccept(row)
		}
		return true

	case sdl.MouseWheelEvent:
		x, y, _ := sdl.GetMouseState()
		if _, ok := scriptComplRowAt(float32(x), float32(y)); !ok {
			return false
		}
		scriptComplMove(-int(t.Y))
		return true
	}
	return false
}

// scriptComplRowAt returns the row under a point in window coordinates.
func scriptComplRowAt(x, y float32) (int, bool) {
	st := &scriptComplState
	if !vec2InRect(imgui.Vec2{X: x, Y: y}, st.rectMin, st.rectMax) || st.rowH <= 0 {
		return 0, false
	}
	row := st.Top + int((y-st.rectMin.Y-4)/st.rowH)
	if row < st.Top || row >= len(st.Items) {
		return 0, false
	}
	return row, true
}

// scriptComplApply splices the parked row into the widget's own buffer.
//
// The buffer and the caret are read back from the callback rather than from
// what the popup was built with: a frame may have passed since the row was
// picked, and the widget's copy is the only one that is certainly current.
func scriptComplApply(data imgui.InputTextCallbackData) {
	st := &scriptComplState
	i := st.accept
	st.accept = -1
	if i < 0 || i >= len(st.Items) {
		return
	}
	it := st.Items[i]

	text := string(data.Buffer())
	cursor := data.CursorPos()
	ctx := scriptComplContext(text, cursor)
	if ctx.Kind == complNone {
		scriptComplClose()
		return
	}
	e := scriptComplAccept(ctx, it, text, cursor)

	// DeleteBytes/InsertBytes rather than a wholesale replacement, so the
	// widget's undo history sees an ordinary edit and Ctrl+Z takes back a
	// completion the way it takes back typing.
	if e.End > e.Start {
		data.DeleteBytes(e.Start, e.End-e.Start)
	}
	if e.Text != "" {
		data.InsertBytes(e.Start, []byte(e.Text))
	}
	data.SetCursorPos(e.Caret)
	data.SetSelectionStart(e.Caret)
	data.SetSelectionEnd(e.Caret)

	scriptComplClose()
	st.Ctx = scriptComplCtx{}

	// The list's idea of the buffer is moved forward to what the splice just
	// made, rather than invalidated. Invalidating it would have the rest of
	// this frame rebuild the list against the completed word and open it again
	// on the one row the author has just chosen; leaving it current means the
	// list stays shut until something is actually typed.
	//
	// The caret is carried across for the same reason: the callback recorded it
	// before the splice, and the gutter, the scroll-to-caret and the popup's
	// anchor all read it.
	st.src = text[:e.Start] + e.Text + text[e.End:]
	st.cursor = e.Caret
	scriptBufCursor = e.Caret
}

// scriptComplSetAnchor records where the list should hang from, in screen
// coordinates.
//
// The word's first character rather than the caret: a list that moved sideways
// with every keystroke is harder to read than one that stays put under the name
// it is completing.
func scriptComplSetAnchor(lines [][]scriptHLChunk, origin imgui.Vec2, lineH float32) {
	at := scriptBufCursor
	// The context is read even when the list is shut, so that the frame it
	// opens on is already anchored to the word rather than to the caret — one
	// frame of the list sliding sideways is one frame too many.
	if c := scriptComplState.Ctx; c.Kind != complNone && c.Start >= 0 && c.Start <= at {
		at = c.Start
	}
	line, x := scriptCaretPos(lines, at)
	if line < 0 {
		return
	}
	scriptComplState.anchor = imgui.Vec2{X: origin.X + x, Y: origin.Y + float32(line)*lineH}
}

// The parked row is an index, so "nothing parked" has to be said explicitly
// rather than being the zero value — row 0 is the one accepted most often.
func init() { scriptComplState.accept = -1 }
