package stages

// terrain_material_test.go — the phase-5 material outputs end to end, on the
// fake Poly Haven: a terrain_material stage over a small terrain and a
// textures stage (the splat material and its maps), the terrain's tiles
// wearing it, scatter and cliff_wall writing their rocks' materials (ground
// blend toward the terrain, the colour match), plants left alone, and a
// generated rocks stage's triplanar base material.

import (
	"context"
	"image"
	_ "image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/material"
	"foundry/internal/procgen"
	"foundry/internal/procgen/terrain"
)

func readPNG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestTerrainMaterialAndRockMaterials(t *testing.T) {
	_, res := withFakePolyHaven(t)
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = res
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	dir := filepath.Join(res, "generated", "t")
	d := polyHavenDoc()
	ref, s, f := procgen.StageRef, procgen.String, procgen.Float
	d.Stages[0].Params["material"] = s("gen_t_ground_mat")
	// The textures stage has one layer; every splat layer names it.
	d.Stages = append(d.Stages[:4], append([]procgen.Stage{{Kind: TerrainMaterialKind, ID: "ground_mat", Params: procgen.Params{
		"terrain": ref("ground"), "textures": ref("tex"), "sand": s("sand"), "cracked": s("sand"), "gravel": s("sand"),
		"rock": s("sand")}}}, d.Stages[4:]...)...)
	walls := d.StageByID("walls")
	walls.Params["ground"], walls.Params["match_ground"] = ref("ground_mat"), f(0.7)
	sc := d.StageByID("scatter")
	sc.Params["ground"] = ref("ground_mat")
	sc.Params["rules"].Items[0]["match_ground"] = f(0.5)
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	r, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	a := r.Stage("ground_mat").Artifact.(*TerrainMaterialArtifact)
	if a.Material != "gen_t_ground_mat" {
		t.Fatalf("material %q", a.Material)
	}
	sum := a.Coverage[0] + a.Coverage[1] + a.Coverage[2] + a.Coverage[3]
	if math.Abs(sum-1) > 0.02 {
		t.Errorf("coverage %v sums to %.3f", a.Coverage, sum)
	}
	// The weights sum to one at every texel; the height map is the terrain's.
	w, ok := readPNG(t, filepath.Join(res, filepath.FromSlash(a.Weights)+".png")).(*image.NRGBA)
	if !ok {
		t.Fatal("the weights map is not NRGBA: its alpha is a weight, not coverage")
	}
	for k := 0; k < len(w.Pix); k += 4 {
		if s := int(w.Pix[k]) + int(w.Pix[k+1]) + int(w.Pix[k+2]) + int(w.Pix[k+3]); s < 250 || s > 260 {
			t.Fatalf("weights at texel %d sum to %d/255", k/4, s)
		}
	}
	tr := r.Stage("ground").Artifact.(*terrain.Terrain)
	h := readPNG(t, filepath.Join(res, filepath.FromSlash(a.Height)+".png"))
	g := tr.Grid
	for _, k := range []int{0, len(g.H) / 3, len(g.H) - 1} {
		r8, g8, _, _ := h.At(k%g.S(), k/g.S()).RGBA()
		v := float64(uint16(r8>>8)<<8|uint16(g8>>8)) / 65535
		got := float64(a.HeightLo) + v*float64(a.HeightHi-a.HeightLo)
		if math.Abs(got-float64(g.H[k])) > 0.01 {
			t.Errorf("height map at %d: %.3f, terrain %.3f", k, got, g.H[k])
		}
	}
	// The material: a splat over the maps, the rock layer triplanar.
	md, err := material.ReadMaterial(filepath.Join(res, "materials", a.Material+".fmat"))
	if err != nil {
		t.Fatal(err)
	}
	fx := material.ResolveSurfaceDoc(md).Fx
	if !fx.HasSplat || fx.SplatWeights != a.Weights || !fx.SplatLayers[3].Triplanar || fx.SplatLayers[0].Texture != "ph_sand" {
		t.Fatalf("splat %+v", fx)
	}
	if fx.SplatMin[0] >= fx.SplatMax[0] || !fx.HasMacro || !fx.HasStreaks {
		t.Fatalf("splat bounds or extras wrong: %+v", fx)
	}
	// The tiles wear it; the boulders and the wall chunks wear their stage's
	// rock material; the trees nothing.
	byPrefix := map[string]string{}
	for _, p := range r.Placements() {
		for _, pre := range []string{".ground.tile", ".scatter.boulders", ".scatter.trees", ".walls.chunk"} {
			if strings.Contains(p.ID, pre) {
				byPrefix[pre] = p.Material
			}
		}
	}
	if byPrefix[".ground.tile"] != "gen_t_ground_mat" {
		t.Errorf("tiles wear %q", byPrefix[".ground.tile"])
	}
	if byPrefix[".scatter.trees"] != "" {
		t.Errorf("plants wear %q", byPrefix[".scatter.trees"])
	}
	for pre, want := range map[string]float32{".scatter.boulders": 0.5, ".walls.chunk": 0.7} {
		name, ok := byPrefix[pre]
		if !ok {
			continue // the small terrain may have no wall
		}
		rd, err := material.ReadMaterial(filepath.Join(res, "materials", name+".fmat"))
		if err != nil {
			t.Fatalf("%s: %v", pre, err)
		}
		rf := material.ResolveSurfaceDoc(rd).Fx
		if !rf.HasGroundBlend || rf.GroundHeight != a.Height || !rf.HasGrade || rf.GradeMatch != want {
			t.Errorf("%s wears %s: %+v", pre, name, rf)
		}
		if rd.Texture != "" {
			t.Errorf("%s: a scan's material must keep the scan's own texture, names %q", pre, rd.Texture)
		}
	}
	if _, ok := byPrefix[".scatter.boulders"]; !ok {
		t.Error("no boulders placed")
	}
}

func TestAddedParamsKeepOldHashes(t *testing.T) {
	k := procgen.KindByName(TerrainKind)
	p := procgen.Resolve(k.Params, smallTerrainDoc().Stages[0].Params)
	h := procgen.StageHash(k, 1, p, nil)
	// The phase-5 `material` parameter at its default is not in the hash:
	// the same params with it deleted hash the same.
	q := p.Clone()
	delete(q, "material")
	if procgen.StageHash(k, 1, q, nil) != h {
		t.Fatal("an Added parameter at its default changed the hash")
	}
	p["material"] = procgen.String("x")
	if procgen.StageHash(k, 1, p, nil) == h {
		t.Fatal("an Added parameter set to something else did not change the hash")
	}
}
