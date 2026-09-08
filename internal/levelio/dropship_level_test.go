package levelio

// dropship_level_test.go — the shipped dropship, and the three things about it
// that a hand edit would quietly break.
//
// It is the only content in the tree that leans on a *frame portal*, and every
// one of its failure modes is silent. A door that no longer seals lets the
// player walk out of a ship in flight. A doorway whose two halves drifted into
// the same place drops them into a sector neither space contains, with no floor
// under them. A sector renamed in the chunk file leaves the script's `door`
// param resolving to null, which reads exactly like a door that has not been
// wired up yet. None of the three is visible in the YAML as a thing that could
// be wrong, which is why they are asserted here.
//
// The hull and the canopy are generated: `go run ./cmd/genmodels`.

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/collision"
	"foundry/internal/gfx"
	"foundry/internal/script"
	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// chunkNamed and findSectorAnywhere are the two lookups these tests need that
// the level does not index by name for them.
func chunkNamed(lvl *world.Level, id string) *world.Chunk {
	for i := range lvl.Chunks {
		if lvl.Chunks[i].ID == id {
			return &lvl.Chunks[i]
		}
	}
	return nil
}

func findSectorAnywhere(lvl *world.Level, id string) *world.Sector {
	for i := range lvl.Chunks {
		if s := sectorNamed(&lvl.Chunks[i], id); s != nil {
			return s
		}
	}
	return nil
}

// leafHeight measures the door as drawn from one side: the vertical extent of
// the ceiling-gap geometry on that sector's portal into the doorway. Zero when
// the two ceilings agree, which is a doorway with no door across it.
func leafHeight(s *world.Sector, doorway string) float32 {
	if s == nil {
		return 0
	}
	for i := range s.Portals {
		p := &s.Portals[i]
		if p.ToSector != doorway || len(p.CeilGap.Vertices) == 0 {
			continue
		}
		lo, hi := float32(1e9), float32(-1e9)
		// A vertex record is five floats: x, y, z, u, v.
		for v := 0; v+4 < len(p.CeilGap.Vertices); v += 5 {
			y := p.CeilGap.Vertices[v+1]
			if y < lo {
				lo = y
			}
			if y > hi {
				hi = y
			}
		}
		return hi - lo
	}
	return 0
}

// dropshipDoorwayApproach is a spot in the docking alcove close enough to its
// mouth to be handed aboard: a quarter of a metre short of the level's own
// portal line, on the ship's centreline, on the bay's floor.
func dropshipDoorwayApproach(lvl *world.Level) mgl32.Vec3 {
	return mgl32.Vec3{0, 200.1, -1.75}
}

func loadDropshipDemo(t *testing.T) world.Level {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	if gfx.Textures == nil {
		gfx.Textures = map[string]uint32{}
	}
	if gfx.TextureNames == nil {
		gfx.TextureNames = map[string]string{}
	}
	for _, n := range []string{"", "default", "sand", "rock",
		"hull_plate", "hull_deck", "hull_door", "hull_glass"} {
		gfx.Textures[n] = 1
		gfx.TextureNames[n] = n
	}
	// The entity type, its chunk attachment and its script are read from disk
	// relative to the install root, which in a checkout is resources/.
	withResourcesRoot(t)

	lvl, err := LoadLevel(filepath.Join(testenv.RepoRoot(t), "resources", "levels", "dropship_demo.yaml"))
	if err != nil {
		t.Fatalf("load dropship_demo.yaml: %v", err)
	}
	if err := world.RebuildIndexes(&lvl); err != nil {
		t.Fatalf("indexes: %v", err)
	}
	return lvl
}

// The hold is not in the level file: it is materialised from the entity type's
// chunk attachment, bound to that entity's frame. If the definition or the
// chunk file goes missing the level still loads — with no ship interior at all.
func TestDropshipCarriesItsHold(t *testing.T) {
	lvl := loadDropshipDemo(t)

	hold := chunkNamed(&lvl, "dropship.hold")
	if hold == nil {
		t.Fatal("the dropship materialised no hold chunk")
	}
	if hold.FrameOwner != "dropship" {
		t.Errorf("hold's frame owner is %q, want the ship", hold.FrameOwner)
	}
	if lvl.Frames.Frame("dropship") == nil {
		t.Error("the ship owns no frame, so nothing rides it")
	}
	for _, id := range []string{"dropship.hold.bay", "dropship.hold.hatch", "dropship.hold.vest"} {
		if sectorNamed(hold, id) == nil {
			t.Errorf("sector %q is missing from the hold", id)
		}
	}
}

// The script writes the door by name through the params the definition binds.
// A sector renamed on either side of that is a door that never moves, reported
// as nothing at all.
func TestDropshipScriptParamsNameRealSectors(t *testing.T) {
	lvl := loadDropshipDemo(t)
	e := world.FindEntity(&lvl, "dropship")
	if e == nil {
		t.Fatal("no dropship in the level")
	}
	for _, name := range []string{"door", "bay"} {
		p, ok := e.ScriptParams[name]
		if !ok {
			t.Errorf("the type binds no %q param", name)
			continue
		}
		id, ok := p.Str()
		if !ok || id == "" {
			t.Errorf("param %q is bound to something that is not an id: %+v", name, p)
			continue
		}
		if findSectorAnywhere(&lvl, id) == nil {
			t.Errorf("param %q names sector %q, which no chunk has", name, id)
		}
	}
}

// The door is the doorway sector's ceiling, and it seals because
// ResolveCeilingEntry refuses a move into a sector whose ceiling is at or below
// the floor being left. That only holds while the shut height is at or below
// BOTH neighbours' floors — raise it by a centimetre and the shut door admits
// from the higher side, silently.
func TestShutDoorSealsAgainstBothNeighbours(t *testing.T) {
	lvl := loadDropshipDemo(t)
	hold := chunkNamed(&lvl, "dropship.hold")
	if hold == nil {
		t.Fatal("no hold")
	}
	hatch := sectorNamed(hold, "dropship.hold.hatch")
	bay := sectorNamed(hold, "dropship.hold.bay")
	vest := sectorNamed(hold, "dropship.hold.vest")
	if hatch == nil || bay == nil || vest == nil {
		t.Fatal("the hold is not the three sectors the script drives")
	}

	// The shut height the script defaults to. Stated here rather than read from
	// the script because the assertion is about the geometry admitting it.
	const shut = float32(0.0)
	if bay.Floor.Height < shut {
		t.Errorf("shut at %v, the bay's floor is %v: the door admits from the hold", shut, bay.Floor.Height)
	}
	if vest.Floor.Height < shut {
		t.Errorf("shut at %v, the vestibule's floor is %v: the door admits from outside", shut, vest.Floor.Height)
	}
	if hatch.Floor.Height >= shut {
		t.Errorf("the doorway's floor is %v, at or above the shut leaf at %v: nowhere for it to seat",
			hatch.Floor.Height, shut)
	}
	if !hatch.Ceiling.Enabled {
		t.Error("the doorway has no ceiling, so it has no door")
	}
	// And the leaf has to be drawn, which is the gap geometry either side of it.
	for _, s := range []*world.Sector{bay, vest} {
		found := false
		for i := range s.Portals {
			if s.Portals[i].ToSector == hatch.ID && s.Portals[i].DrawGap {
				found = true
			}
		}
		if !found {
			t.Errorf("%s has no gap-filling portal into the doorway: the leaf will not draw from that side", s.ID)
		}
	}
}

// The two halves of the frame portal have to be far enough apart, and the
// doorway narrow enough, that a crossing always lands inside the far sector.
// See the header of resources/levels/dropship_demo.yaml.
func TestBoardingCrossesOnceAndLandsInside(t *testing.T) {
	lvl := loadDropshipDemo(t)

	p := world.NewPlayer(dropshipDoorwayApproach(&lvl))
	lvl.Entities = append(lvl.Entities, *p)
	pl := world.FindEntity(&lvl, "player")
	pl.UpdateLocation(&lvl)

	c := collision.FindFramePortalCrossing(&lvl, pl)
	if c == nil {
		t.Fatal("standing in the bay's doorway found no way aboard")
	}
	if c.Sealed {
		t.Fatal("a level ship's doorway was sealed")
	}
	collision.CrossFramePortal(&lvl, pl, c, 0)
	if pl.Frame != "dropship" {
		t.Fatalf("frame after crossing: %q, want the ship", pl.Frame)
	}
	if pl.CurrentSector != "dropship.hold.vest" {
		t.Fatalf("landed in %q, want the vestibule — the two portal lines are too close together",
			pl.CurrentSector)
	}
	if back := collision.FindFramePortalCrossing(&lvl, pl); back != nil {
		t.Errorf("handed straight back out again: %+v", back)
	}

	// And out again, into the bay rather than into nothing.
	pl.Position = mgl32.Vec3{0, pl.Position.Y(), -3.3} // on the vestibule's lip
	pl.UpdateLocation(&lvl)
	out := collision.FindFramePortalCrossing(&lvl, pl)
	if out == nil {
		t.Fatal("standing on the ship's lip found no way off")
	}
	collision.CrossFramePortal(&lvl, pl, out, 0)
	if pl.Frame != "" {
		t.Fatalf("frame after stepping off: %q, want the world", pl.Frame)
	}
	if pl.CurrentSector != "bay_gate" {
		t.Errorf("stepped off into %q, want the docking alcove", pl.CurrentSector)
	}
	if back := collision.FindFramePortalCrossing(&lvl, pl); back != nil {
		t.Errorf("pulled straight back aboard: %+v", back)
	}
}

// The whole sequence, driven by the real script against the real level: walk
// in, the door shuts, the ship falls two hundred metres, the door opens again.
//
// Every layer this crosses has a silent failure mode — a param that resolves to
// null writes no ceiling, a phase that never advances leaves the ship in the
// bay, a ceiling write that does not rebuild the chunk leaves the leaf drawn
// where it was — and none of them stops the level loading. Running the thing is
// the only test that catches them.
func TestTheDropRunsEndToEnd(t *testing.T) {
	lvl := loadDropshipDemo(t)
	hold := chunkNamed(&lvl, "dropship.hold")
	hatch := sectorNamed(hold, "dropship.hold.hatch")
	ship := world.FindEntity(&lvl, "dropship")
	if hatch == nil || ship == nil {
		t.Fatal("the level is not the one the rest of this file describes")
	}
	startY := ship.Position.Y()

	// A passenger, standing in the hold: in the ship's frame, which is what
	// being aboard is, and past the door, which is what the script insists on.
	pl := world.NewPlayer(mgl32.Vec3{0, 0, 3.0})
	pl.Frame = "dropship"
	prevPlayer := world.PlayerEntity
	world.PlayerEntity = pl
	t.Cleanup(func() { world.PlayerEntity = prevPlayer })
	pl.UpdateLocation(&lvl)
	if pl.CurrentSector != "dropship.hold.bay" {
		t.Fatalf("the passenger is in %q, not the hold", pl.CurrentSector)
	}

	prevRT := *script.Scripts
	t.Cleanup(func() { *script.Scripts = prevRT })
	script.ScriptEventsReset()
	script.ScriptRuntimeLoad(&lvl)

	const dt = 1.0 / 60.0
	step := func(frames int) {
		for i := 0; i < frames; i++ {
			script.ScriptTick(&lvl, dt, true)
			// The two things the engine does either side of a tick, and both
			// matter here. The drain applies what the tick deferred — a ceiling
			// write rebuilds its sector and the chunk's gap geometry, which is
			// what actually draws the door. The frame refresh carries the hold
			// and everyone in it to wherever the ship has got to.
			script.DrainScriptWork(&lvl)
			world.RefreshFrames(&lvl, lvl.Frames)
		}
	}

	// Parked: the door is open and the ship has not moved. The default
	// launch_delay is two seconds, so half of one is still comfortably parked.
	step(30)
	if got := hatch.Ceiling.Height; got < 2.3 {
		t.Errorf("the door started closing before anyone asked: ceiling %v", got)
	}
	if ship.Position.Y() != startY {
		t.Errorf("the ship moved while parked: %v", ship.Position.Y())
	}

	// Past the delay, it shuts itself and goes.
	step(240) // four seconds: two waiting, then the leaf coming down at 1.6 m/s
	if got := hatch.Ceiling.Height; got > 0.01 {
		t.Fatalf("the door is still at %v after four seconds", got)
	}
	// Shut and DRAWN. The leaf is the gap geometry the bay's portal grows down
	// from its own ceiling, and it only exists if the write rebuilt the chunk —
	// a door that seals but does not draw is an invisible wall.
	if h := leafHeight(sectorNamed(hold, "dropship.hold.bay"), hatch.ID); h < 2.5 {
		t.Errorf("the shut leaf is %v tall on the hold's side, want the full 2.6", h)
	}
	if h := leafHeight(sectorNamed(hold, "dropship.hold.vest"), hatch.ID); h < 2.1 {
		t.Errorf("the shut leaf is %v tall on the vestibule's side, want the full 2.25", h)
	}
	if ship.Position.Y() >= startY {
		t.Fatalf("the door shut but the ship never let go: still at %v", ship.Position.Y())
	}

	// The descent is eased, so it takes longer than the flat 200/26; twenty
	// seconds is generous either way, and the assertion is that it ARRIVES —
	// exactly, because the position is written rather than integrated.
	step(1200)
	if got := ship.Position.Y(); got != startY-200 {
		t.Errorf("landed at %v, want %v", got, startY-200)
	}
	if got := ship.Position.X(); got != 0 {
		t.Errorf("the shake left the ship at x = %v rather than back on the centreline", got)
	}
	if got := hatch.Ceiling.Height; got < 2.3 {
		t.Errorf("down, but the door never reopened: ceiling %v", got)
	}
	if h := leafHeight(sectorNamed(hold, "dropship.hold.bay"), hatch.ID); h > 0.3 {
		t.Errorf("open, but the leaf is still %v tall across the doorway", h)
	}

	// And the passenger came too — they never moved in the ship's frame, which
	// is the whole point of a frame, but in the world they are two hundred
	// metres lower and standing on the surface's own ground.
	if pl.Position != (mgl32.Vec3{0, 0, 3.0}) {
		t.Errorf("the passenger slid to %v inside the hold", pl.Position)
	}
	if got := lvl.Frames.WorldPosition(pl).Y(); got != startY-200 {
		t.Errorf("the passenger rode down to %v, want %v", got, startY-200)
	}
	if n := script.ScriptStatsSnapshot().Errors; n != 0 {
		t.Errorf("%d script error(s) during the drop", n)
	}

	// And they can get off, onto the surface's ground rather than into the sky
	// the launch bay's alcove used to be. Nothing was authored twice for this:
	// the ship's doorway says `to_frame: world`, and which of the world's
	// sectors that means is settled by where the ship now is.
	pl.Position = mgl32.Vec3{0, 0.03, -3.3}
	pl.UpdateLocation(&lvl)
	off := collision.FindFramePortalCrossing(&lvl, pl)
	if off == nil {
		t.Fatal("no way off the ship after it landed")
	}
	collision.CrossFramePortal(&lvl, pl, off, 0)
	if pl.CurrentSector != "lz_ground" {
		t.Errorf("stepped off into %q, want the landing zone", pl.CurrentSector)
	}
}

// A texture a surface names but which has no file is a warning at load and a
// white surface at runtime, not a failure — so the names the ship is painted
// with are checked here, where a typo is a red test instead of a white hull.
func TestTheDropshipsTexturesExist(t *testing.T) {
	root := filepath.Join(testenv.RepoRoot(t), "resources", "textures")
	lvl := loadDropshipDemo(t)

	seen := map[string]bool{}
	note := func(name string) {
		if name != "" {
			seen[name] = true
		}
	}
	hold := chunkNamed(&lvl, "dropship.hold")
	if hold == nil {
		t.Fatal("no hold")
	}
	for si := range hold.Sectors {
		s := &hold.Sectors[si]
		note(s.Floor.Texture)
		note(s.Ceiling.Texture)
		for i := range s.Walls {
			note(s.Walls[i].Texture)
		}
		for i := range s.Portals {
			note(s.Portals[i].GapTexture)
		}
	}
	if e := world.FindEntity(&lvl, "dropship"); e != nil {
		for i := range e.Models {
			note(e.Models[i].Texture)
		}
	}
	if len(seen) < 4 {
		t.Fatalf("only %d textures found on the ship; the walk missed something", len(seen))
	}
	for name := range seen {
		if _, err := os.Stat(filepath.Join(root, name+".png")); err != nil {
			t.Errorf("the ship is painted with %q, which has no file: %v", name, err)
		}
	}
}

// The band of ground directly under the hull is authored as walls on both sides
// of a shared edge, which InferPortals would ordinarily dissolve into a portal —
// `pinned` is the only thing stopping it, and losing it is invisible until
// someone walks through the ship from behind.
func TestTheGroundUnderTheHullStaysSolid(t *testing.T) {
	lvl := loadDropshipDemo(t)
	surface := chunkNamed(&lvl, "surface")
	if surface == nil {
		t.Fatal("no surface chunk")
	}
	// Specifically: a solid wall on the z = -1.5 line, inside the hull's
	// footprint, from each side. Counting walls would not do it — the level's
	// rim is walls too, and they would keep the count non-zero long after the
	// band under the ship had dissolved into a portal.
	for _, id := range []string{"lz_ground", "lz_apron"} {
		s := sectorNamed(surface, id)
		if s == nil {
			t.Fatalf("sector %q is missing", id)
		}
		found := false
		for i := range s.Walls {
			w := &s.Walls[i]
			onLine := w.StartPoint.Y() == -1.5 && w.EndPoint.Y() == -1.5
			underHull := abs32(w.StartPoint.X()) <= 7 && abs32(w.EndPoint.X()) <= 7
			if onLine && underHull && w.Collision {
				found = true
			}
		}
		if !found {
			t.Errorf("%s has no solid wall under the hull: one can walk in through it", id)
		}
	}
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// And the seal, exercised rather than inferred: the same resolver the player's
// movement runs through has to refuse the doorway when the leaf is down and
// admit it when it is up, from both sides.
func TestTheShutDoorStopsAWalker(t *testing.T) {
	lvl := loadDropshipDemo(t)
	hold := chunkNamed(&lvl, "dropship.hold")
	hatch := sectorNamed(hold, "dropship.hold.hatch")
	if hatch == nil {
		t.Fatal("no doorway")
	}
	walker := world.NewPlayer(mgl32.Vec3{})

	// From the hold, and from the vestibule: a step of half a metre through the
	// doorway's middle. Both are chunk-local, which for this chunk is the
	// ship's own space.
	tries := []struct {
		name     string
		from, to mgl32.Vec3
	}{
		{"out of the hold", mgl32.Vec3{0, 0, 0.35}, mgl32.Vec3{0, 0, -0.15}},
		{"in off the lip", mgl32.Vec3{0, 0.03, -0.65}, mgl32.Vec3{0, 0.03, -0.15}},
	}

	hatch.Ceiling.Height = 2.4 // open
	for _, tc := range tries {
		got := collision.ResolveCeilingEntry(walker, hold, hatch, tc.from, tc.to)
		if got.Z() != tc.to.Z() {
			t.Errorf("open door refused a step %s: %v", tc.name, got)
		}
	}

	hatch.Ceiling.Height = 0.0 // shut
	for _, tc := range tries {
		got := collision.ResolveCeilingEntry(walker, hold, hatch, tc.from, tc.to)
		if got.Z() != tc.from.Z() {
			t.Errorf("shut door admitted a step %s: %v", tc.name, got)
		}
	}
}
