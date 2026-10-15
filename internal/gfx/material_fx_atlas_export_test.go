package gfx

// material_fx_atlas_export_test.go — the splat atlas internals the external
// GL tests (material_fx_atlas_gl_test.go) need: the VRAM record's key, and a
// way to drop a cached atlas so the next ask rebuilds it under new settings.

import (
	"fmt"

	"foundry/internal/material"
)

// SplatAtlasVRAMKey is the F3 record prefix of the atlases for names
// (fx:splat:<hash>; the two atlases add :albedo and :normal).
func SplatAtlasVRAMKey(names [4]string) string {
	return fmt.Sprintf("fx:splat:%x", hashNames(names))
}

// ForgetSplatArrays drops names' cached atlases (their GL objects leak,
// which a test binary does not mind), so SplatArraysFor builds afresh.
func ForgetSplatArrays(names [4]string) { delete(splatArrays, atlasNames32(splatNames8(names))) }

// atlasNames32 is eight names as the 32 the cache is keyed by.
func atlasNames32(names [8]string) [material.SplatAtlasMax]string {
	var n [material.SplatAtlasMax]string
	copy(n[:], names[:])
	return n
}

// SplatAtlasVRAMKey8 is SplatAtlasVRAMKey for an eight-layer splat's names.
func SplatAtlasVRAMKey8(names [8]string) string {
	return fmt.Sprintf("fx:splat:%x", splatKeyHash(atlasNames32(names)))
}

// ForgetSplatArrays8 is ForgetSplatArrays for eight names.
func ForgetSplatArrays8(names [8]string) { delete(splatArrays, atlasNames32(names)) }

// SplatAtlasVRAMKeyPalette is SplatAtlasVRAMKey for a palette's names.
func SplatAtlasVRAMKeyPalette(names [material.SplatAtlasMax]string) string {
	return fmt.Sprintf("fx:splat:%x", splatKeyHash(names))
}

// ForgetSplatArraysPalette is ForgetSplatArrays for a palette's names.
func ForgetSplatArraysPalette(names [material.SplatAtlasMax]string) { delete(splatArrays, names) }
