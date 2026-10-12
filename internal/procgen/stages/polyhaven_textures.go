package stages

// polyhaven_textures.go — the `polyhaven_textures` stage: named layers
// (sand, cracked ground, gravel, sandstone...) each a Poly Haven texture,
// imported as engine texture sets (phimport.ImportTextures: albedo, normal,
// ARM, roughness, AO, height) and described in textures.yaml — the
// texture-set artifact the material phase (docs/canyon/SPEC.md phase 5)
// splats terrain with and puts on generated rocks.
//
// textures.yaml (TextureSetManifest):
//
//	version: 1
//	credit: Assets from Poly Haven ...
//	layers:
//	- name: sand                 # the layer's name, what a material refers to
//	  id: red_sand               # the Poly Haven asset
//	  resolution: 2k
//	  size: 3                    # metres a repeat (Poly Haven's dimensions)
//	  scale: 3                   # metres a repeat the layer asks for (size unless set)
//	  albedo: ph_red_sand        # engine texture names ("" when the asset has none)
//	  normal: ph_red_sand_normal
//	  arm: ph_red_sand_arm
//	  rough: ph_red_sand_rough
//	  ao: ph_red_sand_ao
//	  height: ph_red_sand_height

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"foundry/internal/polyhaven"
	"foundry/internal/polyhaven/phimport"
	"foundry/internal/procgen"

	"gopkg.in/yaml.v2"
)

// TextureSetFile is the stage's manifest.
const TextureSetFile = "textures.yaml"

// TextureSetVersion is its format's version.
const TextureSetVersion = 1

// TextureLayer is one layer of the set.
type TextureLayer struct {
	Name       string  `yaml:"name"`
	ID         string  `yaml:"id"`
	Resolution string  `yaml:"resolution"`
	Size       float64 `yaml:"size"`
	Scale      float64 `yaml:"scale"`
	Albedo     string  `yaml:"albedo"`
	Normal     string  `yaml:"normal,omitempty"`
	ARM        string  `yaml:"arm,omitempty"`
	Rough      string  `yaml:"rough,omitempty"`
	AO         string  `yaml:"ao,omitempty"`
	Height     string  `yaml:"height,omitempty"`
}

// TextureSetManifest is textures.yaml, and the stage's artifact.
type TextureSetManifest struct {
	Version int            `yaml:"version"`
	Credit  string         `yaml:"credit"`
	Layers  []TextureLayer `yaml:"layers"`
}

// Layer is the named layer, or nil.
func (m *TextureSetManifest) Layer(name string) *TextureLayer {
	for i := range m.Layers {
		if m.Layers[i].Name == name {
			return &m.Layers[i]
		}
	}
	return nil
}

// LoadTextureSet reads a polyhaven_textures stage's manifest.
func LoadTextureSet(dir string) (*TextureSetManifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, TextureSetFile))
	if err != nil {
		return nil, err
	}
	var m TextureSetManifest
	if err := yaml.UnmarshalStrict(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", TextureSetFile, err)
	}
	if m.Version != TextureSetVersion {
		return nil, fmt.Errorf("%s: version %d, this build reads %d (re-run the stage)", TextureSetFile, m.Version, TextureSetVersion)
	}
	return &m, nil
}

func init() {
	procgen.RegisterStage(procgen.StageKind{
		Name:     PolyHavenTexturesKind,
		Label:    "Poly Haven textures",
		Category: "Materials",
		Version:  1,
		Doc: "Named texture layers — sand, cracked ground, gravel, sandstone, rock wall — each a Poly Haven (CC0) texture, " +
			"downloaded and converted to engine textures (albedo, normal, ARM, roughness, AO, height) for the terrain and " +
			"rock materials.",
		Params:   polyHavenTextureParams(),
		Run:      runPolyHavenTextures,
		Preview:  previewPolyHavenTextures,
		Load:     func(res *procgen.StageResult) (any, error) { return LoadTextureSet(res.Dir) },
		Validate: validatePolyHavenTextures,
	})
}

func polyHavenTextureParams() []procgen.ParamDef {
	s := procgen.String
	layer := func(name, id string) procgen.Params { return procgen.Params{"name": s(name), "id": s(id)} }
	return []procgen.ParamDef{
		{Name: "layers", Type: procgen.ParamList, AssetList: polyhaven.TypeTextures, Fields: []procgen.ParamDef{
			{Name: "name", Type: procgen.ParamString, Default: s("layer"), Doc: "The layer's name: what a material names it by."},
			{Name: "id", Type: procgen.ParamString, Default: s(""), Doc: "The Poly Haven texture's ID."},
			{Name: "scale", Type: procgen.ParamFloat, Label: "Scale (m)", Default: procgen.Float(0), Min: 0, Max: 1000, Step: 0.05,
				Doc: "Metres a repeat (0: the texture's real-world size, from Poly Haven)."},
		}, Default: procgen.List(layer("sand", "red_sand"), layer("cracked", "cracked_red_ground"), layer("gravel", "red_laterite_soil_stones"),
			layer("rock", "red_sandstone_wall"), layer("cliff", "rock_face"), layer("wash", "dry_riverbed_rock")),
			Doc: "The layers, each a name and a texture. Browse… opens the Poly Haven browser on textures."},
		{Name: "resolution", Type: procgen.ParamEnum, Default: procgen.Enum("2k"), Choices: []string{"1k", "2k", "4k", "8k"},
			Doc: "The resolution downloaded."},
		{Name: "max_size", Type: procgen.ParamInt, Label: "Max texture size", Default: procgen.Int(0), Min: 0, Max: 16384,
			Doc: "Scale the textures down further to at most this many pixels a side (0: as downloaded)."},
		{Name: "offline", Type: procgen.ParamBool, Default: procgen.Bool(false), Doc: "Use only what is already downloaded."},
	}
}

func validatePolyHavenTextures(_ *procgen.Doc, _ *procgen.Stage, p procgen.Params) []string {
	var out []string
	seen := map[string]bool{}
	for k, it := range p.Items("layers") {
		n := strings.TrimSpace(it.Str("name"))
		if n == "" {
			out = append(out, fmt.Sprintf("layer %d has no name", k+1))
		} else if seen[n] {
			out = append(out, fmt.Sprintf("layer %d: the name %q is taken", k+1, n))
		}
		seen[n] = true
		if err := polyhaven.ValidID(strings.TrimSpace(it.Str("id"))); err != nil {
			out = append(out, fmt.Sprintf("layer %d (%s): %v", k+1, n, err))
		}
	}
	if len(p.Items("layers")) == 0 {
		out = append(out, "Add a layer")
	}
	return out
}

func runPolyHavenTextures(rc *procgen.RunContext) error {
	p := rc.Params
	c := stageClient(p)
	res := PolyHavenResources()
	items := p.Items("layers")
	layers := make([]TextureLayer, len(items))
	t0 := time.Now()
	var mu sync.Mutex
	done := 0
	// Each import fetches through the client's four-at-a-time semaphore;
	// converting is CPU, so two layers at once.
	sem := make(chan struct{}, 2)
	err := rc.Parallel(len(items), func(k int) error {
		sem <- struct{}{}
		defer func() { <-sem }()
		it := items[k]
		id := strings.TrimSpace(it.Str("id"))
		ts, err := phimport.ImportTextures(rc.Ctx, c, id, phimport.TextureOptions{Resources: res, Resolution: p.Str("resolution"),
			MaxSize: p.Int("max_size"), Log: func(f string, a ...any) { rc.Logf(f, a...) }})
		if err != nil {
			return fmt.Errorf("layer %s: %w", it.Str("name"), err)
		}
		l := TextureLayer{Name: strings.TrimSpace(it.Str("name")), ID: id, Resolution: ts.Resolution, Size: ts.Size,
			Scale: it.Float("scale"), Albedo: ts.Albedo, Normal: ts.Normal, ARM: ts.ARM, Rough: ts.Rough, AO: ts.AO, Height: ts.Height}
		if l.Scale <= 0 {
			l.Scale = l.Size
		}
		layers[k] = l
		mu.Lock()
		done++
		rc.Progress(float32(done) / float32(len(items)))
		mu.Unlock()
		return nil
	})
	if err != nil {
		return err
	}
	man := &TextureSetManifest{Version: TextureSetVersion, Credit: polyhaven.Credit, Layers: layers}
	data, err := yaml.Marshal(man)
	if err != nil {
		return err
	}
	if err := rc.WriteFile(TextureSetFile, data); err != nil {
		return err
	}
	rc.Artifact = man
	rc.Logf("%d layers in %s", len(layers), time.Since(t0).Round(time.Millisecond))
	return nil
}

func previewPolyHavenTextures(pc *procgen.PreviewContext) (*procgen.PreviewData, error) {
	out := &procgen.PreviewData{}
	res := PolyHavenResources()
	for _, it := range pc.Params.Items("layers") {
		id := strings.TrimSpace(it.Str("id"))
		prov, err := phimport.ReadProvenance(filepath.Join(res, filepath.FromSlash(phimport.TextureSetsDir), id, phimport.ProvenanceFile))
		if err != nil {
			out.Notes = append(out.Notes, fmt.Sprintf("%s: %s, not imported yet", it.Str("name"), id))
			continue
		}
		size := 0.0
		if prov.TextureSet != nil {
			size = prov.TextureSet.Size
		}
		out.Notes = append(out.Notes, fmt.Sprintf("%s: %s — %s, %s, %.1f m a repeat, by %s", it.Str("name"), id, prov.Name,
			prov.Resolution, size, strings.Join(prov.Authors, ", ")))
	}
	out.Notes = append(out.Notes, polyhaven.Credit)
	return out, nil
}
