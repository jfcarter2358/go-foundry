// exposure_comp.glsl — the automatic exposure, metered and adapted on the GPU
// (render/exposure.go; docs/lighting/PLAN.md, phase 4). One workgroup: a
// 64 x 64 grid of the frame's resolved colour (scene units, before the curve),
// each sample's log luminance weighted toward the middle of the screen, the
// sky left out where the occlusion pass's distances say a pixel is sky. Their
// weighted mean is the log of the scene's geometric mean, and the first
// invocation eases the exposure the buffer holds toward the key over it —
// where the frame's output reads it. Nothing is read back to the CPU: a
// synchronous readback a frame is what Mesa's D3D12 driver deadlocks on
// (glext/readback.go).
//
// #version is spliced ahead of it.

layout(local_size_x = 16, local_size_y = 16) in;

layout(std430, binding = 0) buffer Exposure {
    float exposureValue; // the adapted exposure, what the output multiplies by
    float exposureSeeded; // 0 until the first frame metered
};

uniform float expDt;       // seconds since the last frame
uniform float expKey;      // the scene luminance the mean is brought to
uniform float expAdapt;    // how much of the way: (key / mean) to this power
uniform float expMin, expMax;
uniform float expSpeedUp, expSpeedDown;
uniform int   expReset;    // 1: start from expStart, taking the first reading outright
uniform float expStart;

uniform sampler2D meterSrc;
uniform sampler2D meterDist; // the occlusion texture: b the distance, 0 the sky
uniform int meterUseDist;

shared float sLog[256];
shared float sW[256];

void main() {
    uint tid = gl_LocalInvocationIndex;
    float sumLog = 0.0, sumW = 0.0;
    for (uint k = 0u; k < 16u; k++) {
        uint idx = tid + k * 256u;
        vec2 uv = (vec2(float(idx % 64u), float(idx / 64u)) + 0.5) / 64.0;
        if (meterUseDist != 0 && textureLod(meterDist, uv, 0.0).b <= 0.0) {
            continue;
        }
        vec2 d = uv - 0.5;
        float w = exp(-dot(d, d) * 5.0);
        vec3 c = textureLod(meterSrc, uv, 0.0).rgb;
        float l = dot(c, vec3(0.2126, 0.7152, 0.0722));
        sumLog += w * clamp(log2(max(l, 1e-5)), -14.0, 6.0);
        sumW += w;
    }
    sLog[tid] = sumLog;
    sW[tid] = sumW;
    barrier();
    for (uint s = 128u; s > 0u; s >>= 1u) {
        if (tid < s) {
            sLog[tid] += sLog[tid + s];
            sW[tid] += sW[tid + s];
        }
        barrier();
    }
    if (tid == 0u) {
        float cur = expReset != 0 ? expStart : exposureValue;
        float seeded = expReset != 0 ? 0.0 : exposureSeeded;
        if (sW[0] > 1e-3) {
            float mean = exp2(sLog[0] / sW[0]);
            float target = clamp(pow(expKey / max(mean, 1e-4), expAdapt), expMin, expMax);
            if (seeded == 0.0 || !(cur > 0.0)) {
                cur = target;
            } else {
                // Eased in log space, faster toward darker than toward lighter.
                float speed = target < cur ? expSpeedDown : expSpeedUp;
                float k = 1.0 - exp(-expDt * speed);
                cur = exp(mix(log(cur), log(target), k));
            }
            seeded = 1.0;
        }
        exposureValue = cur;
        exposureSeeded = seeded;
    }
}
