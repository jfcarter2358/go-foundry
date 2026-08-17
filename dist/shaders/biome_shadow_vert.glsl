#version 410

// biome_shadow_vert.glsl — directional-cascade depth stage for the biome
// scatter. Paired with shadow_frag.glsl.
//
// Placement comes from biome_scatter_common.glsl, byte-for-byte the same code
// the color pass runs, so the silhouette written into the depth map is the
// silhouette of the blade or pebble actually on screen — including its wind
// sway, which is why `time` has to be uploaded to this program too.
//
// castRange (uploaded shorter here than in the color pass) is what keeps this
// affordable: contact shadows near the camera are the ones that read, so the
// far half of the scatter ring is dropped rather than re-instanced into every
// cascade. See BiomeShadows.CastRange in biome_light.go.

layout(location = 0) in vec3  aLocal;
layout(location = 1) in vec3  aNormal;
layout(location = 2) in float aParam;

uniform mat4 lightSpaceMatrix;

void main() {
    if (!biomePlace(gl_InstanceID, aLocal, aNormal, aParam)) {
        gl_Position = vec4(2.0, 2.0, 2.0, 1.0);
        return;
    }
    gl_Position = lightSpaceMatrix * vec4(bWorldPos, 1.0);
}
