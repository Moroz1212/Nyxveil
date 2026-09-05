package nodeauth

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	tokenPrefix  = "nvp-node-v1"
	maxTokenSkew = 5 * time.Minute
)

var (
	ErrInvalidNodeToken = errors.New("invalid node token")
	ErrExpiredNodeToken = errors.New("node token expired")
	ErrReplayNodeToken  = errors.New("node token replayed")
)

// SignToken creates an Ed25519-signed node heartbeat token.
// Format: <unix_ts>.<base64url(signature)>
func SignToken(nodeID string, priv ed25519.PrivateKey, now time.Time) string {
	ts := now.Unix()
	msg := message(nodeID, ts)
	sig := ed25519.Sign(priv, msg)
	return fmt.Sprintf("%d.%s", ts, base64.RawURLEncoding.EncodeToString(sig))
}

// VerifyToken validates node token against registered public key.
func VerifyToken(nodeID, token string, pub ed25519.PublicKey, now time.Time) error {
	tsStr, sigB64, ok := strings.Cut(token, ".")
	if !ok || tsStr == "" || sigB64 == "" {
		return ErrInvalidNodeToken
	}
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return ErrInvalidNodeToken
	}
	t := time.Unix(ts, 0)
	if now.Sub(t) > maxTokenSkew || t.Sub(now) > maxTokenSkew {
		return ErrExpiredNodeToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return ErrInvalidNodeToken
	}
	if !ed25519.Verify(pub, message(nodeID, ts), sig) {
		return ErrInvalidNodeToken
	}
	return nil
}

// TokenUnix extracts the timestamp from a node token (for anti-replay).
func TokenUnix(token string) (int64, error) {
	tsStr, _, ok := strings.Cut(token, ".")
	if !ok {
		return 0, ErrInvalidNodeToken
	}
	return strconv.ParseInt(tsStr, 10, 64)
}

func message(nodeID string, ts int64) []byte {
	return []byte(fmt.Sprintf("%s|%s|%d", tokenPrefix, nodeID, ts))
}
