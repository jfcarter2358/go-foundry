package imguibackend

import "testing"

func TestComboFilterMatches(t *testing.T) {
	cases := []struct {
		filter, label string
		want          bool
	}{
		{"", "anything", true},
		{"  ", "anything", true},
		{"tex", "sf_Texture_01", true},
		{"TEX", "sf_texture_01", true},
		{"sf 01", "sf_texture_01", true},
		{"01 sf", "sf_texture_01", true},
		{"sf 02", "sf_texture_01", false},
		{"clip", "Idle##clip", false},
		{"idle", "Idle###animid", true},
	}
	for _, c := range cases {
		if got := ComboFilterMatches(c.filter, c.label); got != c.want {
			t.Errorf("ComboFilterMatches(%q, %q) = %v, want %v", c.filter, c.label, got, c.want)
		}
	}
}
