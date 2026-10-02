#version 430

// nanite_debug_frag.glsl — the Nanite debug views' colours
// (render/nanite_debug.go): 1 clusters, 2 LOD level, 3 overdraw (added up by
// the blend, no depth test).

flat in uint vInstance;
flat in uint vCluster;
flat in uint vLevel;
in vec3 vWorld;

uniform int debugMode;

out vec4 outputColor;

vec3 hashColour(uint x) {
    x ^= x >> 16;
    x *= 0x7feb352du;
    x ^= x >> 15;
    x *= 0x846ca68bu;
    x ^= x >> 16;
    return vec3(float(x & 255u), float((x >> 8) & 255u), float((x >> 16) & 255u)) / 255.0 * 0.75 + 0.25;
}

// render.naniteDebugLevelColour.
vec3 levelColour(uint l) {
    const vec3 palette[8] = vec3[8](
        vec3(1.0, 1.0, 1.0), vec3(0.2, 0.4, 1.0), vec3(0.0, 0.9, 0.9), vec3(0.2, 0.9, 0.2),
        vec3(1.0, 0.95, 0.1), vec3(1.0, 0.55, 0.0), vec3(1.0, 0.1, 0.1), vec3(0.9, 0.1, 0.9));
    return palette[min(l, 7u)];
}

void main() {
    if (debugMode == 3) {
        outputColor = vec4(0.08, 0.035, 0.01, 1.0);
        return;
    }
    // The face's own normal, from the derivatives, so the shape reads under
    // the flat colours.
    vec3 n = normalize(cross(dFdx(vWorld), dFdy(vWorld)));
    float shade = 0.55 + 0.45 * abs(dot(n, normalize(vec3(0.4, 0.8, 0.45))));
    vec3 c = debugMode == 2 ? levelColour(vLevel) : hashColour(vCluster * 2654435761u ^ vInstance * 40503u);
    outputColor = vec4(c * shade, 1.0);
}
