package score

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The guide's example file (docs/guide/32-score-editor.md, the yaml block
// that starts "version: 1") reads, and its mixer — effects, buses, lanes —
// has nothing wrong with it. (As a sketch it binds a line it leaves out and
// gives a pickup a whole bar of music.)
func TestTheGuidesExampleParses(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(here), "..", "..", "docs", "guide", "32-score-editor.md"))
	if err != nil {
		t.Skip(err)
	}
	doc := string(b)
	start := strings.Index(doc, "```yaml\nversion: 1\n")
	if start < 0 {
		t.Fatal("no example file in the guide")
	}
	body := doc[start+len("```yaml\n"):]
	body = body[:strings.Index(body, "```")]
	s, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("the guide's example: %v", err)
	}
	for _, is := range s.Validate(nil) {
		for _, w := range []string{"lane", "insert", "effect", "bus", "master"} {
			if strings.Contains(is.Msg, w) {
				t.Errorf("the guide's example: %s", is)
			}
		}
	}
	l := s.Lines[0]
	if len(l.Inserts) != 2 || !l.Inserts[1].Bypass || !l.PreA || l.Swing == nil || len(l.Lanes) != 2 || !s.Playback.Swing.On() || !s.Playback.Humanize.On() || s.Buses.B.Pan != 0.2 {
		t.Errorf("the guide's example lost something: %+v", l)
	}
	if len(s.Buses.A.Lanes) != 1 || len(s.MasterLanes) != 1 {
		t.Errorf("the guide's example lost its bus or master lanes: %+v %+v", s.Buses.A.Lanes, s.MasterLanes)
	}
}
