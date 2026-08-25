package levelio

// level_blend_test.go — blend_texture/blend_mask survive the YAML round trip
// on a wall and a floor, and stay unset (blending off) when never authored.

import (
	"foundry/internal/geom"
	"foundry/internal/testenv"
	"foundry/internal/world"
	"testing"
)

func blendRawChunk() world.RawChunk {
	pts := map[string]geom.Point{
		"A": {X: 0, Y: 0},
		"B": {X: 10, Y: 0},
		"C": {X: 10, Y: 10},
		"D": {X: 0, Y: 10},
	}
	ring := []string{"A", "B", "C", "D"}
	var edges []world.RawEdge
	for i := range ring {
		e := world.RawEdge{
			From:      ring[i],
			To:        ring[(i+1)%len(ring)],
			Kind:      "wall",
			Texture:   "concrete",
			Collision: true,
		}
		if i == 0 {
			e.BlendTexture = "dirt"
			e.BlendMask = "mask"
		}
		edges = append(edges, e)
	}
	return world.RawChunk{
		ID:     "chunk",
		Points: pts,
		Sectors: []world.RawSector{{
			ID: "room",
			Floor: world.RawFloor{
				Enabled: true, Height: 0, Texture: "concrete",
				BlendTexture: "grass", BlendMask: "mask",
			},
			Ceiling: world.RawCeiling{Enabled: true, Height: 4, Texture: "concrete"},
			Edges:   edges,
		}},
	}
}

func TestBlendTextureAndMaskSurviveSaveLoadRoundTrip(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	first, _, err := loadRawChunk(blendRawChunk())
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	s := &first.Sectors[0]

	if s.Floor.BlendTexture != "grass" || s.Floor.BlendMask != "mask" {
		t.Fatalf("floor blend fields did not load: got BlendTexture=%q BlendMask=%q",
			s.Floor.BlendTexture, s.Floor.BlendMask)
	}
	if s.Ceiling.BlendTexture != "" || s.Ceiling.BlendMask != "" {
		t.Fatalf("ceiling blend fields should be unset, got BlendTexture=%q BlendMask=%q",
			s.Ceiling.BlendTexture, s.Ceiling.BlendMask)
	}
	if len(s.Walls) == 0 || s.Walls[0].BlendTexture != "dirt" || s.Walls[0].BlendMask != "mask" {
		t.Fatalf("wall 0 blend fields did not load: %+v", s.Walls)
	}
	for i := 1; i < len(s.Walls); i++ {
		if s.Walls[i].BlendTexture != "" || s.Walls[i].BlendMask != "" {
			t.Fatalf("wall %d should have no blend fields, got %+v", i, s.Walls[i])
		}
	}

	raw, err := chunkToRaw(&first)
	if err != nil {
		t.Fatalf("chunkToRaw: %v", err)
	}
	second, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	s2 := &second.Sectors[0]

	if s2.Floor.BlendTexture != "grass" || s2.Floor.BlendMask != "mask" {
		t.Errorf("floor blend fields did not survive the round trip: got BlendTexture=%q BlendMask=%q",
			s2.Floor.BlendTexture, s2.Floor.BlendMask)
	}
	if len(s2.Walls) == 0 || s2.Walls[0].BlendTexture != "dirt" || s2.Walls[0].BlendMask != "mask" {
		t.Errorf("wall 0 blend fields did not survive the round trip: %+v", s2.Walls)
	}
}
