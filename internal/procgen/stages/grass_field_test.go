package stages

// grass_field_test.go — the grass_field stage end to end against the fake
// Poly Haven: the kit (kinds from the plants stage's pieces, capped LOD 0,
// thinner LODs), one field per terrain tile with every clump on the ground
// inside its tile, one grass volume per field that the level schema reads,
// determinism, validation and the preview.

import (
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/grassfield"
	"foundry/internal/procgen"
	"foundry/internal/procgen/terrain"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

func grassDoc() *procgen.Doc {
	d := smallTerrainDoc()
	f, ref, v2, s := procgen.Float, procgen.StageRef, procgen.Vec2, procgen.String
	d.Stages = append(d.Stages,
		procgen.Stage{Kind: PolyHavenPlantsKind, ID: "flora", Params: procgen.Params{
			"assets": procgen.List(procgen.Params{"id": s("p1")}), "thin_foliage": procgen.Bool(true)}},
		procgen.Stage{Kind: GrassFieldKind, ID: "grass", Params: procgen.Params{"terrain": ref("ground"), "cell": f(8),
			"rules": procgen.List(procgen.Params{"name": s("sward"), "plants": ref("flora"), "density": f(3),
				"scale": v2(0.2, 0.3), "slope": v2(0, 35), "max_triangles": procgen.Int(40)})}},
	)
	return d
}

func TestGrassFieldRunsEndToEnd(t *testing.T) {
	_, res := withFakePolyHaven(t)
	d := grassDoc()
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	dir := t.TempDir()
	r, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	st := r.Stage("grass")
	art := st.Artifact.(*GrassFieldArtifact)
	if art.Instances < 500 || len(art.Fields) == 0 || len(art.Kinds) == 0 {
		t.Fatalf("artifact %+v", art)
	}
	kdata, err := os.ReadFile(filepath.Join(st.Dir, GrassKitFile))
	if err != nil {
		t.Fatal(err)
	}
	kit, err := grassfield.DecodeKit(kdata)
	if err != nil {
		t.Fatal(err)
	}
	for _, kd := range kit.Kinds {
		if kd.Texture == "" || kd.Height <= 0 || len(kd.LODs) == 0 || kd.LODs[0].Triangles() == 0 {
			t.Fatalf("kind %+v", kd.Name)
		}
		for l := 1; l < len(kd.LODs); l++ {
			if kd.LODs[l].Triangles() >= kd.LODs[l-1].Triangles() {
				t.Fatalf("kind %s LOD %d is no thinner than LOD %d", kd.Name, l, l-1)
			}
		}
	}
	// The plants' import is where the kit read its meshes.
	if _, err := os.Stat(filepath.Join(res, "models", "polyhaven", "p1", "p1.gltf")); err != nil {
		t.Fatal(err)
	}
	ground, err := terrain.Load(filepath.Join(dir, "ground"))
	if err != nil {
		t.Fatal(err)
	}
	vols := r.Volumes()
	if len(vols) != len(art.Fields) {
		t.Fatalf("%d volumes for %d fields", len(vols), len(art.Fields))
	}
	total := 0
	for _, v := range vols {
		data, _ := yaml.Marshal(map[string]interface{}(v))
		var raw world.RawVolume
		if err := yaml.UnmarshalStrict(data, &raw); err != nil {
			t.Fatal(err)
		}
		wv, err := world.VolumeFromRaw(raw)
		if err != nil {
			t.Fatal(err)
		}
		if !wv.IsGrass() || wv.Grass.Chunk != world.GrassChunkAuto || !strings.HasPrefix(wv.Grass.File, st.RelDir+"/grass_") {
			t.Fatalf("volume %+v", wv.Grass)
		}
		fdata, err := os.ReadFile(filepath.Join(dir, "grass", filepath.Base(wv.Grass.File)))
		if err != nil {
			t.Fatal(err)
		}
		f, err := grassfield.DecodeField(fdata)
		if err != nil {
			t.Fatal(err)
		}
		if f.Kit != st.RelDir+"/"+GrassKitFile || f.Kinds != len(kit.Kinds) {
			t.Fatalf("field kit %s kinds %d", f.Kit, f.Kinds)
		}
		half := mgl32.Vec3{wv.HalfX, wv.HalfY, wv.HalfZ}
		lo, hi := wv.Position.Sub(half), wv.Position.Add(half)
		for _, in := range f.Instances() {
			if in.Pos[0] < lo[0]-0.01 || in.Pos[0] > hi[0]+0.01 || in.Pos[2] < lo[2]-0.01 || in.Pos[2] > hi[2]+0.01 ||
				in.Pos[1] < lo[1] || in.Pos[1] > hi[1] {
				t.Fatalf("clump %v outside its volume %v..%v", in.Pos, lo, hi)
			}
			if gh := ground.Grid.Sample(float64(in.Pos[0]), float64(in.Pos[2])); math.Abs(float64(gh-in.Pos[1])) > 0.02 {
				t.Fatalf("clump at %v floats over ground %g", in.Pos, gh)
			}
		}
		total += f.Count()
	}
	if total != art.Instances {
		t.Fatalf("fields hold %d, the summary says %d", total, art.Instances)
	}
	// Load reads the summary back.
	a, err := loadGrassField(st)
	if err != nil || a.(*GrassFieldArtifact).Instances != art.Instances {
		t.Fatalf("load: %v", err)
	}

	// Deterministic: a second run elsewhere writes the same bytes.
	dir2 := t.TempDir()
	if _, err := procgen.Run(context.Background(), grassDoc(), procgen.Options{OutDir: dir2}); err != nil {
		t.Fatal(err)
	}
	for _, name := range append([]string{GrassKitFile}, art.Fields...) {
		a, _ := os.ReadFile(filepath.Join(dir, "grass", name))
		b, _ := os.ReadFile(filepath.Join(dir2, "grass", name))
		// A field names its kit by the stage folder's path, which differs
		// between the two output folders: compare with it blanked.
		if strings.HasSuffix(name, grassfield.FieldExt) {
			fa, ea := grassfield.DecodeField(a)
			fb, eb := grassfield.DecodeField(b)
			if ea != nil || eb != nil {
				t.Fatalf("%s: %v %v", name, ea, eb)
			}
			fa.Kit, fb.Kit = "", ""
			a, _ = grassfield.EncodeField(fa)
			b, _ = grassfield.EncodeField(fb)
		}
		if len(a) == 0 || !bytes.Equal(a, b) {
			t.Fatalf("%s differs between runs", name)
		}
	}
}

func TestGrassFieldValidationAndPreview(t *testing.T) {
	d := grassDoc()
	st := d.StageByID("grass")
	st.Params["terrain"] = procgen.StageRef("")
	items := st.Params["rules"]
	items.Items[0]["plants"] = procgen.StageRef("")
	st.Params["rules"] = items
	probs := d.Validate()
	var msgs []string
	for _, pr := range probs {
		msgs = append(msgs, pr.String())
	}
	joined := strings.Join(msgs, "; ")
	if !strings.Contains(joined, "terrain") || !strings.Contains(joined, "plants") {
		t.Fatalf("problems %v", probs)
	}
	d = grassDoc()
	pv, err := procgen.Preview(context.Background(), d, "grass", procgen.NewPreviewCache(0), procgen.PreviewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Points) != 1 || len(pv.Points[0].Points) == 0 {
		t.Fatalf("preview points %+v", pv.Points)
	}
}
