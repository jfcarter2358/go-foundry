package levelio

// canyon_level_test.go — the shipped canyon level stays loadable and walkable.
//
// It is the level that exercises the most of the sector model at once: two
// storeys overlapping in one chunk (layers), sloped ramps, pinned walls holding
// a building's shell together inside an open basin, and a plateau that abuts
// the canyon in plan while sitting eighteen units above it. Each of those has
// its own unit test elsewhere; this one checks they still combine.

import (
	"foundry/internal/gfx"
	"foundry/internal/testenv"
	"foundry/internal/world"
	"path/filepath"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func loadCanyon(t *testing.T) world.Level {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	// Textures are declared resident so EnsureTextureLoaded returns before it
	// tries to decode a PNG without a GL context.
	if gfx.Textures == nil {
		gfx.Textures = map[string]uint32{}
	}
	if gfx.TextureNames == nil {
		gfx.TextureNames = map[string]string{}
	}
	for _, n := range []string{"", "default", "stone_floor", "concrete", "dirt", "rock", "grass"} {
		gfx.Textures[n] = 1
		gfx.TextureNames[n] = n
	}
	lvl, err := LoadLevel(filepath.Join(testenv.RepoRoot(t), "resources", "levels", "canyon.yaml"))
	if err != nil {
		t.Fatalf("load canyon.yaml: %v", err)
	}
	if err := world.RebuildIndexes(&lvl); err != nil {
		t.Fatalf("indexes: %v", err)
	}
	return lvl
}

func TestCanyonLevelLoads(t *testing.T) {
	lvl := loadCanyon(t)
	ch := &lvl.Chunks[0]
	if got := ch.LayerCount(); got != 2 {
		t.Errorf("LayerCount = %d, want 2 (ground and upper)", got)
	}
	for _, id := range []string{"hall", "stairs", "upper", "path_0", "path_5", "rim_e"} {
		if sectorNamed(ch, id) == nil {
			t.Errorf("sector %q is missing", id)
		}
	}
}

// A portal joins two spaces a body can pass between. Two sectors sharing an
// edge but stacked one above the other — the ground floor and the storey over
// it, the canyon and the rim beside it — must NOT be joined. This is what
// InferPortals' height check is for; without it a downstairs doorway opened
// onto an upstairs room.
func TestCanyonPortalsNeverCrossStoreys(t *testing.T) {
	lvl := loadCanyon(t)
	ch := &lvl.Chunks[0]
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		for pi := range s.Portals {
			to := sectorNamed(ch, s.Portals[pi].ToSector)
			if to == nil {
				t.Errorf("%s: portal %q points at unknown sector %q",
					s.ID, s.Portals[pi].ID, s.Portals[pi].ToSector)
				continue
			}
			if to.Floor.Height >= s.Ceiling.Height || s.Floor.Height >= to.Ceiling.Height {
				t.Errorf("%s (%.1f..%.1f) has a portal to %s (%.1f..%.1f): different storeys",
					s.ID, s.Floor.Height, s.Ceiling.Height,
					to.ID, to.Floor.Height, to.Ceiling.Height)
			}
		}
	}
}

// The building's shell must survive InferPortals. Its perimeter is shared with
// the basin cells around it, and without `pinned` every one of those edges
// would be promoted to a portal — dissolving the building into the canyon.
func TestCanyonBuildingKeepsItsWalls(t *testing.T) {
	lvl := loadCanyon(t)
	ch := &lvl.Chunks[0]
	for _, id := range []string{"hall", "hall_s", "hall_n", "upper", "upper_s", "upper_n"} {
		s := sectorNamed(ch, id)
		if s == nil {
			t.Fatalf("sector %q is missing", id)
		}
		if len(s.Walls) == 0 {
			t.Errorf("%s has no walls left — its shell was promoted to portals", id)
		}
	}
}

// Two storeys of one building occupy the same ground, and a height decides
// which one a position is in.
func TestCanyonStoreysResolveByHeight(t *testing.T) {
	lvl := loadCanyon(t)
	ch := &lvl.Chunks[0]
	xz := mgl32.Vec2{-6, 0} // inside the hall, and inside the room above it
	if s := world.FindSectorAt(ch, xz, 0.9); s == nil || s.ID != "hall" {
		t.Errorf("at y=0.9 found %v, want the ground-floor hall", s)
	}
	if s := world.FindSectorAt(ch, xz, 5.1); s == nil || s.ID != "upper" {
		t.Errorf("at y=5.1 found %v, want the upper storey", s)
	}
}

// Nothing in the level may be coplanar: a ceiling and a floor drawn at the same
// height over the same ground z-fight, and no draw order settles it.
func TestCanyonHasNoCoplanarSurfaces(t *testing.T) {
	lvl := loadCanyon(t)
	for _, p := range world.CoplanarStackedSurfaces(&lvl.Chunks[0]) {
		t.Errorf("coplanar: %s ceiling / %s floor at y=%.2f", p.Lower.ID, p.Upper.ID, p.Height)
	}
}

func sectorNamed(ch *world.Chunk, id string) *world.Sector {
	for i := range ch.Sectors {
		if ch.Sectors[i].ID == id {
			return &ch.Sectors[i]
		}
	}
	return nil
}

// The slab between the two storeys must actually be closed. Without a skirt the
// 0.2 band between the hall's ceiling and the storey above is walled by
// neither, and the building has a slit through it at the floor line.
func TestCanyonUpperStoreyHasASkirt(t *testing.T) {
	lvl := loadCanyon(t)
	ch := &lvl.Chunks[0]
	for _, id := range []string{"upper", "upper_s", "upper_n"} {
		s := sectorNamed(ch, id)
		if s == nil {
			t.Fatalf("sector %q is missing", id)
		}
		if s.Floor.Skirt != world.SkirtToCeiling {
			t.Errorf("%s has skirt mode %q, want %q", id, s.Floor.Skirt, world.SkirtToCeiling)
		}
		if !world.SkirtDrawable(s) {
			t.Errorf("%s declares a skirt but produced no geometry", id)
		}
	}
	// And the ground floor, which has nothing under it, must not grow one.
	if s := sectorNamed(ch, "hall"); s != nil && world.SkirtDrawable(s) {
		t.Error("the ground floor grew a skirt with nothing below it")
	}
}

// The building must be roofed everywhere, including over the stairwell.
//
// The shaft is open between the two storeys because no upper-storey sector
// covers that strip — not because the stairs sector is open-topped. Leaving its
// ceiling disabled roofed the building everywhere except over the stairwell,
// where you looked straight up out of it.
func TestCanyonBuildingIsRoofed(t *testing.T) {
	lvl := loadCanyon(t)
	ch := &lvl.Chunks[0]
	open := 0
	for lx := float32(-11.5); lx <= 11.5; lx += 0.5 {
		for lz := float32(-11.5); lz <= 11.5; lz += 0.5 {
			xz := mgl32.Vec2{lx, lz}
			roofed := false
			for si := range ch.Sectors {
				s := &ch.Sectors[si]
				if s.Ceiling.Enabled && world.PointInSector(ch, s, xz) {
					roofed = true
					break
				}
			}
			if !roofed {
				if open == 0 {
					t.Errorf("local(%.1f,%.1f) inside the building has no ceiling over it", lx, lz)
				}
				open++
			}
		}
	}
	if open > 0 {
		t.Errorf("%d sampled points inside the building are open to the sky", open)
	}
}

// ...and the shaft must still be open BETWEEN the storeys, or the stairs lead
// into a lid rather than onto the floor above.
func TestCanyonStairwellIsOpenBetweenStoreys(t *testing.T) {
	lvl := loadCanyon(t)
	ch := &lvl.Chunks[0]
	// Mid-shaft, at the height of the upper storey's floor.
	xz := mgl32.Vec2{8, 0}
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		if s.Layer == 0 || !world.PointInSector(ch, s, xz) {
			continue
		}
		t.Errorf("%q covers the middle of the stairwell at floor %.2f — the shaft is capped",
			s.ID, s.Floor.Height)
	}
	// And the sector there is the stairs, all the way up to the roof.
	if s := world.FindSectorAt(ch, xz, 5.0); s == nil || s.ID != "stairs" {
		t.Errorf("at shaft height 5.0 found %v, want the stairs", s)
	}
}
