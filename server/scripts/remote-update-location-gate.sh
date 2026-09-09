#!/usr/bin/env bash
# Isolated dual-node UpdateNodeLatest location-safety gate.
# Requires two already-registered healthy nodes in the SAME location + CP admin API access.
# LIVE execution is environment-dependent — do not mark PASS without running this.
set -euo pipefail
echo "Prepare NODE_A NODE_B LOCATION_ID CP_URL ADMIN credentials, then enqueue UpdateNodeLatest for A and assert B stays healthy."
echo "This harness documents the required live proof; automate against your staging CP."
exit 2
