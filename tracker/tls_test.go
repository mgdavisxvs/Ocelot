package tracker

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeSelfSignedCert writes a certificate and key valid for localhost and
// returns their paths.
func writeSelfSignedCert(t *testing.T) (certPath, keyPath string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	dir := t.TempDir()
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")

	certOut, err := os.Create(certPath)
	if err != nil {
		t.Fatalf("failed to create cert file: %v", err)
	}
	defer certOut.Close()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatalf("failed to write cert: %v", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("failed to marshal key: %v", err)
	}

	keyOut, err := os.Create(keyPath)
	if err != nil {
		t.Fatalf("failed to create key file: %v", err)
	}
	defer keyOut.Close()
	if err := pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		t.Fatalf("failed to write key: %v", err)
	}

	return certPath, keyPath
}

// boundPort waits for the server to bind and reports the port it chose.
// Binding port 0 and reading the result back avoids the race in reserving a
// port, closing it, and hoping nothing else takes it first.
func boundPort(t *testing.T, server *Server, errs chan error) int {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errs:
			t.Fatalf("server exited before binding: %v", err)
		default:
		}

		for _, addr := range server.Addrs() {
			if tcp, ok := addr.(*net.TCPAddr); ok && tcp.Port != 0 {
				return tcp.Port
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("server never bound a port")
	return 0
}

// startTLSServer brings up a TLS tracker and returns its base URL.
func startTLSServer(t *testing.T, h *testHarness) string {
	t.Helper()

	certPath, keyPath := writeSelfSignedCert(t)

	h.worker.Config.TLSCertFile = certPath
	h.worker.Config.TLSKeyFile = keyPath
	h.worker.Config.TLSAddr = ":0"

	server := NewServer(h.worker.Config, h.worker)
	t.Cleanup(func() { server.Shutdown() })

	errs := make(chan error, 1)
	go func() { errs <- server.ListenAndServeTLS() }()

	base := fmt.Sprintf("https://127.0.0.1:%d", boundPort(t, server, errs))
	waitForTLS(t, base, errs)

	return base
}

// waitForSecondPort returns the port of the listener that is not `exclude`.
func waitForSecondPort(t *testing.T, server *Server, exclude int, errs chan error) int {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errs:
			t.Fatalf("server exited before binding: %v", err)
		default:
		}

		for _, addr := range server.Addrs() {
			if tcp, ok := addr.(*net.TCPAddr); ok && tcp.Port != 0 && tcp.Port != exclude {
				return tcp.Port
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("the second listener never bound")
	return 0
}

func waitForTLS(t *testing.T, base string, errs chan error) {
	t.Helper()

	client := insecureClient()
	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		select {
		case err := <-errs:
			t.Fatalf("TLS server exited: %v", err)
		default:
		}

		if resp, err := client.Get(base + "/probe"); err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatal("TLS server did not become reachable")
}

// insecureClient trusts the self-signed test certificate.
func insecureClient() *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

// The property the previous implementation lacked: a real announce over TLS
// must reach the worker and come back bencoded, not an empty 200.
func TestTLSAnnounceReachesTheWorker(t *testing.T) {
	h := newTestHarness(t)
	_, passkey := h.addUser(t, 1, true)

	base := startTLSServer(t, h)

	params := url.Values{}
	params.Set("info_hash", h.infoHash)
	params.Set("peer_id", string(testPeerID("peer0001")))
	params.Set("port", "6881")
	params.Set("left", "1024")
	params.Set("compact", "1")
	params.Set("event", "started")

	resp, err := insecureClient().Get(base + "/" + passkey + "/announce?" + params.Encode())
	if err != nil {
		t.Fatalf("TLS announce failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (body: %s)", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "8:intervali") {
		t.Errorf("response is not a bencoded announce: %q", body)
	}
	if !strings.Contains(string(body), "10:incompletei1e") {
		t.Errorf("announce did not register the peer: %q", body)
	}

	// The worker, not a stub, must have seen it.
	if h.torrent.Leechers.Size() != 1 {
		t.Errorf("leechers = %d, want 1 — the announce never reached the worker",
			h.torrent.Leechers.Size())
	}
	if h.db.peerCount() != 1 {
		t.Errorf("RecordPeer calls = %d, want 1", h.db.peerCount())
	}
}

func TestTLSScrapeReachesTheWorker(t *testing.T) {
	h := newTestHarness(t)
	user, passkey := h.addUser(t, 1, true)

	req := announceParams(h.infoHash, testPeerID("peer0001"), 6881, 1<<30, "started")
	if _, err := h.worker.Announce(req, user, net.ParseIP("10.0.0.1"), "qB"); err != nil {
		t.Fatalf("seed announce failed: %v", err)
	}

	base := startTLSServer(t, h)

	params := url.Values{}
	params.Set("info_hash", h.infoHash)

	resp, err := insecureClient().Get(base + "/" + passkey + "/scrape?" + params.Encode())
	if err != nil {
		t.Fatalf("TLS scrape failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "10:incompletei1e") {
		t.Errorf("scrape over TLS did not report the peer: %q", body)
	}
}

func TestTLSRejectsPlaintextClient(t *testing.T) {
	h := newTestHarness(t)
	base := startTLSServer(t, h)

	plain := strings.Replace(base, "https://", "http://", 1)

	resp, err := (&http.Client{Timeout: 3 * time.Second}).Get(plain + "/probe")
	if err == nil {
		resp.Body.Close()
		t.Fatal("a plaintext request to the TLS port should not succeed")
	}
}

func TestTLSNegotiatesModernVersion(t *testing.T) {
	h := newTestHarness(t)
	base := startTLSServer(t, h)

	host := strings.TrimPrefix(base, "https://")

	conn, err := tls.Dial("tcp", host, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("TLS handshake failed: %v", err)
	}
	defer conn.Close()

	if v := conn.ConnectionState().Version; v < tls.VersionTLS12 {
		t.Errorf("negotiated TLS version 0x%04x, want at least TLS 1.2", v)
	}
}

func TestTLSRefusesObsoleteVersion(t *testing.T) {
	h := newTestHarness(t)
	base := startTLSServer(t, h)

	host := strings.TrimPrefix(base, "https://")

	conn, err := tls.Dial("tcp", host, &tls.Config{
		InsecureSkipVerify: true,
		MaxVersion:         tls.VersionTLS11,
	})
	if err == nil {
		conn.Close()
		t.Error("server accepted a TLS 1.1 client; the floor should be TLS 1.2")
	}
}

func TestTLSServesAlongsidePlaintext(t *testing.T) {
	h := newTestHarness(t)
	_, passkey := h.addUser(t, 1, true)

	certPath, keyPath := writeSelfSignedCert(t)

	h.worker.Config.ListenAddr = ":0"
	h.worker.Config.TLSCertFile = certPath
	h.worker.Config.TLSKeyFile = keyPath
	h.worker.Config.TLSAddr = ":0"

	server := NewServer(h.worker.Config, h.worker)
	t.Cleanup(func() { server.Shutdown() })

	errs := make(chan error, 2)
	go func() { errs <- server.ListenAndServe() }()
	plainPort := boundPort(t, server, errs)

	go func() { errs <- server.ListenAndServeTLS() }()
	tlsPort := waitForSecondPort(t, server, plainPort, errs)

	tlsBase := fmt.Sprintf("https://127.0.0.1:%d", tlsPort)
	waitForTLS(t, tlsBase, errs)

	params := url.Values{}
	params.Set("info_hash", h.infoHash)

	// Both listeners must serve the same tracker.
	for _, base := range []string{
		fmt.Sprintf("http://127.0.0.1:%d", plainPort),
		tlsBase,
	} {
		resp, err := insecureClient().Get(base + "/" + passkey + "/scrape?" + params.Encode())
		if err != nil {
			t.Fatalf("%s: request failed: %v", base, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if !strings.Contains(string(body), "d5:filesd") {
			t.Errorf("%s: not a scrape response: %q", base, body)
		}
	}
}

func TestTLSRateLimitAppliesToTLSConnections(t *testing.T) {
	h := newTestHarness(t)
	_, passkey := h.addUser(t, 1, true)

	h.worker.Config.RateLimitRPS = 1
	h.worker.Config.RateLimitBurst = 3

	base := startTLSServer(t, h)
	client := insecureClient()

	var limited bool
	for i := 0; i < 12; i++ {
		resp, err := client.Get(base + "/" + passkey + "/scrape?info_hash=x")
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests {
			limited = true
			break
		}
	}

	if !limited {
		t.Error("rate limiting did not apply to TLS connections")
	}
}

func TestListenAndServeTLSRequiresBothFiles(t *testing.T) {
	tests := []struct {
		name string
		cert string
		key  string
	}{
		{"neither", "", ""},
		{"cert only", "/tmp/cert.pem", ""},
		{"key only", "", "/tmp/key.pem"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHarness(t)
			h.worker.Config.TLSCertFile = tt.cert
			h.worker.Config.TLSKeyFile = tt.key

			server := NewServer(h.worker.Config, h.worker)

			err := server.ListenAndServeTLS()
			if err == nil {
				t.Fatal("expected an error when a TLS file is missing")
			}
			if !strings.Contains(err.Error(), "tls_cert_file") {
				t.Errorf("error = %q, want it to name the missing settings", err)
			}
		})
	}
}

func TestListenAndServeTLSRejectsUnreadableCert(t *testing.T) {
	h := newTestHarness(t)
	h.worker.Config.TLSCertFile = filepath.Join(t.TempDir(), "absent.pem")
	h.worker.Config.TLSKeyFile = filepath.Join(t.TempDir(), "absent.key")

	server := NewServer(h.worker.Config, h.worker)

	err := server.ListenAndServeTLS()
	if err == nil {
		t.Fatal("expected an error for a missing certificate file")
	}
	if !strings.Contains(err.Error(), "keypair") {
		t.Errorf("error = %q, want it to mention the keypair", err)
	}
}

func TestConfigTLSEnabled(t *testing.T) {
	tests := []struct {
		name string
		cert string
		key  string
		want bool
	}{
		{"both set", "cert.pem", "key.pem", true},
		{"neither", "", "", false},
		{"cert only", "cert.pem", "", false},
		{"key only", "", "key.pem", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{TLSCertFile: tt.cert, TLSKeyFile: tt.key}
			if got := config.TLSEnabled(); got != tt.want {
				t.Errorf("TLSEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}
