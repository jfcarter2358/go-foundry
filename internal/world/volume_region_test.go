package world

import (
	"math"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

func TestRegionBlockRoundTripsThroughYAML(t *testing.T) {
	src := `id: canyon_west
position: {x: -300, y: 40, z: 0}
half: {x: 300, y: 50, z: 300}
region:
  environment: desert_canyon
  generator: environments/my_canyon.fgen
  priority: 2
  transition: 200
  anchor: [10, -20]
  footprint: [[-300, -300], [300, -300], [300, 300], [-300, 300]]
`
	var raw RawVolume
	if err := yaml.UnmarshalStrict([]byte(src), &raw); err != nil {
		t.Fatal(err)
	}
	v, err := VolumeFromRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	g := v.Region
	if g == nil || g.Environment != "desert_canyon" || g.Generator != "environments/my_canyon.fgen" ||
		g.Priority != 2 || g.Transition != 200 || g.Anchor == nil || *g.Anchor != [2]float32{10, -20} || len(g.Footprint) != 4 {
		t.Fatalf("region = %+v", g)
	}
	if !strings.Contains(volumeRoleSummary(&v), "region desert_canyon") {
		t.Errorf("role summary %q does not say region", volumeRoleSummary(&v))
	}
	out, err := yaml.Marshal(VolumeToRaw(&v))
	if err != nil {
		t.Fatal(err)
	}
	var again RawVolume
	if err := yaml.UnmarshalStrict(out, &again); err != nil {
		t.Fatalf("re-read: %v\n%s", err, out)
	}
	v2, _ := VolumeFromRaw(again)
	if v2.Region == nil || v2.Region.Priority != 2 || len(v2.Region.Footprint) != 4 || *v2.Region.Anchor != *g.Anchor {
		t.Fatalf("round trip lost the region:\n%s", out)
	}
	// The raw copy is the volume's own: editing one footprint leaves the other.
	r := VolumeToRaw(&v)
	r.Region.Footprint[0][0] = 99
	if v.Region.Footprint[0][0] == 99 {
		t.Error("VolumeToRaw shares the footprint")
	}
	if c := g.Clone(); &c.Footprint[0] == &g.Footprint[0] || c.Anchor == g.Anchor {
		t.Error("Clone shares storage")
	}

	// The default transition, and no key on a plain volume.
	var raw3 RawVolume
	if err := yaml.Unmarshal([]byte("id: r\nhalf: {x: 1, y: 1, z: 1}\nregion: {environment: savannah, footprint: [[0,0],[1,0],[0,1]]}\n"), &raw3); err != nil {
		t.Fatal(err)
	}
	v3, _ := VolumeFromRaw(raw3)
	if v3.Region.Transition != DefaultRegionTransition || v3.Region.Anchor != nil {
		t.Errorf("defaults: %+v", v3.Region)
	}
	plain := Volume{ID: "trigger", Enabled: true, HalfX: 1, HalfY: 1, HalfZ: 1}
	if b, _ := yaml.Marshal(VolumeToRaw(&plain)); strings.Contains(string(b), "region") {
		t.Error("an ordinary volume wrote a region block")
	}
}

func near2(a, b [2]float64) bool { return math.Abs(a[0]-b[0]) < 1e-3 && math.Abs(a[1]-b[1]) < 1e-3 }

// Fitting recentres the footprint and sizes the box without moving the
// region in the world, rotated or not.
func TestFitRegionKeepsTheWorldFootprint(t *testing.T) {
	for _, rot := range []mgl32.Vec3{{}, {0, 0.7, 0}} {
		v := NewVolume("r", mgl32.Vec3{10, 5, -20})
		v.Rotation = rot
		v.Region = &VolumeRegion{Environment: "savannah", Transition: 150,
			Footprint: [][2]float32{{0, 0}, {100, 0}, {100, 40}, {40, 40}, {40, 100}, {0, 100}}}
		a := [2]float32{20, 20}
		v.Region.Anchor = &a
		before := v.RegionWorldFootprint()
		anchorBefore, _ := v.RegionWorldAnchor()
		if !v.FitRegion() {
			t.Fatal("FitRegion changed nothing")
		}
		after := v.RegionWorldFootprint()
		for i := range before {
			if !near2(before[i], after[i]) {
				t.Fatalf("rot %v: point %d moved %v -> %v", rot, i, before[i], after[i])
			}
		}
		if anchorAfter, ok := v.RegionWorldAnchor(); !ok || !near2(anchorBefore, anchorAfter) {
			t.Errorf("anchor moved %v -> %v", anchorBefore, anchorAfter)
		}
		if v.HalfX != 50 || v.HalfZ != 50 || v.Shape != VolumeBox {
			t.Errorf("box %v x %v shape %v, want 50 x 50 box", v.HalfX, v.HalfZ, v.Shape)
		}
		if v.Region.Footprint[0] != [2]float32{-50, -50} {
			t.Errorf("footprint not recentred: %v", v.Region.Footprint)
		}
		if v.FitRegion() {
			t.Error("fitting a fitted region changed it")
		}
	}
}

func TestRegionCageAndHelpers(t *testing.T) {
	v := NewVolume("r", mgl32.Vec3{})
	v.HalfX, v.HalfZ = 30, 20
	v.Region = NewVolumeRegion(&v, "desert_canyon")
	if len(v.Region.Footprint) != 4 || v.Region.Footprint[2] != [2]float32{30, 20} {
		t.Fatalf("default footprint %v, want the box's rectangle", v.Region.Footprint)
	}
	verts, idx := v.CageMesh()
	if len(verts) != 8 || len(idx) != 4*6 {
		t.Errorf("cage %d verts %d indices, want 8 and 24", len(verts), len(idx))
	}
	if a := RegionFootprintArea(v.Region.Footprint); a != 2400 {
		t.Errorf("area %v, want 2400", a)
	}
	w := v.RegionLocalToWorld([2]float32{3, 4})
	if got := v.RegionWorldToLocal(w); got != [2]float32{3, 4} {
		t.Errorf("local/world round trip %v", got)
	}
	lvl := &Level{Volumes: []Volume{v, NewVolume("plain", mgl32.Vec3{})}}
	if rs := RegionVolumes(lvl); len(rs) != 1 || rs[0].ID != "r" {
		t.Errorf("RegionVolumes = %v", rs)
	}
}
