#!/usr/bin/env bash
# Builds libreverbnative.a, the native half of the phone's embedded download
# tools, for each platform named (macos, iphoneos, iphonesimulator; all three
# by default), and installs the bundled Python packages (requirements.txt)
# into ios/build/native/app_packages. The library holds FFmpeg's libraries, the ffmpeg and ffprobe programs as
# in-process functions (src/fftools_shim.c), QuickJS-ng with a qjs stand-in
# (src/qjs_run.c), and the _reverb_native Python module that reaches them.
#
# macos is for tests: the embedded Python runner's Go tests link it against
# the host's Python. The iOS slices become ios/Frameworks/ReverbNative.xcframework.
#
# Run fetch.sh first. Needs Xcode's command-line tools; the macos slice also
# needs python3.14 (Homebrew's is fine).
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
out=${REVERB_NATIVE_BUILD:-$here/../build/native}
out=$(cd "$out" && pwd)
ffmpeg_version=$(cat "$out/ffmpeg.version")
ffsrc=$out/ffmpeg-$ffmpeg_version
jobs=$(sysctl -n hw.ncpu)

platforms=("$@")
[ ${#platforms[@]} -gt 0 ] || platforms=(macos iphoneos iphonesimulator)

# What yt-dlp and spotDL ask of ffmpeg when every download keeps the source's
# own stream: probing, remuxing (m4a, Ogg Opus, MP3), tagging, and converting
# thumbnails to PNG or JPEG for embedding. There are no audio encoders.
ffmpeg_components=(
	--enable-protocol=file,pipe
	--enable-demuxer=mov,matroska,ogg,mp3,aac,flac,wav,ffmetadata,image2,image_webp_pipe,image_png_pipe,image_jpeg_pipe
	--enable-muxer=mp4,ipod,mov,matroska,webm,ogg,opus,mp3,adts,flac,wav,ffmetadata,image2,null
	--enable-decoder=aac,opus,vorbis,mp3,mp3float,flac,alac,pcm_s16le,png,mjpeg,webp,vp8
	--enable-encoder=png,mjpeg
	--enable-parser=aac,opus,vorbis,mpegaudio,flac,png,mjpeg,webp,vp8
	--enable-bsf=aac_adtstoasc,null
	--enable-filter=anull,null,acopy,copy,aresample,aformat,format,scale,crop
)

setup() { # platform
	case $1 in
	macos)
		sdk=macosx
		target=arm64-apple-macos13.0
		pyinc=$(python3.14-config --includes)
		;;
	iphoneos)
		sdk=iphoneos
		target=arm64-apple-ios17.0
		pyinc=-I$out/python/Python.xcframework/ios-arm64/Python.framework/Headers
		;;
	iphonesimulator)
		sdk=iphonesimulator
		target=arm64-apple-ios17.0-simulator
		pyinc=-I$out/python/Python.xcframework/ios-arm64_x86_64-simulator/Python.framework/Headers
		;;
	*)
		echo "unknown platform $1" >&2
		exit 2
		;;
	esac
	sysroot=$(xcrun --sdk "$sdk" --show-sdk-path)
	cc="$(xcrun --sdk "$sdk" -f clang) -target $target -isysroot $sysroot"
}

build_ffmpeg() { # platform
	local dir=$out/$1/ffmpeg
	if [ -f "$dir/.built" ]; then
		return
	fi
	rm -rf "$dir"
	mkdir -p "$dir"
	(
		cd "$dir"
		"$ffsrc/configure" \
			--enable-cross-compile --target-os=darwin --arch=aarch64 \
			--cc="$cc" --sysroot="$sysroot" \
			--disable-everything --disable-autodetect --disable-programs --disable-doc \
			--disable-network --disable-avdevice --disable-debug \
			--enable-avcodec --enable-avformat --enable-avfilter --enable-swresample --enable-swscale \
			--enable-zlib --enable-pic --enable-static --disable-shared \
			"${ffmpeg_components[@]}" >configure.log
		make -j"$jobs" >make.log 2>&1
		# The ffmpeg program's graph printer embeds these resources.
		mkdir -p fftools/resources
		make fftools/resources/resman.o fftools/resources/graph.html.o fftools/resources/graph.css.o >>make.log 2>&1
		printf 'include ffbuild/config.mak\nprint-%%: ; @echo $($*)\n' >reverb.mak
	)
	touch "$dir/.built"
}

# Compiles one program's fftools sources with fftools_prefix.h and links them
# into one object whose only external symbol is the renamed main.
build_tool() { # platform program sources...
	local plat=$1 prog=$2
	shift 2
	local dir=$out/$plat/ffmpeg objdir=$out/$plat/fftools-$prog
	local cflags cppflags
	cflags=$(make -s -C "$dir" -f reverb.mak print-CFLAGS)
	cppflags=$(make -s -C "$dir" -f reverb.mak print-CPPFLAGS)
	rm -rf "$objdir"
	mkdir -p "$objdir"
	local objs=()
	for src in "$@"; do
		local obj=$objdir/$(echo "$src" | tr / _).o
		local def=
		[ "$src" = "fftools/$prog.c" ] && def=-Dmain=reverb_${prog}_main
		# shellcheck disable=SC2086
		$cc $cppflags $cflags -I"$dir" -I"$ffsrc" -include "$here/src/fftools_prefix.h" $def \
			-Wno-macro-redefined -c "$ffsrc/$src" -o "$obj"
		objs+=("$obj")
	done
	if [ "$prog" = ffmpeg ]; then
		objs+=("$dir/fftools/resources/resman.o" "$dir/fftools/resources/graph.html.o" "$dir/fftools/resources/graph.css.o")
	fi
	xcrun --sdk "$sdk" ld -r -arch arm64 -exported_symbol "_reverb_${prog}_main" -o "$out/$plat/$prog-tool.o" "${objs[@]}"
}

textformat=(
	fftools/textformat/avtextformat.c fftools/textformat/tf_compact.c fftools/textformat/tf_default.c
	fftools/textformat/tf_flat.c fftools/textformat/tf_ini.c fftools/textformat/tf_json.c
	fftools/textformat/tf_mermaid.c fftools/textformat/tf_xml.c fftools/textformat/tw_avio.c
	fftools/textformat/tw_buffer.c fftools/textformat/tw_stdout.c
)

build_platform() { # platform
	local plat=$1
	setup "$plat"
	echo "building $plat"
	build_ffmpeg "$plat"
	build_tool "$plat" ffmpeg fftools/ffmpeg.c fftools/ffmpeg_dec.c fftools/ffmpeg_demux.c fftools/ffmpeg_enc.c \
		fftools/ffmpeg_filter.c fftools/ffmpeg_hw.c fftools/ffmpeg_mux.c fftools/ffmpeg_mux_init.c \
		fftools/ffmpeg_opt.c fftools/ffmpeg_sched.c fftools/graph/graphprint.c fftools/sync_queue.c \
		fftools/thread_queue.c fftools/cmdutils.c fftools/opt_common.c "${textformat[@]}"
	build_tool "$plat" ffprobe fftools/ffprobe.c fftools/cmdutils.c fftools/opt_common.c "${textformat[@]}"

	local dir=$out/$plat/ffmpeg obj=$out/$plat/obj
	mkdir -p "$obj"
	# shellcheck disable=SC2086
	$cc -O2 -fPIC -w -c "$out/quickjs/quickjs-amalgam.c" -o "$obj/quickjs.o"
	# shellcheck disable=SC2086
	$cc -O2 -fPIC -Wall -I"$out/quickjs" -c "$here/src/qjs_run.c" -o "$obj/qjs_run.o"
	# shellcheck disable=SC2086
	$cc -O2 -fPIC -Wall -I"$dir" -I"$ffsrc" -c "$here/src/fftools_shim.c" -o "$obj/fftools_shim.o"
	# shellcheck disable=SC2086
	$cc -O2 -fPIC -Wall $pyinc -c "$here/src/reverb_native.c" -o "$obj/reverb_native.o"

	xcrun --sdk "$sdk" libtool -static -no_warning_for_no_symbols -o "$out/$plat/libreverbnative.a" \
		"$out/$plat/ffmpeg-tool.o" "$out/$plat/ffprobe-tool.o" "$obj"/*.o \
		"$dir/libavformat/libavformat.a" "$dir/libavcodec/libavcodec.a" "$dir/libavfilter/libavfilter.a" \
		"$dir/libswresample/libswresample.a" "$dir/libswscale/libswscale.a" "$dir/libavutil/libavutil.a"
	mkdir -p "$out/$plat/include"
	cp "$here/src/reverb_native.h" "$out/$plat/include/"
	echo "built $out/$plat/libreverbnative.a"
}

for p in "${platforms[@]}"; do
	build_platform "$p"
done

packages=$out/app_packages
if [ ! -f "$packages/.installed" ] || [ "$here/requirements.txt" -nt "$packages/.installed" ]; then
	rm -rf "$packages"
	python3.14 -m pip install --quiet --disable-pip-version-check --target "$packages" --no-deps \
		--only-binary=:all: --require-hashes --no-compile -r "$here/requirements.txt"
	# Console scripts and data files are of no use inside the app.
	rm -rf "$packages/bin" "$packages/share"
	touch "$packages/.installed"
	echo "installed $packages"
fi

if [ -f "$out/iphoneos/libreverbnative.a" ] && [ -f "$out/iphonesimulator/libreverbnative.a" ]; then
	fw=$here/../Frameworks/ReverbNative.xcframework
	rm -rf "$fw" "$here/../Frameworks/Python.xcframework"
	mkdir -p "$here/../Frameworks"
	cp -R "$out/python/Python.xcframework" "$here/../Frameworks/"
	xcodebuild -create-xcframework \
		-library "$out/iphoneos/libreverbnative.a" -headers "$out/iphoneos/include" \
		-library "$out/iphonesimulator/libreverbnative.a" -headers "$out/iphonesimulator/include" \
		-output "$fw" >/dev/null
	echo "wrote $fw and Python.xcframework"
fi
