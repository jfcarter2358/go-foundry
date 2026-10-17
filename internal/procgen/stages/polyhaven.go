package stages

// polyhaven.go — the Poly Haven stages (docs/canyon/SPEC.md phase 4;
// docs/canyon/PHASE_4.md): downloaded CC0 scans in place of generated
// meshes, the user's standing direction for every canyon default.
//
//   - polyhaven_rocks: rock and cliff scans (namaqualand cliffs and
//     boulders, rock faces, stones) as a ROCKS MANIFEST — the rocks.yaml a
//     generated rocks stage writes (rocks.Manifest, *RocksArtifact) — so
//     scatter and cliff_wall place them unchanged (RockSources).
//   - polyhaven_plants: plant scans (quiver trees, shrubs, dead wood) in the
//     same manifest, no backs and no collision, for scatter rules
//     (ScatterSources) — vegetation as real scenery rather than biome
//     volumes.
//   - polyhaven_textures (polyhaven_textures.go): named texture-set layers
//     for the material phase.
//
// The assets are named by a list of IDs (the editor's id-list widget, with
// thumbnails and the Poly Haven browser behind Browse…), or a query —
// category, tags, text, count — that picks the same IDs every run (by ID
// order, never popularity). Each is imported by internal/polyhaven/phimport
// into resources/models/polyhaven/<id>/ (shared by every generator: a
// second stage naming the same asset at the same settings downloads and
// cooks nothing) and the stage's manifest points at those meshes
// (VariantMeta.Model). Downloads go through the client's md5-addressed cache
// (os.UserCacheDir()/foundry/polyhaven), four at a time.
//
// Imports run side by side as far as cook_memory allows (the cook's peak:
// ~1000 B a triangle mesh-only, ~8500 B with hulls).

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/nanite"
	"foundry/internal/polyhaven"
	"foundry/internal/polyhaven/phimport"
	"foundry/internal/procgen"
	"foundry/internal/procgen/rocks"

	"gopkg.in/yaml.v2"
)

// The stage kinds' names.
const (
	PolyHavenRocksKind    = "polyhaven_rocks"
	PolyHavenPlantsKind   = "polyhaven_plants"
	PolyHavenTexturesKind = "polyhaven_textures"
)

// ScatterSources are what a scatter rule may place: every rock source and
// the plant scans.
var ScatterSources = append(append([]string(nil), RockSources...), PolyHavenPlantsKind)

// polyHavenPlantPreset is a plant stage's manifest preset.
const polyHavenPlantPreset = "polyhaven_plant"

// PolyHavenClient is the client the stages use (tests point it at an
// httptest server).
var PolyHavenClient = func() *polyhaven.Client { return polyhaven.New() }

// PolyHavenResources is the resources directory imports go under.
var PolyHavenResources = func() string { return fileutil.ResourcesPath }

// CreditsFile lists a Poly Haven stage's assets and their authors.
const CreditsFile = "credits.yaml"

func init() {
	procgen.RegisterStage(procgen.StageKind{
		Name:     PolyHavenRocksKind,
		Label:    "Poly Haven rocks",
		Category: "Rocks",
		// 2: scans cook with phimport.ScanAttrRadiusCap (docs/canyon/PERF.md).
		Version: 2,
		// Per environment region of a level generation doc; the imports are
		// shared (polyhaven_region.go).
		Regional: procgen.RegionalPerRegion,
		OnDemand: true,
		Doc: "Rock and cliff scans downloaded from Poly Haven (CC0) — namaqualand cliffs and boulders, rock faces, stones — " +
			"imported (textures converted, normal maps, the placement frame with a back estimated for cliff pieces), cooked " +
			"to Nanite meshes, and described in the rocks manifest scatter and cliff_wall place from.",
		Params: polyHavenModelParams(false),
		Run:    func(rc *procgen.RunContext) error { return runPolyHavenModels(rc, false) },
		Preview: func(pc *procgen.PreviewContext) (*procgen.PreviewData, error) {
			return previewPolyHavenModels(pc, false)
		},
		Load:     loadRocks,
		Validate: validatePolyHavenModels,
	})
	procgen.RegisterStage(procgen.StageKind{
		Name:     PolyHavenPlantsKind,
		Label:    "Poly Haven plants",
		Category: "Rocks",
		// 2: scans cook with phimport.ScanAttrRadiusCap (docs/canyon/PERF.md).
		Version:  2,
		Regional: procgen.RegionalPerRegion,
		OnDemand: true,
		Doc: "Plant scans downloaded from Poly Haven (CC0) — quiver trees, shrubs, dead wood, succulents — imported with " +
			"their alpha-cutout, double-sided leaves, cooked to Nanite meshes without collision, and described in a rocks " +
			"manifest so scatter rules place them as scenery.",
		Params: polyHavenModelParams(true),
		Run:    func(rc *procgen.RunContext) error { return runPolyHavenModels(rc, true) },
		Preview: func(pc *procgen.PreviewContext) (*procgen.PreviewData, error) {
			return previewPolyHavenModels(pc, true)
		},
		Load:     loadRocks,
		Validate: validatePolyHavenModels,
	})
}

func polyHavenModelParams(plant bool) []procgen.ParamDef {
	f := procgen.Float
	i := procgen.Int
	s := procgen.String
	backDoc := "Which way the piece's flat back faces, for cliff_wall: auto estimates it from the scan (the area-weighted " +
		"horizontal normal points out of a rock face); none for a piece seen from all round; -z, +z, -x, +x or a heading in " +
		"degrees name the back's direction in the file."
	item := []procgen.ParamDef{
		{Name: "id", Type: procgen.ParamString, Default: s(""), Doc: "The Poly Haven asset ID (the end of its polyhaven.com/a/ address)."},
		{Name: "back", Type: procgen.ParamString, Default: s(""), Doc: "This asset's back, over the stage's (empty: the stage's)."},
		{Name: "max_triangles", Type: procgen.ParamInt, Label: "Max triangles", Default: i(0), Min: 0, Max: 100000000,
			Doc: "This asset's triangle budget, over the stage's (0: the stage's)."},
	}
	if plant {
		item = []procgen.ParamDef{item[0], item[2]}
	}
	assetsDoc := "The assets, by ID, in order. Browse… opens the Poly Haven browser."
	defIDs := procgen.List()
	if plant {
		defIDs = procgen.List(procgen.Params{"id": s("quiver_tree_01")}, procgen.Params{"id": s("shrub_02")})
	} else {
		defIDs = procgen.List(procgen.Params{"id": s("namaqualand_cliff_01")}, procgen.Params{"id": s("namaqualand_boulder_02")},
			procgen.Params{"id": s("rock_face_01")})
	}
	out := []procgen.ParamDef{
		{Name: "assets", Type: procgen.ParamList, Fields: item, Default: defIDs, AssetList: polyhaven.TypeModels, Doc: assetsDoc},
		{Name: "category", Type: procgen.ParamString, Default: s(""),
			Doc: "A query instead of (or as well as) the list: a Poly Haven category (rocks, plants, trees...). The query takes matching assets in ID order, so a rerun picks the same ones."},
		{Name: "tags", Type: procgen.ParamString, Default: s(""), Doc: "The query's tags, all required (space or comma separated): e.g. `namaqualand desert`."},
		{Name: "search", Type: procgen.ParamString, Default: s(""), Doc: "The query's words, matched against IDs, names, tags and categories."},
		{Name: "count", Type: procgen.ParamInt, Default: i(0), Min: 0, Max: 1000, Doc: "The most assets the query adds (0: every match)."},
		{Name: "resolution", Type: procgen.ParamEnum, Default: procgen.Enum("2k"), Choices: []string{"1k", "2k", "4k", "8k"},
			Doc: "The texture resolution downloaded (the largest offered below it when it is not). 2k is plenty for scattered pieces."},
		{Name: "max_size", Type: procgen.ParamInt, Label: "Max texture size", Default: i(0), Min: 0, Max: 16384,
			Doc: "Scale the textures down further to at most this many pixels a side (0: as downloaded)."},
		{Name: "max_triangles", Type: procgen.ParamInt, Label: "Max triangles", Default: i(0), Min: 0, Max: 100000000,
			Doc: "Decimate each scan to this budget before the cook (0: as scanned). Nanite draws dense meshes cheaply, but every placement costs the cull its leaves, and the disk holds every one."},
		{Name: "lite_triangles", Type: procgen.ParamInt, Label: "Lite triangles", Default: i(0), Min: 0, Max: 10000000,
			Doc: "Also make a light copy of each scan at this budget (named <id>_lite) — what a scatter rule's Max triangles picks for pieces placed by the thousand."},
	}
	if plant {
		// Added: off keeps every existing plants stage's hash and meshes.
		out = append(out, procgen.ParamDef{Name: "thin_foliage", Added: true, Type: procgen.ParamBool, Label: "Thin foliage",
			Default: procgen.Bool(false),
			Doc: "Decimate leaves and blades by keeping whole cards (fewer, enlarged) instead of collapsing their edges: a " +
				"needle tree or a grass clump of many tiny pieces keeps its foliage at a tenth of its triangles instead of " +
				"going bare (phimport/decimate_cards.go). Its files are named _c<triangles>."})
	}
	if !plant {
		out = append(out,
			procgen.ParamDef{Name: "back", Type: procgen.ParamString, Default: s("auto"), Doc: backDoc},
			procgen.ParamDef{Name: "collision", Type: procgen.ParamEnum, Default: procgen.Enum("auto"), Choices: []string{"auto", "hulls", "mesh", "none"},
				Doc: "What the cook builds and placements ask for: auto gives hulls to pieces up to Hull size and the mesh alone above it."},
			procgen.ParamDef{Name: "hull_size", Type: procgen.ParamFloat, Label: "Hull size (m)", Default: f(4), Min: 0, Max: 100, Step: 0.1,
				Doc: "Auto collision's largest extent for hulls."},
			procgen.ParamDef{Name: "match_ground", Added: true, Type: procgen.ParamFloat, Label: "Match ground colour", Default: f(0), Min: 0, Max: 1, Step: 0.01,
				Doc: "How far the stages placing these rocks carry their colour toward the ground's, unless a rule says otherwise " +
					"(scatter's and cliff_wall's match_ground): 0 keeps the scans' own colour — Poly Haven's Karoo granite is grey-brown " +
					"with orange lichen — and 1 gives them the ground's hue and saturation over their own brightness and detail, which " +
					"reads as the red rock of sandstone country. A colour grade, not paint: every texel moves by the same ratio."},
		)
	}
	out = append(out,
		procgen.ParamDef{Name: "cook", Type: procgen.ParamBool, Default: procgen.Bool(true), Doc: "Cook Nanite meshes (off writes .glb files)."},
		procgen.ParamDef{Name: "cook_memory", Type: procgen.ParamInt, Label: "Cook memory (MB)", Default: i(2000), Min: 100, Max: 64000,
			Doc: "The memory the imports may use at once: they run side by side as far as this allows."},
		procgen.ParamDef{Name: "offline", Type: procgen.ParamBool, Default: procgen.Bool(false),
			Doc: "Use only what is already downloaded (the cache in the user's cache directory): no requests."},
		procgen.ParamDef{Name: "gallery", Type: procgen.ParamBool, Default: procgen.Bool(false),
			Doc: "Place every variant in a row along +X from Gallery origin, to look at in the target level."},
		procgen.ParamDef{Name: "gallery_origin", Type: procgen.ParamVec3, Label: "Gallery origin", Default: procgen.Vec3(0, 0, 0),
			Doc: "Where the gallery row starts."},
		procgen.ParamDef{Name: "preview_asset", Type: procgen.ParamInt, Label: "Preview asset", Default: i(0), Min: 0, Max: 1000, PreviewOnly: true,
			Doc: "Which asset the 3D preview turns (an imported one's cooked mesh, at its fallback LOD)."},
	)
	return out
}

func validatePolyHavenModels(_ *procgen.Doc, _ *procgen.Stage, p procgen.Params) []string {
	var out []string
	n := 0
	for k, it := range p.Items("assets") {
		id := strings.TrimSpace(it.Str("id"))
		if id == "" {
			out = append(out, fmt.Sprintf("asset %d names no ID", k+1))
			continue
		}
		if err := polyhaven.ValidID(id); err != nil {
			out = append(out, fmt.Sprintf("asset %d: %v", k+1, err))
		}
		n++
	}
	if n == 0 && p.Str("category") == "" && p.Str("tags") == "" && p.Str("search") == "" {
		out = append(out, "Name some assets, or a category, tags or search words to pick them by")
	}
	if b := p.Str("back"); b != "" {
		if _, err := backValid(b); err != nil {
			out = append(out, err.Error())
		}
	}
	for k, it := range p.Items("assets") {
		if b := strings.TrimSpace(it.Str("back")); b != "" {
			if _, err := backValid(b); err != nil {
				out = append(out, fmt.Sprintf("asset %d: %v", k+1, err))
			}
		}
	}
	return out
}

// backValid checks a back param.
func backValid(b string) (bool, error) {
	for _, c := range phimport.BackChoices {
		if strings.EqualFold(strings.TrimSpace(b), c) {
			return true, nil
		}
	}
	var d float64
	if _, err := fmt.Sscanf(strings.TrimSuffix(strings.TrimSpace(b), "°"), "%g", &d); err != nil {
		return false, fmt.Errorf("back %q: want auto, none, -z, +z, -x, +x or degrees", b)
	}
	return true, nil
}

// splitTags reads a tags param.
func splitTags(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' || r == ';' })
}

// polyHavenSelection is the stage's asset IDs, and each listed item's
// overrides by ID.
func polyHavenSelection(ctx context.Context, c *polyhaven.Client, typ string, p procgen.Params) ([]string, map[string]procgen.Params, error) {
	var ids []string
	items := map[string]procgen.Params{}
	for _, it := range p.Items("assets") {
		id := strings.TrimSpace(it.Str("id"))
		if id == "" {
			continue
		}
		ids = append(ids, id)
		items[id] = it
	}
	q := polyhaven.Query{Category: strings.TrimSpace(p.Str("category")), Tags: splitTags(p.Str("tags")), Text: p.Str("search")}
	sel, err := polyhaven.Select(ctx, c, typ, ids, q, p.Int("count"))
	return sel, items, err
}

// stageClient is the client for a run: offline when the stage says so.
func stageClient(p procgen.Params) *polyhaven.Client {
	c := PolyHavenClient()
	if p.Bool("offline") {
		c = c.Clone()
		c.Offline = true
	}
	return c
}

// credit is one asset in credits.yaml.
type credit struct {
	ID      string   `yaml:"id"`
	Name    string   `yaml:"name"`
	Authors []string `yaml:"authors"`
	Page    string   `yaml:"page"`
	License string   `yaml:"license"`
}

type credits struct {
	Credit string   `yaml:"credit"`
	Assets []credit `yaml:"assets"`
}

func creditOf(a *polyhaven.Asset) credit {
	return credit{ID: a.ID, Name: a.Name, Authors: a.AuthorList(), Page: "https://polyhaven.com/a/" + a.ID, License: polyhaven.License}
}

func runPolyHavenModels(rc *procgen.RunContext, plant bool) error {
	p := rc.Params
	c := stageClient(p)
	t0 := time.Now()
	ids, items, err := polyHavenSelection(rc.Ctx, c, polyhaven.TypeModels, p)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return fmt.Errorf("no assets: name some, or a query that matches")
	}
	res := PolyHavenResources()
	// How many side by side: by the largest scan's polycount (the API's,
	// before decimation).
	maxTris := 0
	for _, id := range ids {
		if a, err := c.Info(rc.Ctx, id); err == nil {
			maxTris = max(maxTris, a.Polycount)
		} else {
			return err
		}
	}
	per := float64(max(maxTris, 10000)) * float64(cookMeshOnlyBytesPerTriangle+rockGenBytesPerTriangle)
	if !plant && p.Str("collision") != "mesh" && p.Str("collision") != "none" {
		per = float64(max(maxTris, 10000)) * float64(cookBytesPerTriangle) * 0.5 // hulls only for the small ones
	}
	conc := min(max(int(float64(p.Int("cook_memory"))*(1<<20)/per), 1), len(ids), 4)
	rc.Logf("%d assets (%s), %d at a time", len(ids), strings.Join(ids, ", "), conc)

	imports := make([]*phimport.ModelImport, len(ids))
	sem := make(chan struct{}, conc)
	var mu sync.Mutex
	done := 0
	err = rc.Parallel(len(ids), func(k int) error {
		sem <- struct{}{}
		defer func() { <-sem }()
		if err := rc.Err(); err != nil {
			return err
		}
		id := ids[k]
		opt := phimport.ModelOptions{Resources: res, Resolution: p.Str("resolution"), MaxSize: p.Int("max_size"),
			MaxTriangles: p.Int("max_triangles"), LiteTriangles: p.Int("lite_triangles"), Plant: plant,
			Cook: p.Bool("cook"), Log: func(f string, a ...any) { rc.Logf(f, a...) }}
		if plant {
			opt.ThinFoliage = p.Bool("thin_foliage")
		}
		if !plant {
			opt.Back, opt.Collision, opt.HullSize = p.Str("back"), p.Str("collision"), p.Float("hull_size")
		}
		if it, ok := items[id]; ok {
			if b := strings.TrimSpace(it.Str("back")); b != "" && !plant {
				opt.Back = b
			}
			if t := it.Int("max_triangles"); t > 0 {
				opt.MaxTriangles = t
			}
		}
		var imp *phimport.ModelImport
		var err error
		if rc.Region != nil {
			var reused bool
			if imp, reused, err = importModelShared(rc.Ctx, c, id, opt); reused {
				rc.Logf("%s: the import another region's stage made this run", id)
			}
		} else {
			imp, err = phimport.ImportModel(rc.Ctx, c, id, opt)
		}
		if err != nil {
			return err
		}
		imports[k] = imp
		mu.Lock()
		done++
		rc.Progress(float32(done) / float32(len(ids)))
		mu.Unlock()
		return nil
	})
	if err != nil {
		return err
	}
	preset := "polyhaven"
	if plant {
		preset = polyHavenPlantPreset
	}
	man := &rocks.Manifest{Version: rocks.ManifestVersion, Preset: preset, Lithology: "scan", Frame: rocks.FrameDoc}
	if !plant {
		man.MatchGround = rc.Params.Float("match_ground")
	}
	cr := credits{Credit: polyhaven.Credit}
	var bytes int64
	for _, imp := range imports {
		cr.Assets = append(cr.Assets, creditOf(imp.Asset))
		bytes += imp.Downloaded
		for _, v := range imp.Variants {
			meta := v.Meta
			meta.Model = v.Model
			meta.Size = float64(max(meta.Extent[0], meta.Extent[1], meta.Extent[2]))
			man.Variants = append(man.Variants, meta)
		}
		for _, w := range imp.Warnings {
			rc.Logf("%s: %s", imp.Asset.ID, w)
		}
	}
	data, err := man.Encode()
	if err != nil {
		return err
	}
	if err := rc.WriteFile(rocks.ManifestFile, data); err != nil {
		return err
	}
	cdata, _ := yaml.Marshal(cr)
	if err := rc.WriteFile(CreditsFile, cdata); err != nil {
		return err
	}
	art := &RocksArtifact{Dir: rc.Dir, RelDir: rc.RelDir, Manifest: man}
	rc.Artifact = art
	if p.Bool("gallery") {
		if rc.Region != nil {
			rc.Logf("gallery: not placed for a region (every region would place it)")
		} else {
			placeGallery(rc, art, p.Vec3("gallery_origin"))
		}
	}
	rc.Logf("%d variants of %d assets in %s (%.1f MB downloaded)", len(man.Variants), len(ids),
		time.Since(t0).Round(time.Millisecond), float64(bytes)/(1<<20))
	rc.Progress(1)
	return nil
}

// previewPolyHavenModels lists the assets and turns an imported one's cooked
// mesh (its fallback cut) — from what is on disk only: a preview never
// downloads.
func previewPolyHavenModels(pc *procgen.PreviewContext, plant bool) (*procgen.PreviewData, error) {
	p := pc.Params
	t0 := time.Now()
	out := &procgen.PreviewData{}
	res := PolyHavenResources()
	var ids []string
	for _, it := range p.Items("assets") {
		if id := strings.TrimSpace(it.Str("id")); id != "" {
			ids = append(ids, id)
		}
	}
	if q := strings.TrimSpace(p.Str("category") + p.Str("tags") + p.Str("search")); q != "" {
		out.Notes = append(out.Notes, "the query's assets are resolved when the stage runs")
	}
	man := &rocks.Manifest{Version: rocks.ManifestVersion, Preset: "polyhaven", Frame: rocks.FrameDoc}
	for _, id := range ids {
		prov, err := phimport.ReadProvenance(filepath.Join(res, filepath.FromSlash(phimport.ModelDir(id)), phimport.ProvenanceFile))
		if err != nil || prov.Import == nil {
			out.Notes = append(out.Notes, fmt.Sprintf("%s: not imported yet (Run downloads it)", id))
			continue
		}
		ext := prov.Import.Extent
		back := ""
		if prov.Import.Back {
			back = ", back"
		}
		out.Notes = append(out.Notes, fmt.Sprintf("%s — %s: %.2f×%.2f×%.2f m, %d triangles scanned%s, by %s",
			id, prov.Name, ext[0], ext[1], ext[2], prov.Import.Triangles, back, strings.Join(prov.Authors, ", ")))
		for _, m := range prov.Import.Meshes {
			v := rocks.VariantMeta{Name: strings.TrimSuffix(m.File, filepath.Ext(m.File)), File: m.File, Model: phimport.ModelDir(id) + "/" + m.File,
				Extent: ext, Max: [3]float32{ext[0] / 2, ext[1], ext[2]}, Min: [3]float32{-ext[0] / 2, 0, 0},
				Height: ext[1], Radius: float32(math.Hypot(float64(ext[0]), float64(ext[2])) / 2),
				FootprintRadius: float32(math.Min(float64(ext[0]), float64(ext[2])) / 2), BaseBand: 0.12 * ext[1], Top: ext[1],
				Triangles: m.Triangles, Collision: m.Collision, PlaceCollision: m.Collision, Size: float64(max(ext[0], ext[1], ext[2]))}
			if !prov.Import.Back {
				v.Min[2], v.Max[2] = -ext[2]/2, ext[2]/2
			} else {
				v.Back = &[3]float32{0, 0, -1}
			}
			man.Variants = append(man.Variants, v)
		}
	}
	out.Artifact = &RocksArtifact{RelDir: "", Manifest: man}
	// The turntable: the chosen asset's first cooked mesh, its fallback cut.
	if k := p.Int("preview_asset"); len(man.Variants) > 0 {
		v := man.Variants[min(k, len(man.Variants)-1)]
		if pm, ok := naniteFallbackPreview(filepath.Join(res, filepath.FromSlash(v.Model))); ok {
			pm.Name = v.Name
			out.Meshes = append(out.Meshes, pm)
		}
	}
	out.Notes = append(out.Notes, polyhaven.Credit, fmt.Sprintf("preview %s", time.Since(t0).Round(time.Millisecond)))
	return out, nil
}

// naniteFallbackPreview is a cooked mesh's fallback cut as a preview mesh.
func naniteFallbackPreview(path string) (procgen.PreviewMesh, bool) {
	if _, err := os.Stat(path); err != nil || !strings.HasSuffix(path, ".fnm") {
		return procgen.PreviewMesh{}, false
	}
	m, err := nanite.Load(path)
	if err != nil || len(m.Fallback) == 0 || len(m.FallbackVertices) == 0 {
		return procgen.PreviewMesh{}, false
	}
	pm := procgen.PreviewMesh{Turntable: true}
	nv := len(m.FallbackVertices) / 5
	for v := 0; v < nv; v++ {
		pm.Positions = append(pm.Positions, [3]float32{m.FallbackVertices[5*v], m.FallbackVertices[5*v+1], m.FallbackVertices[5*v+2]})
		pm.UVs = append(pm.UVs, [2]float32{m.FallbackVertices[5*v+3], m.FallbackVertices[5*v+4]})
		if 3*v+2 < len(m.FallbackNormals) {
			pm.Normals = append(pm.Normals, [3]float32{m.FallbackNormals[3*v], m.FallbackNormals[3*v+1], m.FallbackNormals[3*v+2]})
		}
	}
	pm.Indices = append(pm.Indices, m.Fallback...)
	if len(pm.Normals) != len(pm.Positions) {
		pm.Normals = nil
	}
	return pm, true
}
