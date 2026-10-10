package collision

import (
	"fmt"
	"math/rand"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// benchSquadLevel is a field of n solid, movable, cylinder-ish soldiers spread
// over 60 m, a few of them overlapping, plus a player in the middle — the
// entity-against-entity pass of a busy battle.
func benchSquadLevel(n int) (*world.Level, *world.Entity) {
	r := rand.New(rand.NewSource(1))
	lvl := &world.Level{}
	for i := 0; i < n; i++ {
		e := world.NewBlankEntity(fmt.Sprintf("soldier_%d", i))
		e.Position = mgl32.Vec3{r.Float32()*60 - 30, 0.9, r.Float32()*60 - 30}
		e.Scale = mgl32.Vec3{1, 1, 1}
		e.BoundHalfX, e.BoundHalfY, e.BoundHalfZ = 0.3, 0.9, 0.3
		e.Rotation = mgl32.Vec3{0, r.Float32() * 6, 0}
		e.Collision.Entities, e.Collision.Movable = true, true
		lvl.Entities = append(lvl.Entities, e)
	}
	p := world.NewBlankEntity("player")
	p.Position = mgl32.Vec3{0, 0.9, 0}
	p.Scale = mgl32.Vec3{1, 1, 1}
	p.BoundHalfX, p.BoundHalfY, p.BoundHalfZ = 0.3, 0.9, 0.3
	p.Collision.Movable = true
	return lvl, &p
}

func BenchmarkResolveEntityCollisions(b *testing.B) {
	lvl, p := benchSquadLevel(150)
	start := append([]world.Entity(nil), lvl.Entities...)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		copy(lvl.Entities, start)
		b.StartTimer()
		ResolveEntityCollisions(lvl, p)
	}
}
