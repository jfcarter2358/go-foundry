package place

// scatter_tiled_test.go — the tiled scatter: footprints still kept apart
// across tile edges, any worker count, a cache that hands every tile back
// placing exactly the same rocks, the codec exact, and a footprint too big
// to tile refused.

import (
	"math"
	"reflect"
	"sync"
	"testing"
)

// mapCache is a TileCache in memory.
type mapCache struct {
	mu    sync.Mutex
	tiles map[[2]int]*ScatterTile
	keys  map[[2]int]string
	hits  int
}

func (c *mapCache) Load(i, j int, key string) (*ScatterTile, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.keys[[2]int{i, j}] != key {
		return nil, false
	}
	t, err := DecodeScatterTile(EncodeScatterTile(c.tiles[[2]int{i, j}]))
	if err != nil {
		return nil, false
	}
	c.hits++
	return t, true
}

func (c *mapCache) Store(t *ScatterTile, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tiles[[2]int{t.I, t.J}], c.keys[[2]int{t.I, t.J}] = t, key
	return nil
}

func tiledRules() []Rule {
	big := DefaultRule()
	big.Variants, big.Density, big.Spacing, big.Cluster = boulders(3), 6, 1.2, 0
	small := big
	small.Name, small.Scale, small.Density, small.Spacing, small.Satellites = "small", [2]float64{0.2, 0.4}, 30, 1, 1.5
	return []Rule{big, small}
}

func TestTiledScatterKeepsApartAcrossTilesAndCaches(t *testing.T) {
	g := stepGround(t, 192, 0.5, 6)
	rules := tiledRules()
	set := func(workers int, c TileCache) (*ScatterResult, error) {
		return Scatter(g, ScatterSettings{Rules: rules, Seed: 5, Margin: 1, Tiles: 64, Cache: c,
			TileKey: func(i, j int) string { return "own" }}, pool(workers))
	}
	c := &mapCache{tiles: map[[2]int]*ScatterTile{}, keys: map[[2]int]string{}}
	a, err := set(1, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Tiles) != 16 || len(a.Instances) < 200 {
		t.Fatalf("%d tiles, %d rocks", len(a.Tiles), len(a.Instances))
	}
	in := a.Instances
	for x := range in {
		for y := x + 1; y < len(in); y++ {
			f := math.Max(rules[in[x].Rule].Spacing, rules[in[y].Rule].Spacing)
			if d := math.Hypot(in[x].Pos[0]-in[y].Pos[0], in[x].Pos[2]-in[y].Pos[2]); d < f*(in[x].Radius+in[y].Radius)-1e-9 {
				t.Fatalf("rocks %d and %d overlap (%.2f m apart)", x, y, d)
			}
		}
	}
	b, err := set(7, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Instances, b.Instances) || !reflect.DeepEqual(a.Stats, b.Stats) {
		t.Fatal("1 and 7 workers placed different rocks")
	}
	// Every tile from the cache: the same rocks and counts.
	r, err := set(4, c)
	if err != nil {
		t.Fatal(err)
	}
	if c.hits != 16 {
		t.Errorf("%d cache hits, want 16", c.hits)
	}
	if !reflect.DeepEqual(a.Instances, r.Instances) || !reflect.DeepEqual(a.Stats, r.Stats) {
		t.Fatal("tiles read back from the cache placed different rocks")
	}
}

func TestTiledScatterRefusesHugeFootprints(t *testing.T) {
	g := stepGround(t, 128, 1, 6)
	r := DefaultRule()
	r.Variants, r.Scale = chunks(1), [2]float64{4, 5} // 3 m × 5 × 2 = 30 m+
	r.Spacing = 1.2
	if _, err := Scatter(g, ScatterSettings{Rules: []Rule{r}, Seed: 1, Tiles: 64}, pool(2)); err != ErrTiledReach {
		t.Fatalf("err %v, want ErrTiledReach", err)
	}
}
