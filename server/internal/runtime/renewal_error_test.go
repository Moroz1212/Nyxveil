package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestClassifyRenewalFailure_Permission(t *testing.T) {
	code, msg := classifyRenewalFailure(errors.New("open /var/lib/nyxveil/acme/acme-account.key: permission denied"))
	if code != "renew_permission_denied" {
		t.Fatalf("code=%q", code)
	}
	if !strings.Contains(msg, "permissions") {
		t.Fatalf("msg=%q", msg)
	}
	if strings.Contains(strings.ToLower(msg), "begin private") {
		t.Fatal("must not leak key material markers")
	}
}

func TestClassifyRenewalFailure_Challenge(t *testing.T) {
	code, msg := classifyRenewalFailure(errors.New("acme: authorization error: http-01 challenge failed for vpn.example.com"))
	if code != "renew_acme_challenge_failed" {
		t.Fatalf("code=%q msg=%q", code, msg)
	}
	if !strings.Contains(msg, "vpn.example.com") {
		t.Fatalf("expected hostname in safe detail, got %q", msg)
	}
}

func TestClassifyRenewalFailure_Listen(t *testing.T) {
	code, msg := classifyRenewalFailure(errors.New(`listen tcp :80: bind: address already in use (HTTP-01 port 80)`))
	if code != "renew_acme_challenge_failed" {
		t.Fatalf("code=%q", code)
	}
	if !strings.Contains(msg, "HTTP-01") {
		t.Fatalf("msg=%q", msg)
	}
}

func TestClassifyRenewalFailure_Timeout(t *testing.T) {
	code, msg := classifyRenewalFailure(context.DeadlineExceeded)
	if code != "renew_failed" || msg != "ACME renewal timed out" {
		t.Fatalf("code=%q msg=%q", code, msg)
	}
}

func TestClassifyRenewalFailure_SanitizesPEM(t *testing.T) {
	_, msg := classifyRenewalFailure(errors.New("boom -----BEGIN PRIVATE KEY-----\nMII...\n-----END PRIVATE KEY-----"))
	if strings.Contains(msg, "BEGIN") || strings.Contains(msg, "MII") {
		t.Fatalf("leaked secret material: %q", msg)
	}
}

func TestSafeRenewalError_NoLongerGenericOnly(t *testing.T) {
	msg := safeRenewalError(errors.New("nodetls: ACME finalize: connection reset"))
	if msg == "ACME renewal failed; details are available in local logs" {
		t.Fatal("expected useful detail, got generic-only message")
	}
	if !strings.Contains(msg, "finalize") && !strings.Contains(msg, "ACME") {
		t.Fatalf("msg=%q", msg)
	}
}
