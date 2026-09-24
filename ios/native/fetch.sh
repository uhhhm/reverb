#!/usr/bin/env bash
# Fetches the pinned third-party inputs of the phone's embedded download tools
# into ios/build/native, checking each against its recorded SHA-256:
#
#   - CPython for iOS (BeeWare's Python-Apple-support, an XCframework with the
#     standard library);
#   - QuickJS-ng's amalgamated source, yt-dlp's JavaScript runtime;
#   - FFmpeg's release source, also checked against FFmpeg's release signature
#     when gpg is installed.
#
# Python packages are fetched by build.sh, from the hash-pinned requirements.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
out=${REVERB_NATIVE_BUILD:-$here/../build/native}
dl=$out/dl
mkdir -p "$dl"

PYTHON_SUPPORT=3.14-b11
PYTHON_SUPPORT_SHA256=b591f3301bd22a4f423c49c746cac9e55558b909fd14d6eb8327ccc62234ab7b
QUICKJS=v0.17.0
QUICKJS_SHA256=a0955463c74809173a253ff87365095e6972e8b171cfb6aa8a1e781adf35cfeb
FFMPEG=9.0.2
FFMPEG_SHA256=8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e
# FFmpeg release signing key <ffmpeg-devel@ffmpeg.org>.
FFMPEG_KEY=FCF986EA15E6E293A5644F10B4322F04D67658D8

fetch() { # url file sha256
	local url=$1 file=$dl/$2 sum=$3
	if [ ! -f "$file" ] || ! echo "$sum  $file" | shasum -a 256 -c - >/dev/null 2>&1; then
		echo "fetching $url"
		curl -fsSL -o "$file.part" "$url"
		mv "$file.part" "$file"
	fi
	echo "$sum  $file" | shasum -a 256 -c - >/dev/null || {
		echo "checksum mismatch for $file" >&2
		rm -f "$file"
		exit 1
	}
}

fetch "https://github.com/beeware/Python-Apple-support/releases/download/$PYTHON_SUPPORT/Python-3.14-iOS-support.${PYTHON_SUPPORT#*-}.tar.gz" \
	python-ios.tar.gz "$PYTHON_SUPPORT_SHA256"
fetch "https://github.com/quickjs-ng/quickjs/releases/download/$QUICKJS/quickjs-amalgam.zip" \
	quickjs-amalgam.zip "$QUICKJS_SHA256"
fetch "https://ffmpeg.org/releases/ffmpeg-$FFMPEG.tar.xz" "ffmpeg-$FFMPEG.tar.xz" "$FFMPEG_SHA256"

if command -v gpg >/dev/null 2>&1; then
	curl -fsSL -o "$dl/ffmpeg-$FFMPEG.tar.xz.asc" "https://ffmpeg.org/releases/ffmpeg-$FFMPEG.tar.xz.asc"
	gnupg=$(mktemp -d)
	trap 'rm -rf "$gnupg"' EXIT
	curl -fsSL https://ffmpeg.org/ffmpeg-devel.asc | GNUPGHOME=$gnupg gpg --quiet --import 2>/dev/null
	GNUPGHOME=$gnupg gpg --status-fd 1 --verify "$dl/ffmpeg-$FFMPEG.tar.xz.asc" "$dl/ffmpeg-$FFMPEG.tar.xz" 2>/dev/null |
		grep -q "VALIDSIG $FFMPEG_KEY" || {
		echo "FFmpeg's release signature did not verify" >&2
		exit 1
	}
fi

stamp="$PYTHON_SUPPORT_SHA256 $QUICKJS_SHA256 $FFMPEG_SHA256"
if [ "$(cat "$out/.fetched" 2>/dev/null)" = "$stamp" ]; then
	echo "already unpacked in $out"
	exit 0
fi
rm -rf "$out/python" "$out/quickjs" "$out/ffmpeg-$FFMPEG" "$out"/*/ffmpeg
mkdir -p "$out/python" "$out/quickjs"
tar xzf "$dl/python-ios.tar.gz" -C "$out/python"
unzip -q -o "$dl/quickjs-amalgam.zip" -d "$out/quickjs"
tar xJf "$dl/ffmpeg-$FFMPEG.tar.xz" -C "$out"
echo "$FFMPEG" >"$out/ffmpeg.version"
echo "$stamp" >"$out/.fetched"
echo "fetched into $out"
