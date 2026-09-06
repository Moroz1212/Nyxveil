package health

import (
	"strings"
	"testing"
)

func goodDataplane(cp bool) Status {
	return Status{
		Running: true, Accepting: true, BridgeOK: true, TLSOK: true, QUICOK: true,
		TUNReady: true, IdentityPresent: true, VersionBlocked: false,
		CPConnected: cp, TicketKeysLoaded: cp, RevocationStale: !cp,
	}
}

func TestUpdateSucceedsWhenPreexistingCPDisconnectedAndDataplaneHealthy(t *testing.T) {
	pre := CaptureBaseline(goodDataplane(false))
	post := goodDataplane(false)
	res := EvaluatePostUpdate(pre, post)
	if !res.OK || !res.UpdateSuccess {
		t.Fatalf("%+v", res)
	}
	if !res.DataplaneHealthy || res.ManagementPlaneConnected || !res.PreexistingManagementDegradation {
		t.Fatalf("%+v", res)
	}
}

func TestUpdateFailsWhenCPConnectivityRegressesFromConnectedToDisconnected(t *testing.T) {
	pre := CaptureBaseline(goodDataplane(true))
	post := goodDataplane(false)
	res := EvaluatePostUpdate(pre, post)
	if res.OK {
		t.Fatal("must fail on cp_connected regression")
	}
	if !strings.Contains(res.Reason, "cp_connected") {
		t.Fatalf("reason=%q", res.Reason)
	}
}

func TestUpdateFailsOnNewTunRegression(t *testing.T) {
	pre := CaptureBaseline(goodDataplane(false))
	post := goodDataplane(false)
	post.TUNReady = false
	res := EvaluatePostUpdate(pre, post)
	if res.OK {
		t.Fatal("tun regression must fail")
	}
}

func TestUpdateFailsOnNewTLSRegression(t *testing.T) {
	pre := CaptureBaseline(goodDataplane(false))
	post := goodDataplane(false)
	post.TLSOK = false
	res := EvaluatePostUpdate(pre, post)
	if res.OK {
		t.Fatal("tls regression must fail")
	}
}

func TestUpdateFailsOnNewQUICRegression(t *testing.T) {
	pre := CaptureBaseline(goodDataplane(false))
	post := goodDataplane(false)
	post.QUICOK = false
	res := EvaluatePostUpdate(pre, post)
	if res.OK {
		t.Fatal("quic regression must fail")
	}
}

func TestRollbackComparedAgainstPreUpdateBaseline(t *testing.T) {
	pre := CaptureBaseline(goodDataplane(false))
	post := goodDataplane(false)
	rb := EvaluateRollbackSuccess(pre, post)
	if !rb.Complete || !rb.BaselineRestored || rb.Incomplete {
		t.Fatalf("%+v", rb)
	}
}

func TestRollbackHealthyFalseBaselineDoesNotReportIncomplete(t *testing.T) {
	preSt := goodDataplane(false)
	preSt.Healthy = false
	pre := CaptureBaseline(preSt)
	if pre.Healthy {
		t.Fatal("pre should remain globally unhealthy")
	}
	post := goodDataplane(false)
	rb := EvaluateRollbackSuccess(pre, post)
	if !rb.Complete || rb.Incomplete {
		t.Fatalf("same preexisting degradation must not be ROLLBACK INCOMPLETE: %+v", rb)
	}
}

func TestDataplaneOKRequiresAcceptingAndListeners(t *testing.T) {
	st := goodDataplane(false)
	if !st.DataplaneOK() {
		t.Fatal("expected dataplane ok")
	}
	st.Accepting = false
	if st.DataplaneOK() {
		t.Fatal("accepting required")
	}
}
