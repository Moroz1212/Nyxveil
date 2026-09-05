package nodeauth

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func FuzzCanonicalMessageV2(f *testing.F) {
	f.Add("node-1", "1700000000", "abc", "GET", "/api/v1/node/config", "")
	f.Add("n", "0", "x", "POST", "/api/v1/nodes/n/health", `{"node_id":"n"}`)
	f.Fuzz(func(t *testing.T, nodeID, ts, nonce, method, path, body string) {
		sum := sha256.Sum256([]byte(body))
		_ = CanonicalMessageV2(nodeID, ts, nonce, method, path, hex.EncodeToString(sum[:]))
	})
}
