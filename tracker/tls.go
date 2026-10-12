package tracker

import (
	"crypto/tls"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

// TLSConfig holds TLS configuration
type TLSConfig struct {
	CertFile string
	KeyFile  string
	AutoTLS  bool
	Domain   string
	CacheDir string // directory for Let's Encrypt certificate cache
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

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.tlsHandler())

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

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.tlsHandler())

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

// tlsHandler returns an http.HandlerFunc that delegates to handleRequest via
// connection hijacking, preserving the same raw-byte response path as the
// plain-TCP server.
func (s *Server) tlsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijacking not supported", http.StatusInternalServerError)
			return
		}
		conn, rw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()

		host := r.RemoteAddr
		if i := strings.LastIndex(host, ":"); i >= 0 {
			host = host[:i]
		}
		clientIP := net.ParseIP(host)

		response, _ := s.handleRequest(r, clientIP)
		rw.Write(response)
		rw.Flush()
	}
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
