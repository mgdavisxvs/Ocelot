package tracker

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// ── NoOpSiteComm ─────────────────────────────────────────────────────────────

func TestNoOpSiteComm_NotifyFreeleech(t *testing.T) {
	var sc NoOpSiteComm
	if err := sc.NotifyFreeleech(42, 24); err != nil {
		t.Errorf("NotifyFreeleech: %v", err)
	}
}

func TestNoOpSiteComm_ReportAnomaly(t *testing.T) {
	var sc NoOpSiteComm
	if err := sc.ReportAnomaly(7, 0.95); err != nil {
		t.Errorf("ReportAnomaly: %v", err)
	}
}

func TestNoOpSiteComm_UpdateStats(t *testing.T) {
	var sc NoOpSiteComm
	if err := sc.UpdateStats(10, 20, 5); err != nil {
		t.Errorf("UpdateStats: %v", err)
	}
}

func TestNoOpSiteComm_BanUser(t *testing.T) {
	var sc NoOpSiteComm
	if err := sc.BanUser(99); err != nil {
		t.Errorf("BanUser: %v", err)
	}
}

func TestNoOpSiteComm_UnbanUser(t *testing.T) {
	var sc NoOpSiteComm
	if err := sc.UnbanUser(99); err != nil {
		t.Errorf("UnbanUser: %v", err)
	}
}

// ── GazelleSiteComm ───────────────────────────────────────────────────────────

func newTestGazelleServer(status int) (*httptest.Server, *GazelleSiteComm) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	g := NewGazelleSiteComm(ts.URL, "secret")
	return ts, g
}

func TestGazelleSiteComm_ExpireToken_Success(t *testing.T) {
	ts, g := newTestGazelleServer(http.StatusOK)
	defer ts.Close()
	// ExpireToken is fire-and-forget; it must not panic.
	g.ExpireToken(TorrentID(1), UserID(2))
}

func TestGazelleSiteComm_ExpireToken_ServerError(t *testing.T) {
	ts, g := newTestGazelleServer(http.StatusInternalServerError)
	defer ts.Close()
	// ExpireToken logs but does not return; must not panic.
	g.ExpireToken(TorrentID(1), UserID(2))
}

func TestGazelleSiteComm_NotifyFreeleech_Success(t *testing.T) {
	ts, g := newTestGazelleServer(http.StatusOK)
	defer ts.Close()
	if err := g.NotifyFreeleech(42, 24); err != nil {
		t.Errorf("NotifyFreeleech: %v", err)
	}
}

func TestGazelleSiteComm_NotifyFreeleech_ServerError(t *testing.T) {
	ts, g := newTestGazelleServer(http.StatusBadRequest)
	defer ts.Close()
	if err := g.NotifyFreeleech(42, 24); err == nil {
		t.Error("expected error on HTTP 400")
	}
}

func TestGazelleSiteComm_ReportAnomaly(t *testing.T) {
	ts, g := newTestGazelleServer(http.StatusOK)
	defer ts.Close()
	if err := g.ReportAnomaly(7, 0.95); err != nil {
		t.Errorf("ReportAnomaly: %v", err)
	}
}

func TestGazelleSiteComm_UpdateStats(t *testing.T) {
	ts, g := newTestGazelleServer(http.StatusOK)
	defer ts.Close()
	if err := g.UpdateStats(10, 20, 5); err != nil {
		t.Errorf("UpdateStats: %v", err)
	}
}

func TestGazelleSiteComm_BanUser(t *testing.T) {
	ts, g := newTestGazelleServer(http.StatusOK)
	defer ts.Close()
	if err := g.BanUser(99); err != nil {
		t.Errorf("BanUser: %v", err)
	}
}

func TestGazelleSiteComm_UnbanUser(t *testing.T) {
	ts, g := newTestGazelleServer(http.StatusOK)
	defer ts.Close()
	if err := g.UnbanUser(99); err != nil {
		t.Errorf("UnbanUser: %v", err)
	}
}

func TestGazelleSiteComm_Post_NetworkError(t *testing.T) {
	// Point to a non-existent server.
	g := NewGazelleSiteComm("http://127.0.0.1:19999", "secret")
	// NotifyFreeleech calls post and returns the error.
	if err := g.NotifyFreeleech(1, 1); err == nil {
		t.Error("expected network error from unreachable server")
	}
}
