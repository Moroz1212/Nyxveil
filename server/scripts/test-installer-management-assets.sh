#!/usr/bin/env bash
# Regression: installer must accept the FULL current 8-asset release contract,
# including nyxveil-update-service + nyxveil-management-polkit.
# Reproduces the Ubuntu 24.04 fresh-install failure on asset #7.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FAIL=0
INSTALLER="${ROOT}/installer/install.sh"

pass() { echo "OK  $*"; }
fail() { echo "FAIL $*" >&2; FAIL=1; }

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || { echo "missing $1" >&2; exit 1; }
}
need_cmd bash
need_cmd sha256sum
if ! command -v jq >/dev/null 2>&1 \
  && ! command -v python3 >/dev/null 2>&1 \
  && ! command -v python >/dev/null 2>&1; then
  echo "missing jq or python" >&2
  exit 1
fi

mutate_asset_field() {
  local src="$1" dest="$2" name="$3" field="$4" value="$5"
  if command -v jq >/dev/null 2>&1; then
    jq --arg n "${name}" --arg f "${field}" --arg v "${value}" \
      '(.assets[] | select(.name==$n))[$f]=$v' "${src}" > "${dest}"
    return
  fi
  local py=python3
  command -v python3 >/dev/null 2>&1 || py=python
  "${py}" - "${src}" "${dest}" "${name}" "${field}" "${value}" <<'PY'
import json, sys
src, dest, name, field, value = sys.argv[1:6]
with open(src, encoding="utf-8") as f:
    m = json.load(f)
for a in m["assets"]:
    if a.get("name") == name:
        a[field] = value
with open(dest, "w", encoding="utf-8") as f:
    json.dump(m, f, indent=2)
    f.write("\n")
PY
}

delete_asset() {
  local src="$1" dest="$2" name="$3"
  if command -v jq >/dev/null 2>&1; then
    jq --arg n "${name}" 'del(.assets[] | select(.name==$n))' "${src}" > "${dest}"
    return
  fi
  local py=python3
  command -v python3 >/dev/null 2>&1 || py=python
  "${py}" - "${src}" "${dest}" "${name}" <<'PY'
import json, sys
src, dest, name = sys.argv[1:4]
with open(src, encoding="utf-8") as f:
    m = json.load(f)
m["assets"] = [a for a in m["assets"] if a.get("name") != name]
with open(dest, "w", encoding="utf-8") as f:
    json.dump(m, f, indent=2)
    f.write("\n")
PY
}

append_evil_asset() {
  local src="$1" dest="$2" name="$3"
  if command -v jq >/dev/null 2>&1; then
    jq --arg n "${name}" --arg sha "${MOCK_SHA}" \
      '.assets += [{"name":$n,"sha256":$sha,"url":"https://example.invalid/"+$n,"destination":"/usr/local/sbin/"+$n,"mode":"0755","required":true}]' \
      "${src}" > "${dest}"
    return
  fi
  local py=python3
  command -v python3 >/dev/null 2>&1 || py=python
  "${py}" - "${src}" "${dest}" "${name}" "${MOCK_SHA}" <<'PY'
import json, sys
src, dest, name, sha = sys.argv[1:5]
with open(src, encoding="utf-8") as f:
    m = json.load(f)
m["assets"].append({
    "name": name,
    "sha256": sha,
    "url": "https://example.invalid/" + name,
    "destination": "/usr/local/sbin/" + name,
    "mode": "0755",
    "required": True,
})
with open(dest, "w", encoding="utf-8") as f:
    json.dump(m, f, indent=2)
    f.write("\n")
PY
}

TMP="$(mktemp -d /tmp/nyxveil-installer-mgmt.XXXXXX)"
cleanup() { rm -rf "${TMP}"; }
trap cleanup EXIT

MOCK_BODY=$'mock-binary\n'
MOCK_SHA="$(printf '%s' "${MOCK_BODY}" | sha256sum | awk '{print $1}')"

write_asset() {
  local path="$1"
  printf '%s' "${MOCK_BODY}" > "${path}"
}

# Build a canonical 8-asset unsigned manifest (production destinations).
write_full_manifest() {
  local out="$1"
  local version="${2:-1.1.10}"
  local arch="${3:-linux/amd64}"
  cat > "${out}" <<EOF
{
  "version": "${version}",
  "arch": "${arch}",
  "min_core": "1.0.0",
  "min_protocol": 1,
  "assets": [
    {"name":"nyxveil-server","sha256":"${MOCK_SHA}","url":"https://example.invalid/nyxveil-server-linux-amd64","destination":"/usr/local/sbin/nyxveil-server","mode":"0755","required":true},
    {"name":"nyxveilctl","sha256":"${MOCK_SHA}","url":"https://example.invalid/nyxveilctl-linux-amd64","destination":"/usr/local/sbin/nyxveilctl","mode":"0755","required":true},
    {"name":"nyxveil-catalog-verify","sha256":"${MOCK_SHA}","url":"https://example.invalid/nyxveil-catalog-verify-linux-amd64","destination":"/usr/local/sbin/nyxveil-catalog-verify","mode":"0755","required":true},
    {"name":"production-gate","sha256":"${MOCK_SHA}","url":"https://example.invalid/production-gate.sh","destination":"/usr/local/share/nyxveil/scripts/production-gate.sh","mode":"0755","required":true},
    {"name":"share-version","sha256":"${MOCK_SHA}","url":"https://example.invalid/VERSION","destination":"/usr/local/share/nyxveil/VERSION","mode":"0644","required":true},
    {"name":"share-third-party-core","sha256":"${MOCK_SHA}","url":"https://example.invalid/THIRD_PARTY_CORE.md","destination":"/usr/local/share/nyxveil/THIRD_PARTY_CORE.md","mode":"0644","required":true},
    {"name":"nyxveil-update-service","sha256":"${MOCK_SHA}","url":"https://example.invalid/nyxveil-update.service","destination":"/etc/systemd/system/nyxveil-update.service","mode":"0644","required":true},
    {"name":"nyxveil-management-polkit","sha256":"${MOCK_SHA}","url":"https://example.invalid/50-nyxveil-management.rules","destination":"/etc/polkit-1/rules.d/50-nyxveil-management.rules","mode":"0644","required":true}
  ]
}
EOF
}

run_remote_mock() {
  local mock_root="$1"
  local manifest="$2"
  shift 2
  NYXVEIL_INSTALL_MOCK=1 \
  NYXVEIL_INSTALL_MOCK_ROOT="${mock_root}" \
  NYXVEIL_INSTALL_MOCK_MANIFEST="${manifest}" \
    bash "${INSTALLER}" \
      --control-plane https://example.test \
      --location hel-1 \
      --name "mock-node" \
      --public-host vpn.example.test \
      --bootstrap-token "tok" \
      --non-interactive \
      "$@"
}

echo "== FULL 8-ASSET MANIFEST fresh install =="
MANIFEST="${TMP}/full.json"
write_full_manifest "${MANIFEST}"
MOCK1="${TMP}/mock1"
mkdir -p "${MOCK1}"
if run_remote_mock "${MOCK1}" "${MANIFEST}" >/tmp/nyxveil-full8-out.txt 2>&1; then
  pass "full 8-asset remote mock install exit 0"
else
  fail "full 8-asset remote mock install should exit 0"
  cat /tmp/nyxveil-full8-out.txt >&2 || true
fi
if [[ -f "${MOCK1}/etc/systemd/system/nyxveil-update.service" ]]; then
  pass "update service installed from manifest"
else
  fail "update service missing"
fi
if [[ -f "${MOCK1}/etc/polkit-1/rules.d/50-nyxveil-management.rules" ]]; then
  pass "polkit rule installed from manifest"
else
  fail "polkit rule missing"
fi
if grep -q 'keeping release-verified nyxveil-update.service' /tmp/nyxveil-full8-out.txt; then
  pass "embedded update-unit overwrite prevented"
else
  fail "expected skip embedded overwrite for update service"
fi
if grep -q 'keeping release-verified 50-nyxveil-management.rules' /tmp/nyxveil-full8-out.txt; then
  pass "embedded polkit overwrite prevented"
else
  fail "expected skip embedded overwrite for polkit"
fi
# Marker content from mock download must remain (embedded would rewrite Description differently only if overwritten;
# mock body is "mock-binary\n").
if grep -qx 'mock-binary' "${MOCK1}/etc/systemd/system/nyxveil-update.service"; then
  pass "update service content is release-verified mock body"
else
  fail "update service was overwritten by embedded writer"
fi

echo "== unknown ninth required asset = FAIL =="
MAN_BAD9="${TMP}/bad9.json"
append_evil_asset "${MANIFEST}" "${MAN_BAD9}" "evil-root-script"
MOCK9="${TMP}/mock9"
mkdir -p "${MOCK9}"
set +e
run_remote_mock "${MOCK9}" "${MAN_BAD9}" >/tmp/nyxveil-bad9-out.txt 2>&1
rc=$?
set -e
if [[ "${rc}" -ne 0 ]] && grep -qi 'unknown required asset' /tmp/nyxveil-bad9-out.txt; then
  pass "unknown required asset fail-closed"
else
  fail "unknown ninth asset should die"
  cat /tmp/nyxveil-bad9-out.txt >&2 || true
fi
if [[ ! -f "${MOCK9}/etc/systemd/system/nyxveil-update.service" ]]; then
  pass "rollback removed/avoided update service after unknown asset"
else
  # May exist if installed before ninth asset; rollback must remove on failure.
  if grep -qi 'rollback complete' /tmp/nyxveil-bad9-out.txt && [[ ! -f "${MOCK9}/etc/systemd/system/nyxveil-update.service" ]]; then
    pass "rollback cleared update service"
  elif grep -qi 'rollback complete' /tmp/nyxveil-bad9-out.txt; then
    # Asset order: management assets are 7/8, evil is 9 — so 7/8 installed then fail on 9, rollback should remove.
    if [[ -f "${MOCK9}/etc/systemd/system/nyxveil-update.service" ]]; then
      fail "update service left behind after rollback"
    fi
  else
    fail "expected rollback after unknown asset"
  fi
fi

expect_contract_fail() {
  local label="$1" manifest="$2"
  local mock="${TMP}/m-$(echo "${label}" | tr ' /' '__')"
  mkdir -p "${mock}"
  set +e
  run_remote_mock "${mock}" "${manifest}" >"/tmp/nyxveil-${label}.out" 2>&1
  rc=$?
  set -e
  if [[ "${rc}" -ne 0 ]] && grep -qiE 'contract mismatch|not installed from manifest|unknown required' "/tmp/nyxveil-${label}.out"; then
    pass "${label}"
  else
    fail "${label}"
    cat "/tmp/nyxveil-${label}.out" >&2 || true
  fi
}

echo "== wrong destinations / modes / missing management = FAIL =="
mutate_asset_field "${MANIFEST}" "${TMP}/wrong-upd-dest.json" "nyxveil-update-service" "destination" "/tmp/evil-update.service"
expect_contract_fail "wrong-update-service-destination" "${TMP}/wrong-upd-dest.json"

mutate_asset_field "${MANIFEST}" "${TMP}/wrong-upd-mode.json" "nyxveil-update-service" "mode" "0755"
expect_contract_fail "wrong-update-service-mode" "${TMP}/wrong-upd-mode.json"

mutate_asset_field "${MANIFEST}" "${TMP}/wrong-polkit-dest.json" "nyxveil-management-polkit" "destination" "/tmp/evil.rules"
expect_contract_fail "wrong-polkit-destination" "${TMP}/wrong-polkit-dest.json"

mutate_asset_field "${MANIFEST}" "${TMP}/wrong-polkit-mode.json" "nyxveil-management-polkit" "mode" "0755"
expect_contract_fail "wrong-polkit-mode" "${TMP}/wrong-polkit-mode.json"

delete_asset "${MANIFEST}" "${TMP}/missing-upd.json" "nyxveil-update-service"
expect_contract_fail "missing-update-service" "${TMP}/missing-upd.json"

delete_asset "${MANIFEST}" "${TMP}/missing-polkit.json" "nyxveil-management-polkit"
expect_contract_fail "missing-management-polkit" "${TMP}/missing-polkit.json"

echo "== rollback restores prior management assets on repair failure =="
MOCKR="${TMP}/mock-repair"
mkdir -p "${MOCKR}/etc/systemd/system" "${MOCKR}/etc/polkit-1/rules.d" \
  "${MOCKR}/etc/nyxveil" "${MOCKR}/var/lib/nyxveil" "${MOCKR}/usr/local/sbin"
printf 'OLD-UPDATE-UNIT\n' > "${MOCKR}/etc/systemd/system/nyxveil-update.service"
printf 'OLD-POLKIT\n' > "${MOCKR}/etc/polkit-1/rules.d/50-nyxveil-management.rules"
printf '{"node_id":"nv-keep","location_id":"hel-1"}\n' > "${MOCKR}/etc/nyxveil/server.json"
printf 'old-key\n' > "${MOCKR}/var/lib/nyxveil/node.key"
# Cause failure after management install by injecting unknown asset at end.
append_evil_asset "${MANIFEST}" "${TMP}/repair-fail.json" "evil-after"
set +e
run_remote_mock "${MOCKR}" "${TMP}/repair-fail.json" >/tmp/nyxveil-repair-out.txt 2>&1
rc=$?
set -e
if [[ "${rc}" -ne 0 ]] && grep -qi 'rollback complete' /tmp/nyxveil-repair-out.txt; then
  pass "repair-path failure triggered rollback"
else
  fail "expected rollback on repair failure"
  cat /tmp/nyxveil-repair-out.txt >&2 || true
fi
if grep -qx 'OLD-UPDATE-UNIT' "${MOCKR}/etc/systemd/system/nyxveil-update.service"; then
  pass "rollback restored previous update service"
else
  fail "update service not restored to previous content"
fi
if grep -qx 'OLD-POLKIT' "${MOCKR}/etc/polkit-1/rules.d/50-nyxveil-management.rules"; then
  pass "rollback restored previous polkit rule"
else
  fail "polkit rule not restored to previous content"
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "test-installer-management-assets FAILED" >&2
  exit 1
fi
echo "test-installer-management-assets PASSED"
exit 0
