// env_prefilter_comp.glsl — one mip of one cube of the environment array,
// GGX-prefiltered from a captured cube (render/envmap.go; docs/lighting/
// PLAN.md, phase 2). The split-sum's first half: mip m holds the radiance a
// surface of roughness m / (mips - 1) reflects about each direction, with the
// usual N = V = R assumption, so the shading reads it with one textureLod.
//
// Filtered importance sampling (Křivánek and Colbert): each GGX sample reads
// the source at the mip whose texels are the size of the solid angle the
// sample stands for, so a handful of samples give a smooth lobe rather than
// a speckled one.
//
// #version is spliced ahead of it (render.giShaderSource).

#include "env_common.glsl"

layout(local_size_x = 8, local_size_y = 8) in;

layout(rgba16f, binding = 0) uniform writeonly imageCubeArray outEnv;

uniform samplerCube srcEnv;  // the capture, with its mips
uniform int   cube;          // which cube of the array
uniform int   size;          // this mip's texels on a side
uniform float roughness;     // perceptual
uniform float srcSize;       // the capture's mip 0 texels on a side
uniform int   sampleCount;

float radicalInverse(uint b) {
    b = (b << 16u) | (b >> 16u);
    b = ((b & 0x55555555u) << 1u) | ((b & 0xAAAAAAAAu) >> 1u);
    b = ((b & 0x33333333u) << 2u) | ((b & 0xCCCCCCCCu) >> 2u);
    b = ((b & 0x0F0F0F0Fu) << 4u) | ((b & 0xF0F0F0F0u) >> 4u);
    b = ((b & 0x00FF00FFu) << 8u) | ((b & 0xFF00FF00u) >> 8u);
    return float(b) * 2.3283064365386963e-10;
}

void main() {
    ivec3 id = ivec3(gl_GlobalInvocationID);
    if (id.x >= size || id.y >= size) {
        return;
    }
    int face = id.z;
    vec3 N = envFaceDir(face, (vec2(id.xy) + 0.5) / float(size));

    vec3 result;
    if (roughness <= 0.0) {
        result = textureLod(srcEnv, N, 0.0).rgb;
    } else {
        float a = roughness * roughness;
        float a2 = a * a;
        vec3 up = abs(N.y) < 0.999 ? vec3(0.0, 1.0, 0.0) : vec3(1.0, 0.0, 0.0);
        vec3 T = normalize(cross(up, N));
        vec3 B = cross(N, T);
        float saTexel = 4.0 * 3.14159265 / (6.0 * srcSize * srcSize);
        vec3 sum = vec3(0.0);
        float wsum = 0.0;
        for (int i = 0; i < sampleCount; i++) {
            vec2 xi = vec2((float(i) + 0.5) / float(sampleCount), radicalInverse(uint(i)));
            float phi = 6.2831853 * xi.x;
            float cosT = sqrt((1.0 - xi.y) / (1.0 + (a2 - 1.0) * xi.y));
            float sinT = sqrt(max(1.0 - cosT * cosT, 0.0));
            vec3 H = T * (cos(phi) * sinT) + B * (sin(phi) * sinT) + N * cosT;
            vec3 L = 2.0 * dot(N, H) * H - N;
            float NoL = dot(N, L);
            if (NoL <= 0.0) {
                continue;
            }
            float NoH = cosT;
            float d = (NoH * a2 - NoH) * NoH + 1.0;
            float D = a2 / (3.14159265 * d * d);
            float pdf = D * 0.25; // D·NoH / (4·VoH) with N = V
            float saSample = 1.0 / (float(sampleCount) * pdf + 1e-6);
            float lod = max(0.5 * log2(saSample / saTexel) + 1.0, 0.0);
            sum += textureLod(srcEnv, L, lod).rgb * NoL;
            wsum += NoL;
        }
        result = wsum > 0.0 ? sum / wsum : textureLod(srcEnv, N, 0.0).rgb;
    }
    imageStore(outEnv, ivec3(id.xy, cube * 6 + face), vec4(result, 1.0));
}
