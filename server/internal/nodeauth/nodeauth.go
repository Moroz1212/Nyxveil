package nodeauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	ReqV2Prefix  = "nvp-node-req-v2"
	CoreV1Prefix = "nvp-node-v1"
)

// SignRequestV2 attaches nvp-node-req-v2 headers to req.
// pathAndQuery must be the exact Path+Query the Control Plane will see
// (EscapedPath + "?" + RawQuery when query present). body is exact transmitted bytes.
func SignRequestV2(req *http.Request, nodeID string, key ed25519.PrivateKey, pathAndQuery string, body []byte) error {
	if req == nil {
		return fmt.Errorf("nodeauth: nil request")
	}
	if len(key) != ed25519.PrivateKeySize {
		return fmt.Errorf("nodeauth: invalid private key")
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	nonceB64 := base64.RawURLEncoding.EncodeToString(nonce)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sum := sha256.Sum256(body)
	bodyHex := hex.EncodeToString(sum[:])
	method := strings.ToUpper(req.Method)
	msg := CanonicalMessageV2(nodeID, ts, nonceB64, method, pathAndQuery, bodyHex)
	sig := ed25519.Sign(key, []byte(msg))
	req.Header.Set("X-Node-Id", nodeID)
	req.Header.Set("X-Node-Timestamp", ts)
	req.Header.Set("X-Node-Nonce", nonceB64)
	req.Header.Set("X-Node-Signature", base64.RawURLEncoding.EncodeToString(sig))
	return nil
}

// CanonicalMessageV2 builds the exact nvp-node-req-v2 signing string
// (ManagementInterop / Control Plane verify):
//
//	nvp-node-req-v2|{node_id}|{ts}|{nonce}|{METHOD}|{pathAndQuery}|{sha256_hex(body)}
func CanonicalMessageV2(nodeID, ts, nonceB64, method, pathAndQuery, bodySHA256Hex string) string {
	return strings.Join([]string{
		ReqV2Prefix,
		nodeID,
		ts,
		nonceB64,
		strings.ToUpper(method),
		pathAndQuery,
		bodySHA256Hex,
	}, "|")
}

// CanonicalPathQuery builds the path/query string matching ASP.NET ToUriComponent semantics
// for typical absolute URLs without PathBase (PathBase is empty on Control Plane).
func CanonicalPathQuery(u *http.Request) string {
	p := u.URL.EscapedPath()
	if p == "" {
		p = "/"
	}
	if u.URL.RawQuery != "" {
		return p + "?" + u.URL.RawQuery
	}
	return p
}

// SignCoreNodeToken builds Frozen Core nvp-node-v1 PoP for existing-node registration retry.
func SignCoreNodeToken(nodeID string, key ed25519.PrivateKey, unix int64) string {
	msg := fmt.Sprintf("%s|%s|%d", CoreV1Prefix, nodeID, unix)
	sig := ed25519.Sign(key, []byte(msg))
	return fmt.Sprintf("%d.%s", unix, base64.RawURLEncoding.EncodeToString(sig))
}

// EmptyBodySHA256 is SHA-256 of empty body (GET/DELETE).
const EmptyBodySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
