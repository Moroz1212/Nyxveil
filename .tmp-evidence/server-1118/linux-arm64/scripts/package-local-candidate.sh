#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"
VERSION="$(tr -d '[:space:]' < VERSION)"
STAGE="${ROOT}/dist/local-candidate/nyxveil-server-${VERSION}-local-candidate"
[[ ! -e "${STAGE}" ]] || { echo 'candidate staging already exists; use a fresh build'; exit 1; }
mkdir -p "${STAGE}"
cp -a dist/release/. "${STAGE}/"
cp scripts/run-local-candidate-gate.sh "${STAGE}/run-local-candidate-gate.sh"
cat >"${STAGE}/LOCAL-CANDIDATE.txt" <<'EOF'
Nyxveil Server 1.1.18 вЂ” LOCAL candidate, not a GitHub Release.
Only for a clean disposable Ubuntu 24.04 host with systemd PID1 and /dev/net/tun.
Required tools: bash, coreutils, util-linux, curl, jq, openssl, nftables,
libcap2-bin, python3, iproute2, ca-certificates. Public DNS must point to the host;
TCP 80/443 and UDP 443 must be reachable. Use an existing Control Plane 1.3.3
and a NEW bootstrap token. Nothing is sent or provisioned by this bundle alone.

Verify the outer archive SHA256 against the local report before extracting.
Create /root/NYXVEIL_DISPOSABLE_TEST_HOST on the DISPOSABLE host.
Run from the extracted directory (substitute your CP, location, DNS and email):

sudo bash ./run-local-candidate-gate.sh --confirm-disposable-host \
  --control-plane https://CP-HOST:PORT --location LOCATION-ID \
  --public-host NODE-FQDN --tls-domain NODE-FQDN --tls-email ACME-EMAIL

Provide the bootstrap token on stdin when prompted, or use a root-owned 0600
file with --bootstrap-token-file PATH. Never put the token in command arguments.
If the CP uses a private CA, also supply --control-plane-ca-file PATH.
The runner verifies all bundled files before invoking the local-candidate gate.
Reports: /tmp/nyxveil-clean-host-gate-report.txt and .json.
Exit 0 means every mandatory check passed. Failure is nonzero; preserve node.key
and config and use a NEW token for repair. Never run this on a production host.
No live gate was run when building this bundle on Windows.
EOF
{
  printf 'head=%s\n' "$(git rev-parse HEAD)"
  printf 'working_tree=local-uncommitted\nversion=%s\n' "${VERSION}"
  printf 'diff_sha256=%s\n' "$(git diff --binary HEAD | sha256sum | awk '{print $1}')"
} >"${STAGE}/SOURCE.txt"
(cd "${STAGE}"; find . -type f ! -name BUNDLE-SHA256SUMS -print0 | LC_ALL=C sort -z | xargs -0 sha256sum > BUNDLE-SHA256SUMS)
go run scripts/make-release-archive.go -dir "${STAGE}" -out "${ROOT}/dist/nyxveil-server-${VERSION}-local-candidate.tar.gz"
sha256sum "${ROOT}/dist/nyxveil-server-${VERSION}-local-candidate.tar.gz"
