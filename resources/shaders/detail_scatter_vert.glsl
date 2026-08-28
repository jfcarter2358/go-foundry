#version 410

// detail_scatter_vert.glsl — color-pass vertex stage for scattered rock
// instances. Emits the varyings fragment.glsl consumes so scattered rocks get
// full lighting + shadow receiving. Placement comes from the injected
// detail_scatter_common.glsl.

layout(location = 0) in vec3 aLocal;
layout(location = 1) in vec3 aNormal;
layout(location = 2) in float aParam;

uniform mat4 projection;
uniform mat4 camera;
uniform mat4 model;

out vec2 fragTexCoord;
out vec3 fragWorldPos;
out vec3 fragNormal;
// View-axis distance; the fragment stage uses it to select a shadow cascade.
out float fragViewDepth;

void main() {
    vec3 w, n;
    if (!scatterPlaceN(gl_InstanceID, aLocal, aNormal, w, n)) {
        gl_Position = vec4(2.0, 2.0, 2.0, 1.0); // collapse off-screen; clipped away
        fragTexCoord = vec2(0.0);
        fragWorldPos = vec3(0.0);
        fragNormal = vec3(0.0);
        fragViewDepth = 0.0;
        return;
    }
    fragTexCoord = aLocal.xz * 0.5 + 0.5;
    // w and n are chunk-local (see detail_scatter_common.glsl); model carries
    // the placed instance out to world space in one step, same as vertex.glsl.
    vec4 worldPos = model * vec4(w, 1.0);
    fragWorldPos = worldPos.xyz;
    // The rock template carries one normal per facet, so this matches the flat
    // normal the fragment stage would derive — but without the derivative, and
    // it follows the mesh if it ever gains smoothed normals. Inverse-transpose
    // for the same non-uniform-scale-safety reason vertex.glsl uses it.
    fragNormal = transpose(inverse(mat3(model))) * n;

    vec4 viewPos = camera * worldPos;
    fragViewDepth = -viewPos.z;
    gl_Position = projection * viewPos;
}
