package procgen

import (
	"os"
	"strings"
	"testing"

	"foundry/internal/fileutil"
)

func init() {
	RegisterEnvironment(Environment{Name: "tenv", Label: "Test env", Color: [3]float64{0.1, 0.2, 0.3},
		New: func() *Doc {
			return &Doc{Seed: 4, Stages: []Stage{{Kind: "tsource", ID: "src", Params: Params{"count": Int(2)}}}}
		}})
}

// A preset is written only when absent, the file is preferred once there,
// and a user's edit survives another write.
func TestEnvironmentWriteIfAbsentAndLoaderPrefersTheFile(t *testing.T) {
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = t.TempDir()
	defer func() { fileutil.ResourcesPath = prev }()

	d, from, err := LoadEnvironment("tenv")
	if err != nil || from != "built-in tenv" || d.Name != "tenv" {
		t.Fatalf("before writing: %v %q %v", d, from, err)
	}
	path, wrote, err := WriteEnvironment("tenv")
	if err != nil || !wrote {
		t.Fatalf("first write: %v %v", wrote, err)
	}
	// The user edits it.
	data, _ := os.ReadFile(path)
	edited := strings.Replace(string(data), "seed: 4", "seed: 77", 1)
	if edited == string(data) {
		t.Fatalf("no seed line in:\n%s", data)
	}
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, wrote, err := WriteEnvironment("tenv"); err != nil || wrote {
		t.Fatalf("second write: wrote=%v err=%v; must leave the file alone", wrote, err)
	}
	if got, _ := os.ReadFile(path); string(got) != edited {
		t.Fatal("the user's edit was overwritten")
	}
	d, from, err = LoadEnvironment("tenv")
	if err != nil || d.Seed != 77 || from != "environments/tenv.fgen" {
		t.Fatalf("after editing: seed %v from %q err %v", d.Seed, from, err)
	}
	// A project's own preset is listed beside the registered ones.
	if err := WriteDoc(EnvironmentPath("mine"), &Doc{Name: "mine", Seed: 1}); err != nil {
		t.Fatal(err)
	}
	names := strings.Join(EnvironmentNames(), ",")
	if !strings.Contains(names, "mine") || !strings.Contains(names, "tenv") {
		t.Errorf("EnvironmentNames = %s", names)
	}
	if _, _, err := LoadEnvironment("nope"); err == nil {
		t.Error("an unknown environment loaded")
	}
	if g, _, err := LoadGenerator("environments/mine.fgen"); err != nil || g.Name != "mine" {
		t.Errorf("LoadGenerator by path: %v %v", g, err)
	}
	if c := EnvironmentColor("tenv"); c != [3]float64{0.1, 0.2, 0.3} {
		t.Errorf("colour %v", c)
	}
	if a, b := EnvironmentColor("unknown_one"), EnvironmentColor("unknown_one"); a != b {
		t.Error("an unregistered colour is not stable")
	}
}
