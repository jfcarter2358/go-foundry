package render

// scatter_column_test.go — where the height bake looks for ground.
//
// The bake takes the highest walkable surface on each column, so wherever the
// column starts is the ceiling on where a blade can grow. It used to start
// scatterDropMargin above the volume, and the floor of an entity parked over a
// grass volume won: the grass grew up on the entity instead of in the region
// the author drew.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestScatterColumnStaysInsideTheVolume(t *testing.T) {
	v := &world.Volume{Position: mgl32.Vec3{3, 10, -2}, HalfX: 5, HalfY: 2, HalfZ: 5}

	topY, drop := scatterColumn(v)
	if topY != 12 {
		t.Errorf("column starts at y=%v, want the volume's own top at 12", topY)
	}
	// Still lenient downward: a volume floated a little clear of its floor
	// finds it.
	if bottom := topY - drop; bottom != 8-scatterDropMargin {
		t.Errorf("column reaches y=%v, want %v below the volume's bottom", bottom, scatterDropMargin)
	}
}
