package editor

// editor_yaml_complete_ui.go — the level YAML editor's completion popup.
//
// The script editor's popup (editor_script_complete_ui.go), rebuilt for this
// window's state: painted into the foreground draw list rather than opened as
// a window, with its keys taken in the SDL event filter — both because the text
// widget must never lose the keyboard while it is up. That file explains the
// constraint; this one follows it.
//
// What this popup adds is a documentation panel beside the list for the row
// that is selected: a key's comment from the schema is usually the only place
// its meaning, its units and its default are written down, and a list of bare
// key names is not much help choosing between `offset` and `origin`.

import (
	"sort"
	"strings"

	"foundry/internal/audio"
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/levelio"
	"foundry/internal/ui"
	"foundry/internal/world"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// yamlCompl is the popup's state.
var yamlCompl = struct {
	Open  bool
	Ctx   yamlComplCtx
	Items []scriptComplItem
	Sel   int
	Top   int

	// src and cursor are what the list was built from.
	src    string
	cursor int

	dismissed string
	force     bool
	// accept is the row parked for the callback, or -1.
	accept int

	anchor           imgui.Vec2
	rowH             float32
	rectMin, rectMax imgui.Vec2
	drawn            bool
}{accept: -1}

// yamlComplClose puts the list away.
func yamlComplClose() {
	yamlCompl.Open = false
	yamlCompl.Items = nil
	yamlCompl.accept = -1
	yamlCompl.force = false
	yamlCompl.drawn = false
}

// yamlComplUpdate rebuilds the list for the caret, when the text or the caret
// has moved since it was built.
func yamlComplUpdate(d *yamlDoc) {
	st := &yamlCompl
	if st.src == d.Text && st.cursor == yamlEd.cursor && !st.force {
		return
	}
	// As in the script editor, only typing opens a list; a caret moved by the
	// arrows or the mouse puts one away.
	typed := st.src != d.Text && st.src != ""
	wasOpen, prevPrefix := st.Open, st.Ctx.Prefix
	st.src, st.cursor = d.Text, yamlEd.cursor

	ctx := yamlComplContext(d.Index(), yamlEd.cursor)
	if ctx.Kind == yamlComplNone || (!typed && !st.force) {
		st.Ctx = ctx
		st.dismissed = ""
		yamlComplClose()
		return
	}
	if st.dismissed != "" && !st.force {
		if strings.HasPrefix(ctx.Prefix, st.dismissed) {
			st.Ctx = ctx
			return
		}
		st.dismissed = ""
	}
	if !st.force && !yamlComplWanted(ctx, d.Text, yamlEd.cursor) {
		st.Ctx = ctx
		yamlComplClose()
		return
	}

	items := scriptComplFilter(yamlComplItems(ctx, yamlComplSourceFor(d)), ctx.Prefix)
	// A list whose only row is exactly what is typed has nothing to offer.
	if len(items) == 1 && items[0].Text == ctx.Prefix && !st.force {
		items = nil
	}
	st.Ctx, st.Items = ctx, items
	st.Open = len(items) > 0
	st.force = false
	if !st.Open {
		return
	}
	if !wasOpen || !strings.HasPrefix(ctx.Prefix, prevPrefix) || st.Sel >= len(items) {
		st.Sel, st.Top = 0, 0
	}
	yamlComplScrollToSel()
}

// yamlComplWanted decides whether a keystroke should open the list: a key once
// a character of it is typed, a value once a character is typed or the colon's
// space has just been — `shape: ` is exactly the moment the options are wanted.
func yamlComplWanted(ctx yamlComplCtx, text string, cursor int) bool {
	if ctx.Prefix != "" {
		return true
	}
	if ctx.Kind != yamlComplValue {
		return false
	}
	return cursor >= 2 && (text[cursor-2:cursor] == ": " || text[cursor-2:cursor] == "- ")
}

// yamlComplSourceFor wires the providers and the file scan.
func yamlComplSourceFor(d *yamlDoc) yamlComplSource {
	return yamlComplSource{
		Providers: yamlValueProviders,
		Observed: func(key string) []string {
			return yamlObservedCached(d, key)
		},
	}
}

// yamlObservedCache holds the last file scan, for one key and one version of
// the text.
var yamlObservedCache struct {
	key  string
	gen  int
	line int
	vals []string
}

func yamlObservedCached(d *yamlDoc, key string) []string {
	c := &yamlObservedCache
	line := d.Index().LineOf(yamlEd.cursor)
	if c.key == key && c.gen == d.gen && c.line == line && c.vals != nil {
		return c.vals
	}
	c.key, c.gen, c.line = key, d.gen, line
	c.vals = yamlObservedValues(d.Text, key, yamlEd.cursor, 300)
	return c.vals
}

// yamlProviderCacheMS is how long a folder listing is trusted. The folders
// change rarely and under the author's own hand, and listing them on every
// keystroke of a value would be a directory read per character.
const yamlProviderCacheMS = 3000

var yamlProviderCache = map[string]struct {
	at   uint32
	vals []string
}{}

// yamlProvided returns a named list, from the cache when it is fresh.
func yamlProvided(name string, list func() []string) []string {
	now := sdl.GetTicks()
	if c, ok := yamlProviderCache[name]; ok && now-c.at < yamlProviderCacheMS {
		return c.vals
	}
	vals := list()
	yamlProviderCache[name] = struct {
		at   uint32
		vals []string
	}{now, vals}
	return vals
}

// yamlValueProviders names the files a field can point at, by the key and the
// struct it belongs to, in the form the level editor writes them.
func yamlValueProviders(ctx yamlComplCtx) []string {
	owner := ""
	if ctx.HasField && ctx.Field.Owner != nil {
		owner = ctx.Field.Owner.Name()
	}
	switch {
	case ctx.Key == "script" || ctx.Key == "scripts":
		return yamlProvided("scripts", func() []string {
			files := fileutil.ScanScriptFiles()
			out := make([]string, len(files))
			for i, f := range files {
				out[i] = fileutil.ScriptsDirName + "/" + f
			}
			return out
		})
	case ctx.Key == "texture":
		return yamlProvided("textures", func() []string {
			out := make([]string, 0, len(gfx.TextureNames))
			for n := range gfx.TextureNames {
				out = append(out, n)
			}
			sort.Strings(out)
			return out
		})
	case ctx.Key == "ui" && owner == "RawLevel":
		return yamlProvided("ui", func() []string {
			files := ui.ScanScreenFiles()
			out := make([]string, len(files))
			for i, f := range files {
				out[i] = strings.TrimSuffix(f, ui.UIExt)
			}
			return out
		})
	case ctx.Key == "start_level" && owner == "Manifest":
		return yamlProvided("levels", func() []string {
			files := levelio.ScanLevelFiles(Editor.LevelsDir)
			out := make([]string, len(files))
			for i, f := range files {
				out[i] = strings.TrimSuffix(f, levelio.LevelExt)
			}
			return out
		})
	case ctx.Key == "type" && owner == "RawEntity":
		return yamlProvided("entities", world.ScanEntityDefs)
	case ctx.Key == "file" && owner == "RawPrefabInstance":
		return yamlProvided("prefabs", levelio.ScanPrefabNames)
	case ctx.Key == "effect" && owner == "RawEmitter":
		return yamlProvided("particles", particleEffectNames)
	case ctx.Key == "model" && owner == "RawScenery":
		return yamlProvided("models", modelFileNames)
	case ctx.Key == "clip" || ctx.Key == "track":
		return yamlProvided("sounds", audio.ScanSoundNames)
	}
	return nil
}

// ---------- drawing ----------

// yamlComplGroupColor colours a row by what it is, from the buffer's palette.
var yamlComplGroupColor = map[string]imgui.Vec4{
	"yamlkey":  scriptHLPalette[yamlClassKey],
	"yamlbool": scriptHLPalette[yamlClassConst],
	"yamlenum": scriptHLPalette[yamlClassConst],
	"yamlfile": scriptHLPalette[yamlClassString],
	"yamlseen": scriptHLPalette[yamlClassString],
}

// yamlComplDoc is the documentation shown beside the selected row.
func yamlComplDoc(it scriptComplItem) (head, doc string) {
	ctx := yamlCompl.Ctx
	switch ctx.Kind {
	case yamlComplKey:
		f, ok := yamlFieldNamed(ctx.Type, it.Text)
		if !ok {
			return "", ""
		}
		return f.Name + ": " + yamlTypeLabel(f.Type), yamlFieldDoc(f)
	case yamlComplValue:
		if ctx.HasField && it.Group == "yamlenum" {
			return ctx.Key, yamlFieldDoc(ctx.Field)
		}
	}
	return "", ""
}

// yamlWrap breaks text into lines no wider than w pixels, at spaces.
func yamlWrap(text string, w float32) []string {
	var out []string
	for _, para := range strings.Split(text, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			try := word
			if line != "" {
				try = line + " " + word
			}
			if line != "" && imgui.CalcTextSize(try, false, 0).X > w {
				out = append(out, line)
				line = word
				continue
			}
			line = try
		}
		out = append(out, line)
	}
	return out
}

// drawYAMLComplPopup paints the list, and the selected row's documentation
// beside it. Called at the end of the frame, after every window.
func drawYAMLComplPopup() {
	st := &yamlCompl
	st.drawn = false
	if !st.Open || len(st.Items) == 0 || !yamlEd.textValid || !yamlEd.active || yamlEditorWindow == nil {
		return
	}
	if !vec2InRect(st.anchor, yamlEd.rectMin, yamlEd.rectMax) {
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

	view := imgui.Vec2{X: float32(yamlEditorWindow.W), Y: float32(yamlEditorWindow.H)}
	pos := imgui.Vec2{X: st.anchor.X, Y: st.anchor.Y + lineH + 2}
	if pos.Y+h > view.Y {
		pos.Y = st.anchor.Y - h - 2
	}
	if pos.X+w > view.X {
		pos.X = max(view.X-w-4, 0)
	}
	minP, maxP := pos, imgui.Vec2{X: pos.X + w, Y: pos.Y + h}
	st.rectMin, st.rectMax, st.drawn = minP, maxP, true

	list := imgui.ForegroundDrawList()
	bg := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.13, Y: 0.14, Z: 0.17, W: 0.97})
	border := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.32, Y: 0.35, Z: 0.42, W: 1})
	selBg := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.24, Y: 0.34, Z: 0.50, W: 1})
	muted := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.55, Y: 0.59, Z: 0.66, W: 1})
	text := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.88, Y: 0.90, Z: 0.94, W: 1})
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
			list.AddRectFilled(imgui.Vec2{X: minP.X + 1, Y: y}, imgui.Vec2{X: maxP.X - 1, Y: y + rowH}, selBg)
		}
		col := imgui.Vec4{X: 0.88, Y: 0.90, Z: 0.94, W: 1}
		if c, ok := yamlComplGroupColor[it.Group]; ok {
			col = c
		}
		list.AddText(imgui.Vec2{X: minP.X + pad.X, Y: y + 1}, imgui.PackedColorFromVec4(col), it.Label)
		if dt := scriptComplTrim(it.Detail); dt != "" {
			list.AddText(imgui.Vec2{X: minP.X + pad.X + labelW + gap, Y: y + 1}, muted, dt)
		}
	}
	if len(st.Items) > n {
		trackH := h - 4
		thumbH := max(trackH*float32(n)/float32(len(st.Items)), 8)
		off := trackH * float32(st.Top) / float32(len(st.Items))
		x := maxP.X - 3
		list.AddRectFilled(imgui.Vec2{X: x, Y: minP.Y + 2 + off}, imgui.Vec2{X: x + 2, Y: minP.Y + 2 + off + thumbH}, muted)
	}

	// The selected row's documentation, to the right of the list — or to its
	// left when the window has no room there.
	if st.Sel < 0 || st.Sel >= len(st.Items) {
		return
	}
	head, doc := yamlComplDoc(st.Items[st.Sel])
	if doc == "" {
		return
	}
	docW := min(float32(380), view.X*0.45)
	lines := yamlWrap(doc, docW-pad.X*2)
	if len(lines) > 16 {
		lines = append(lines[:15], "…")
	}
	dh := float32(len(lines)+1)*lineH + pad.Y*3
	dp := imgui.Vec2{X: maxP.X + 4, Y: minP.Y}
	if dp.X+docW > view.X {
		dp.X = minP.X - docW - 4
	}
	if dp.X < 0 {
		return // no room either side; the list alone is still useful
	}
	if dp.Y+dh > view.Y {
		dp.Y = max(view.Y-dh-4, 0)
	}
	list.AddRectFilled(dp, imgui.Vec2{X: dp.X + docW, Y: dp.Y + dh}, bg)
	list.AddRect(dp, imgui.Vec2{X: dp.X + docW, Y: dp.Y + dh}, border)
	list.AddText(imgui.Vec2{X: dp.X + pad.X, Y: dp.Y + pad.Y},
		imgui.PackedColorFromVec4(scriptHLPalette[yamlClassKey]), head)
	for i, l := range lines {
		list.AddText(imgui.Vec2{X: dp.X + pad.X, Y: dp.Y + pad.Y*2 + float32(i+1)*lineH}, text, l)
	}
}

// ---------- keys and mouse ----------

func yamlComplScrollToSel() {
	st := &yamlCompl
	switch {
	case st.Sel < st.Top:
		st.Top = st.Sel
	case st.Sel >= st.Top+scriptComplRows:
		st.Top = st.Sel - scriptComplRows + 1
	}
	st.Top = max(min(st.Top, len(st.Items)-scriptComplRows), 0)
}

func yamlComplMove(n int) {
	st := &yamlCompl
	st.Sel = min(max(st.Sel+n, 0), len(st.Items)-1)
	yamlComplScrollToSel()
}

// yamlComplHandleKey takes the list's keys before ImGui can read them as
// edits. See scriptComplHandleKey.
func yamlComplHandleKey(t sdl.KeyboardEvent) bool {
	st := &yamlCompl
	ctrl := sdl.GetModState()&sdl.KMOD_CTRL != 0
	if ctrl && t.Keysym.Scancode == sdl.SCANCODE_SPACE {
		st.force = true
		st.dismissed = ""
		st.cursor = -1
		return true
	}
	if !st.Open || ctrl {
		return false
	}
	switch t.Keysym.Scancode {
	case sdl.SCANCODE_DOWN:
		yamlComplMove(1)
	case sdl.SCANCODE_UP:
		yamlComplMove(-1)
	case sdl.SCANCODE_PAGEDOWN:
		yamlComplMove(scriptComplRows)
	case sdl.SCANCODE_PAGEUP:
		yamlComplMove(-scriptComplRows)
	case sdl.SCANCODE_TAB:
		st.accept = st.Sel
	case sdl.SCANCODE_RETURN, sdl.SCANCODE_KP_ENTER:
		if st.Sel < len(st.Items) && st.Items[st.Sel].Text == st.Ctx.Prefix {
			yamlComplClose()
			return false
		}
		st.accept = st.Sel
	case sdl.SCANCODE_ESCAPE:
		st.dismissed = st.Ctx.Prefix
		yamlComplClose()
		return true
	default:
		return false
	}
	return true
}

// yamlComplHandleMouse takes a click or a wheel over the painted list.
func yamlComplHandleMouse(event sdl.Event) bool {
	st := &yamlCompl
	if !st.Open || !st.drawn {
		return false
	}
	switch t := event.(type) {
	case sdl.MouseButtonEvent:
		if t.Button != sdl.ButtonLeft {
			return false
		}
		row, ok := yamlComplRowAt(float32(t.X), float32(t.Y))
		if !ok {
			return false
		}
		if t.Type == sdl.MOUSEBUTTONDOWN {
			st.Sel = row
			st.accept = row
		}
		return true
	case sdl.MouseWheelEvent:
		x, y, _ := sdl.GetMouseState()
		if _, ok := yamlComplRowAt(float32(x), float32(y)); !ok {
			return false
		}
		yamlComplMove(-int(t.Y))
		return true
	}
	return false
}

func yamlComplRowAt(x, y float32) (int, bool) {
	st := &yamlCompl
	if !vec2InRect(imgui.Vec2{X: x, Y: y}, st.rectMin, st.rectMax) || st.rowH <= 0 {
		return 0, false
	}
	row := st.Top + int((y-st.rectMin.Y-4)/st.rowH)
	if row < st.Top || row >= len(st.Items) {
		return 0, false
	}
	return row, true
}

// yamlComplApply splices the parked row into the widget's buffer, working the
// context out again from the widget's own text, which is the only certainly
// current copy.
func yamlComplApply(data imgui.InputTextCallbackData) {
	st := &yamlCompl
	i := st.accept
	st.accept = -1
	if i < 0 || i >= len(st.Items) {
		return
	}
	it := st.Items[i]
	text := string(data.Buffer())
	cursor := data.CursorPos()
	ctx := yamlComplContext(newYAMLLineIndex(text), cursor)
	if ctx.Kind == yamlComplNone {
		yamlComplClose()
		return
	}
	e := yamlComplAccept(ctx, it)
	if e.End > e.Start {
		data.DeleteBytes(e.Start, e.End-e.Start)
	}
	if e.Text != "" {
		data.InsertBytes(e.Start, []byte(e.Text))
	}
	data.SetCursorPos(e.Caret)
	data.SetSelectionStart(e.Caret)
	data.SetSelectionEnd(e.Caret)
	yamlEd.cbRecord = max(yamlEd.cbRecord, yamlRecordSealed)

	yamlComplClose()
	st.Ctx = yamlComplCtx{}
	// Moved forward to the result rather than invalidated — see
	// scriptComplApply for the reopening this prevents.
	st.src = text[:e.Start] + e.Text + text[e.End:]
	st.cursor = e.Caret
	yamlEd.cursor = e.Caret
	yamlEd.sel = scriptSel{e.Caret, e.Caret}
}

// yamlComplSetAnchor records where the list hangs from: the start of the word
// being completed, so the list does not slide sideways as it is typed.
func yamlComplSetAnchor(ix *yamlLineIndex, origin imgui.Vec2, lineH float32) {
	at := yamlEd.cursor
	if c := yamlCompl.Ctx; c.Kind != yamlComplNone && c.Start >= 0 && c.Start <= at {
		at = c.Start
	}
	_, x, y := yamlCaretXY(ix, at, lineH)
	yamlCompl.anchor = imgui.Vec2{X: origin.X + x, Y: origin.Y + y}
}
