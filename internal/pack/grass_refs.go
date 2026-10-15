package pack

// grass_refs.go — what the grass instancer's binary files name
// (internal/grassfield): a field (.fgrass) its kit, by resources-relative
// path; a kit (.fgk) its clumps' albedo textures, by engine name. The level
// names each field in its grass volume's YAML, which the token scan finds;
// these two hops are inside binary files, so they are read out as a cooked
// Nanite mesh's slots are.

import (
	"os"
	"path/filepath"
	"strings"

	"foundry/internal/grassfield"
)

// grassRefs is the names the grass file at path refers to, or nil when it is
// not one (or cannot be read: a broken file reaches nothing, as an unreadable
// level does).
func grassRefs(path string) []string {
	switch strings.ToLower(filepath.Ext(path)) {
	case grassfield.FieldExt:
		data, err := os.ReadFile(path)
		if err != nil {
			return []string{}
		}
		f, err := grassfield.DecodeField(data)
		if err != nil {
			return []string{}
		}
		return []string{f.Kit}
	case grassfield.KitExt:
		data, err := os.ReadFile(path)
		if err != nil {
			return []string{}
		}
		k, err := grassfield.DecodeKit(data)
		if err != nil {
			return []string{}
		}
		var out []string
		for _, kd := range k.Kinds {
			out = append(out, kd.Texture)
		}
		return out
	}
	return nil
}
