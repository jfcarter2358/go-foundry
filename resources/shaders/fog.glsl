// fog.glsl — fog, shared by every stage that shades world geometry.
//
// Two kinds, one file. DISTANCE fog is the air of the whole level: a
// function of how far away a point is, with no position of its own. A FOG
// VOLUME is a placed region that is full of fog, and its term is the length
// of the view ray that was inside it. They compose — see applyFog at the
// bottom, which is the one entry point every pass calls.
//
// Injected after the #version line of fragment.glsl (world geometry, water and
// the detail tessellation/scatter programs that reuse it), of biome_light.glsl's
// consumers (the scatter in biome_frag.glsl, the ground patch in
// ground_frag.glsl and sprites in sprite_frag.glsl) and of sky_frag.glsl — see
// InjectFog in gfx/graphics.go and biomeLightSnippet in render/biome_light.go.
//
// The same trick biome_light.glsl uses: ONE declaration of the uniform names,
// compiled into every program that shades, so world.UploadFogTo drives all of
// them and there is no per-pass fog model to keep in step with the others. A
// wall, the grass in front of it, the rock scattered over that grass and the
// sprite standing on it all fade at exactly the same rate, which is the whole
// point — fog that disagreed between passes would read as the passes
// disagreeing about where things are.
//
// Distance is RADIAL (from the eye), not the view-axis depth a fixed-function
// fog would use. Planar depth makes the fog on a fixed object change as the
// camera turns its head, which reads as the world breathing.
//
// A third part, the ATMOSPHERE (docs/canyon/PHASE_6.md), is the air itself
// rather than a fade: exponential height fog lying in the low ground,
// aerial perspective — haze that thickens with range and shifts toward blue
// on the way — and the sun's forward-scattering glow through both. It is a
// medium with an optical depth along the view ray, integrated in closed form
// (fogAtmosSegment), not a blend factor, so a valley floor seen from the rim
// and the same floor seen from inside the fog agree. Off (fogAtmos 0) unless
// a level's fog block asks for it.

// The fog's parameters — fogMode (0 = off, 1 = linear, 2 = exponential, 3 =
// exponential-squared; world.FogMode's values), fogColor, fogStart, fogEnd,
// fogDensity, fogMaxOpacity, fogCamPos — and the volumes' below are members
// of the scene block (scene_block.glsl), which is what "one declaration of the
// names, driven by one upload" has become.
#include "scene_block.glsl"

// ---------- HDR: display-referred colour into the scene's units ----------
//
// In an HDR frame (hdrMode 1, gfx/hdr.go) the frame's last pass maps scene
// light to the screen through exposure and the ACES fit. Colours authored as
// screen colour — the fog's, the sky's, an unlit surface's, and whatever an
// HDR-unaware pass shaded — are carried back through the curve's inverse
// first, so they reach the screen as authored. Off, it is the identity.
vec3 hdrFromDisplay(vec3 c) {
    if (hdrMode == 0) {
        return c;
    }
    vec3 y = clamp(c, vec3(0.0), vec3(0.999));
    // The positive root of (Cy - A)x² + (Dy - B)x + Ey = 0 (gfx.ACESFilmInverse).
    vec3 a = 2.43 * y - 2.51;
    vec3 b = 0.59 * y - 0.03;
    vec3 e = 0.14 * y;
    vec3 x = (-b - sqrt(max(b * b - 4.0 * a * e, vec3(0.0)))) / (2.0 * a);
    return x / max(hdrExposure, 1e-4);
}

// hdrLightMax is the most summed light a surface keeps: 1 in LDR, as it
// always was; gfx.HDRLightMax in an HDR frame.
float hdrLightMax() {
    return hdrMode != 0 ? 64.0 : 1.0;
}

// fogFactor is how much of fogColor this point has turned into: 0 = untouched,
// 1 = the fog and nothing else.
float fogFactor(vec3 worldPos) {
    // 4 is world.FogNone: a fog block with the atmosphere below and no
    // distance fade of its own.
    if (fogMode == 0 || fogMode == 4) {
        return 0.0;
    }
    float d = distance(worldPos, fogCamPos);
    float f;
    if (fogMode == 1) {
        f = (d - fogStart) / max(fogEnd - fogStart, 1e-4);
    } else if (fogMode == 2) {
        f = 1.0 - exp(-fogDensity * max(d - fogStart, 0.0));
    } else {
        float t = fogDensity * max(d - fogStart, 0.0);
        f = 1.0 - exp(-t * t);
    }
    return clamp(f, 0.0, 1.0) * fogMaxOpacity;
}

// ---------- the atmosphere ----------
//
// Two exponential media share the ray: HEIGHT FOG, dense at its base and
// thinning by e every 1/falloff metres up (ground fog, a canyon's floor haze),
// and the AERIAL haze, the same shape with a scale height of hundreds of
// metres, so across a level it is nearly uniform and it is range, not height,
// that thickens it. The aerial part's extinction is per channel
// (fogAerialTint): blue dies first, as Rayleigh scattering has it, so far
// rock shifts toward the haze's blue before its red is gone.
//
// Each medium's optical depth along the segment from ro, length len along
// unit dir, is the closed form of the integral of its density:
//
//	∫0..len  σ·e^(−k·(y0 + dir.y·t − base)) dt
//	   = σ·len·(e^(−h0) − e^(−h1)) / (h1 − h0),   h = k·(y − base)
//
// which tends to σ·len·e^(−h0) as the ray goes level. The exponents are
// clamped where e^ would overflow (deep below the base, where the medium has
// long since hidden everything), never on the side that would make a ray
// climbing out of the fog see more of it than there is.
//
// What the medium sends toward the eye is its colour, with the sun's light
// redistributed by a Henyey-Greenstein lobe around the sun's direction,
// relative to isotropic: a haze looking into the sun glows and looking away
// is a little duller, and on average it is its own colour. That light is
// unshadowed: there is no march.

// fogExpDepth is the integral above for unit density at the base.
float fogExpDepth(vec3 ro, vec3 dir, float len, float base, float k) {
    k = max(k, 1e-6);
    float h0 = k * (ro.y - base);
    float dh = k * dir.y * len;
    float e0 = exp(-clamp(h0, -30.0, 80.0));
    if (abs(dh) < 1e-4) {
        return len * e0;
    }
    float e1 = exp(-clamp(h0 + dh, -30.0, 80.0));
    return max(len * (e0 - e1) / dh, 0.0);
}

// fogSunLobe is the in-scattered sun's phase toward the eye, looking along
// dir: Henyey-Greenstein over isotropic, so 1 on average over the sphere.
float fogSunLobe(vec3 dir) {
    float g = clamp(fogSunG, -0.95, 0.95);
    float mu = dot(dir, fogSunDir);
    float den = max(1.0 + g * g - 2.0 * g * mu, 1e-4);
    return (1.0 - g * g) / (den * sqrt(den));
}

// fogAtmosMedium is the two media over the segment from ro, len along unit
// dir: what survives of what is behind it (T, per channel, capped by
// fogMaxOpacity as the distance fog is) and the medium's own colour (each
// part's by its share of the depth), as authored: screen colour.
void fogAtmosMedium(vec3 ro, vec3 dir, float len, out vec3 T, out vec3 base) {
    float th = fogHeightDensity > 0.0
        ? fogHeightDensity * fogExpDepth(ro, dir, len, fogHeightBase, fogHeightFalloff) : 0.0;
    vec3 ta = fogAerialDensity > 0.0
        ? fogAerialTint * (fogAerialDensity * fogExpDepth(ro, dir, len, fogAerialBase, fogAerialFalloff))
        : vec3(0.0);
    vec3 tau = vec3(th) + ta;
    T = max(exp(-tau), vec3(1.0 - fogMaxOpacity));
    base = (fogHeightColor * th + fogAerialColor * ta) / max(tau, vec3(1e-6));
}

// fogAtmosSegmentDisplay is the atmosphere over the segment in screen
// colour: what the air adds (returned) and what survives (T), so the result
// is surface * T + returned. The sun's lobe is added less its average: the
// sun's light is moved toward the eye looking into it and away from it
// looking away, and the haze's mean brightness stays its colour's. Added on
// top, a lobe bright enough to see near the sun turned a whole sky white.
vec3 fogAtmosSegmentDisplay(vec3 ro, vec3 dir, float len, out vec3 T) {
    vec3 base;
    fogAtmosMedium(ro, dir, len, T, base);
    vec3 c = max(base + fogSunColor * (fogSunLobe(dir) - 1.0), vec3(0.0));
    return c * (1.0 - T);
}

// fogAtmosSegment is fogAtmosSegmentDisplay in the frame's units: the same
// outside an HDR frame. In one (gfx/hdr.go) the medium's colour, being screen
// colour, is carried into the scene's units, and the sun's lobe — light, not
// a colour — is added there as it is: carried through the curve's inverse,
// a lobe that took a channel past white would clip each channel at its own
// point and draw coloured rings round the sun.
vec3 fogAtmosSegment(vec3 ro, vec3 dir, float len, out vec3 T) {
    if (hdrMode == 0) {
        return fogAtmosSegmentDisplay(ro, dir, len, T);
    }
    vec3 base;
    fogAtmosMedium(ro, dir, len, T, base);
    vec3 c = max(hdrFromDisplay(base) + fogSunColor * (fogSunLobe(dir) - 1.0), vec3(0.0));
    return c * (1.0 - T);
}

// fogAtmosAt is fogAtmosSegment from the eye to worldPos.
vec3 fogAtmosAt(vec3 worldPos, out vec3 T) {
    vec3 seg = worldPos - fogCamPos;
    float len = length(seg);
    if (fogAtmos == 0 || len < 1e-4) {
        T = vec3(1.0);
        return vec3(0.0);
    }
    return fogAtmosSegment(fogCamPos, seg / len, len, T);
}

// ---------- fog volumes ----------
//
// A fog volume is a placed region that is FULL of fog, as against the distance
// fog above which is everywhere. The two are different questions: distance fog
// asks "how far away is this", a volume asks "how much of the air between the
// eye and this point was inside that box".
//
// So the term is not a function of distance at all — it is the LENGTH of the
// eye-to-fragment segment that lies inside the volume, run through the same
// exponential extinction. That is what makes a fog bank behave like a bank: it
// thickens as you look along it and thins as you look across it, a wall seen
// through a corner of it is only lightly touched, and walking in fills the
// screen because the segment now starts inside. A "distance from the centre"
// falloff gets all four of those wrong, and gets them wrong in a way that looks
// fine standing still.
//
// Every shape is solved in NORMALIZED space, where the volume is the unit box,
// unit sphere or unit cylinder: fogVolBasis maps a world-space offset from the
// centre through the inverse rotation and then divides by the half-extents, so
// one intersection routine per shape covers every size and orientation. The ray
// direction is carried through that same matrix WITHOUT renormalizing, which is
// what keeps the parameter t measured in world units — the span has to come out
// in metres, not in half-extents, or a wide volume would fog less than a narrow
// one at the same density.

// The volumes (numFogVolumes, fogVol*[MAX_FOG_VOLUMES]) are scene-block
// members too.

// Shape values match world.VolumeShape (volume.go): 0 box, 1 sphere, 2 cylinder.
#define FOG_VOL_BOX      0
#define FOG_VOL_SPHERE   1
#define FOG_VOL_CYLINDER 2

// fogShapeInterval narrows [t0, t1] to the part of the ray ro + t*dir that lies
// inside a volume shape, and reports whether anything is left. dir must be a
// unit vector for t to be in world units. The shape is given as its basis (see
// the note above), centre and kind, so the volumetric passes
// (volume_common.glsl) solve their cloud volumes with the SAME intersection a
// fog bank uses — a cloud and a fog bank placed in one box cannot disagree
// about where its walls are.
//
// The interval arrives already clamped by the caller, which is what makes the
// two cases that matter fall out with no special-casing: a ray starting INSIDE
// the shape keeps t0 at the caller's 0 (the fog starts at the eye), and a shape
// behind the fragment is clipped away by the caller's t1 rather than fogging
// things in front of it.
bool fogShapeInterval(int shape, mat3 basis, vec3 center, vec3 ro, vec3 dir,
                      inout float t0, inout float t1) {
    vec3 o = basis * (ro - center);
    vec3 d = basis * dir;

    if (shape == FOG_VOL_SPHERE) {
        // Unit sphere. |o + t*d| = 1.
        float a = dot(d, d);
        float b = 2.0 * dot(o, d);
        float c = dot(o, o) - 1.0;
        float disc = b * b - 4.0 * a * c;
        if (a < 1e-12 || disc <= 0.0) {
            return false;
        }
        float sq = sqrt(disc);
        t0 = max(t0, (-b - sq) / (2.0 * a));
        t1 = min(t1, (-b + sq) / (2.0 * a));
    } else {
        // Box and cylinder share the |y| <= 1 slab; the box adds the other two
        // slabs, the cylinder a unit disc in XZ.
        int lo = (shape == FOG_VOL_CYLINDER) ? 1 : 0;
        int hi = (shape == FOG_VOL_CYLINDER) ? 1 : 2;
        for (int ax = lo; ax <= hi; ++ax) {
            if (abs(d[ax]) < 1e-6) {
                // Parallel to this pair of planes: either always between them
                // or never, and never means the whole ray misses.
                if (abs(o[ax]) > 1.0) {
                    return false;
                }
            } else {
                float inv = 1.0 / d[ax];
                float ta = (-1.0 - o[ax]) * inv;
                float tb = ( 1.0 - o[ax]) * inv;
                t0 = max(t0, min(ta, tb));
                t1 = min(t1, max(ta, tb));
            }
        }
        if (shape == FOG_VOL_CYLINDER) {
            vec2 o2 = o.xz;
            vec2 d2 = d.xz;
            float a = dot(d2, d2);
            float c = dot(o2, o2) - 1.0;
            if (a < 1e-12) {
                // Straight up the axis: inside for its whole length, or outside
                // for all of it.
                if (c > 0.0) {
                    return false;
                }
            } else {
                float b = 2.0 * dot(o2, d2);
                float disc = b * b - 4.0 * a * c;
                if (disc <= 0.0) {
                    return false;
                }
                float sq = sqrt(disc);
                t0 = max(t0, (-b - sq) / (2.0 * a));
                t1 = min(t1, (-b + sq) / (2.0 * a));
            }
        }
    }
    return t1 > t0;
}

// fogVolumeSpan is how far the segment from fogCamPos to worldPos travels inside
// volume i, in world units. 0 when the segment misses it entirely.
float fogVolumeSpan(int i, vec3 worldPos) {
    vec3 seg = worldPos - fogCamPos;
    float segLen = length(seg);
    if (segLen < 1e-5) {
        return 0.0;
    }
    float t0 = 0.0;
    float t1 = segLen;
    if (!fogShapeInterval(fogVolShape[i], fogVolBasis[i], fogVolCenter[i],
                          fogCamPos, seg / segLen, t0, t1)) {
        return 0.0;
    }
    return max(t1 - t0, 0.0);
}

// fogVolumeOpacity is volume i's share of this pixel: how much of its colour has
// replaced what was behind it.
float fogVolumeOpacity(int i, vec3 worldPos) {
    float span = fogVolumeSpan(i, worldPos);
    if (span <= 0.0) {
        return 0.0;
    }
    return clamp(1.0 - exp(-fogVolDensity[i] * span), 0.0, 1.0) * fogVolMaxOpacity[i];
}

// applyFogVolumes blends every volume the segment crosses over the surface,
// in level order.
//
// Level order, rather than sorted back-to-front: two overlapping volumes are the
// one case the order is visible, sorting per-pixel is not something a fragment
// shader can do to a uniform array, and volumes are authored to fill rooms
// rather than to be stacked. Each one composites correctly against everything
// already there, so a single volume — which is the case that exists — is exact.
vec3 applyFogVolumes(vec3 color, vec3 worldPos) {
    for (int i = 0; i < MAX_FOG_VOLUMES; ++i) {
        if (i >= numFogVolumes) {
            break;
        }
        color = mix(color, hdrFromDisplay(fogVolColor[i]), fogVolumeOpacity(i, worldPos));
    }
    return color;
}

// applyFogVolumesDisplay is applyFogVolumes in screen colour (the sky's,
// which is carried into the scene's units once, at its end).
vec3 applyFogVolumesDisplay(vec3 color, vec3 worldPos) {
    for (int i = 0; i < MAX_FOG_VOLUMES; ++i) {
        if (i >= numFogVolumes) {
            break;
        }
        color = mix(color, fogVolColor[i], fogVolumeOpacity(i, worldPos));
    }
    return color;
}

// fogVolumeTransmittance is how much of an ADDITIVE surface survives the
// volumes in front of it — the same reasoning as applyFogAdditive: the
// framebuffer already holds them, so a spark deep in a fog bank is swallowed
// rather than tinted.
float fogVolumeTransmittance(vec3 worldPos) {
    float t = 1.0;
    for (int i = 0; i < MAX_FOG_VOLUMES; ++i) {
        if (i >= numFogVolumes) {
            break;
        }
        t *= 1.0 - fogVolumeOpacity(i, worldPos);
    }
    return t;
}

// ---------- ambient volumes: the haze inside them ----------
//
// The fog's colour is light the sky put into the air; inside an ambient
// volume (world/volume_ambient.go) the sky does not reach, so the share of
// the eye-to-surface segment that lies inside one is dimmed by the volume's
// cut (the mean of its sky and ground factors, weighted by its fog share,
// ambVolParams.z). 1 when no volume is crossed.
float ambVolFogPath(vec3 worldPos) {
    if (numAmbVolumes <= 0) {
        return 1.0;
    }
    vec3 seg = worldPos - fogCamPos;
    float segLen = length(seg);
    if (segLen < 1e-4) {
        return 1.0;
    }
    vec3 dir = seg / segLen;
    float f = 1.0;
    for (int i = 0; i < MAX_AMBIENT_VOLUMES; ++i) {
        if (i >= numAmbVolumes) {
            break;
        }
        float t0 = 0.0;
        float t1 = segLen;
        if (!fogShapeInterval(int(ambVolCenter[i].w + 0.5), ambVolBasis[i], ambVolCenter[i].xyz,
                              fogCamPos, dir, t0, t1)) {
            continue;
        }
        float frac = clamp((t1 - t0) / segLen, 0.0, 1.0);
        float k = mix(1.0, 0.5 * (ambVolParams[i].x + ambVolParams[i].y), ambVolParams[i].z);
        f *= 1.0 - frac * (1.0 - k);
    }
    return f;
}

// ---------- what a shading pass calls ----------

// applyFog is the ordinary case: the surface is replaced by the fog in front of
// it, in proportion. Its colour is screen colour (every pass but
// fragment.glsl's lit surfaces): in an HDR frame it is carried into the
// scene's units first (hdrFromDisplay), so it reaches the screen as shaded.
vec3 applyFogScene(vec3 color, vec3 worldPos);

vec3 applyFog(vec3 color, vec3 worldPos) {
    return applyFogScene(hdrFromDisplay(color), worldPos);
}

// applyFogScene is applyFog for a colour already in the scene's units:
// fragment.glsl's lit surfaces, which keep light above 1 in an HDR frame.
// Outside one it is applyFog exactly.
vec3 applyFogScene(vec3 color, vec3 worldPos) {
    // Volumes first, then the distance fog over the top of them. The distance
    // fog fills the WHOLE segment, including the part in front of the nearest
    // volume, so it is the outermost layer; doing it the other way round would
    // let a fog bank tint the haze that is actually between the viewer and it.
    // The atmosphere fills the whole segment too, and goes on between the two.
    color = applyFogVolumes(color, worldPos);
    // The haze is lit by the sky; inside an ambient volume (a cave) it is
    // not, as the walls are not (light_model.glsl's ambientVolumeFactor).
    float amb = ambVolFogPath(worldPos);
    if (fogAtmos != 0) {
        vec3 T;
        vec3 add = fogAtmosAt(worldPos, T);
        color = color * T + add * amb;
    }
    return mix(color, hdrFromDisplay(fogColor) * amb, fogFactor(worldPos));
}

// fogAtmosTransmittance is what survives the atmosphere in front of
// worldPos, for the additive and multiplying blends below.
vec3 fogAtmosTransmittance(vec3 worldPos) {
    vec3 T = vec3(1.0);
    if (fogAtmos != 0) {
        fogAtmosAt(worldPos, T);
    }
    return T;
}

// applyFogAdditive is the same physical answer for a surface drawn with an
// ADDITIVE blend, where the framebuffer already holds the fog behind it. Mixing
// toward fogColor there would add the fog a second time and make a distant
// spark BRIGHTER than a near one; what fog does to something that only ever
// adds light is swallow it, so the contribution is attenuated instead.
vec3 applyFogAdditive(vec3 color, vec3 worldPos) {
    return color * (1.0 - fogFactor(worldPos)) * fogVolumeTransmittance(worldPos)
         * fogAtmosTransmittance(worldPos);
}

// applyFogModulate is the same answer again for a surface drawn with a MULTIPLY
// blend — a decal's scorch mask (gfx.SurfaceBlendModulate).
//
// Such a fragment is not a colour but a FACTOR on what is already in the
// framebuffer, and what is already there is the wall, fogged. Mixing the factor
// toward fogColor would darken a distant wall by a grey mask on top of the haze
// that has already replaced it; what fog does to a mark on a surface is hide it,
// so the factor is faded toward WHITE instead — white being the factor that
// changes nothing.
vec3 applyFogModulate(vec3 color, vec3 worldPos) {
    return mix(vec3(1.0), color,
               (1.0 - fogFactor(worldPos)) * fogVolumeTransmittance(worldPos)
               * fogAtmosTransmittance(worldPos));
}
