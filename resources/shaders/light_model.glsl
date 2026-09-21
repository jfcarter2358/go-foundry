// light_model.glsl — how light falls across a surface, for every pass that
// lights one.
//
// Spliced after the #version line of the world fragment stage (fragment.glsl,
// via gfx.InjectLightModel) and ahead of biome_light.glsl (render's
// biomeLightSnippet), so the walls, the ground patches, the scatter and lit
// sprites are one lighting model rather than one per pass. The level compiler
// bakes with the same one (world.GlobalLight.LightWrap/AmbientAt) — change a
// term here and change it there, or a baked surface stops matching a live one.

// The level's lighting-model knobs, world.GlobalLight's Wrap and Hemisphere,
// uploaded with the rest of the lights (world.UploadLightsTo).
uniform float lightWrap;
uniform float ambientHemisphere;

// Wrapped Lambert: how much of a light a surface at cosine ndl to it takes. At
// lightWrap 0 it is max(N.L, 0), the hard terminator; above that the light
// carries on round the curve and eases out instead of stopping at side-on.
float lightWrapTerm(float ndl) {
    return max((ndl + lightWrap) / (1.0 + lightWrap), 0.0);
}

// Hemispheric ambient: the ambient colour brighter facing the sky and darker
// facing the ground, averaging to the colour itself.
vec3 ambientAt(vec3 ambient, vec3 n) {
    return ambient * (1.0 + 0.5 * clamp(ambientHemisphere, 0.0, 1.0) * n.y);
}
