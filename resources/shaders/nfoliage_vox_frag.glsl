#version 430

// nfoliage_vox_frag.glsl — ray-marching a voxel brick (render/nfoliage.go;
// the box is nfoliage_vox_vert.glsl's).
//
// The ray enters at the box's surface and steps voxel by voxel (a 3D DDA,
// at most ten cells across a 4x4x4 brick). A voxel the brick's mask says is
// occupied STOPS the ray with the chance its coverage gives along the ray's
// direction — its projected areas along the axes and the body diagonals
// over its cross-section that way (internal/cluster's voxel.go), the nearest
// directions weighted most — times the
// instance's thinning fade. The chance is drawn per pixel and per frame, so
// TAA averages a canopy's gaps into the partial cover they are, as it
// averages the cut-out cards' dither; without TAA the draw is per pixel and
// steady.
//
// The first voxel that stops the ray is the pixel: its depth (written, so
// everything after composites against it), its normal drawn from the
// voxel's normal distribution (the mean's direction, spread by how little
// the normals in it agreed), its colouring (the instance's colour, accent
// or bark, by the shares the cards and stems inside it had), and the
// lighting foliage_vert.glsl gives a vertex, here per pixel.
//
// Each pixel casts its own ray, from the eye through its centre, into the
// brick's voxel frame through the inverse the vertex stage worked out.

flat in uvec4 vBrick;
flat in vec3 vOrigin;
flat in vec3 vRow0;
flat in vec3 vRow1;
flat in vec3 vRow2;
flat in vec3 vUpWorld;
flat in vec3 vMotionDelta;
flat in vec3 vColor;
flat in vec3 vAccent;
flat in vec4 vMisc;
flat in vec3 vBark;

uniform mat4 projView;
// The view's inverse and its viewport (x, y, w, h), to cast each pixel's
// ray.
uniform mat4 invProjView;
uniform vec4 viewport;
uniform vec3 camPos;
uniform int coverageDither;
uniform mat4 motionViewProj;
uniform mat4 motionPrevViewProj;
uniform int writeMotion;

// A box's fragment is on its front face and the march only goes deeper:
// the depth test may run first.
layout(depth_greater) out float gl_FragDepth;
layout(location = 0) out vec4 outputColor;
layout(location = 2) out vec2 outMotion;

vec3 unproject(float z) {
    vec2 ndc = (gl_FragCoord.xy - viewport.xy) / viewport.zw * 2.0 - 1.0;
    vec4 p = invProjView * vec4(ndc, z, 1.0);
    return p.xyz / p.w;
}

void main() {
    // This pixel's ray in the world, from ro along dw...
    vec3 ro = camPos;
    vec3 dw = unproject(1.0) - camPos;
    // ...and in the brick's voxels: local(s) = lo + s·d, world(s) = ro + s·dw.
    mat3 Ainv = transpose(mat3(vRow0, vRow1, vRow2));
    vec3 lo = Ainv * (ro - vOrigin);
    vec3 d = Ainv * dw;
    vec3 inv = 1.0 / max(abs(d), vec3(1e-9)) * sign(d + vec3(1e-30));
    vec3 t0 = (vec3(0.0) - lo) * inv;
    vec3 t1 = (vec3(4.0) - lo) * inv;
    vec3 tNear = min(t0, t1);
    vec3 tFar = max(t0, t1);
    float sEnter = max(max(max(tNear.x, tNear.y), tNear.z), 0.0);
    float sExit = min(min(tFar.x, tFar.y), tFar.z);
    if (sEnter >= sExit) {
        discard;
    }
    vec3 dn = normalize(d);

    // A 3D DDA from the cell the ray enters.
    vec3 entry = lo + d * sEnter;
    ivec3 cell = clamp(ivec3(floor(entry)), ivec3(0), ivec3(3));
    ivec3 stepv = ivec3(sign(d));
    vec3 next = (vec3(cell) + max(vec3(stepv), vec3(0.0)) - lo) * inv;
    vec3 delta = abs(inv);
    float s = sEnter;
    bool hit = false;
    uvec4 vox = uvec4(0u);
    int hitCell = 0;
    for (int i = 0; i < 12; i++) {
        if (any(lessThan(cell, ivec3(0))) || any(greaterThan(cell, ivec3(3))) || s > sExit) {
            break;
        }
        int l = cell.x + 4 * cell.y + 16 * cell.z;
        if (nfVoxelAt(vBrick.x, vBrick.y, vBrick.z, l, vox)) {
            float alpha = nfVoxelCoverage(vox, dn) * vMisc.x;
            float chance = nfHash01(uvec4(uvec2(gl_FragCoord.xy), uint(coverageDither) * 131u + uint(l), vBrick.w));
            if (chance < alpha) {
                hit = true;
                hitCell = l;
                break;
            }
        }
        // Step to the next cell.
        if (next.x < next.y && next.x < next.z) {
            s = next.x;
            next.x += delta.x;
            cell.x += stepv.x;
        } else if (next.y < next.z) {
            s = next.y;
            next.y += delta.y;
            cell.y += stepv.y;
        } else {
            s = next.z;
            next.z += delta.z;
            cell.z += stepv.z;
        }
    }
    if (!hit) {
        discard;
    }
    vec3 world = ro + s * dw;
    vec4 clip = projView * vec4(world, 1.0);
    gl_FragDepth = clamp(clip.z / clip.w * 0.5 + 0.5, gl_FragCoord.z, 1.0);
    vec3 bark = vBark * (0.8 + 0.4 * vMisc.y);
    uvec4 seed = uvec4(uvec2(gl_FragCoord.xy), uint(coverageDither) + 17u, vBrick.w * 64u + uint(hitCell));
    vec3 col = nfVoxelShade(vox, world, camPos, vRow0, vRow1, vRow2, vUpWorld, vColor, vAccent, bark, seed);
    outputColor = vec4(col, 1.0);
    outMotion = vec2(16384.0);
    if (writeMotion != 0) {
        vec4 c = motionViewProj * vec4(world, 1.0);
        vec4 pr = motionPrevViewProj * vec4(world + vMotionDelta, 1.0);
        outMotion = (c.xy / c.w - pr.xy / pr.w) * 0.5;
    }
}
