package impostor

// write.go — where a bake goes and how a later run knows it is current.
//
// An impostor sits beside the cooked mesh it was baked from, as its own
// cooked mesh, <stem>_imp.fnm, with a provenance file <stem>_imp.yaml; its
// atlas is an ordinary project texture, textures/ph_imp_<stem>_cov.png with
// its normal map beside it (<name>_normal.png, the engine's convention:
// gfx/texture_normal.go). The provenance records the bake's Version, the
// mesh's SourceHash and the options, so a rerun bakes again only when one
// of them changed. Cooking the cards is the caller's (nanitecook, which this
// package stays below).

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/nanite"

	"gopkg.in/yaml.v2"
)

// Paths are where the impostor of the cooked mesh at model (resources-
// relative) lives: its cooked cards and provenance (resources-relative) and
// its texture's name.
type Paths struct {
	Model, Provenance, Texture string
}

// PathsFor is model's impostor's paths.
func PathsFor(model string) Paths {
	stem := strings.TrimSuffix(model, filepath.Ext(model))
	base := filepath.Base(stem)
	return Paths{Model: stem + "_imp" + nanite.Ext, Provenance: stem + "_imp.yaml", Texture: "ph_imp_" + base + "_cov"}
}

// provenance is the <stem>_imp.yaml.
type provenance struct {
	Version    int     `yaml:"version"`
	SourceHash string  `yaml:"source_hash"`
	Texture    string  `yaml:"texture"`
	Options    Options `yaml:"options"`
}

func provenanceOf(m *nanite.Mesh, tex string, o Options) provenance {
	return provenance{Version: Version, SourceHash: hex.EncodeToString(m.SourceHash[:]), Texture: tex, Options: o.withDefaults()}
}

// Current reports whether the impostor at p (under resources/) was baked
// from m, at this Version, with opt, and its files are all there.
func Current(p Paths, m *nanite.Mesh, opt Options) bool {
	data, err := os.ReadFile(fileutil.ResolveAssetPath(p.Provenance))
	if err != nil {
		return false
	}
	var have provenance
	if yaml.Unmarshal(data, &have) != nil || have != provenanceOf(m, p.Texture, opt) {
		return false
	}
	for _, f := range []string{fileutil.ResolveAssetPath(p.Model), texturePath(p.Texture), texturePath(fileutil.NormalMapName(p.Texture))} {
		if _, err := os.Stat(f); err != nil {
			return false
		}
	}
	return true
}

func texturePath(name string) string { return filepath.Join(fileutil.TexturesDir(), name+".png") }

// WriteTextures writes r's atlas and normal map as the textures p names.
func WriteTextures(r *Result, p Paths) error {
	for _, w := range []struct {
		name string
		img  *image.NRGBA
	}{{p.Texture, r.Albedo}, {fileutil.NormalMapName(p.Texture), r.Normal}} {
		var buf bytes.Buffer
		if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, w.img); err != nil {
			return fmt.Errorf("impostor %s: %w", w.name, err)
		}
		if err := fileutil.WriteFileAtomicNoBackup(texturePath(w.name), buf.Bytes()); err != nil {
			return fmt.Errorf("impostor %s: %w", w.name, err)
		}
	}
	return nil
}

// WriteProvenance records that the impostor at p was baked from m with opt;
// written last, once the cards are cooked and the textures written.
func WriteProvenance(p Paths, m *nanite.Mesh, opt Options) error {
	data, err := yaml.Marshal(provenanceOf(m, p.Texture, opt))
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomicNoBackup(fileutil.ResolveAssetPath(p.Provenance), data)
}

// TexturesFrom is a Textures reading the project's textures/<name>.png.
func TexturesFrom() Textures {
	return func(name string) (image.Image, error) {
		f, err := os.Open(texturePath(name))
		if err != nil {
			return nil, err
		}
		defer f.Close()
		img, _, err := image.Decode(f)
		return img, err
	}
}
