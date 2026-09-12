// volume_common.glsl — what every volumetric march shares: the noise, the two
// density models, and light.
//
// Spliced after fog.glsl (which it needs for fogShapeInterval) into the
// volume pass (volume_frag.glsl) and the cloud shadow bake
// (cloud_shadow_bake_frag.glsl) — see VolumetricInit in render/volumetric.go.
// The bake has to evaluate EXACTLY the density the pass draws, or a cloud's
// shadow would be the shadow of a different cloud; one file is how that is
// structural rather than remembered.
//
// Two density models:
//
//   cloudDensity — a region of cloud: a volume's shape (box, sphere, cylinder)
//   or the sky deck's slab, filled with tileable noise thresholded by
//   coverage. This is weather.
//
//   blobDensity — up to MAX_VOLUME_BLOBS soft balls, each one particle of a
//   volume particle layer, displaced by noise that rolls outward over its
//   life. This is a fireball, or the smoke after one.
//
// Every texture lookup is textureLod with an explicit mip, because every one of
// them happens inside a loop whose iteration count and early-outs vary per
// pixel, where implicit derivatives are undefined.

// The noise volumes (internal/noise3d). The texel counts must match
// noise3d.ShapeSize and noise3d.DetailSize: they are what turns a sample's
// world-space footprint into a mip level.
uniform sampler3D volNoiseShape;   // R Perlin-Worley, G Worley fbm
uniform sampler3D volNoiseDetail;  // R Worley fbm
#define VOL_SHAPE_TEXELS  64.0
#define VOL_DETAIL_TEXELS 32.0

// Shape values. 0..2 match world.VolumeShape and fog.glsl's FOG_VOL_*; SLAB is
// the sky deck, which has no volume behind it.
#define VOL_SHAPE_BOX      0
#define VOL_SHAPE_SPHERE   1
#define VOL_SHAPE_CYLINDER 2
#define VOL_SHAPE_SLAB     3

// CloudParams is one cloud, as render/volumetric.go's uploadCloudParams writes
// it. A struct so the pass (one cloud per draw) and the bake (an array of them)
// read the same fields by the same names.
struct CloudParams {
    int   shape;
    mat3  basis;        // world offset -> normalized shape space (not the slab)
    vec3  center;
    float slabBottom;   // the slab only
    float slabTop;
    float softness;     // fraction of the half-extent the edge fades over
    float coverage;
    float density;      // extinction per world unit at full
    float noiseFreq;    // 1 / Scale
    float detailFreq;   // 1 / Detail
    float erosion;
    // Wind * time for each noise, in world units, wrapped by that noise's own
    // repeat on the CPU so it never grows past float precision in a long
    // session. The detail noise drifts faster, so its edges boil.
    vec3  windOffset;
    vec3  detailOffset;
    vec3  albedo;
    float absorption;
    float ambient;
};

float volRemap(float v, float lo, float hi) {
    return (v - lo) / max(hi - lo, 1e-4);
}

// volNoiseLod is the mip at which a noise of freq repeats per world unit, with
// texels texels per repeat, is sampled when one sample stands for footprint
// world units. A far cloud samples a coarse mip rather than aliasing into
// sparkle.
float volNoiseLod(float footprint, float freq, float texels) {
    return max(log2(max(footprint * freq * texels, 1e-6)), 0.0);
}

// slabInterval narrows [t0, t1] to the part of a ray between two heights.
bool slabInterval(float bottom, float top, vec3 ro, vec3 rd, inout float t0, inout float t1) {
    if (abs(rd.y) < 1e-6) {
        if (ro.y < bottom || ro.y > top) {
            return false;
        }
    } else {
        float ta = (bottom - ro.y) / rd.y;
        float tb = (top - ro.y) / rd.y;
        t0 = max(t0, min(ta, tb));
        t1 = min(t1, max(ta, tb));
    }
    return t1 > t0;
}

// cloudInterval narrows [t0, t1] to where a ray is inside a cloud's region.
bool cloudInterval(CloudParams c, vec3 ro, vec3 rd, inout float t0, inout float t1) {
    if (c.shape == VOL_SHAPE_SLAB) {
        return slabInterval(c.slabBottom, c.slabTop, ro, rd, t0, t1);
    }
    return fogShapeInterval(c.shape, c.basis, c.center, ro, rd, t0, t1);
}

// cloudHeight is how far up through its region p is: 0 at the bottom, 1 at the
// top. What the height profile shapes, and what the ambient light is graded by.
float cloudHeight(CloudParams c, vec3 p) {
    if (c.shape == VOL_SHAPE_SLAB) {
        return (p.y - c.slabBottom) / max(c.slabTop - c.slabBottom, 1e-3);
    }
    return (c.basis * (p - c.center)).y * 0.5 + 0.5;
}

// cloudShapeMask is 0 outside the region, rising to 1 once softness in from
// its walls, and shaped by height into a flat-bottomed, round-topped mass —
// which is what a cumulus is, and what stops a box of cloud reading as a box.
float cloudShapeMask(CloudParams c, vec3 p) {
    float h = cloudHeight(c, p);
    if (h <= 0.0 || h >= 1.0) {
        return 0.0;
    }
    float edge = 1.0;
    float profile;
    if (c.shape == VOL_SHAPE_SLAB) {
        profile = smoothstep(0.0, 0.2, h) * (1.0 - smoothstep(0.55, 1.0, h));
    } else {
        vec3 q = c.basis * (p - c.center);
        float d;
        if (c.shape == VOL_SHAPE_SPHERE) {
            d = 1.0 - length(q);
        } else if (c.shape == VOL_SHAPE_CYLINDER) {
            d = 1.0 - length(q.xz);
        } else {
            d = 1.0 - max(abs(q.x), abs(q.z));
        }
        edge = clamp(d / max(c.softness, 1e-3), 0.0, 1.0);
        // A sphere already rounds its own top; it only wants its base flattened.
        profile = (c.shape == VOL_SHAPE_SPHERE)
            ? smoothstep(0.0, 0.15, h)
            : smoothstep(0.0, 0.12, h) * (1.0 - smoothstep(0.6, 1.0, h));
    }
    return edge * profile;
}

// cloudDensity is the extinction per world unit at p. footprint is the world
// size one sample stands for (mip selection); detail gates the erosion lookup,
// which the light march skips — a shadow does not need wisps.
float cloudDensity(CloudParams c, vec3 p, float footprint, bool detail) {
    float mask = cloudShapeMask(c, p);
    if (mask <= 0.0) {
        return 0.0;
    }
    vec3 uvw = (p + c.windOffset) * c.noiseFreq;
    vec2 n = textureLod(volNoiseShape, uvw,
                        volNoiseLod(footprint, c.noiseFreq, VOL_SHAPE_TEXELS)).rg;
    float base = n.r * 0.7 + n.g * 0.3;
    // Coverage is the threshold: at 0.3 only the brightest 30% of the noise is
    // cloud, at 1 all of it is. Multiplying by the mask BEFORE thresholding is
    // what makes the edges thin out rather than stop at a wall.
    float d = clamp(volRemap(base * mask, 1.0 - c.coverage, 1.0), 0.0, 1.0);
    if (d <= 0.0) {
        return 0.0;
    }
    if (detail && c.erosion > 0.0) {
        vec3 duvw = (p + c.detailOffset) * c.detailFreq;
        float det = textureLod(volNoiseDetail, duvw,
                               volNoiseLod(footprint, c.detailFreq, VOL_DETAIL_TEXELS)).r;
        // Eroded hardest where the cloud is thinnest, which is its edge; the
        // dense core survives.
        d = clamp(volRemap(d, det * c.erosion * (1.0 - d), 1.0), 0.0, 1.0);
    }
    return d * c.density;
}

// ---------- particle blobs ----------

// MUST match render.MaxVolumeBlobs.
#define MAX_VOLUME_BLOBS 16

uniform int   volBlobCount;
uniform vec4  volBlobSphere[MAX_VOLUME_BLOBS]; // xyz centre, w radius
uniform vec4  volBlobGlow[MAX_VOLUME_BLOBS];   // rgb emitted light, a core density
uniform vec4  volBlobState[MAX_VOLUME_BLOBS];  // x life t, y erosion, z seed
uniform float volBlobNoiseScale;               // noise repeats per radius
uniform float volBlobDisplacement;             // fraction of radius
uniform float volBlobBillow;                   // repeats rolled outward per life

// blobDensity is the summed extinction of every blob at p, and adds each one's
// emitted light, weighted by its own density, into glow — which is a source
// term per world unit, integrated exactly as scattered light is.
float blobDensity(vec3 p, float footprint, bool detail, inout vec3 glow) {
    float sum = 0.0;
    for (int i = 0; i < MAX_VOLUME_BLOBS; ++i) {
        if (i >= volBlobCount) {
            break;
        }
        vec3 off = p - volBlobSphere[i].xyz;
        float r = volBlobSphere[i].w;
        float reach = r * (1.0 + volBlobDisplacement);
        float dd = dot(off, off);
        if (dd >= reach * reach) {
            continue;
        }
        float dist = sqrt(dd);
        float dn = dist / r;
        vec3 outward = off / max(dist, 1e-4);
        vec4 st = volBlobState[i];

        // Noise in the blob's own space, so it scales with the blob, offset by
        // its seed, and scrolled outward along the radius as it ages — the
        // churn of a fireball rolling over itself as it grows.
        float freq = volBlobNoiseScale / r;
        vec3 uvw = off * freq + vec3(st.z * 17.3, st.z * 31.7, st.z * 7.1)
                   - outward * st.x * volBlobBillow;
        float n = textureLod(volNoiseShape, uvw, volNoiseLod(footprint, freq, VOL_SHAPE_TEXELS)).r;

        // The surface pushed in and out by the noise, then a soft shell inside
        // it rather than a hard edge.
        float surface = 1.0 - dn + (n - 0.5) * 2.0 * volBlobDisplacement;
        float d = smoothstep(0.0, 0.3, surface);
        if (d <= 0.0) {
            continue;
        }
        if (detail && st.y > 0.0) {
            float det = textureLod(volNoiseDetail, uvw * 2.7,
                                   volNoiseLod(footprint, freq * 2.7, VOL_DETAIL_TEXELS)).r;
            d = clamp(volRemap(d, det * st.y, 1.0), 0.0, 1.0);
        }
        float core = d * volBlobGlow[i].a;
        sum += core;
        // Hotter at the heart than the rim — but the rim still glows at half:
        // a dense ball is only ever SEEN at its rim, so a rim that went dark
        // would hide the colour the author gave the fire.
        glow += volBlobGlow[i].rgb * core * clamp(1.5 - dn, 0.5, 1.0);
    }
    return sum;
}

// ---------- light ----------

// The sun, and the sky light filling the shadowed side, resolved on the CPU
// from the level's first directional light, its global light and its sky (see
// volumeLighting in render/volumetric.go). volSunColor is zero with no sun.
uniform vec3 volSunDir;         // unit, TOWARD the sun
uniform vec3 volSunColor;
uniform vec3 volAmbientTop;
uniform vec3 volAmbientBottom;

// Point lights, under fragment.glsl's own names so world.UploadLightsTo drives
// them — a torch lights the smoke rising past it.
#define VOL_MAX_POINT_LIGHTS 8
uniform int   numPointLights;
uniform vec3  pointLightPos[VOL_MAX_POINT_LIGHTS];
uniform vec3  pointLightColor[VOL_MAX_POINT_LIGHTS];
uniform float pointLightBrightness[VOL_MAX_POINT_LIGHTS];
uniform float pointLightRadius[VOL_MAX_POINT_LIGHTS];

// volHG is the Henyey-Greenstein phase function scaled by 4 pi, so an
// isotropic medium (g = 0) scatters exactly 1.
float volHG(float cosTheta, float g) {
    float g2 = g * g;
    return (1.0 - g2) / pow(max(1.0 + g2 - 2.0 * g * cosTheta, 1e-4), 1.5);
}

// volPhase is two lobes: strong forward scattering, which is the bright silver
// lining of a cloud in front of the sun, and a weaker backward lobe so the
// side facing the sun is not flat.
float volPhase(float cosTheta) {
    return mix(volHG(cosTheta, 0.65), volHG(cosTheta, -0.25), 0.3);
}

vec3 volPointLights(vec3 p) {
    vec3 sum = vec3(0.0);
    for (int i = 0; i < VOL_MAX_POINT_LIGHTS; ++i) {
        if (i >= numPointLights) {
            break;
        }
        float dist = distance(pointLightPos[i], p);
        if (dist < pointLightRadius[i]) {
            float a = 1.0 - dist / pointLightRadius[i];
            sum += pointLightColor[i] * pointLightBrightness[i] * a * a;
        }
    }
    return sum;
}
