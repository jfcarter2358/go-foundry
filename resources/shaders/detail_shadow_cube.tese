#version 410

// detail_shadow_cube.tese — point-light (cubemap) shadow evaluation stage. Same
// displacement as detail.tese; outputs the displaced world position so
// shadow_cube_frag.glsl can store linear distance from the light.

layout(triangles, equal_spacing, ccw) in;

uniform mat4 lightSpaceMatrix;
uniform mat4 model;

in vec3 teWorld[];
in vec2 teUV[];

out vec3 fragWorldPos;

void main() {
    vec3 w = gl_TessCoord.x * teWorld[0] + gl_TessCoord.y * teWorld[1] + gl_TessCoord.z * teWorld[2];
    vec3 dp = detailDisplace(w);
    vec4 worldPos = model * vec4(dp, 1.0);
    fragWorldPos = worldPos.xyz;
    gl_Position = lightSpaceMatrix * worldPos;
}
