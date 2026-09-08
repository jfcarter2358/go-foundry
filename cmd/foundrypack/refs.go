package main

// refs.go — which assets a game can actually reach.
//
// The problem: prune a content tree down to what ships, without a schema. Every
// authored format in the engine is YAML or flang source, and between them they
// name assets a dozen different ways — a texture by bare name ("grass"), an
// entity model by an install-relative path ("models/crate.fmdl"), a script by
// name with or without its extension, a UI screen by name, a level a volume
// exits into by name, a level a script loads by name from inside a string.
//
// Teaching this tool each of those shapes would mean a second copy of the
// engine's loaders, kept in step by hand, wrong the first time a field is
// added. So it does not read the formats at all. It builds an index of every
// name any asset could be referred to by, then scans each reachable file for
// tokens and follows the ones that hit the index — transitively, to a fixpoint.
//
// That is a conservative approximation in exactly the direction that is safe.
// A level containing the word "grass" in a comment pulls in grass.png that
// nothing samples: the pack is one texture larger. The failure that matters —
// dropping something the game reaches — needs an asset named by a string the
// content never spells out, which in this engine means a name assembled at
// runtime inside a script. See the note on scanTokens.

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/game"
)

// assetIndex maps every name an asset can be referred to by onto its
// slash-separated path under the content root.
//
// One name can index several files — "default" is both textures/default.png and
// potentially a level called default.yaml — so the value is a set. Following a
// reference pulls in all of them, which is the generous direction.
type assetIndex map[string]map[string]bool

// textExts are the formats worth scanning for references. Everything else in a
// content tree is a leaf: a PNG, a font, a sound, a packed texture.
var textExts = map[string]bool{
	".yaml":  true, // levels, entity definitions
	".yml":   true,
	".fui":   true, // UI screens
	".fth":   true, // UI themes
	".fpar":  true, // particle effects
	".fmdl":  true, // models
	".flang": true,
	".obj":   true, // model source, names its .mtl
	".mtl":   true, // and that names its textures
}

// indexAssets builds the reference index over the given content directories.
func indexAssets(src string, dirs []string) (assetIndex, error) {
	idx := assetIndex{}
	for _, dir := range dirs {
		root := filepath.Join(src, dir)
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, rerr := filepath.Rel(src, path)
			if rerr != nil {
				return rerr
			}
			for _, name := range referenceNames(filepath.ToSlash(rel)) {
				if idx[name] == nil {
					idx[name] = map[string]bool{}
				}
				idx[name][filepath.ToSlash(rel)] = true
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return idx, nil
}

// referenceNames is every string the content might use to name this file.
//
// The four shapes the engine's own resolvers accept, which is what makes this
// list a description rather than a guess: fileutil.ScriptPath takes a name with
// or without its extension and with or without its directory prefix;
// fileutil.ResolveAssetPath takes an install-relative path; gfx texture names
// are bare; levelio.LevelPath takes a name or a filename.
func referenceNames(slash string) []string {
	base := filepath.Base(slash)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	dir := filepath.Dir(slash) // "textures", "scripts/doors", ...

	names := []string{
		slash,            // textures/grass.png
		base,             // grass.png
		stem,             // grass
		dir + "/" + stem, // textures/grass
	}
	// A script in a subdirectory is written relative to scripts/, so
	// "doors/sliding" has to resolve as well as "scripts/doors/sliding".
	if i := strings.IndexByte(slash, '/'); i >= 0 {
		inner := slash[i+1:]
		names = append(names, inner, strings.TrimSuffix(inner, filepath.Ext(inner)))
	}
	// A normal map is never named by content: the engine derives its name from
	// the albedo's and loads it alongside (gfx.ensureNormalMapLoaded). So it
	// answers to the albedo's names too — otherwise every texture's relief
	// would be pruned out of a release for looking unreferenced.
	if base := strings.TrimSuffix(stem, fileutil.NormalMapSuffix); base != stem {
		names = append(names, base, dir+"/"+base, base+filepath.Ext(base))
	}
	return names
}

// manifestRoots is what a game reaches before any content says anything.
//
// The start level and the start screen come from the manifest. The rest are
// named from Go and would be invisible to a walk: internal/ui always reads the
// active theme, internal/app puts up a screen called "loading" over every level
// change, and Escape puts up one called "pause". Dropping any of them produces
// a game that boots and then breaks later — at the first level change, or the
// first time the player reaches for the pause menu — which is the worst kind of
// packing bug, since it survives every test that only checks the game starts.
func manifestRoots(m game.Manifest) []string {
	roots := []string{
		"levels/" + m.StartLevel + ".yaml",
		"ui/theme.fth",
		"ui/loading.fui",
		"ui/pause.fui",
		// The fallback every kind of placed object falls back to when its own
		// texture is missing (world.DefaultSpriteTexture and friends).
		"textures/default.png",
	}
	if m.StartScreen != "" {
		roots = append(roots, "ui/"+m.StartScreen+".fui")
	}
	return roots
}

// reachable walks out from roots and returns the set of asset paths the game
// can get to, as slash-separated paths under the content root.
func reachable(src string, idx assetIndex, roots []string) map[string]bool {
	keep := map[string]bool{}

	// A root is a path rather than a name, but it is still resolved through the
	// index so that a manifest naming a level that does not exist fails at boot
	// with the engine's own message rather than here with a worse one.
	var frontier []string
	for _, r := range roots {
		for path := range idx[r] {
			if !keep[path] {
				keep[path] = true
				frontier = append(frontier, path)
			}
		}
	}

	for len(frontier) > 0 {
		path := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]

		if !textExts[strings.ToLower(filepath.Ext(path))] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(path)))
		if err != nil {
			continue
		}
		for _, tok := range scanTokens(string(data)) {
			for hit := range idx[tok] {
				if !keep[hit] {
					keep[hit] = true
					frontier = append(frontier, hit)
				}
			}
		}
	}
	return keep
}

// tokenRe is what could be an asset name in a text file: a run of the
// characters an asset path is allowed to contain.
//
// Deliberately not "quoted strings only". YAML says `texture: grass` far more
// often than it says `texture: "grass"`, and a scan that only saw quoted values
// would miss most of a level file. The cost of the looser rule is that a bare
// word in a comment can pull an asset in, which is the harmless direction.
var tokenRe = regexp.MustCompile(`[A-Za-z0-9_./-]+`)

// scanTokens returns the candidate asset names in a file.
//
// Every token, not just the ones in a position where a reference is legal:
// there is no parse here to establish position, and there does not need to be —
// an index hit on a name that happens to be a texture is a texture that ships.
//
// What this cannot see is a name a script builds at runtime, `load_level("lvl_"
// + n)`. There is no static analysis that would; a project doing that names the
// levels in its manifest's reach some other way, or packs without -prune. The
// per-directory prune counts this tool logs are how anyone would notice.
func scanTokens(s string) []string {
	return tokenRe.FindAllString(s, -1)
}
