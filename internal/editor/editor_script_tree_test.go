package editor

import (
	"strings"
	"testing"
)

// The sidebar's tree, checked without a window. Everything the drawing does is
// a walk of what this builds, so a folder that lands in the wrong place, or
// vanishes because nothing is in it, is a bug that shows up here first.

// treeShape renders a tree as indented lines, so a test can assert on the whole
// structure at once rather than on one node at a time.
func treeShape(n *scriptTreeNode, depth int) []string {
	var out []string
	pad := strings.Repeat("  ", depth)
	for _, kid := range n.Kids {
		out = append(out, pad+kid.Name+"/")
		out = append(out, treeShape(kid, depth+1)...)
	}
	for _, f := range n.Files {
		out = append(out, pad+f.Name)
	}
	return out
}

func TestBuildScriptTreeNests(t *testing.T) {
	tree := buildScriptTree([]string{
		"doors/lift.flang",
		"doors/inner/latch.flang",
		"root.flang",
		"enemies/guard.flang",
	}, nil)

	want := []string{
		"doors/",
		"  inner/",
		"    latch.flang",
		"  lift.flang",
		"enemies/",
		"  guard.flang",
		"root.flang",
	}
	got := treeShape(tree, 0)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("tree =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A folder made a moment ago is exactly the one with nothing in it, so a tree
// built only from file paths would make it disappear the instant it appeared.
func TestBuildScriptTreeKeepsEmptyFolders(t *testing.T) {
	tree := buildScriptTree([]string{"a.flang"}, []string{"empty", "deep/nested"})

	want := []string{"deep/", "  nested/", "empty/", "a.flang"}
	got := treeShape(tree, 0)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("tree =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The folder paths are what the row's ID, its menu and its "is the open file in
// here" test are all built from, so they have to be the full path and not the
// leaf name.
func TestBuildScriptTreePathsAreFull(t *testing.T) {
	tree := buildScriptTree([]string{"doors/inner/latch.flang"}, nil)
	doors := tree.Kids[0]
	if doors.Path != "doors" {
		t.Errorf("doors.Path = %q, want %q", doors.Path, "doors")
	}
	inner := doors.Kids[0]
	if inner.Path != "doors/inner" {
		t.Errorf("inner.Path = %q, want %q", inner.Path, "doors/inner")
	}
	if got := inner.Files[0].Rel; got != "doors/inner/latch.flang" {
		t.Errorf("file Rel = %q, want the path relative to the scripts folder", got)
	}
}

func TestScriptTreeCountIsRecursive(t *testing.T) {
	tree := buildScriptTree([]string{
		"doors/lift.flang",
		"doors/inner/latch.flang",
		"doors/inner/bolt.flang",
		"loose.flang",
	}, nil)
	if got := scriptTreeCount(tree); got != 4 {
		t.Errorf("root count = %d, want 4", got)
	}
	if got := scriptTreeCount(tree.Kids[0]); got != 3 {
		t.Errorf("doors/ count = %d, want 3 (its own plus the nested folder's)", got)
	}
	if got := scriptTreeCountLabel(tree.Kids[0]); got != "3 scripts" {
		t.Errorf("label = %q", got)
	}
}

// The folder holding the open document starts open, which is only right if
// "holds" means at any depth and does not match a folder whose name merely
// starts the same way.
func TestScriptTreeHolds(t *testing.T) {
	tree := buildScriptTree([]string{"doors/inner/latch.flang", "doorstop/x.flang"}, nil)
	doors, doorstop := tree.Kids[0], tree.Kids[1]
	if doors.Name != "doors" || doorstop.Name != "doorstop" {
		t.Fatalf("unexpected folders %q and %q", doors.Name, doorstop.Name)
	}
	if !scriptTreeHolds(doors, "doors/inner/latch.flang") {
		t.Error("doors/ should hold a file nested inside it")
	}
	if scriptTreeHolds(doorstop, "doors/inner/latch.flang") {
		t.Error("doorstop/ must not claim a file in doors/")
	}
	if scriptTreeHolds(doors, "") {
		t.Error("an unsaved document is in no folder")
	}
}

// Dropping a script on a folder is a move, and the move is worked out from two
// strings — the dragged path and the folder's — so everything that can go wrong
// with it goes wrong here rather than on screen.
func TestScriptDropPath(t *testing.T) {
	cases := []struct {
		name, rel, dir, want string
		ok                   bool
	}{
		{"root into a folder", "main.flang", "doors", "doors/main.flang", true},
		{"folder to folder", "doors/lift.flang", "enemies", "enemies/lift.flang", true},
		{"folder to a nested one", "doors/lift.flang", "doors/inner", "doors/inner/lift.flang", true},
		{"back out to the root", "doors/inner/lift.flang", "", "lift.flang", true},
		{"up one level", "doors/inner/lift.flang", "doors", "doors/lift.flang", true},
		// The drop everyone makes by accident: onto the folder the file is
		// already in. A rename to itself is not worth a status line.
		{"already there", "doors/lift.flang", "doors", "", false},
		{"already at the root", "main.flang", "", "", false},
		// Both strings reach here from scans and from row IDs; a stray prefix
		// or slash must not turn into a second scripts/ inside the path.
		{"prefixed source", "scripts/doors/lift.flang", "enemies", "enemies/lift.flang", true},
		{"prefixed folder", "doors/lift.flang", "scripts/enemies", "enemies/lift.flang", true},
		{"trailing slash", "doors/lift.flang", "enemies/", "enemies/lift.flang", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := scriptDropPath(c.rel, c.dir)
			if ok != c.ok || got != c.want {
				t.Errorf("scriptDropPath(%q, %q) = %q, %v; want %q, %v",
					c.rel, c.dir, got, ok, c.want, c.ok)
			}
		})
	}
}

// A folder delete closes a tab and drops a cache for every script this returns,
// after the folder is gone — so anything it misses is a tab left pointing at a
// deleted file, and anything it wrongly includes is a tab closed for nothing.
func TestScriptFilesUnder(t *testing.T) {
	files := []string{
		"doors/lift.flang",
		"doors/inner/latch.flang",
		"doorstop/stop.flang",
		"loose.flang",
	}
	cases := []struct {
		name, dir string
		want      []string
	}{
		{"at any depth", "doors", []string{"doors/lift.flang", "doors/inner/latch.flang"}},
		{"a nested folder", "doors/inner", []string{"doors/inner/latch.flang"}},
		// The same trap scriptTreeHolds guards against: a name that only starts
		// the same way is a different folder.
		{"not a lookalike", "doorstop", []string{"doorstop/stop.flang"}},
		{"prefixed and slashed", "scripts/doors/", []string{"doors/lift.flang", "doors/inner/latch.flang"}},
		{"nothing in it", "empty", nil},
		// The root is every script, and is never something to delete.
		{"not the root", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := scriptFilesUnder(files, c.dir)
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("scriptFilesUnder(%q) = %v, want %v", c.dir, got, c.want)
			}
		})
	}
	// Importers come back from the runtime prefixed, and the confirmation asks
	// about them one at a time.
	if got := scriptFilesUnder([]string{"scripts/doors/lift.flang"}, "doors"); len(got) != 1 {
		t.Errorf("a prefixed path inside the folder was not recognised: %v", got)
	}
}

// The root's menu and its "new script here" both build a path from the folder,
// and at the root that path must not start with a slash — a name like
// "/main.flang" reads as somewhere else entirely.
func TestScriptTreeDirPrefix(t *testing.T) {
	if got := scriptTreeDirPrefix(""); got != "" {
		t.Errorf("root prefix = %q, want empty", got)
	}
	if got := scriptTreeDirPrefix("doors/inner"); got != "doors/inner/" {
		t.Errorf("prefix = %q, want %q", got, "doors/inner/")
	}
}
