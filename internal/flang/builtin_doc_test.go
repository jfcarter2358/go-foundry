package flang

import "testing"

// TestBuiltinDocsCoverTable is the point of having the doc table in this
// package at all: a builtin added to `builtins` and forgotten here would leave
// the F1 reference quietly incomplete, which is the failure nobody notices
// until they are looking for the function that is missing.
func TestBuiltinDocsCoverTable(t *testing.T) {
	documented := map[string]bool{}
	for _, d := range BuiltinDocs() {
		if documented[d.Name] {
			t.Errorf("builtin %q is documented twice", d.Name)
		}
		documented[d.Name] = true
		if !IsBuiltin(d.Name) {
			t.Errorf("builtin_doc.go documents %q, which is not a builtin", d.Name)
		}
		if d.Sig == "" || d.Summary == "" || d.Ret == "" || d.Group == "" {
			t.Errorf("builtin %q has an incomplete doc entry: %+v", d.Name, d)
		}
	}
	for _, name := range BuiltinNames() {
		if !documented[name] {
			t.Errorf("builtin %q has no entry in builtin_doc.go", name)
		}
	}
}

// TestBuiltinDocGroupsAreKnown keeps a typo'd group from creating a section the
// reference orders arbitrarily.
func TestBuiltinDocGroupsAreKnown(t *testing.T) {
	known := map[string]bool{}
	for _, g := range BuiltinGroups() {
		known[g] = true
	}
	for _, d := range BuiltinDocs() {
		if !known[d.Group] {
			t.Errorf("builtin %q is in group %q, which is not in builtinGroupOrder", d.Name, d.Group)
		}
	}
}

// TestBuiltinDocsGrouped checks the display order: groups in the declared
// order, names alphabetical within one.
func TestBuiltinDocsGrouped(t *testing.T) {
	docs := BuiltinDocs()
	rank := map[string]int{}
	for i, g := range BuiltinGroups() {
		rank[g] = i
	}
	for i := 1; i < len(docs); i++ {
		prev, cur := docs[i-1], docs[i]
		switch {
		case rank[prev.Group] > rank[cur.Group]:
			t.Fatalf("group %q comes after %q", cur.Group, prev.Group)
		case prev.Group == cur.Group && prev.Name > cur.Name:
			t.Fatalf("%q comes after %q within %q", cur.Name, prev.Name, cur.Group)
		}
	}
	if n := len(BuiltinDocsIn(GroupVectors)); n == 0 {
		t.Fatal("BuiltinDocsIn returned nothing for a group that has entries")
	}
}
