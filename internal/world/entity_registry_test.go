package world

import (
	"foundry/internal/fileutil"
	"foundry/internal/testenv"
	"os"
	"path/filepath"
	"testing"
)

// withEntityDefsDir points the registry at a temporary install root holding the
// given definition files, and restores the real one afterwards.
//
// The registry resolves paths against ResourcesPath and caches what it reads,
// so both have to be swapped or a test would either read the real
// resources/entities/ or be served a definition an earlier test cached.
func withEntityDefsDir(t *testing.T, files map[string]string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, EntitiesDirName), 0755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		path := filepath.Join(root, EntitiesDirName, name)
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}

	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = root
	ReloadEntityDefs()
	t.Cleanup(func() {
		fileutil.ResourcesPath = prev
		ReloadEntityDefs()
	})
}

func TestLoadEntityDefReadsFromDisk(t *testing.T) {
	withEntityDefsDir(t, map[string]string{
		"crate.yaml": "id: crate\ntags: [prop]\nbound_half_x: 0.5\n",
		// No `id:`, so the filename is the name — the common case for a
		// definition an author never bothered to name twice.
		"unnamed.yaml": "tags: [scenery]\n",
	})

	def, err := LoadEntityDef("crate")
	if err != nil {
		t.Fatalf("LoadEntityDef: %v", err)
	}
	if def.Name != "crate" || def.BoundHalfX != 0.5 || !def.Visible {
		t.Errorf("got %+v", def)
	}

	def, err = LoadEntityDef("unnamed")
	if err != nil {
		t.Fatalf("LoadEntityDef(unnamed): %v", err)
	}
	if def.Name != "unnamed" {
		t.Errorf("name: got %q, want the filename", def.Name)
	}
}

func TestLoadEntityDefReportsMissingAndMalformed(t *testing.T) {
	withEntityDefsDir(t, map[string]string{
		"broken.yaml": "id: broken\nattributes:\n  health: {type: colour, value: 1}\n",
	})

	if _, err := LoadEntityDef("nothere"); err == nil {
		t.Error("a missing definition loaded")
	}
	if _, err := LoadEntityDef("broken"); err == nil {
		t.Error("a definition with an unknown attribute type loaded")
	}
	// The failure is cached too, so a level placing fifty of a missing type
	// reports it once rather than opening the file fifty times.
	if _, err := LoadEntityDef("nothere"); err == nil {
		t.Error("the second lookup of a missing definition succeeded")
	}
}

func TestScanEntityDefsListsSortedNames(t *testing.T) {
	withEntityDefsDir(t, map[string]string{
		"zeta.yaml":  "id: zeta\n",
		"alpha.yaml": "id: alpha\n",
		"notes.txt":  "not a definition",
	})

	got := ScanEntityDefs()
	if len(got) != 2 || got[0] != "alpha" || got[1] != "zeta" {
		t.Errorf("got %v, want [alpha zeta]", got)
	}
}

func TestSaveEntityDefRoundTrips(t *testing.T) {
	withEntityDefsDir(t, nil)

	def := &EntityDef{
		Name:         "turret",
		Description:  "Shoots.",
		Tags:         []string{"hostile"},
		BoundingKind: BoundingCylinder,
		BoundHalfX:   0.5, BoundHalfY: 1, BoundHalfZ: 0.5,
		Visible:    true,
		Attributes: AttrSet{"damage": IntAttr(7)},
		Models: []EntityModel{{
			Name:    "barrel",
			Path:    "models/turret.fmdl",
			Visible: true,
			Animations: []EntityAnimation{
				{Name: "fire", Clip: "recoil", Speed: 2},
			},
		}},
	}
	if err := SaveEntityDef(def); err != nil {
		t.Fatalf("SaveEntityDef: %v", err)
	}

	got, err := LoadEntityDef("turret")
	if err != nil {
		t.Fatalf("LoadEntityDef: %v", err)
	}
	if got.Name != def.Name || got.BoundingKind != def.BoundingKind || !got.Visible {
		t.Errorf("identity/bounds drifted: %+v", got)
	}
	if v := got.Attributes["damage"]; v != IntAttr(7) {
		t.Errorf("attribute drifted: %+v", v)
	}
	if len(got.Models) != 1 {
		t.Fatalf("got %d models, want 1", len(got.Models))
	}
	m := got.Models[0]
	if m.Name != "barrel" || m.Path != "models/turret.fmdl" || !m.Visible {
		t.Errorf("model drifted: %+v", m)
	}
	if len(m.Animations) != 1 || m.Animations[0].Clip != "recoil" || m.Animations[0].Speed != 2 {
		t.Errorf("animations drifted: %+v", m.Animations)
	}
}

// The definitions shipped in resources/entities/ are content the editor's
// catalog offers on a fresh checkout, so a typo in one is a broken catalog.
func TestShippedEntityDefsLoad(t *testing.T) {
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join(testenv.RepoRoot(t), "resources")
	ReloadEntityDefs()
	t.Cleanup(func() {
		fileutil.ResourcesPath = prev
		ReloadEntityDefs()
	})

	names := ScanEntityDefs()
	if len(names) == 0 {
		t.Skip("no definitions shipped in resources/entities/")
	}
	for _, name := range names {
		def, err := LoadEntityDef(name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		for _, m := range def.Models {
			if m.Path == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(testenv.RepoRoot(t), "resources", m.Path)); err != nil {
				t.Errorf("%s: model %q points at a file that is not there: %v", name, m.Name, err)
			}
		}
	}
}
