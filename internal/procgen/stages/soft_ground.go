package stages

// soft_ground.go — which generated ground layers are soft (docs/trails/
// SPEC.md): footprints press into them, steps sound and kick up as they do.
//
// Decided by the TEXTURE, not by the layer's role. A role is a slot in the
// ground rules — the pine forest's "sand" slot holds pine needles, the
// prairie's grass, the volcanic's cinders — so "the sand layer" says where
// the rules put it, not what it is. The Poly Haven id says what it is: a
// sand scan (red_sand, coast_sand_01, damp_beach_sand, the black coast_sand_05
// the volcanic preset lays as cinders) is sand, snow_02 is snow. Sandstone is
// rock. A triplanar layer (a cliff's rock) is never soft.

import (
	"strings"

	"foundry/internal/material"
)

// softKindFor is a ground texture's soft-ground kind by its Poly Haven id,
// "" for firm ground.
func softKindFor(id string, triplanar bool) string {
	if triplanar {
		return ""
	}
	id = strings.ToLower(id)
	switch {
	case strings.Contains(id, "snow"):
		return material.SoftSnow
	case strings.Contains(id, "sand") && !strings.Contains(id, "sandstone"):
		return material.SoftSand
	}
	return ""
}

// setSoft marks splat layer i of e soft when its texture is.
func setSoft(e *material.Effect, i int, l *TextureLayer, triplanar bool) {
	if l == nil {
		return
	}
	if k := softKindFor(l.ID, triplanar); k != "" {
		e.Set(material.SplatLayerParam(i, "soft"), material.EnumValue(k))
	}
}
