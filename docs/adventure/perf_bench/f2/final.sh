#!/bin/bash
S=/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/ec789bcb-0346-4ea6-a32e-b00173193e5a/scratchpad/phf2; W=/mnt/c/Users/jcart/AppData/Local/Temp/foundry-adventure-perf
cd $S; exe=${EXE:-foundry-f5.exe}
for cfg in $@; do
  cp graphics_$cfg.yaml $W/graphics.yaml
  ./run.sh fin${TAG}_${cfg}_static $exe "level=adventure;compiled=0;size=2560x1080;warm=150;frames=120;at=$(cat poses_static.txt)" >/dev/null 2>&1
  for seg in slot cave ruins; do
    TMO=1200 ./run.sh fin${TAG}_${cfg}_fly_$seg $exe "level=adventure;compiled=0;size=2560x1080;warm=120;frames=600;fly=1;slow=50;at=$(cat fly_$seg.txt)" >/dev/null 2>&1
  done
  echo "== $cfg [$(tail -4 gpu.log | tr '\n' ' ')]"
done
cp graphics_userC.yaml $W/graphics.yaml
