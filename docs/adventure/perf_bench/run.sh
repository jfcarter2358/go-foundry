#!/bin/bash
S=/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/ec789bcb-0346-4ea6-a32e-b00173193e5a/scratchpad/phf/bench
# run.sh NAME EXE 'bench spec (no out=)' [ENV=VAL ...]
W=/mnt/c/Users/jcart/AppData/Local/Temp/foundry-adventure-perf
WW='C:\Users\jcart\AppData\Local\Temp\foundry-adventure-perf'
n=$1; exe=$2; spec=$3; shift 3
mkdir -p $W/out
b=$W/run_$n.bat
{
  echo '@echo off'
  echo "cd /d $WW"
  for kv in "$@"; do echo "set \"$kv\""; done
  echo "set \"FOUNDRY_BENCH=$spec;out=$WW\\out\\$n.txt;shots=$WW\\out\\${n}_shots\""
  echo "$exe > out\\$n.log 2>&1"
} | sed 's/$/\r/' > $b
echo "pre: $(nvidia-smi.exe --query-gpu=utilization.gpu,memory.used --format=csv,noheader)" >> $S/gpu.log 2>/dev/null; cd $W && timeout ${TMO:-1200} cmd.exe /c run_$n.bat
ls -la $W/out/$n.txt 2>/dev/null && grep -a -E "^=== pose|^frame|GPU frame|^frame +[0-9]|^  *gpu|^(frame|cpu)" $W/out/$n.txt | head -40
