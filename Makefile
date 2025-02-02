linux:
	rm -rf dist || true
	mkdir dist
	cp -r resources/* dist
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 go build -v -o dist/foundry *.go

windows:
	rm -rf dist || true
	mkdir dist
	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CXX=x86_64-w64-mingw32-g++ CC=x86_64-w64-mingw32-gcc go build -v -o dist/foundry.exe *.go

sdl:
	rm -rf dist || true
	mkdir dist
	cp resources/SDL2.dll dist/SDL2.dll
	env CGO_ENABLED="1" CC="/usr/bin/x86_64-w64-mingw32-gcc" GOOS="windows" CGO_LDFLAGS="-lmingw32 -lSDL2" CGO_CFLAGS="-D_REENTRANT" go build -o dist/foundry -x *.go 
