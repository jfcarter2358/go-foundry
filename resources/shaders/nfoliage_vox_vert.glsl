#version 430

// nfoliage_vox_vert.glsl — a Nanite foliage voxel brick, placed for the ray
// march in nfoliage_vox_frag.glsl (render/nfoliage.go): the path that draws
// voxels where the compute rasteriser does not (nfoliage_voxraster.go).
//
// Each instance of the draw is a reference (item, brick). The brick rides
// its bone rigidly, at its binding's place along it, and goes to the world
// the way a vertex of the plant does (nfoliage_common.glsl) — an affine map
// from the brick's VOXEL units (0..4 on each axis) to the world: an origin
// and one voxel's step along each axis. The fragment stage gets the map's
// inverse and casts each pixel's own ray through it; the box — the brick's
// six faces, back faces culled — only says which pixels to cast.
//
// (A point sprite a brick, sized to its footprint, was tried for the many
// bricks a few pixels across: on the GTX 1070 it cost as much as the boxes
// at 4 px and more above. Where the driver has 64-bit atomics those bricks
// go to the compute rasteriser instead.)

layout(location = 0) in vec3 aCorner;
// The reference: item, brick (nfoliage_scatter_comp.glsl).
layout(location = 4) in uvec2 aRef;

uniform mat4 projView;
uniform int writeMotion;

flat out uvec4 vBrick;      // mask lo, mask hi, first voxel, brick
flat out vec3 vOrigin;      // the brick's corner in the world
flat out vec3 vRow0;        // the inverse of the brick's linear map, by
flat out vec3 vRow1;        // rows: world steps to voxel steps, and its
flat out vec3 vRow2;        // transpose a voxel-frame normal to the world
flat out vec3 vUpWorld;
flat out vec3 vMotionDelta; // where the brick was last frame, less where it is
flat out vec3 vColor;
flat out vec3 vAccent;
flat out vec4 vMisc;        // fade, seed, 0, 0
flat out vec3 vBark;        // the shape's bark colour

void main() {
    vBrick = uvec4(0u);
    vOrigin = vec3(0.0);
    vRow0 = vec3(0.0);
    vRow1 = vec3(0.0);
    vRow2 = vec3(0.0);
    vUpWorld = vec3(0.0);
    vMotionDelta = vec3(0.0);
    vColor = vec3(0.0);
    vAccent = vec3(0.0);
    vMisc = vec4(0.0);
    vBark = vec3(0.0);
    if (!nfBeginRef(aRef)) {
        gl_Position = vec4(2.0, 2.0, 2.0, 1.0);
        return;
    }
    int bi = nFirst + int(nWhich);
    uvec4 b1 = texelFetch(nfBricks, bi * 2 + 1);
    vec3 o;
    mat3 A;
    nfBrickMap(false, bi, o, A);
    mat3 Ainv = inverse(A);
    vOrigin = o;
    vRow0 = vec3(Ainv[0][0], Ainv[1][0], Ainv[2][0]);
    vRow1 = vec3(Ainv[0][1], Ainv[1][1], Ainv[2][1]);
    vRow2 = vec3(Ainv[0][2], Ainv[1][2], Ainv[2][2]);
    vUpWorld = normalize(mat3(nModel) * nUp);

    gl_Position = projView * vec4(o + A * (aCorner * 4.0), 1.0);
    vBrick = uvec4(b1.x, b1.y, b1.z, uint(bi));
    if (writeMotion != 0) {
        vec3 po;
        mat3 pA;
        nfBrickMap(true, bi, po, pA);
        vMotionDelta = (po + pA * vec3(2.0)) - (o + A * vec3(2.0));
    }
    vColor = iColorRank.rgb;
    vAccent = iAccentSeed.rgb;
    vMisc = vec4(nFade, iAccentSeed.w, 0.0, 0.0);
    vBark = shapeBark[nShape];
}
