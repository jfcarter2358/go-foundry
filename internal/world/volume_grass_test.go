package world

import (
	"testing"

	"gopkg.in/yaml.v2"
)

func TestVolumeGrassRoundTrip(t *testing.T) {
	src := `
id: gen.w.r_main_grass.grass_1_2
position: {x: 10, y: 3, z: -20}
half: {x: 64, y: 5, z: 64}
grass:
  file: generated/w/r_main_grass/grass_1_2.fgrass
  chunk: auto
`
	var r RawVolume
	if err := yaml.UnmarshalStrict([]byte(src), &r); err != nil {
		t.Fatal(err)
	}
	v, err := VolumeFromRaw(r)
	if err != nil {
		t.Fatal(err)
	}
	if !v.IsGrass() || v.Grass.File != "generated/w/r_main_grass/grass_1_2.fgrass" || v.Grass.Chunk != GrassChunkAuto {
		t.Fatalf("grass %+v", v.Grass)
	}
	back := VolumeToRaw(&v)
	if back.Grass == nil || *back.Grass != *r.Grass {
		t.Fatalf("round trip %+v", back.Grass)
	}
	plain := Volume{ID: "x"}
	if plain.IsGrass() || VolumeToRaw(&plain).Grass != nil {
		t.Fatal("a plain volume is not a grass field")
	}
	if got := volumeRoleSummary(&v); got != "grass generated/w/r_main_grass/grass_1_2.fgrass" {
		t.Fatalf("role summary %q", got)
	}
}

func TestGrassChunk(t *testing.T) {
	lvl := &Level{Volumes: []Volume{
		{ID: "a", Grass: &VolumeGrass{File: "a.fgrass"}},
		{ID: "b", Grass: &VolumeGrass{File: "b.fgrass", Chunk: "missing"}},
		{ID: "c"},
	}}
	if n := len(GrassVolumes(lvl)); n != 2 {
		t.Fatalf("%d grass volumes", n)
	}
	if lvl.Volumes[0].GrassChunk(lvl) != nil || lvl.Volumes[1].GrassChunk(lvl) != nil {
		t.Fatal("no chunk: always in")
	}
}
