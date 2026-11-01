#!/bin/sh
# Build volt for every platform and publish to dl.voltembedded.com (R2 bucket volt-dl): under the version, and
# under latest/ for links that always fetch the newest build.
#   scripts/release.sh v0.1.0           build, package, upload, move latest
#   scripts/release.sh v0.1.0 --dry     build and package only
# Run on macOS: the darwin builds need cgo (serial port enumeration uses IOKit).
set -eu

VERSION=${1:?usage: release.sh vX.Y.Z [--dry]}
DRY=${2:-}
BUCKET=volt-dl
ROOT=$(cd "$(dirname "$0")/.." && pwd)
OUT="$ROOT/dist/$VERSION"
WEB="$ROOT/../web"

rm -rf "$OUT" && mkdir -p "$OUT"
cd "$ROOT"

# The macOS binaries are signed with the Developer ID in the login keychain and
# notarized with the notarytool profile (default "volt", as for the IDE).
SIGN_ID=$(security find-identity -v -p codesigning | sed -n 's/.*"\(Developer ID Application: [^"]*\)".*/\1/p' | head -1)
[ -n "$SIGN_ID" ] || { echo "error: no Developer ID Application identity in the keychain" >&2; exit 1; }
APPLE_KEYCHAIN_PROFILE=${APPLE_KEYCHAIN_PROFILE:-volt}

# The framework, device examples and default device libraries ship inside the
# binary (internal/sdkfiles); everything else comes from the Grid.
go run ./scripts/embedsdk

for t in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64; do
	os=${t%/*} arch=${t#*/} name=volt-$os-$arch ext= cgo=0 cc=
	[ "$os" = windows ] && ext=.exe
	if [ "$os" = darwin ]; then
		cgo=1
		[ "$arch" = amd64 ] && cc="clang -arch x86_64" || cc="clang -arch arm64"
	fi
	mkdir -p "$OUT/$name"
	CGO_ENABLED=$cgo CC=$cc GOOS=$os GOARCH=$arch go build -trimpath \
		-ldflags "-s -w -X main.version=$VERSION" -o "$OUT/$name/volt$ext" ./cmd/volt
	if [ "$os" = darwin ]; then
		# Developer ID + notarization, or Gatekeeper blocks a volt downloaded in a
		# browser. A bare binary can't hold a stapled ticket; Gatekeeper asks Apple online.
		codesign --sign "$SIGN_ID" --force --timestamp --options runtime "$OUT/$name/volt"
		if [ "$DRY" != --dry ]; then
			ditto -c -k "$OUT/$name/volt" "$OUT/$name.notarize.zip"
			xcrun notarytool submit "$OUT/$name.notarize.zip" --keychain-profile "$APPLE_KEYCHAIN_PROFILE" \
				--wait | tee "$OUT/$name.notarize.log"
			grep -q "status: Accepted" "$OUT/$name.notarize.log" || { echo "error: notarization of $name failed" >&2; exit 1; }
			rm -f "$OUT/$name.notarize.zip" "$OUT/$name.notarize.log"
			# Apple's online ticket can lag the "Accepted" by minutes; until it shows,
			# Gatekeeper blocks the download. Don't publish before it does.
			tries=0
			until codesign -v --check-notarization -R=notarized "$OUT/$name/volt" 2>/dev/null; do
				tries=$((tries + 1))
				[ $tries -le 30 ] || { echo "error: $name's notarization ticket never appeared" >&2; exit 1; }
				sleep 30
			done
		fi
	fi
	if [ "$os" = windows ]; then
		(cd "$OUT/$name" && zip -q "../$name.zip" "volt$ext")
	else
		# No macOS xattrs: GNU tar warns about LIBARCHIVE.xattr.* headers.
		COPYFILE_DISABLE=1 tar --no-xattrs --no-mac-metadata -C "$OUT/$name" -czf "$OUT/$name.tar.gz" volt
	fi
	rm -rf "${OUT:?}/$name"
	echo "built $name"
done

(cd "$OUT" && shasum -a 256 volt-* > SHA256SUMS)
echo "$VERSION" > "$OUT/latest.txt"

[ "$DRY" = --dry ] && { echo "dry run: $OUT"; exit 0; }

cd "$WEB"
for f in "$OUT"/volt-* "$OUT/SHA256SUMS"; do
	npx wrangler r2 object put "$BUCKET/$VERSION/$(basename "$f")" --file "$f" --remote
done
# The same files under latest/, so a link can always download the newest build:
# https://dl.voltembedded.com/latest/volt-windows-amd64.zip
for f in "$OUT"/volt-* "$OUT/SHA256SUMS"; do
	npx wrangler r2 object put "$BUCKET/latest/$(basename "$f")" --file "$f" --cache-control "max-age=300" --remote
done
# latest.txt last, so installers never see a version whose files are not there yet.
npx wrangler r2 object put "$BUCKET/latest.txt" --file "$OUT/latest.txt" \
	--content-type text/plain --cache-control "max-age=60" --remote
echo "published $VERSION"
