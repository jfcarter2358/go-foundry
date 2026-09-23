package editor

// The reference tools, against the engine's own tables — which is the point of
// them: if a verb is added to flang or a key to the level schema, these answers
// change with it, and nothing here has to be maintained.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
)

func TestScriptAPIIsTheEnginesOwnTable(t *testing.T) {
	full := claudeScriptAPIText("")
	// A verb every script in this project uses, and a property.
	for _, want := range []string{"# flang builtins", "Properties, by kind", "log(", "sqrt("} {
		if !strings.Contains(full, want) {
			t.Errorf("the API listing does not mention %q", want)
		}
	}
	// Unfiltered it must stay compact: this is read into a context window.
	if len(full) > 20000 {
		t.Errorf("the unfiltered listing is %d bytes — too much to read for a lookup", len(full))
	}
	if strings.Contains(full, "—") {
		t.Error("the unfiltered listing includes summaries; it should be signatures only")
	}

	// Filtered, it explains itself.
	one := claudeScriptAPIText("explode")
	if !strings.Contains(one, "explode(") || !strings.Contains(one, "—") {
		t.Errorf("filtered listing = %q, want the signature and its summary", one)
	}
	if strings.Contains(one, "sqrt(") {
		t.Error("the filter did not narrow the list")
	}
	if got := claudeScriptAPIText("no_such_verb_anywhere"); !strings.Contains(got, "Nothing") {
		t.Errorf("a filter that matches nothing said %q", got)
	}
}

func TestReadOnlyPropertiesAreMarked(t *testing.T) {
	// Writing one of these is reported rather than obeyed, so Claude has to be
	// able to tell before it writes the script.
	got := claudeScriptAPIText("")
	if !strings.Contains(got, "(read-only)") {
		t.Error("no property is marked read-only; the distinction is lost")
	}
}

func TestLevelSchemaAnswersFromTheGeneratedTable(t *testing.T) {
	got := claudeLevelSchemaText("bake_exclude")
	if !strings.Contains(got, "bake_exclude") {
		t.Errorf("schema lookup = %q", got)
	}
	// The key keeps its type, because "which thing has this key" is half the
	// answer.
	if !strings.Contains(got, "world.Raw") {
		t.Errorf("schema lookup = %q, want the owning type named", got)
	}
	if got := claudeLevelSchemaText("no_such_key_at_all"); !strings.Contains(got, "No schema key") {
		t.Errorf("a miss said %q", got)
	}
}

func TestListAssetsNamesThemAsALevelWouldWriteThem(t *testing.T) {
	root := withClaudeProject(t)
	write := func(rel string) {
		p := filepath.Join(fileutil.ResourcesPath, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("textures/sf_panel.png")
	write("textures/cn_rock.png")
	write("models/marine.fmdl")
	write("scripts/hangar.flang")
	write("levels/level1.yaml")
	_ = root

	all := claudeListAssetsText("all", "")
	for _, want := range []string{"sf_panel", "marine", "hangar", "level1"} {
		if !strings.Contains(all, want) {
			t.Errorf("listing does not include %q:\n%s", want, all)
		}
	}
	// Names, not filenames: a level writes `texture: sf_panel`.
	if strings.Contains(all, "sf_panel.png") {
		t.Errorf("assets are listed with their extensions:\n%s", all)
	}

	only := claudeListAssetsText("textures", "")
	if !strings.Contains(only, "sf_panel") || strings.Contains(only, "marine") {
		t.Errorf("kind filter did not narrow the list:\n%s", only)
	}
	if got := claudeListAssetsText("all", "cn_"); !strings.Contains(got, "cn_rock") || strings.Contains(got, "sf_panel") {
		t.Errorf("name filter did not narrow the list:\n%s", got)
	}
	if got := claudeListAssetsText("all", "nothing_matches_this"); !strings.Contains(got, "No assets match") {
		t.Errorf("a miss said %q", got)
	}
}

func TestReferenceToolsAreDeclaredWell(t *testing.T) {
	for _, tool := range claudeReferenceTools() {
		if tool.Name == "" || tool.Description == "" || tool.Run == nil {
			t.Errorf("tool %+v is incomplete", tool.Name)
		}
		// Claude picks a tool by its description; a bare name is not enough.
		if len(tool.Description) < 40 {
			t.Errorf("tool %s has a description too short to choose it by", tool.Name)
		}
	}
}

func TestThePrimerNamesTheToolsItExpectsToBeUsed(t *testing.T) {
	// A tool Claude is not told about is a tool it uses only by luck.
	primer := claudePrimer()
	for _, tool := range append(claudeEditorTools(), claudeReferenceTools()...) {
		if !strings.Contains(primer, tool.Name) {
			t.Errorf("the primer never mentions the %s tool", tool.Name)
		}
	}
}
