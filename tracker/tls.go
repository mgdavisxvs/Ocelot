package tracker

import (
	"crypto/tls"
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

// startManualTLS starts with manual certificate files
func (s *Server) startManualTLS(certFile, keyFile string) error {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13,
		CipherSuites: []uint16{
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_CHACHA20_POLY1305_SHA256,
		},
		PreferServerCipherSuites: true,
	}

	// Create HTTP handler (stub - would need integration)
	mux := http.NewServeMux()
	mux.HandleFunc("/announce", func(w http.ResponseWriter, r *http.Request) {
		// Stub handler
		w.WriteHeader(http.StatusOK)
	})

	server := &http.Server{
		Addr:         ":34443",
		Handler:      mux,
		TLSConfig:    tlsConfig,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return server.ListenAndServeTLS(certFile, keyFile)
}

// startAutoTLS starts with automatic Let's Encrypt certificates
func (s *Server) startAutoTLS(domain string) error {
	certManager := &autocert.Manager{
		Prompt:      autocert.AcceptTOS,
		HostPolicy:  autocert.HostWhitelist(domain),
		Cache:       autocert.DirCache("/var/lib/ocelot/certs"),
		RenewBefore: 30 * 24 * time.Hour, // Renew 30 days before expiry
	}

	tlsConfig := certManager.TLSConfig()
	tlsConfig.MinVersion = tls.VersionTLS13

	// Start HTTP redirect server for ACME challenges
	go func() {
		http.ListenAndServe(":80", certManager.HTTPHandler(nil))
	}()

	// Create HTTP handler (stub - would need integration)
	mux := http.NewServeMux()
	mux.HandleFunc("/announce", func(w http.ResponseWriter, r *http.Request) {
		// Stub handler
		w.WriteHeader(http.StatusOK)
	})

	server := &http.Server{
		Addr:         ":443",
		Handler:      mux,
		TLSConfig:    tlsConfig,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return server.ListenAndServeTLS("", "")
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
