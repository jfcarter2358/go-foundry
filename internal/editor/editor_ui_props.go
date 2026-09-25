package editor

// editor_ui_props.go — the UI editor's properties panel.
//
// SETTINGS_LAYOUT.md's ordering throughout: Identity, Transform, then whatever
// is specific to the kind, then Scripting, then Actions. With the root selected
// the panel becomes the *screen's* — its canvas and its three input semantics —
// because the root is the canvas and has nothing else to say.
//
// Every control that can be dragged calls uiBeginEdit with a label of its own,
// so a slider dragged across the panel is one undo entry rather than sixty.

import (
	"strconv"

	"foundry/internal/imguibackend"
	"foundry/internal/ui"
	"foundry/internal/uiwin"
	"foundry/internal/video"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

func uiEditorPanel(w *uiwin.UIWindow) {
	imgui.SetNextWindowPos(imgui.Vec2{X: float32(w.W) - uiPanelWidth, Y: uiToolbarH})
	imgui.SetNextWindowSize(imgui.Vec2{X: uiPanelWidth, Y: float32(w.H) - uiToolbarH})
	imgui.BeginV("Properties##uipanel", nil,
		imgui.WindowFlagsNoMove|imgui.WindowFlagsNoResize|imgui.WindowFlagsNoSavedSettings)

	if uiDoc == nil {
		imgui.End()
		return
	}

	if imgui.BeginTabBar("##uipaneltabs") {
		if imgui.BeginTabItem("Properties") {
			uiPropertiesTab()
			imgui.EndTabItem()
		}
		if imgui.BeginTabItem("Theme") {
			uiThemeTab()
			imgui.EndTabItem()
		}
		imgui.EndTabBar()
	}
	imgui.End()
}

// uiPropertiesTab shows the screen when the root is selected and a widget
// otherwise. The root is the canvas: it has a canvas and three input semantics
// to configure, and nothing else to say.
func uiPropertiesTab() {
	sel := uiSelected()
	if sel == nil {
		editorTextMutedWrapped("Nothing selected. Pick a widget on the canvas or in the hierarchy.")
		return
	}
	if sel.ID == uiDoc.Root.ID {
		uiScreenProperties()
		return
	}
	uiNodeProperties(sel)
}

// ---------- the screen ----------

func uiScreenProperties() {
	s := uiDoc

	editorPropGroupOpen("Screen", func() {
		id := s.ID
		if imgui.InputText("ID##uiscreenid", &id) {
			uiBeginEdit("screen.id")
			s.ID = id
			uiEdited()
		}
		editorTextMuted("The name a level's ui: list and show_screen use.")
	})

	editorPropGroupOpen("Canvas", func() {
		mode := int32(s.Canvas.Mode)
		if imguibackend.Combo("Scale##uicanvasmode", &mode, ui.ScaleModeNames) {
			uiBeginEdit("canvas.mode")
			s.Canvas.Mode = ui.ScaleMode(mode)
			uiEdited()
		}
		if s.Canvas.Mode == ui.ScaleWithScreen {
			if editorDragFloat("Reference width", &s.Canvas.RefW, 320, 7680, "%.0f") {
				uiBeginEdit("canvas.refw")
				uiEdited()
			}
			if editorDragFloat("Reference height", &s.Canvas.RefH, 240, 4320, "%.0f") {
				uiBeginEdit("canvas.refh")
				uiEdited()
			}
			if editorDragFloat("Match width/height", &s.Canvas.Match, 0, 1, "%.2f") {
				uiBeginEdit("canvas.match")
				uiEdited()
			}
			editorTextMutedWrapped("0 follows the window's width, 1 its height, 0.5 splits the difference.")
		} else {
			editorTextMutedWrapped("One canvas unit is one device pixel. For a pixel-art HUD, where a fractional scale would blur the sprites.")
		}
		if imgui.Checkbox("Snap to pixels##uisnap", &s.Canvas.PixelSnap) {
			uiBeginEdit("canvas.snap")
			uiEdited()
		}
	})

	editorPropGroupOpen("Input", func() {
		if imgui.Checkbox("Modal##uimodal", &s.Modal) {
			uiBeginEdit("screen.modal")
			uiEdited()
		}
		editorTextMuted("Nothing below this screen sees input.")
		if imgui.Checkbox("Pauses the game##uipauses", &s.Pauses) {
			uiBeginEdit("screen.pauses")
			uiEdited()
		}
		editorTextMuted("The player, vehicles and entities stop. Scripts do not.")
		if imgui.Checkbox("Shows the cursor##uicursor", &s.ShowsCursor) {
			uiBeginEdit("screen.cursor")
			uiEdited()
		}
		editorTextMuted("Mouse-look is released while this screen is up.")
		if imgui.Checkbox("Clicks reach the world##uiclickthrough", &s.ClickThrough) {
			uiBeginEdit("screen.click_through")
			uiEdited()
		}
		editorTextMuted("A click on none of this screen's widgets reaches the level behind it, even while modal.")

		uiNodeIDCombo("Initial focus", &s.InitialFocus, true)
	})

	uiScriptingProperties(&s.ScriptData, "uiscreen", strconv.Quote(s.ID))
}

// ---------- scripting ----------

// uiScriptingProperties is the tags / attributes / script group, for the screen
// or a widget — the same keys a level object carries, so the same widgets.
//
// Those shared widgets push through EditorPushUndo, which here has to land on
// this document's stack rather than the level's, and mark the document changed.
// So the group draws inside withUndoRedirect, the way the entity editor's does.
func uiScriptingProperties(d *world.ScriptData, idSuffix, owner string) {
	withUndoRedirect(uiPushUndoEdited, func() { editorScriptDataUI(d, idSuffix, owner) })
}

// uiPushUndoEdited is that redirect: a snapshot, then the change mark.
func uiPushUndoEdited() {
	UIPushUndo()
	uiEdited()
}

// ---------- a node ----------

func uiNodeProperties(n *ui.Node) {
	editorPropGroupOpen("Identity", func() {
		id := n.ID
		if imgui.InputText("ID##uinodeid", &id) && id != "" {
			uiBeginEdit("node.id")
			uiRenameNode(n, id)
			uiEdited()
		}
		editorTextMuted("Kind: " + ui.KindName(n.Kind))

		if imgui.Checkbox("Visible##uivisible", &n.Visible) {
			uiBeginEdit("node.visible")
			uiEdited()
		}
		if n.Kind.Interactive() {
			if imgui.Checkbox("Enabled##uienabled", &n.Enabled) {
				uiBeginEdit("node.enabled")
				uiEdited()
			}
		}
		if imgui.Checkbox("Blocks input##uiblocks", &n.Blocks) {
			uiBeginEdit("node.blocks")
			uiEdited()
		}
		editorTextMuted("Swallow a click landing here even if this kind is inert.")
		if imgui.Checkbox("Draggable##uidraggable", &n.Draggable) {
			uiBeginEdit("node.draggable")
			uiEdited()
		}
		editorTextMuted("Pressing and moving it posts :drag_start:, :drag: and :drop:.")
		if editorDragFloat("Alpha", &n.Alpha, 0, 1, "%.2f") {
			uiBeginEdit("node.alpha")
			uiEdited()
		}
		editorTextMuted("Multiplied down the whole subtree.")
	})

	editorPropGroupOpen("Rect", func() { uiRectProperties(n) })
	// Open, not collapsed. The background colour is the single most-edited
	// property on any widget, and behind a shut header labelled "Style" it was
	// unfindable — a label with a background nobody could explain reads as a
	// renderer bug rather than as a colour someone can change.
	editorPropGroupOpen("Style", func() { uiStyleProperties(n) })

	if n.Kind.Interactive() {
		editorPropGroup("States", 0, func() { uiStateProperties(n) })
	}
	switch n.Kind {
	case ui.KindText, ui.KindButton, ui.KindInput, ui.KindToggle:
		editorPropGroupOpen("Text", func() { uiTextProperties(n) })
	}
	switch n.Kind {
	case ui.KindBar, ui.KindSlider, ui.KindToggle:
		editorPropGroupOpen("Value", func() { uiValueProperties(n) })
	}
	if n.Kind == ui.KindVideo {
		editorPropGroupOpen("Video", func() { uiVideoProperties(n) })
	}
	if n.Kind.Interactive() {
		editorPropGroup("Navigation", 0, func() { uiNavProperties(n) })
	}
	if n.Kind == ui.KindInclude {
		editorPropGroupOpen("Include", func() { uiIncludeProperties(n) })
	}

	uiScriptingProperties(&n.ScriptData, "uinode", strconv.Quote(n.ID))

	editorPropGroupOpen("Actions", func() {
		if imgui.Button("Duplicate##uidup") {
			uiDuplicateSelection()
		}
		imgui.SameLine()
		if imgui.Button("Wrap in panel##uiwrap") {
			uiWrapInPanel()
		}
		imgui.SameLine()
		if imgui.Button("Delete##uidel") {
			uiDeleteSelection()
		}
	})
}

// uiRenameNode changes an ID, refusing one that is already taken — two nodes
// sharing an ID makes every reference to it a coin flip, and the format will
// not save it.
func uiRenameNode(n *ui.Node, id string) {
	if other := uiDoc.Find(id); other != nil && other != n {
		uiStatus = "There is already a node called " + id
		return
	}
	old := n.ID
	n.ID = id
	if uiSel == old {
		uiSel = id
	}
}

func uiRectProperties(n *ui.Node) {
	// The preset grid first. Typing four anchor numbers to mean "bottom left"
	// is where anchors stop being worth it, and every one of these is a pair
	// an author would otherwise have to know by heart.
	parent := uiParentOf(n)
	editorTextMuted("Anchor preset")
	for i, p := range ui.AnchorPresets {
		if i%3 != 0 {
			imgui.SameLine()
		}
		active := n.Rect.AnchorMin == p.Min && n.Rect.AnchorMax == p.Max
		if editorActiveToggleButton(p.Name+"##uianchor", active) && parent != nil {
			uiBeginEdit("rect.preset")
			ui.SetAnchorsPreservingRect(n, parent.Solved(), p.Min, p.Max)
			uiEdited()
		}
	}
	imgui.Separator()

	// Then the numbers, spelled the way the axis actually works: a pinned axis
	// gets a position and a size, a stretched one gets its two insets. Showing
	// both spellings at once is where RectTransform's reputation comes from.
	uiAxisProperties(n, true)
	uiAxisProperties(n, false)

	pivot := [3]float32{n.Rect.Pivot[0], n.Rect.Pivot[1], 0}
	if editorDragFloat("Pivot X", &pivot[0], 0, 1, "%.2f") ||
		editorDragFloat("Pivot Y", &pivot[1], 0, 1, "%.2f") {
		uiBeginEdit("rect.pivot")
		n.Rect.Pivot = mgl32.Vec2{pivot[0], pivot[1]}
		uiEdited()
	}

	if n.Kind.Leaf() {
		if editorDragFloat("Rotation", &n.Rect.Rotation, -6.283, 6.283, "%.3f") {
			uiBeginEdit("rect.rotation")
			uiEdited()
		}
		editorTextMuted("Radians. Leaf kinds only — a rotated container cannot be clipped.")
	}

	scale := [3]float32{n.Rect.Scale[0], n.Rect.Scale[1], 0}
	if editorDragFloat("Scale X", &scale[0], 0, 8, "%.2f") ||
		editorDragFloat("Scale Y", &scale[1], 0, 8, "%.2f") {
		uiBeginEdit("rect.scale")
		n.Rect.Scale = mgl32.Vec2{scale[0], scale[1]}
		uiEdited()
	}

	if imgui.Checkbox("Clip children##uiclip", &n.Clip) {
		uiBeginEdit("rect.clip")
		uiEdited()
	}
}

// uiAxisProperties shows one axis, in whichever spelling fits how it is
// anchored.
func uiAxisProperties(n *ui.Node, horizontal bool) {
	stretched := n.Rect.StretchedY()
	lo, hi := ui.OffTop, ui.OffBottom
	loName, hiName, posName, sizeName := "Top", "Bottom", "Pos Y", "Height"
	if horizontal {
		stretched = n.Rect.StretchedX()
		lo, hi = ui.OffLeft, ui.OffRight
		loName, hiName, posName, sizeName = "Left", "Right", "Pos X", "Width"
	}

	if stretched {
		if editorDragFloat(loName, &n.Rect.Offset[lo], -4000, 4000, "%.0f") {
			uiBeginEdit("rect.off" + loName)
			uiEdited()
		}
		if editorDragFloat(hiName, &n.Rect.Offset[hi], -4000, 4000, "%.0f") {
			uiBeginEdit("rect.off" + hiName)
			uiEdited()
		}
		return
	}

	var pos, size float32
	if horizontal {
		pos, size = n.Rect.PinnedX()
	} else {
		pos, size = n.Rect.PinnedY()
	}
	changed := editorDragFloat(posName, &pos, -8000, 8000, "%.0f")
	changed = editorDragFloat(sizeName, &size, 0, 8000, "%.0f") || changed
	if changed {
		uiBeginEdit("rect.pin" + posName)
		if horizontal {
			n.Rect.SetPinnedX(pos, size)
		} else {
			n.Rect.SetPinnedY(pos, size)
		}
		uiEdited()
	}
}

func uiStyleProperties(n *ui.Node) {
	// Named for what it does to *this* kind. On an image it multiplies the
	// texture; on everything else it fills the rect behind the widget, and
	// calling that "Color" on a label — which already has a Text color — is how
	// someone ends up unable to find the one that changes the box.
	label := "Background##uicolor"
	if n.Kind == ui.KindImage {
		label = "Tint##uicolor"
	}
	col := [4]float32{n.Style.Color[0], n.Style.Color[1], n.Style.Color[2], n.Style.Color[3]}
	if imgui.ColorEdit4V(label, &col, imgui.ColorEditFlagsAlphaBar) {
		uiBeginEdit("style.color")
		n.Style.Color = mgl32.Vec4{col[0], col[1], col[2], col[3]}
		uiEdited()
	}
	switch n.Kind {
	case ui.KindText:
		editorTextMuted("Fills the rect behind the text. Alpha 0 for none.")
	case ui.KindImage:
		editorTextMuted("Multiplies the texture. White leaves it alone.")
	case ui.KindBar, ui.KindSlider:
		editorTextMuted("The empty part of the bar. The filled part is under Value.")
	case ui.KindVideo:
		editorTextMuted("Shows before the first frame, and behind the letterbox bars.")
	}
	if textureCombo("Texture##uitex", &n.Style.Texture, true) {
		uiBeginEdit("style.texture")
		uiEdited()
	}
	if n.Style.Texture != "" {
		uv := mgl32.Vec2{n.Style.UV[0], n.Style.UV[1]}
		size := mgl32.Vec2{n.Style.UV[2], n.Style.UV[3]}
		if editorDragFloat("UV X", &uv[0], 0, 1, "%.3f") ||
			editorDragFloat("UV Y", &uv[1], 0, 1, "%.3f") ||
			editorDragFloat("UV W", &size[0], 0, 1, "%.3f") ||
			editorDragFloat("UV H", &size[1], 0, 1, "%.3f") {
			uiBeginEdit("style.uv")
			n.Style.UV = mgl32.Vec4{uv[0], uv[1], size[0], size[1]}
			uiEdited()
		}

		editorTextMuted("Nine-slice insets, in texels of the source image")
		for i, name := range []string{"Slice left", "Slice top", "Slice right", "Slice bottom"} {
			if editorDragFloat(name, &n.Style.Slice[i], 0, 512, "%.0f") {
				uiBeginEdit("style.slice")
				uiEdited()
			}
		}

	}

	// Outside the texture block, because a video has a picture without having a
	// texture name: the fit mode is the difference between a letterboxed film
	// and a stretched one, and hiding it behind a field a video never fills
	// left it unreachable on the one kind that most needs it.
	if n.Style.Texture != "" || n.Kind == ui.KindVideo {
		fill := int32(n.Style.Fill)
		if imguibackend.Combo("Fit##uifill", &fill, ui.FillModeNames) {
			uiBeginEdit("style.fill")
			n.Style.Fill = ui.FillMode(fill)
			uiEdited()
		}
		if n.Kind == ui.KindVideo {
			editorTextMuted("Fit letterboxes, fill crops, stretch distorts. Tile does nothing here.")
		}
	}
}

func uiStateProperties(n *ui.Node) {
	editorTextMutedWrapped("Unset states are derived by tinting, so a button with none authored still responds.")
	uiStateRow("Hover", &n.Style.Hover)
	uiStateRow("Press", &n.Style.Press)
	uiStateRow("Disabled", &n.Style.Disabled)
}

func uiStateRow(name string, slot **ui.StateStyle) {
	on := *slot != nil
	if imgui.Checkbox(name+"##uistate", &on) {
		uiBeginEdit("state." + name)
		if on {
			c := mgl32.Vec4{1, 1, 1, 1}
			*slot = &ui.StateStyle{Color: &c}
		} else {
			*slot = nil
		}
		uiEdited()
	}
	st := *slot
	if st == nil {
		return
	}
	imgui.Indent()
	if st.Color != nil {
		col := [4]float32{(*st.Color)[0], (*st.Color)[1], (*st.Color)[2], (*st.Color)[3]}
		if imgui.ColorEdit4V("Color##uistatecol"+name, &col, imgui.ColorEditFlagsAlphaBar) {
			uiBeginEdit("state.color." + name)
			*st.Color = mgl32.Vec4{col[0], col[1], col[2], col[3]}
			uiEdited()
		}
	}
	if textureCombo("Texture##uistatetex"+name, &st.Texture, true) {
		uiBeginEdit("state.texture." + name)
		uiEdited()
	}
	imgui.Unindent()
}

func uiTextProperties(n *ui.Node) {
	bound := n.TextIsBound()
	imguibackend.EditorPushDisabled(bound)
	v := n.Text.Value
	if imgui.InputText("Text##uitext", &v) {
		uiBeginEdit("text.value")
		n.Text.Value = v
		uiEdited()
	}
	imguibackend.EditorPopDisabled()
	if bound {
		editorTextMutedWrapped("A binding owns this string. Clear the binding below to type one.")
	}

	if uiFontCombo("Font##uifont", &n.Text.Font) {
		uiBeginEdit("text.font")
		uiEdited()
	}
	if editorDragFloat("Size", &n.Text.Size, 0, 200, "%.0f") {
		uiBeginEdit("text.size")
		uiEdited()
	}
	editorTextMuted("0 takes the theme's size.")

	col := [4]float32{n.Text.Color[0], n.Text.Color[1], n.Text.Color[2], n.Text.Color[3]}
	if imgui.ColorEdit4V("Text color##uitextcol", &col, imgui.ColorEditFlagsAlphaBar) {
		uiBeginEdit("text.color")
		n.Text.Color = mgl32.Vec4{col[0], col[1], col[2], col[3]}
		uiEdited()
	}

	h := int32(n.Text.HAlign)
	if imguibackend.Combo("Align##uihalign", &h, ui.HAlignNames) {
		uiBeginEdit("text.halign")
		n.Text.HAlign = ui.HAlign(h)
		uiEdited()
	}
	vv := int32(n.Text.VAlign)
	if imguibackend.Combo("Vertical##uivalign", &vv, ui.VAlignNames) {
		uiBeginEdit("text.valign")
		n.Text.VAlign = ui.VAlign(vv)
		uiEdited()
	}
	if imgui.Checkbox("Wrap##uiwrap", &n.Text.Wrap) {
		uiBeginEdit("text.wrap")
		uiEdited()
	}

	imgui.Separator()
	bind := n.Text.Bind
	if imgui.InputText("Bind##uitextbind", &bind) {
		uiBeginEdit("text.bind")
		n.Text.Bind = bind
		uiEdited()
	}
	format := n.Text.Format
	if imgui.InputText("Format##uitextformat", &format) {
		uiBeginEdit("text.format")
		n.Text.Format = format
		uiEdited()
	}
	if bound {
		// The live readout is what turns a binding from a guess into something
		// an author can see working.
		editorTextMuted("Showing: " + n.Text.Shown())
	} else {
		editorTextMutedWrapped(`An expression like @player.attr.health. Empty means the text above is used.`)
	}
}

func uiValueProperties(n *ui.Node) {
	bound := n.ValueIsBound()
	imguibackend.EditorPushDisabled(bound)
	if editorDragFloat("Value", &n.Value.Value, n.Value.Min, n.Value.Max, "%.2f") {
		uiBeginEdit("value.value")
		uiEdited()
	}
	imguibackend.EditorPopDisabled()

	if editorDragFloat("Min", &n.Value.Min, -10000, 10000, "%.2f") {
		uiBeginEdit("value.min")
		uiEdited()
	}
	if editorDragFloat("Max", &n.Value.Max, -10000, 10000, "%.2f") {
		uiBeginEdit("value.max")
		uiEdited()
	}
	if n.Kind == ui.KindSlider {
		if editorDragFloat("Step", &n.Value.Step, 0, 100, "%.2f") {
			uiBeginEdit("value.step")
			uiEdited()
		}
		editorTextMuted("0 is continuous.")
	}
	if n.Kind == ui.KindToggle {
		if imgui.Checkbox("On##uion", &n.Value.On) {
			uiBeginEdit("value.on")
			uiEdited()
		}
	}

	dir := int32(n.Value.Dir)
	if imguibackend.Combo("Fills##uidir", &dir, ui.FillDirNames) {
		uiBeginEdit("value.dir")
		n.Value.Dir = ui.FillDir(dir)
		uiEdited()
	}

	col := [4]float32{n.Value.FillColor[0], n.Value.FillColor[1], n.Value.FillColor[2], n.Value.FillColor[3]}
	if imgui.ColorEdit4V("Fill color##uifillcol", &col, imgui.ColorEditFlagsAlphaBar) {
		uiBeginEdit("value.fillcolor")
		n.Value.FillColor = mgl32.Vec4{col[0], col[1], col[2], col[3]}
		uiEdited()
	}
	if textureCombo("Fill texture##uifilltex", &n.Value.FillTexture, true) {
		uiBeginEdit("value.filltex")
		uiEdited()
	}

	imgui.Separator()
	bind := n.Value.Bind
	if imgui.InputText("Bind##uivaluebind", &bind) {
		uiBeginEdit("value.bind")
		n.Value.Bind = bind
		uiEdited()
	}
	bindMax := n.Value.BindMax
	if imgui.InputText("Bind max##uivaluebindmax", &bindMax) {
		uiBeginEdit("value.bindmax")
		n.Value.BindMax = bindMax
		uiEdited()
	}
	if bound {
		editorTextMuted("Showing: " + fmtF(n.Value.Current()) + " / " + fmtF(n.Value.Ceiling()))
	}
}

// uiVideoProperties is the clip a video widget plays, and a transport for
// watching it here rather than having to launch the game to find out whether it
// is the right take.
//
// The transport buttons write through Node's setters rather than to the fields
// directly, so the preview stack's driver picks them up on its next tick — the
// same path a script's `playing: true` takes. Nothing in the editor talks to a
// player, and that is what stops the preview and the game from being two
// different implementations of "play".
func uiVideoProperties(n *ui.Node) {
	if videoFileCombo("Clip##uividsrc", &n.Video.Source) {
		uiBeginEdit("video.source")
		n.SeekVideo(0)
		uiEdited()
	}
	if n.Video.Source == "" {
		editorTextMutedWrapped("Nothing chosen. Drop an .avi (motion-JPEG) or an animated .gif into " +
			video.VideoDirName + "/ beside sounds/.")
		return
	}

	if imgui.Checkbox("Autoplay##uividauto", &n.Video.AutoPlay) {
		uiBeginEdit("video.autoplay")
		uiEdited()
	}
	editorTextMuted("Off, the widget sits on the first frame until a script says otherwise.")
	if imgui.Checkbox("Loop##uividloop", &n.Video.Loop) {
		uiBeginEdit("video.loop")
		uiEdited()
	}
	if imgui.Checkbox("Muted##uividmute", &n.Video.Muted) {
		uiBeginEdit("video.muted")
		uiEdited()
	}
	if editorDragFloat("Volume", &n.Video.Volume, 0, 1, "%.2f") {
		uiBeginEdit("video.volume")
		uiEdited()
	}
	editorTextMuted("Multiplied by the music slider, which is the bus a soundtrack plays on.")

	imgui.Separator()

	// The transport is not an edit: starting a clip to look at it is not a
	// change to the document, and pushing an undo snapshot for it would fill
	// the history with entries that restore nothing.
	label := "Play##uividplay"
	if n.VideoPlaying() {
		label = "Pause##uividplay"
	}
	if imgui.Button(label) {
		n.SetVideoPlaying(!n.VideoPlaying())
	}
	imgui.SameLine()
	if imgui.Button("Restart##uividrestart") {
		n.SeekVideo(0)
		n.SetVideoPlaying(true)
	}
	imgui.SameLine()
	if imgui.Button("Rewind##uividrewind") {
		n.SeekVideo(0)
		n.SetVideoPlaying(false)
	}

	pos := n.VideoTime()
	dur := n.VideoDuration()
	if dur <= 0 {
		editorTextMuted("Not opened yet, or the file will not read — see the log.")
		return
	}
	if editorDragFloat("Time", &pos, 0, dur, "%.2f") {
		n.SeekVideo(pos)
	}
	editorTextMuted(fmtF(pos) + " / " + fmtF(dur) + " s")
}

func uiNavProperties(n *ui.Node) {
	editorTextMutedWrapped("Leave these empty for the geometric search, which is what a player expects most of the time.")
	uiNodeIDCombo("Up##uinavup", &n.Nav.Up, true)
	uiNodeIDCombo("Down##uinavdown", &n.Nav.Down, true)
	uiNodeIDCombo("Left##uinavleft", &n.Nav.Left, true)
	uiNodeIDCombo("Right##uinavright", &n.Nav.Right, true)

	action := n.Action
	if imgui.InputText("Action##uiaction", &action) {
		uiBeginEdit("node.action")
		n.Action = action
		uiEdited()
	}
	editorTextMuted("The atom posted on activation. Empty uses the kind's default.")
}

func uiIncludeProperties(n *ui.Node) {
	if uiScreenCombo("Source##uiincsrc", &n.Include.Source) {
		uiBeginEdit("include.source")
		uiEdited()
		uiStatus = "Reopen the document to re-splice the include"
	}
	if n.Include.Source != "" {
		imgui.SameLine()
		if imgui.Button("Edit##uiincedit") {
			OpenUIEditorOn(n.Include.Source)
		}
	}
	editorTextMutedWrapped("Params are substituted into {{name}} in the spliced subtree's text, textures and actions.")

	for k, v := range n.Include.Params {
		val := v
		if imgui.InputText(k+"##uiincparam", &val) {
			uiBeginEdit("include.param." + k)
			n.Include.Params[k] = val
			uiEdited()
		}
	}
	if imgui.Button("Add param##uiincaddparam") {
		uiBeginEdit("include.addparam")
		if n.Include.Params == nil {
			n.Include.Params = map[string]string{}
		}
		n.Include.Params[uiUniqueParamName(n)] = ""
		uiEdited()
	}
}

func uiUniqueParamName(n *ui.Node) string {
	base := "param"
	if _, taken := n.Include.Params[base]; !taken {
		return base
	}
	for i := 2; ; i++ {
		name := base + itoaSmall(i)
		if _, taken := n.Include.Params[name]; !taken {
			return name
		}
	}
}

// uiNodeIDCombo picks another node in this document by ID.
func uiNodeIDCombo(label string, target *string, allowEmpty bool) bool {
	shown := *target
	if shown == "" {
		shown = "(auto)"
	}
	changed := false
	if imguibackend.BeginCombo(label, shown) {
		if allowEmpty && imguibackend.SelectableV("(auto)", *target == "", 0, imgui.Vec2{}) {
			*target, changed = "", true
		}
		if uiDoc != nil {
			uiDoc.Root.Walk(func(parent, n *ui.Node) bool {
				if parent == nil {
					return true
				}
				if imguibackend.SelectableV(n.ID, *target == n.ID, 0, imgui.Vec2{}) {
					*target, changed = n.ID, true
				}
				return true
			})
		}
		imguibackend.EndCombo()
	}
	if changed {
		uiBeginEdit("nodeid." + label)
		uiEdited()
	}
	return changed
}

// uiScreenCombo picks another .fui by name.
func uiScreenCombo(label string, target *string) bool {
	shown := *target
	if shown == "" {
		shown = "(none)"
	}
	changed := false
	if imguibackend.BeginCombo(label, shown) {
		for _, f := range ui.ScanScreenFiles() {
			n := f[:len(f)-len(ui.UIExt)]
			if n == uiScreenName() {
				// A document cannot include itself; offering it would only
				// produce a cycle for the loader to refuse.
				continue
			}
			if imguibackend.SelectableV(n, *target == n, 0, imgui.Vec2{}) {
				*target, changed = n, true
			}
		}
		imguibackend.EndCombo()
	}
	return changed
}

func fmtF(v float32) string { return strconv.FormatFloat(float64(v), 'g', 4, 32) }

func itoaSmall(v int) string { return strconv.Itoa(v) }
