#version 410

// detail_shadow.tese — directional-shadow-pass evaluation stage. Same
// displacement as detail.tese (shared noise injected above main) but outputs
// only clip position in the light's space, so the cast shadow matches the bumps.
// Paired with shadow_frag.glsl.

layout(triangles, equal_spacing, ccw) in;

uniform mat4 lightSpaceMatrix;
uniform mat4 model;

in vec3 teWorld[];
in vec2 teUV[];

void main() {
    vec3 w = gl_TessCoord.x * teWorld[0] + gl_TessCoord.y * teWorld[1] + gl_TessCoord.z * teWorld[2];
    vec3 dp = detailDisplace(w);
    gl_Position = lightSpaceMatrix * model * vec4(dp, 1.0);
}
