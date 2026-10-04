#!/bin/sh
# Build volt for every platform and publish to dl.voltembedded.com (R2 bucket volt-dl).
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
	if [ "$os" = windows ]; then
		(cd "$OUT/$name" && zip -q "../$name.zip" "volt$ext")
	else
		tar -C "$OUT/$name" -czf "$OUT/$name.tar.gz" volt
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
# latest.txt last, so installers never see a version whose files are not there yet.
npx wrangler r2 object put "$BUCKET/latest.txt" --file "$OUT/latest.txt" \
	--content-type text/plain --cache-control "max-age=60" --remote
echo "published $VERSION"
