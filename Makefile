linux:
	rm -rf dist || true
	mkdir dist
	cp -r resources/* dist
	GOOS=linux GOARCH=amd64 CGO_ENABLED=1 go build -v -x -o dist/foundry ./cmd/foundry

windows:
	cp -r resources/* /mnt/c/Users/jcart/OneDrive/Documents/Foundry
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
#   make release-check        prove the editor is not in the binary
#
# PRUNE=0 stages the whole content tree instead of only what the game reaches
# from its manifest. Worth reaching for when a script names a level or a texture
# by a string it assembles at runtime, which is the one thing the reference walk
# in cmd/foundrypack cannot see.

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
        release-check test-release clean-release

## Build the release binary and stage a Linux install tree, then tar it.
release-linux:
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
release-windows:
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
	     | grep -E 'internal/(editor|textureui|imguibackend|uiwin)$$|imgui-go'; then \
	  echo "FAIL: a development-only package is linked into the release build"; exit 1; \
	else \
	  echo "  none — editor, textureui, imguibackend, uiwin and imgui are all absent"; \
	fi

## Run the tests over the release build's own configuration.
test-release:
	go test -tags $(RELEASE_TAGS) $$(go list -deps -tags $(RELEASE_TAGS) ./cmd/foundry | grep foundry/) ./cmd/foundrypack

clean-release:
	rm -rf $(BUILD_DIR)
