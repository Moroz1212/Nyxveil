#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
APP="$ROOT/.build/xcode/Build/Products/Release-iphoneos/Nyxveil.app"
EXT="$APP/PlugIns/NyxveilTunnel.appex"
SHARED="$APP/Frameworks/NyxveilShared.framework"
for binary in "$APP/Nyxveil" "$EXT/NyxveilTunnel" "$SHARED/NyxveilShared"; do
  test -s "$binary"
  xcrun lipo -verify_arch arm64 "$binary"
done
plutil -lint "$APP/Info.plist" "$EXT/Info.plist" "$SHARED/Info.plist"
test "$(/usr/libexec/PlistBuddy -c 'Print :NSExtension:NSExtensionPointIdentifier' "$EXT/Info.plist")" = 'com.apple.networkextension.packet-tunnel'
mkdir -p .build/output
# Unique staging directory; no recursive deletion of build outputs.
STAGE="$(mktemp -d "$ROOT/.build/package.XXXXXX")"
mkdir "$STAGE/Payload"
ditto "$APP" "$STAGE/Payload/Nyxveil.app"
SOURCE_SHA="$(git rev-parse HEAD)"
export SOURCE_SHA
IPA="$ROOT/.build/output/Nyxveil-0.1.0-UNSIGNED-${SOURCE_SHA:0:12}.ipa"
ditto -c -k --keepParent "$STAGE/Payload" "$IPA"
export IPA
python3 - <<'PY'
import hashlib, json, os, pathlib, zipfile
p = pathlib.Path(os.environ['IPA'])
with zipfile.ZipFile(p) as z:
    assert z.testzip() is None
    for name in ['Payload/Nyxveil.app/Nyxveil', 'Payload/Nyxveil.app/PlugIns/NyxveilTunnel.appex/NyxveilTunnel']:
        assert name in z.namelist(), name
report = dict(source_head=os.environ['SOURCE_SHA'], workflow_run_id=os.environ.get('GITHUB_RUN_ID'),
    filename=p.name, size=p.stat().st_size, sha256=hashlib.sha256(p.read_bytes()).hexdigest(),
    signed=False, installable=False, xcode_build='PASS', real_iphone='NOT EXECUTED')
(p.parent/'build-report.json').write_text(json.dumps(report, indent=2)+'\n')
(p.parent/'NOT-INSTALLABLE.txt').write_text('UNSIGNED build. This IPA cannot be installed as-is.\n'
    'Nyxveil requires Apple provisioning for the app and PacketTunnel extension.\n'
    'Free Personal Team signing does not support Network Extensions.\n')
print(json.dumps(report, indent=2))
PY
