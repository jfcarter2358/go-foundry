#version 410

// biome_vert.glsl — GPU-scattered biome instancing (grass blades / rock debris).
//
// Every blade/rock is generated entirely on the GPU: gl_InstanceID indexes a
// world-stable lattice around the camera; a hash scatters/jitters it; a
// point-in-polygon test against the sector's own points (uploaded directly as
// a uniform array — no coverage texture) decides whether it survives; a
// per-sector height texture drops it onto the structural floor beneath it.
// No per-instance CPU data is used — only a small template mesh drawn with
// glDrawArraysInstanced.

in vec3  aLocal;   // template-local position (blade: y in [0,1] up; rock: unit blob)
in vec3  aNormal;  // template normal (rock lighting; grass ignores)
in float aParam;   // blade height fraction 0..1 (0 for rocks)

uniform mat4  projView;

// Camera-centered instancing ring.
uniform vec2  camXZ;    // camera world XZ
uniform float spacing;  // lattice spacing (world units) between scatter slots
uniform int   gridN;    // slots per side (instanceCount == gridN*gridN)
uniform float range;    // max spawn radius from camera

uniform float time;       // seconds, for wind animation
uniform bool  isRock;     // false = grass blade, true = rock
uniform float bladeHeight;// world height of a unit grass blade
uniform float bladeWidth; // world half-width of a grass blade at its base
uniform float rockScale;  // world scale of a unit rock
uniform float density;    // 0..1 survival probability, uniform across the sector

// Per-sector polygon mask (the sector's own drawn points, world XZ). Tested
// directly instead of sampling a rasterized coverage texture.
const int MAX_POLY = 64;
uniform vec2 polyPts[MAX_POLY];
uniform int  numPolyPts;
uniform vec2 polyMin, polyMax; // AABB, for a cheap reject before the ray-cast loop

// Per-sector height texture: covers [heightOrigin, heightOrigin+heightSize].
uniform sampler2D heightTex;
uniform vec2 heightOrigin;
uniform vec2 heightSize;

// Per-sector seed (derived from sector ID + brush settings) shifts the hash
// inputs so different sectors don't scatter identically even when their
// lattices overlap in world space — e.g. tall grass and short grass painted
// over the same area land on different spots instead of stacking.
uniform vec2 seed;
uniform vec3 tintColor; // base color of the painted object itself (the brush's paint color)

// Lighting (mirrors the main shader's globals, kept cheap / Gouraud).
uniform vec3 ambient;   // globalColor * globalBrightness
uniform vec3 sunDir;    // direction the sun light travels (normalized)
uniform vec3 sunColor;  // dirColor * dirBrightness

out vec3 vColor;

float hash11(float n) { return fract(sin(n) * 43758.5453123); }

vec2 hash22(vec2 p) {
    p = vec2(dot(p, vec2(127.1, 311.7)), dot(p, vec2(269.5, 183.3)));
    return fract(sin(p) * 43758.5453);
}

// Bilinear structural-floor height at a world XZ, sampled from this sector's
// cached height texture.
float sampleHeight(vec2 world) {
    vec2 uv = (world - heightOrigin) / heightSize;
    return textureLod(heightTex, uv, 0.0).r;
}

// Ray-casting point-in-polygon test against the sector's own drawn points.
bool pointInPoly(vec2 p) {
    if (p.x < polyMin.x || p.x > polyMax.x || p.y < polyMin.y || p.y > polyMax.y) {
        return false;
    }
    bool inside = false;
    int j = numPolyPts - 1;
    for (int i = 0; i < numPolyPts; i++) {
        vec2 a = polyPts[i];
        vec2 b = polyPts[j];
        if (((a.y > p.y) != (b.y > p.y)) &&
            (p.x < (b.x - a.x) * (p.y - a.y) / (b.y - a.y) + a.x)) {
            inside = !inside;
        }
        j = i;
    }
    return inside;
}

void main() {
    int id = gl_InstanceID;
    int ix = id % gridN;
    int iz = id / gridN;

    // World-stable lattice. The blade is hashed from its INTEGER global lattice
    // index, not its float world position: as the camera moves, the (min corner,
    // ix) split that reaches a given world point changes, so the world position
    // recomputes with ~1 ULP of difference every frame. Feeding that through
    // sin()-based hashing produces a totally different jitter each frame and the
    // grass visibly crawls. The integer index is bit-identical regardless of the
    // split, so every blade is deterministic and stays put. mod() keeps the
    // index small for stable sin() precision far from the origin.
    vec2 baseIdx = floor((camXZ - vec2(range)) / spacing);
    vec2 gi = baseIdx + vec2(float(ix), float(iz));
    vec2 hgi = mod(gi, 2048.0);
    vec2 cellPos = gi * spacing;
    vec2 rnd = hash22(hgi + seed);
    cellPos += (rnd - 0.5) * spacing * 0.85;

    float dist = distance(cellPos, camXZ);
    float pick = hash11(dot(hgi + seed, vec2(12.9898, 78.233)));

    bool alive = pointInPoly(cellPos) && (dist <= range) && (pick <= density);

    if (!alive) {
        // Collapse the whole instance off-screen; it clips away.
        gl_Position = vec4(2.0, 2.0, 2.0, 1.0);
        vColor = vec3(0.0);
        return;
    }

    float baseY = sampleHeight(cellPos);
    vec3 basePt = vec3(cellPos.x, baseY, cellPos.y);

    float yaw = rnd.x * 6.2831853;
    float ca = cos(yaw), sa = sin(yaw);
    float sizeVar = 0.7 + 0.6 * rnd.y;

    // Shrink over a wide band toward the far edge of the ring so the boundary
    // dissolves gradually instead of reading as a hard circle around the camera.
    float fade = clamp(1.0 - smoothstep(range * 0.6, range, dist), 0.0, 1.0);

    vec3 off;
    if (isRock) {
        vec3 p = aLocal * rockScale * sizeVar;
        off = vec3(p.x * ca - p.z * sa, p.y, p.x * sa + p.z * ca);
        vec3 nrm = normalize(vec3(aNormal.x * ca - aNormal.z * sa, aNormal.y,
                                  aNormal.x * sa + aNormal.z * ca));
        vec3 base = tintColor * (0.8 + 0.4 * rnd.x);
        float ndl = max(dot(nrm, -normalize(sunDir)), 0.0);
        vColor = base * (ambient + sunColor * ndl);
    } else {
        float t = aParam;
        float h = bladeHeight * sizeVar;
        // Wind sway grows with height (t*t) and blows along a fixed world dir.
        float phase = rnd.x * 6.2831 + time * 1.8;
        float sway = (sin(phase) + 0.3 * sin(phase * 2.3)) * 0.18 * t * t;
        vec3 p = vec3(aLocal.x * bladeWidth * sizeVar, t * h, 0.0);
        off = vec3(p.x * ca, p.y, p.x * sa);
        vec2 windDir = normalize(vec2(0.8, 0.6));
        off.xz += windDir * sway * h;
        // Grass lit as an up-facing surface with a root→tip brightness gradient,
        // shaded around the brush's paint color rather than a hardcoded green.
        vec3 root = tintColor * 0.55;
        vec3 tip  = tintColor;
        vec3 base = mix(root, tip, t) * (0.85 + 0.3 * rnd.y);
        float ndl = max(dot(vec3(0.0, 1.0, 0.0), -normalize(sunDir)), 0.0);
        vColor = base * (ambient + sunColor * (0.4 + 0.6 * ndl));
    }

    vec3 worldPos = basePt + off * fade;
    gl_Position = projView * vec4(worldPos, 1.0);
}
