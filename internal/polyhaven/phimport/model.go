package phimport

// model.go — ImportModel: a Poly Haven model, downloaded, converted,
// normalised, cooked and recorded. The steps, in order:
//
//  1. The asset's info and file listing (the client caches both).
//  2. The glTF at the chosen resolution (Resolution, default 2k; the largest
//     offered below it otherwise) and everything it includes, plus an alpha
//     map for each cutout material and a displacement map when there is one:
//     fetched into the client's store (once ever, md5-verified, four at a
//     time).
//  3. Textures converted to engine PNGs (textures.go), four at a time; the
//     glTF rewritten with its images pointing at them, beside its .bin.
//  4. The glTF loaded as an ImportMesh (model.LoadGLTF), put in the placement
//     frame (frame.go), and measured against the API's dimensions (a scan
//     that came in at the wrong scale is reported, not silently placed).
//  5. Each requested VARIANT — the full mesh, or decimated to MaxTriangles,
//     and optionally a light copy at LiteTriangles for scattering by the
//     thousand — cooked to a .fnm with its collision: none for plants, hulls
//     for a piece up to HullSize, the mesh alone above it (the cook then skips
//     the convex decomposition).
//  6. polyhaven.yaml: who made it, where it came from, every file's md5, and
//     what the import made.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"foundry/internal/model"
	"foundry/internal/polyhaven"
	"foundry/internal/procgen/meshout"
	"foundry/internal/procgen/rocks"
	"foundry/internal/texcompress"
)

// ModelOptions are an import's settings.
type ModelOptions struct {
	// Resources is the project's resources directory (absolute).
	Resources string
	// Resolution is the texture resolution to fetch: 1k, 2k (default), 4k...
	Resolution string
	// MaxSize, when set, scales the textures down further to this many
	// pixels a side.
	MaxSize int
	// MaxTriangles decimates the main variant to this budget (0: as scanned).
	MaxTriangles int
	// LiteTriangles, when set, also makes a light variant at this budget.
	LiteTriangles int
	// Back is the back estimate: auto, none, -z, +z, -x, +x, or degrees.
	Back string
	// Plant marks vegetation: no back, no collision, cutouts expected.
	Plant bool
	// ThinFoliage decimates by thinning slots made of many small pieces
	// (needle cards, grass blades) whole-piece rather than collapsing
	// their edges (decimate_cards.go); its files are named _c<tris>.
	ThinFoliage bool
	// Collision: auto (by size), hulls, mesh or none.
	Collision string
	// HullSize is auto's largest extent for hulls (metres).
	HullSize float64
	// NoSplit keeps a file of several root meshes (a set of variations)
	// as one mesh.
	NoSplit bool
	// Cook makes the .fnm variants (off: the glTF and textures only).
	Cook bool
	// Force recooks and rewrites textures even when unchanged.
	Force bool
	Log   Log
	Stage Stage
}

func (o *ModelOptions) defaults() {
	if o.Resolution == "" {
		o.Resolution = "2k"
	}
	if o.Collision == "" {
		o.Collision = "auto"
	}
	if o.HullSize <= 0 {
		o.HullSize = 4
	}
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	if o.Stage == nil {
		o.Stage = func(string, float32) {}
	}
}

// ScanAttrRadiusCap is the radius, in metres, an imported scan's DAG weighs
// its normals and UVs by at most (nanitecook.Options.AttrRadiusCap). A scan's
// normals are bumpy all over, and weighed by the full radius of a 20 m cliff
// they costed every simplification about a metre: the generated canyon's 570
// cliff chunks drew 23 M triangles from its overview, at full detail to
// ~800 m. Capped at a metre — about the rock the weights were chosen on —
// namaqualand_cliff_02 cuts to 1.9 k triangles where it drew 149 k, at the
// same 1 px of surface error (docs/canyon/PERF.md).
const ScanAttrRadiusCap = 1.0

// Variant is one cooked (or written) mesh of an import.
type Variant struct {
	// Meta is the manifest entry, File the mesh's path relative to the
	// asset's directory; Model the resources-relative path a placement uses.
	Meta  rocks.VariantMeta
	Model string
}

// ModelImport is what ImportModel made.
type ModelImport struct {
	Asset      *polyhaven.Asset
	Resolution string
	// Dir is the asset's directory under resources/.
	Dir      string
	GLTF     string // resources-relative
	Textures []string
	Variants []Variant
	Frame    FrameResult
	// Cutout lists the slots drawn alpha-tested.
	Cutout []string
	// Warnings are things worth telling the user (a scale mismatch...).
	Warnings []string
	// Downloaded is the bytes fetched this time (0 when all were cached);
	// the times are wall-clock milliseconds.
	Downloaded                      int64
	DownloadMS, TextureMS, CookMS   float64
	SourceTriangles, SourceVertices int
}

// gltfJSON is the parts of the glTF the importer reads and rewrites; the
// rest of the document is carried through untouched as raw JSON.
type gltfMaterial struct {
	Name        string  `json:"name"`
	AlphaMode   string  `json:"alphaMode"`
	AlphaCutoff float64 `json:"alphaCutoff"`
	DoubleSided bool    `json:"doubleSided"`
	Normal      *struct {
		Index int `json:"index"`
	} `json:"normalTexture"`
	PBR *struct {
		Base *struct {
			Index int `json:"index"`
		} `json:"baseColorTexture"`
		MR *struct {
			Index int `json:"index"`
		} `json:"metallicRoughnessTexture"`
	} `json:"pbrMetallicRoughness"`
}

type gltfTexture struct {
	Source *int `json:"source"`
}

// ImportModel imports one model asset.
func ImportModel(ctx context.Context, c *polyhaven.Client, id string, opt ModelOptions) (*ModelImport, error) {
	opt.defaults()
	if opt.Resources == "" {
		return nil, fmt.Errorf("no resources directory")
	}
	t0 := time.Now()
	opt.Stage("listing "+id, 0)
	info, err := c.Info(ctx, id)
	if err != nil {
		return nil, err
	}
	if !info.IsModel() {
		return nil, fmt.Errorf("%s is not a model (type %d)", id, info.Type)
	}
	files, err := c.Files(ctx, id)
	if err != nil {
		return nil, err
	}
	res, err := pickResolution(files.Resolutions("gltf"), opt.Resolution)
	if err != nil {
		return nil, fmt.Errorf("%s: glTF: %w", id, err)
	}
	gf, ok := files.Get("gltf", res, "gltf")
	if !ok {
		return nil, fmt.Errorf("%s: no glTF at %s", id, res)
	}
	out := &ModelImport{Asset: info, Resolution: res, Dir: ModelDir(id)}
	dir := filepath.Join(opt.Resources, filepath.FromSlash(out.Dir))

	// What to fetch: the glTF, its includes, and the extra maps.
	type got struct {
		f    polyhaven.File
		path string
	}
	fetched := map[string]*got{} // include path ("" for the gltf, "map:<name>" for extras)
	var jobs []polyhaven.Job
	add := func(key string, f polyhaven.File) {
		if _, ok := fetched[key]; ok {
			return
		}
		fetched[key] = &got{f: f}
		jobs = append(jobs, polyhaven.Job{File: f})
	}
	add("", gf)
	inc := make([]string, 0, len(gf.Include))
	for p := range gf.Include {
		inc = append(inc, p)
	}
	sort.Strings(inc)
	for _, p := range inc {
		add(p, gf.Include[p])
	}
	// The glTF itself first: its materials say which extras are wanted.
	if _, err := c.Fetch(ctx, gf, nil); err != nil {
		return nil, err
	}
	gdata, err := os.ReadFile(c.StorePath(gf))
	if err != nil {
		return nil, err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(gdata, &doc); err != nil {
		return nil, fmt.Errorf("%s: glTF: %w", id, err)
	}
	var mats []gltfMaterial
	var texs []gltfTexture
	var images []map[string]any
	_ = json.Unmarshal(doc["materials"], &mats)
	_ = json.Unmarshal(doc["textures"], &texs)
	_ = json.Unmarshal(doc["images"], &images)
	imgOf := func(ti int) int {
		if ti < 0 || ti >= len(texs) || texs[ti].Source == nil {
			return -1
		}
		return *texs[ti].Source
	}
	// Each material's textures. A material's albedo is ph_<id>[_<part>];
	// its normal map and ARM are named after the ALBEDO (the engine finds
	// a normal map as <albedo>_normal), so a normal image two materials
	// share is written once per albedo. Materials sharing one diffuse image
	// (an atlas: a bush's twigs and leaves) share one albedo.
	type matTex struct {
		albedo                   string
		diffImg, normImg, armImg int
		alpha                    string
	}
	mt := make([]matTex, len(mats))
	albedoBy := map[string]string{}
	uriOf := func(ii int) string {
		if ii < 0 || ii >= len(images) {
			return ""
		}
		u, _ := images[ii]["uri"].(string)
		return u
	}
	multi := len(mats) > 1
	usedParts := map[string]bool{}
	for si, mat := range mats {
		part := ""
		if multi {
			if part = partName(id, mat.Name); part == "" || usedParts[part] {
				if part == "" && !usedParts["main"] {
					part = "main"
				} else {
					part = fmt.Sprintf("m%d", si)
				}
			}
			usedParts[part] = true
		}
		t := matTex{albedo: TextureName(id, part), diffImg: -1, normImg: -1, armImg: -1}
		if mat.AlphaMode == "MASK" || mat.AlphaMode == "BLEND" {
			// Poly Haven ships the cutout as its own map: Alpha (one
			// material, or one atlas) or <part>_alpha. A BLEND material
			// with no alpha map (a quiver tree's solid rosettes) stays
			// opaque.
			for _, k := range alphaKeys(part) {
				if _, ok := files[k]; ok {
					t.alpha = k
					break
				}
			}
			if t.alpha != "" || mat.AlphaMode == "MASK" {
				out.Cutout = append(out.Cutout, mat.Name)
				// Thinned foliage keeps its cutout's coverage down the
				// mip chain (texcompress/mip_coverage.go), asked for by
				// the albedo's name.
				if opt.ThinFoliage {
					t.albedo += texcompress.CoverageSuffix
				}
			}
		}
		if mat.PBR != nil && mat.PBR.Base != nil {
			t.diffImg = imgOf(mat.PBR.Base.Index)
		}
		if mat.Normal != nil {
			t.normImg = imgOf(mat.Normal.Index)
		}
		if mat.PBR != nil && mat.PBR.MR != nil {
			t.armImg = imgOf(mat.PBR.MR.Index)
		}
		fresh := true
		if t.diffImg >= 0 {
			key := uriOf(t.diffImg) + "|" + t.alpha
			if n, ok := albedoBy[key]; ok {
				t.albedo, fresh = n, false
			} else {
				albedoBy[key] = t.albedo
			}
		}
		mt[si] = t
		if !fresh {
			continue
		}
		if t.alpha != "" {
			if f, ok := mapFile(files, t.alpha, res); ok {
				add("map:"+t.alpha, f)
			}
		}
		for _, k := range dispKeys(part) {
			if f, ok := mapFile(files, k, res); ok {
				add("map:"+k+":"+t.albedo, f)
				break
			}
		}
	}

	// 2. Fetch everything (the store skips what it has).
	opt.Stage("downloading "+id, 0)
	var need int64
	for _, j := range jobs {
		if !c.Cached(j.File) {
			need += j.File.Size
		}
	}
	if err := c.FetchAll(ctx, jobs, func(done, total int64) {
		if total > 0 {
			opt.Stage("downloading "+id, float32(done)/float32(total))
		}
	}); err != nil {
		return nil, err
	}
	for _, g := range fetched {
		g.path = c.StorePath(g.f)
	}
	out.Downloaded = need
	out.DownloadMS = ms(time.Since(t0))
	if need > 0 {
		opt.Log("%s: downloaded %.1f MB in %.1f s", id, float64(need)/(1<<20), out.DownloadMS/1000)
	}

	// 3. Textures, and the re-pointed glTF.
	t1 := time.Now()
	opt.Stage("textures "+id, 0)
	var specs []convertSpec
	written := map[string]bool{}
	imageName := map[int]string{}
	emit := func(name string, ii int, alpha string) error {
		if ii < 0 || ii >= len(images) {
			return nil
		}
		// Every image a material uses points at its engine name, even when
		// another image (the same file under another index) wrote it.
		if _, ok := imageName[ii]; !ok {
			imageName[ii] = name
		}
		if written[name] {
			return nil
		}
		src := fetched[uriOf(ii)]
		if src == nil {
			return fmt.Errorf("%s: image %q is not among the glTF's files", id, uriOf(ii))
		}
		s := convertSpec{src: src.path, dst: texturePath(opt.Resources, name), ch: chRGB, maxSize: opt.MaxSize}
		if strings.HasSuffix(name, "_normal") {
			s.ch = chNormal
		}
		if alpha != "" {
			if a := fetched["map:"+alpha]; a != nil {
				s.alpha = a.path
			}
		}
		written[name] = true
		specs = append(specs, s)
		out.Textures = append(out.Textures, name)
		return nil
	}
	for _, t := range mt {
		if err := emit(t.albedo, t.diffImg, t.alpha); err != nil {
			return nil, err
		}
		if t.diffImg < 0 {
			continue // a normal map with no albedo to hang it on
		}
		if err := emit(t.albedo+"_normal", t.normImg, ""); err != nil {
			return nil, err
		}
		if err := emit(t.albedo+"_arm", t.armImg, ""); err != nil {
			return nil, err
		}
	}
	for ii, name := range imageName {
		images[ii] = map[string]any{"name": name, "uri": "../../../textures/" + name + ".png", "mimeType": "image/png"}
	}
	for key, g := range fetched {
		if strings.HasPrefix(key, "map:") && strings.Count(key, ":") == 2 {
			base := key[strings.LastIndex(key, ":")+1:]
			specs = append(specs, convertSpec{src: g.path, dst: texturePath(opt.Resources, base+"_height"), ch: chLuma, maxSize: opt.MaxSize})
			out.Textures = append(out.Textures, base+"_height")
		}
	}
	sort.Strings(out.Textures)
	prov := &Provenance{}
	old, _ := ReadProvenance(filepath.Join(dir, ProvenanceFile))
	if err := convertAll(specs, opt.Force, old, prov); err != nil {
		return nil, fmt.Errorf("%s: %w", id, err)
	}
	out.TextureMS = ms(time.Since(t1))

	imgJSON, _ := json.Marshal(images)
	doc["images"] = imgJSON
	// The buffers keep their names; copy each beside the glTF.
	var bufs []struct {
		URI string `json:"uri"`
	}
	_ = json.Unmarshal(doc["buffers"], &bufs)
	for _, b := range bufs {
		if b.URI == "" || strings.HasPrefix(b.URI, "data:") {
			continue
		}
		g := fetched[b.URI]
		if g == nil {
			return nil, fmt.Errorf("%s: buffer %q is not among the glTF's files", id, b.URI)
		}
		if err := polyhaven.CopyFile(g.path, filepath.Join(dir, filepath.FromSlash(b.URI))); err != nil {
			return nil, err
		}
	}
	gjson, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		return nil, err
	}
	gpath := filepath.Join(dir, id+".gltf")
	if err := writeIfChanged(gpath, gjson); err != nil {
		return nil, err
	}
	out.GLTF = out.Dir + "/" + id + ".gltf"

	// 4. The mesh, in the frame. A file whose scene has several root nodes
	// with meshes is a set of variations side by side (Poly Haven packs a
	// bush's five shapes, a scatter set's stones, that way): each is its own
	// part, normalised and cooked alone, unless SplitParts is off.
	m, err := model.LoadGLTF(gpath)
	if err != nil {
		return nil, err
	}
	out.SourceTriangles, out.SourceVertices = m.TriangleCount(), len(m.Positions)
	whole := cloneMesh(m)
	if _, err := Normalise(whole, "none", false); err != nil {
		return nil, err
	}
	full := Measure(whole, false)
	if w := checkScale(info, full.Extent); w != "" {
		out.Warnings = append(out.Warnings, w)
		opt.Log("%s: %s", id, w)
	}
	type part struct {
		suffix string
		m      *model.ImportMesh
	}
	parts := []part{{"", m}}
	if !opt.NoSplit {
		if ps, err := splitRoots(id, gjson, dir); err != nil {
			return nil, err
		} else if len(ps) > 1 {
			parts = parts[:0]
			for _, p := range ps {
				parts = append(parts, part{p.suffix, p.m})
			}
			opt.Log("%s: %d parts (%s)", id, len(ps), strings.Join(func() []string {
				var n []string
				for _, p := range ps {
					n = append(n, p.suffix)
				}
				return n
			}(), ", "))
		}
	}

	// 5. Variants: per part, the main mesh and its light copy.
	t2 := time.Now()
	backTag := ""
	if b := strings.ToLower(strings.TrimSpace(opt.Back)); b != "" && b != "auto" {
		backTag = "_b" + strings.NewReplacer("+", "p", "-", "m", ".", "_").Replace(b)
	}
	type want struct {
		suffix string
		tris   int
	}
	for pi, pt := range parts {
		pm := pt.m
		fr, err := Normalise(pm, opt.Back, !opt.Plant)
		if err != nil {
			return nil, err
		}
		if pi == 0 {
			out.Frame = fr
		}
		wants := []want{{"", opt.MaxTriangles}}
		if opt.LiteTriangles > 0 && opt.LiteTriangles < pm.TriangleCount() && (opt.MaxTriangles <= 0 || opt.LiteTriangles < opt.MaxTriangles) {
			wants = append(wants, want{"lite", opt.LiteTriangles})
		}
		pid := id
		if pt.suffix != "" {
			pid = id + "_" + pt.suffix
		}
		for wi, w := range wants {
			opt.Stage("cooking "+id, float32(pi*len(wants)+wi)/float32(len(parts)*len(wants)))
			vm := pm
			decimated := w.tris > 0 && w.tris < pm.TriangleCount()
			if decimated && opt.ThinFoliage {
				vm = DecimateCards(pm, w.tris, out.Cutout)
			} else if decimated {
				vm = Decimate(cloneMesh(pm), w.tris)
			}
			meta := Measure(vm, fr.HasBack)
			meta.Name = pid
			if w.suffix != "" {
				meta.Name = pid + "_" + w.suffix
			}
			stem := pid
			if decimated && opt.ThinFoliage {
				stem = fmt.Sprintf("%s_c%d", pid, w.tris)
			} else if decimated {
				stem = fmt.Sprintf("%s_t%d", pid, w.tris)
			}
			stem += backTag
			meta.PlaceCollision = collisionFor(opt, meta.Extent)
			meta.Collision = "mesh"
			if meta.PlaceCollision == "hulls" {
				meta.Collision = "hulls"
			}
			tc := time.Now()
			if opt.Cook {
				meta.File = stem + ".fnm"
				skipped, err := meshout.Cook(vm, filepath.Join(dir, meta.File), meshout.CookOptions{Force: opt.Force,
					CollisionMesh: meta.Collision == "mesh", CollisionHulls: meta.Collision == "hulls",
					AttrRadiusCap: ScanAttrRadiusCap})
				if err != nil {
					return nil, fmt.Errorf("%s: cook: %w", meta.Name, err)
				}
				if skipped {
					opt.Log("%s: %s up to date", id, meta.File)
				}
			} else {
				meta.File = stem + ".glb"
				if err := meshout.WriteGLB(filepath.Join(dir, meta.File), vm); err != nil {
					return nil, err
				}
			}
			meta.CookMS = ms(time.Since(tc))
			size := int64(0)
			if st, err := os.Stat(filepath.Join(dir, meta.File)); err == nil {
				size = st.Size()
			}
			opt.Log("%s: %d triangles (scanned %d), %.2f×%.2f×%.2f m, %s collision, back %v (%.2f), %s %.1f MB in %.1f s",
				meta.Name, meta.Triangles, pm.TriangleCount(), meta.Extent[0], meta.Extent[1], meta.Extent[2],
				meta.PlaceCollision, fr.HasBack, fr.Confidence, meta.File, float64(size)/(1<<20), meta.CookMS/1000)
			out.Variants = append(out.Variants, Variant{Meta: meta, Model: out.Dir + "/" + meta.File})
		}
	}
	out.CookMS = ms(time.Since(t2))

	// 6. Provenance.
	prov.fill(info, res, "model")
	for key, g := range fetched {
		name := key
		if name == "" {
			name = path.Base(gf.URL)
		}
		prov.Files = append(prov.Files, ProvFile{Name: name, URL: g.f.URL, MD5: g.f.MD5, Size: g.f.Size})
	}
	sort.Slice(prov.Files, func(i, j int) bool { return prov.Files[i].Name < prov.Files[j].Name })
	prov.Import = &ProvImport{Triangles: out.SourceTriangles, Back: out.Frame.HasBack, BackConfidence: round3(out.Frame.Confidence),
		Yaw: round3(out.Frame.Yaw * 180 / math.Pi), Extent: full.Extent, Textures: out.Textures, Cutout: out.Cutout, Warnings: out.Warnings}
	for _, v := range out.Variants {
		prov.Import.Meshes = append(prov.Import.Meshes, ProvMesh{File: v.Meta.File, Triangles: v.Meta.Triangles, Collision: v.Meta.PlaceCollision})
	}
	if err := prov.Write(filepath.Join(dir, ProvenanceFile)); err != nil {
		return nil, err
	}
	opt.Stage(id, 1)
	return out, nil
}

// alphaKeys and dispKeys are the file-listing names a material's alpha and
// displacement maps go by.
func alphaKeys(part string) []string {
	if part == "" {
		return []string{"Alpha", "alpha"}
	}
	return []string{part + "_alpha", "Alpha"}
}

func dispKeys(part string) []string {
	if part == "" {
		return []string{"Displacement", "disp"}
	}
	return []string{part + "_disp", part + "_Displacement"}
}

// mapFile is a texture map's file at res (or the nearest offered), JPG
// preferred, then PNG.
func mapFile(files polyhaven.Files, name, res string) (polyhaven.File, bool) {
	r, err := pickResolution(files.Resolutions(name), res)
	if err != nil {
		return polyhaven.File{}, false
	}
	for _, f := range []string{"jpg", "png"} {
		if x, ok := files.Get(name, r, f); ok {
			return x, true
		}
	}
	return polyhaven.File{}, false
}

// collisionFor is a variant's placement collision.
func collisionFor(opt ModelOptions, ext [3]float32) string {
	if opt.Plant && opt.Collision == "auto" {
		return "none"
	}
	if opt.Collision != "auto" {
		return opt.Collision
	}
	if float64(max(ext[0], ext[1], ext[2])) <= opt.HullSize {
		return "hulls"
	}
	return "mesh"
}

// checkScale compares the mesh's extents with the API's dimensions (both
// sorted, largest first); "" when they agree within 15%.
func checkScale(a *polyhaven.Asset, ext [3]float32) string {
	want := a.SizeMetres()
	if len(want) < 3 || want[0] <= 0 {
		return ""
	}
	got := []float64{float64(ext[0]), float64(ext[1]), float64(ext[2])}
	sort.Sort(sort.Reverse(sort.Float64Slice(got)))
	r := got[0] / want[0]
	if r > 0.85 && r < 1.15 {
		return ""
	}
	return fmt.Sprintf("the mesh is %.2f m across but Poly Haven lists %.2f m (×%.2f): check its scale", got[0], want[0], r)
}

// convertAll writes the textures, four at a time, skipping one whose output
// exists and whose sources' md5s match the previous import's record.
func convertAll(specs []convertSpec, force bool, old, prov *Provenance) error {
	prev := map[string]string{}
	if old != nil {
		for _, t := range old.TextureSources {
			prev[t.Texture] = t.Sources
		}
	}
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	for _, s := range specs {
		s := s
		key, err := sourceKey(s)
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(filepath.Base(s.dst), ".png")
		prov.TextureSources = append(prov.TextureSources, ProvTexture{Texture: name, Sources: key})
		if _, err := os.Stat(s.dst); err == nil && !force && prev[name] == key {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := convert(s); err != nil {
				mu.Lock()
				if first == nil {
					first = err
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	sort.Slice(prov.TextureSources, func(i, j int) bool { return prov.TextureSources[i].Texture < prov.TextureSources[j].Texture })
	return first
}

// sourceKey names a conversion's inputs: the stored files are named by
// their md5, so their base names are it; plus the channel and size.
func sourceKey(s convertSpec) (string, error) {
	k := filepath.Base(s.src)
	if s.alpha != "" {
		k += "+" + filepath.Base(s.alpha)
	}
	return fmt.Sprintf("%s/%d/%d", k, s.ch, s.maxSize), nil
}

// writeIfChanged writes data unless path already holds it.
func writeIfChanged(path string, data []byte) error {
	if old, err := os.ReadFile(path); err == nil && string(old) == string(data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func ms(d time.Duration) float64 { return math.Round(float64(d.Microseconds())/100) / 10 }

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// rootPart is one root node of a glTF scene as its own mesh.
type rootPart struct {
	suffix string
	m      *model.ImportMesh
}

// splitRoots is the scene's root nodes that carry meshes (directly or
// below), each parsed alone — nil when there is at most one.
func splitRoots(id string, gjson []byte, dir string) ([]rootPart, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(gjson, &doc); err != nil {
		return nil, err
	}
	var nodes []struct {
		Name     string `json:"name"`
		Mesh     *int   `json:"mesh"`
		Children []int  `json:"children"`
	}
	var scenes []struct {
		Nodes []int `json:"nodes"`
	}
	_ = json.Unmarshal(doc["nodes"], &nodes)
	_ = json.Unmarshal(doc["scenes"], &scenes)
	var roots []int
	if len(scenes) > 0 {
		var scene int
		_ = json.Unmarshal(doc["scene"], &scene)
		if scene < 0 || scene >= len(scenes) {
			scene = 0
		}
		roots = scenes[scene].Nodes
	}
	var hasMesh func(n, depth int) bool
	hasMesh = func(n, depth int) bool {
		if n < 0 || n >= len(nodes) || depth > 64 {
			return false
		}
		if nodes[n].Mesh != nil {
			return true
		}
		for _, c := range nodes[n].Children {
			if hasMesh(c, depth+1) {
				return true
			}
		}
		return false
	}
	var keep []int
	for _, r := range roots {
		if hasMesh(r, 0) {
			keep = append(keep, r)
		}
	}
	if len(keep) < 2 {
		return nil, nil
	}
	var out []rootPart
	used := map[string]bool{}
	for k, r := range keep {
		one := map[string]json.RawMessage{}
		for key, v := range doc {
			one[key] = v
		}
		sc, _ := json.Marshal([]map[string]any{{"nodes": []int{r}}})
		one["scenes"] = sc
		one["scene"] = json.RawMessage("0")
		data, err := json.Marshal(one)
		if err != nil {
			return nil, err
		}
		m, err := model.ParseGLTF(data, dir)
		if err != nil {
			return nil, fmt.Errorf("%s part %d: %w", id, k, err)
		}
		suffix := partName(id, nodes[r].Name)
		if suffix == "" || used[suffix] {
			suffix = fmt.Sprintf("p%d", k+1)
		}
		used[suffix] = true
		out = append(out, rootPart{suffix: suffix, m: m})
	}
	return out, nil
}
