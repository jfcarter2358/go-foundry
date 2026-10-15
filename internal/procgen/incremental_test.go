package procgen

// incremental_test.go — StageKind.Incremental: a rerun keeps the stage's
// files, reuses the tiles whose hash did not change, prunes the rest, and a
// failed run leaves a manifest that vouches only for whole tiles.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var incWrites = map[string]int{}

func init() {
	RegisterStage(StageKind{
		Name: "tinc", Label: "Test incremental", Category: "Test", Version: 1, Incremental: true,
		Params: []ParamDef{
			// values: one tile per entry, its content the value.
			{Name: "values", Type: ParamString, Default: String("a,b,c,d")},
			{Name: "fail_after", Type: ParamInt, Default: Int(-1), Min: -1, Max: 100},
		},
		Run: func(rc *RunContext) error {
			vals := splitComma(rc.Params.Str("values"))
			if err := rc.WriteFile("summary.txt", []byte(rc.Params.Str("values"))); err != nil {
				return err
			}
			for i, v := range vals {
				name := fmt.Sprintf("t%d", i)
				file := name + ".txt"
				if rc.ReuseTile(name, "h:"+v, file) {
					continue
				}
				if fa := rc.Params.Int("fail_after"); fa >= 0 && i >= fa {
					rc.BeginTile(name)
					rc.WriteFile(file, []byte("half"))
					return errors.New("asked to fail")
				}
				rc.BeginTile(name)
				if err := rc.WriteFile(file, []byte(v)); err != nil {
					return err
				}
				incWrites[name]++
				rc.SetTile(name, "h:"+v)
			}
			return nil
		},
	})
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}

func TestIncrementalStageReusesFreshTiles(t *testing.T) {
	dir := t.TempDir()
	d := &Doc{Name: "inc", Seed: 1, Stages: []Stage{{Kind: "tinc", ID: "tiles", Params: Params{"values": String("a,b,c,d")}}}}
	run := func(force bool) {
		t.Helper()
		if _, err := Run(context.Background(), d, Options{OutDir: dir, Force: force}); err != nil {
			t.Fatal(err)
		}
	}
	for k := range incWrites {
		delete(incWrites, k)
	}
	run(false)
	if len(incWrites) != 4 {
		t.Fatalf("first run wrote %v", incWrites)
	}
	man := ReadManifest(dir)
	if e := man.Stages["tiles"]; len(e.Tiles) != 4 || e.Tiles["t2"] != "h:c" || e.Hash == "" {
		t.Fatalf("manifest entry %+v", e)
	}
	t0 := filepath.Join(dir, "tiles", "t0.txt")
	old := time.Now().Add(-time.Hour)
	os.Chtimes(t0, old, old)

	// One tile's input changes, one tile goes: only t2 is written, t3's
	// file is pruned, t0 is untouched.
	d.Stages[0].Params["values"] = String("a,b,C")
	run(false)
	if incWrites["t2"] != 2 || incWrites["t0"] != 1 || incWrites["t1"] != 1 {
		t.Errorf("writes %v, want only t2 rewritten", incWrites)
	}
	if st, _ := os.Stat(t0); !st.ModTime().Equal(old) {
		t.Error("a reused tile's file was touched")
	}
	if _, err := os.Stat(filepath.Join(dir, "tiles", "t3.txt")); !os.IsNotExist(err) {
		t.Error("the dropped tile's file is still there")
	}
	if e := ReadManifest(dir).Stages["tiles"]; len(e.Tiles) != 3 || e.Tiles["t2"] != "h:C" {
		t.Errorf("manifest tiles %v", e.Tiles)
	}

	// A failure at tile 1 after a change to tiles 1 and 2: the entry has no
	// hash (stale) and vouches for t0 (untouched) and nothing else.
	d.Stages[0].Params["values"] = String("a,B,D")
	d.Stages[0].Params["fail_after"] = Int(1)
	if _, err := Run(context.Background(), d, Options{OutDir: dir}); err == nil {
		t.Fatal("the failing run succeeded")
	}
	e := ReadManifest(dir).Stages["tiles"]
	if e.Hash != "" {
		t.Error("a failed incremental run's entry claims a hash")
	}
	if len(e.Tiles) != 2 || e.Tiles["t0"] != "h:a" || e.Tiles["t2"] != "h:C" {
		// t2 was not reached: its old file and hash are still whole.
		t.Errorf("surviving tiles %v, want t0 and the untouched t2", e.Tiles)
	}
	if _, ok := e.Tiles["t1"]; ok {
		t.Error("the tile the failure half-wrote is still vouched for")
	}
	// The next run reuses t0, rewrites t1 and t2.
	d.Stages[0].Params["fail_after"] = Int(-1)
	before := map[string]int{}
	for k, v := range incWrites {
		before[k] = v
	}
	run(false)
	if incWrites["t0"] != before["t0"] || incWrites["t1"] != before["t1"]+1 || incWrites["t2"] != before["t2"]+1 {
		t.Errorf("after the failure: writes %v (before %v)", incWrites, before)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "tiles", "t1.txt")); string(got) != "B" {
		t.Errorf("t1 = %q", got)
	}
	// Force rebuilds everything.
	run(true)
	if incWrites["t0"] != before["t0"]+1 {
		t.Errorf("Force reused a tile: %v", incWrites)
	}
	// Up to date: nothing runs.
	n := incWrites["t0"]
	run(false)
	if incWrites["t0"] != n {
		t.Error("a fresh incremental stage ran again")
	}
}
