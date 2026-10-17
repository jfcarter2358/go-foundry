package stages

// grass_field_cover_test.go — ground cover through the grass_field stage
// (grass_field_cover.go, grass_field_card.go) against the fake Poly Haven:
// a still, ground-laid, sunk rule and a card rule beside an ordinary grass
// rule; their flags, meshes and texture in the kit; oriented fields whose
// axes are the terrain's normals; and a stage without any of it still
// writing version-1 fields at 12 bytes a clump.

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/grassfield"
	"foundry/internal/procgen"
	"foundry/internal/procgen/place"
	"foundry/internal/procgen/terrain"
)

func groundCoverDoc() *procgen.Doc {
	d := grassDoc()
	f, ref, v2, s := procgen.Float, procgen.StageRef, procgen.Vec2, procgen.String
	d.Stages = append(d.Stages[:len(d.Stages)-1:len(d.Stages)-1],
		procgen.Stage{Kind: PolyHavenTexturesKind, ID: "tex", Params: procgen.Params{
			"layers": procgen.List(procgen.Params{"name": s("litter"), "id": s("sand")})}},
		procgen.Stage{Kind: GrassFieldKind, ID: "grass", Params: procgen.Params{"terrain": ref("ground"), "cell": f(8),
			"rules": procgen.List(
				procgen.Params{"name": s("sward"), "plants": ref("flora"), "density": f(3),
					"scale": v2(0.2, 0.3), "slope": v2(0, 35), "max_triangles": procgen.Int(40)},
				procgen.Params{"name": s("twigs"), "plants": ref("flora"), "density": f(1), "scale": v2(0.2, 0.3),
					"slope": v2(0, 60), "max_triangles": procgen.Int(40), "orient": procgen.Enum("ground"),
					"wind": procgen.Bool(false), "sink": f(0.3), "tilt_jitter": f(8)},
				procgen.Params{"name": s("litter"), "card_textures": ref("tex"), "card": s("litter"), "density": f(1),
					"scale": v2(0.4, 0.6), "slope": v2(0, 60), "card_tiles": procgen.Int(2)},
			)}},
	)
	return d
}

func TestGrassFieldGroundCover(t *testing.T) {
	_, res := withFakePolyHaven(t)
	d := groundCoverDoc()
	if probs := d.Validate(); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	dir := t.TempDir()
	r, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	st := r.Stage("grass")
	kdata, err := os.ReadFile(filepath.Join(st.Dir, GrassKitFile))
	if err != nil {
		t.Fatal(err)
	}
	kit, err := grassfield.DecodeKit(kdata)
	if err != nil {
		t.Fatal(err)
	}
	var plain, still, cards []int
	for k, kd := range kit.Kinds {
		switch {
		case kd.Card():
			cards = append(cards, k)
		case kd.Flags&grassfield.KindNoWind != 0:
			still = append(still, k)
		default:
			plain = append(plain, k)
		}
	}
	if len(plain) == 0 || len(still) == 0 || len(cards) != 4 {
		t.Fatalf("kinds: %d plain, %d still, %d cards (want 4 tiles)", len(plain), len(still), len(cards))
	}
	for _, k := range plain {
		if kit.Kinds[k].Flags&^grassfield.KindCasts != 0 || kit.Kinds[k].Bend == 0 {
			t.Fatalf("the plain grass kind changed: flags %#x bend %g", kit.Kinds[k].Flags, kit.Kinds[k].Bend)
		}
	}
	for _, k := range still {
		kd := &kit.Kinds[k]
		if !kd.Oriented() || kd.Bend != 0 || kd.Flutter != 0 {
			t.Fatalf("twig kind %s: flags %#x bend %g flutter %g", kd.Name, kd.Flags, kd.Bend, kd.Flutter)
		}
		for _, m := range kd.LODs {
			for v := 0; v < m.VertexCount(); v++ {
				if w := m.Vertices[v*grassfield.VertexFloats+10:][:2]; w[0] != 0 || w[1] != 0 {
					t.Fatalf("a still kind's vertex sways: %v", w)
				}
			}
		}
	}
	tex := kit.Kinds[cards[0]].Texture
	for _, k := range cards {
		kd := &kit.Kinds[k]
		if kd.Casts() || !kd.Oriented() || kd.Texture != tex || len(kd.LODs) != 1 || kd.LODs[0].VertexCount() != 9 {
			t.Fatalf("card kind %s: %+v", kd.Name, kd.Flags)
		}
	}
	if strings.HasSuffix(tex, "_cov") {
		t.Fatalf("card texture %s must not keep its coverage down the mips (_cov): distant cards would be smudges", tex)
	}
	if _, err := os.Stat(filepath.Join(res, fileutil.TexturesDirName, tex+".png")); err != nil {
		t.Fatalf("card cut-out not written: %v", err)
	}
	// Oriented fields: every twig and card stands on the terrain's normal,
	// within the tilt jitter (8°) and the 12-bit code; sunk twigs sit below
	// the ground, cards and grass on it.
	ground, err := terrain.Load(filepath.Join(dir, "ground"))
	if err != nil {
		t.Fatal(err)
	}
	g, err := place.NewGround(ground)
	if err != nil {
		t.Fatal(err)
	}
	art := st.Artifact.(*GrassFieldArtifact)
	counts := map[string]int{}
	for _, name := range art.Fields {
		data, err := os.ReadFile(filepath.Join(st.Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		f, err := grassfield.DecodeField(data)
		if err != nil {
			t.Fatal(err)
		}
		if !f.Oriented || len(f.Data) != 4*f.Count() {
			t.Fatalf("%s is not an oriented field", name)
		}
		for _, in := range f.Instances() {
			kd := &kit.Kinds[in.Kind]
			x, z := float64(in.Pos[0]), float64(in.Pos[2])
			gh := ground.Grid.Sample(x, z)
			n := g.Normal(x, z, ground.Grid.Cell)
			dot := float64(in.Up[0])*n[0] + float64(in.Up[1])*n[1] + float64(in.Up[2])*n[2]
			lean := math.Acos(math.Min(1, dot)) * 180 / math.Pi
			switch {
			case kd.Card():
				counts["card"]++
				if lean > 0.2 || math.Abs(float64(in.Pos[1]-gh)) > 0.02 {
					t.Fatalf("card at %v leans %.2f° / %.3f m off the ground", in.Pos, lean, in.Pos[1]-gh)
				}
			case kd.Oriented():
				counts["twig"]++
				if lean > 8.2 {
					t.Fatalf("twig leans %.2f° from the normal %v (up %v, h %g scale %g, pos %v)", lean, n, in.Up, kd.Height, in.Scale, in.Pos)
				}
				if want := float64(gh) - 0.3*float64(kd.Height*in.Scale); math.Abs(float64(in.Pos[1])-want) > 0.03 {
					t.Fatalf("a twig sunk 0.3 of its height sits at %g, want %g", in.Pos[1], want)
				}
			default:
				counts["grass"]++
				if in.Up != [3]float32{0, 1, 0} || math.Abs(float64(in.Pos[1]-gh)) > 0.02 {
					t.Fatalf("grass clump moved or tilted: %v up %v", in.Pos, in.Up)
				}
			}
		}
	}
	if counts["card"] == 0 || counts["twig"] == 0 || counts["grass"] == 0 {
		t.Fatalf("counts %v", counts)
	}
}

// A grass_field that uses none of the ground cover options writes what it
// always did: version-1 fields at 12 bytes a clump and kinds with no flag
// but KindCasts.
func TestGrassFieldWithoutCoverStaysVersion1(t *testing.T) {
	withFakePolyHaven(t)
	dir := t.TempDir()
	r, err := procgen.Run(context.Background(), grassDoc(), procgen.Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	st := r.Stage("grass")
	for _, name := range st.Artifact.(*GrassFieldArtifact).Fields {
		data, _ := os.ReadFile(filepath.Join(st.Dir, name))
		if v := uint32(data[4]) | uint32(data[5])<<8; v != grassfield.FieldVersion {
			t.Fatalf("%s is version %d", name, v)
		}
		f, err := grassfield.DecodeField(data)
		if err != nil || f.Oriented || len(f.Data) != 3*f.Count() {
			t.Fatalf("%s: %v", name, err)
		}
	}
	kdata, _ := os.ReadFile(filepath.Join(st.Dir, GrassKitFile))
	kit, err := grassfield.DecodeKit(kdata)
	if err != nil {
		t.Fatal(err)
	}
	for _, kd := range kit.Kinds {
		if kd.Flags&^grassfield.KindCasts != 0 || strings.Contains(kd.Name, "#") {
			t.Fatalf("kind %s has ground cover flags %#x", kd.Name, kd.Flags)
		}
	}
}
