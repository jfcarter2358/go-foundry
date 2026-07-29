#version 410

// detail.tese — color-pass tessellation evaluation stage. Interpolates the
// triangular patch, displaces the generated vertex by the shared noise (see
// detail_common.glsl, injected above main by the compiler), and emits exactly
// the varyings fragment.glsl consumes — so lighting and shadow *receiving* are
// identical to ordinary world geometry.

layout(triangles, equal_spacing, ccw) in;

uniform mat4 projection;
uniform mat4 camera;

#define MAX_DIR_LIGHTS 4
uniform int  numDirLights;
uniform mat4 lightSpaceMatrix[MAX_DIR_LIGHTS];

in vec3 teWorld[];
in vec2 teUV[];

out vec2 fragTexCoord;
out vec3 fragWorldPos;
out vec4 fragPosLightSpace[MAX_DIR_LIGHTS];

void main() {
    vec3 w = gl_TessCoord.x * teWorld[0] + gl_TessCoord.y * teWorld[1] + gl_TessCoord.z * teWorld[2];
    vec2 uv = gl_TessCoord.x * teUV[0] + gl_TessCoord.y * teUV[1] + gl_TessCoord.z * teUV[2];

    // Geometric (flat) normal of the patch — the coplanar patch verts give the
    // true face normal, consistent across the patch so displacement is C0.
    vec3 nrm = cross(teWorld[1] - teWorld[0], teWorld[2] - teWorld[0]);
    float nl = length(nrm);
    nrm = (nl > 1e-8) ? nrm / nl : vec3(0.0, 1.0, 0.0);

    vec3 dp = detailDisplace(w, nrm);

    fragTexCoord = uv;
    fragWorldPos = dp;
    for (int i = 0; i < numDirLights; i++) {
        fragPosLightSpace[i] = lightSpaceMatrix[i] * vec4(dp, 1.0);
    }
    gl_Position = projection * camera * vec4(dp, 1.0);
}
