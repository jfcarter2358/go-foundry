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
