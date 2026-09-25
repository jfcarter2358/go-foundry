package model

// model_skeleton_users.go — which models wear a given skeleton.
//
// Two callers need this and neither can afford to parse for it. The editor's
// panel says whose animation an edit is about to change, every frame the
// section is open; the reload path has to rebuild every level model on a rig
// the moment its .fskl is saved. A rigged character is around a megabyte of
// YAML, so a full parse of the models folder is out of the question for either.
//
// So the link is read off the file's text. `skeleton:` is a top-level key and
// the writer puts it on a line of its own at column zero, where nothing nested
// can appear — a bone or clip called "skeleton" would still be indented under
// its list. That makes a line match both cheap and unambiguous.

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SkeletonLinkOf returns the skeleton the .fmdl at path links, or "" for a
// model that owns its armature. Errors read as "no link": the caller is either
// listing files it will not open or about to load the file properly anyway, and
// both are better served by an empty answer than by an error to thread through.
func SkeletonLinkOf(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	// A long vertex list is one line per vertex, so the default 64 KiB token is
	// plenty; the cap is raised anyway rather than have Scan stop early on a
	// file written with flow-style sequences.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == ' ' || line[0] == '-' || line[0] == '\t' {
			continue
		}
		rest, ok := strings.CutPrefix(line, "skeleton:")
		if !ok {
			continue
		}
		return strings.Trim(strings.TrimSpace(rest), `"'`)
	}
	return ""
}

// ModelsUsingSkeleton lists the models in resources/models that link the named
// skeleton, by bare model name, sorted. excludePath is skipped — the editor
// passes the document it has open, which does not want to be told it shares a
// rig with itself.
func ModelsUsingSkeleton(name, excludePath string) []string {
	if name == "" {
		return nil
	}
	if excludePath != "" {
		if abs, err := filepath.Abs(excludePath); err == nil {
			excludePath = abs
		}
	}
	var out []string
	for _, file := range ScanModelFiles() {
		path := filepath.Join(ModelsDir(), file)
		if excludePath != "" {
			if abs, err := filepath.Abs(path); err == nil && abs == excludePath {
				continue
			}
		}
		if SkeletonLinkOf(path) == name {
			out = append(out, trimModelExt(file))
		}
	}
	sort.Strings(out)
	return out
}

// ModelPathsUsingSkeleton is ModelsUsingSkeleton as full paths, for the reload
// path — which addresses a model by where it is, not by what it is called.
func ModelPathsUsingSkeleton(name string) []string {
	if name == "" {
		return nil
	}
	var out []string
	for _, file := range ScanModelFiles() {
		path := filepath.Join(ModelsDir(), file)
		if SkeletonLinkOf(path) == name {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}
