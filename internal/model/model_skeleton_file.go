package model

// model_skeleton_file.go — the .fskl skeleton document format.
//
// The same schema a .fmdl used to hold inline: RawModelBone and RawModelClip
// are shared with model_file.go rather than redeclared, so a rig lifted out of
// a model round-trips byte-for-byte and the two formats cannot drift apart.
//
// It lives beside the models it serves, in resources/skeletons/, because a
// skeleton is an asset the content names — a .fmdl says `skeleton: human` and
// the packer follows that token like any other reference (pack/refs.go).

import (
	"fmt"
	"foundry/internal/fileutil"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v2"
)

const SkeletonsDirName = "skeletons"

// SkeletonExt is the skeleton document extension; SkeletonsDir is where the
// editor's open/save dialogs look.
const SkeletonExt = ".fskl"

func SkeletonsDir() string { return filepath.Join(fileutil.ResourcesPath, SkeletonsDirName) }

// SkeletonPath is where the skeleton called name lives. The name is bare — a
// model links "human", not "skeletons/human.fskl" — for the same reason a brush
// face names a bare texture: it is what the user types, and one place decides
// where that lands on disk.
func SkeletonPath(name string) string { return filepath.Join(SkeletonsDir(), name+SkeletonExt) }

// RawSkeleton is the on-disk form of a SkeletonDoc.
type RawSkeleton struct {
	ID         string         `yaml:"id"`
	Bones      []RawModelBone `yaml:"bones"`
	Animations []RawModelClip `yaml:"animations,omitempty"`
}

// skeletonToRaw converts a skeleton for serialization.
func skeletonToRaw(s *SkeletonDoc) RawSkeleton {
	raw := RawSkeleton{ID: s.ID}
	raw.Bones = rawBonesFrom(s.Bones)
	raw.Animations = rawClipsFrom(s.Animations, s.Bones)
	return raw
}

// rawToSkeleton rebuilds a skeleton from its on-disk form. Assumes the raw data
// has already passed validateRawSkeleton.
func rawToSkeleton(raw RawSkeleton) *SkeletonDoc {
	s := &SkeletonDoc{ID: raw.ID}
	s.Bones = bonesFromRaw(raw.Bones)
	s.Animations = clipsFromRaw(raw.Animations, s.Bones)
	return s
}

// validateRawSkeleton rejects a rig the mesh code could not walk, on the same
// terms as an inline armature: no cycles, no duplicate names, no zero-length
// bone, no clip keyed to a bone that is not here.
//
// A skeleton with no bones is refused outright. An empty .fmdl is a model
// somebody has not started; an empty .fskl is a link every model pointing at it
// would resolve to nothing, and the failure would show up as characters in
// their rest pose rather than as a file that would not load.
func validateRawSkeleton(raw RawSkeleton) error {
	if len(raw.Bones) == 0 {
		return fmt.Errorf("skeleton %q has no bones", raw.ID)
	}
	if err := validateRawBones(raw.ID, raw.Bones); err != nil {
		return err
	}
	return validateRawClips(raw.ID, raw.Bones, raw.Animations)
}

// SkeletonFingerprint identifies the skeleton's content as SaveSkeleton would
// write it, for comparing against what was last saved. See fileutil.Fingerprint.
func SkeletonFingerprint(s *SkeletonDoc) uint64 {
	if s == nil {
		return 0
	}
	return fileutil.Fingerprint(skeletonToRaw(s))
}

// SaveSkeleton writes the skeleton to path as YAML.
func SaveSkeleton(s *SkeletonDoc, path string) error {
	if s == nil {
		return fmt.Errorf("save skeleton: no document")
	}
	raw := skeletonToRaw(s)
	if err := validateRawSkeleton(raw); err != nil {
		return err
	}
	data, err := yaml.Marshal(&raw)
	if err != nil {
		return fmt.Errorf("marshal skeleton %q: %w", s.ID, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create skeleton directory: %w", err)
	}
	if err := fileutil.WriteFileAtomic(path, data); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// LoadSkeleton reads a .fskl document.
func LoadSkeleton(path string) (*SkeletonDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var raw RawSkeleton
	if err := decodeYAMLCached(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := validateRawSkeleton(raw); err != nil {
		return nil, err
	}
	s := rawToSkeleton(raw)
	s.Path = path
	if s.ID == "" {
		s.ID = trimModelExt(filepath.Base(path))
	}
	return s, nil
}

// SkeletonPathFor is where the skeleton called name lives for the model at
// modelPath.
//
// Usually that is SkeletonPath — the editor and the game both run with
// fileutil.ResourcesPath pointing at the project. The command-line tools do not:
// they are handed a model by path and rewrite it in place, often with no
// resources directory configured at all, and a rigged model that only resolved
// through the global would fail to load for every one of them.
//
// So a model sitting in a folder called "models" looks for its skeleton in the
// "skeletons" folder beside it first. The configured tree is the fallback, and
// the one used for a model that is somewhere else entirely.
func SkeletonPathFor(modelPath, name string) string {
	sib := siblingSkeletonPath(modelPath, name)
	if sib == "" {
		return SkeletonPath(name)
	}
	if fileExists(sib) {
		return sib
	}
	// Neither exists yet (a first save): the configured tree wins if it has one
	// already, so a tool writing into a project does not start a second folder
	// of skeletons beside the first.
	if cfg := SkeletonPath(name); fileExists(cfg) {
		return cfg
	}
	return sib
}

// siblingSkeletonPath is resources/skeletons/<name>.fskl as implied by a model
// in resources/models, or "" when modelPath is not laid out that way.
func siblingSkeletonPath(modelPath, name string) string {
	if modelPath == "" {
		return ""
	}
	dir := filepath.Dir(modelPath)
	if filepath.Base(dir) != ModelsDirName {
		return ""
	}
	return filepath.Join(filepath.Dir(dir), SkeletonsDirName, name+SkeletonExt)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// LoadSkeletonNamed reads the skeleton the model at modelPath links. The error
// names the link rather than the path, because "skeleton human" is what the
// .fmdl says and what the person editing it can act on.
func LoadSkeletonNamed(modelPath, name string) (*SkeletonDoc, error) {
	s, err := LoadSkeleton(SkeletonPathFor(modelPath, name))
	if err != nil {
		return nil, fmt.Errorf("skeleton %q: %w", name, err)
	}
	s.ID = name
	return s, nil
}

// ScanSkeletonFiles lists the .fskl documents available to the open dialog.
func ScanSkeletonFiles() []string { return fileutil.ScanFilesByExt(SkeletonsDir(), SkeletonExt) }

// SkeletonNames is ScanSkeletonFiles with the extension trimmed — the form a
// .fmdl's `skeleton:` field and the editor's picker both speak.
func SkeletonNames() []string {
	files := ScanSkeletonFiles()
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, trimModelExt(f))
	}
	return out
}
