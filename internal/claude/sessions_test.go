package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProjectSlugMatchesTheCLIsOwnNaming(t *testing.T) {
	// Both of these were read off a real ~/.claude/projects directory.
	cases := map[string]string{
		"/home/jfcarter2358/personal/code/main/foundry": "-home-jfcarter2358-personal-code-main-foundry",
		"/tmp/claude-1000/-home-x/a_b.c":                "-tmp-claude-1000--home-x-a-b-c",
	}
	for in, want := range cases {
		if got := ProjectSlug(in); got != want {
			t.Errorf("ProjectSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFirstUserTextSkipsWhatThePersonDidNotType(t *testing.T) {
	cases := []struct{ in, want string }{
		{`"make the door open"`, "make the door open"},
		{`[{"type":"text","text":"<ide_opened_file>a.md</ide_opened_file>write a script"}]`, "write a script"},
		{`[{"type":"text","text":"<system-reminder>noise</system-reminder>\n\nreal question"}]`, "real question"},
		{`[{"type":"text","text":"Caveat: the messages below were generated"}]`, ""},
		{`[{"type":"tool_result","tool_use_id":"x"}]`, ""},
		{`"line one\nline two"`, "line one"},
	}
	for _, c := range cases {
		if got := firstUserText(json.RawMessage(c.in)); got != c.want {
			t.Errorf("firstUserText(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLongTitlesAreClipped(t *testing.T) {
	long := ""
	for i := 0; i < 50; i++ {
		long += "word "
	}
	got := cleanTitle(long)
	if len([]rune(got)) > 95 {
		t.Errorf("title is %d characters, want it clipped", len([]rune(got)))
	}
}

func TestListSessionsIsNewestFirstAndTitled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	project := "/projects/demo"
	dir := filepath.Join(home, ".claude", "projects", ProjectSlug(project))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, text string) {
		rec := `{"type":"queue-operation"}` + "\n" +
			`{"type":"user","message":{"content":[{"type":"text","text":` + text + `}]}}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("aaa.jsonl", `"the older one"`)
	write("bbb.jsonl", `"the newer one"`)
	// Make the second one plainly newer regardless of filesystem timestamp
	// resolution.
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "aaa.jsonl"), old, old); err != nil {
		t.Fatal(err)
	}

	got := ListSessions(project, 0)
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2", len(got))
	}
	if got[0].ID != "bbb" || got[0].Title != "the newer one" {
		t.Errorf("first session = %+v, want the newest with its title", got[0])
	}
	if n := len(ListSessions(project, 1)); n != 1 {
		t.Errorf("limit 1 returned %d", n)
	}
}

func TestListSessionsToleratesNoStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	if got := ListSessions("/nowhere", 0); got != nil {
		t.Errorf("got %v, want nothing when the store does not exist", got)
	}
	if got := ListSessions("", 0); got != nil {
		t.Errorf("got %v for an empty project path", got)
	}
}
