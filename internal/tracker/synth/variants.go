package synth

// variants.go — recipes re-rendered with their knobs turned.
//
// The orchestral instruments want what one recipe at its defaults cannot
// give them: the same section sampled at two pitches so a violin's top
// string is not a chipmunk C-4, a soft and a hard-blown take for velocity
// layers, a short-bowed take for staccato, and a second and third take of a
// snare so a roll does not machine-gun. Each is a Variant: a recipe, a base
// note and a few knob values, rendered into sounds/samples/orchestral/ by
// gen-instruments (and by gen-samples -only, like any recipe).
//
// A variant's name seeds its PRNG, so two variants of one recipe with the
// same knobs are two different takes — which is exactly what round robin
// wants — and each regenerates byte-identically.

import (
	"fmt"

	"foundry/internal/tracker"
)

// VariantCategory is the library folder variants are written to.
const VariantCategory = "orchestral"

// Variant is one derived sample.
type Variant struct {
	Name   string // "vln_sus_soft_g3"
	Recipe string // the recipe it renders, "strings_section"
	Base   string // its base note; "" keeps the recipe's
	Desc   string
	Params map[string]float32
}

// LibraryName is the name instruments use for the sample.
func (v *Variant) LibraryName() string { return VariantCategory + "/" + v.Name }

// recipe is the variant as a recipe: the original's body under the
// variant's name, base note and folder.
func (v *Variant) recipe() *Recipe {
	src := ByName(v.Recipe)
	if src == nil {
		panic(fmt.Sprintf("synth: variant %s renders unknown recipe %q", v.Name, v.Recipe))
	}
	r := *src
	r.Name, r.Category, r.Desc = v.Name, VariantCategory, v.Desc
	if v.Base != "" {
		r.base = v.Base
	}
	r.tags = append(append([]string(nil), src.tags...), "variant:"+src.Name)
	params := v.Params
	r.Generate = func(rate int, p map[string]float32) *tracker.Sample {
		if p == nil {
			p = params
		}
		return r.generate(rate, p)
	}
	return &r
}

// VariantByName finds a variant by bare or library name.
func VariantByName(name string) *Variant {
	for i := range Variants {
		if Variants[i].Name == name || Variants[i].LibraryName() == name {
			return &Variants[i]
		}
	}
	return nil
}

func vr(name, recipe, base, desc string, params map[string]float32) Variant {
	return Variant{Name: name, Recipe: recipe, Base: base, Desc: desc, Params: params}
}

type kv = map[string]float32

// Variants are every derived sample the starter instruments play.
var Variants = []Variant{
	// Upper strings (violins, violas): strings_section at two pitches, a
	// light and a heavy bow for velocity layers, and a short stroke.
	vr("vln_sus_soft_g3", "strings_section", "G-3", "Upper strings, light bow, low register", kv{"pressure": 0.22, "vibrato": 0.35}),
	vr("vln_sus_loud_g3", "strings_section", "G-3", "Upper strings, heavy bow, low register", kv{"pressure": 0.78, "vibrato": 0.45}),
	vr("vln_sus_soft_d5", "strings_section", "D-5", "Upper strings, light bow, high register", kv{"pressure": 0.22, "vibrato": 0.35}),
	vr("vln_sus_loud_d5", "strings_section", "D-5", "Upper strings, heavy bow, high register", kv{"pressure": 0.78, "vibrato": 0.45}),
	vr("vln_short_g3", "strings_section", "G-3", "Upper strings, short bow stroke, low register", kv{"pressure": 0.6, "attack": 0.05, "vibrato": 0.2}),
	vr("vln_short_d5", "strings_section", "D-5", "Upper strings, short bow stroke, high register", kv{"pressure": 0.6, "attack": 0.05, "vibrato": 0.2}),
	// Lower strings (cellos, basses): the solo cello recipe, likewise.
	vr("vc_sus_soft_c2", "cello", "C-2", "Low strings, light bow, low register", kv{"pressure": 0.28, "vibrato": 0.4}),
	vr("vc_sus_loud_c2", "cello", "C-2", "Low strings, heavy bow, low register", kv{"pressure": 0.8, "vibrato": 0.5}),
	vr("vc_sus_soft_g3", "cello", "G-3", "Low strings, light bow, high register", kv{"pressure": 0.28, "vibrato": 0.4}),
	vr("vc_sus_loud_g3", "cello", "G-3", "Low strings, heavy bow, high register", kv{"pressure": 0.8, "vibrato": 0.5}),
	vr("vc_short_c2", "cello", "C-2", "Low strings, short bow stroke, low register", kv{"pressure": 0.6, "attack": 0.05, "vibrato": 0.2}),
	vr("vc_short_g3", "cello", "G-3", "Low strings, short bow stroke, high register", kv{"pressure": 0.6, "attack": 0.05, "vibrato": 0.2}),
	vr("pizz_c2", "strings_pizz", "C-2", "Low pizzicato: a longer, rounder pluck", kv{"decay": 1.2, "brightness": 0.35}),
	// Brass: softly blown takes under the recipes' hard-blown defaults, and
	// a tuba from the trombone an octave down.
	vr("trumpet_soft", "trumpet", "", "Trumpet, softly blown", kv{"level": 0.42}),
	vr("horn_soft", "french_horn", "", "French horn, softly blown", kv{"level": 0.42}),
	vr("trombone_soft", "trombone", "", "Trombone, softly blown", kv{"level": 0.42, "buzz": 0.15}),
	vr("tuba_c2", "trombone", "C-2", "Tuba: the trombone an octave down, rounder", kv{"level": 0.7, "buzz": 0.2, "attack": 0.08}),
	// Woodwinds: a bassoon from the oboe's reed, wider and lower.
	vr("bassoon_c2", "oboe", "C-2", "Bassoon: a wide double reed in the bass", kv{"width": 0.2, "nasal": 0.45}),
	// Keys: the piano in three registers, each soft and hard.
	vr("piano_soft_c2", "piano", "C-2", "Piano, soft hammer, bass register", kv{"hardness": 0.15, "hammer": 0.15}),
	vr("piano_loud_c2", "piano", "C-2", "Piano, hard hammer, bass register", kv{"hardness": 0.6}),
	vr("piano_soft_c4", "piano", "C-4", "Piano, soft hammer, middle register", kv{"hardness": 0.15, "hammer": 0.15}),
	vr("piano_soft_c6", "piano", "C-6", "Piano, soft hammer, treble register", kv{"hardness": 0.15, "hammer": 0.15, "decay": 3}),
	vr("piano_loud_c6", "piano", "C-6", "Piano, hard hammer, treble register", kv{"hardness": 0.6, "decay": 3}),
	vr("celesta_c5", "glockenspiel", "C-5", "Celesta: soft hammers on steel plates", kv{"decay": 2, "hardness": 0.2}),
	vr("harp_c2", "harp", "C-2", "Harp, bass strings", kv{"decay": 5, "brightness": 0.4}),
	// Percussion.
	vr("timpani_soft", "timpani", "", "Timpani, soft mallets", kv{"mallet": 0.1}),
	vr("xylophone_c5", "marimba", "C-5", "Xylophone: hard mallets on a short, high bar", kv{"decay": 0.5, "hardness": 0.95}),
	vr("snare_acoustic_rr2", "snare_acoustic", "", "Snare, second take (round robin)", nil),
	vr("snare_acoustic_rr3", "snare_acoustic", "", "Snare, third take (round robin)", nil),
	vr("hat_closed_rr2", "hat_closed_808", "", "Closed hat, second take (round robin)", nil),
}
