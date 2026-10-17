package stages

// materials_out.go — the .fmat files the generator stages write
// (docs/canyon/PHASE_5.md).
//
// A stage's own outputs live in its folder under generated/, but a material
// is found by NAME in resources/materials/ — the engine's materials are one
// flat namespace, like its textures — so the materials a stage writes go
// there, named gen_<generator>_<stage>[_<part>] so that two generators, or
// two stages of one, cannot overwrite each other's. The maps a material
// points at stay in the stage's folder: material texture parameters accept a
// resources-relative path (gfx.FxTexturePath).
//
// The files are written whole on every run of their stage and carry a
// header saying so: edit the stage's parameters, not the file. They are not
// in the stage's manifest (that lists the folder), so a stage that stops
// writing one leaves it behind, harmless, until it is deleted by hand.
//
// What a placing stage writes for the rocks it places composes two things:
// the rock source's own BASE material (a generated rock's triplanar texture,
// which its rocks stage writes) and the placing stage's additions (the ground
// blend toward the terrain it sits on, the colour match, desert varnish).

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"os"
	"path/filepath"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/material"
	"foundry/internal/procgen"

	"github.com/go-gl/mathgl/mgl32"
	_ "image/png"
)

// genMaterialName is a stage's material name, with an optional part.
func genMaterialName(rc *procgen.RunContext, part string) string {
	n := "gen_" + safeName(rc.Doc.Name, "gen") + "_" + safeName(rc.Stage.ID, "stage")
	if part != "" {
		n += "_" + safeName(part, "part")
	}
	return n
}

// writeMaterial writes d as resources/materials/<d.Name>.fmat, with a
// header naming the stage that owns it.
func writeMaterial(rc *procgen.RunContext, d *material.Doc) error {
	if probs := d.Validate(); len(probs) > 0 {
		return fmt.Errorf("material %s: %s", d.Name, strings.Join(probs, "; "))
	}
	data, err := material.EncodeMaterial(d)
	if err != nil {
		return err
	}
	head := fmt.Sprintf("# %s.fmat — written by the %s stage %q of generators/%s.fgen.\n"+
		"# Rerunning the stage rewrites it: change the stage's parameters, not this file.\n",
		d.Name, rc.Stage.Kind, rc.Stage.ID, rc.Doc.Name)
	dir := filepath.Join(fileutil.ResourcesPath, material.MaterialsDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, d.Name+material.MaterialExt)
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, append([]byte(head), data...)) {
		return nil // unchanged: leave its time alone
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append([]byte(head), data...), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readMaterialDoc reads a material by name from resources/materials, or nil.
func readMaterialDoc(name string) *material.Doc {
	if name == "" {
		return nil
	}
	d, err := material.ReadMaterial(filepath.Join(fileutil.ResourcesPath, material.MaterialsDirName, name+material.MaterialExt))
	if err != nil {
		return nil
	}
	return d
}

// mapRef is a file in the stage's folder as a material names it: its
// resources-relative path without .png.
func mapRef(rc *procgen.RunContext, file string) string {
	return strings.TrimSuffix(rc.AssetPath(file), ".png")
}

// textureMean is the average colour of textures/<name>.png (alpha-weighted,
// a stride of texels), and false when it cannot be read.
func textureMean(name string) ([3]float64, bool) {
	if name == "" {
		return [3]float64{}, false
	}
	f, err := os.Open(filepath.Join(fileutil.TexturesDir(), name+".png"))
	if err != nil {
		return [3]float64{}, false
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return [3]float64{}, false
	}
	b := img.Bounds()
	rgba, ok := img.(*image.RGBA)
	if !ok {
		rgba = image.NewRGBA(b)
		draw.Draw(rgba, b, img, b.Min, draw.Src)
	}
	n := len(rgba.Pix) / 4
	stride := 1
	for n/stride > 1<<18 {
		stride *= 2
	}
	var r, g, bl, a float64
	for i := 0; i < n; i += stride {
		px := rgba.Pix[4*i : 4*i+4]
		w := float64(px[3])
		r += float64(px[0]) * w
		g += float64(px[1]) * w
		bl += float64(px[2]) * w
		a += w
	}
	if a == 0 {
		return [3]float64{}, false
	}
	a *= 255
	return [3]float64{r / a, g / a, bl / a}, true
}

// rockFx are a placing stage's additions to the rocks it places.
type rockFx struct {
	// Ground is the terrain_material stage's artifact (nil: no ground blend,
	// and the match target is MatchColor).
	Ground *TerrainMaterialArtifact
	// Match is how far the rocks' colour moves toward the ground's (0..1).
	Match      float64
	MatchColor [3]float64
	// Band is the ground blend's height (m); Streaks the varnish strength.
	Band, Streaks float64
	// Brightness scales the matched colour (0 or 1: as the scan is): a pale
	// scan beside darker heightfield rock.
	Brightness float64
}

func (f rockFx) any() bool { return f.Ground != nil || f.Match > 0 || f.Streaks > 0 }

// rockMaterial writes the material a placing stage's rocks from one source
// wear — the source's base material (its texture and its effects) plus fx —
// and returns its name; the base's own name when fx adds nothing, "" when
// there is nothing at all.
func rockMaterial(rc *procgen.RunContext, part, base string, fx rockFx) (string, error) {
	if !fx.any() {
		return base, nil
	}
	d := &material.Doc{Name: genMaterialName(rc, part), Domain: material.DomainSurface,
		Doc: fmt.Sprintf("Rocks placed by %s (%s): their own texture", rc.Stage.ID, part)}
	if b := readMaterialDoc(base); b != nil {
		d.Texture = b.Texture
		d.Effects = append(d.Effects, b.Clone().Effects...)
		d.Doc += ", " + base
	}
	target := fx.MatchColor
	if g := fx.Ground; g != nil {
		e := d.AddEffect(material.KindGroundBlend)
		e.Set("ground", material.TextureValue(g.Ground))
		e.Set("height", material.TextureValue(g.Height))
		e.Set("bounds_min", material.Vec2Value(mgl32.Vec2{float32(g.Min[0]), float32(g.Min[1])}))
		e.Set("bounds_max", material.Vec2Value(mgl32.Vec2{float32(g.Max[0]), float32(g.Max[1])}))
		e.Set("height_range", material.Vec2Value(mgl32.Vec2{g.HeightLo, g.HeightHi}))
		e.Set("band", material.FloatValue(float32(fx.Band)))
		d.Doc += ", blended into the ground"
	}
	if fx.Match > 0 {
		e := d.AddEffect(material.KindColorGrade)
		e.Set("match", material.FloatValue(float32(fx.Match)))
		e.Set("target", material.ColorValue(mgl32.Vec3{float32(target[0]), float32(target[1]), float32(target[2])}))
		if fx.Brightness > 0 && fx.Brightness != 1 {
			e.Set("brightness", material.FloatValue(float32(fx.Brightness)))
		}
		d.Doc += fmt.Sprintf(", %.0f%% matched to the ground's colour", fx.Match*100)
	}
	if fx.Streaks > 0 {
		e := d.AddEffect(material.KindStreaks)
		e.Set("strength", material.FloatValue(float32(fx.Streaks)))
		d.Doc += ", desert varnish"
	}
	if err := writeMaterial(rc, d); err != nil {
		return "", err
	}
	return d.Name, nil
}

// rockFxParams are the parameters scatter (per rule) and cliff_wall give the
// rocks they place.
func rockFxParams(streaks float64) []procgen.ParamDef {
	return []procgen.ParamDef{
		{Name: "match_ground", Added: true, Type: procgen.ParamFloat, Label: "Match ground colour", Default: procgen.Float(-1), Min: -1, Max: 1, Step: 0.01,
			Doc: "How far the rocks' colour is carried toward the ground's (0: their own colour; 1: the ground's hue and saturation, their own brightness and detail). " +
				"-1: what their Poly Haven rocks stage says."},
		{Name: "ground_band", Added: true, Type: procgen.ParamFloat, Label: "Ground blend (m)", Default: procgen.Float(0.5), Min: 0, Max: 20, Step: 0.05,
			Doc: "With a terrain material (the stage's `ground`), how far up a rock's foot fades into the ground's colour. 0: no blend."},
		{Name: "streaks", Added: true, Type: procgen.ParamFloat, Label: "Desert varnish", Default: procgen.Float(streaks), Min: 0, Max: 1, Step: 0.01,
			Doc: "Dark vertical varnish streaks down the rocks' steep faces."},
	}
}

// rockFxOf reads rockFxParams from it, the ground from the stage's `ground`
// param (when set), and the source's default match.
func rockFxOf(rc *procgen.RunContext, it procgen.Params, ground *TerrainMaterialArtifact, src *RocksArtifact) rockFx {
	fx := rockFx{Match: it.Float("match_ground"), Band: it.Float("ground_band"), Streaks: it.Float("streaks"),
		MatchColor: [3]float64{0.62, 0.38, 0.27}}
	if fx.Match < 0 {
		fx.Match = 0
		if src != nil && src.Manifest != nil {
			fx.Match = src.Manifest.MatchGround
		}
	}
	if fx.Band > 0 {
		fx.Ground = ground
	}
	if ground != nil {
		fx.MatchColor = ground.GroundMean
	}
	return fx
}

// groundArtifact is the terrain_material (or region_material) stage the
// param names, or nil.
func groundArtifact(rc *procgen.RunContext, id string) (*TerrainMaterialArtifact, error) {
	if id == "" {
		return nil, nil
	}
	a, err := rc.UpstreamArtifact(id)
	if err != nil {
		return nil, err
	}
	g, ok := a.(*TerrainMaterialArtifact)
	if !ok {
		return nil, fmt.Errorf("stage %s is not a terrain_material or region_material stage", id)
	}
	// An environment region's stage matches its rocks to its own ground
	// (place_region_ground.go); a classic run gets g itself.
	return regionGround(&rc.Env, g), nil
}

// rocksTriplanarMaterial writes a generated rocks stage's base material when
// it names a textures stage: the layer's albedo (and so its _normal),
// projected triplanar — the box-mapped UVs a generated rock has seam on every
// edge — and returns its name; "" when the stage names none.
func rocksTriplanarMaterial(rc *procgen.RunContext) (string, error) {
	p := rc.Params
	id := p.Str("textures")
	if id == "" {
		return "", nil
	}
	a, err := rc.UpstreamArtifact(id)
	if err != nil {
		return "", err
	}
	set, ok := a.(*TextureSetManifest)
	if !ok {
		return "", fmt.Errorf("stage %s is not a Poly Haven textures stage", id)
	}
	l := set.Layer(p.Str("texture_layer"))
	if l == nil {
		return "", fmt.Errorf("the textures stage %s has no layer %q", id, p.Str("texture_layer"))
	}
	scale := p.Float("triplanar_scale")
	if scale <= 0 {
		scale = l.Scale
	}
	if scale <= 0 {
		scale = 2
	}
	d := &material.Doc{Name: genMaterialName(rc, ""), Domain: material.DomainSurface, Texture: l.Albedo,
		Doc: fmt.Sprintf("Generated rocks: %s (%s) projected triplanar under their vertex colour", l.Name, l.ID)}
	e := d.AddEffect(material.KindTriplanar)
	e.Set("scale", material.FloatValue(float32(scale)))
	if err := writeMaterial(rc, d); err != nil {
		return "", err
	}
	rc.Logf("material %s: %s triplanar at %.2g m", d.Name, l.Albedo, scale)
	return d.Name, nil
}
