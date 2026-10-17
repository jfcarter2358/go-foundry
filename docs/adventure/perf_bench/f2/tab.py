import sys, re
def parse(path):
    poses=[]; cur=None
    for line in open(path, encoding='utf-8', errors='replace'):
        if line.startswith('=== pose'):
            cur={}; poses.append(cur); continue
        if cur is None: continue
        m=re.match(r'frame  avg ([\d.]+) ms .*p50 ([\d.]+)\s+p95 ([\d.]+)\s+p99 ([\d.]+)\s+max ([\d.]+)', line)
        if m: cur.update(avg=float(m[1]), p50=float(m[2]), p99=float(m[4]), mx=float(m[5])); continue
        m=re.match(r'^(\s*)(\S.*?)\s{2,}([\d.]+)\s+([\d.]+)\s+([\d.]+)\s+([\d.]+)\s*$', line)
        if m and not line.startswith('  texture'):
            name=m[2].strip(); d=len(m[1])//2
            key=(d,name)
            if key not in cur: cur[key]=(float(m[3]), float(m[4]), float(m[5]), float(m[6]))
    return poses
for path in sys.argv[1:]:
    ps=parse(path)
    print(path.split('/')[-1])
    for i,p in enumerate(ps):
        g=p.get((0,'frame'),(0,0,0,0)); mat=p.get((1,'nanite: material'),(0,0,0,0)); sh=p.get((2,'shafts'),(0,0,0,0))
        print("  %d p50 %.1f p99 %.1f | gpu %.1f cpu %.1f | mat %.1f shafts %.1f" % (i+1, p.get('p50',0), p.get('p99',0), g[2], g[0], mat[2], sh[2]))
