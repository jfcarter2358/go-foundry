package gfx

// material_fx_atlas_export_test.go — the splat atlas internals the external
// GL tests (material_fx_atlas_gl_test.go) need: the VRAM record's key, and a
// way to drop a cached atlas so the next ask rebuilds it under new settings.

import "fmt"

// SplatAtlasVRAMKey is the F3 record prefix of the atlases for names
// (fx:splat:<hash>; the two atlases add :albedo and :normal).
func SplatAtlasVRAMKey(names [4]string) string {
	return fmt.Sprintf("fx:splat:%x", hashNames(names))
}

// ForgetSplatArrays drops names' cached atlases (their GL objects leak,
// which a test binary does not mind), so SplatArraysFor builds afresh.
func ForgetSplatArrays(names [4]string) { delete(splatArrays, names) }
