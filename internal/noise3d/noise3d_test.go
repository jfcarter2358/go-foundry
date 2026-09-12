package noise3d

// noise3d_test.go — the three properties the renderer relies on.
//
// The volumes are sampled with GL_REPEAT, so a seam at the wrap would draw as a
// flat plane through every cloud once per noise repeat. They are generated at
// startup, so two runs must agree or a level looks different each launch. And
// the coverage remap in volume_common.glsl assumes the channels span their
// range; a volume stuck in the middle greys every cloud out.

import (
	"bytes"
	"testing"
)

func TestVolumesAreDeterministic(t *testing.T) {
	if !bytes.Equal(Shape(16), Shape(16)) {
		t.Error("two Shape volumes of the same size differ")
	}
	if !bytes.Equal(Detail(16), Detail(16)) {
		t.Error("two Detail volumes of the same size differ")
	}
}

func TestChannelsSpanTheirRange(t *testing.T) {
	check := func(name string, data []uint8, stride, channel int) {
		lo, hi := uint8(255), uint8(0)
		for i := channel; i < len(data); i += stride {
			if data[i] < lo {
				lo = data[i]
			}
			if data[i] > hi {
				hi = data[i]
			}
		}
		if lo > 5 || hi < 250 {
			t.Errorf("%s spans %d..%d; it should be stretched to the full range", name, lo, hi)
		}
	}
	shape := Shape(ShapeSize)
	check("shape R", shape, 2, 0)
	check("shape G", shape, 2, 1)
	check("detail", Detail(DetailSize), 1, 0)
}

// The wrap is compared against the interior: the step from the last texel to
// the first, on each axis, must look like any other step between neighbours. A
// function that did not wrap its lattice shows up here as a step several times
// the interior average.
func TestVolumesTile(t *testing.T) {
	const size = ShapeSize
	data := Shape(size)
	at := func(x, y, z int) float64 { return float64(data[((z*size+y)*size+x)*2]) }
	abs := func(v float64) float64 {
		if v < 0 {
			return -v
		}
		return v
	}

	for axis := 0; axis < 3; axis++ {
		var seam, inner float64
		n := 0
		for a := 0; a < size; a++ {
			for b := 0; b < size; b++ {
				var last, first, m0, m1 float64
				switch axis {
				case 0:
					last, first = at(size-1, a, b), at(0, a, b)
					m0, m1 = at(size/2-1, a, b), at(size/2, a, b)
				case 1:
					last, first = at(a, size-1, b), at(a, 0, b)
					m0, m1 = at(a, size/2-1, b), at(a, size/2, b)
				default:
					last, first = at(a, b, size-1), at(a, b, 0)
					m0, m1 = at(a, b, size/2-1), at(a, b, size/2)
				}
				seam += abs(last - first)
				inner += abs(m1 - m0)
				n++
			}
		}
		seam /= float64(n)
		inner /= float64(n)
		if seam > inner*1.6+1 {
			t.Errorf("axis %d: the wrap step averages %.2f against %.2f inside; the volume does not tile", axis, seam, inner)
		}
	}
}
