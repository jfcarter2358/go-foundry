package render

import (
	"testing"

	"foundry/internal/gfx"
)

// TestFxUnitsAreFree checks the material effects' texture units
// (gfx.FxUnit*) are none of the world program's other samplers' units: the
// effects' samplers point at them while an effect draws.
func TestFxUnitsAreFree(t *testing.T) {
	for _, u := range []int{gfx.FxUnitSplatWeights, gfx.FxUnitSplatAlbedo, gfx.FxUnitSplatNormal, gfx.FxUnitStrata,
		gfx.FxUnitGroundColor, gfx.FxUnitGroundHeight, gfx.FxUnitDetail, gfx.FxUnitSplatWeights2} {
		for _, taken := range []int{0, int(CascadeTexUnit), gfx.BlendTexUnit, gfx.BlendMaskUnit, gfx.NormalMapUnit, CloudShadowTexUnit,
			naniteVisIDUnit, naniteVisBinUnit, GIIrradianceUnit, GIVisibilityUnit, GIProbeDataUnit, GIProbeSurfaceUnit,
			GIProbePackedUnit, giHalfResIrrUnit, giHalfResGuideUnit, VSMAtlasUnit, VSMTableUnit} {
			if u == taken {
				t.Errorf("effect unit %d is taken by another sampler of the world program", u)
			}
		}
		if u >= 2 && u < 2+8 {
			t.Errorf("effect unit %d is a point-shadow cubemap's", u)
		}
	}
}
