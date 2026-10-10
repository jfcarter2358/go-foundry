#!/usr/bin/env python3
"""make_composite.py LEVELS_DIR ENGINE_LEVELS_DIR — writes phase4_composite.yaml,
the Phase 4 composite test level (docs/nanite/PHASE_4.md), into LEVELS_DIR.

Built from a game project's nanite_bench.yaml and level1.yaml (both in
LEVELS_DIR) and the engine repository's zz_foliage.yaml (ENGINE_LEVELS_DIR):
the bench's wall, 15 cooked rocks in front of it, a classic dropship prop, a
translucent glass brush, the foliage level's terrain sunk 16 m (grass and
foliage on it), a scripted marine (skinned), a fire emitter, a cloud volume,
two decals, level1's sky and player (the viewmodel), and the bench's lights
(a shadowing dynamic sun). The camera pose the captures use is
`teleport 0,2,7; look -90 -8`."""
import re, sys

lv, eng = sys.argv[1], sys.argv[2]
nb = open(f'{lv}/nanite_bench.yaml').read()
l1 = open(f'{lv}/level1.yaml').read().split('\n')
fol = open(f'{eng}/zz_foliage.yaml').read()

chunks = nb[:nb.index('\nspawn:')]
# The bench's floor goes: the terrain is the ground.
chunks = chunks[:chunks.index('  - id: floor')] + chunks[chunks.index('  - id: wall'):]
fc = fol[fol.index('chunks:\n') + len('chunks:\n'):]
m = re.search(r'\n[a-z_]+:', fc)
terrain = (fc[:m.start()] if m else fc).replace('- id: ground', '- id: terrain', 1)
zero = '  origin:\n    x: 0\n    "y": 0\n    z: 0'
i = terrain.index(zero)
terrain = terrain[:i] + '  origin:\n    x: 0\n    "y": -16\n    z: 0' + terrain[i + len(zero):]

rocks, i = [], 0
for z in [-2.0, -4.0, -6.0]:
    for x in [-5, -2.5, 0, 2.5, 5]:
        rocks.append(f"""- id: prock{i}
  model: models/bench_rock.fnm
  position:
    x: {x}
    "y": 0.6
    z: {z}
  rotation:
    x: 0
    "y": {0.7*i:.2f}
    z: 0
  scale:
    x: 1.1
    "y": 1.1
    z: 1.1
  materials:
    moss: cn_rock
    stone: cn_rock""")
        i += 1
rocks.append("""- id: ship_prop
  model: models/dropship.fmdl
  position:
    x: 7
    "y": 1.5
    z: -3
  rotation:
    x: 0
    "y": 0.8
    z: 0
  scale:
    x: 0.6
    "y": 0.6
    z: 0.6""")

glass = "  - id: glass\n    verts:\n"
for v in [(-1.5, 0, 1.5), (1.5, 0, 1.5), (-1.5, 3, 1.5), (1.5, 3, 1.5),
          (-1.5, 0, 1.4), (1.5, 0, 1.4), (-1.5, 3, 1.4), (1.5, 3, 1.4)]:
    glass += "".join(f"    - {c}\n" for c in v)
glass += "    faces:\n"
for idx in [[0, 4, 6, 2], [5, 1, 3, 7], [0, 1, 5, 4], [2, 6, 7, 3], [1, 0, 2, 3], [4, 5, 7, 6]]:
    glass += "    - indices:\n" + "".join(f"      - {k}\n" for k in idx) + "      texture: concrete\n      alpha: 0.4\n"
j = chunks.index('  origin:')
chunks = chunks[:j] + glass + chunks[j:]

def section(name):
    start = l1.index(name + ':')
    out = [l1[start]]
    for line in l1[start + 1:]:
        if line and not line.startswith(' ') and not line.startswith('-'):
            break
        out.append(line)
    return '\n'.join(out)

rest = """spawn:
  position:
    x: 0
    "y": 1
    z: 7
  rotation:
    x: 0
    "y": 0
    z: 0
scenery:
""" + "\n".join(rocks) + """
entities:
- type: friendly_rifle
  id: squad_1
  position:
    x: -1.6
    "y": 0
    z: 2.5
  rotation:
    x: 0
    "y": 0.4
    z: 0
  models:
  - name: body
    texture: marine_ally
emitters:
- id: test_fire
  effect: fire
  position:
    x: 2.2
    "y": 0.2
    z: 1.8
volumes:
- id: test_cloud
  shape: box
  position:
    x: 0
    "y": 6
    z: -4
  half:
    x: 6
    "y": 2
    z: 4
  cloud:
    color:
      r: 1
      g: 1
      b: 1
    coverage: 0.55
    density: 0.5
    scale: 6
    detail: 3
    erosion: 0.3
    absorption: 1.2
    ambient: 1.4
    softness: 0.35
decals:
- id: wall_mark
  position:
    x: 0
    "y": 2.5
    z: -7.45
  rotation:
    x: 0
    "y": 0
    z: 0
  texture: cn_rock
  width: 3
  height: 2
- id: floor_scorch
  position:
    x: 1
    "y": 0.02
    z: 3
  rotation:
    x: -1.5708
    "y": 0
    z: 0
  texture: cn_rock
  blend: multiply
  width: 2
  height: 2
  depth: 0.4
"""
out = chunks + "\n" + terrain.rstrip('\n') + "\n" + rest + section('sky') + "\n" + section('player') + "\n" + nb[nb.index('\nlights:') + 1:]
open(f'{lv}/phase4_composite.yaml', 'w').write(out)
