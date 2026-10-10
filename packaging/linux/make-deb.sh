#!/bin/sh
# make-deb.sh — build a .deb from a staged install tree.
#
#   packaging/linux/make-deb.sh build/install-linux build/MyGame.deb
#
# The tree is the one foundrypack produced; its .foundrypack stamp supplies the
# package name, version and executable, so nothing about a particular game is
# written down here.
#
# The layout is the ordinary one for a self-contained game: the tree under
# /opt/<slug>, a launcher on PATH, a desktop entry. /opt rather than
# /usr/share/games because the tree is not architecture-independent data — the
# executable lives in it, and the engine finds every asset relative to that
# executable, so the two cannot be separated the way a policy-pure package would
# want them to be.
#
# Built with dpkg-deb --root-owner-group, so the files inside the archive are
# owned by root without this script needing to be run as root.

set -eu

src=${1:?usage: make-deb.sh <staged-install-dir> <output.deb>}
out=${2:?usage: make-deb.sh <staged-install-dir> <output.deb>}

[ -f "$src/.foundrypack" ] || { echo "make-deb.sh: $src has no .foundrypack stamp — is it a foundrypack output?" >&2; exit 1; }
. "$src/.foundrypack"

command -v dpkg-deb >/dev/null 2>&1 || { echo "make-deb.sh: dpkg-deb not found (apt install dpkg-dev)" >&2; exit 1; }

slug=${EXE%.exe}

# Debian versions must begin with a digit. A project versioning its releases
# "v1.2" or "alpha" is not wrong, it just is not a Debian version — prefix it
# rather than refusing to build.
version=$VERSION
case "$version" in
    [0-9]*) ;;
    "")     version=0.0.0 ;;
    *)      version=0.0.0~$(echo "$version" | tr -c 'A-Za-z0-9.+~' '-') ;;
esac

arch=$(dpkg --print-architecture)
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT INT TERM

# mktemp -d gives 0700, which would become the mode of the package's own root
# directory and show up in every listing of it.
chmod 755 "$stage"
mkdir -p "$stage/opt/$slug" "$stage/usr/bin" "$stage/usr/share/applications" "$stage/DEBIAN"

# install.sh is the tarball's installer; a .deb has already been installed by
# the time anything in it is on disk, and shipping a second installer inside one
# is only ever confusing.
tar -cf - -C "$src" --exclude=./install.sh . | tar -xf - -C "$stage/opt/$slug"
chmod +x "$stage/opt/$slug/$EXE"

cat > "$stage/usr/bin/$slug" <<LAUNCHER
#!/bin/sh
exec "/opt/$slug/$EXE" "\$@"
LAUNCHER
chmod +x "$stage/usr/bin/$slug"

cat > "$stage/usr/share/applications/$slug.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=$TITLE
Comment=$TITLE
Exec=/usr/bin/$slug
Path=/opt/$slug
Terminal=false
Categories=Game;
DESKTOP

# Installed-Size is in KiB and is what apt reports before downloading.
size=$(du -ks "$stage" | cut -f1)

# The dependencies are what the engine links against and does not ship: SDL2 for
# the window, the input and the audio device, and libGL for the context. Both
# are dynamically linked (CGO_ENABLED=1 with the system SDL2), which is why they
# are named here rather than being bundled — a distribution's own SDL2 is the
# one that knows about its display server.
cat > "$stage/DEBIAN/control" <<CONTROL
Package: $slug
Version: $version
Section: games
Priority: optional
Architecture: $arch
Depends: libsdl2-2.0-0 (>= 2.0.10), libgl1
Installed-Size: $size
Maintainer: $TITLE <root@localhost>
Description: $TITLE
 $TITLE, built with the Foundry engine.
CONTROL

mkdir -p "$(dirname "$out")"
dpkg-deb --build --root-owner-group "$stage" "$out" >/dev/null
echo "make-deb.sh: wrote $out ($(du -h "$out" | cut -f1))"
