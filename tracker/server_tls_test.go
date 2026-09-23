package tracker

import (
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
