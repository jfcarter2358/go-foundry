package pack

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
	"foundry/internal/nanite"
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
	".yaml": true, // levels, entity definitions
	".yml":  true,
	".fui":  true, // UI screens
	".fth":  true, // UI themes
	".fpar": true, // particle effects
	".fai":  true, // AI decision trees, which name the animations and effects
	//                their nodes play, and the tags they send to
	".fmat":   true, // materials, which name the textures their effects use
	".fbio":   true, // terrain biomes, which name their ground textures
	".fmdl":   true, // models, which name the skeleton they are rigged to
	".fskl":   true, // skeletons, whose clips name nothing but are scanned for consistency
	".flang":  true,
	".ftrk":   true, // tracker songs, which name their library samples and instruments
	".finst":  true, // library instruments, which name their samples
	".fscore": true, // notated scores, which name their library instruments
	".fgen":   true, // procedural generators, which name textures and their level
	".obj":    true, // model source, names its .mtl
	".mtl":    true, // and that names its textures
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
			// A backup answers to its original's names — enemy.yaml.bak's stem
			// is enemy.yaml — so indexing one would make every reference to
			// the original reach the backup too. Nothing loads a backup.
			if IsDevelopmentFile(d.Name()) {
				return nil
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
	// A library sample is named by songs relative to the library root —
	// "drums/kick_808" for sounds/samples/drums/kick_808.wav — and its .fsmp
	// sidecar answers to the same names, so the two always ship together.
	if rel, ok := strings.CutPrefix(slash, trackerSamplesDir); ok {
		names = append(names, rel, strings.TrimSuffix(rel, filepath.Ext(rel)))
	}
	// A library instrument is named the same way, relative to its library:
	// a song slot's `ref: strings/violin_section` is
	// sounds/instruments/strings/violin_section.finst.
	if rel, ok := strings.CutPrefix(slash, trackerInstrumentsDir); ok {
		names = append(names, rel, strings.TrimSuffix(rel, filepath.Ext(rel)))
	}
	// A normal map is never named by content: the engine derives its name from
	// the albedo's and loads it alongside (gfx.ensureNormalMapLoaded). So it
	// answers to the albedo's names too — otherwise every texture's relief
	// would be pruned out of a release for looking unreferenced.
	if base := strings.TrimSuffix(stem, fileutil.NormalMapSuffix); base != stem {
		names = append(names, base, dir+"/"+base, base+filepath.Ext(base))
	}
	// A splat layer's height and AO maps are the same: the material names the
	// albedo and the engine finds <name>_height and <name>_ao beside it
	// (gfx.SplatArraysFor, docs/canyon/PHASE_5.md).
	for _, suf := range []string{"_height", "_ao"} {
		if base := strings.TrimSuffix(stem, suf); base != stem && dir == fileutil.TexturesDirName {
			names = append(names, base, dir+"/"+base)
		}
	}
	// A cubemap face is named by its set's base: `sky_cubemap = "dusk"` in a
	// script, or the editor's base-name field, becomes dusk_px, dusk_nx, ...
	// (world.Sky.SetFacesFromBase). Nothing spells the face names out, so a
	// sky only a script switches to was pruned out of every release.
	for _, suf := range cubemapFaceSuffixes {
		if base := strings.TrimSuffix(stem, suf); base != stem && base != "" {
			names = append(names, base, dir+"/"+base)
		}
	}
	return names
}

// cubemapFaceSuffixes mirror world.SkyFaceSuffix, spelled out here for the
// same reason as LevelSourceExt: this tool does not depend on the engine.
var cubemapFaceSuffixes = []string{"_px", "_nx", "_py", "_ny", "_pz", "_nz"}

// trackerSamplesDir and trackerInstrumentsDir mirror tracker.SamplesDir and
// tracker.InstrumentsDir under the content root.
const (
	trackerSamplesDir     = "sounds/samples/"
	trackerInstrumentsDir = "sounds/instruments/"
)

// manifestRoots is what a game reaches before any content says anything.
//
// The start level comes from the manifest; a main menu is found like any other
// screen, through the level that lists it or the script that shows it. The rest
// are named from Go and would be invisible to a walk: internal/ui always reads the
// active theme, internal/app puts up a screen called "loading" over every level
// change, and Escape puts up one called "pause". Dropping any of them produces
// a game that boots and then breaks later — at the first level change, or the
// first time the player reaches for the pause menu — which is the worst kind of
// packing bug, since it survives every test that only checks the game starts.
func manifestRoots(m game.Manifest) []string {
	return []string{
		"levels/" + m.StartLevel + ".yaml",
		"ui/theme.fth",
		"ui/loading.fui",
		"ui/pause.fui",
		// The fallback every kind of placed object falls back to when its own
		// texture is missing (world.DefaultSpriteTexture and friends).
		"textures/default.png",
	}
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

		// A level's compiled form is what a release build actually loads, and
		// nothing NAMES it: the manifest and every load_level say a level's
		// name, and the extension is chosen by the build. So it is pulled in
		// beside the source rather than found by the scan below.
		if compiled, ok := compiledSibling(path); ok {
			if _, err := os.Stat(filepath.Join(src, filepath.FromSlash(compiled))); err == nil {
				keep[compiled] = true
			}
		}

		// A cooked Nanite mesh is binary, and names the textures of its
		// material slots inside it: read them out rather than scan for them,
		// or every cooked rock would ship untextured.
		if nanite.IsCookedPath(path) {
			if m, err := nanite.Read(filepath.Join(src, filepath.FromSlash(path))); err == nil {
				for _, sl := range m.Slots {
					for hit := range idx[sl.Texture] {
						if !keep[hit] {
							keep[hit] = true
							frontier = append(frontier, hit)
						}
					}
				}
			}
			continue
		}

		// The grass instancer's files are binary too (internal/grassfield): a
		// field names its kit, a kit its clumps' textures.
		if names := grassRefs(filepath.Join(src, filepath.FromSlash(path))); names != nil {
			for _, n := range names {
				for hit := range idx[n] {
					if !keep[hit] {
						keep[hit] = true
						frontier = append(frontier, hit)
					}
				}
			}
			continue
		}

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
		for _, pat := range scanNamePatterns(string(data)) {
			for name, paths := range idx {
				if !pat.matches(name) {
					continue
				}
				for hit := range paths {
					if !keep[hit] {
						keep[hit] = true
						frontier = append(frontier, hit)
					}
				}
			}
		}
	}
	return keep
}

// namePattern is a name a script assembles at runtime, as the fixed pieces
// around its holes: format("ammo_0{}", n) is ["ammo_0", ""], and
// "pickup_" + kind is ["pickup_", ""].
type namePattern []string

// minPatternFixed is how many fixed characters a pattern needs before it is
// believed. "{}_{}" or "_" + x would otherwise reach every asset with an
// underscore in its name — generous, but no longer telling anyone anything.
const minPatternFixed = 3

// stringLitRe is a double-quoted flang string, escapes and all.
var stringLitRe = regexp.MustCompile(`"((?:[^"\\\n]|\\.)*)"`)

// nameCharsRe is a piece that could be part of an asset name.
var nameCharsRe = regexp.MustCompile(`^[A-Za-z0-9_./-]*$`)

// scanNamePatterns finds the names a file builds rather than spells out: the
// string a format() fills in, and a string concatenated with something. This
// is the hole scanTokens leaves — a counter swapping `format("ammo_{}", n)`
// textures names none of them — and it is a pattern match on the source, not
// an evaluation, so it only sees a name whose fixed part is written down.
func scanNamePatterns(s string) []namePattern {
	var out []namePattern
	for _, loc := range stringLitRe.FindAllStringSubmatchIndex(s, -1) {
		lit := s[loc[2]:loc[3]]
		var pat namePattern
		switch {
		case strings.Contains(lit, "{}"):
			pat = strings.Split(lit, "{}")
		case strings.HasPrefix(strings.TrimLeft(s[loc[1]:], " \t"), "+"):
			pat = namePattern{lit, ""}
		case strings.HasSuffix(strings.TrimRight(s[:loc[0]], " \t"), "+"):
			pat = namePattern{"", lit}
		default:
			continue
		}
		fixed := 0
		ok := true
		for _, piece := range pat {
			fixed += len(piece)
			ok = ok && nameCharsRe.MatchString(piece)
		}
		if ok && fixed >= minPatternFixed {
			out = append(out, pat)
		}
	}
	return out
}

// matches reports whether name could be what the pattern builds: its pieces
// in order, the first at the start and the last at the end, with room for
// something in every hole.
func (p namePattern) matches(name string) bool {
	if len(p) < 2 || !strings.HasPrefix(name, p[0]) {
		return false
	}
	rest := name[len(p[0]):]
	last := p[len(p)-1]
	if !strings.HasSuffix(rest, last) || len(rest) <= len(last) {
		return false
	}
	rest = rest[:len(rest)-len(last)]
	for _, mid := range p[1 : len(p)-1] {
		i := strings.Index(rest, mid)
		if i <= 0 {
			return false
		}
		rest = rest[i+len(mid):]
	}
	return len(rest) > 0
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

// LevelSourceExt and CompiledLevelExt are what an authored level and its
// compiled form are called. Spelled out here rather than imported from
// internal/levelio, which this tool deliberately does not depend on — it packs
// a tree of files and should not need the engine to do it.
const (
	LevelSourceExt   = ".yaml"
	CompiledLevelExt = ".fbsp"
	levelsDir        = "levels/"
)

// compiledSibling is the compiled form of an authored level path, and whether
// the path was one at all. Entity definitions and the manifest are .yaml too,
// so the directory is part of the test.
func compiledSibling(path string) (string, bool) {
	if !strings.HasPrefix(path, levelsDir) || !strings.HasSuffix(path, LevelSourceExt) {
		return "", false
	}
	return strings.TrimSuffix(path, LevelSourceExt) + CompiledLevelExt, true
}

// IsSupersededLevelSource reports whether a path is an authored level that has
// been compiled, and so should not be staged.
//
// A release build cannot read one — it loads .fbsp and only .fbsp
// (app/levelsource_release.go) — so shipping it is dead weight and, less
// happily, ships the level's source to whoever installs the game. A level with
// no compiled form beside it is still staged: the failure then belongs to the
// engine's own "recompile the level" message rather than to a packer that
// silently dropped the only copy.
func IsSupersededLevelSource(src, path string) bool {
	compiled, ok := compiledSibling(path)
	if !ok {
		return false
	}
	_, err := os.Stat(filepath.Join(src, filepath.FromSlash(compiled)))
	return err == nil
}
