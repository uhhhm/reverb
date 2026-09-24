#!/usr/bin/env bash
# An Xcode build phase of the Reverb target: puts the embedded Python into the
# built app. It copies the standard library for the platform being built
# (python/lib) and the bundled packages (app_packages), compiles both to
# bytecode so the first run does not, and moves each binary extension module
# into a framework, the only form of loadable code iOS accepts. It signs those
# frameworks when the build signs, and leaves them for the signer otherwise.
#
# Adapted from Python.xcframework/build/utils.sh (BeeWare), which signs
# unconditionally and so cannot build the unsigned IPA.
set -euo pipefail

xcf=$PROJECT_DIR/Frameworks/Python.xcframework
packages=$PROJECT_DIR/build/native/app_packages
app=$CODESIGNING_FOLDER_PATH

case $EFFECTIVE_PLATFORM_NAME in
-iphonesimulator) slice=ios-arm64_x86_64-simulator ;;
-iphoneos) slice=ios-arm64 ;;
*)
	echo "error: unsupported platform $EFFECTIVE_PLATFORM_NAME" >&2
	exit 1
	;;
esac
arch=${ARCHS%% *}

# Parts of the standard library an app has no use for, the largest being
# Python's own test suite.
unused=(--exclude 'libpython*.dylib' --exclude '/python3.*/test/' --exclude '/python3.*/idlelib/'
	--exclude '/python3.*/tkinter/' --exclude '/python3.*/turtledemo/' --exclude '/python3.*/ensurepip/'
	--exclude '/python3.*/pydoc_data/' --exclude '/python3.*/lib-dynload/*test*' --exclude '/python3.*/lib-dynload/xxlimited*')
mkdir -p "$app/python/lib"
rsync -au --delete --delete-excluded "$xcf/lib/" "$app/python/lib/" "${unused[@]}"
rsync -au "$xcf/$slice/lib-$arch/" "$app/python/lib/" "${unused[@]}"
rsync -au --delete --exclude .installed "$packages/" "$app/app_packages/"

python3.14 -m compileall -q -j 0 -d /python "$app/python/lib" >/dev/null || true
python3.14 -m compileall -q -j 0 "$app/app_packages" >/dev/null || true

sign() {
	if [ -n "${EXPANDED_CODE_SIGN_IDENTITY:-}" ] && [ "${CODE_SIGNING_ALLOWED:-YES}" != NO ]; then
		/usr/bin/codesign --force --sign "$EXPANDED_CODE_SIGN_IDENTITY" ${OTHER_CODE_SIGN_FLAGS:-} \
			-o runtime --timestamp=none --preserve-metadata=identifier,entitlements,flags \
			--generate-entitlement-der "$1"
	fi
}

# Python finds a moved module through the .fwork file left in its place.
install_extension() { # base-relative-to-app full-path
	local base=$1 ext=$2
	local rel=${ext#"$app"/}
	local dotted
	dotted=$(echo "${rel#"$base"/}" | cut -d . -f 1 | tr / .)
	local fw=Frameworks/$dotted.framework
	if [ ! -d "$app/$fw" ]; then
		mkdir -p "$app/$fw"
		cp "$xcf/build/iOS-dylib-Info-template.plist" "$app/$fw/Info.plist"
		plutil -replace CFBundleExecutable -string "$dotted" "$app/$fw/Info.plist"
		plutil -replace CFBundleIdentifier -string "$(echo "$PRODUCT_BUNDLE_IDENTIFIER.$dotted" | tr _ -)" "$app/$fw/Info.plist"
	fi
	mv "$ext" "$app/$fw/$dotted"
	echo "$fw/$dotted" >"${ext%.so}.fwork"
	echo "${rel%.so}.fwork" >"$app/$fw/$dotted.origin"
	sign "$app/$fw"
}

find "$app/python/lib" -name '*.so' | while read -r ext; do
	install_extension "python/lib/$(ls "$app/python/lib" | grep -E '^python3\.[0-9]+$')/lib-dynload" "$ext"
done
find "$app/app_packages" -name '*.so' | while read -r ext; do
	install_extension app_packages "$ext"
done
