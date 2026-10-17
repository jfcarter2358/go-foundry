package phimport

// plant_parts.go — an imported plant's pieces as meshes, for a consumer that
// is not the Nanite cook: the grass instancer (procgen/stages grass_field,
// docs/regions/PRESETS_2B.md) builds its own small LOD meshes from the same
// source the import cooked.
//
// ImportModel leaves the re-pointed glTF (and its buffers) beside the cooked
// meshes in models/polyhaven/<id>/; this reads it back, splits it into the
// same parts the import cooked (splitRoots, the same suffixes) and puts each
// into the same placement frame (Normalise, no back: a plant's), so a part
// here is exactly the mesh the import cooked as <id>_<suffix> before any
// decimation. Nothing is downloaded: an asset that was never imported is an
// error.

import (
	"fmt"
	"os"
	"path/filepath"

	"foundry/internal/model"
)

// PlantPart is one part of an imported plant.
type PlantPart struct {
	// Name is the variant name the import gave it: <id>_<suffix>, or <id>
	// for a file of one part (rocks.VariantMeta.Name, before any _lite).
	Name string
	Mesh *model.ImportMesh
}

// LoadPlantParts reads an imported asset's parts from resources.
func LoadPlantParts(resources, id string) ([]PlantPart, error) {
	dir := filepath.Join(resources, filepath.FromSlash(ModelDir(id)))
	gpath := filepath.Join(dir, id+".gltf")
	gjson, err := os.ReadFile(gpath)
	if err != nil {
		return nil, fmt.Errorf("%s is not imported: %w", id, err)
	}
	var parts []PlantPart
	ps, err := splitRoots(id, gjson, dir)
	if err != nil {
		return nil, err
	}
	if len(ps) > 1 {
		for _, p := range ps {
			parts = append(parts, PlantPart{Name: id + "_" + p.suffix, Mesh: p.m})
		}
	} else {
		m, err := model.LoadGLTF(gpath)
		if err != nil {
			return nil, err
		}
		parts = []PlantPart{{Name: id, Mesh: m}}
	}
	for _, p := range parts {
		if _, err := Normalise(p.Mesh, "none", false); err != nil {
			return nil, err
		}
	}
	return parts, nil
}
