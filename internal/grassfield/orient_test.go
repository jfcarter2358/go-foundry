package grassfield

// orient_test.go — ground cover's instance format (orient.go, card.go): the
// up axis packs and unpacks, the rotation stands a clump on it, an oriented
// field round-trips at version 2, and a field WITHOUT oriented kinds is
// byte for byte what the instancer wrote before P3 (a golden taken from the
// code at HEAD before the change).

import (
	"crypto/sha256"
	"fmt"
	"math"
	"testing"
)

func near3(a, b [3]float32, eps float64) bool {
	for i := range a {
		if math.Abs(float64(a[i]-b[i])) > eps {
			return false
		}
	}
	return true
}

func TestUpPacksAndUnpacks(t *testing.T) {
	cases := [][3]float32{{0, 1, 0}, {0.3, 0.9, 0.1}, {-0.7, 0.4, 0.5}, {0.99, 0.1, -0.05}, {0, 0.06, -1}, {-0.2, 0.3, -0.9}}
	for _, c := range cases {
		n := ClampUp(c)
		got := UnpackUp(PackUp(c))
		// 12 bits a side: well under 0.1° anywhere.
		if d := math.Acos(math.Min(1, float64(n[0]*got[0]+n[1]*got[1]+n[2]*got[2]))) * 180 / math.Pi; d > 0.1 {
			t.Errorf("%v -> %v: %.3f° off", n, got, d)
		}
	}
	if w := PackUp([3]float32{}); !near3(UnpackUp(w), [3]float32{0, 1, 0}, 1e-3) {
		t.Errorf("a zero axis should be +Y, got %v", UnpackUp(w))
	}
	// Below the horizon clamps to 0.05 up.
	if u := UnpackUp(PackUp([3]float32{1, -1, 0})); u[1] < 0.04 || u[1] > 0.06 || u[0] < 0.99 {
		t.Errorf("a downward axis should clamp just above the horizon, got %v", u)
	}
	// The reserved byte stays clear.
	if w := PackUp([3]float32{0.5, 0.5, 0.5}); w>>24 != 0 {
		t.Errorf("reserved bits set: %#x", w)
	}
}

func TestUpRotateStandsAClumpOnItsAxis(t *testing.T) {
	for _, c := range [][3]float32{{0, 1, 0}, {0.3, 0.9, 0.1}, {-0.6, 0.5, 0.62}, {0.9, 0.2, 0.3}} {
		n := ClampUp(c)
		if got := UpRotate(n, [3]float32{0, 1, 0}); !near3(got, n, 1e-5) {
			t.Errorf("+Y onto %v gave %v", n, got)
		}
		// Orthonormal: lengths and right angles are kept.
		x, z := UpRotate(n, [3]float32{1, 0, 0}), UpRotate(n, [3]float32{0, 0, 1})
		dot := func(a, b [3]float32) float64 { return float64(a[0]*b[0] + a[1]*b[1] + a[2]*b[2]) }
		if math.Abs(dot(x, x)-1) > 1e-5 || math.Abs(dot(z, z)-1) > 1e-5 || math.Abs(dot(x, z)) > 1e-5 ||
			math.Abs(dot(x, n)) > 1e-5 || math.Abs(dot(z, n)) > 1e-5 {
			t.Errorf("not a rotation for %v: x %v z %v", n, x, z)
		}
		// Right-handed: x × y = z survives.
		cx := [3]float32{x[1]*n[2] - x[2]*n[1], x[2]*n[0] - x[0]*n[2], x[0]*n[1] - x[1]*n[0]}
		if !near3(cx, z, 1e-5) {
			t.Errorf("reflection for %v", n)
		}
	}
	// Level ground is the upright clump exactly.
	if got := UpRotate([3]float32{0, 1, 0}, [3]float32{0.3, 0.7, -0.2}); !near3(got, [3]float32{0.3, 0.7, -0.2}, 1e-7) {
		t.Errorf("identity broken: %v", got)
	}
	// A twig lying along +X on a slope rising toward +X lies up the slope.
	n := ClampUp([3]float32{-0.5, 1, 0}) // normal tilted back: ground rises to +X
	tip := UpRotate(n, [3]float32{1, 0, 0})
	if tip[1] <= 0 || math.Abs(float64(tip[0]*n[0]+tip[1]*n[1]+tip[2]*n[2])) > 1e-5 {
		t.Errorf("a level twig should follow the slope up: %v on %v", tip, n)
	}
}

// goldenInstances is the instance set the pre-P3 golden was taken from.
func goldenInstances() []Instance {
	var ins []Instance
	for i := 0; i < 200; i++ {
		fx := float32((i*37)%101) / 101
		fz := float32((i*53)%97) / 97
		ins = append(ins, Instance{Kind: i % 3, Pos: [3]float32{10 + 40*fx, 3 + float32(i%7)*0.3, -20 + 40*fz},
			Yaw: float32(i) * 0.37, Scale: 0.5 + float32(i%5)*0.25, Tint: [3]float32{1, 0.9 + float32(i%3)*0.05, 0.8}})
	}
	return ins
}

func TestUprightFieldIsByteIdenticalToVersion1(t *testing.T) {
	spec := FieldSpec{Kit: "generated/x/grass.fgk", Min: [2]float32{10, -20}, Max: [2]float32{50, 20}, Cell: 16, Kinds: 3}
	f, err := BuildField(spec, goldenInstances())
	if err != nil {
		t.Fatal(err)
	}
	if f.Oriented || f.Words() != 3 {
		t.Fatalf("an upright field came out oriented")
	}
	b, err := EncodeField(f)
	if err != nil {
		t.Fatal(err)
	}
	// Taken from internal/grassfield at HEAD (before ground cover) on the
	// same instances.
	const want = "2769 84d0f7ecae0069b791dcd725f1740fc1c6f5c608accf7e782f394f09264b4dba"
	if got := fmt.Sprintf("%d %x", len(b), sha256.Sum256(b)); got != want {
		t.Fatalf("upright field bytes changed:\n got %s\nwant %s", got, want)
	}
	// And it reads back as an upright version-1 field.
	g, err := DecodeField(b)
	if err != nil || g.Oriented || g.Count() != 200 {
		t.Fatalf("read back: oriented %v count %d err %v", g != nil && g.Oriented, g.Count(), err)
	}
}

func TestOrientedFieldRoundTrips(t *testing.T) {
	ins := goldenInstances()
	for i := range ins {
		ins[i].Up = ClampUp([3]float32{float32(i%5) * 0.1, 1, float32(i%3) * -0.15})
	}
	spec := FieldSpec{Kit: "k.fgk", Min: [2]float32{10, -20}, Max: [2]float32{50, 20}, Cell: 16, Kinds: 3, Oriented: true}
	f, err := BuildField(spec, ins)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Oriented || len(f.Data) != 4*len(ins) {
		t.Fatalf("oriented %v, %d words for %d clumps", f.Oriented, len(f.Data), len(ins))
	}
	b, err := EncodeField(f)
	if err != nil {
		t.Fatal(err)
	}
	if v := uint32(b[4]) | uint32(b[5])<<8; v != FieldVersionOriented {
		t.Fatalf("version %d", v)
	}
	g, err := DecodeField(b)
	if err != nil {
		t.Fatal(err)
	}
	if !g.Oriented || g.Count() != len(ins) {
		t.Fatalf("decoded oriented %v count %d", g.Oriented, g.Count())
	}
	// Every instance keeps its axis (matched to the nearest input).
	for _, in := range g.Instances() {
		best, bd := -1, math.Inf(1)
		for i, src := range ins {
			if d := math.Hypot(float64(in.Pos[0]-src.Pos[0]), float64(in.Pos[2]-src.Pos[2])); d < bd {
				best, bd = i, d
			}
		}
		src := ins[best]
		if bd > 0.01 || !near3(in.Up, src.Up, 2e-3) || in.Kind != src.Kind {
			t.Fatalf("instance at %v (kind %d, axis %v) vs input %v (kind %d, axis %v)", in.Pos, in.Kind, in.Up, src.Pos, src.Kind, src.Up)
		}
	}
	// A truncated oriented file is an error, not a panic.
	if _, err := DecodeField(b[:len(b)-3]); err == nil {
		t.Fatal("truncated oriented field decoded")
	}
	// An instance with an axis makes the field oriented even when the spec
	// did not ask.
	spec.Oriented = false
	if f2, _ := BuildField(spec, ins[:3]); !f2.Oriented {
		t.Fatal("an axis did not make the field oriented")
	}
}

func TestCardMeshAndFlagsThroughTheKit(t *testing.T) {
	m := CardMesh(1, 0, 2)
	if m.VertexCount() != 9 || m.Triangles() != 8 {
		t.Fatalf("card: %d vertices %d triangles", m.VertexCount(), m.Triangles())
	}
	for v := 0; v < m.VertexCount(); v++ {
		p := m.Vertices[v*VertexFloats:]
		if p[0] < -0.5 || p[0] > 0.5 || p[2] < -0.5 || p[2] > 0.5 || p[1] < 0 || p[1] > CardCup+1e-6 {
			t.Fatalf("vertex %d at %v", v, p[:3])
		}
		if p[6] < 0.5-1e-6 || p[6] > 1+1e-6 || p[7] < -1e-6 || p[7] > 0.5+1e-6 {
			t.Fatalf("vertex %d UV %v outside tile (1, 0) of 2", v, p[6:8])
		}
		if p[10] != 0 || p[11] != 0 {
			t.Fatalf("a card sways: bend %g flutter %g", p[10], p[11])
		}
	}
	// Every triangle faces up.
	for tr := 0; tr < m.Triangles(); tr++ {
		at := func(q int) [3]float32 {
			i := m.Indices[3*tr+q] * VertexFloats
			return [3]float32{m.Vertices[i], m.Vertices[i+1], m.Vertices[i+2]}
		}
		a, b, c := at(0), at(1), at(2)
		u := [3]float32{b[0] - a[0], b[1] - a[1], b[2] - a[2]}
		w := [3]float32{c[0] - a[0], c[1] - a[1], c[2] - a[2]}
		if ny := u[2]*w[0] - u[0]*w[2]; ny <= 0 {
			t.Fatalf("triangle %d faces down", tr)
		}
	}
	k := &Kit{Kinds: []Kind{
		{Name: "litter", Texture: "t_cov", Height: 0.05, Radius: 0.7, Flags: KindOriented | KindNoWind | KindCard, LODs: []Mesh{m}},
		{Name: "twig", Texture: "b", Height: 0.1, Radius: 0.4, Flags: KindOriented | KindNoWind | KindCasts, LODs: []Mesh{m}},
	}}
	data, err := EncodeKit(k)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeKit(data)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Kinds[0].Card() || !got.Kinds[0].Oriented() || got.Kinds[0].Casts() ||
		got.Kinds[1].Card() || !got.Kinds[1].Oriented() || !got.Kinds[1].Casts() || got.Kinds[1].Flags&KindNoWind == 0 {
		t.Fatalf("flags lost: %#x %#x", got.Kinds[0].Flags, got.Kinds[1].Flags)
	}
}
