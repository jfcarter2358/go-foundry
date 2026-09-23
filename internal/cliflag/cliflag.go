package cliflag

// Package cliflag parses a command line whose flags and arguments are mixed.
//
// Go's flag package stops at the first argument that is not a flag, so
//
//	foundry model inspect models/marine.fmdl -pose walk:0.25
//
// parses no flags at all and reports the -pose as a second argument. Every tool
// in the engine takes a path and some flags, and nobody types them in the order
// that happens to work, so they all go through this.

import "flag"

// Parse fills fs from args wherever the flags appear, and answers the arguments
// that are not flags, in order.
//
// It re-parses from just past each argument it takes, which is what lets a flag
// follow one. A flag's own value is never mistaken for an argument, because the
// re-parse starts inside fs's own parsing rather than scanning the words here.
func Parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}
