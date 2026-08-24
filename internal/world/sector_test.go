package world

import "testing"

func TestWallTopHeightUsesCeilingOffsets(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
		floorH  float32
		ceilH   float32
		offsets []float32
		want    []float32
	}{
		{
			name:    "enabled ceiling slopes wall tops",
			enabled: true,
			floorH:  0, ceilH: 4,
			offsets: []float32{0, 0, 4, 4},
			want:    []float32{4, 4, 8, 8},
		},
		{
			name:    "disabled ceiling still slopes wall tops",
			enabled: false,
			floorH:  0, ceilH: 4,
			offsets: []float32{0, 0, 4, 4},
			want:    []float32{4, 4, 8, 8},
		},
		{
			name:    "disabled ceiling with no height of its own falls back to floor+3",
			enabled: false,
			floorH:  2, ceilH: 2,
			offsets: []float32{0, 1, 0, 1},
			want:    []float32{5, 6, 5, 6},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := Sector{
				Floor:   Floor{Enabled: true, Height: tc.floorH},
				Ceiling: Ceiling{Enabled: tc.enabled, Height: tc.ceilH, VertexOffsets: tc.offsets},
			}
			for i, want := range tc.want {
				if got := s.WallTopHeight(i); got != want {
					t.Errorf("WallTopHeight(%d) = %v, want %v", i, got, want)
				}
			}
		})
	}
}

// An out-of-range vertex index (endpoint not found in the sector polygon) must
// fall back to the flat height rather than panicking.
func TestWallTopHeightUnknownVertex(t *testing.T) {
	s := Sector{
		Floor:   Floor{Enabled: true, Height: 0},
		Ceiling: Ceiling{Enabled: true, Height: 4, VertexOffsets: []float32{1, 2}},
	}
	if got := s.WallTopHeight(-1); got != 4 {
		t.Errorf("WallTopHeight(-1) = %v, want 4", got)
	}
	if got := s.WallTopHeight(7); got != 4 {
		t.Errorf("WallTopHeight(7) = %v, want 4", got)
	}
}
