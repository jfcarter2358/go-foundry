package terrain

// tiled_cache_test.go — the tiled erosion's window cache: a cached run is
// the uncached run to the bit, and a far edit erodes only the windows it
// reaches.

import (
	"context"
	"fmt"
	"testing"
)

type memCache struct {
	m             map[string][]byte
	loads, stores int
}

func (c *memCache) Load(name string, key [32]byte) ([]byte, bool) {
	d, ok := c.m[name+fmt.Sprintf("%x", key)]
	if ok {
		c.loads++
	}
	return d, ok
}

func (c *memCache) Store(name string, key [32]byte, p []byte) error {
	c.m[name+fmt.Sprintf("%x", key)] = append([]byte(nil), p...)
	c.stores++
	return nil
}

func threeTiled(eastRelief float64) *Union {
	u := twoPlains()
	c := u.Providers[1]
	c.Name = "far"
	c.Settings.Seed = 9
	c.Settings.Base.Relief = eastRelief
	c.Measure = Rect{64, -128, 128, 128}
	u.Providers[1].Measure = Rect{0, -128, 64, 128}
	u.Providers = append(u.Providers, c)
	u.Weights = stripes([]float64{0, 80}, 30)
	u.Erosion, u.Window, u.Margin = ErosionTiled, 64, 16
	for i := range u.Providers {
		u.Providers[i].Key = fmt.Sprintf("%s/%d/%g", u.Providers[i].Name, u.Providers[i].Settings.Seed, u.Providers[i].Settings.Base.Relief)
	}
	return u
}

func TestWindowCacheIsExactAndLocal(t *testing.T) {
	pool := func() Pool { return NewPool(context.Background(), 4) }
	cache := &memCache{m: map[string][]byte{}}
	u := threeTiled(4)
	u.Cache = cache
	first, err := GenerateUnion(u, pool(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.WindowsCached != 0 || first.WindowsEroded == 0 || cache.stores != first.WindowsEroded {
		t.Fatalf("first run: cached %d eroded %d stored %d", first.WindowsCached, first.WindowsEroded, cache.stores)
	}
	plain, _ := GenerateUnion(threeTiled(4), pool(), nil)
	if ResultHash(first) != ResultHash(plain) {
		t.Fatal("a run that stores its windows differs from one without a cache")
	}
	// The same inputs again: every window from the cache, the same bits.
	u = threeTiled(4)
	u.Cache = cache
	again, _ := GenerateUnion(u, pool(), nil)
	if again.WindowsEroded != 0 || ResultHash(again) != ResultHash(plain) || again.Droplets != plain.Droplets {
		t.Fatalf("rerun: eroded %d, same=%v, droplets %d vs %d", again.WindowsEroded, ResultHash(again) == ResultHash(plain),
			again.Droplets, plain.Droplets)
	}
	// Edit the far east region: the west windows come from the cache, the
	// east erode, and the answer is the uncached run's.
	u = threeTiled(9)
	u.Cache = cache
	edited, _ := GenerateUnion(u, pool(), nil)
	want, _ := GenerateUnion(threeTiled(9), pool(), nil)
	if edited.WindowsCached == 0 || edited.WindowsEroded == 0 {
		t.Errorf("far edit: cached %d, eroded %d — want some of each", edited.WindowsCached, edited.WindowsEroded)
	}
	if ResultHash(edited) != ResultHash(want) {
		t.Fatal("the partly cached run differs from a full run of the edited union")
	}
	// A provider without a key is never cached.
	u = threeTiled(9)
	u.Cache = cache
	for i := range u.Providers {
		u.Providers[i].Key = ""
	}
	nokey, _ := GenerateUnion(u, pool(), nil)
	if nokey.WindowsCached != 0 {
		t.Errorf("%d windows came from the cache with no provider keys", nokey.WindowsCached)
	}
}
