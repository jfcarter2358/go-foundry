// env_capture_comp.glsl — a reflection volume's environment, captured by
// tracing the GI distance field from its centre (render/envmap.go;
// world/volume_reflection.go). One invocation a texel of one face: a ray
// out along the texel's direction; where it hits, what the hit sends back as
// the GI probes see it (gi_hit_light.glsl — the lights, one shadowed by a
// ray of its own, and last frame's probes for the bounce); where it leaves
// the field, the sky's own cube.
//
// #version, gi_sdf_common.glsl and light_model.glsl are spliced ahead of it
// (giShaderSource), as for the probe trace.

#include "env_common.glsl"

layout(local_size_x = 8, local_size_y = 8) in;

layout(rgba16f, binding = 0) uniform writeonly imageCube outCube;

uniform vec3  captureCenter;
uniform int   size;
uniform int   frameIndex;
uniform float maxDistance; // how far a ray looks before it calls it sky (gi_hit_light.glsl reads it too)
uniform samplerCubeArray skyEnv; // the environment array; cube 0 is the sky

#include "gi_hit_light.glsl"

void main() {
    ivec3 id = ivec3(gl_GlobalInvocationID);
    if (id.x >= size || id.y >= size) {
        return;
    }
    int face = id.z;
    vec3 dir = envFaceDir(face, (vec2(id.xy) + 0.5) / float(size));
    float t;
    int level;
    float voxel;
    vec3 radiance;
    if (giSdfTraceAt(captureCenter, dir, maxDistance, 64, t, level, voxel)) {
        vec3 hp = captureCenter + dir * t;
        vec3 n = giSdfNormalFrom(hp, voxel, level);
        vec4 surf = giSdfSurface(hp);
        vec3 lp = hp + n * voxel;
        uint seed = giHash(uint(id.x + id.y * size + face * size * size) ^ giHash(uint(frameIndex)));
        float u = float(seed >> 8) * (1.0 / 16777216.0);
        radiance = surf.rgb * (directAt(lp, n, u) + bounceAt(lp, n)) + surf.rgb * surf.a;
    } else {
        radiance = textureLod(skyEnv, vec4(dir, 0.0), 0.0).rgb;
    }
    imageStore(outCube, ivec3(id.xy, face), vec4(radiance, 1.0));
}
