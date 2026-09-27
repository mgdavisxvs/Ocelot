package tracker

import (
	"net"
	"testing"
)

// ── TLS ──────────────────────────────────────────────────────────────────────

// TestStartTLS_ManualPath verifies that StartTLS with AutoTLS=false dispatches
// to startManualTLS, which tries to load the cert files.  Since the files do
// not exist the call returns quickly with a non-nil error, but all setup
// statements in both StartTLS and startManualTLS execute.
func TestStartTLS_ManualPath_ReturnsError(t *testing.T) {
	f := newTestFixture()
	err := f.server.StartTLS(TLSConfig{
		AutoTLS:  false,
		CertFile: "/nonexistent/cert.pem",
		KeyFile:  "/nonexistent/key.pem",
	})
	if err == nil {
		t.Error("expected error when cert files do not exist, got nil")
	}
}

// TestStartManualTLS_NoCertFile covers startManualTLS directly: all the TLS
// config and http.Server setup code runs, then ListenAndServeTLS fails because
// the cert file is absent.
func TestStartManualTLS_NoCertFile_ReturnsError(t *testing.T) {
	f := newTestFixture()
	err := f.server.startManualTLS("/no/such/cert.pem", "/no/such/key.pem")
	if err == nil {
		t.Error("expected error for missing cert, got nil")
	}
}

// TestStartTLS_AutoTLS_ReturnsError covers the AutoTLS=true dispatch branch in
// StartTLS and all setup code in startAutoTLS.  A listener is pre-bound to
// :443 so that startAutoTLS's ListenAndServeTLS fails immediately with
// "address already in use" rather than blocking indefinitely.
func TestStartTLS_AutoTLS_ReturnsError(t *testing.T) {
	ln, err := net.Listen("tcp", ":443")
	if err != nil {
		t.Skipf("cannot pre-bind :443 to force error: %v", err)
	}
	defer ln.Close()

	f := newTestFixture()
	errTLS := f.server.StartTLS(TLSConfig{
		AutoTLS: true,
		Domain:  "localhost.test",
	})
	if errTLS == nil {
		t.Error("expected error from startAutoTLS (port pre-bound), got nil")
	}
}
