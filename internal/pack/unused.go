package pack

// unused.go — which assets in a content tree nothing uses.
//
// The same question a pruned pack answers, asked the other way round: a prune
// keeps what the game can reach from its manifest, and everything it would
// leave behind is a candidate for deleting from the project. Using refs.go's
// walk rather than a second one is the point — "unused" here means exactly
// "what a pruned release would not ship", so the report and the release can
// never disagree about a file.
//
// The walk errs towards calling a file used (a bare word in a comment is
// enough), so what it flags is very probably dead. The one way it is wrong is
// an asset named by a string a script assembles at runtime, `load_level("lvl_"
// + n)` — which is why this only reports, and the editor asks before deleting.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/game"
)

// UnusedOptions is one analysis.
type UnusedOptions struct {
	// Src is the content directory: a project's resources/.
	Src string
	// AllLevels counts every level as used, not only those the start level
	// leads to. A level the game cannot reach yet — a test map, the next
	// mission — is still the author's to keep; with this set the report is only
	// the assets that no level at all refers to.
	AllLevels bool
}

// UnusedAsset is one file nothing reaches.
type UnusedAsset struct {
	// Path is slash-separated, under the content root: "textures/old.png".
	Path string
	Size int64
	// ReferencedBy names the other unused files that mention this one. Empty
	// means nothing in the tree names it at all; otherwise it is only kept
	// alive by things that are themselves dead — delete them together.
	ReferencedBy []string
}

// UnusedReport is what an analysis found.
type UnusedReport struct {
	// Scanned is how many files the content directories hold.
	Scanned int
	// Unused is sorted by path.
	Unused []UnusedAsset
	// UnusedBytes is their total size.
	UnusedBytes int64
}

// FindUnused reports the assets under o.Src that the game never reaches.
//
// Only the content directories a prune walks are considered: shaders and fonts
// are named from Go and always ship (alwaysDirs), and the loose files at the
// top of the tree are the manifest and runtime libraries. READMEs that keep a
// directory in version control are never flagged. Editor backups (*.bak) are,
// being exactly the clutter this is for.
func FindUnused(o UnusedOptions) (UnusedReport, error) {
	m, err := game.Load(filepath.Join(o.Src, game.FileName))
	if err != nil {
		return UnusedReport{}, err
	}
	idx, err := indexAssets(o.Src, contentDirs)
	if err != nil {
		return UnusedReport{}, err
	}

	roots := manifestRoots(m)
	all, err := contentFiles(o.Src)
	if err != nil {
		return UnusedReport{}, err
	}
	if o.AllLevels {
		for _, path := range all {
			if strings.HasPrefix(path, levelsDir) && strings.HasSuffix(path, LevelSourceExt) {
				roots = append(roots, path)
			}
		}
	}
	keep := reachable(o.Src, idx, roots)

	var r UnusedReport
	r.Scanned = len(all)
	unused := map[string]int{} // path -> index in r.Unused
	for _, path := range all {
		if keep[path] || alwaysKeep(path) || editorOwned[path] {
			continue
		}
		var size int64
		if info, err := os.Stat(filepath.Join(o.Src, filepath.FromSlash(path))); err == nil {
			size = info.Size()
		}
		unused[path] = len(r.Unused)
		r.Unused = append(r.Unused, UnusedAsset{Path: path, Size: size})
		r.UnusedBytes += size
	}

	// Who still mentions each dead file, among the other dead files. The live
	// ones cannot — anything a live file names was reached through it.
	for _, from := range sortedKeys(unused) {
		if !textExts[strings.ToLower(filepath.Ext(from))] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(o.Src, filepath.FromSlash(from)))
		if err != nil {
			continue
		}
		seen := map[string]bool{}
		for _, tok := range scanTokens(string(data)) {
			for hit := range idx[tok] {
				i, ok := unused[hit]
				if !ok || hit == from || seen[hit] {
					continue
				}
				seen[hit] = true
				r.Unused[i].ReferencedBy = append(r.Unused[i].ReferencedBy, from)
			}
		}
	}
	for i := range r.Unused {
		sort.Strings(r.Unused[i].ReferencedBy)
	}
	return r, nil
}

// editorOwned are files in a content directory that the editor, not the game,
// reads — so no content names them, a release rightly leaves them out, and
// they are still not the author's to delete.
var editorOwned = map[string]bool{
	"textures/palette.pal": true, // the texture editor's palette (texture.palettePath)
}

// contentFiles is every file in the content directories, slash-separated and
// sorted.
func contentFiles(src string) ([]string, error) {
	var out []string
	for _, dir := range contentDirs {
		root := filepath.Join(src, dir)
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(src, path)
			if err != nil {
				return err
			}
			out = append(out, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// DeleteUnused removes the given content-relative paths from src, and any
// directory that leaves empty. It refuses a path that escapes src or is not in
// a content directory, so a stale or hand-edited list cannot reach the
// manifest, the shaders, or anything outside the project. Returns the paths it
// removed; the first error stops it.
func DeleteUnused(src string, paths []string) ([]string, error) {
	var done []string
	for _, p := range paths {
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(p)))
		top, _, _ := strings.Cut(clean, "/")
		if !isContentDir(top) || clean == top || strings.HasPrefix(clean, "../") {
			return done, &os.PathError{Op: "delete", Path: p, Err: fs.ErrPermission}
		}
		full := filepath.Join(src, filepath.FromSlash(clean))
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return done, err
		}
		done = append(done, clean)
		// Tidy the directories the file leaves empty, up to (not including)
		// the content directory itself.
		for dir := filepath.Dir(full); dir != filepath.Join(src, top); dir = filepath.Dir(dir) {
			if os.Remove(dir) != nil {
				break
			}
		}
	}
	return done, nil
}

func isContentDir(name string) bool {
	for _, d := range contentDirs {
		if d == name {
			return true
		}
	}
	return false
}

// UnusedCommand is the development binary's command for this report.
const UnusedCommand = "find-unused"

// UnusedMain is `foundry find-unused`: the report for the project at root,
// printed to out. It returns the process's exit code — 0 whether or not
// anything was found, since finding something is not a failure.
func UnusedMain(root string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet(UnusedCommand, flag.ContinueOnError)
	fs.SetOutput(out)
	project := fs.String("root", root, "project directory: the one holding resources/")
	fromStart := fs.Bool("from-start", false, "also flag the levels game.yaml's start level never leads to, and what only they use")
	fs.Usage = func() {
		fmt.Fprintf(out, "foundry %s — list the assets in resources/ that nothing uses.\n\n"+
			"An asset is unused when no level, script or other asset refers to it. With\n"+
			"-from-start, only what the start level leads to counts — exactly what a\n"+
			"pruned release ships. Nothing is deleted.\n\nFlags:\n", UnusedCommand)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return 2
	}

	r, err := FindUnused(UnusedOptions{Src: fileutil.ResourcesDir(*project), AllLevels: !*fromStart})
	if err != nil {
		fmt.Fprintf(out, "find-unused: %v\n", err)
		return 1
	}
	for _, a := range r.Unused {
		fmt.Fprintf(out, "%s  (%s)", a.Path, FormatSize(a.Size))
		if len(a.ReferencedBy) > 0 {
			fmt.Fprintf(out, "  only named by unused %s", strings.Join(a.ReferencedBy, ", "))
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintf(out, "%d of %d asset(s) unused, %s\n", len(r.Unused), r.Scanned, FormatSize(r.UnusedBytes))
	return 0
}

// FormatSize is a byte count for people.
func FormatSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
