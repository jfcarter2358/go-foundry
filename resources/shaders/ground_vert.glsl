#version 430

// ground_vert.glsl — conforming ground-patch mesh underneath a painted biome
// sector (see biome_ground.go). Geometry is fully baked on the CPU, chunk-local
// (already height-conformed and displaced), so this stage's own job is just
// carrying it out to world space via `model` — all the per-pixel detail (bump,
// AO) and all the lighting happen in ground_frag.glsl.

in vec3 aPos;
in vec3 aNormal;
in vec2 aUV;
// Slope-gating coverage baked per vertex (biome_displace.go's slopeCoverage):
// 1 = this spot is inside the brush's slope range, 0 = outside. Interpolated
// across the triangle and thresholded per-pixel in ground_frag.glsl, so the
// patch's cutoff follows the real slope contour instead of the patch mesh's
// own coarse (~2 world unit) triangulation.
in float aCoverage;

uniform mat4 projView;
uniform mat4 model;

out vec3 vWorldPos;
out vec3 vNormal;
out vec2 vUV;
out float vCoverage;
// Distance along the view axis, for cascade selection. Computed exactly the way
// vertex.glsl computes fragViewDepth, so a patch picks the same cascade as the
// structural floor it drapes over and their shadow edges line up instead of
// stepping apart at the patch boundary.
out float vViewDepth;

void main() {
    vec4 worldPos = model * vec4(aPos, 1.0);
    vWorldPos = worldPos.xyz;
    // Inverse-transpose rather than mat3(model), same reason vertex.glsl uses
    // it — safe even though Chunk.Scale is uniform today.
    vNormal = transpose(inverse(mat3(model))) * aNormal;
    vUV = aUV;
    vCoverage = aCoverage;
    vViewDepth = biomeViewDepth(vWorldPos);
    gl_Position = projView * worldPos;
}
