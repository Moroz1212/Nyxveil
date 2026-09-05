package nodeauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
)

func TestCanonicalMessageV2MatchesManagementInterop(t *testing.T) {
	// Mirrors licensing/tests/ManagementInterop/sign.go message construction.
	nodeID := "go-interop"
	ts := "1710000000"
	nonce := make([]byte, 16)
	for i := range nonce {
		nonce[i] = byte(i + 1)
	}
	nonceB64 := base64.RawURLEncoding.EncodeToString(nonce)
	method := "POST"
	pathAndQuery := "/api/v1/nodes/go-interop/health"
	body := "{\"node_id\":\"go-interop\",\"current_sessions\":3,\"note\":\"Привет\"}\n"
	hash := sha256.Sum256([]byte(body))
	bodyHex := hex.EncodeToString(hash[:])

	want := strings.Join([]string{
		"nvp-node-req-v2",
		nodeID,
		ts,
		nonceB64,
		method,
		pathAndQuery,
		bodyHex,
	}, "|")
	got := CanonicalMessageV2(nodeID, ts, nonceB64, method, pathAndQuery, bodyHex)
	if got != want {
		t.Fatalf("canonical message mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestSignRequestV2HeadersAndVerify(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"node_id":"n1","current_sessions":1}`)
	req, err := http.NewRequest(http.MethodPost, "https://cp.example/api/v1/nodes/n1/health", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	pq := CanonicalPathQuery(req)
	if err := SignRequestV2(req, "n1", priv, pq, body); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"X-Node-Id", "X-Node-Timestamp", "X-Node-Nonce", "X-Node-Signature"} {
		if req.Header.Get(h) == "" {
			t.Fatalf("missing header %s", h)
		}
	}
	sum := sha256.Sum256(body)
	msg := CanonicalMessageV2(
		req.Header.Get("X-Node-Id"),
		req.Header.Get("X-Node-Timestamp"),
		req.Header.Get("X-Node-Nonce"),
		req.Method,
		pq,
		hex.EncodeToString(sum[:]),
	)
	sig, err := base64.RawURLEncoding.DecodeString(req.Header.Get("X-Node-Signature"))
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(pub, []byte(msg), sig) {
		t.Fatal("signature did not verify")
	}
}

func TestCanonicalPathQueryEscaping(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://control.example/api/v1/node/config?node_id=go-interop&x=a%2Fb&x=a+b", nil)
	if err != nil {
		t.Fatal(err)
	}
	pq := CanonicalPathQuery(req)
	if !strings.HasPrefix(pq, "/api/v1/node/config?") {
		t.Fatalf("unexpected path: %s", pq)
	}
	if !strings.Contains(pq, "a%2Fb") {
		t.Fatalf("expected escaped path segment preserved: %s", pq)
	}
}

func TestEmptyBodySHA256(t *testing.T) {
	sum := sha256.Sum256(nil)
	if hex.EncodeToString(sum[:]) != EmptyBodySHA256 {
		t.Fatalf("EmptyBodySHA256 constant wrong")
	}
}
