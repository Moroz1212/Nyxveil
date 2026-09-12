package controlplane

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHeartbeatManagementAgentFieldsMarshal(t *testing.T) {
	supports := true
	raw, err := json.Marshal(HeartbeatRequest{
		NodeID:                 "n1",
		SupportsCommands:       &supports,
		ManagementCapabilities: "certificate_renew,service_restart,host_reboot",
		BootID:                 "abc-123",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"supports_commands", "management_capabilities", "boot_id"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("missing %q in %s", key, raw)
		}
	}
	if got["supports_commands"] != true {
		t.Fatalf("supports_commands=%v", got["supports_commands"])
	}
	if got["management_capabilities"] != "certificate_renew,service_restart,host_reboot" {
		t.Fatalf("management_capabilities=%v", got["management_capabilities"])
	}
	if got["boot_id"] != "abc-123" {
		t.Fatalf("boot_id=%v", got["boot_id"])
	}
}

func TestClaimNextCommandSignedAndParsesDTO(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var signed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/node/commands/next" {
			http.NotFound(w, r)
			return
		}
		signed = r.Header.Get("X-Node-Signature") != ""
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"11111111-1111-1111-1111-111111111111","node_id":"n1","type":"RenewCertificate","status":"Claimed","issued_at":"2026-09-08T12:00:00Z","expires_at":"2026-09-08T12:30:00Z","correlation_id":"22222222-2222-2222-2222-222222222222"}`))
	}))
	defer srv.Close()

	c, err := NewClient(srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.NodeID, c.PrivateKey = "n1", priv
	cmd, err := c.ClaimNextCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !signed {
		t.Fatal("expected signed GET")
	}
	if cmd.Type != "RenewCertificate" || cmd.NodeID != "n1" {
		t.Fatalf("%+v", cmd)
	}
}

func TestClaimNextCommandNoContent(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.NodeID, c.PrivateKey = "n1", priv
	_, err = c.ClaimNextCommand(context.Background())
	if !errors.Is(err, ErrNoCommand) {
		t.Fatalf("err=%v want ErrNoCommand", err)
	}
}

func TestMarkCommandStartedProgressAndReportResultSigned(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var startedSigned, progressSigned, resultSigned bool
	var progressBody, resultBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/started"):
			startedSigned = r.Header.Get("X-Node-Signature") != ""
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/progress"):
			progressSigned = r.Header.Get("X-Node-Signature") != ""
			body, _ := io.ReadAll(r.Body)
			progressBody = string(body)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/result"):
			resultSigned = r.Header.Get("X-Node-Signature") != ""
			body, _ := io.ReadAll(r.Body)
			resultBody = string(body)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.NodeID, c.PrivateKey = "n1", priv
	id := "33333333-3333-3333-3333-333333333333"
	if err := c.MarkCommandStarted(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := c.ReportCommandProgress(context.Background(), id, NodeCommandProgressRequest{
		Phase:   "verifying",
		Message: "Verifying signed release assets",
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.ReportCommandResult(context.Background(), id, NodeCommandResultRequest{
		Success:       true,
		ResultCode:    "renewed",
		ResultMessage: "ok",
		BootID:        "boot-1",
	}); err != nil {
		t.Fatal(err)
	}
	if !startedSigned || !progressSigned || !resultSigned {
		t.Fatalf("started=%v progress=%v result=%v", startedSigned, progressSigned, resultSigned)
	}
	if !strings.Contains(progressBody, `"phase":"verifying"`) ||
		!strings.Contains(progressBody, `"message":"Verifying signed release assets"`) {
		t.Fatalf("progress body=%s", progressBody)
	}
	if !strings.Contains(resultBody, `"success":true`) || !strings.Contains(resultBody, `"boot_id":"boot-1"`) {
		t.Fatalf("body=%s", resultBody)
	}
}
