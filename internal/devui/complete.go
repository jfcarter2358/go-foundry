package devui

// complete.go — what Tab does.
//
// The candidates depend on where in the line the cursor is, because that is the
// only thing that makes completion worth having here: a console whose Tab
// offered the same eighty words wherever you pressed it is a console where Tab
// is noise. The first word is a command; after `spawn` it is an entity type;
// after `tp` it is anything in the level with a position; after `level` it is a
// level. A word starting with @ or # is a name or a tag wherever it appears,
// including in the flang prompt, which is where it is most useful of all.
//
// The cycle is the shell's: the first press fills in as far as the candidates
// agree and lists them, and each press after that offers the next one whole.

import (
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/levelio"
	"foundry/internal/script"
	"foundry/internal/world"
)

// maxListed is how many candidates are printed when Tab cannot narrow them. A
// level with four hundred entities would otherwise answer a Tab by clearing the
// screen.
const maxListed = 40

// complete fills in or cycles the word under the cursor. reverse is Shift+Tab.
func complete(reverse bool) {
	if console.compActive {
		cycle(reverse)
		return
	}
	start := tokenStart()
	prefix := string(console.line[start:console.cursor])
	all := candidates(tokenIndex(start), prefix)
	if len(all) == 0 {
		return
	}

	console.compAll = all
	// Before the first candidate, not on it: the press that starts a cycle has
	// already done something — it filled in the common prefix and listed the
	// candidates — so the next press must offer the first of them rather than
	// skipping straight to the second.
	console.compIdx = -1

	if len(all) == 1 {
		replaceToken(start, all[0]+" ")
		resetCompletion()
		return
	}

	// As far as they agree, then say what they were. Anything longer than the
	// prefix is progress; when they agree on nothing more, the list is the
	// whole of the answer.
	if common := commonPrefix(all); len(common) > len(prefix) {
		replaceToken(start, common)
	}
	listCandidates(all)
	console.compActive = true
}

// cycle offers the next candidate of a completion already in progress.
func cycle(reverse bool) {
	n := len(console.compAll)
	if n == 0 {
		resetCompletion()
		return
	}
	switch {
	case console.compIdx < 0 && reverse:
		console.compIdx = n - 1
	case console.compIdx < 0:
		console.compIdx = 0
	case reverse:
		console.compIdx = (console.compIdx - 1 + n) % n
	default:
		console.compIdx = (console.compIdx + 1) % n
	}
	// The token to replace is whatever the last cycle left, which is why the
	// start is recomputed rather than remembered: the line may be longer now.
	replaceToken(tokenStart(), console.compAll[console.compIdx])
}

// resetCompletion ends any cycle in progress. Called by every key that is not
// Tab, so that typing after a completion starts a fresh one.
func resetCompletion() {
	console.compActive = false
	console.compAll = nil
	console.compIdx = 0
}

// tokenStart is where the word under the cursor begins.
func tokenStart() int {
	i := console.cursor
	for i > 0 && !isSpace(console.line[i-1]) {
		i--
	}
	return i
}

// tokenIndex is which word of the line the token starting at start is: 0 for
// the command, 1 for its first argument, and so on.
func tokenIndex(start int) int {
	n := 0
	inWord := false
	for i := 0; i < start; i++ {
		if isSpace(console.line[i]) {
			inWord = false
			continue
		}
		if !inWord {
			inWord = true
			n++
		}
	}
	return n
}

// replaceToken swaps the word starting at start for s, and puts the cursor at
// the end of it.
func replaceToken(start int, s string) {
	tail := append([]rune{}, console.line[console.cursor:]...)
	console.line = append(console.line[:start], []rune(s)...)
	console.cursor = len(console.line)
	console.line = append(console.line, tail...)
}

// candidates is what could go where the cursor is.
func candidates(word int, prefix string) []string {
	lvl := world.CurrentLevel()

	// A sigil says what the word is whatever else the line says, and it stays
	// on the front of every candidate so that completing it does not delete it.
	switch {
	case strings.HasPrefix(prefix, "@"):
		return matching(prefix, prefixed("@", script.LevelNames(lvl)))
	case strings.HasPrefix(prefix, "#"):
		return matching(prefix, prefixed("#", script.LevelTags(lvl)))
	}

	// The flang prompt has no commands in it, and a bare word there is a
	// function or a local rather than anything this package knows about.
	if console.repl {
		return nil
	}
	if word == 0 {
		return matching(prefix, commandNames())
	}

	head, _ := split1(strings.TrimLeft(string(console.line), " \t"))
	switch head {
	case "help":
		return matching(prefix, commandNames())
	case "spawn", "types":
		if word > 1 {
			return nil
		}
		return matching(prefix, world.ScanEntityDefs())
	case "tp", "destroy":
		if word > 1 {
			return nil
		}
		return matching(prefix, append(script.LevelNames(lvl), prefixed("#", script.LevelTags(lvl))...))
	case "find":
		return matching(prefix, script.LevelTags(lvl))
	case "level":
		return matching(prefix, levelNames())
	case "script":
		return matching(prefix, scriptNames())
	}
	return nil
}

// levelNames is every level `level` will load, without its extension.
func levelNames() []string {
	files := levelio.ScanLevelFiles(levelio.LevelsDir())
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, strings.TrimSuffix(filepath.Base(f), filepath.Ext(f)))
	}
	sort.Strings(out)
	return out
}

// scriptNames is every .flang file `script` will run, as `script` spells them.
func scriptNames() []string {
	files := fileutil.ScanScriptFilesIn(fileutil.ScriptsDir())
	out := make([]string, 0, len(files))
	for _, f := range files {
		rel, err := filepath.Rel(fileutil.ScriptsDir(), f)
		if err != nil {
			rel = filepath.Base(f)
		}
		out = append(out, filepath.ToSlash(strings.TrimSuffix(rel, fileutil.ScriptExt)))
	}
	sort.Strings(out)
	return out
}

// matching keeps the candidates that start with prefix, sorted and unique.
func matching(prefix string, all []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(all))
	for _, s := range all {
		if s == "" || seen[s] || !strings.HasPrefix(s, prefix) {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// prefixed puts a sigil on the front of every name.
func prefixed(sigil string, names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = sigil + n
	}
	return out
}

// commonPrefix is the longest string every candidate starts with.
func commonPrefix(all []string) string {
	if len(all) == 0 {
		return ""
	}
	p := all[0]
	for _, s := range all[1:] {
		for !strings.HasPrefix(s, p) {
			p = p[:len(p)-1]
			if p == "" {
				return ""
			}
		}
	}
	return p
}

// listCandidates prints what Tab could not choose between, in columns wide
// enough for the longest of them.
func listCandidates(all []string) {
	shown := all
	if len(shown) > maxListed {
		shown = shown[:maxListed]
	}
	w := 0
	for _, s := range shown {
		if len(s) > w {
			w = len(s)
		}
	}
	const perLine = 4
	var b strings.Builder
	for i, s := range shown {
		if i%perLine == 0 && i > 0 {
			printf(kindDim, "  %s", b.String())
			b.Reset()
		}
		b.WriteString(pad(s, w+2))
	}
	if b.Len() > 0 {
		printf(kindDim, "  %s", strings.TrimRight(b.String(), " "))
	}
	if len(all) > len(shown) {
		printf(kindDim, "  ... and %d more", len(all)-len(shown))
	}
}

func pad(s string, w int) string {
	for len(s) < w {
		s += " "
	}
	return s
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' }
