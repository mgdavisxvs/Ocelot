package tracker

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

// TLSConfig holds TLS configuration
type TLSConfig struct {
	CertFile string
	KeyFile  string
	AutoTLS  bool
	Domain   string
}

// StartTLS starts the tracker with TLS support
func (s *Server) StartTLS(config TLSConfig) error {
	logger := GetDefaultLogger()

	if config.AutoTLS {
		logger.Info("starting tracker with auto TLS (Let's Encrypt)",
			"domain", config.Domain,
			"port", "443",
		)
		return s.startAutoTLS(config.Domain)
	}

	logger.Info("starting tracker with TLS",
		"cert", config.CertFile,
		"key", config.KeyFile,
		"port", "34443",
	)
	return s.startManualTLS(config.CertFile, config.KeyFile)
}

// startManualTLS loads a certificate pair and wraps the listener with TLS
// before handing it to the shared accept loop. All tracker routes
// (announce, scrape, update, stats, …) work identically over TLS.
func (s *Server) startManualTLS(certFile, keyFile string) error {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("load TLS certificate: %w", err)
	}
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}
	ln, err := net.Listen("tcp", ":34443")
	if err != nil {
		return fmt.Errorf("listen :34443: %w", err)
	}
	return s.serveListener(tls.NewListener(ln, tlsConfig))
}

// startAutoTLS obtains and renews a Let's Encrypt certificate for domain
// and wraps the listener with TLS before handing it to the accept loop.
// A plain-HTTP server on :80 handles ACME challenges.
func (s *Server) startAutoTLS(domain string) error {
	certManager := &autocert.Manager{
		Prompt:      autocert.AcceptTOS,
		HostPolicy:  autocert.HostWhitelist(domain),
		Cache:       autocert.DirCache("/var/lib/ocelot/certs"),
		RenewBefore: 30 * 24 * time.Hour,
	}
	// Serve ACME HTTP-01 challenges on port 80.
	go func() {
		http.ListenAndServe(":80", certManager.HTTPHandler(nil))
	}()
	ln, err := net.Listen("tcp", ":443")
	if err != nil {
		return fmt.Errorf("listen :443: %w", err)
	}
	return s.serveListener(tls.NewListener(ln, certManager.TLSConfig()))
}

// RedirectHTTPToHTTPS returns middleware to redirect HTTP to HTTPS
func RedirectHTTPToHTTPS() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target := "https://" + r.Host + r.URL.Path
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	}
}
