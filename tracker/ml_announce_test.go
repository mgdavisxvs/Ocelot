package tracker

import (
	"net"
	"testing"

	"github.com/mgdavisxvs/Ocelot/ml"
)

// peerID whose bytes are all identical — triggers suspicious_client_id_pattern.
const repeatingPeerID = "\xAA\xAA\xAA\xAA\xAA\xAA\xAA\xAA\xAA\xAA\xAA\xAA\xAA\xAA\xAA\xAA\xAA\xAA\xAA\xAA"

// ── ClientDetector tests ──────────────────────────────────────────────────────

func TestAnnounce_ClientDetector_SuspiciousPeerID(t *testing.T) {
	f := newTestFixture()
	f.worker.ClientDetector = ml.NewClientAnomalyDetector()

	user, _ := f.worker.Users.Get(testPasskey)
	req := newAnnounceReqFull(testInfoHash, repeatingPeerID, "started", 0, 0, 1024)

	_, err := f.worker.Announce(req, user, net.ParseIP(testIP), "qBittorrent/4.4")
	if err == nil {
		t.Fatal("expected rejection for suspicious peer ID, got nil error")
	}
	want := "client rejected: suspicious_client_id_pattern"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestAnnounce_ClientDetector_MaliciousUserAgent(t *testing.T) {
	f := newTestFixture()
	f.worker.ClientDetector = ml.NewClientAnomalyDetector()

	user, _ := f.worker.Users.Get(testPasskey)
	req := newAnnounceReqFull(testInfoHash, testPeerID, "started", 0, 0, 1024)

	_, err := f.worker.Announce(req, user, net.ParseIP(testIP), "BitSpirit/3.7")
	if err == nil {
		t.Fatal("expected rejection for known malicious client, got nil error")
	}
	want := "client rejected: known_malicious_client"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestAnnounce_ClientDetector_NilSkipsCheck(t *testing.T) {
	f := newTestFixture()
	// ClientDetector is nil by default — repeating peer ID must pass.

	user, _ := f.worker.Users.Get(testPasskey)
	req := newAnnounceReqFull(testInfoHash, repeatingPeerID, "started", 0, 0, 1024)

	_, err := f.worker.Announce(req, user, net.ParseIP(testIP), "BitSpirit/3.7")
	if err != nil {
		t.Errorf("expected no error with nil ClientDetector, got: %v", err)
	}
}

// ── AnomalyDetector tests ─────────────────────────────────────────────────────

func TestAnnounce_AnomalyDetector_RatioCheating(t *testing.T) {
	f := newTestFixture()
	f.worker.AnomalyDetector = ml.NewAnomalyDetector()

	user, _ := f.worker.Users.Get(testPasskey)
	// 200 GB uploaded, only 500 KB downloaded → ratio ≈ 0.0000025 < 0.01 threshold.
	req := newAnnounceReqFull(testInfoHash, testPeerID, "started",
		200_000_000_000, // uploaded: 200 GB
		500_000,         // downloaded: 500 KB
		0,               // left: seeder
	)

	_, err := f.worker.Announce(req, user, net.ParseIP(testIP), "qBittorrent/4.4")
	if err == nil {
		t.Fatal("expected rejection for ratio cheating, got nil error")
	}
	want := "announce rejected: ratio_cheating"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestAnnounce_AnomalyDetector_NilSkipsCheck(t *testing.T) {
	f := newTestFixture()
	// AnomalyDetector is nil by default — ratio cheating must pass undetected.

	user, _ := f.worker.Users.Get(testPasskey)
	req := newAnnounceReqFull(testInfoHash, testPeerID, "started",
		200_000_000_000, 500_000, 0)

	_, err := f.worker.Announce(req, user, net.ParseIP(testIP), "qBittorrent/4.4")
	if err != nil {
		t.Errorf("expected no error with nil AnomalyDetector, got: %v", err)
	}
}

// ── PeerScorer tests ──────────────────────────────────────────────────────────

func TestAnnounce_PeerScorer_SeederDeliveredToLeecher(t *testing.T) {
	f := newTestFixture()
	f.worker.PeerScorer = ml.NewPeerScorer()

	// Register a seeder first (user 2, testPeerID2).
	seederUser, _ := f.worker.Users.Get(testPasskey2)
	seederReq := newAnnounceReqFull(testInfoHash, testPeerID2, "started", 1024, 0, 0)
	seederReq.IP = net.ParseIP("2.3.4.5")
	_, err := f.worker.Announce(seederReq, seederUser, seederReq.IP, "qBittorrent/4.4")
	if err != nil {
		t.Fatalf("seeder announce failed: %v", err)
	}

	// Leecher announces — should receive the seeder regardless of scorer.
	leecherUser, _ := f.worker.Users.Get(testPasskey)
	leecherReq := newAnnounceReqFull(testInfoHash, testPeerID, "started", 0, 0, 512*1024*1024)
	leecherReq.IP = net.ParseIP(testIP)
	resp, err := f.worker.Announce(leecherReq, leecherUser, leecherReq.IP, "qBittorrent/4.4")
	if err != nil {
		t.Fatalf("leecher announce failed: %v", err)
	}

	// Expect at least one peer in the compact response (6 bytes = 1 IPv4 peer).
	if len(resp.Peers) < 6 {
		t.Errorf("expected seeder in response, got %d bytes of peers", len(resp.Peers))
	}
}
