package stages

// grass_field_cover.go — the grass_field rule's GROUND COVER options
// (docs/terrain-paint/SPEC.md "Ground-cover drawing", PHASE_P3.md): what lets
// the grass instancer draw leaf litter, twigs, pine needles and pebbles as
// well as grass.
//
// Dense flat clutter cannot be scenery (about a millisecond of CPU frame per
// thousand placements in view), and the instancer already draws hundreds of
// thousands of clumps cheaply — so ground cover is grass_field rules with a
// few more fields, every one Added and defaulting to exactly what a grass
// rule did before (a document that does not set them hashes, places and
// draws byte for byte as it did):
//
//   - `orient: ground` lays each clump along the terrain's normal under it,
//     at its random yaw (a twig lies down a bank, a pebble sits on it);
//     `tilt_jitter` (degrees) leans each one at random as well. Either makes
//     the kind ORIENTED (grassfield.KindOriented) and the stage writes
//     oriented fields (16 bytes a clump, the fourth word the axis);
//   - `wind: false` keeps the kind still (bend and flutter 0 in its
//     vertices, grassfield.KindNoWind);
//   - `sink` pushes each clump into the ground by that fraction of its
//     height (straight down, so it never leaves its tile) — a pebble half
//     buried, a branch bedded in the litter rather than resting on its
//     highest twig;
//   - `card` makes the rule's kinds CARDS (grassfield.KindCard): flat
//     alpha-cut squares lying on the ground, cut out of a Poly Haven ground
//     texture (a polyhaven_textures stage's layer, `card_textures` + `card`)
//     by its own height map — the leaves are the high parts of a leaf-litter
//     scan, so the cut-out is the scan's leaves with the soil between them
//     gone (grass_field_card.go). `card_tiles` cuts the image into an n × n
//     atlas, one kind a tile, all sharing one texture (one draw group). A
//     card is always oriented, never sways and never casts.
//
// A rule's kinds are keyed by piece AND these options, so the same piece
// grown upright by one rule and laid down by another is two kinds.
//
// SOLID PIECES. The kit's distant meshes thin a grass clump by dropping
// whole blades (grassfield.Thin); a pebble or a branch is one piece, which
// thinning cannot shorten. A piece of fewer than solidPieceLimit connected
// components is SOLID: its meshes are the import's own quadric decimation
// (phimport.Decimate) to max_triangles and the LOD ratios instead.

import (
	"fmt"
	"math"
	"strings"

	"foundry/internal/grassfield"
	"foundry/internal/model"
	"foundry/internal/polyhaven/phimport"
	"foundry/internal/procgen"
	"foundry/internal/procgen/place"
)

// solidPieceLimit is the component count under which a piece is solid.
const solidPieceLimit = 6

// groundCoverRuleFields are a grass rule's ground cover params.
func groundCoverRuleFields() []procgen.ParamDef {
	f := procgen.Float
	return []procgen.ParamDef{
		{Name: "orient", Added: true, Type: procgen.ParamEnum, Default: procgen.Enum("upright"), Choices: []string{"upright", "ground"},
			Doc: "upright: the clumps stand up the world's Y (grass). ground: each lies along the terrain's normal under it, at a random " +
				"turn about it (twigs, needles, pebbles, leaves)."},
		{Name: "wind", Added: true, Type: procgen.ParamBool, Default: procgen.Bool(true),
			Doc: "Whether the clumps sway. Off for branches, stones and litter."},
		{Name: "tilt_jitter", Added: true, Type: procgen.ParamFloat, Label: "Tilt jitter (°)", Default: f(0), Min: 0, Max: 90, Step: 0.5,
			Doc: "Lean each clump at random by up to this many degrees off its axis (a jumble of twigs, stones not all sitting square)."},
		{Name: "sink", Added: true, Type: procgen.ParamFloat, Default: f(0), Min: 0, Max: 1, Step: 0.01,
			Doc: "Push each clump into the ground by this fraction of its height: pebbles half buried, branches bedded in."},
		{Name: "card_textures", Added: true, Type: procgen.ParamStage, StageKinds: []string{PolyHavenTexturesKind}, Default: procgen.StageRef(""),
			Label: "Card textures", Doc: "The polyhaven_textures stage the card's ground texture comes from."},
		{Name: "card", Added: true, Type: procgen.ParamString, Default: procgen.String(""),
			Doc: "Make this rule CARDS: flat alpha-cut squares lying on the ground, cut out of this layer of card_textures by its own height " +
				"map (the leaves of a leaf-litter scan, the soil between them gone). Empty: the rule grows plants."},
		{Name: "card_cut", Added: true, Type: procgen.ParamFloat, Label: "Card cut", Default: f(0.55), Min: 0.05, Max: 0.95, Step: 0.01,
			Doc: "The height (0..1 of the scan's range) above which the scan is kept: higher keeps only the top leaves."},
		{Name: "card_tiles", Added: true, Type: procgen.ParamInt, Label: "Card tiles", Default: procgen.Int(2), Min: 1, Max: 4,
			Doc: "Cut the texture into this many tiles a side, each a card kind (a card is 1 m at scale 1, showing one tile)."},
	}
}

// grassRuleHasSource reports whether a rule names what it grows: a plants
// stage, or a card texture.
func grassRuleHasSource(it procgen.Params) bool {
	return it.Str("plants") != "" || isCardRule(it)
}

// isCardRule reports whether rule item it is a card rule.
func isCardRule(it procgen.Params) bool { return it.Str("card") != "" && it.Str("card_textures") != "" }

// groundCoverRule copies the placement half of the options onto a place rule.
func groundCoverRule(it procgen.Params, r *place.GrassRule) {
	r.Ground = it.Str("orient") == "ground" || isCardRule(it)
	r.TiltJitter = it.Float("tilt_jitter") * math.Pi / 180
}

func validateCoverRule(i int, it procgen.Params) []string {
	var out []string
	if (it.Str("card") == "") != (it.Str("card_textures") == "") && it.Bool("enabled") {
		out = append(out, fmt.Sprintf("rule %d (%s): a card rule needs both card_textures and card", i+1, it.Str("name")))
	}
	return out
}

// coverFlags is the kind flags a rule's options give its kinds.
func coverFlags(it procgen.Params) uint32 {
	var fl uint32
	if it.Str("orient") == "ground" || it.Float("tilt_jitter") > 0 || isCardRule(it) {
		fl |= grassfield.KindOriented
	}
	if !it.Bool("wind") || isCardRule(it) {
		fl |= grassfield.KindNoWind
	}
	if isCardRule(it) {
		fl |= grassfield.KindCard
	}
	return fl
}

// coverKey is what tells a rule's kinds apart from another rule's of the
// same piece: empty for a plain grass rule (so a kit's kind names and order
// are what they always were).
func coverKey(it procgen.Params) string {
	fl := coverFlags(it)
	if fl == 0 {
		return ""
	}
	return fmt.Sprintf("#%x", fl)
}

// sourceOf is an import mesh as the kit's source mesh.
func sourceOf(m *model.ImportMesh) *grassfield.SourceMesh {
	src := &grassfield.SourceMesh{Indices: m.Indices}
	for i, p := range m.Positions {
		src.Positions = append(src.Positions, [3]float32{p[0], p[1], p[2]})
		if i < len(m.Normals) {
			n := m.Normals[i]
			src.Normals = append(src.Normals, [3]float32{n[0], n[1], n[2]})
		}
		if i < len(m.UVs) {
			src.UVs = append(src.UVs, [2]float32{m.UVs[i][0], m.UVs[i][1]})
		}
	}
	return src
}

// solidLODs is a solid piece's meshes: decimated to max triangles, then to
// each LOD ratio of that (a ratio that decimates no further than the mesh
// before it is left out).
func solidLODs(m *model.ImportMesh, maxTris int, ratios [2]float64) []*grassfield.SourceMesh {
	lod0 := phimport.Decimate(m, maxTris)
	out := []*grassfield.SourceMesh{sourceOf(lod0)}
	last := lod0.TriangleCount()
	for _, r := range ratios {
		if r <= 0 {
			continue
		}
		lm := phimport.Decimate(lod0, max(int(float64(lod0.TriangleCount())*r), 8))
		if lm.TriangleCount() >= last*9/10 {
			continue
		}
		out = append(out, sourceOf(lm))
		last = lm.TriangleCount()
	}
	return out
}

// cardKinds are a card rule's kinds: one per atlas tile of its cut-out,
// built on first sight (two rules with the same cut share them).
func (b *grassKitBuilder) cardKinds(rc *procgen.RunContext, it procgen.Params) ([]int, error) {
	a, err := rc.UpstreamArtifact(it.Str("card_textures"))
	if err != nil {
		return nil, err
	}
	set, ok := a.(*TextureSetManifest)
	if !ok {
		return nil, fmt.Errorf("stage %s is not a textures stage", it.Str("card_textures"))
	}
	l := set.Layer(it.Str("card"))
	if l == nil {
		return nil, fmt.Errorf("textures stage %s has no layer %q", it.Str("card_textures"), it.Str("card"))
	}
	n := min(max(it.Int("card_tiles"), 1), 4)
	tex, err := buildCardTexture(b.resources, l, it.Float("card_cut"), n)
	if err != nil {
		return nil, err
	}
	var out []int
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			key := fmt.Sprintf("%s#card%d_%d", tex, i, j)
			if k, ok := b.index[key]; ok {
				out = append(out, k)
				continue
			}
			kd := grassfield.Kind{Name: fmt.Sprintf("%s_card_%d_%d", l.ID, i, j), Texture: tex, Height: 0.05, Radius: 0.71,
				Range: float32(it.Float("range")), Flags: coverFlags(it)}
			kd.LODs = []grassfield.Mesh{grassfield.CardMesh(i, j, n)}
			b.index[key] = len(b.kit.Kinds)
			b.sink = append(b.sink, 0)
			b.kit.Kinds = append(b.kit.Kinds, kd)
			out = append(out, len(b.kit.Kinds)-1)
		}
	}
	return out, nil
}

// sinkInstance pushes a clump of a kind of height h into the ground by
// sink × its height. Straight down, not along its axis: a clump never
// leaves the tile (and the field's box) it was placed in.
func sinkInstance(in *place.GrassInstance, sink float64, h float32) {
	if sink > 0 {
		in.Pos[1] -= sink * float64(h) * in.Scale
	}
}

// coverLogName is how the log names a kind's options.
func coverLogName(fl uint32) string {
	var parts []string
	if fl&grassfield.KindCard != 0 {
		parts = append(parts, "card")
	} else if fl&grassfield.KindOriented != 0 {
		parts = append(parts, "oriented")
	}
	if fl&grassfield.KindNoWind != 0 && fl&grassfield.KindCard == 0 {
		parts = append(parts, "still")
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}
