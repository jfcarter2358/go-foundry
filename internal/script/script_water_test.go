package script

import (
	"testing"

	"foundry/internal/material"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// water_at answers from the level's water (a brush pool here), and
// @level.prop.rain_ripples is written clamped (phase W5).
func TestWaterAtAndRainRipples(t *testing.T) {
	d := &material.Doc{Name: "script_test_water", Domain: material.DomainSurface}
	d.AddEffect(material.KindWater)
	material.Put(d)
	t.Cleanup(func() { material.Remove(d.Name) })

	lvl := pointerLevel(t, `
def on_spawn(:spawn:) -> null
    w = water_at(<10.0, -0.5, 10.0>)
    @self.attr.x = w["depth"]
    if w["under"]
        @self.attr.seen = "under"
    end
    @self.attr.hit = w["material"]
    if water_at(<40.0, 0.0, 40.0>) == null
        @self.attr.level = "dry"
    end
    @level.prop.rain_ripples = 2.0
    @self.attr.n = round(@level.prop.rain_ripples * 10.0)
end`, "")
	pool := world.NewBoxBrush("pool", mgl32.Vec3{10, -1, 10}, mgl32.Vec3{4, 1, 4}, "")
	for i := range pool.Faces {
		pool.Faces[i].Material = d.Name
	}
	lvl.Chunks = append(lvl.Chunks, world.Chunk{ID: "pool_chunk", Brushes: []world.Brush{pool}})
	world.InvalidateWaterQuery()
	ptick(lvl, 2)

	if x := crateAttr(t, lvl, "x").F; x < 0.49 || x > 0.51 {
		t.Errorf("depth %v, want 0.5 under the pool's top", x)
	}
	if crateAttr(t, lvl, "seen").S != "under" || crateAttr(t, lvl, "hit").S != d.Name {
		t.Errorf("under %q material %q", crateAttr(t, lvl, "seen").S, crateAttr(t, lvl, "hit").S)
	}
	if crateAttr(t, lvl, "level").S != "dry" {
		t.Error("water_at beside the pool was not null")
	}
	if n := crateAttr(t, lvl, "n").I; n != 10 || lvl.RainRipples != 1 {
		t.Errorf("rain_ripples read back %d/10 (level %v), want clamped to 1", n, lvl.RainRipples)
	}
}
