package world

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// TestFindEntityIndexAgreesWithTheScan drives the level's entity slice through
// every kind of edit its writers make — append (in and past capacity), remove
// by reslicing, wholesale replacement, swaps, in-place renames, and a slice
// rebuilt with a duplicate ID in it — and checks after each that FindEntity
// returns exactly the entity the plain scan does, for every ID present and some
// that are not.
func TestFindEntityIndexAgreesWithTheScan(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	lvl := &Level{}
	next := 0
	newID := func() string { next++; return fmt.Sprintf("e%d", next) }
	for i := 0; i < 50; i++ {
		lvl.Entities = append(lvl.Entities, Entity{ID: newID()})
	}
	check := func(step int, op string) {
		t.Helper()
		probe := []string{"missing", "e0", fmt.Sprintf("e%d", next+1)}
		for i := range lvl.Entities {
			probe = append(probe, lvl.Entities[i].ID)
		}
		for _, id := range probe {
			if got, want := FindEntity(lvl, id), findEntityScan(lvl, id); got != want {
				t.Fatalf("step %d (%s): FindEntity(%q) = %p, scan = %p", step, op, id, got, want)
			}
		}
	}
	for step := 0; step < 3000; step++ {
		op := ""
		switch k := r.Intn(7); {
		case k == 0:
			op = "append"
			lvl.Entities = append(lvl.Entities, Entity{ID: newID()})
		case k == 1 && len(lvl.Entities) > 0:
			op = "remove"
			i := r.Intn(len(lvl.Entities))
			lvl.Entities = append(lvl.Entities[:i], lvl.Entities[i+1:]...)
		case k == 2 && len(lvl.Entities) > 1:
			op = "swap"
			i, j := r.Intn(len(lvl.Entities)), r.Intn(len(lvl.Entities))
			lvl.Entities[i], lvl.Entities[j] = lvl.Entities[j], lvl.Entities[i]
		case k == 3 && len(lvl.Entities) > 0:
			op = "rename"
			lvl.Entities[r.Intn(len(lvl.Entities))].ID = newID()
		case k == 4 && len(lvl.Entities) > 1:
			// A duplicate arriving with a new slice (a hand-edited level loaded
			// raw). The index leaves duplicated IDs to the scan. What it cannot
			// see is a duplicate made by renaming an entity *in place* to an ID
			// already taken earlier in the array; nothing in the engine does
			// that (every ID goes through UniqueEntityID), and it is not tested.
			op = "duplicate"
			es := append([]Entity(nil), lvl.Entities...)
			es[r.Intn(len(es))].ID = es[r.Intn(len(es))].ID
			lvl.Entities = es
		case k == 5:
			op = "replace"
			lvl.Entities = append([]Entity(nil), lvl.Entities...)
		default:
			op = "remove+append"
			if len(lvl.Entities) > 0 {
				i := r.Intn(len(lvl.Entities))
				lvl.Entities = append(lvl.Entities[:i], lvl.Entities[i+1:]...)
				lvl.Entities = append(lvl.Entities, Entity{ID: newID()})
			}
		}
		check(step, op)
	}
}

// TestFindEntityIndexIsSafeConcurrently is for -race: parallel phases look
// entities up from many goroutines while the index is stale.
func TestFindEntityIndexIsSafeConcurrently(t *testing.T) {
	lvl := &Level{}
	for i := 0; i < 200; i++ {
		lvl.Entities = append(lvl.Entities, Entity{ID: fmt.Sprintf("e%d", i)})
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				id := fmt.Sprintf("e%d", (i*7+g)%200)
				if e := FindEntity(lvl, id); e == nil || e.ID != id {
					t.Errorf("FindEntity(%q) = %v", id, e)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

// BenchmarkFindEntity is a lookup in a level of 300 entities, hitting one near
// the end — what an AI agent's target lookup costs.
func BenchmarkFindEntity(b *testing.B) {
	lvl := &Level{}
	for i := 0; i < 300; i++ {
		lvl.Entities = append(lvl.Entities, Entity{ID: fmt.Sprintf("enemy_soldier_%d", i)})
	}
	b.Run("index", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = FindEntity(lvl, "enemy_soldier_250")
		}
	})
	b.Run("scan", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = findEntityScan(lvl, "enemy_soldier_250")
		}
	})
}
