#!/bin/bash
set -euo pipefail
export PATH="/opt/homebrew/bin:/usr/local/go/bin:/usr/local/bin:$PATH"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
[[ "$(uname -s)" == Darwin ]] || { echo 'Nvp.xcframework requires macOS and Xcode.' >&2; exit 1; }
command -v go >/dev/null || { echo 'Install Go 1.26+ on the Mac, then rebuild.' >&2; exit 1; }
xcrun --sdk iphoneos --show-sdk-path >/dev/null
cd "$ROOT/Bridge"
mkdir -p "$ROOT/.build/bin" "$ROOT/Frameworks"
# Fingerprint actual inputs so a source edit cannot silently reuse the old binding.
STAMP="$( { find . ../../windows/third_party/nvp -type f \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' \) -exec shasum -a 256 {} \; | LC_ALL=C sort; go version; xcodebuild -version; cat "$ROOT/scripts/build-core.sh"; } | shasum -a 256 | cut -d ' ' -f 1)"
if [[ -f "$ROOT/Frameworks/Nvp.xcframework/Info.plist" && -f "$ROOT/.build/core.stamp" && "$(cat "$ROOT/.build/core.stamp")" == "$STAMP" ]]; then exit 0; fi
go build -o "$ROOT/.build/bin/gobind" golang.org/x/mobile/cmd/gobind
go build -o "$ROOT/.build/bin/gomobile" golang.org/x/mobile/cmd/gomobile
export PATH="$ROOT/.build/bin:$PATH"
gomobile init
gomobile bind -target=ios,iossimulator -iosversion=16.0 -o "$ROOT/Frameworks/Nvp.xcframework" -ldflags='-s -w' .
printf '%s\n' "$STAMP" > "$ROOT/.build/core.stamp"
