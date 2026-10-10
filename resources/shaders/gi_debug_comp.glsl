// gi_debug_comp.glsl — the GI distance field as the camera would see it
// (render/gi_debug.go, `gi_debug sdf`).
//
// Sphere-traces the field once per pixel and shades the hit with its stored
// albedo and a fixed key light, so what GI believes the level to be can be
// laid against what is drawn. Unreached pixels are the dark blue of no data.
//
// gi_sdf_common.glsl is spliced in ahead of this file.

layout(local_size_x = 8, local_size_y = 8) in;

layout(rgba8, binding = 0) uniform writeonly image2D outImage;

uniform mat4 invProjView;
uniform vec3 eye;

void main() {
    ivec2 px = ivec2(gl_GlobalInvocationID.xy);
    ivec2 size = imageSize(outImage);
    if (any(greaterThanEqual(px, size))) {
        return;
    }
    vec2 ndc = (vec2(px) + 0.5) / vec2(size) * 2.0 - 1.0;
    vec4 far = invProjView * vec4(ndc, 1.0, 1.0);
    vec3 dir = normalize(far.xyz / far.w - eye);

    vec3 color = vec3(0.05, 0.07, 0.12);
    float t;
    if (giSdfTrace(eye, dir, 400.0, 160, t)) {
        vec3 p = eye + dir * t;
        float voxel;
        giSdf(p, voxel);
        vec3 n = giSdfNormal(p, voxel);
        vec3 albedo = giSdfSurface(p).rgb;
        float key = max(dot(n, normalize(vec3(0.4, 0.8, 0.3))), 0.0);
        color = albedo * (0.35 + 0.65 * key);
    }
    imageStore(outImage, px, vec4(color, 1.0));
}
