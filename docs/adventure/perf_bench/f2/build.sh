#!/bin/bash
# build.sh N  -> foundry-fN.exe + fresh shader snapshot in the bench copy
W=/mnt/c/Users/jcart/AppData/Local/Temp/foundry-adventure-perf; S=/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/ec789bcb-0346-4ea6-a32e-b00173193e5a/scratchpad/phf2
cd /home/jfcarter2358/personal/code/main/foundry
GOOS=windows CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc CXX=x86_64-w64-mingw32-g++ GOFLAGS=-p=6 timeout 1500 go build -ldflags '-extldflags "-static-libgcc -static-libstdc++"' -o $W/foundry-f$1.exe ./cmd/foundry 2>&1 | grep -E "\.go:[0-9]" 
rm -rf $W/resources/shaders; cp -r resources/shaders $W/resources/shaders
rm -rf $S/shaders_snap; cp -r resources/shaders $S/shaders_snap
ls -la $W/foundry-f$1.exe
