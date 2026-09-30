package render

import (
	"math"
	"testing"

	"foundry/internal/world"
)

// Every shape builds every variant at every level of detail as whole
// triangles of finite numbers, and stays inside the extent placement bounds
// sections with — a plant poking out of its section's box is culled while
// still on screen.
func TestFoliageTemplatesAreWellFormed(t *testing.T) {
	for s := world.FoliageShape(0); s < world.FoliageShapeCount; s++ {
		ext := world.FoliageExtent[s]
		for v := 0; v < world.FoliageVariants[s]; v++ {
			for lod := 0; lod < foliageLODs; lod++ {
				m := buildFoliageTemplate(s, v, lod)
				if len(m) == 0 || len(m)%(3*foliageVertexFloats) != 0 {
					t.Fatalf("%v variant %d lod %d: %d floats is not whole triangles", s, v, lod, len(m))
				}
				var top, reach float64
				for i := 0; i < len(m); i += foliageVertexFloats {
					for k := 0; k < foliageVertexFloats; k++ {
						if f := float64(m[i+k]); math.IsNaN(f) || math.IsInf(f, 0) {
							t.Fatalf("%v variant %d lod %d: non-finite vertex data", s, v, lod)
						}
					}
					top = max(top, math.Abs(float64(m[i+1])))
					reach = max(reach, math.Hypot(float64(m[i]), float64(m[i+2])))
				}
				// Placement bounds an instance by the larger of the two, in
				// every direction.
				if bound := float64(max(ext[0], ext[1])); max(top, reach) > bound*1.02 {
					t.Errorf("%v variant %d lod %d: %.2f tall, %.2f wide, outside its extent %v", s, v, lod, top, reach, ext)
				}
			}
		}
	}
}
