#!/bin/bash
# summ.sh name...: per pose p50, gpu frame, material, shafts
W=/mnt/c/Users/jcart/AppData/Local/Temp/foundry-adventure-perf
for n in "$@"; do
  awk -v n=$n '/^=== pose/{p++} /^frame  avg/{p50[p]=$7} /^frame  /&&!/avg/{g[p]=$4; c[p]=$2} /nanite: material/&&!/classify/{m[p]=$5} /^    shafts/{s[p]=$4}
   END{printf "%-18s", n; for(i=1;i<=p;i++) printf " | p50 %5.1f gpu %5.1f cpu %4.1f mat %5.1f sh %3.1f", p50[i], g[i], c[i], m[i], s[i]; printf "\n"}' <(grep -a -v "  texture" $W/out/$n.txt)
done
