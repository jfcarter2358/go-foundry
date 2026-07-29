#version 410

// detail.tesc — tessellation control stage shared by every detail displacement
// program (color + directional shadow + point-cube shadow). Sets a uniform
// subdivision level per triangular patch from `tessLevel`.

layout(vertices = 3) out;

uniform float tessLevel;

in vec3 tcWorld[];
in vec2 tcUV[];

out vec3 teWorld[];
out vec2 teUV[];

void main() {
    teWorld[gl_InvocationID] = tcWorld[gl_InvocationID];
    teUV[gl_InvocationID] = tcUV[gl_InvocationID];

    if (gl_InvocationID == 0) {
        gl_TessLevelOuter[0] = tessLevel;
        gl_TessLevelOuter[1] = tessLevel;
        gl_TessLevelOuter[2] = tessLevel;
        gl_TessLevelInner[0] = tessLevel;
    }
}
