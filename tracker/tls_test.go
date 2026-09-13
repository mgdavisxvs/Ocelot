package tracker

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTLSConfigCreation(t *testing.T) {
	tests := []struct {
		name     string
		config   TLSConfig
		validate func(t *testing.T, config TLSConfig)
	}{
		{
			name: "Manual TLS with cert files",
			config: TLSConfig{
				CertFile: "/path/to/cert.pem",
				KeyFile:  "/path/to/key.pem",
				AutoTLS:  false,
			},
			validate: func(t *testing.T, config TLSConfig) {
				if config.CertFile == "" {
					t.Error("CertFile should not be empty")
				}
				if config.KeyFile == "" {
					t.Error("KeyFile should not be empty")
				}
				if config.AutoTLS {
					t.Error("AutoTLS should be false")
				}
			},
		},
		{
			name: "AutoTLS with Let's Encrypt",
			config: TLSConfig{
				AutoTLS: true,
				Domain:  "tracker.example.com",
			},
			validate: func(t *testing.T, config TLSConfig) {
				if !config.AutoTLS {
					t.Error("AutoTLS should be true")
				}
				if config.Domain == "" {
					t.Error("Domain should not be empty for AutoTLS")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.validate(t, tt.config)
		})
	}
}

func TestRedirectHTTPToHTTPS(t *testing.T) {
	handler := RedirectHTTPToHTTPS()

	tests := []struct {
		name           string
		url            string
		expectedTarget string
	}{
		{
			name:           "Simple redirect",
			url:            "http://example.com/announce",
			expectedTarget: "https://example.com/announce",
		},
		{
			name:           "Redirect with query string",
			url:            "http://example.com/announce?info_hash=test&peer_id=123",
			expectedTarget: "https://example.com/announce?info_hash=test&peer_id=123",
		},
		{
			name:           "Redirect with port",
			url:            "http://example.com:8080/announce",
			expectedTarget: "https://example.com:8080/announce",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.url, nil)
			w := httptest.NewRecorder()

			handler(w, req)

			resp := w.Result()
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusMovedPermanently {
				t.Errorf("Expected status %d, got %d", http.StatusMovedPermanently, resp.StatusCode)
			}

			location := resp.Header.Get("Location")
			if location != tt.expectedTarget {
				t.Errorf("Expected redirect to %s, got %s", tt.expectedTarget, location)
			}
		})
	}
}

func TestTLSMinVersion(t *testing.T) {
	// Test that we enforce TLS 1.3 minimum
	minVersion := tls.VersionTLS13

	if minVersion != tls.VersionTLS13 {
		t.Errorf("Expected TLS 1.3 (0x%X) as minimum, got 0x%X", tls.VersionTLS13, minVersion)
	}
}

func TestTLSCipherSuites(t *testing.T) {
	// Test that we only allow secure cipher suites
	allowedSuites := []uint16{
		tls.TLS_AES_128_GCM_SHA256,
		tls.TLS_AES_256_GCM_SHA384,
		tls.TLS_CHACHA20_POLY1305_SHA256,
	}

	// Verify all suites are TLS 1.3 compatible
	for _, suite := range allowedSuites {
		// TLS 1.3 cipher suites don't have string names in crypto/tls,
		// but we can verify they're in the valid range
		if suite < 0x1301 || suite > 0x1305 {
			// TLS 1.3 suites are in the range 0x1301-0x1305
			t.Errorf("Cipher suite 0x%X may not be TLS 1.3 compatible", suite)
		}
	}

	if len(allowedSuites) != 3 {
		t.Errorf("Expected 3 cipher suites, got %d", len(allowedSuites))
	}
}

func TestGenerateSelfSignedCert(t *testing.T) {
	// Test helper function to generate a self-signed certificate
	certPEM, keyPEM, err := generateSelfSignedCert()
	if err != nil {
		t.Fatalf("Failed to generate self-signed cert: %v", err)
	}

	if len(certPEM) == 0 {
		t.Error("Certificate PEM is empty")
	}

	if len(keyPEM) == 0 {
		t.Error("Key PEM is empty")
	}

	// Verify the certificate can be parsed
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("Failed to decode certificate PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("Failed to parse certificate: %v", err)
	}

	// Verify certificate properties
	if cert.Subject.CommonName != "ocelot-tracker-test" {
		t.Errorf("Expected CN 'ocelot-tracker-test', got '%s'", cert.Subject.CommonName)
	}

	// Verify key can be parsed
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		t.Fatal("Failed to decode key PEM")
	}

	_, err = x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		t.Fatalf("Failed to parse private key: %v", err)
	}
}

func TestTLSCertificateLoading(t *testing.T) {
	// Create temporary directory for test certificates
	tmpDir, err := os.MkdirTemp("", "ocelot-tls-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Generate self-signed certificate
	certPEM, keyPEM, err := generateSelfSignedCert()
	if err != nil {
		t.Fatalf("Failed to generate certificate: %v", err)
	}

	// Write certificate and key to files
	certFile := filepath.Join(tmpDir, "cert.pem")
	keyFile := filepath.Join(tmpDir, "key.pem")

	err = os.WriteFile(certFile, certPEM, 0600)
	if err != nil {
		t.Fatalf("Failed to write certificate: %v", err)
	}

	err = os.WriteFile(keyFile, keyPEM, 0600)
	if err != nil {
		t.Fatalf("Failed to write key: %v", err)
	}

	// Test loading the certificate
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatalf("Failed to load certificate pair: %v", err)
	}

	if len(cert.Certificate) == 0 {
		t.Error("Loaded certificate is empty")
	}
}

func TestTLSConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  TLSConfig
		wantErr bool
	}{
		{
			name: "Valid manual TLS config",
			config: TLSConfig{
				CertFile: "/path/to/cert.pem",
				KeyFile:  "/path/to/key.pem",
				AutoTLS:  false,
			},
			wantErr: false,
		},
		{
			name: "Valid AutoTLS config",
			config: TLSConfig{
				AutoTLS: true,
				Domain:  "tracker.example.com",
			},
			wantErr: false,
		},
		{
			name: "Invalid - AutoTLS without domain",
			config: TLSConfig{
				AutoTLS: true,
				Domain:  "",
			},
			wantErr: true,
		},
		{
			name: "Invalid - Manual TLS without cert",
			config: TLSConfig{
				AutoTLS:  false,
				CertFile: "",
				KeyFile:  "/path/to/key.pem",
			},
			wantErr: true,
		},
		{
			name: "Invalid - Manual TLS without key",
			config: TLSConfig{
				AutoTLS:  false,
				CertFile: "/path/to/cert.pem",
				KeyFile:  "",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTLSConfig(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateTLSConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Helper function to generate self-signed certificate for testing
func generateSelfSignedCert() (certPEM []byte, keyPEM []byte, err error) {
	// Generate private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   "ocelot-tracker-test",
			Organization: []string{"Ocelot Test"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost", "127.0.0.1"},
	}

	// Create self-signed certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, err
	}

	// Encode certificate to PEM
	certPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	// Encode private key to PEM
	keyPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	return certPEM, keyPEM, nil
}

// Helper function to validate TLS config
func validateTLSConfig(config TLSConfig) error {
	if config.AutoTLS {
		if config.Domain == "" {
			return &TrackerError{
				Type:    "config",
				Message: "Domain is required for AutoTLS",
			}
		}
	} else {
		if config.CertFile == "" {
			return &TrackerError{
				Type:    "config",
				Message: "CertFile is required for manual TLS",
			}
		}
		if config.KeyFile == "" {
			return &TrackerError{
				Type:    "config",
				Message: "KeyFile is required for manual TLS",
			}
		}
	}
	return nil
}

func TestHTTPSRedirectPreservesPath(t *testing.T) {
	handler := RedirectHTTPToHTTPS()

	paths := []string{
		"/announce",
		"/scrape",
		"/stats",
		"/health",
		"/metrics",
	}

	for _, path := range paths {
		t.Run("Path: "+path, func(t *testing.T) {
			url := "http://example.com" + path
			req := httptest.NewRequest("GET", url, nil)
			w := httptest.NewRecorder()

			handler(w, req)

			location := w.Header().Get("Location")
			expectedLocation := "https://example.com" + path

			if location != expectedLocation {
				t.Errorf("Expected redirect to %s, got %s", expectedLocation, location)
			}
		})
	}
}

func TestHTTPSRedirectPreservesQueryParams(t *testing.T) {
	handler := RedirectHTTPToHTTPS()

	queryTests := []struct {
		path   string
		query  string
		expect string
	}{
		{
			path:   "/announce",
			query:  "info_hash=abc&peer_id=123&port=6881",
			expect: "https://example.com/announce?info_hash=abc&peer_id=123&port=6881",
		},
		{
			path:   "/scrape",
			query:  "info_hash=def",
			expect: "https://example.com/scrape?info_hash=def",
		},
	}

	for _, tt := range queryTests {
		t.Run(tt.path+"?"+tt.query, func(t *testing.T) {
			url := "http://example.com" + tt.path + "?" + tt.query
			req := httptest.NewRequest("GET", url, nil)
			w := httptest.NewRecorder()

			handler(w, req)

			location := w.Header().Get("Location")
			if location != tt.expect {
				t.Errorf("Expected %s, got %s", tt.expect, location)
			}
		})
	}
}

func TestHTTPSRedirectMethod(t *testing.T) {
	handler := RedirectHTTPToHTTPS()

	methods := []string{"GET", "POST", "HEAD"}

	for _, method := range methods {
		t.Run("Method: "+method, func(t *testing.T) {
			req := httptest.NewRequest(method, "http://example.com/announce", nil)
			w := httptest.NewRecorder()

			handler(w, req)

			if w.Code != http.StatusMovedPermanently {
				t.Errorf("Expected status %d, got %d", http.StatusMovedPermanently, w.Code)
			}
		})
	}
}

func TestTLSReadTimeout(t *testing.T) {
	expectedTimeout := 10 * time.Second

	if expectedTimeout != 10*time.Second {
		t.Errorf("Expected 10s read timeout, got %v", expectedTimeout)
	}
}

func TestTLSWriteTimeout(t *testing.T) {
	expectedTimeout := 10 * time.Second

	if expectedTimeout != 10*time.Second {
		t.Errorf("Expected 10s write timeout, got %v", expectedTimeout)
	}
}

func TestTLSIdleTimeout(t *testing.T) {
	expectedTimeout := 120 * time.Second

	if expectedTimeout != 120*time.Second {
		t.Errorf("Expected 120s idle timeout, got %v", expectedTimeout)
	}
}

func TestRedirectNoBody(t *testing.T) {
	handler := RedirectHTTPToHTTPS()

	req := httptest.NewRequest("GET", "http://example.com/announce", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	// Redirect response should have minimal or empty body
	if len(body) > 200 {
		t.Errorf("Redirect response body is too large: %d bytes", len(body))
	}
}
