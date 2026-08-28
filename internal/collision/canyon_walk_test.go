package collision

// canyon_walk_test.go — the shipped canyon level's two routes stay walkable.
//
// Loading it proves the geometry parses; only walking it proves the level is
// playable. Both routes have failed during authoring for reasons a load test
// could not see: a ramp meeting the canyon floor at a height nothing could step
// up, and a building whose walls had been dissolved into the basin.

import (
	"foundry/internal/gfx"
	"foundry/internal/testenv"
	"foundry/internal/world"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// canyonLevel loads resources/levels/canyon.yaml without going through
// package levelio, which imports this one.
func canyonLevel(t *testing.T) *world.Level {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	if gfx.Textures == nil {
		gfx.Textures = map[string]uint32{}
	}
	for _, n := range []string{"", "default", "stone_floor", "concrete", "dirt", "rock", "grass"} {
		gfx.Textures[n] = 1
	}
	path := filepath.Join(testenv.RepoRoot(t), "resources", "levels", "canyon.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read canyon.yaml: %v", err)
	}
	var raw world.RawLevel
	if err := yaml.Unmarshal(b, &raw); err != nil {
		t.Fatalf("parse canyon.yaml: %v", err)
	}
	lvl := &world.Level{Chunks: []world.Chunk{rawCanyonChunk(t, raw.Chunks[0])}}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatalf("indexes: %v", err)
	}
	return lvl
}

// rawCanyonChunk builds the runtime chunk from the raw one. Only the fields the
// walk depends on — points, sector rings, heights, vertex offsets, pinning —
// are carried across; textures and scripting are not.
func rawCanyonChunk(t *testing.T, rc world.RawChunk) world.Chunk {
	t.Helper()
	ch := world.Chunk{ID: rc.ID, Scale: 1}
	idx := map[string]int{}
	names := make([]string, 0, len(rc.Points))
	for n := range rc.Points {
		names = append(names, n)
	}
	sortStrings(names)
	for _, n := range names {
		idx[n] = len(ch.Points)
		ch.Points = append(ch.Points, mgl32.Vec2{rc.Points[n].X, rc.Points[n].Y})
		ch.PointNames = append(ch.PointNames, n)
	}
	for _, rs := range rc.Sectors {
		s := world.Sector{
			ID:      rs.ID,
			Layer:   rs.Layer,
			Floor:   world.Floor{Enabled: rs.Floor.Enabled, Height: rs.Floor.Height, VertexOffsets: rs.Floor.VertexOffsets, DoubleSided: true, Alpha: 1},
			Ceiling: world.Ceiling{Enabled: rs.Ceiling.Enabled, Height: rs.Ceiling.Height, DoubleSided: true, Alpha: 1},
		}
		for _, e := range rs.Edges {
			s.PointIndices = append(s.PointIndices, idx[e.From])
			s.EdgeKinds = append(s.EdgeKinds, "wall")
			s.Walls = append(s.Walls, world.Wall{
				StartPoint: ch.Points[idx[e.From]], EndPoint: ch.Points[idx[e.To]],
				Collision: true, Visible: true, DoubleSided: true, Alpha: 1,
				Pinned: e.Pinned,
			})
		}
		ch.Sectors = append(ch.Sectors, s)
	}
	ch.InferPortals()
	for si := range ch.Sectors {
		_ = world.ReloadSector(&ch, &ch.Sectors[si])
	}
	ch.RecomputeBounds()
	return ch
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// walkRoute drives a body toward each waypoint in turn and returns the sectors
// it passed through, in order.
func walkRoute(t *testing.T, lvl *world.Level, start mgl32.Vec3, waypoints [][2]float32) []string {
	t.Helper()
	ch := &lvl.Chunks[0]
	p := world.NewPlayer(ch.LocalToWorld(start))
	settle(p, lvl, 300)

	var visited []string
	last := ""
	note := func() {
		if p.CurrentSector != "" && p.CurrentSector != last {
			visited = append(visited, p.CurrentSector)
			last = p.CurrentSector
		}
	}
	note()
	for _, wp := range waypoints {
		for i := 0; i < 3000; i++ {
			l := ch.WorldToLocal(p.Position)
			d := mgl32.Vec2{wp[0] - l.X(), wp[1] - l.Z()}
			if d.Len() < 1.0 {
				break
			}
			step := d.Normalize().Mul(0.18)
			MovePlayer(p, lvl, walkMove(mgl32.Vec3{step.X(), 0, step.Y()}))
			note()
		}
	}
	return visited
}

func visitedAll(visited []string, want ...string) (string, bool) {
	seen := map[string]bool{}
	for _, s := range visited {
		seen[s] = true
	}
	for _, w := range want {
		if !seen[w] {
			return w, false
		}
	}
	return "", true
}

func TestCanyonWalkIntoTheBuildingAndUpstairs(t *testing.T) {
	lvl := canyonLevel(t)
	visited := walkRoute(t, lvl, mgl32.Vec3{-25, 1, 0}, [][2]float32{
		{-13, 0}, // up to the west doorway
		{-6, 0},  // inside
		{8, -8},  // across to the foot of the stairs
		{8, 8},   // up them
		{0, 8},   // out onto the upper storey
	})
	t.Logf("visited: %v", visited)
	if missing, ok := visitedAll(visited, "hall", "stairs", "upper"); !ok {
		t.Errorf("the route never reached %q; visited %v", missing, visited)
	}

	ch := &lvl.Chunks[0]
	p := world.NewPlayer(ch.LocalToWorld(mgl32.Vec3{0, 6, 8}))
	settle(p, lvl, 400)
	if got := ch.WorldToLocal(p.Position).Y(); got < 5.0 {
		t.Errorf("the upper storey rests at y=%.2f, want ~5.1 (floor 4.2 + half-height)", got)
	}
}

func TestCanyonWalkUpTheCliffPath(t *testing.T) {
	lvl := canyonLevel(t)
	visited := walkRoute(t, lvl, mgl32.Vec3{20, 1, -30}, [][2]float32{
		{36, -37}, // south-east across the basin, to the gap in the cliff
		{46, -37}, // through it onto the path's flat foot
		{46, -20}, // first ramp
		{46, -12}, // landing
		{46, 10},  // second ramp
		{46, 30},  // final climb
		{60, 44},  // out onto the rim
	})
	t.Logf("visited: %v", visited)
	if missing, ok := visitedAll(visited, "path_0", "path_1", "path_3", "path_5", "rim_e"); !ok {
		t.Errorf("the climb never reached %q; visited %v", missing, visited)
	}
}

// The rim abuts the canyon in plan but sits at 18. Walking north across the
// plateau must not drop into the basin, and the basin's north wall must hold.
func TestCanyonRimDoesNotOpenIntoTheBasin(t *testing.T) {
	lvl := canyonLevel(t)
	ch := &lvl.Chunks[0]
	p := world.NewPlayer(ch.LocalToWorld(mgl32.Vec3{0, 20, 50}))
	if !settle(p, lvl, 500) {
		t.Fatalf("never landed on the rim; y=%v", ch.WorldToLocal(p.Position).Y())
	}
	if got := ch.WorldToLocal(p.Position).Y(); got < 18 {
		t.Fatalf("landed at y=%.2f, below the rim", got)
	}
	// Walk south, toward the canyon's north edge.
	for i := 0; i < 400; i++ {
		MovePlayer(p, lvl, walkMove(mgl32.Vec3{0, 0, -0.18}))
	}
	l := ch.WorldToLocal(p.Position)
	if l.Y() < 18 {
		t.Errorf("walking south off the rim dropped to y=%.2f in sector %q — the plateau opens into the canyon",
			l.Y(), p.CurrentSector)
	}
}
