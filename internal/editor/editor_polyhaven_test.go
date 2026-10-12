package editor

// editor_polyhaven_test.go — the Poly Haven browser against the fake Poly
// Haven (polyhaven/phtest; never the live API), with no GL: the window draws
// (catalog loading, the grid, a selection's details and file listing, an
// import's progress and log), thumbnails are fetched for visible cells and
// uploaded through the stand-in, Download & Import writes the asset into
// the project, and Add to stage appends to a Generator stage's asset list as
// one undo step — with the id-list widget (thumbnails, Browse…) drawn in the
// Generator's param panel.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/polyhaven"
	"foundry/internal/polyhaven/phtest"
	"foundry/internal/procgen"
	"foundry/internal/procgen/stages"
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

func withPolyHavenBrowser(t *testing.T) *phtest.Server {
	t.Helper()
	withGeneratorWindow(t) // the stand-in uploads, a fake Generator window, a temp project
	srv := phtest.New(t)
	savedC, savedJobs, savedUI, savedWin := phClientOnce.c, phJobs.started, phUI, polyHavenWindow
	phClientOnce.c = srv.Client(t)
	phJobs.started = false
	polyHavenWindow = &uiwin.UIWindow{W: phWindowW, H: phWindowH}
	t.Cleanup(func() {
		phClientOnce.c, phJobs.started, phUI, polyHavenWindow = savedC, savedJobs, savedUI, savedWin
	})
	return srv
}

func phFrame() {
	io := imgui.CurrentIO()
	io.SetDisplaySize(imgui.Vec2{X: phWindowW, Y: phWindowH})
	io.SetDeltaTime(1.0 / 60.0)
	imgui.NewFrame()
	drawPolyHavenWindow()
	imgui.Render()
}

// phUntil draws frames until cond holds (or fails after a few seconds).
func phUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		phFrame()
		time.Sleep(5 * time.Millisecond)
	}
	phFrame()
}

func TestPolyHavenBrowserImportsAndAddsToAStage(t *testing.T) {
	withPolyHavenBrowser(t)
	withImgui(func() { phBrowserImportsAndAdds(t) })
}

func phBrowserImportsAndAdds(t *testing.T) {
	phUntil(t, "the catalog", func() bool { as, _, _ := phCatalog(polyhaven.TypeModels); return len(as) == 2 })
	// The grid asked for the visible thumbnails and they were uploaded.
	phUntil(t, "thumbnails", func() bool { return phThumb("t1") != 0 && phThumb("p1") != 0 })

	// A selection: its details and file listing, the plant default from
	// its categories.
	phUI.selected = "p1"
	phUntil(t, "the file listing", func() bool { f, _ := phFiles("p1"); return f != nil })
	if !phUI.opt.Plant {
		t.Fatal("a plants asset did not default to Plant")
	}
	phUI.selected = "t1"
	phFrame()
	if phUI.opt.Plant {
		t.Fatal("a rocks asset defaulted to Plant")
	}

	// Download & Import.
	cat, _, _ := phCatalog(polyhaven.TypeModels)
	a := phFind(cat, "t1")
	if !phStartImport(a, phImportOptions{Resolution: "1k", Back: "auto"}) {
		t.Fatal("the import did not start")
	}
	if phStartImport(a, phImportOptions{}) {
		t.Fatal("a second import started while one ran")
	}
	phUntil(t, "the import", func() bool { return phImportSnapshot().done })
	if j := phImportSnapshot(); j.err != nil || len(j.log) == 0 {
		t.Fatalf("import: %v (%d log lines)", j.err, len(j.log))
	}
	for _, f := range []string{"models/polyhaven/t1/t1.fnm", "models/polyhaven/t1/polyhaven.yaml", "textures/ph_t1_rock.png"} {
		if _, err := os.Stat(filepath.Join(fileutil.ResourcesPath, f)); err != nil {
			t.Fatal(err)
		}
	}
	if !phImported(a) {
		t.Fatal("the asset does not read as imported")
	}

	// Add to stage: the selected Poly Haven stage's asset list, one undo step.
	genDoc = &procgen.Doc{Name: "ph", Seed: 1, Stages: []procgen.Stage{{Kind: stages.PolyHavenRocksKind, ID: "scans",
		Params: procgen.Params{"assets": procgen.List(procgen.Params{"id": procgen.String("t1")})}}}}
	genSelected = 0
	if err := phAddToStage(a); err == nil {
		t.Fatal("added an asset already in the list")
	}
	s2 := &polyhaven.Asset{ID: "s2", Type: 2}
	if phTargetLabel(s2) == "" {
		t.Fatal("no target with a Poly Haven stage selected")
	}
	if err := phAddToStage(s2); err != nil {
		t.Fatal(err)
	}
	if items := genDoc.Stages[0].Params["assets"].Items; len(items) != 2 || items[1]["id"].Str != "s2" {
		t.Fatalf("assets %+v", items)
	}
	GeneratorUndo()
	if items := genDoc.Stages[0].Params["assets"].Items; len(items) != 1 {
		t.Fatalf("undo left %d assets", len(items))
	}
	// A texture is not a model list's.
	if phTargetLabel(&polyhaven.Asset{ID: "sand", Type: 1}) != "" {
		t.Fatal("a texture targets a models list")
	}
	// Browse… aims Add to stage at that list.
	OpenPolyHavenBrowserFor(polyhaven.TypeModels, "scans", genParamPath{{Name: "assets", Item: -1}})
	genSelected = -1
	if phTargetLabel(s2) == "" {
		t.Fatal("Browse's target was not kept")
	}

	// The Generator draws the asset list with its thumbnails and Browse….
	genSelected = 0
	genFrame(imgui.Vec2{}, false, false)
}

func TestPolyHavenBrowserDrawsTexturesAndAFailedCatalog(t *testing.T) {
	withPolyHavenBrowser(t)
	withImgui(func() { phBrowserTextures(t) })
}

func phBrowserTextures(t *testing.T) {
	phUI.typ = polyhaven.TypeTextures
	phUntil(t, "the texture catalog", func() bool { as, _, _ := phCatalog(polyhaven.TypeTextures); return len(as) == 1 })
	phUI.selected, phUI.search = "sand", "red"
	phUntil(t, "the texture listing", func() bool { f, _ := phFiles("sand"); return f != nil })
	if n := len(phVisible(func() []*polyhaven.Asset { as, _, _ := phCatalog(polyhaven.TypeTextures); return as }())); n != 1 {
		t.Fatalf("search shows %d", n)
	}
	// An unknown type's catalog fails and the window says so.
	phUI.typ = "hdris"
	phUntil(t, "the failure", func() bool { _, _, err := phCatalog("hdris"); return err != nil })
}
