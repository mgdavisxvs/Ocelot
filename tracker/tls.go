package tracker

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

// TLSConfig holds TLS configuration fields read from ocelot.conf.
type TLSConfig struct {
	CertFile string
	KeyFile  string
	AutoTLS  bool
	Domain   string
	CacheDir string // directory for Let's Encrypt certificate cache
}

// newTLSListener wraps net.Listen with a tls.Config loaded from certFile/keyFile.
func newTLSListener(addr, certFile, keyFile string) (net.Listener, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS cert/key: %w", err)
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		CipherSuites: []uint16{
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_CHACHA20_POLY1305_SHA256,
		},
	}
	ln, err := tls.Listen("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("tls listen on %s: %w", addr, err)
	}
	return ln, nil
}

// newAutoTLSListener creates a tls.Listener backed by Let's Encrypt.
// It also starts a plain HTTP server on :80 to answer ACME challenges.
func newAutoTLSListener(addr, domain, cacheDir string) (net.Listener, error) {
	if cacheDir == "" {
		cacheDir = "/var/lib/ocelot/certs"
	}
	mgr := &autocert.Manager{
		Prompt:      autocert.AcceptTOS,
		HostPolicy:  autocert.HostWhitelist(domain),
		Cache:       autocert.DirCache(cacheDir),
		RenewBefore: 30 * 24 * time.Hour,
	}

	// ACME HTTP-01 challenge responder.
	go func() {
		srv := &http.Server{
			Addr:         ":80",
			Handler:      mgr.HTTPHandler(nil),
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 5 * time.Second,
		}
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("TLS: ACME HTTP-01 challenge server error: %v", err)
		}
	}()

	cfg := mgr.TLSConfig()
	cfg.MinVersion = tls.VersionTLS13
	ln, err := tls.Listen("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("auto-tls listen on %s: %w", addr, err)
	}
	return ln, nil
}

// RedirectHTTPToHTTPS returns an http.HandlerFunc that issues a 301 redirect
// to the same path over HTTPS.
func RedirectHTTPToHTTPS() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target := "https://" + r.Host + r.URL.Path
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	}
}
