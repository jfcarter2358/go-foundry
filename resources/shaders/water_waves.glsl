// water_waves.glsl — the water surface's height field, shared by vertex.glsl
// (which displaces the surface with it) and fragment.glsl (which
// differentiates it for the shading normal). One file, included by both, is
// what keeps the two identical: if they drifted, lit highlights would slide
// off the actual crests (docs/terrain-paint/PHASE_W1.md).
//
// The material pass of the visibility buffer splices vertex.glsl into
// fragment.glsl (render/nanite_vis_source.go); the guard makes the second
// copy vanish there, and the vertex part's calls land on the first.
#ifndef WATER_WAVES_GLSL
#define WATER_WAVES_GLSL

// Water animation. isWater gates the whole path; time is seconds since start.
uniform int   isWater;
uniform float time;
uniform float waveAmplitude;
uniform float waveLength;
uniform float waveSpeed;
// x,z: the ripples' drift in world X/Z (m/s); z: metres per detail tile;
// w: the detail's strength (gfx/water_draw.go). The swell runs along the
// drift's heading.
uniform vec4  waterFlow;
// x: the swell's height (m), y: its wavelength (m), z: caustics strength,
// w: screen-space reflection share.
uniform vec4  waterSwell;

// waterSwellHeight is a Gerstner-PROFILE swell: four deep-water waves round
// the drift heading, each shaped like a trochoid — sharp crests, broad flat
// troughs — and travelling at deep water's phase speed for its length.
// Vertical only: a true Gerstner wave also moves the surface sideways, and
// the fragment stage needs the undisplaced X/Z to evaluate the field at
// (fragWorldPos.xz) — the sideways part would need an inversion per pixel
// for a difference the eye reads mostly as crest shape, which this has.
float waterSwellHeight(vec2 p) {
    float a = waterSwell.x;
    if (a <= 0.0) {
        return 0.0;
    }
    vec2 d0 = dot(waterFlow.xy, waterFlow.xy) > 1e-8 ? normalize(waterFlow.xy) : vec2(1.0, 0.0);
    float L = max(waterSwell.y, 1.0);
    float h = 0.0;
    // heading offset (radians), wavelength scale, share of the height
    const vec3 W[4] = vec3[4](vec3(0.0, 1.0, 0.46), vec3(0.55, 0.63, 0.25),
                              vec3(-0.42, 0.79, 0.19), vec3(1.2, 0.41, 0.10));
    for (int i = 0; i < 4; i++) {
        float c = cos(W[i].x), s = sin(W[i].x);
        vec2 d = vec2(d0.x * c - d0.y * s, d0.x * s + d0.y * c);
        float k = 6.2831853 / (L * W[i].y);
        float speed = sqrt(9.81 / k);
        float u = 0.5 + 0.5 * sin(dot(d, p) * k - speed * k * time);
        h += W[i].z * (2.0 * pow(u, 2.2) - 0.55);
    }
    return h * a;
}

// Sum of three travelling sine waves at different headings, so the surface
// never reads as a single repeating ripple, plus the swell. Driven by world
// XZ, which keeps wave size independent of how far the plane is scaled up.
// The headings are deliberately off the world axes and off each other's
// multiples: on the axes (as they once were) the crests of a level-wide
// surface lined up into a grid, which perspective drew as rays to the
// vanishing point.
float waterHeight(vec2 p) {
    float k = 6.2831853 / max(waveLength, 0.001);
    float t = time * waveSpeed * 6.2831853;

    float h = 0.0;
    h += sin(dot(p, vec2(0.83, 0.56)) * k + t) * 0.50;
    h += sin(dot(p, vec2(-0.47, 0.88)) * k * 1.31 - t * 1.1) * 0.30;
    h += sin(dot(p, vec2(0.23, -0.97)) * k * 0.77 + t * 0.8) * 0.20;
    return h * waveAmplitude + waterSwellHeight(p);
}

// Central-difference gradient of the wave field. Done per-pixel rather than
// per-vertex so the swell stays smooth regardless of mesh density, and so no
// extra varying is needed — fragment.glsl is also linked against the detail
// tessellation and scatter vertex stages, which know nothing of water.
vec3 waterNormal(vec2 p) {
    float e  = max(waveLength * 0.02, 0.01);
    float hL = waterHeight(p - vec2(e, 0.0));
    float hR = waterHeight(p + vec2(e, 0.0));
    float hD = waterHeight(p - vec2(0.0, e));
    float hU = waterHeight(p + vec2(0.0, e));
    return normalize(vec3(hL - hR, 2.0 * e, hD - hU));
}

#endif
