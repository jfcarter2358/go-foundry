#!/bin/sh
# fetch-windows.sh — the OGG Vorbis and MP3 encoders the Windows editor
# carries in its codecs/ folder (internal/score/io/audioenc loads them at
# run time; nothing links against them). `task codecs-windows` runs this.
#
#   packaging/codecs/fetch-windows.sh [dir]     (default build/codecs/windows-amd64)
#
# The DLLs are MSYS2's mingw64 builds, pinned below: libogg, libvorbis and
# libvorbisenc (BSD-3-Clause, Xiph.Org) and LAME's libmp3lame (LGPL 2).
# They import nothing but each other, kernel32 and msvcrt, which
# `foundrypack editor -codecs` checks. Their licences come from upstream,
# and README.txt (written by the packer) says where each library's source
# is and that the LGPL'd one may be replaced by the user.
set -eu

dir=${1:-build/codecs/windows-amd64}
repo=https://repo.msys2.org/mingw/mingw64
pkgs="mingw-w64-x86_64-libogg-1.3.6-1-any mingw-w64-x86_64-libvorbis-1.3.7-3-any mingw-w64-x86_64-lame-3.100-3-any"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$dir"
for p in $pkgs; do
	curl -sSfL -o "$tmp/$p.pkg.tar.zst" "$repo/$p.pkg.tar.zst"
	mkdir -p "$tmp/$p"
	tar --zstd -xf "$tmp/$p.pkg.tar.zst" -C "$tmp/$p"
done
for dll in libogg-0.dll libvorbis-0.dll libvorbisenc-2.dll libmp3lame-0.dll; do
	found=$(find "$tmp" -name "$dll" | head -n 1)
	if [ -z "$found" ]; then
		echo "fetch-windows: $dll is not in the packages" >&2
		exit 1
	fi
	cp "$found" "$dir/$dll"
done
curl -sSfL -o "$dir/libogg-COPYING.txt" https://raw.githubusercontent.com/xiph/ogg/v1.3.6/COPYING
curl -sSfL -o "$dir/libvorbis-COPYING.txt" https://raw.githubusercontent.com/xiph/vorbis/v1.3.7/COPYING
curl -sSfL -o "$dir/LAME-COPYING.txt" "https://sourceforge.net/p/lame/svn/HEAD/tree/trunk/lame/COPYING?format=raw"
echo "fetch-windows: $(ls "$dir" | wc -l) files in $dir"
