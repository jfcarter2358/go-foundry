package game

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Scaffold runs on every boot of every build, so what is guarded here is that it
// makes exactly what is missing — and that what it makes is a project the
// engine will actually start in.

func TestScaffoldLaysOutAnEmptyProject(t *testing.T) {
	res := filepath.Join(t.TempDir(), "resources")

	created, err := Scaffold(res, []string{"levels", "textures"})
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	if want := []string{"levels", "textures", FileName}; !reflect.DeepEqual(created, want) {
		t.Errorf("created %v, want %v", created, want)
	}
	for _, dir := range []string{"levels", "textures"} {
		if info, err := os.Stat(filepath.Join(res, dir)); err != nil || !info.IsDir() {
			t.Errorf("%s was not made: %v", dir, err)
		}
	}

	// A default a release build would refuse is no default at all.
	m, err := Load(filepath.Join(res, FileName))
	if err != nil {
		t.Fatalf("the default manifest does not load: %v", err)
	}
	if m.StartLevel != "untitled" {
		t.Errorf("start level = %q, want the name the editor saves an unnamed level as", m.StartLevel)
	}
}

func TestScaffoldLeavesWhatIsThereAlone(t *testing.T) {
	res := t.TempDir()
	mine := "title: Mine\nstart_level: intro\n"
	if err := os.WriteFile(filepath.Join(res, FileName), []byte(mine), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(res, "levels"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(res, "levels", "intro.yaml"), []byte("chunks: []\n"), 0644); err != nil {
		t.Fatal(err)
	}

	created, err := Scaffold(res, []string{"levels", "sounds"})
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	if want := []string{"sounds"}; !reflect.DeepEqual(created, want) {
		t.Errorf("created %v, want only the missing folder %v", created, want)
	}
	if got, _ := os.ReadFile(filepath.Join(res, FileName)); string(got) != mine {
		t.Errorf("an existing game.yaml was overwritten:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(res, "levels", "intro.yaml")); err != nil {
		t.Error("a level in an existing folder went missing")
	}

	if created, err := Scaffold(res, []string{"levels", "sounds"}); err != nil || len(created) != 0 {
		t.Errorf("a second boot created %v, %v; want nothing", created, err)
	}
}

func TestScaffoldReportsAFileInTheWayAndCarriesOn(t *testing.T) {
	res := t.TempDir()
	if err := os.WriteFile(filepath.Join(res, "levels"), []byte("not a folder"), 0644); err != nil {
		t.Fatal(err)
	}

	created, err := Scaffold(res, []string{"levels", "models"})
	if err == nil {
		t.Error("a file standing where a folder belongs was not reported")
	}
	if want := []string{"models", FileName}; !reflect.DeepEqual(created, want) {
		t.Errorf("created %v, want %v: one bad folder should not stop the rest", created, want)
	}
}
