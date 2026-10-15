package world

// volume_grass.go — a grass field: one terrain tile's Poly Haven grass,
// drawn GPU-instanced by render/grass*.go (docs/regions/PRESETS_2B.md).
//
// A generator's grass_field stage writes each terrain tile's instances to a
// binary file (internal/grassfield, .fgrass) and places ONE volume per tile
// naming it — not tens of thousands of scenery placements:
//
//	volumes:
//	  - id: gen.p_prairie_world.r_main_grass.grass_3_4
//	    position: [-128, 6, 64]        # the tile's box: what the renderer
//	    half: [64, 9, 64]              # culls the whole field by
//	    grass:
//	      file: generated/p_prairie_world/r_main_grass/grass_3_4.fgrass
//	      chunk: auto
//
// The volume's box is the field's bounds (the ground under it and the
// tallest clump on it), which is what picking and the editor's cage read;
// the cells inside the file are culled on their own. CHUNK streams the field
// with a chunk's switch, as a terrain tile's scenery does: `auto` takes the
// chunk whose footprint holds the volume's position (SceneryChunkAt, the
// rule `chunk: auto` scenery is resolved by), "" is always in.
//
// Like a region, a grass volume is not a trigger: it has no script and
// nothing collides with it. The field itself is drawing only — grass does
// not collide, block navigation or take decals.

// VolumeGrass makes a volume a grass field.
type VolumeGrass struct {
	// File is the field, resources-relative.
	File string
	// Chunk is the chunk whose switch the field streams with: a chunk ID,
	// GrassChunkAuto, or "" for always in.
	Chunk string
}

// GrassChunkAuto is VolumeGrass.Chunk's "the chunk under the field".
const GrassChunkAuto = "auto"

// RawVolumeGrass is the YAML form.
type RawVolumeGrass struct {
	// File is the grass field (.fgrass), resources-relative.
	File string `yaml:"file"`
	// Chunk streams the field with a chunk's switch: a chunk ID, auto (the
	// chunk under the volume) or empty for always in.
	Chunk string `yaml:"chunk,omitempty"`
}

// VolumeGrassFromRaw converts the YAML form; nil stays nil.
func VolumeGrassFromRaw(r *RawVolumeGrass) *VolumeGrass {
	if r == nil {
		return nil
	}
	return &VolumeGrass{File: r.File, Chunk: r.Chunk}
}

// VolumeGrassToRaw is the inverse.
func VolumeGrassToRaw(g *VolumeGrass) *RawVolumeGrass {
	if g == nil {
		return nil
	}
	return &RawVolumeGrass{File: g.File, Chunk: g.Chunk}
}

// IsGrass reports whether the volume is a grass field.
func (v *Volume) IsGrass() bool { return v != nil && v.Grass != nil && v.Grass.File != "" }

// GrassChunk is the chunk the field streams with: its own, the one under it
// for GrassChunkAuto, or nil (always in).
func (v *Volume) GrassChunk(lvl *Level) *Chunk {
	if !v.IsGrass() || lvl == nil {
		return nil
	}
	id := v.Grass.Chunk
	if id == GrassChunkAuto {
		id = SceneryChunkAt(lvl, v.Position)
	}
	if id == "" {
		return nil
	}
	for i := range lvl.Chunks {
		if lvl.Chunks[i].ID == id {
			return &lvl.Chunks[i]
		}
	}
	return nil
}

// GrassVolumes is the level's grass fields, in level order.
func GrassVolumes(lvl *Level) []*Volume {
	if lvl == nil {
		return nil
	}
	var out []*Volume
	for i := range lvl.Volumes {
		if lvl.Volumes[i].IsGrass() {
			out = append(out, &lvl.Volumes[i])
		}
	}
	return out
}
