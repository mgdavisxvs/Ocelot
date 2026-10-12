package tracker

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestGazelleSiteCommExpireTokenPayload(t *testing.T) {
	var received url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error("ParseForm:", err)
		}
		received = r.Form
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	g := NewGazelleSiteComm(srv.URL, "secret123")
	g.ExpireToken(TorrentID(77), UserID(42))

	if received.Get("action") != "expire_token" {
		t.Errorf("action = %q, want expire_token", received.Get("action"))
	}
	if received.Get("torrentid") != "77" {
		t.Errorf("torrentid = %q, want 77", received.Get("torrentid"))
	}
	if received.Get("userid") != "42" {
		t.Errorf("userid = %q, want 42", received.Get("userid"))
	}
	if received.Get("password") != "secret123" {
		t.Errorf("password not forwarded correctly, got %q", received.Get("password"))
	}
}

func TestGazelleSiteCommRetryOn5xx(t *testing.T) {
	origDelay := siteCommBaseDelay
	siteCommBaseDelay = 0
	defer func() { siteCommBaseDelay = origDelay }()

	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < int32(siteCommMaxRetries) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	g := NewGazelleSiteComm(srv.URL, "pw")
	g.ExpireToken(TorrentID(1), UserID(1))

	got := atomic.LoadInt32(&attempts)
	if got != int32(siteCommMaxRetries) {
		t.Errorf("want %d attempts (2 failures + 1 success), got %d",
			siteCommMaxRetries, got)
	}
}

func TestGazelleSiteCommNoRetryOn4xx(t *testing.T) {
	origDelay := siteCommBaseDelay
	siteCommBaseDelay = 0
	defer func() { siteCommBaseDelay = origDelay }()

	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	g := NewGazelleSiteComm(srv.URL, "pw")
	g.ExpireToken(TorrentID(1), UserID(1))

	if atomic.LoadInt32(&attempts) != 1 {
		t.Error("4xx client error should not be retried")
	}
}

func TestGazelleSiteCommExhaustRetries(t *testing.T) {
	origDelay := siteCommBaseDelay
	siteCommBaseDelay = 0
	defer func() { siteCommBaseDelay = origDelay }()

	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	g := NewGazelleSiteComm(srv.URL, "pw")
	g.ExpireToken(TorrentID(1), UserID(1)) // must not panic or hang

	if atomic.LoadInt32(&attempts) != int32(siteCommMaxRetries) {
		t.Errorf("want %d total attempts when all fail, got %d",
			siteCommMaxRetries, atomic.LoadInt32(&attempts))
	}
}

func TestNoOpSiteCommDoesNotPanic(t *testing.T) {
	var n NoOpSiteComm
	n.ExpireToken(TorrentID(1), UserID(1))
}
