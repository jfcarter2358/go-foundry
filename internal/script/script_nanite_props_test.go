package script

import (
	"testing"

	"foundry/internal/gfx"
)

// The Nanite settings a settings screen offers: the LOD threshold and the
// page pool's budget (Phase 3), clamped and marked for saving.
func TestNaniteSettingProps(t *testing.T) {
	lod, pool := gfx.NaniteLODError, gfx.NaniteStreamPoolMB
	t.Cleanup(func() {
		gfx.NaniteLODError, gfx.NaniteStreamPoolMB, gfx.GraphicsSettingsChanged = lod, pool, false
	})
	root := withTempScriptDir(t)
	withUIStack(t)
	path := writeScript(t, root, "l.flang", `def on_spawn(:spawn:) -> null
    @level.prop.nanite_stream_pool_mb = 128
    @self.attr.pool = @level.prop.nanite_stream_pool_mb
    @level.prop.nanite_lod_error = 2
end`)
	lvl := tickTestLevel(t)
	lvl.Chunks[0].Brushes[0].Script = path
	runTicks(t, lvl, 2)
	if gfx.NaniteStreamPoolMB != 128 || gfx.NaniteLODError != 2 || !gfx.GraphicsSettingsChanged {
		t.Errorf("pool %d MB, lod %g, changed %v", gfx.NaniteStreamPoolMB, gfx.NaniteLODError, gfx.GraphicsSettingsChanged)
	}
	gfx.SetNaniteStreamPoolMB(1)
	if gfx.NaniteStreamPoolMB != gfx.NaniteStreamPoolMBMin {
		t.Errorf("a 1 MB pool came in as %d", gfx.NaniteStreamPoolMB)
	}
}
