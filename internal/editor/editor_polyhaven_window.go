package editor

// editor_polyhaven_window.go — the "Poly Haven" asset browser
// (docs/canyon/SPEC.md phase 4; docs/guide/34-polyhaven.md): the CC0 scan
// library the canyon generators default to, searched, previewed and
// imported from inside the editor.
//
//   - Models or Textures; a search box (ID, name, tags, categories), a
//     category filter and a sort; a thumbnail grid (thumbnails fetched only
//     for cells on screen, through the client's cache).
//   - The selected asset's details: name, ID, authors, categories, tags,
//     triangles, real-world size, published date, licence, and whether this
//     project has imported it.
//   - A resolution picker (from the asset's file listing), the import
//     options (plant, back, triangle budgets, texture size cap), and
//     Download & Import with its progress, log and Cancel — the same
//     importer (internal/polyhaven/phimport) the Generator's Poly Haven
//     stages run, into models/polyhaven/<id>/ and textures/ph_<id>*.
//   - Add to stage: appends the asset's ID to a Generator stage's asset list
//     — the list whose Browse… opened the window, or else the selected
//     stage's first list of that type.
//
// The Poly Haven terms ask for a visible credit wherever their content is
// shown: the window is titled "Poly Haven" and its footer always reads
// polyhaven.Credit. Nothing here implies endorsement.

import (
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"

	"foundry/internal/imguibackend"
	"foundry/internal/polyhaven"
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

const (
	phWindowW    = 1280
	phWindowH    = 820
	phDetailsW   = float32(380)
	phFooterH    = float32(30)
	phCellPx     = float32(112)
	phCellLabelH = float32(34)
)

var polyHavenWindow *uiwin.UIWindow

// phUI is the browser's view state.
var phUI = struct {
	typ      string
	search   string
	category string
	sortBy   string
	selected string
	res      string
	opt      phImportOptions
	optFor   string // the asset opt was defaulted for
	// target is the Generator list Add to stage appends to (stage "" =
	// the selected stage's first list of the type).
	targetStage string
	targetPath  genParamPath
	status      string
}{typ: polyhaven.TypeModels, sortBy: "downloads", res: "2k"}

// PolyHavenBrowserOpen reports whether the window is up.
func PolyHavenBrowserOpen() bool { return polyHavenWindow != nil }

// OpenPolyHavenBrowser opens the browser, or raises it.
func OpenPolyHavenBrowser() {
	if polyHavenWindow != nil {
		polyHavenWindow.Win.Raise()
		return
	}
	w, err := uiwin.NewUIWindow("Poly Haven", phWindowW, phWindowH, drawPolyHavenWindow, onPolyHavenClose)
	if err != nil {
		log.Printf("open Poly Haven window: %v", err)
		return
	}
	w.ClearColor = [4]float32{0.10, 0.11, 0.13, 1}
	polyHavenWindow = w
}

// OpenPolyHavenBrowserFor opens the browser on a type with Add to stage
// aimed at one Generator list. Deferred: the caller is inside a Draw.
func OpenPolyHavenBrowserFor(typ, stageID string, path genParamPath) {
	if typ != "" {
		phUI.typ = typ
	}
	phUI.targetStage, phUI.targetPath = stageID, path
	uiwin.RequestWindowOpen(OpenPolyHavenBrowser)
}

func onPolyHavenClose() { polyHavenWindow = nil }

// phCategories is every category in the catalog, by how many assets carry it.
func phCategories(as []*polyhaven.Asset) []string {
	n := map[string]int{}
	for _, a := range as {
		for _, c := range a.Categories {
			n[c]++
		}
	}
	var out []string
	for c := range n {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if n[out[i]] != n[out[j]] {
			return n[out[i]] > n[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// phVisible is the catalog filtered and sorted for the grid.
func phVisible(as []*polyhaven.Asset) []*polyhaven.Asset {
	q := polyhaven.Query{Text: phUI.search, Category: phUI.category}
	var out []*polyhaven.Asset
	for _, a := range as {
		if q.Match(a) {
			out = append(out, a)
		}
	}
	polyhaven.Sort(out, phUI.sortBy)
	return out
}

func phFind(as []*polyhaven.Asset, id string) *polyhaven.Asset {
	for _, a := range as {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// ---------- the frame ----------

func drawPolyHavenWindow() {
	w := polyHavenWindow
	if w == nil {
		return
	}
	phStepJobs()
	phToolbar(float32(w.W))
	as, loading, err := phCatalog(phUI.typ)
	top := editorTopBarH
	h := float32(w.H) - top - phFooterH
	gridW := float32(w.W) - phDetailsW

	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: top})
	imgui.SetNextWindowSize(imgui.Vec2{X: gridW, Y: h})
	imgui.BeginV("##phgrid", nil, genPanelFlags)
	switch {
	case err != nil:
		editorTextWrapped(fmt.Sprintf("Could not read the Poly Haven catalog: %v", err))
		if imgui.Button("Retry##phretry") {
			phReloadCatalog(phUI.typ)
		}
	case loading:
		imgui.Text("Reading the Poly Haven catalog...")
	default:
		phGrid(phVisible(as), gridW)
	}
	imgui.End()

	imgui.SetNextWindowPos(imgui.Vec2{X: gridW, Y: top})
	imgui.SetNextWindowSize(imgui.Vec2{X: phDetailsW, Y: h})
	imgui.BeginV("##phdetails", nil, genPanelFlags)
	if a := phFind(as, phUI.selected); a != nil {
		phDetails(a)
	} else {
		editorTextMutedWrapped("Pick an asset to see its details and import it.")
	}
	phImportPanel()
	imgui.End()

	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: float32(w.H) - phFooterH})
	imgui.SetNextWindowSize(imgui.Vec2{X: float32(w.W), Y: phFooterH})
	imgui.BeginV("##phfooter", nil, genPanelFlags|imgui.WindowFlagsNoScrollbar)
	// The credit Poly Haven's terms ask for, always on screen.
	imgui.Text(polyhaven.Credit)
	if phUI.status != "" {
		imgui.SameLine()
		editorToolbarGroupSep()
		imgui.SameLine()
		editorTextMuted(phUI.status)
	}
	imgui.End()
}

func phToolbar(width float32) {
	editorTopBarBegin("##phtoolbar", width)
	if imgui.BeginMenuBar() {
		if imgui.BeginMenu("File") {
			if imgui.MenuItem("Reload catalog") {
				phReloadCatalog(phUI.typ)
			}
			editorCloseWindowItem(func() {
				if polyHavenWindow != nil {
					polyHavenWindow.CloseRequested = true
				}
			})
			imgui.EndMenu()
		}
		editorMenuBarShared(editorKindPolyHaven)
		imgui.EndMenuBar()
	}
	if editorActiveToggleButton("Models##phmodels", phUI.typ == polyhaven.TypeModels) {
		phUI.typ, phUI.category, phUI.selected = polyhaven.TypeModels, "", ""
	}
	imgui.SameLine()
	if editorActiveToggleButton("Textures##phtextures", phUI.typ == polyhaven.TypeTextures) {
		phUI.typ, phUI.category, phUI.selected = polyhaven.TypeTextures, "", ""
	}
	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	imgui.SetNextItemWidth(240)
	imgui.InputTextWithHint("##phsearch", "search: id, name, tags", &phUI.search)
	imgui.SameLine()
	as, _, _ := phCatalog(phUI.typ)
	cat := phUI.category
	if cat == "" {
		cat = "all categories"
	}
	imgui.SetNextItemWidth(200)
	if imguibackend.BeginCombo("##phcat", cat) {
		if imguibackend.SelectableV("all categories", phUI.category == "", 0, imgui.Vec2{}) {
			phUI.category = ""
		}
		for _, c := range phCategories(as) {
			if imguibackend.SelectableV(c, phUI.category == c, 0, imgui.Vec2{}) {
				phUI.category = c
			}
		}
		imguibackend.EndCombo()
	}
	imgui.SameLine()
	imgui.SetNextItemWidth(130)
	if imguibackend.BeginCombo("##phsort", "sort: "+phUI.sortBy) {
		for _, s := range []string{"downloads", "name", "date", "polycount"} {
			if imguibackend.SelectableV(s, phUI.sortBy == s, 0, imgui.Vec2{}) {
				phUI.sortBy = s
			}
		}
		imguibackend.EndCombo()
	}
	imgui.SameLine()
	editorTextMuted(fmt.Sprintf("%d shown", len(phVisible(as))))
	imgui.End()
}

// phGrid is the thumbnail grid; cells off screen ask for nothing.
func phGrid(as []*polyhaven.Asset, width float32) {
	cols := max(int((width-16)/(phCellPx+8)), 1)
	for i, a := range as {
		if i%cols != 0 {
			imgui.SameLine()
		}
		imgui.PushID(a.ID)
		imgui.BeginGroup()
		pos := imgui.CursorScreenPos()
		size := imgui.Vec2{X: phCellPx, Y: phCellPx}
		if phRectVisible(pos, size) {
			if tex := phThumb(a.ID); tex != 0 {
				imgui.Image(imgui.TextureID(tex), size)
			} else {
				imgui.Dummy(size)
				imgui.WindowDrawList().AddRectFilled(pos, pos.Plus(size), imgui.PackedColorFromVec4(imgui.Vec4{X: 0.2, Y: 0.2, Z: 0.22, W: 1}))
			}
		} else {
			imgui.Dummy(size)
		}
		if imgui.IsItemClicked() {
			phUI.selected = a.ID
		}
		if a.ID == phUI.selected {
			imgui.WindowDrawList().AddRect(pos, pos.Plus(size), imgui.PackedColorFromVec4(imgui.Vec4{X: 1, Y: 0.7, Z: 0.2, W: 1}))
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip(a.Name + "\n" + a.ID)
		}
		name := a.Name
		if len(name) > 16 {
			name = name[:15] + "…"
		}
		imgui.PushTextWrapPos()
		imgui.Text(name)
		imgui.PopTextWrapPos()
		imgui.EndGroup()
		imgui.PopID()
	}
	if len(as) == 0 {
		editorTextMuted("Nothing matches.")
	}
}

// phRectVisible reports whether a screen rectangle overlaps the current
// window (this imgui binding has no IsRectVisible).
func phRectVisible(pos, size imgui.Vec2) bool {
	wp, ws := imgui.WindowPos(), imgui.WindowSize()
	return pos.Y+size.Y >= wp.Y && pos.Y <= wp.Y+ws.Y && pos.X+size.X >= wp.X && pos.X <= wp.X+ws.X
}

// phDetails is the selected asset's record and its import settings.
func phDetails(a *polyhaven.Asset) {
	if phUI.optFor != a.ID {
		phUI.optFor = a.ID
		phUI.opt = phImportOptions{Back: "auto"}
		for _, c := range a.Categories {
			if c == "plants" || c == "trees" || c == "ground cover" || c == "flowers" {
				phUI.opt.Plant = true
			}
		}
	}
	if tex := phThumb(a.ID); tex != 0 {
		imgui.Image(imgui.TextureID(tex), imgui.Vec2{X: 160, Y: 160})
	}
	imgui.Text(a.Name)
	editorTextMuted(a.ID)
	imgui.Separator()
	row := func(k, v string) {
		if v == "" {
			return
		}
		editorTextMuted(k)
		imgui.SameLineV(110, 0)
		imgui.PushTextWrapPos()
		imgui.Text(v)
		imgui.PopTextWrapPos()
	}
	row("Authors", strings.Join(a.AuthorList(), ", "))
	row("Categories", strings.Join(a.Categories, ", "))
	row("Tags", strings.Join(a.Tags, ", "))
	if a.IsModel() && a.Polycount > 0 {
		row("Triangles", fmt.Sprintf("%d", a.Polycount))
	}
	if s := a.SizeMetres(); len(s) > 0 {
		parts := make([]string, len(s))
		for i, v := range s {
			parts[i] = fmt.Sprintf("%.2f", v)
		}
		row("Size", strings.Join(parts, " × ")+" m")
	}
	if a.DatePublished > 0 {
		row("Published", time.Unix(a.DatePublished, 0).UTC().Format("2006-01-02"))
	}
	row("Licence", "CC0 — free for any use, no attribution required")
	if phImported(a) {
		row("In project", "imported")
	}
	imgui.Separator()

	files, ferr := phFiles(a.ID)
	key := "gltf"
	if a.IsTexture() {
		key = "Diffuse"
	}
	rs := files.Resolutions(key)
	switch {
	case ferr != nil:
		editorTextMutedWrapped(fmt.Sprintf("file listing: %v", ferr))
	case files == nil:
		editorTextMuted("reading the file listing...")
	}
	imgui.SetNextItemWidth(120)
	if imguibackend.BeginCombo("Resolution##phres", phUI.res) {
		for _, r := range rs {
			if imguibackend.SelectableV(r, phUI.res == r, 0, imgui.Vec2{}) {
				phUI.res = r
			}
		}
		imguibackend.EndCombo()
	}
	editorMenuTooltip("The texture resolution downloaded (2k is plenty for most uses; the largest offered below this when it is not).")
	maxSize := int32(phUI.opt.MaxSize)
	imgui.SetNextItemWidth(120)
	if imgui.InputInt("Max texture px##phmaxsize", &maxSize) {
		phUI.opt.MaxSize = int(math.Max(0, float64(maxSize)))
	}
	editorMenuTooltip("Scale the textures down further to at most this many pixels a side (0: as downloaded).")
	if a.IsModel() {
		imgui.Checkbox("Plant (cutout, no collision)##phplant", &phUI.opt.Plant)
		if !phUI.opt.Plant {
			imgui.SetNextItemWidth(120)
			imgui.InputText("Back##phback", &phUI.opt.Back)
			editorMenuTooltip("auto estimates a cliff piece's flat back; none, -z, +z, -x, +x or degrees name it.")
		}
		mt, lt := int32(phUI.opt.MaxTriangles), int32(phUI.opt.LiteTriangles)
		imgui.SetNextItemWidth(120)
		if imgui.InputInt("Max triangles##phmaxtris", &mt) {
			phUI.opt.MaxTriangles = int(math.Max(0, float64(mt)))
		}
		editorMenuTooltip("Decimate before the cook (0: as scanned).")
		imgui.SetNextItemWidth(120)
		if imgui.InputInt("Lite triangles##phlite", &lt) {
			phUI.opt.LiteTriangles = int(math.Max(0, float64(lt)))
		}
		editorMenuTooltip("Also make a light copy for scattering by the thousand (0: none).")
	}
	job := phImportSnapshot()
	imguibackend.EditorPushDisabled(job.running)
	if imgui.Button("Download & Import##phimport") {
		opt := phUI.opt
		opt.Resolution = phUI.res
		if phStartImport(a, opt) {
			phUI.status = "importing " + a.ID
		}
	}
	imguibackend.EditorPopDisabled()
	editorMenuTooltip("Download (once — the files are cached and md5-checked) and import into this project:\n" +
		"models/polyhaven/<id>/ and textures/ph_<id>*.png, cooked to Nanite meshes.")
	imgui.SameLine()
	target := phTargetLabel(a)
	imguibackend.EditorPushDisabled(target == "")
	if imgui.Button("Add to stage##phadd") {
		if err := phAddToStage(a); err != nil {
			phUI.status = err.Error()
		} else {
			phUI.status = fmt.Sprintf("added %s to %s", a.ID, target)
		}
	}
	imguibackend.EditorPopDisabled()
	if target != "" {
		editorMenuTooltip("Append " + a.ID + " to " + target + ".")
	} else {
		editorMenuTooltip("Open a generator and select a Poly Haven stage (or press its Browse…) to add assets to it.")
	}
}

// phImportPanel is the running (or last) import's progress and log.
func phImportPanel() {
	job := phImportSnapshot()
	if !job.running && !job.done {
		return
	}
	imgui.Separator()
	if job.running {
		imgui.Text("Importing " + job.id)
		imgui.ProgressBarV(job.frac, imgui.Vec2{X: -1, Y: 0}, job.step)
		if imgui.Button("Cancel##phcancel") {
			phCancelImport()
		}
		imgui.SameLine()
		editorTextMuted(time.Since(job.started).Round(time.Second).String())
	} else if job.err != nil {
		editorTextWrapped("Import failed: " + job.err.Error())
	} else {
		editorTextWrapped("Imported " + job.result)
	}
	imgui.BeginChildV("##phlog", imgui.Vec2{X: 0, Y: 160}, true, 0)
	for _, l := range job.log {
		editorTextWrapped(l)
	}
	if job.running {
		imgui.SetScrollHereY(1)
	}
	imgui.EndChild()
}
