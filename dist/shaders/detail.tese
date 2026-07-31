#version 410

// detail.tese — color-pass tessellation evaluation stage. Interpolates the
// triangular patch, displaces the generated vertex by the shared noise (see
// detail_common.glsl, injected above main by the compiler), and emits exactly
// the varyings fragment.glsl consumes — so lighting and shadow *receiving* are
// identical to ordinary world geometry.

layout(triangles, equal_spacing, ccw) in;

uniform mat4 projection;
uniform mat4 camera;

in vec3 teWorld[];
in vec2 teUV[];

out vec2 fragTexCoord;
out vec3 fragWorldPos;
out vec3 fragNormal;
// View-axis distance; the fragment stage uses it to select a shadow cascade.
out float fragViewDepth;

void main() {
    vec3 w = gl_TessCoord.x * teWorld[0] + gl_TessCoord.y * teWorld[1] + gl_TessCoord.z * teWorld[2];
    vec2 uv = gl_TessCoord.x * teUV[0] + gl_TessCoord.y * teUV[1] + gl_TessCoord.z * teUV[2];

    // Displacement direction is the face-constant dispNormal uniform, not this
    // patch's own geometric normal — see detail_common.glsl.
    vec3 dp = detailDisplace(w);

    fragTexCoord = uv;
    fragWorldPos = dp;

    // Without this the fragment stage derives a flat normal per generated
    // triangle, so a displaced surface reads as a field of facets no matter how
    // finely it is tessellated.
    fragNormal = (smoothShading != 0) ? detailNormal(w) : vec3(0.0);

    vec4 viewPos = camera * vec4(dp, 1.0);
    fragViewDepth = -viewPos.z;
    gl_Position = projection * viewPos;
}
