// foliage_common.glsl — placing one foliage instance's vertex
// (render/foliage.go). Spliced into both foliage vertex stages, the colour
// pass and the cascade depth pass, so a tree casts the shadow of the tree on
// screen — sway included.
//
// A template vertex is in the plant's own frame: y up its growth axis, unit
// scale. The instance carries where it stands (chunk space), its growth axis,
// its turn about that axis, its size, its colours and a random rank. The
// vertex is carried into the chunk, then out to the world by `model`, and the
// wind bends it there, in world space, so every plant in a gust bends the same
// way whatever its chunk's rotation.

layout(location = 0) in vec3 aPos;
layout(location = 1) in vec3 aNormal;
layout(location = 2) in vec2 aUV;
// sway (0 root .. 1 tip), part (render/foliage_mesh.go part*), tone.
layout(location = 3) in vec3 aParam;
// Per instance.
layout(location = 4) in vec4 iPosScale; // chunk-space position, scale
layout(location = 5) in vec4 iUpYaw;    // growth axis, turn about it
layout(location = 6) in vec4 iColorRank;
layout(location = 7) in vec4 iAccentSeed;

uniform mat4 model;
// The inverse-transpose of model's upper 3x3, uploaded with it.
uniform mat3 normalMatrix;
uniform vec3 camPos;
uniform float time;
uniform vec2 windDir;
uniform float windStrength;
// This batch's draw range: instances thin out over its outer part, each at
// its own distance by its own rank.
uniform float drawRange;
// Sink into the ground while fading, rather than only dissolving.
uniform int shrinkFade;

vec3 fWorldPos;
vec3 fNormal;
vec3 fUp;
float fFade;

bool foliagePlace() {
    vec3 up = normalize(iUpYaw.xyz);
    vec3 ref = abs(up.y) < 0.95 ? vec3(0.0, 1.0, 0.0) : vec3(1.0, 0.0, 0.0);
    vec3 t0 = normalize(cross(ref, up));
    vec3 b0 = cross(up, t0);
    float c = cos(iUpYaw.w), s = sin(iUpYaw.w);
    vec3 X = t0 * c + b0 * s;
    vec3 Z = cross(X, up);

    vec3 anchor = (model * vec4(iPosScale.xyz, 1.0)).xyz;
    float dist = length(anchor - camPos);
    // The fraction of this batch still drawn at this distance; this
    // instance stays while its rank is under it, fading over a narrow band.
    float keep = 1.0 - smoothstep(drawRange * 0.35, drawRange, dist);
    // Scaled so that at keep = 1 even the highest rank is wholly in.
    const float band = 0.06;
    float fade = clamp((keep * (1.0 + band) - iColorRank.w) / band, 0.0, 1.0);
    if (fade <= 0.0) {
        return false;
    }
    fFade = fade;

    float scale = iPosScale.w;
    if (shrinkFade != 0) {
        scale *= mix(0.25, 1.0, fade);
    }
    vec3 lp = aPos * scale;
    vec3 local = iPosScale.xyz + X * lp.x + up * lp.y + Z * lp.z;
    vec3 n = X * aNormal.x + up * aNormal.y + Z * aNormal.z;
    vec4 w = model * vec4(local, 1.0);
    fNormal = normalize(normalMatrix * n);
    fUp = normalize(mat3(model) * up);

    // Wind: a slow travelling gust — waves you can watch cross a meadow —
    // and a quick flutter, both growing toward the tip.
    float sway = aParam.x;
    if (sway > 0.0 && windStrength > 0.0) {
        vec3 wd = vec3(windDir.x, 0.0, windDir.y);
        float phase = dot(anchor.xz, windDir) * 0.09 - time * 1.3;
        float gust = 0.5 + 0.5 * sin(phase + 1.7 * sin(phase * 0.37 + anchor.x * 0.05));
        float flutter = sin(time * 6.5 + anchor.x * 1.7 + anchor.z * 1.3 + iAccentSeed.w * 40.0 + aPos.y * 3.0);
        float bend = windStrength * sway * sway * (0.2 + 0.8 * gust);
        vec3 side = normalize(mat3(model) * X);
        vec3 disp = wd * bend * scale * 0.35 + side * flutter * sway * 0.03 * scale * windStrength;
        w.xyz += disp;
        // Keep the blade's length: what bends over comes down.
        w.xyz -= fUp * dot(disp, disp) * 0.5 / max(scale * max(aPos.y, 0.05), 0.05);
    }
    fWorldPos = w.xyz;
    return true;
}
