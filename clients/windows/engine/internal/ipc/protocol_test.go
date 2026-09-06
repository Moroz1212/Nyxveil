package ipc_test

import (
	"encoding/json"
	"testing"

	"github.com/nyxveil/client-windows/internal/ipc"
)

func TestConnectRequestRoundTrip(t *testing.T) {
	req := ipc.ConnectRequest{
		Envelope:          ipc.Envelope{Version: ipc.ProtocolVersion, Type: ipc.TypeConnect, ID: "c1"},
		DesiredLocationID: "fi-hel",
		AccessTicket:      "ticket",
		SignedCatalogJSON: []byte(`{"v":1}`),
		CatalogKeys:       map[string]string{"k1": "YWJj"},
		DevicePrivateKey:  make([]byte, 64),
		ControlPlaneHost:  "cp.example",
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var got ipc.ConnectRequest
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != ipc.TypeConnect || got.DesiredLocationID != "fi-hel" || got.ControlPlaneHost != "cp.example" {
		t.Fatalf("%+v", got)
	}
	if len(got.DevicePrivateKey) != 64 || len(got.CatalogKeys) != 1 {
		t.Fatalf("keys/catalog %+v", got)
	}
}

func TestNeedProvideTicketCorrelation(t *testing.T) {
	need := ipc.NeedAccessTicket{
		Envelope:          ipc.Envelope{Version: 1, Type: ipc.TypeNeedAccessTicket, ID: "rid"},
		RequestID:         "abc123",
		DesiredLocationID: "fi-hel",
		Reason:            "session_lost_failover",
		State:             "Reconnecting",
	}
	b, _ := json.Marshal(need)
	var got ipc.NeedAccessTicket
	_ = json.Unmarshal(b, &got)
	if got.RequestID != "abc123" || got.DesiredLocationID != "fi-hel" {
		t.Fatalf("%+v", got)
	}
	provide := ipc.ProvideAccessTicket{
		Envelope:     ipc.Envelope{Version: 1, Type: ipc.TypeProvideAccessTicket},
		RequestID:    got.RequestID,
		AccessTicket: "fresh",
	}
	pb, _ := json.Marshal(provide)
	var pgot ipc.ProvideAccessTicket
	_ = json.Unmarshal(pb, &pgot)
	if pgot.RequestID != "abc123" || pgot.AccessTicket != "fresh" {
		t.Fatalf("%+v", pgot)
	}
}
