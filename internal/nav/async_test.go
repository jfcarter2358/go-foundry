package nav

// async_test.go — building navigation off the frame loop's thread.
//
// What SyncAsync promises: a level whose shape changes keeps serving its old
// grid until the new one lands, nothing is served before anything has been
// built, a result for a level that has since been replaced is dropped, and a
// moving chunk does not keep throwing the grids away.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// readyAfterBuild asks for a grid, lets the background build finish, and
// installs it the way the next frame's SyncAsync would.
func readyAfterBuild(t *testing.T, lvl *world.Level) *Grid {
	t.Helper()
	SyncAsync(lvl)
	ForReady(lvl, playerParams)
	Wait()
	SyncAsync(lvl)
	g, fresh := ForReady(lvl, playerParams)
	if g == nil || !fresh {
		t.Fatalf("after the build landed: grid %v, fresh %v", g != nil, fresh)
	}
	return g
}

func TestNothingIsServedBeforeTheFirstBuildLands(t *testing.T) {
	Invalidate()
	t.Cleanup(Invalidate)
	lvl := roomLevel()

	SyncAsync(lvl)
	if g, _ := ForReady(lvl, playerParams); g != nil {
		t.Fatal("a grid was served before any had been built; the request should have started a build")
	}
	Wait()
	// Installed on the frame loop's thread, at the next sync — not by the
	// goroutine that built it.
	if g, _ := ForReady(lvl, playerParams); g != nil {
		t.Fatal("a finished build was installed without a sync")
	}
	SyncAsync(lvl)
	if g, fresh := ForReady(lvl, playerParams); g == nil || len(g.Nodes) == 0 || !fresh {
		t.Fatalf("the finished build was not served: grid %v fresh %v", g != nil, fresh)
	}
}

func TestTheStaleGridIsServedWhileANewOneBuilds(t *testing.T) {
	Invalidate()
	t.Cleanup(Invalidate)
	pillar := world.NewBoxBrush("pillar", mgl32.Vec3{5, 2, 5}, mgl32.Vec3{2, 2, 2}, "concrete")
	lvl := roomLevel(pillar)
	old := readyAfterBuild(t, lvl)
	if _, ok := old.Locate(mgl32.Vec3{5, 0, 5}, 0.01); ok {
		t.Fatal("the pillar's footprint is walkable before it moves — the test proves nothing")
	}

	b := &lvl.Chunks[0].Brushes[len(lvl.Chunks[0].Brushes)-1]
	for i := range b.Verts {
		b.Verts[i] = b.Verts[i].Add(mgl32.Vec3{0, 10, 0})
	}
	SyncAsync(lvl)
	g, fresh := ForReady(lvl, playerParams)
	if g != old {
		t.Fatal("the old grid was dropped the moment the shape changed; agents would lose their navigation for the whole build")
	}
	if fresh {
		t.Error("the stale grid was reported fresh")
	}

	Wait()
	SyncAsync(lvl)
	g, fresh = ForReady(lvl, playerParams)
	if g == old || !fresh {
		t.Fatalf("the rebuilt grid did not replace the old one (same %v, fresh %v)", g == old, fresh)
	}
	if _, ok := g.Locate(mgl32.Vec3{5, 0, 5}, 0.01); !ok {
		t.Error("the rebuilt grid still has the pillar in it")
	}
}

func TestABuildForAReplacedLevelIsDropped(t *testing.T) {
	// A level load writes the new level over the old one in place, so the
	// *Level is the same; only the generation Invalidate bumps tells them apart.
	Invalidate()
	t.Cleanup(Invalidate)
	lvl := roomLevel()
	SyncAsync(lvl)
	ForReady(lvl, playerParams)
	Wait()

	Invalidate()
	*lvl = *roomLevel(world.NewBoxBrush("pillar", mgl32.Vec3{5, 2, 5}, mgl32.Vec3{2, 2, 2}, "concrete"))
	SyncAsync(lvl)
	if g, _ := ForReady(lvl, playerParams); g != nil {
		t.Fatal("the previous level's grid was installed into the new one")
	}
	g := readyAfterBuild(t, lvl)
	if _, ok := g.Locate(mgl32.Vec3{5, 0, 5}, 0.01); ok {
		t.Error("the grid describes the level that was replaced")
	}
}

func TestAMovingChunkIsLeftOutOfNavigation(t *testing.T) {
	Invalidate()
	t.Cleanup(Invalidate)
	lvl := roomLevel()
	lift := world.Chunk{ID: "lift", Velocity: mgl32.Vec3{0, 1, 0}}
	lift.Brushes = []world.Brush{world.NewBoxBrush("deck", mgl32.Vec3{30, 0, 30}, mgl32.Vec3{2, 0.25, 2}, "concrete")}
	lvl.Chunks = append(lvl.Chunks, lift)

	sig := Signature(lvl)
	lvl.Chunks[1].Origin = mgl32.Vec3{0, 5, 0}
	if Signature(lvl) != sig {
		t.Error("a moving chunk's placement is in the signature; every frame it moves would throw the grids away")
	}
	g := readyAfterBuild(t, lvl)
	if _, ok := g.Locate(mgl32.Vec3{30, 5.25, 30}, 0.5); ok {
		t.Error("a moving chunk's deck was baked into the grid")
	}

	// Once it stops it is ordinary geometry again.
	lvl.Chunks[1].Velocity = mgl32.Vec3{}
	if Signature(lvl) == sig {
		t.Error("a chunk that has stopped is still left out of the signature")
	}
}

func TestSyncAfterAsyncAnswersForTheLevelAsItIsNow(t *testing.T) {
	// The editor's overlay (and find_path) use the synchronous pair on the same
	// cache play filled asynchronously; they must never see a stale grid.
	Invalidate()
	t.Cleanup(Invalidate)
	pillar := world.NewBoxBrush("pillar", mgl32.Vec3{5, 2, 5}, mgl32.Vec3{2, 2, 2}, "concrete")
	lvl := roomLevel(pillar)
	old := readyAfterBuild(t, lvl)

	b := &lvl.Chunks[0].Brushes[len(lvl.Chunks[0].Brushes)-1]
	for i := range b.Verts {
		b.Verts[i] = b.Verts[i].Add(mgl32.Vec3{0, 10, 0})
	}
	SyncAsync(lvl) // starts a background build
	Sync(lvl)
	g := For(lvl, playerParams)
	if g == old {
		t.Fatal("Sync served the stale grid play was waiting to replace")
	}
	if _, ok := g.Locate(mgl32.Vec3{5, 0, 5}, 0.01); !ok {
		t.Error("the synchronous grid still has the pillar in it")
	}
	Wait()
	Sync(lvl)
	if For(lvl, playerParams) != g {
		t.Error("a background result replaced a grid that was already current")
	}
}
