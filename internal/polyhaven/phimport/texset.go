package phimport

// texset.go — ImportTextures: a Poly Haven texture (ground, rock wall...)
// as an engine texture set for the material phase (docs/canyon/SPEC.md
// phase 5: splat layers, triplanar rock).
//
// From the file listing at the chosen resolution it fetches the Diffuse,
// nor_gl, arm and Displacement JPGs (falling back to Rough and AO when an
// asset has no ARM) and writes:
//
//	ph_<id>.png         albedo
//	ph_<id>_normal.png  normal (OpenGL convention; the engine's normal map)
//	ph_<id>_arm.png     AO / roughness / metal
//	ph_<id>_rough.png   roughness, grey (the ARM's G)
//	ph_<id>_ao.png      ambient occlusion, grey (the ARM's R)
//	ph_<id>_height.png  displacement, grey (height blending)
//
// and textures/polyhaven/<id>/polyhaven.yaml. The set's real-world size (the
// API's dimensions, metres a repeat) is recorded so a material can tile it
// at its true scale.

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"foundry/internal/polyhaven"
)

// TextureOptions are a texture-set import's settings.
type TextureOptions struct {
	Resources  string
	Resolution string
	MaxSize    int
	Force      bool
	Log        Log
	Stage      Stage
}

// TextureSet is an imported texture set: engine texture names per map
// ("" when the asset has none).
type TextureSet struct {
	ID, Name   string
	Resolution string
	// Size is metres a repeat (the API's dimensions; 0 when unknown).
	Size                                   float64
	Albedo, Normal, ARM, Rough, AO, Height string
	Downloaded                             int64
	DownloadMS, TextureMS                  float64
}

// ImportTextures imports one texture asset.
func ImportTextures(ctx context.Context, c *polyhaven.Client, id string, opt TextureOptions) (*TextureSet, error) {
	if opt.Resolution == "" {
		opt.Resolution = "2k"
	}
	if opt.Log == nil {
		opt.Log = func(string, ...any) {}
	}
	if opt.Stage == nil {
		opt.Stage = func(string, float32) {}
	}
	t0 := time.Now()
	info, err := c.Info(ctx, id)
	if err != nil {
		return nil, err
	}
	if !info.IsTexture() {
		return nil, fmt.Errorf("%s is not a texture (type %d)", id, info.Type)
	}
	files, err := c.Files(ctx, id)
	if err != nil {
		return nil, err
	}
	res, err := pickResolution(files.Resolutions("Diffuse"), opt.Resolution)
	if err != nil {
		return nil, fmt.Errorf("%s: Diffuse: %w", id, err)
	}
	ts := &TextureSet{ID: id, Name: info.Name, Resolution: res}
	if d := info.Dimensions; len(d) > 0 && d[0] > 0 {
		ts.Size = d[0] / 1000
	}
	type want struct {
		key   string
		ch    channel
		dst   string
		field *string
	}
	base := TextureName(id, "")
	wants := []want{
		{"Diffuse", chRGB, base, &ts.Albedo},
		{"nor_gl", chNormal, base + "_normal", &ts.Normal},
		{"arm", chRGB, base + "_arm", &ts.ARM},
		{"arm", chGrayG, base + "_rough", &ts.Rough},
		{"arm", chGrayR, base + "_ao", &ts.AO},
		{"Displacement", chLuma, base + "_height", &ts.Height},
	}
	if _, ok := files["arm"]; !ok {
		wants[3] = want{"Rough", chLuma, base + "_rough", &ts.Rough}
		wants[4] = want{"AO", chLuma, base + "_ao", &ts.AO}
	}
	var jobs []polyhaven.Job
	srcs := map[string]polyhaven.File{}
	for _, w := range wants {
		if _, ok := srcs[w.key]; ok {
			continue
		}
		if f, ok := mapFile(files, w.key, res); ok {
			srcs[w.key] = f
			jobs = append(jobs, polyhaven.Job{File: f})
			if !c.Cached(f) {
				ts.Downloaded += f.Size
			}
		}
	}
	if _, ok := srcs["Diffuse"]; !ok {
		return nil, fmt.Errorf("%s: no diffuse map", id)
	}
	opt.Stage("downloading "+id, 0)
	if err := c.FetchAll(ctx, jobs, func(done, total int64) {
		if total > 0 {
			opt.Stage("downloading "+id, float32(done)/float32(total))
		}
	}); err != nil {
		return nil, err
	}
	ts.DownloadMS = ms(time.Since(t0))
	t1 := time.Now()
	opt.Stage("textures "+id, 0)
	var specs []convertSpec
	for _, w := range wants {
		f, ok := srcs[w.key]
		if !ok {
			continue
		}
		specs = append(specs, convertSpec{src: c.StorePath(f), dst: texturePath(opt.Resources, w.dst), ch: w.ch, maxSize: opt.MaxSize})
		*w.field = w.dst
	}
	dir := filepath.Join(opt.Resources, filepath.FromSlash(TextureSetsDir), id)
	prov := &Provenance{}
	old, _ := ReadProvenance(filepath.Join(dir, ProvenanceFile))
	if err := convertAll(specs, opt.Force, old, prov); err != nil {
		return nil, fmt.Errorf("%s: %w", id, err)
	}
	ts.TextureMS = ms(time.Since(t1))
	prov.fill(info, res, "texture")
	for k, f := range srcs {
		prov.Files = append(prov.Files, ProvFile{Name: k, URL: f.URL, MD5: f.MD5, Size: f.Size})
	}
	sort.Slice(prov.Files, func(i, j int) bool { return prov.Files[i].Name < prov.Files[j].Name })
	prov.TextureSet = &ProvTexSet{Size: ts.Size, Textures: map[string]string{}}
	for k, v := range map[string]string{"albedo": ts.Albedo, "normal": ts.Normal, "arm": ts.ARM, "rough": ts.Rough, "ao": ts.AO, "height": ts.Height} {
		if v != "" {
			prov.TextureSet.Textures[k] = v
		}
	}
	if err := prov.Write(filepath.Join(dir, ProvenanceFile)); err != nil {
		return nil, err
	}
	if ts.Downloaded > 0 {
		opt.Log("%s: downloaded %.1f MB in %.1f s, textures %.1f s", id, float64(ts.Downloaded)/(1<<20), ts.DownloadMS/1000, ts.TextureMS/1000)
	}
	opt.Stage(id, 1)
	return ts, nil
}
