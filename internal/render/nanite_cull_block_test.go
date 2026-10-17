package render

import "testing"

// The command-buffer warning fires only past a known storage block limit.
func TestCmdBlockTooBig(t *testing.T) {
	for _, c := range []struct {
		size, max int64
		want      bool
	}{
		{100, 0, false},               // limit unknown: never warn
		{128 << 20, 128 << 20, false}, // exactly the limit fits
		{128<<20 + 1, 128 << 20, true},
		{8_000_000 * 20, 128 << 20, true}, // the showcase's ~8 M slots on WSLg
		{8_000_000 * 20, 2 << 30, false},  // the same on a 2 GB native limit
	} {
		if got := cmdBlockTooBig(c.size, c.max); got != c.want {
			t.Errorf("cmdBlockTooBig(%d, %d) = %v, want %v", c.size, c.max, got, c.want)
		}
	}
}
