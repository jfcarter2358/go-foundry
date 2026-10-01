#version 430

// foliage_imp_vert.glsl — a foliage impostor (render/foliage_impostor.go):
// one quad per instance, turned to face the camera, showing the three baked
// views nearest the direction it is seen from.
//
// Placement, thinning and the tier window are foliage_common.glsl's, so an
// impostor hands over to and from the far mesh through the same dither the
// meshes use. The views are laid out hemi-octahedrally over the plant's
// upper hemisphere (its own frame — a tree on a slope leans with its view
// grid); the view direction, carried into that frame, falls in one triangle
// of grid points, and those three are blended by barycentric weight. Each is
// looked up through its OWN image basis: the quad's corner is projected onto
// that view's image plane, so neighbouring views line up where they overlap
// instead of sliding past each other.

uniform mat4 projView;
uniform float impGrid;

out vec2 vUV0;
out vec2 vUV1;
out vec2 vUV2;
flat out vec2 vCell0;
flat out vec2 vCell1;
flat out vec2 vCell2;
flat out vec3 vWeights;
flat out float vLayer;
flat out vec3 vAxX;
flat out vec3 vAxY;
flat out vec3 vAxZ;
flat out vec3 vColor;
flat out vec3 vAccent;
flat out float vSeed;
out float vFade;
out vec2 vXfade;
out vec3 vWorldPos;

vec3 hemiOctDecode(vec2 uv) {
    vec2 x = uv * 2.0 - 1.0;
    vec2 t = vec2(x.x + x.y, x.x - x.y) * 0.5;
    return normalize(vec3(t.x, 1.0 - abs(t.x) - abs(t.y), t.y));
}

vec2 hemiOctEncode(vec3 d) {
    d.y = max(d.y, 0.0);
    d /= abs(d.x) + abs(d.y) + abs(d.z);
    return vec2(d.x + d.z, d.x - d.z) * 0.5 + 0.5;
}

// render/foliage_impostor.go impFrameBasis.
void frameBasis(vec3 dir, out vec3 right, out vec3 up) {
    vec3 ref = abs(dir.y) > 0.999 ? vec3(0.0, 0.0, -1.0) : vec3(0.0, 1.0, 0.0);
    right = normalize(cross(ref, dir));
    up = cross(dir, right);
}

// Where offset `off` from the centre lands in grid view `cell`, 0..1 across
// the view, for a sphere of radius r.
vec2 viewUV(vec2 cell, vec3 off, float r) {
    vec3 right, up;
    frameBasis(hemiOctDecode(cell / (impGrid - 1.0)), right, up);
    return vec2(dot(off, right), dot(off, up)) / (2.0 * r) + 0.5;
}

void main() {
    if (!foliageBegin() || bucketInfo[fBucket].y < 0) {
        gl_Position = vec4(2.0, 2.0, 2.0, 1.0);
        vUV0 = vUV1 = vUV2 = vec2(0.0);
        vCell0 = vCell1 = vCell2 = vec2(0.0);
        vWeights = vec3(0.0);
        vLayer = 0.0;
        vAxX = vAxY = vAxZ = vec3(0.0);
        vColor = vAccent = vec3(0.0);
        vSeed = 0.0;
        vFade = 0.0;
        vXfade = vec2(0.0);
        vWorldPos = vec3(0.0);
        return;
    }
    vec4 sph = bucketImp[fBucket];
    // A sphere grows evenly: the mean of the across and up growth.
    float scale = iPosScale.w * pow(fComp, 0.5 * (1.0 + shapeMore[fShape].x));
    vec3 C = sph.xyz * scale;
    float R = sph.w * scale;

    vec3 ax = normalize(mat3(model) * fX);
    vec3 ay = normalize(mat3(model) * fUpL);
    vec3 az = normalize(mat3(model) * fZ);
    vec3 centre = (model * vec4(iPosScale.xyz + fX * C.x + fUpL * C.y + fZ * C.z, 1.0)).xyz;
    vec3 Vw = normalize(camPos - centre);
    vec3 v = vec3(dot(Vw, ax), dot(Vw, ay), dot(Vw, az));

    vec2 g = hemiOctEncode(v) * (impGrid - 1.0);
    vec2 f0 = clamp(floor(g), vec2(0.0), vec2(impGrid - 2.0));
    vec2 fr = g - f0;
    vec2 a, b, c;
    if (fr.x + fr.y < 1.0) {
        a = f0;
        b = f0 + vec2(1.0, 0.0);
        c = f0 + vec2(0.0, 1.0);
        vWeights = vec3(1.0 - fr.x - fr.y, fr.x, fr.y);
    } else {
        a = f0 + vec2(1.0, 1.0);
        b = f0 + vec2(1.0, 0.0);
        c = f0 + vec2(0.0, 1.0);
        vWeights = vec3(fr.x + fr.y - 1.0, 1.0 - fr.y, 1.0 - fr.x);
    }
    vWeights = max(vWeights, vec3(0.0));

    // The quad: through the centre, square to the view, the sphere's size.
    vec3 br, bu;
    frameBasis(normalize(v), br, bu);
    vec3 off = (br * aPos.x + bu * aPos.y) * R;
    vUV0 = viewUV(a, off, R);
    vUV1 = viewUV(b, off, R);
    vUV2 = viewUV(c, off, R);
    vCell0 = a / impGrid;
    vCell1 = b / impGrid;
    vCell2 = c / impGrid;
    vLayer = float(bucketInfo[fBucket].y);

    vec3 P = C + off;
    vec4 w = model * vec4(iPosScale.xyz + fX * P.x + fUpL * P.y + fZ * P.z, 1.0);
    vAxX = ax;
    vAxY = ay;
    vAxZ = az;
    vColor = iColorRank.rgb;
    vAccent = iAccentSeed.rgb;
    vSeed = iAccentSeed.w;
    vFade = fFade;
    vXfade = fXfade;
    vWorldPos = w.xyz;
    gl_Position = projView * w;
}
