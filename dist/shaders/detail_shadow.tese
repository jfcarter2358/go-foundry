#version 410

// detail_shadow.tese — directional-shadow-pass evaluation stage. Same
// displacement as detail.tese (shared noise injected above main) but outputs
// only clip position in the light's space, so the cast shadow matches the bumps.
// Paired with shadow_frag.glsl.

layout(triangles, equal_spacing, ccw) in;

uniform mat4 lightSpaceMatrix;

in vec3 teWorld[];
in vec2 teUV[];

void main() {
    vec3 w = gl_TessCoord.x * teWorld[0] + gl_TessCoord.y * teWorld[1] + gl_TessCoord.z * teWorld[2];
    vec3 nrm = cross(teWorld[1] - teWorld[0], teWorld[2] - teWorld[0]);
    float nl = length(nrm);
    nrm = (nl > 1e-8) ? nrm / nl : vec3(0.0, 1.0, 0.0);
    vec3 dp = detailDisplace(w, nrm);
    gl_Position = lightSpaceMatrix * vec4(dp, 1.0);
}
