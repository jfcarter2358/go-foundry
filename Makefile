linux:
	rm -rf dist || true
	mkdir dist
	cp -r resources/* dist
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 go build -v -x -o dist/foundry ./cmd/foundry

windows:
	rm -rf /mnt/c/Users/jcart/OneDrive/Documents/Foundry/resources
	cp -r resources/shaders /mnt/c/Users/jcart/OneDrive/Documents/Foundry
	cp -r resources/textures /mnt/c/Users/jcart/OneDrive/Documents/Foundry
	cp resources/SDL2.dll /mnt/c/Users/jcart/OneDrive/Documents/Foundry/SDL2.dll
	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CXX=x86_64-w64-mingw32-g++ CC=x86_64-w64-mingw32-gcc go build -x -v -o /mnt/c/Users/jcart/OneDrive/Documents/Foundry/foundry.exe ./cmd/foundry

sdl:
	rm -rf dist || true
	mkdir dist
	cp resources/SDL2.dll dist/SDL2.dll
	env CGO_ENABLED="1" CC="/usr/bin/x86_64-w64-mingw32-gcc" GOOS="windows" CGO_LDFLAGS="-lmingw32 -lSDL2" CGO_CFLAGS="-D_REENTRANT" go build -v -o dist/foundry -x ./cmd/foundry

run-linux:
	./dist/foundry

run-windows:
	./dist/foundry.exe

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l $(shell git ls-files '*.go')

textures:
	go run ./cmd/gentextures
