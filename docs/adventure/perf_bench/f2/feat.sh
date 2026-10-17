#!/bin/bash
# feat.sh EXE name "yaml lines" ...  : run p158 with extra graphics.yaml lines
S=/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/ec789bcb-0346-4ea6-a32e-b00173193e5a/scratchpad/phf2; W=/mnt/c/Users/jcart/AppData/Local/Temp/foundry-adventure-perf
cd $S; exe=$1; shift
while [ $# -ge 2 ]; do
  n=$1; extra=$2; shift 2
  { cat graphics_userC.yaml; printf "$extra"; } > $W/graphics.yaml
  TMO=900 ./run.sh $n $exe "level=adventure;compiled=0;size=2560x1080;warm=150;frames=90;at=$(cat ${POSES:-p158.txt})" >/dev/null 2>&1
  echo "== $n [$(tail -1 gpu.log)] $(echo "$extra" | tr '\n' ' ')"; python3 tab.py $W/out/$n.txt | tail -n +2
done
cp graphics_userC.yaml $W/graphics.yaml
