package editor

// editor_generator_paint_live_test.go — live painting without a window: the
// 3D brush's strokes (aimed at a stubbed ground), its keys and feel (Shift
// erases, [ ] resize, Ctrl+wheel strength, spacing leaves no gaps), the
// live splice's in-place diff, the layer menu's rename and delete, and the
// whole path from a stroke's undo through the debounce, the incremental run
// and the splice into the open level.

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"foundry/internal/levelio"
	"foundry/internal/procgen"
	"foundry/internal/procgen/paint"
	"foundry/internal/testenv"
	"foundry/internal/uiwin"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// zlivetiles is a test stage that places one scenery entry and one volume
// per painted tile of its layer: what a paint-reading stage's output looks
// like to the live splice, without any assets.
func registerLiveTestStage(t *testing.T) {
	procgen.RegisterStage(procgen.StageKind{Name: "zlivetiles", Category: "Test", Version: 1,
		Params: []procgen.ParamDef{{Name: "layer", Type: procgen.ParamMask, Default: procgen.Mask(""), Doc: "d"}},
		Run: func(rc *procgen.RunContext) error {
			l := rc.PaintLayer(rc.Params.Str("layer"))
			for _, k := range l.Keys() {
				r := k.Rect()
				x, z := float32(r[0]+32), float32(r[1]+32)
				rc.Place(procgen.Placement{ID: "t_" + k.String(), Position: procgen.Point3{X: x, Z: z}})
				rc.PlaceVolume(procgen.VolumeEntry{"id": "v_" + k.String(),
					"position": map[interface{}]interface{}{"x": float64(x), "y": 0.0, "z": float64(z)},
					"half":     map[interface{}]interface{}{"x": 32.0, "y": 4.0, "z": 32.0}})
			}
			return nil
		}})
	t.Cleanup(func() { procgen.UnregisterStage("zlivetiles") })
}

// liveTestSetup opens a generator naming level "livelvl", that level (empty)
// in the level editor in 3D, a ground plane at y = 2 for the brush's rays,
// and the paint tool on layer "lush".
func liveTestSetup(t *testing.T) *world.Level {
	t.Helper()
	withGeneratorWindow(t)
	testenv.WithoutGeometryUploads(t)
	registerLiveTestStage(t)
	savedPaint, savedLive, savedLevel, savedPath, savedMode := genPaint, genPaintLive, Editor.level, Editor.SavePath, Editor.Mode
	savedRay, savedShift := genPaintRayHit, genPaintShiftHeld
	t.Cleanup(func() {
		genPaint, genPaintLive = savedPaint, savedLive
		Editor.level, Editor.SavePath, Editor.Mode = savedLevel, savedPath, savedMode
		genPaintRayHit, genPaintShiftHeld = savedRay, savedShift
		genPaint3D.stroking, genPaint3D.hoverValid = false, false
		genPaintReset()
		uiwin.PendingWindowOpens = nil
	})
	genPaintRayHit = func(_ *world.Level, o, d mgl32.Vec3) (mgl32.Vec3, bool) {
		if d.Y() >= 0 {
			return mgl32.Vec3{}, false
		}
		tt := (2 - o.Y()) / d.Y()
		return o.Add(d.Mul(tt)), true
	}
	shift := false
	genPaintShiftHeld = func() bool { return shift }
	t.Cleanup(func() { shift = false })

	d := &procgen.Doc{Name: "livegen", Seed: 1, Level: "livelvl", Stages: []procgen.Stage{
		{Kind: "zlivetiles", ID: "tiles", Params: procgen.Params{"layer": procgen.Mask("lush")}},
	}}
	d.EnsurePaintLayer(paint.LayerDef{Name: "lush", Kind: paint.KindDensity})
	genSetDoc(d, "")
	lvl := &world.Level{}
	Editor.level, Editor.SavePath, Editor.Mode = lvl, levelio.LevelPath("livelvl"), Mode3D
	genPaint.on, genPaint.layer, genPaint.radius, genPaint.strength, genPaint.falloff = true, "lush", 4, 1, 0
	genPaint.spacing, genPaint.mode = 0.25, paint.ModeAdd
	genPaintLive = savedLive
	genPaintLive.off, genPaintLive.pending, genPaintLive.running, genPaintLive.res, genPaintLive.last = false, false, false, nil, nil
	return lvl
}

// stroke3D paints a stroke through the 3D brush's entry points from (x0,
// z0) to (x1, z1), the hover moved a metre at a time.
func stroke3D(x0, z0, x1, z1 float32) {
	genPaint3D.hover, genPaint3D.hoverValid = mgl32.Vec3{x0, 2, z0}, true
	genPaint3DMouseDown()
	n := int(math.Ceil(math.Hypot(float64(x1-x0), float64(z1-z0))))
	for i := 1; i <= n; i++ {
		f := float32(i) / float32(n)
		genPaint3D.hover = mgl32.Vec3{x0 + (x1-x0)*f, 2, z0 + (z1-z0)*f}
		genPaintDrag(float64(genPaint3D.hover.X()), float64(genPaint3D.hover.Z()))
	}
	genPaint3DEnd()
}

func TestGeneratorPaint3DBrush(t *testing.T) {
	liveTestSetup(t)
	if !genPaint3DActive() {
		t.Fatal("the 3D brush is not active with the generator's level open and the tool on")
	}
	p, ok := genPaintRayHit(Editor.level, mgl32.Vec3{10, 50, 10}, mgl32.Vec3{0, -1, 0})
	if !ok || p.Y() != 2 {
		t.Fatalf("stub ground: %v %v", p, ok)
	}
	// A fast drag (one hover sample 30 m on) still paints every metre on
	// the way: the stamps are spaced, not the samples.
	genPaint3D.hover, genPaint3D.hoverValid = mgl32.Vec3{0, 2, 10}, true
	genPaint3DMouseDown()
	genPaintDrag(30, 10)
	genPaint3DEnd()
	l := genDoc.PaintLayer("lush")
	for x := 0.0; x <= 30; x += 0.5 {
		if v := l.Sample(x, 10); v < 0.9 {
			t.Fatalf("a gap at x = %.1f (painted %.2f)", x, v)
		}
	}
	if genPaintUndoStack.len() != 1 {
		t.Fatalf("%d undo steps for one stroke", genPaintUndoStack.len())
	}
	// Shift as a stroke starts erases.
	genPaintShiftHeld = func() bool { return true }
	stroke3D(10, 10, 12, 10)
	genPaintShiftHeld = func() bool { return false }
	if v := l.Sample(11, 10); v > 0.01 {
		t.Errorf("a shift stroke left %.2f", v)
	}
	if genPaint.mode != paint.ModeAdd {
		t.Error("the shift erase stuck to the brush's mode")
	}
	// Ctrl+Z through the level editor's keys undoes the stroke, not the level.
	if !genPaint3DHandleKey(sdl.SCANCODE_Z, sdl.KMOD_LCTRL) {
		t.Fatal("Ctrl+Z went past the brush")
	}
	if v := l.Sample(11, 10); v < 0.9 {
		t.Errorf("undo did not bring the erased paint back (%.2f)", v)
	}
	genPaint3DHandleKey(sdl.SCANCODE_Z, sdl.KMOD_LCTRL|sdl.KMOD_LSHIFT)
	if v := l.Sample(11, 10); v > 0.01 {
		t.Errorf("Ctrl+Shift+Z did not redo the erase (%.2f)", v)
	}
	// [ and ] resize; Ctrl+wheel sets strength.
	r0 := genPaint.radius
	genPaint3DHandleKey(sdl.SCANCODE_RIGHTBRACKET, 0)
	if genPaint.radius <= r0 {
		t.Error("] did not grow the brush")
	}
	genPaint3DHandleKey(sdl.SCANCODE_LEFTBRACKET, 0)
	if math.Abs(float64(genPaint.radius-r0)) > 1e-4 {
		t.Errorf("[ ] round trip: %g, want %g", genPaint.radius, r0)
	}
	genPaint.strength = 0.5
	genPaintWheelStrength(1, true)
	if genPaint.strength <= 0.5 {
		t.Error("Ctrl+wheel up did not raise strength")
	}
	if genPaintWheelStrength(1, false) {
		t.Error("the plain wheel was taken for strength")
	}
	// The ring lies on the ground.
	for _, q := range genPaintRingPoints(Editor.level, mgl32.Vec3{5, 2, 5}, 3, 12) {
		if math.Abs(float64(q.Y()-2.08)) > 1e-4 {
			t.Fatalf("ring point %v is off the ground", q)
		}
	}
	// The overlay's draped mesh sits on the ground plus the lift.
	verts, ok := genPaint3DTileMesh(Editor.level, paint.TileKey{}, 100)
	if !ok || len(verts) != (genPaint3DGrid-1)*(genPaint3DGrid-1)*6*5 {
		t.Fatalf("tile mesh: %d floats, %v", len(verts), ok)
	}
	if y := verts[1]; math.Abs(float64(y-2-genPaint3DLift)) > 1e-4 {
		t.Errorf("mesh vertex at y %g", y)
	}
	// Another tool armed takes the view back.
	terrainTool.Active = true
	if genPaint3DActive() {
		t.Error("the brush stayed active with the terrain tool armed")
	}
	terrainTool.Active = false
}

func TestGeneratorPaintLiveSpliceDiffs(t *testing.T) {
	lvl := liveTestSetup(t)
	lvl.Scenery = []world.Scenery{
		{ID: "hand_made", Model: ""},
		{ID: "gen.livegen.tiles.t_0_0", Position: mgl32.Vec3{32, 0, 32}, Scale: mgl32.Vec3{1, 1, 1}, Solid: true},
		{ID: "gen.livegen.tiles.t_1_0", Position: mgl32.Vec3{1, 0, 1}, Scale: mgl32.Vec3{1, 1, 1}, Solid: true},
		{ID: "gen.livegen.tiles.t_5_5", Position: mgl32.Vec3{5, 0, 5}, Scale: mgl32.Vec3{1, 1, 1}, Solid: true},
		{ID: "gen.livegen.other.keep", Position: mgl32.Vec3{9, 0, 9}},
	}
	res := &procgen.Result{Generator: "livegen", OutDir: t.TempDir(), Stages: []*procgen.StageResult{
		{ID: "other", Skipped: true, Placements: []procgen.Placement{{ID: "keep", Position: procgen.Point3{X: 99}}}},
		{ID: "tiles", Placements: []procgen.Placement{
			{ID: "t_0_0", Position: procgen.Point3{X: 32, Z: 32}},  // unchanged
			{ID: "t_1_0", Position: procgen.Point3{X: 96, Z: 32}},  // moved
			{ID: "t_2_0", Position: procgen.Point3{X: 160, Z: 32}}, // new
		}},
	}}
	st, err := genLiveSplice(lvl, genDoc, res, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if st.Scenery != [3]int{1, 1, 1} || st.Kept != 1 {
		t.Fatalf("stats %s", st)
	}
	ids := map[string]mgl32.Vec3{}
	for _, s := range lvl.Scenery {
		ids[s.ID] = s.Position
	}
	if _, ok := ids["hand_made"]; !ok {
		t.Error("the hand-made prop went")
	}
	if p := ids["gen.livegen.other.keep"]; p.X() != 9 {
		t.Error("a stage that did not run had its placement touched")
	}
	if p := ids["gen.livegen.tiles.t_1_0"]; p.X() != 96 {
		t.Errorf("the moved placement is at %v", p)
	}
	if _, ok := ids["gen.livegen.tiles.t_5_5"]; ok {
		t.Error("a placement the run no longer makes stayed")
	}
	if _, ok := ids["gen.livegen.tiles.t_2_0"]; !ok {
		t.Error("the new placement was not added")
	}
}

func TestGeneratorPaintLiveUndoRerunsAndSplices(t *testing.T) {
	lvl := liveTestSetup(t)
	// The generator has run once (live updates need its manifest).
	if _, err := procgen.Run(nil, genDoc.Clone(), procgen.Options{}); err != nil {
		t.Fatal(err)
	}
	if _, why := genLiveTarget(); why != "" {
		t.Fatalf("not a live target: %s", why)
	}
	waitLive := func() {
		t.Helper()
		genPaintLive.due = time.Now().Add(-time.Millisecond)
		genPaintLiveStep()
		if !genPaintLive.running {
			t.Fatal("the debounced rerun did not start")
		}
		deadline := time.Now().Add(20 * time.Second)
		for genPaintLive.running && time.Now().Before(deadline) {
			genPaintLiveStep()
			time.Sleep(5 * time.Millisecond)
		}
		genPaintLiveStep() // queue the splice
		uiwin.DrainWindowOpens()
		if genPaintLive.last == nil {
			t.Fatalf("no live update landed (status %q)", genStatus)
		}
	}
	count := func() int {
		n := 0
		for _, s := range lvl.Scenery {
			if len(s.ID) > 4 && s.ID[:4] == "gen." {
				n++
			}
		}
		return n
	}
	stroke3D(10, 10, 20, 10) // tile 0_0
	if !genPaintLive.pending {
		t.Fatal("a committed stroke did not arm the live rerun")
	}
	waitLive()
	if count() != 1 || len(lvl.Volumes) != 1 {
		t.Fatalf("after one stroke: %d placements, %d volumes; want 1 and 1", count(), len(lvl.Volumes))
	}
	genPaintLive.last = nil
	stroke3D(70, 10, 80, 10) // tile 1_0
	waitLive()
	if count() != 2 {
		t.Fatalf("after two strokes: %d placements", count())
	}
	t.Logf("live update: %s", genPaintLive.last)
	// Undo the second stroke: the rerun takes its tile's placement away.
	genPaintLive.last = nil
	genPaintUndo()
	if !genPaintLive.pending {
		t.Fatal("undo did not arm the live rerun")
	}
	waitLive()
	if count() != 1 || len(lvl.Volumes) != 1 {
		t.Fatalf("after the undo: %d placements, %d volumes", count(), len(lvl.Volumes))
	}
	if genPaintLive.last.Stats.Scenery[2] != 1 {
		t.Errorf("the undo's splice removed %d", genPaintLive.last.Stats.Scenery[2])
	}
	// The output folder holds the run's manifest; the level file was never
	// written (the open level is edited in memory).
	if _, err := os.Stat(filepath.Join(procgen.OutDirFor(genDoc, procgen.Options{}), procgen.ManifestFileName)); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(levelio.LevelPath("livelvl")); err == nil {
		t.Error("the live splice wrote the level file")
	}
}

func TestGeneratorPaintLayerRenameAndDelete(t *testing.T) {
	liveTestSetup(t)
	stroke3D(5, 5, 8, 5)
	genPaintRenameLayer("lush", "meadow")
	if genPaint.layer != "meadow" || genDoc.PaintLayer("meadow").Empty() {
		t.Fatal("rename did not carry the layer and its pixels")
	}
	if genDoc.Stages[0].Params["layer"].Str != "meadow" {
		t.Error("the stage still reads the old name")
	}
	GeneratorUndo()
	if genDoc.PaintDef("lush") == nil || genDoc.PaintLayer("lush").Empty() {
		t.Fatal("undoing the rename lost the layer")
	}
	genPaint.layer = "lush"
	genPaintDeleteLayer("lush")
	if genDoc.PaintDef("lush") != nil || genDoc.Stages[0].Params["layer"].Str != "" {
		t.Fatal("delete left the declaration or the param")
	}
	GeneratorUndo()
	if l := genDoc.PaintLayer("lush"); l == nil || l.Empty() {
		t.Fatal("undoing the delete did not bring the pixels back")
	}
}
