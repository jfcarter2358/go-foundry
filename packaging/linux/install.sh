#!/bin/sh
# install.sh — install this game on Linux.
#
# Shipped inside the release tarball, beside the executable. Run it from the
# unpacked directory:
#
#   ./install.sh                 # into ~/.local, no root needed
#   PREFIX=/usr/local sudo ./install.sh
#   ./install.sh --uninstall
#
# It is deliberately a shell script and not a package. A .deb (packaging/linux/
# make-deb.sh) is the better answer where dpkg exists, and does not exist
# everywhere; a tarball with an installer in it works on every distribution and
# needs nothing but sh. What it does is what a package would: put the tree
# somewhere out of the way, put a launcher on PATH, and register a desktop
# entry so the game appears in the applications menu.
#
# The engine writes nothing into its install directory in a release build —
# settings, saves and the log go to $XDG_DATA_HOME (or ~/.local/share) — which
# is what lets this install into a root-owned prefix and still be played by an
# ordinary user.

set -eu

here=$(cd "$(dirname "$0")" && pwd)

if [ ! -f "$here/.foundrypack" ]; then
    echo "install.sh: no .foundrypack stamp beside this script — run it from the unpacked release directory." >&2
    exit 1
fi
# TITLE, VERSION, EXE and DATA_DIR, written by foundrypack.
. "$here/.foundrypack"

slug=${EXE%.exe}

# Where this install lives.
#
# The default is ~/.local, but the copy of this script *inside* an install must
# not use it: --uninstall is run from $PREFIX/lib/<slug>/install.sh, in a shell
# that has no idea what PREFIX was when the install happened. Taking the default
# there would have it tidily remove a different install, or — far more likely —
# nothing at all while reporting success, which is what it did before this
# check existed. So a script that finds itself already inside an install tree
# reads the prefix off its own location, and an explicit PREFIX still wins over
# both.
if [ -z "${PREFIX:-}" ]; then
    case "$here" in
        */lib/"$slug") PREFIX=$(dirname "$(dirname "$here")") ;;
        *)             PREFIX=$HOME/.local ;;
    esac
fi

libdir="$PREFIX/lib/$slug"
bindir="$PREFIX/bin"
appdir="$PREFIX/share/applications"
launcher="$bindir/$slug"
desktop="$appdir/$slug.desktop"

uninstall() {
    echo "Removing $TITLE"
    rm -rf "$libdir"
    rm -f "$launcher" "$desktop"
    if command -v update-desktop-database >/dev/null 2>&1; then
        update-desktop-database "$appdir" 2>/dev/null || true
    fi
    echo
    echo "Removed. Your saves and settings are still in:"
    echo "  ${XDG_DATA_HOME:-$HOME/.local/share}/$DATA_DIR"
    echo "Delete that directory too if you want them gone."
}

if [ "${1:-}" = "--uninstall" ] || [ "${1:-}" = "-u" ]; then
    uninstall
    exit 0
fi

echo "Installing $TITLE $VERSION into $PREFIX"

# Replaced wholesale rather than merged: an asset renamed between releases would
# otherwise stay behind for ever, and the game would keep finding it.
rm -rf "$libdir"
mkdir -p "$libdir" "$bindir" "$appdir"

# The whole tree, this script included — it is what --uninstall is run from
# later, and a copy inside the install is the only one guaranteed to still be
# around when the downloaded tarball has been deleted.
tar -cf - -C "$here" . | tar -xf - -C "$libdir"
chmod +x "$libdir/$EXE" "$libdir/install.sh"

# A launcher rather than a symlink: the engine resolves every asset path against
# its executable's real directory, and a symlink on PATH would be resolved to
# the real one on Linux — but exec'ing the real path is what makes that
# guaranteed rather than incidental.
cat > "$launcher" <<LAUNCHER
#!/bin/sh
exec "$libdir/$EXE" "\$@"
LAUNCHER
chmod +x "$launcher"

cat > "$desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=$TITLE
Comment=$TITLE
Exec=$launcher
Path=$libdir
Terminal=false
Categories=Game;
DESKTOP

if command -v update-desktop-database >/dev/null 2>&1; then
    update-desktop-database "$appdir" 2>/dev/null || true
fi

echo
echo "Installed."
echo "  Game:     $libdir"
echo "  Launcher: $launcher"
echo "  Saves:    ${XDG_DATA_HOME:-$HOME/.local/share}/$DATA_DIR"
case ":$PATH:" in
    *":$bindir:"*) echo; echo "Run it with: $slug" ;;
    *) echo; echo "$bindir is not on your PATH. Run it with: $launcher" ;;
esac
echo "Uninstall with: $libdir/install.sh --uninstall"
