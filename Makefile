linux:
	rm -rf dist || true
	mkdir dist
	cp -r resources dist/resources
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 go build -v -x -o dist/foundry ./cmd/foundry
	$(MAKE) runtime-linux RUNTIMES_DIR=dist/runtimes

windows:
# 	cp -r resources/* /mnt/c/Users/jcart/OneDrive/Documents/Foundry
# 	cp resources/SDL2.dll /mnt/c/Users/jcart/OneDrive/Documents/Foundry/SDL2.dll
# 	cp foundrypack /mnt/c/Users/jcart/OneDrive/Documents/Foundry/foundrypack
	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CXX=x86_64-w64-mingw32-g++ CC=x86_64-w64-mingw32-gcc go build -x -v -o /mnt/c/Users/jcart/OneDrive/Documents/Foundry/foundry.exe ./cmd/foundry
	# The shaders go with the binary: the engine binds uniforms and attribute
	# locations they declare, so an exe newer than its shaders renders wrong
	# (dynamic lights, explosions). Only the shaders — the rest of resources/
	# is the game project's own content, which the copy above would clobber.
	cp resources/shaders/* /mnt/c/Users/jcart/OneDrive/Documents/Foundry/resources/shaders/
	$(MAKE) runtime-windows RUNTIMES_DIR=/mnt/c/Users/jcart/OneDrive/Documents/Foundry/runtimes

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

models:
	go run ./cmd/genmodels

# ============================================================================
# Release
# ============================================================================
#
# A release build is the engine with the editor compiled out (`-tags release`,
# see internal/app/devtools_release.go), staged into an install tree beside the
# content it can actually reach, and wrapped in something a player can run.
#
#   make release-linux        build + stage + tar.gz
#   make release-windows      cross-build + stage
#   make installer-linux      the above, plus a .deb
#   make installer-windows    the above, plus a Setup.exe (needs makensis)
#   make compile-levels       build every level's .fbsp (part of the release targets)
#   make release-check        prove the editor is not in the binary
#   make runtimes             the game binaries an editor's Release window packs
#   make dist-editor          the editor itself, for a Foundry release on GitHub
#
# PRUNE=0 stages the whole content tree instead of only what the game reaches
# from its manifest. Worth reaching for when a script names a level or a texture
# by a string it assembles at runtime, which is the one thing the reference walk
# in internal/pack cannot see.
#
# These targets build the engine's own resources/. A game kept in a project
# directory of its own is built from the editor running there — its Release
# window, or `./foundry build-release` — see docs/guide/20-shipping.md.

BUILD_DIR    ?= build
PRUNE        ?= 1
RELEASE_TAGS := release

# -s -w drop the symbol table and DWARF: about a third of the binary, and
# nothing a player's machine has any use for.
RELEASE_LDFLAGS := -s -w
# -H windowsgui suppresses the console window that would otherwise open behind
# the game. The engine logs to a file for exactly this reason (app.go).
WINDOWS_LDFLAGS := $(RELEASE_LDFLAGS) -H windowsgui

ifeq ($(PRUNE),1)
PRUNE_FLAG := -prune
else
PRUNE_FLAG :=
endif

LINUX_STAGE   := $(BUILD_DIR)/install-linux
WINDOWS_STAGE := $(BUILD_DIR)/install-windows

.PHONY: release-linux release-windows installer-linux installer-windows \
        release-check test-release clean-release compile-levels \
        runtimes runtime-linux runtime-windows \
        dist-editor dist-editor-windows dist-editor-linux editor-runtimes

## The release game binaries an editor packs a project's content around.
##
## An editor running from a game's own project directory has no Foundry source
## beside it to build from, so its Release window looks for them in runtimes/
## next to itself (internal/release/player.go). This is what puts them there:
##
##   make runtimes RUNTIMES_DIR=/path/to/my-game/runtimes
##
## Rebuild them with the editor beside them: a game binary refuses levels
## compiled by a different version of the compiler, and the editor compiles
## them. `make linux` and `make windows` each build their own platform's.
RUNTIMES_DIR ?= dist/runtimes

runtimes: runtime-linux runtime-windows

runtime-linux:
	@mkdir -p $(RUNTIMES_DIR)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 \
	  go build -tags $(RELEASE_TAGS) -ldflags "$(RELEASE_LDFLAGS)" -o $(RUNTIMES_DIR)/foundry-release-linux-amd64 ./cmd/foundry

runtime-windows:
	@mkdir -p $(RUNTIMES_DIR)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 \
	  CXX=x86_64-w64-mingw32-g++ CC=x86_64-w64-mingw32-gcc \
	  go build -tags $(RELEASE_TAGS) -ldflags "$(WINDOWS_LDFLAGS)" -o $(RUNTIMES_DIR)/foundry-release-windows-amd64.exe ./cmd/foundry
	# The installer every Windows release is appended to (internal/setup). Pure
	# Go, so no mingw.
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
	  go build -ldflags "$(WINDOWS_LDFLAGS)" -o $(RUNTIMES_DIR)/foundry-setup-windows-amd64.exe ./cmd/foundrysetup

# ============================================================================
# The editor, for a Foundry release
# ============================================================================
#
# What goes on a GitHub release: the editor itself, for everybody who works on
# a game made with it. It holds no project — it opens a project folder from
# wherever its author keeps one (File > Open Project, or `foundry -project
# <dir>`; see internal/project) — so it installs once and upgrades whole:
#
#   foundry-editor-<version>-windows-amd64-setup.exe   Setup.exe, per user, no admin
#   foundry-editor-<version>-windows-amd64.zip         the same tree, portable
#   foundry-editor-<version>-linux-amd64.tar.gz        needs libsdl2 on the system
#
# Each carries the shaders it was built from (which it reads instead of any
# project's) and runtimes/ — the release game binaries for both platforms and
# the Windows installer program — so its Release window can ship a game with
# nothing else installed. Then:
#
#   make dist-editor
#   gh release create v$(EDITOR_VERSION) $(EDITOR_DIST)/*

EDITOR_VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//')
EDITOR_DIST     ?= $(BUILD_DIR)/editor-dist
EDITOR_RUNTIMES := $(BUILD_DIR)/editor-runtimes
EDITOR_PACK     := go run ./cmd/foundrypack editor -version $(EDITOR_VERSION) -out $(EDITOR_DIST) \
                     -shaders resources/shaders \
                     -runtime $(EDITOR_RUNTIMES)/foundry-release-windows-amd64.exe \
                     -runtime $(EDITOR_RUNTIMES)/foundry-release-linux-amd64 \
                     -runtime $(EDITOR_RUNTIMES)/foundry-setup-windows-amd64.exe

dist-editor: dist-editor-windows dist-editor-linux
	@echo "dist-editor: $(EDITOR_DIST)"; ls -1 $(EDITOR_DIST)

editor-runtimes:
	$(MAKE) runtimes RUNTIMES_DIR=$(EDITOR_RUNTIMES)

## The Windows editor: a console program, as `make windows` builds it — the
## console is where a crash says what happened, and the git and go processes
## the editor starts would each flash one up under a windowsgui parent.
dist-editor-windows: editor-runtimes
	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 \
	  CXX=x86_64-w64-mingw32-g++ CC=x86_64-w64-mingw32-gcc \
	  go build -o $(BUILD_DIR)/foundry-editor-windows.exe ./cmd/foundry
	$(EDITOR_PACK) -goos windows -exe $(BUILD_DIR)/foundry-editor-windows.exe \
	  -lib resources/SDL2.dll -setup-stub $(EDITOR_RUNTIMES)/foundry-setup-windows-amd64.exe \
	  -stage $(BUILD_DIR)/editor-windows

dist-editor-linux: editor-runtimes
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 go build -o $(BUILD_DIR)/foundry-editor-linux ./cmd/foundry
	$(EDITOR_PACK) -goos linux -exe $(BUILD_DIR)/foundry-editor-linux -stage $(BUILD_DIR)/editor-linux

## Compile every level to the form a release build loads. See
## docs/guide/23-compiling-levels.md, and internal/app/levelsource_release.go
## for why a shipped game reads nothing else.
compile-levels:
	go run ./cmd/foundrycompile -strict

## Build the release binary and stage a Linux install tree, then tar it.
release-linux: compile-levels
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 \
	  go build -tags $(RELEASE_TAGS) -ldflags "$(RELEASE_LDFLAGS)" -o $(BUILD_DIR)/foundry-release ./cmd/foundry
	go run ./cmd/foundrypack -goos linux -exe $(BUILD_DIR)/foundry-release -out $(LINUX_STAGE) $(PRUNE_FLAG)
	cp packaging/linux/install.sh $(LINUX_STAGE)/install.sh
	@. $(LINUX_STAGE)/.foundrypack; \
	  name=$$(basename $$EXE); \
	  tar -czf $(BUILD_DIR)/$$name-$$VERSION-linux-amd64.tar.gz \
	      --transform "s,^\.,$$name-$$VERSION," -C $(LINUX_STAGE) . ; \
	  echo "release-linux: $(BUILD_DIR)/$$name-$$VERSION-linux-amd64.tar.gz"

## Cross-build the release binary for Windows and stage an install tree.
release-windows: compile-levels
	@mkdir -p $(BUILD_DIR)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 \
	  CXX=x86_64-w64-mingw32-g++ CC=x86_64-w64-mingw32-gcc \
	  go build -tags $(RELEASE_TAGS) -ldflags "$(WINDOWS_LDFLAGS)" -o $(BUILD_DIR)/foundry-release.exe ./cmd/foundry
	go run ./cmd/foundrypack -goos windows -exe $(BUILD_DIR)/foundry-release.exe -out $(WINDOWS_STAGE) $(PRUNE_FLAG)
	@echo "release-windows: staged in $(WINDOWS_STAGE)"

## Build a .deb from the staged Linux tree.
installer-linux: release-linux
	@. $(LINUX_STAGE)/.foundrypack; \
	  packaging/linux/make-deb.sh $(LINUX_STAGE) $(BUILD_DIR)/$$(basename $$EXE)-$$VERSION-amd64.deb

## Build a Windows Setup.exe from the staged Windows tree.
##
## NSIS is not a Go dependency and is not installed by default: `apt install
## nsis` (it cross-packages from Linux, which is the whole reason it is NSIS and
## not Inno Setup).
installer-windows: release-windows
	@command -v makensis >/dev/null 2>&1 || { \
	  echo "installer-windows: makensis not found. Install it with:  sudo apt install nsis"; exit 1; }
	@. $(WINDOWS_STAGE)/.foundrypack; \
	  out=$(BUILD_DIR)/$$(basename $${EXE%.exe})-$$VERSION-setup.exe; \
	  makensis -V2 \
	    -DAPPNAME="$$TITLE" -DVERSION="$$VERSION" -DEXENAME="$$EXE" \
	    -DDATADIR="$$DATA_DIR" -DSRCDIR="$(CURDIR)/$(WINDOWS_STAGE)" \
	    -DOUTFILE="$(CURDIR)/$$out" \
	    packaging/windows/installer.nsi && echo "installer-windows: $$out"

## Prove the release binary contains no editor, no imgui and no sub-editors.
##
## The seam in internal/app is a pair of files that have to agree, and nothing
## in the type system makes them: the check that the release half is the one
## being compiled is that none of these packages is reachable from the release
## build's import graph. Cheap enough to run in CI on every change.
release-check:
	@echo "release build's forbidden imports:"
	@if go list -deps -tags $(RELEASE_TAGS) ./cmd/foundry \
	     | grep -E 'internal/(editor|textureui|imguibackend|uiwin|release|pack)$$|imgui-go'; then \
	  echo "FAIL: a development-only package is linked into the release build"; exit 1; \
	else \
	  echo "  none — editor, textureui, imguibackend, uiwin, release, pack and imgui are all absent"; \
	fi

## Run the tests over the release build's own configuration.
test-release:
	go test -tags $(RELEASE_TAGS) $$(go list -deps -tags $(RELEASE_TAGS) ./cmd/foundry | grep foundry/) ./internal/pack

clean-release:
	rm -rf $(BUILD_DIR)
