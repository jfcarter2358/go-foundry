package levelio

// beachhead_level_test.go — the shipped beachhead level stays loadable, and the
// three things about it that a hand edit would quietly break stay true.
//
// It is the level that leans hardest on geometry the engine derives rather than
// stores: the bunkers' outer faces are portal risers between open ground and
// the roof slab, the walkway's thickness is a skirt, and the terrain's
// continuity is nothing but neighbouring sectors agreeing about a shared
// point's height. None of the three is visible in the YAML as a thing that
// could be wrong — which is exactly why they are asserted here.
//
// Regenerate the level with `python3 resources/levels/beachhead_gen.py
// resources/levels/beachhead.yaml`; the checked-in YAML is its output.

import (
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/biome"
	"foundry/internal/gfx"
	"foundry/internal/testenv"
	"foundry/internal/world"
)

func loadBeachhead(t *testing.T) world.Level {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	if gfx.Textures == nil {
		gfx.Textures = map[string]uint32{}
	}
	if gfx.TextureNames == nil {
		gfx.TextureNames = map[string]string{}
	}
	for _, n := range []string{"", "default", "stone_floor", "concrete", "dirt",
		"rock", "grass", "sand", "water", "moss"} {
		gfx.Textures[n] = 1
		gfx.TextureNames[n] = n
	}
	lvl, err := LoadLevel(filepath.Join(testenv.RepoRoot(t), "resources", "levels", "beachhead.yaml"))
	if err != nil {
		t.Fatalf("load beachhead.yaml: %v", err)
	}
	if err := world.RebuildIndexes(&lvl); err != nil {
		t.Fatalf("indexes: %v", err)
	}
	return lvl
}

func TestBeachheadLoads(t *testing.T) {
	lvl := loadBeachhead(t)
	if len(lvl.Chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(lvl.Chunks))
	}
	ch := &lvl.Chunks[0]
	for _, id := range []string{
		"w_room_w", "w_room_e", "w_ramp", "w_door_s", "w_door_gate", "w_roof_w",
		"e_room_w", "e_ramp", "e_roof_e",
		"gate_s", "gate_m", "gate_n", "walkway",
	} {
		if sectorNamed(ch, id) == nil {
			t.Errorf("sector %q is missing", id)
		}
	}
	if lvl.Spawn == nil {
		t.Error("the level has no spawn point")
	}
	if len(ch.BiomeSectors) == 0 {
		t.Error("the level lost its biome paint")
	}
}

// The terrain is one height field sampled per vertex, and that is the only
// thing keeping neighbouring cells from tearing. Two open-ground sectors
// meeting at a point must put their floor at the same height there.
func TestBeachheadTerrainHasNoSeams(t *testing.T) {
	lvl := loadBeachhead(t)
	ch := &lvl.Chunks[0]

	type seen struct {
		y   float32
		sid string
	}
	at := map[int]seen{}
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		if !strings.HasPrefix(s.ID, "t_") {
			continue // only open ground shares the field
		}
		for vi, pi := range s.PointIndices {
			y := s.Floor.VertexHeight(vi)
			if prev, ok := at[pi]; ok {
				if d := prev.y - y; d > 1e-3 || d < -1e-3 {
					t.Fatalf("point %d: %s puts the floor at %g, %s at %g",
						pi, prev.sid, prev.y, s.ID, y)
				}
				continue
			}
			at[pi] = seen{y, s.ID}
		}
	}
}

// A bunker's outer face is not authored. It is the riser the engine draws
// between the ground outside and the roof slab four units above it, which only
// happens if the two are portal-linked along that boundary — so a roof whose
// outer edges came back as walls is a block with no outside.
func TestBunkerFacesAreRisersAgainstOpenGround(t *testing.T) {
	lvl := loadBeachhead(t)
	ch := &lvl.Chunks[0]

	for _, roofID := range []string{"w_roof_w", "e_roof_w"} {
		roof := sectorNamed(ch, roofID)
		if roof == nil {
			t.Fatalf("sector %q is missing", roofID)
		}
		ground := 0
		for _, p := range roof.Portals {
			if strings.HasPrefix(p.ToSector, "t_") {
				ground++
			}
		}
		if ground == 0 {
			t.Errorf("%s meets no open ground: its outer face will not be drawn", roofID)
		}
	}
}

// Every doorway is its own sector through the thickness of a wall, so it has to
// open at BOTH ends — one portal is a cupboard.
func TestDoorwaysOpenAtBothEnds(t *testing.T) {
	lvl := loadBeachhead(t)
	ch := &lvl.Chunks[0]

	for _, id := range []string{"w_door_s", "w_door_mid", "w_door_gate", "w_door_n",
		"e_door_s", "e_door_mid", "e_door_gate", "e_door_n"} {
		s := sectorNamed(ch, id)
		if s == nil {
			t.Fatalf("sector %q is missing", id)
		}
		if len(s.Portals) < 2 {
			t.Errorf("%s has %d portal(s); a doorway needs one at each end", id, len(s.Portals))
		}
	}
}

// The rooms are what give the walls their inner face. An edge of a room that
// came back as a portal is a wall that dissolved into whatever was next to it,
// which for this level means a room open to the inside of its own wall.
func TestRoomsKeepTheirWalls(t *testing.T) {
	lvl := loadBeachhead(t)
	ch := &lvl.Chunks[0]

	for _, id := range []string{"w_room_w", "w_room_e", "e_room_w", "e_room_e"} {
		s := sectorNamed(ch, id)
		if s == nil {
			t.Fatalf("sector %q is missing", id)
		}
		if len(s.Walls) == 0 {
			t.Errorf("%s has no walls left", id)
		}
	}
}

// The sea's surface height is not authored. Water is authored as a lake — the
// plane sits `water_depth` below the structural floor sampled at the polygon's
// AABB centre — so the level's depth is a number the generator READ OFF the
// terrain at one point, by reproducing the floor's own triangle-fan
// interpolation. If either side of that drifts, the sea silently moves and the
// waterline lands somewhere up the beach or out past the surf.
func TestSeaSurfaceSitsAtZero(t *testing.T) {
	lvl := loadBeachhead(t)
	ch := &lvl.Chunks[0]

	var sea *world.BiomeSector
	for i := range ch.BiomeSectors {
		if ch.BiomeSectors[i].ID == "sea" {
			sea = &ch.BiomeSectors[i]
		}
	}
	if sea == nil {
		t.Fatal("the level has no sea")
	}
	_, floorY := biome.BiomeWaterCentroidAndFloor(&lvl, ch, sea)
	surface := floorY - sea.Brush.WaterDepth
	if surface > 0.02 || surface < -0.02 {
		t.Errorf("the sea surface is at y=%.3f, want 0 (floor %.3f - depth %.3f)",
			surface, floorY, sea.Brush.WaterDepth)
	}
}
