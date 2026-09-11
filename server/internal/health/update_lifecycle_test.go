package health

import "testing"

func TestUpgrade119DrainedTo113(t *testing.T) {
	old := goodDataplane(true)
	old.ServerVersion, old.NodeID, old.ConfigVersion = "1.1.9", "node-1", 7
	old.Draining, old.Accepting = true, false
	pre := CaptureBaseline(old)
	post := old
	post.ServerVersion, post.TLSOK, post.QUICOK = "1.1.13", false, false
	if r := EvaluatePostUpdate(pre, post); !r.OK {
		t.Fatalf("%+v", r)
	}
	if r := EvaluateRollbackSuccess(pre, post); !r.Complete {
		t.Fatalf("%+v", r)
	}
	if post.DataplaneOK() {
		t.Fatal("ordinary readiness must stay strict")
	}
	for name, change := range map[string]func(*Status){
		"identity":         func(s *Status) { s.IdentityPresent = false },
		"changed_identity": func(s *Status) { s.NodeID = "other" },
		"tun":              func(s *Status) { s.TUNReady = false },
		"bridge":           func(s *Status) { s.BridgeOK = false },
		"cp":               func(s *Status) { s.CPConnected = false },
		"tickets":          func(s *Status) { s.TicketKeysLoaded = false },
		"revocation":       func(s *Status) { s.RevocationStale = true },
		"blocked":          func(s *Status) { s.VersionBlocked = true },
		"running":          func(s *Status) { s.Running = false },
		"config":           func(s *Status) { s.ConfigVersion = 6 },
		"undrain":          func(s *Status) { s.Draining = false },
	} {
		t.Run(name, func(t *testing.T) {
			bad := post
			change(&bad)
			if EvaluatePostUpdate(pre, bad).OK || EvaluateRollbackSuccess(pre, bad).Complete {
				t.Fatal("unsafe state accepted")
			}
		})
	}
	pre.LifecycleKnown = false
	if EvaluatePostUpdate(pre, post).OK {
		t.Fatal("legacy accepting=false alone is insufficient")
	}
	active := goodDataplane(true)
	pre = CaptureBaseline(active)
	active.TLSOK = false
	active.QUICOK = false
	if EvaluatePostUpdate(pre, active).OK {
		t.Fatal("active listeners required")
	}
}

func TestMaintenanceUpdate(t *testing.T) {
	s := goodDataplane(true)
	s.Accepting = false
	s.MaintenanceMode = true
	pre := CaptureBaseline(s)
	s.TLSOK = false
	s.QUICOK = false
	if !EvaluatePostUpdate(pre, s).OK {
		t.Fatal("maintenance must pass")
	}
}
