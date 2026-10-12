package phimport

// provenance.go — polyhaven.yaml, written beside every import: what the
// asset is, who made it, its licence, where every file came from (URL, md5,
// size), when it was fetched, and what the import made of it.
//
// Poly Haven's assets are CC0 and need no attribution; the record is for
// the project's own bookkeeping (and the credits screen a game may want to
// write anyway). The importer also reads the previous record back to skip
// re-converting textures whose sources have not changed (TextureSources).

import (
	"os"
	"path/filepath"
	"time"

	"foundry/internal/polyhaven"

	"gopkg.in/yaml.v2"
)

// Provenance is polyhaven.yaml.
type Provenance struct {
	Source         string    `yaml:"source"`
	ID             string    `yaml:"id"`
	Name           string    `yaml:"name"`
	Type           string    `yaml:"type"`
	Page           string    `yaml:"page"`
	License        string    `yaml:"license"`
	Credit         string    `yaml:"credit"`
	Authors        []string  `yaml:"authors"`
	Categories     []string  `yaml:"categories,flow"`
	Tags           []string  `yaml:"tags,flow"`
	Dimensions     []float64 `yaml:"dimensions_mm,omitempty,flow"`
	Polycount      int       `yaml:"polycount,omitempty"`
	Resolution     string    `yaml:"resolution"`
	Published      string    `yaml:"published,omitempty"`
	Downloaded     string    `yaml:"downloaded"`
	Files          []ProvFile
	Import         *ProvImport   `yaml:"import,omitempty"`
	TextureSet     *ProvTexSet   `yaml:"texture_set,omitempty"`
	TextureSources []ProvTexture `yaml:"texture_sources,omitempty"`
}

// ProvFile is one downloaded file.
type ProvFile struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
	MD5  string `yaml:"md5"`
	Size int64  `yaml:"size"`
}

// ProvImport is what a model import made.
type ProvImport struct {
	Triangles      int        `yaml:"triangles"`
	Extent         [3]float32 `yaml:"extent,flow"`
	Back           bool       `yaml:"back"`
	BackConfidence float64    `yaml:"back_confidence"`
	Yaw            float64    `yaml:"yaw_degrees"`
	Textures       []string   `yaml:"textures"`
	Cutout         []string   `yaml:"cutout,omitempty"`
	Meshes         []ProvMesh `yaml:"meshes"`
	Warnings       []string   `yaml:"warnings,omitempty"`
}

// ProvMesh is one cooked variant.
type ProvMesh struct {
	File      string `yaml:"file"`
	Triangles int    `yaml:"triangles"`
	Collision string `yaml:"collision"`
}

// ProvTexSet is what a texture-set import made.
type ProvTexSet struct {
	Size     float64           `yaml:"size_m"`
	Textures map[string]string `yaml:"textures"`
}

// ProvTexture is one converted texture and its sources' key.
type ProvTexture struct {
	Texture string `yaml:"texture"`
	Sources string `yaml:"sources"`
}

func (p *Provenance) fill(a *polyhaven.Asset, res, typ string) {
	p.Source = "polyhaven"
	p.ID, p.Name, p.Type = a.ID, a.Name, typ
	p.Page = "https://polyhaven.com/a/" + a.ID
	p.License = polyhaven.License
	p.Credit = polyhaven.Credit
	p.Authors = a.AuthorList()
	p.Categories, p.Tags = a.Categories, a.Tags
	p.Dimensions, p.Polycount = a.Dimensions, a.Polycount
	p.Resolution = res
	if a.DatePublished > 0 {
		p.Published = time.Unix(a.DatePublished, 0).UTC().Format("2006-01-02")
	}
	p.Downloaded = time.Now().UTC().Format(time.RFC3339)
}

// Write saves the record.
func (p *Provenance) Write(path string) error {
	data, err := yaml.Marshal(p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ReadProvenance reads a record (nil, err when there is none).
func ReadProvenance(path string) (*Provenance, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Provenance
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
