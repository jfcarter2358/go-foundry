#!/bin/bash
cd /tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/ec789bcb-0346-4ea6-a32e-b00173193e5a/scratchpad/phf2
for h in "$@"; do
  EXE=foundry-f1.exe POSES=p1.txt ./hackrun.sh hk_$h $h FOUNDRY_NANITE_MAT_PROFILE=1
  ./keys.sh hk_$h
done
