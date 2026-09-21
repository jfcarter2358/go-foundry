package setup

// stamp.go — reading the .foundrypack an install tree carries.
//
// internal/pack writes it as shell assignments, for the Linux installer
// scripts to source: KEY='value', with an apostrophe written '\''. This is the
// one reader of it that is not a shell.

import (
	"fmt"
	"strings"
)

// StampName is the stamp's name at the top of an install tree.
const StampName = ".foundrypack"

// Stamp is what an install tree says about itself.
type Stamp struct {
	Title   string
	Version string
	Exe     string
	DataDir string
	// Kind is "" for a game and KindEditor for the editor itself, installed
	// from a Foundry release (make installer-editor). Only the wording differs:
	// the editor keeps nothing in its install folder either — a project lives
	// wherever its author put it — so both are replaced whole on an upgrade.
	Kind string
}

// KindEditor is an editor install's KIND — pack.KindEditor, which this package
// does not import: the packer has no business in the installer program.
const KindEditor = "editor"

// IsEditor reports whether the stamp is an editor install's.
func (s Stamp) IsEditor() bool { return s.Kind == KindEditor }

// ParseStamp reads a .foundrypack.
//
// The executable and the data directory each have to be a single name. The
// installer joins both onto folders it deletes from, and a stamp that could
// say `..` would be a way to point an uninstall at the parent of the saves.
func ParseStamp(data []byte) (Stamp, error) {
	var s Stamp
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		key, raw, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		val, err := unquoteShell(raw)
		if err != nil {
			return Stamp{}, fmt.Errorf("%s: %s: %w", StampName, key, err)
		}
		switch key {
		case "TITLE":
			s.Title = val
		case "VERSION":
			s.Version = val
		case "EXE":
			s.Exe = val
		case "DATA_DIR":
			s.DataDir = val
		case "KIND":
			s.Kind = val
		}
	}
	if s.Title == "" || s.Exe == "" {
		return Stamp{}, fmt.Errorf("%s names no title or no executable", StampName)
	}
	for what, name := range map[string]string{"executable": s.Exe, "data directory": s.DataDir} {
		if name == "" && what == "data directory" {
			continue
		}
		if !singleName(name) {
			return Stamp{}, fmt.Errorf("%s: the %s %q is not a single file name", StampName, what, name)
		}
	}
	return s, nil
}

func singleName(name string) bool {
	return name != "." && name != ".." && !strings.ContainsAny(name, `/\:`)
}

// unquoteShell undoes shell quoting of the kind pack's shellQuote writes.
func unquoteShell(s string) (string, error) {
	var b strings.Builder
	quoted := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quoted && c == '\'':
			quoted = false
		case quoted:
			b.WriteByte(c)
		case c == '\'':
			quoted = true
		case c == '\\' && i+1 < len(s):
			i++
			b.WriteByte(s[i])
		default:
			b.WriteByte(c)
		}
	}
	if quoted {
		return "", fmt.Errorf("unterminated quote")
	}
	return b.String(), nil
}
