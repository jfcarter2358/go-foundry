package editor

// editor_particle_panel.go — the particle editor's toolbar, layer list,
// right-hand parameter panel, transport bar and dialogs.
//
// The panel is grouped the way an effect is actually built: what comes out
// (Emission), where from (Shape), what it does then (Motion, Rotation), what it
// looks like (Appearance), and what it hits (Collision). An author works down
// it once and then back to whichever group is wrong.
//
// Every mutating widget pushes undo. That is a real cost — a drag pushes one
// snapshot per frame it moves — so the drags push on the frame they *start*
// instead, tracked by particleDragActive.

import (
	"fmt"
	"foundry/internal/gfx"
	"foundry/internal/imguibackend"
	"foundry/internal/material"
	"foundry/internal/particle"
	"foundry/internal/render"
	"path/filepath"
	"strconv"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// Dialog visibility.
var (
	particleShowOpenDialog   bool
	particleShowSaveAsDialog bool
	particleFileList         []string
	particleSaveAsName       string
	particleRenameBuf        string
	particleRenameFor        = -1
)

// particleDragActive is the label of the drag currently in progress, so undo is
// pushed once when it starts rather than once per frame while it moves.
var particleDragActive string

// particleBeginEdit pushes undo for an edit identified by label, at most once
// per continuous drag.
func particleBeginEdit(label string) {
	if particleDragActive == label {
		return
	}
	particleDragActive = label
	ParticlePushUndo()
}

// particleEndEdits clears the drag latch. Called once a frame, after the panel,
// when nothing is being dragged any more.
func particleEndEdits() {
	if !imgui.IsAnyItemActive() {
		particleDragActive = ""
	}
}

// particleTouch marks the document changed so the running preview picks the
// change up this frame.
func particleTouch() {
	if particleDoc != nil {
		particleDoc.Invalidate()
	}
}

// ---------- toolbar ----------

func particleEditorToolbar() {
	w := particleEditorWindow
	if w == nil {
		return
	}
	editorTopBarBegin("##particletoolbar", float32(w.W))

	if imgui.BeginMenuBar() {
		particleEditorMenus()
		editorMenuBarShared(editorKindParticles)
		name := particleEffectName()
		if name == "" {
			name = "(unsaved)"
		}
		editorMenuBarDoc(name, particleEditorDirty())
		imgui.EndMenuBar()
	}

	// Second row: view toggles and the status line. The toggles are in the
	// View menu too; here they are one click.
	imgui.Checkbox("Grid##pgrid", &particleShowGrid)
	imgui.SameLine()
	imgui.Checkbox("Ground##pground", &particleShowGround)
	imgui.SameLine()
	imgui.Checkbox("Shape [B]##pshape", &particleShowShape)
	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	imgui.SetNextItemWidth(110)
	imgui.ColorEdit3V("BG##pbg", &particleBgColor, imgui.ColorEditFlagsNoInputs)
	if w != nil {
		w.ClearColor = [4]float32{particleBgColor[0], particleBgColor[1], particleBgColor[2], 1}
	}
	editorStatusText(particleStatus)

	imgui.End()
}

// particleEditorMenus draws the particle editor's own menus.
func particleEditorMenus() {
	if imgui.BeginMenu("File") {
		if imgui.MenuItemV("New", "Ctrl+N", false, true) {
			particleNewDoc()
		}
		if imgui.MenuItemV("Open...", "Ctrl+O", false, true) {
			particleShowOpenDialog = true
			particleFileList = nil
		}
		imgui.Separator()
		if imgui.MenuItemV("Save", "Ctrl+S", false, true) {
			particleSaveCurrent()
		}
		if imgui.MenuItem("Save As...") {
			particleSaveAsName = particleEffectName()
			particleShowSaveAsDialog = true
		}
		editorSaveAllMenuItem()
		editorCloseWindowItem(CloseParticleEditorWindow)
		imgui.EndMenu()
	}

	if imgui.BeginMenu("Edit") {
		editorUndoRedoItems(ParticleCanUndo(), ParticleCanRedo(), ParticleUndo, ParticleRedo)
		imgui.EndMenu()
	}

	if imgui.BeginMenu("View") {
		editorMenuToggle("Grid", "", &particleShowGrid)
		editorMenuToggle("Ground", "", &particleShowGround)
		editorMenuToggle("Emitter Shape", "B", &particleShowShape)
		imgui.EndMenu()
	}
}

// ---------- layer list ----------

func particleLayerListWindow() {
	w := particleEditorWindow
	if w == nil || particleDoc == nil {
		return
	}
	h := float32(w.H) - particleToolbarH - particleTransportHeight()
	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: particleToolbarH})
	imgui.SetNextWindowSize(imgui.Vec2{X: particleLayerListWidth, Y: h})
	imgui.BeginV("Layers##players", nil, imgui.WindowFlagsNoMove|imgui.WindowFlagsNoResize)

	editorTextMutedWrapped("S solos, M mutes. Order is draw order.")
	imgui.Separator()

	del, dupIdx, move := -1, -1, 0
	moveIdx := -1
	for i := range particleDoc.Layers {
		l := &particleDoc.Layers[i]
		imgui.PushID(strconv.Itoa(i))

		soloed := particleSolo == i
		if editorActiveToggleButton("S", soloed) {
			// Soloing is a view state, not the document's: it is how you look
			// at one layer of three, not something an effect ships with.
			if soloed {
				particleSolo = -1
			} else {
				particleSolo = i
			}
		}
		imgui.SameLine()
		if editorActiveToggleButton("M", l.Muted) {
			ParticlePushUndo()
			l.Muted = !l.Muted
			particleTouch()
		}
		imgui.SameLine()

		label := l.Name
		if label == "" {
			label = "(unnamed)"
		}
		if imguibackend.SelectableV(label, i == particleActiveLayer, 0, imgui.Vec2{}) {
			particleActiveLayer = i
			particleRenameFor = -1
		}
		if imgui.BeginPopupContextItem() {
			if imgui.MenuItem("Duplicate") {
				dupIdx = i
			}
			if imgui.MenuItem("Move up") {
				moveIdx, move = i, -1
			}
			if imgui.MenuItem("Move down") {
				moveIdx, move = i, 1
			}
			imgui.Separator()
			if imgui.MenuItem("Delete") {
				del = i
			}
			imgui.EndPopup()
		}
		imgui.PopID()
	}

	imgui.Separator()
	if imgui.Button("+##paddlayer") {
		ParticlePushUndo()
		particleActiveLayer = particleDoc.AddLayer("layer")
		particleRebuildSystems()
	}
	imgui.SameLine()
	imguibackend.EditorPushDisabled(len(particleDoc.Layers) <= 1)
	if imgui.Button("-##pdellayer") {
		del = particleActiveLayer
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	if imgui.Button("^##pup") {
		moveIdx, move = particleActiveLayer, -1
	}
	imgui.SameLine()
	if imgui.Button("v##pdown") {
		moveIdx, move = particleActiveLayer, 1
	}

	// Mutations after the loop, never inside it: every one of them reallocates
	// or reorders the slice being ranged over.
	if dupIdx >= 0 {
		ParticlePushUndo()
		particleActiveLayer = particleDoc.DuplicateLayer(dupIdx)
		particleRebuildSystems()
	}
	if moveIdx >= 0 && move != 0 {
		ParticlePushUndo()
		to := particleDoc.MoveLayer(moveIdx, move)
		if moveIdx == particleActiveLayer {
			particleActiveLayer = to
		}
		particleRebuildSystems()
	}
	if del >= 0 && len(particleDoc.Layers) > 1 {
		ParticlePushUndo()
		particleDoc.DeleteLayer(del)
		if particleActiveLayer >= len(particleDoc.Layers) {
			particleActiveLayer = len(particleDoc.Layers) - 1
		}
		if particleSolo == del {
			particleSolo = -1
		} else if particleSolo > del {
			particleSolo--
		}
		particleRebuildSystems()
	}

	imgui.End()
}

// ---------- the parameter panel ----------

func particleEditorPanel() {
	w := particleEditorWindow
	if w == nil || particleDoc == nil {
		return
	}
	h := float32(w.H) - particleToolbarH - particleTransportHeight()
	imgui.SetNextWindowPos(imgui.Vec2{X: float32(w.W) - particlePanelWidth, Y: particleToolbarH})
	imgui.SetNextWindowSize(imgui.Vec2{X: particlePanelWidth, Y: h})
	imgui.BeginV("Effect##ppanel", nil, imgui.WindowFlagsNoMove|imgui.WindowFlagsNoResize)

	particleDocSection()

	l := particleActive()
	if l == nil {
		editorTextMutedWrapped("No layer selected.")
		imgui.End()
		return
	}
	imgui.Separator()
	particleLayerNameRow(l)
	particleEmissionSection(l)
	particleShapeSection(l)
	particleInitialSection(l)
	particleMotionSection(l)
	particleAppearanceSection(l)
	particleCollisionSection(l)

	imgui.End()
	particleEndEdits()
}

// particleDocSection draws what belongs to the whole effect rather than to one
// layer.
func particleDocSection() {
	if !imgui.CollapsingHeaderV("Effect##pdoc", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	id := particleDoc.ID
	if imgui.InputTextV("ID##pdocid", &id, imgui.InputTextFlagsEnterReturnsTrue, nil) && id != particleDoc.ID {
		ParticlePushUndo()
		particleDoc.ID = id
		particleTouch()
	}
	g := particleDoc.Gravity
	if editorDragFloat("Gravity##pdocg", &g, 0, 100, "%.2f") {
		particleBeginEdit("pdocg")
		particleDoc.Gravity = g
		particleTouch()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("World units per second squared, before each layer's\nown multiplier. Carried on the effect so it does not\nchange meaning between levels.")
	}
	seed := int32(particleDoc.Seed)
	if imgui.DragIntV("Seed##pdocseed", &seed, 1, 0, 1<<30, "%d", 0) {
		particleBeginEdit("pdocseed")
		particleDoc.Seed = uint32(seed)
		particleTouch()
		particleRestart()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Fixes every random draw, so the preview is reproducible.\nChanging it restarts the effect.")
	}
	particleFlashRows()
}

// particleFlashRows draws the effect's own light (particle.Flash): the flash a
// muzzle flash or a grenade throws across the level while it goes off, as
// opposed to the particles, which are what it throws it FROM.
//
// Brightness is the switch — 0 is no light — so it comes first, and the rest
// of the rows are greyed out while it is off. The preview does not show it:
// there is no scene there to light.
func particleFlashRows() {
	f := &particleDoc.Light
	if f.Radius == 0 && f.Duration == 0 && f.Color == (mgl32.Vec3{}) {
		// An effect that has never had a light: give the numbers something
		// usable to start from, so turning the brightness up lights rather
		// than doing nothing at all.
		def := particle.DefaultFlash()
		f.Radius, f.Duration, f.Color = def.Radius, def.Duration, def.Color
	}
	particleFloat("Light##pdoclb", &f.Brightness, 0, 60, "%.2f",
		"How hard the effect lights the level at the peak of its\nflash. 0 is no light at all, which is the default.\nThe preview does not show it: there is no scene to light.")
	if !f.On() {
		imguibackend.EditorPushDisabled(true)
	}
	col := [3]float32{f.Color.X(), f.Color.Y(), f.Color.Z()}
	if imgui.ColorEdit3("Light colour##pdoclc", &col) {
		particleBeginEdit("pdoclc")
		f.Color = mgl32.Vec3{col[0], col[1], col[2]}
		particleTouch()
	}
	particleFloat("Light reach##pdoclr", &f.Radius, 0, 200, "%.1f",
		"How far the flash reaches, in world units.")
	particleFloat("Light time##pdocld", &f.Duration, 0.01, 5, "%.3f",
		"How long the flash lasts, in seconds - its own, not the\nlayers'. It goes off with the effect's cycle, so a\nlooping effect flashes once a lap.")
	if !f.On() {
		imguibackend.EditorPopDisabled()
	}
}

// particleLayerNameRow draws the rename field, committed on Enter for the
// reason an object ID's is: a name is what a script and the list address.
func particleLayerNameRow(l *particle.Layer) {
	if particleRenameFor != particleActiveLayer {
		particleRenameBuf = l.Name
		particleRenameFor = particleActiveLayer
	}
	if imgui.InputTextV("Layer##pname", &particleRenameBuf,
		imgui.InputTextFlagsEnterReturnsTrue|imgui.InputTextFlagsAutoSelectAll, nil) {
		if particleRenameBuf != "" && particleDoc.LayerIndexByName(particleRenameBuf) < 0 {
			ParticlePushUndo()
			l.Name = particleRenameBuf
			particleTouch()
		} else {
			particleStatus = "A layer called " + strconv.Quote(particleRenameBuf) + " already exists"
			particleRenameBuf = l.Name
		}
	}
}

func particleEmissionSection(l *particle.Layer) {
	if !imgui.CollapsingHeaderV("Emission##pemit", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	particleFloat("Rate /s##prate", &l.Rate, 0, 5000, "%.1f",
		"Particles per second, released continuously.\n0 makes this a burst-only layer.")
	particleFloat("Duration##pdur", &l.Duration, 0.01, 600, "%.2f",
		"One cycle's length. Bursts are timed within it.")
	particleBool("Loop##ploop", &l.Loop, "Restart the cycle rather than stopping at the end.")
	particleBool("Prewarm##pprewarm", &l.Prewarm,
		"Run a whole cycle before the first visible frame, so the\neffect is already in full flow rather than starting up.")

	max := int32(l.MaxParticles)
	if imgui.DragIntV("Max##pmax", &max, 1, 1, int32(particle.MaxPoolSize), "%d", 0) {
		particleBeginEdit("pmax")
		l.MaxParticles = int(max)
		particleTouch()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The pool ceiling. Reached, emission stalls rather than\ngrowing - which is what makes a particle budget mean\nsomething.")
	}

	particleBurstList(l)
}

// particleBurstList draws the timed one-off releases.
func particleBurstList(l *particle.Layer) {
	imgui.Separator()
	editorTextMuted("Bursts")
	del := -1
	for i := range l.Bursts {
		imgui.PushID("burst" + strconv.Itoa(i))
		imgui.SetNextItemWidth(70)
		t := l.Bursts[i].Time
		if imgui.DragFloatV("##bt", &t, 0.01, 0, 600, "t %.2f", 0) {
			particleBeginEdit("bt" + strconv.Itoa(i))
			l.Bursts[i].Time = t
			particleTouch()
		}
		imgui.SameLine()
		imgui.SetNextItemWidth(70)
		n := int32(l.Bursts[i].Count)
		if imgui.DragIntV("##bn", &n, 1, 1, 10000, "n %d", 0) {
			particleBeginEdit("bn" + strconv.Itoa(i))
			l.Bursts[i].Count = int(n)
			particleTouch()
		}
		imgui.SameLine()
		if imgui.Button("x") {
			del = i
		}
		imgui.PopID()
	}
	if del >= 0 {
		ParticlePushUndo()
		l.Bursts = append(l.Bursts[:del], l.Bursts[del+1:]...)
		particleTouch()
	}
	if imgui.Button("+ burst##paddburst") {
		ParticlePushUndo()
		l.Bursts = append(l.Bursts, particle.Burst{Time: 0, Count: 20})
		particleTouch()
	}
}

func particleShapeSection(l *particle.Layer) {
	if !imgui.CollapsingHeaderV("Shape##pshapesec", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	if imguibackend.BeginCombo("Kind##pshapekind", particle.ShapeKindName(l.Shape)) {
		for i, n := range particle.ShapeKindNames {
			k := particle.ShapeKind(i)
			if imguibackend.SelectableV(n, k == l.Shape, 0, imgui.Vec2{}) && k != l.Shape {
				ParticlePushUndo()
				l.Shape = k
				particleTouch()
			}
		}
		imguibackend.EndCombo()
	}

	switch l.Shape {
	case particle.ShapeBox:
		half := [3]float32{l.BoxHalf.X(), l.BoxHalf.Y(), l.BoxHalf.Z()}
		if editorDragFloat3("Half extents##pboxhalf", &half, 0, 100, "%.2f") {
			particleBeginEdit("pboxhalf")
			l.BoxHalf = mgl32.Vec3{half[0], half[1], half[2]}
			particleTouch()
		}
	case particle.ShapePoint:
		editorTextMutedWrapped("Every particle is born at the emitter's origin.")
	default:
		particleFloat("Radius##pradius", &l.Radius, 0, 100, "%.3f", "")
	}
	if l.Shape == particle.ShapeCone {
		particleFloat("Angle (deg)##pangle", &l.Angle, 0, 90, "%.1f",
			"The cone's half-angle. 0 is a straight jet.")
	}
	if l.Shape != particle.ShapePoint {
		particleFloat("Thickness##pthick", &l.Thickness, 0, 1, "%.2f",
			"0 fills the volume evenly; 1 is a hollow shell.")
	}

	if imguibackend.BeginCombo("Direction##pdirmode", particle.DirModeName(l.Dir)) {
		for i, n := range particle.DirModeNames {
			d := particle.DirMode(i)
			if imguibackend.SelectableV(n, d == l.Dir, 0, imgui.Vec2{}) && d != l.Dir {
				ParticlePushUndo()
				l.Dir = d
				particleTouch()
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip(particleDirHelp[i])
			}
		}
		imguibackend.EndCombo()
	}
	if l.Dir == particle.DirFixed {
		dir := [3]float32{l.Direction.X(), l.Direction.Y(), l.Direction.Z()}
		if editorDragFloat3("Vector##pdirvec", &dir, -1, 1, "%.2f") {
			particleBeginEdit("pdirvec")
			l.Direction = mgl32.Vec3{dir[0], dir[1], dir[2]}
			particleTouch()
		}
	}
	particleFloat("Spread (deg)##pspread", &l.Spread, 0, 180, "%.1f",
		"Random cone around whatever direction was chosen.")
}

var particleDirHelp = []string{
	"Outward along the shape's own normal - away from a sphere's\ncentre, up a cone, off a disc's face.",
	"Ignore the shape; pick a direction on the whole sphere.",
	"Send every particle the same way.",
}

func particleInitialSection(l *particle.Layer) {
	if !imgui.CollapsingHeaderV("Initial##pinit", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	particleRange("Life##plife", &l.LifeMin, &l.LifeMax, 0, 600, "%.2f",
		"Seconds. Each particle draws its own between the two.")
	particleRange("Speed##pspeed", &l.SpeedMin, &l.SpeedMax, 0, 200, "%.2f", "")
	particleRange("Size##psize", &l.SizeMin, &l.SizeMax, 0, 100, "%.3f",
		"World units. The size ramp scales this over the life.")
	particleRangeDeg("Rotation##prot", &l.RotMin, &l.RotMax,
		"Degrees of roll at birth.")
	particleRangeDeg("Rot speed##protvel", &l.RotVelMin, &l.RotVelMax,
		"Degrees per second.")
}

func particleMotionSection(l *particle.Layer) {
	if !imgui.CollapsingHeaderV("Motion##pmotion", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	particleFloat("Gravity##pgrav", &l.Gravity, -10, 10, "%.2f",
		"Multiplies the effect's gravity. 0 floats, 1 falls,\nnegative rises - which is what smoke wants.")
	particleFloat("Drag##pdrag", &l.Drag, 0, 20, "%.2f",
		"Fraction of velocity shed per second. 0 coasts forever.")
	particleFloat("Radial##pradial", &l.Radial, -50, 50, "%.2f",
		"Acceleration away from the emitter's origin.")
	particleFloat("Tangential##ptan", &l.Tangential, -50, 50, "%.2f",
		"Swirl about the emitter's up axis.")
	particleFloat("Turbulence##pturb", &l.TurbulenceAmp, 0, 50, "%.2f",
		"Strength of a smooth pseudo-random field. What stops\nevery particle moving in a straight line.")
	particleFloat("Turb freq##pturbf", &l.TurbulenceFreq, 0, 20, "%.2f",
		"How tightly the field varies in space.")
	particleFloat("Wind##pwind", &l.Wind, 0, 10, "%.2f",
		"How readily the level's wind carries the particles: the rate\ntheir sideways speed is pulled toward the wind's. 0 ignores it;\na waterfall's mist drifts downwind at about 1.")
	particleBool("Local space##plocal", &l.LocalSpace,
		"Carry live particles when the emitter moves. Off, they are\nreleased into the world and left behind - the difference\nbetween a torch on a ship and the smoke trailing it.")
}

func particleAppearanceSection(l *particle.Layer) {
	if !imgui.CollapsingHeaderV("Appearance##papp", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	if imguibackend.BeginCombo("Render##prender", particle.RenderKindName(l.Render)) {
		for i, n := range particle.RenderKindNames {
			k := particle.RenderKind(i)
			if imguibackend.SelectableV(n, k == l.Render, 0, imgui.Vec2{}) && k != l.Render {
				ParticlePushUndo()
				l.Render = k
				particleTouch()
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip(particleRenderHelp[i])
			}
		}
		imguibackend.EndCombo()
	}
	if l.Render == particle.RenderVolume {
		particleVolumeLookSection(l)
		return
	}

	if materialCombo("Material##pmat", &l.Material, material.DomainParticle) {
		ParticlePushUndo()
	}
	if textureCombo("Texture##ptex", &l.Texture, false) {
		ParticlePushUndo()
		particleTouch()
	}

	cols, rows := int32(l.SheetCols), int32(l.SheetRows)
	if imgui.DragIntV("Sheet cols##pcols", &cols, 1, 0, 32, "%d", 0) {
		particleBeginEdit("pcols")
		l.SheetCols = int(cols)
		particleTouch()
	}
	if imgui.DragIntV("Sheet rows##prows", &rows, 1, 0, 32, "%d", 0) {
		particleBeginEdit("prows")
		l.SheetRows = int(rows)
		particleTouch()
	}
	if l.SheetCols > 0 && l.SheetRows > 0 {
		if imguibackend.BeginCombo("Frames##pframes", particle.FrameModeName(l.FrameMode)) {
			for i, n := range particle.FrameModeNames {
				f := particle.FrameMode(i)
				if imguibackend.SelectableV(n, f == l.FrameMode, 0, imgui.Vec2{}) && f != l.FrameMode {
					ParticlePushUndo()
					l.FrameMode = f
					particleTouch()
				}
				if imgui.IsItemHovered() {
					imgui.SetTooltip(particleFrameHelp[i])
				}
			}
			imguibackend.EndCombo()
		}
	} else {
		editorTextMutedWrapped("Zero on either axis uses the whole texture.")
	}

	if imguibackend.BeginCombo("Blend##pblend", gfx.SpriteBlendName(l.Blend)) {
		for i, n := range gfx.SpriteBlendNames {
			b := gfx.SpriteBlend(i)
			if imguibackend.SelectableV(n, b == l.Blend, 0, imgui.Vec2{}) && b != l.Blend {
				ParticlePushUndo()
				l.Blend = b
				particleTouch()
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip(spriteBlendHelp[i])
			}
		}
		imguibackend.EndCombo()
	}
	if imguibackend.BeginCombo("Billboard##pbb", gfx.BillboardModeName(l.Billboard)) {
		for i, n := range gfx.BillboardModeNames {
			m := gfx.BillboardMode(i)
			if imguibackend.SelectableV(n, m == l.Billboard, 0, imgui.Vec2{}) && m != l.Billboard {
				ParticlePushUndo()
				l.Billboard = m
				particleTouch()
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip(spriteBillboardHelp[i])
			}
		}
		imguibackend.EndCombo()
	}

	tint := [3]float32{l.Tint.X(), l.Tint.Y(), l.Tint.Z()}
	if imgui.ColorEdit3("Tint##ptint", &tint) {
		particleBeginEdit("ptint")
		l.Tint = mgl32.Vec3{tint[0], tint[1], tint[2]}
		particleTouch()
	}
	particleBool("Lit##plit", &l.Lit,
		"Take the scene's lights and shadows. Off for anything\nemissive. The preview is always unlit - it has no scene.")
	particleBool("Cast shadows##pcast", &l.CastShadows,
		"Draw into the shadow maps, facing the light. Expensive\nfor a large pool; convincing only for solid particles.")
}

var particleRenderHelp = []string{
	"Each particle is a textured quad, drawn by the sprite pass.",
	"Each particle is a soft ball of volumetric smoke or fire, ray-marched\nand lit by the sun and sky. Size is its diameter, the Alpha ramp its\ndensity and the Color ramp the light it gives off. What a fireball is.",
}

// particleVolumeLookSection draws a volume layer's look in place of the quad
// fields it has no use for.
func particleVolumeLookSection(l *particle.Layer) {
	v := &l.Volume
	editorTextMutedWrapped("Size is each ball's diameter, the Alpha ramp its density, the Color ramp " +
		"(times Tint) the light it gives off. The Emission and Erosion strips below shape both over life.")

	particleFloat("Density##pvdens", &v.Density, 0, 50, "%.2f",
		"Thickness per world unit at a ball's core, before the Alpha ramp.")
	particleFloat("Noise scale##pvnoise", &v.NoiseScale, 0.05, 10, "%.2f",
		"How many repeats of the noise span one ball's radius.\nLow is a few big lumps; high is a cauliflower.")
	particleFloat("Displacement##pvdisp", &v.Displacement, 0, 1, "%.2f",
		"How far the noise pushes the surface in and out, as a fraction\nof the radius. 0 is a smooth sphere.")
	particleFloat("Billow##pvbillow", &v.Billow, -5, 5, "%.2f",
		"How fast the noise rolls outward over the particle's life -\nthe churn of a fireball growing.")
	particleFloat("Erosion##pverode", &v.Erosion, 0, 1, "%.2f",
		"How hard fine noise eats the edges, times the Erosion ramp.\nRising erosion is fire breaking up into smoke.")

	albedo := [3]float32{v.Albedo.X(), v.Albedo.Y(), v.Albedo.Z()}
	if imgui.ColorEdit3("Smoke colour##pvalbedo", &albedo) {
		particleBeginEdit("pvalbedo")
		v.Albedo = mgl32.Vec3{albedo[0], albedo[1], albedo[2]}
		particleTouch()
	}
	particleFloat("Absorption##pvabs", &v.Absorption, 0, 8, "%.2f",
		"How much sunlight the smoke's own mass takes out. Higher\nshades a plume's underside darker.")
	particleFloat("Emission##pvemit", &v.Emission, 0, 20, "%.2f",
		"How brightly the Color ramp glows, times the Emission ramp.\n0 is plain smoke.")

	tint := [3]float32{l.Tint.X(), l.Tint.Y(), l.Tint.Z()}
	if imgui.ColorEdit3("Tint##pvtint", &tint) {
		particleBeginEdit("pvtint")
		l.Tint = mgl32.Vec3{tint[0], tint[1], tint[2]}
		particleTouch()
	}

	if limit := render.MaxVolumeBlobs * render.MaxVolumeBlobDraws; l.EffectiveMax() > limit {
		editorTextMutedWrapped(fmt.Sprintf("At most %d balls of one layer are drawn at once; past that the nearest are skipped.", limit))
	}
}

var particleFrameHelp = []string{
	"Walk the sheet from the first cell to the last across the\nparticle's life - a flipbook.",
	"Pick one cell at birth and keep it. What stops a cloud of\nsmoke looking like one puff repeated.",
	"Always the first cell.",
}

func particleCollisionSection(l *particle.Layer) {
	if !imgui.CollapsingHeaderV("Collision##pcoll", 0) {
		return
	}
	if imguibackend.BeginCombo("Mode##pcollmode", particle.CollideModeName(l.Collision)) {
		for i, n := range particle.CollideModeNames {
			c := particle.CollideMode(i)
			if imguibackend.SelectableV(n, c == l.Collision, 0, imgui.Vec2{}) && c != l.Collision {
				ParticlePushUndo()
				l.Collision = c
				particleTouch()
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip(particleCollideHelp[i])
			}
		}
		imguibackend.EndCombo()
	}
	if l.Collision == particle.CollideNone {
		editorTextMutedWrapped("Off costs nothing. Most effects are smoke and fire, which have no business bouncing.")
		return
	}
	particleFloat("Radius##pcollr", &l.CollideRadius, 0, 10, "%.3f",
		"How far from a surface a particle is stopped.")
	if l.Collision == particle.CollideBounce {
		particleFloat("Bounce##pbounce", &l.Bounce, 0, 1, "%.2f",
			"How much of the speed into the surface comes back out.")
		particleFloat("Friction##pfric", &l.Friction, 0, 1, "%.2f",
			"How much of the speed along the surface is lost.")
	}
	editorTextMutedWrapped("In the preview the floor is the ground plane at y=0.")
}

var particleCollideHelp = []string{
	"Never test. Costs nothing.",
	"Park the particle where it hit and let it live out its life\nthere - scorch, splatter, settled dust.",
	"Reflect it, scaled by bounce and friction.",
	"End the particle on contact.",
}

// ---------- small typed widget wrappers ----------
//
// Each pushes undo once per drag and marks the document changed, so no call
// site has to remember either.

func particleFloat(label string, v *float32, lo, hi float32, format, tip string) {
	if editorDragFloat(label, v, lo, hi, format) {
		particleBeginEdit(label)
		particleTouch()
	}
	if tip != "" && imgui.IsItemHovered() {
		imgui.SetTooltip(tip)
	}
}

func particleBool(label string, v *bool, tip string) {
	if imgui.Checkbox(label, v) {
		ParticlePushUndo()
		particleTouch()
	}
	if tip != "" && imgui.IsItemHovered() {
		imgui.SetTooltip(tip)
	}
}

// particleRange draws a min/max pair on one row, which is how every one of
// these is actually read.
func particleRange(label string, lo, hi *float32, min, max float32, format, tip string) {
	imgui.PushID(label)
	imgui.SetNextItemWidth(70)
	if imgui.DragFloatV("##lo", lo, 0.01, min, max, format, 0) {
		particleBeginEdit(label + "lo")
		particleTouch()
	}
	imgui.SameLine()
	imgui.SetNextItemWidth(70)
	if imgui.DragFloatV("##hi", hi, 0.01, min, max, format, 0) {
		particleBeginEdit(label + "hi")
		particleTouch()
	}
	imgui.SameLine()
	imgui.Text(labelText(label))
	if tip != "" && imgui.IsItemHovered() {
		imgui.SetTooltip(tip)
	}
	imgui.PopID()
}

// particleRangeDeg is the same in degrees over a radian-valued pair, since
// nobody authors a rotation in radians.
func particleRangeDeg(label string, lo, hi *float32, tip string) {
	dlo, dhi := mgl32.RadToDeg(*lo), mgl32.RadToDeg(*hi)
	before := [2]float32{dlo, dhi}
	particleRange(label, &dlo, &dhi, -3600, 3600, "%.0f", tip)
	if dlo != before[0] || dhi != before[1] {
		*lo, *hi = mgl32.DegToRad(dlo), mgl32.DegToRad(dhi)
	}
}

// labelText strips the ## id suffix so a label can carry one and still read.
func labelText(label string) string {
	for i := 0; i+1 < len(label); i++ {
		if label[i] == '#' && label[i+1] == '#' {
			return label[:i]
		}
	}
	return label
}

// ---------- file actions ----------

func particleNewDoc() {
	particleDoc = particle.NewDoc("effect")
	particleActiveLayer = 0
	particleSolo = -1
	ParticleClearUndoHistory()
	particleRebuildSystems()
	particleMarkSaved()
	particleStatus = "New effect"
}

func particleSaveCurrent() {
	if particleDoc == nil {
		return
	}
	if particleDoc.Path == "" {
		particleSaveAsName = particleDoc.ID
		particleShowSaveAsDialog = true
		return
	}
	if err := particleSave(particleDoc.Path); err != nil {
		particleStatus = err.Error()
	}
}

func particleDialogs() {
	imguibackend.WithModalPopup("Open Effect##popendlg", &particleShowOpenDialog, func() {
		if particleFileList == nil {
			particleFileList = particle.ScanParticleFiles()
		}
		if len(particleFileList) == 0 {
			editorTextMutedWrapped("Nothing in particles/ yet. Save one first.")
		}
		for _, name := range particleFileList {
			if !imguibackend.Selectable(name) {
				continue
			}
			if err := particleLoad(filepath.Join(particle.ParticlesDir(), name)); err != nil {
				particleStatus = err.Error()
			}
			imgui.CloseCurrentPopup()
		}
		imgui.Separator()
		if imgui.Button("Cancel##popencancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Save Effect As##psavedlg", &particleShowSaveAsDialog, func() {
		imgui.InputTextV("Name##psavename", &particleSaveAsName, 0, nil)
		editorTextMutedWrapped("Written to particles/<name>" + particle.ParticleExt)
		if imgui.Button("Save##psavego") && particleSaveAsName != "" {
			if err := particleSave(particle.ParticlePath(particleSaveAsName)); err != nil {
				particleStatus = err.Error()
			}
			// The picker's cache is stale the moment a new file exists.
			Editor.ParticleFileNames = nil
			particleFileList = nil
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##psavecancel") {
			imgui.CloseCurrentPopup()
		}
	})
}
