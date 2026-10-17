import math, re, sys
def eye_for(P, T, boom=4.2, h=1.7):
    dx, dz = T[0]-P[0], T[2]-P[2]; L = math.hypot(dx, dz) or 1
    E = (P[0]-dx/L*boom, P[1]+h, P[2]-dz/L*boom)
    v = (T[0]-E[0], T[1]-E[1], T[2]-E[2])
    yaw = math.atan2(v[2], v[0]); pitch = math.atan2(v[1], math.hypot(v[0], v[2]))
    return E, yaw, pitch
def pose(P, T, boom=4.2):
    E, y, p = eye_for(P, T, boom)
    return "%.2f,%.2f,%.2f,%.3f,%.3f@%.2f,%.2f,%.2f" % (E+(y,p)+tuple(P))
static = [
 ("SPAWN",    (-335,14.4,131), (-300,19,131)),
 ("SLOTSHAFT",(-129.5,18.4,114.2), (-112,19,122)),
 ("MIDSLOT",  (-228.4,15.6,134.5), (-160,22,122)),
 ("SKYLIGHT", (-96,21.2,127.1), (-78,30,120), 3.0),
 ("ROCKPILE", (-31,37.3,97.3), (-7.9,41,83.3), 3.0),
 ("MOUTH",    (67.6,93.5,41.4), (100,95,37.5), 3.0),
 ("LEDGE",    (131.2,94.3,51.3), (240,30,36), 2.0),
 ("STAIRS",   (153,83.9,40.5), (220,45,40)),
 ("GOAL",     (306,11.2,30.5), (314.2,12,34.8)),
]
if sys.argv[1] == "static":
    print("|".join(pose(*s[1:3], *(s[3:] or [4.2])) for s in static))
    print(" ".join(s[0] for s in static), file=sys.stderr)
else:
    # fly: waypoints from the walk's `to` legs, every k-th, eye behind along motion
    pts = [tuple(map(float, m.split(","))) for m in re.findall(r"^to ([-\d.]+,[-\d.]+,[-\d.]+)", open(sys.argv[2]).read(), re.M)]
    a, b, k = int(sys.argv[3]), int(sys.argv[4]), int(sys.argv[5])
    seg = pts[a:b]
    out = []
    for i in range(0, len(seg), k):
        P = seg[i]; N = seg[min(i+k, len(seg)-1)] if i+k < len(seg) else seg[-1]
        if N == P: N = (P[0]+(P[0]-seg[i-k][0]), P[1], P[2]+(P[2]-seg[i-k][2]))
        T = (N[0]+(N[0]-P[0])*3, N[1]+1.0, N[2]+(N[2]-P[2])*3)
        out.append(pose(P, T, float(sys.argv[6]) if len(sys.argv) > 6 else 4.2))
    print("|".join(out)); print(len(pts), len(out), file=sys.stderr)
