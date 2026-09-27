package tracker

// announce_edge_guc_test.go — GUC edge-case coverage for the Announce path.
//
// GUC lenses:
//   Knuth  – algorithmic correctness, loop invariants, data-structure invariants
//   Turing – termination conditions, halting behaviour, decidability
//   Church – functional purity, side-effect isolation, referential transparency
//   Gödel  – formal consistency, invariant preservation, impossible-state detection

import (
	"context"
	"net"
	"net/url"
	"strings"
	"testing"
)

// maxInt64Val is math.MaxInt64 without importing "math".
const maxInt64Val = int64(1<<63 - 1)

// ── Knuth tests ───────────────────────────────────────────────────────────────

// TestGUC_Knuth_ZeroUploadDownloadLeft_PeerAddedAsSeeder verifies that an
// announce with all-zero numeric stats and left=0 is treated as a seeder with
// no crash and no leecher registration.
func TestGUC_Knuth_ZeroUploadDownloadLeft_PeerAddedAsSeeder(t *testing.T) {
	w, _, _, u := setupAnnounce(t)
	req := newAnnounceReq("started", 0)
	req.Uploaded = 0
	req.Downloaded = 0

	resp, err := w.Announce(context.Background(), req, u, net.ParseIP("10.0.0.1"), "", "")
	if err != nil {
		t.Fatalf("expected no error for all-zero stats seeder: %v", err)
	}
	if resp == nil {
		t.Fatal("nil response")
	}
	tor, _ := w.Torrents.Get(req.InfoHash)
	if tor.Seeders.Size() != 1 {
		t.Errorf("seeder count = %d, want 1 (left=0 → seeder)", tor.Seeders.Size())
	}
	if tor.Leechers.Size() != 0 {
		t.Errorf("leecher count = %d, want 0 when left=0", tor.Leechers.Size())
	}
}

// TestGUC_Knuth_ExtremeLeft_MaxInt64_AcceptedAsLeecher verifies that
// Left=MaxInt64 is accepted without overflow, panic, or misclassification.
func TestGUC_Knuth_ExtremeLeft_MaxInt64_AcceptedAsLeecher(t *testing.T) {
	w, _, _, u := setupAnnounce(t)
	req := newAnnounceReq("started", maxInt64Val)

	resp, err := w.Announce(context.Background(), req, u, net.ParseIP("10.0.0.1"), "", "")
	if err != nil {
		t.Fatalf("MaxInt64 left should not cause error: %v", err)
	}
	if resp == nil {
		t.Fatal("nil response")
	}
	tor, _ := w.Torrents.Get(req.InfoHash)
	if tor.Leechers.Size() != 1 {
		t.Errorf("leecher count = %d, want 1 for MaxInt64 left", tor.Leechers.Size())
	}
	if tor.Seeders.Size() != 0 {
		t.Errorf("seeder count = %d, want 0 (left > 0 → leecher)", tor.Seeders.Size())
	}
}

// TestGUC_Knuth_NumWantZero_ReplacedByNumWantLimit verifies the loop invariant
// that NumWant=0 is replaced by NumWantLimit before peer selection, so an
// existing seeder is returned to a new leecher.
func TestGUC_Knuth_NumWantZero_ReplacedByNumWantLimit(t *testing.T) {
	w, _, _, u := setupAnnounce(t)
	ip := net.ParseIP("5.5.5.5")

	// Seed with a different user so there is a peer to return.
	seeder := NewUser(UserID(2), true, false)
	seederPeerID := make([]byte, 20)
	copy(seederPeerID, "-DE1300000000000000s")
	seederReq := &AnnounceRequest{
		InfoHash: "testhash000000000001",
		PeerID:   seederPeerID,
		Port:     51413,
		Left:     0,
		Compact:  true,
		Event:    "started",
		NumWant:  50,
	}
	if _, err := w.Announce(context.Background(), seederReq, seeder, ip, "", ""); err != nil {
		t.Fatalf("seeder announce: %v", err)
	}

	// Leecher with NumWant=0 — should receive the seeder.
	leecherPeerID := make([]byte, 20)
	copy(leecherPeerID, "-qB4000000000000000L")
	req := &AnnounceRequest{
		InfoHash:   "testhash000000000001",
		PeerID:     leecherPeerID,
		Port:       6881,
		Left:       1000,
		Compact:    true,
		Event:      "started",
		NumWant:    0, // should be replaced by NumWantLimit
	}
	resp, err := w.Announce(context.Background(), req, u, net.ParseIP("10.0.0.2"), "", "")
	if err != nil {
		t.Fatalf("leecher announce with NumWant=0: %v", err)
	}
	// NumWant=0 replaced by limit (50) → seeder should be included (6 bytes).
	if len(resp.Peers) != 6 {
		t.Errorf("NumWant=0 → %d peer bytes, want 6 (limit applied, seeder present)", len(resp.Peers))
	}
}

// TestGUC_Knuth_NumWantAboveLimit_ClampedToMax checks that NumWant values
// exceeding NumWantLimit never cause the response to carry more than limit×6
// bytes, preserving the data-structure invariant of the compact peer list.
func TestGUC_Knuth_NumWantAboveLimit_ClampedToMax(t *testing.T) {
	w, _, _, u := setupAnnounce(t)
	req := newAnnounceReq("started", 1000)
	req.NumWant = 9999 // far above NumWantLimit=50

	resp, err := w.Announce(context.Background(), req, u, net.ParseIP("10.0.0.1"), "", "")
	if err != nil {
		t.Fatalf("oversized NumWant: %v", err)
	}
	maxBytes := int(w.Config.NumWantLimit) * 6
	if len(resp.Peers) > maxBytes {
		t.Errorf("Peers len %d exceeds clamped max %d (NumWantLimit=%d)",
			len(resp.Peers), maxBytes, w.Config.NumWantLimit)
	}
}

// TestGUC_Knuth_DuplicatePeerIDReannounce_AnnouncesCounterIncrements verifies
// the per-peer Announces counter increments on each re-announce, proving the
// loop invariant that every call to Announce is recorded on the peer struct.
func TestGUC_Knuth_DuplicatePeerIDReannounce_AnnouncesCounterIncrements(t *testing.T) {
	w, _, _, u := setupAnnounce(t)
	ip := net.ParseIP("10.0.0.1")

	req := newAnnounceReq("started", 1000)
	if _, err := w.Announce(context.Background(), req, u, ip, "", ""); err != nil {
		t.Fatalf("first announce: %v", err)
	}

	req2 := newAnnounceReq("", 1000) // re-announce, same peer ID
	if _, err := w.Announce(context.Background(), req2, u, ip, "", ""); err != nil {
		t.Fatalf("re-announce: %v", err)
	}

	tor, _ := w.Torrents.Get(req.InfoHash)
	peerKey := PeerKeyPrime(req.PeerID, u.ID, tor.ID)
	peer, ok := tor.Leechers.Get(peerKey)
	if !ok {
		t.Fatal("peer not found in leechers after re-announce")
	}
	if peer.Announces < 2 {
		t.Errorf("Announces = %d after two announces, want ≥ 2", peer.Announces)
	}
}

// ── Turing tests ──────────────────────────────────────────────────────────────

// TestGUC_Turing_StoppedBeforeStarted_SwarmRemainsEmpty verifies that sending
// event=stopped for a peer that was never added to the swarm terminates cleanly
// and leaves the swarm with zero peers (no leaked entries).
func TestGUC_Turing_StoppedBeforeStarted_SwarmRemainsEmpty(t *testing.T) {
	w, _, _, u := setupAnnounce(t)
	req := newAnnounceReq("stopped", 1000) // peer not in swarm yet

	// Should not panic or hang; error is acceptable.
	_, _ = w.Announce(context.Background(), req, u, net.ParseIP("10.0.0.1"), "", "")

	tor, _ := w.Torrents.Get(req.InfoHash)
	if tor.Leechers.Size() != 0 {
		t.Errorf("leechers = %d after stopped-before-started, want 0", tor.Leechers.Size())
	}
	if tor.Seeders.Size() != 0 {
		t.Errorf("seeders = %d after stopped-before-started, want 0", tor.Seeders.Size())
	}
}

// TestGUC_Turing_CompletedWithLeftGtZero_NotTreatedAsSnatch verifies the
// decidability rule: event=completed is a snatch only when Left==0.  When
// Left>0 the peer is treated as a leecher and no snatch record is emitted.
func TestGUC_Turing_CompletedWithLeftGtZero_NotTreatedAsSnatch(t *testing.T) {
	w, db, _, u := setupAnnounce(t)
	req := newAnnounceReq("completed", 500) // completed but left > 0

	_, err := w.Announce(context.Background(), req, u, net.ParseIP("10.0.0.1"), "", "")
	if err != nil {
		t.Fatalf("completed+left>0 announce: %v", err)
	}

	if db.snatchRecs != 0 {
		t.Errorf("snatch records = %d, want 0 when completed with left>0", db.snatchRecs)
	}
	tor, _ := w.Torrents.Get(req.InfoHash)
	if tor.Leechers.Size() != 1 {
		t.Errorf("leecher count = %d, want 1 (left>0 → leecher despite completed event)",
			tor.Leechers.Size())
	}
}

// TestGUC_Turing_EmptyInfoHash_ParseError verifies that ParseAnnounceParams
// halts immediately on a missing info_hash, since a tracker cannot admit a
// request without knowing which torrent is being announced.
func TestGUC_Turing_EmptyInfoHash_ParseError(t *testing.T) {
	params := url.Values{
		"peer_id": {"-qB40000000000000000"},
		"port":    {"6881"},
		"compact": {"1"},
		// deliberately omit info_hash
	}
	_, err := ParseAnnounceParams(params, net.ParseIP("10.0.0.1"))
	if err == nil {
		t.Error("expected error for missing info_hash; ParseAnnounceParams should halt")
	}
}

// TestGUC_Turing_NonCompact_AnnounceRejected verifies that Announce halts with
// an error when Compact is false, enforcing the BEP-23 compactness requirement.
func TestGUC_Turing_NonCompact_AnnounceRejected(t *testing.T) {
	w, _, _, u := setupAnnounce(t)
	req := newAnnounceReq("started", 1000)
	req.Compact = false

	_, err := w.Announce(context.Background(), req, u, net.ParseIP("10.0.0.1"), "", "")
	if err == nil {
		t.Error("expected error for non-compact announce (BEP-23 required)")
	}
}

// TestGUC_Turing_UnregisteredInfoHash_AnnounceRejected verifies that Announce
// halts when the info_hash is not registered, preventing the creation of
// phantom swarms for unknown torrents.
func TestGUC_Turing_UnregisteredInfoHash_AnnounceRejected(t *testing.T) {
	w, _, _ := newTestWorker()
	// No torrent added to w.Torrents.
	u := NewUser(UserID(1), true, false)
	req := newAnnounceReq("started", 1000)

	_, err := w.Announce(context.Background(), req, u, net.ParseIP("10.0.0.1"), "", "")
	if err == nil {
		t.Error("expected error for unregistered torrent info_hash")
	}
}

// ── Church tests ──────────────────────────────────────────────────────────────

// TestGUC_Church_ParseNegativeValues_ClampedToZero_TableDriven verifies the
// referential-transparency property of parseInt64: for any negative string
// input the output is always 0, with no side effects.
func TestGUC_Church_ParseNegativeValues_ClampedToZero_TableDriven(t *testing.T) {
	cases := []struct {
		name       string
		uploaded   string
		downloaded string
		left       string
	}{
		{"all_negative", "-1", "-2", "-3"},
		{"upload_negative", "-100", "0", "0"},
		{"download_negative", "0", "-999", "0"},
		{"left_negative", "0", "0", "-1"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			params := url.Values{
				"info_hash":  {"00000000000000000001"},
				"peer_id":    {"-qB40000000000000000"},
				"port":       {"6881"},
				"uploaded":   {tc.uploaded},
				"downloaded": {tc.downloaded},
				"left":       {tc.left},
			}
			req, err := ParseAnnounceParams(params, net.ParseIP("10.0.0.1"))
			if err != nil {
				t.Fatalf("ParseAnnounceParams: %v", err)
			}
			if req.Uploaded != 0 {
				t.Errorf("uploaded = %d, want 0 for negative input %q", req.Uploaded, tc.uploaded)
			}
			if req.Downloaded != 0 {
				t.Errorf("downloaded = %d, want 0 for negative input %q", req.Downloaded, tc.downloaded)
			}
			if req.Left != 0 {
				t.Errorf("left = %d, want 0 for negative input %q", req.Left, tc.left)
			}
		})
	}
}

// TestGUC_Church_UnknownPasskey_ServerRejectsDetectably verifies that the
// server's announce handler is a pure gateway: an unknown 32-char passkey
// produces a "failure reason" response without mutating any tracker state.
func TestGUC_Church_UnknownPasskey_ServerRejectsDetectably(t *testing.T) {
	f := newTestFixture()
	// 32-char passkey that is not in the user list.
	unknownPasskey := "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
	httpReq := buildAnnounceURL(unknownPasskey, testInfoHash, testPeerID, "started", 0, 0, 1000)
	resp, _ := f.server.handleRequest(httpReq, net.ParseIP("1.2.3.4"))

	if !strings.Contains(string(resp), "failure reason") {
		t.Errorf("expected 'failure reason' in response for unknown passkey; got: %q",
			string(resp))
	}
	// No peers should have been added to the torrent.
	tor, _ := f.worker.Torrents.Get(testInfoHash)
	if tor.Leechers.Size() != 0 || tor.Seeders.Size() != 0 {
		t.Error("unknown passkey rejection mutated torrent state unexpectedly")
	}
}

// TestGUC_Church_ShortPasskey_ServerRejectsMalformed verifies that the server
// refuses a passkey shorter than 32 characters before touching any state.
func TestGUC_Church_ShortPasskey_ServerRejectsMalformed(t *testing.T) {
	f := newTestFixture()
	shortPasskey := "tooshort" // 8 chars — well under the 32-char requirement
	httpReq := buildAnnounceURL(shortPasskey, testInfoHash, testPeerID, "started", 0, 0, 1000)
	resp, _ := f.server.handleRequest(httpReq, net.ParseIP("1.2.3.4"))

	body := string(resp)
	if !strings.Contains(body, "failure reason") && !strings.Contains(body, "Malformed") {
		t.Errorf("expected rejection response for short passkey; got: %q", body)
	}
}

// TestGUC_Church_CanLeechFalse_RejectsLeeching_Deterministically verifies the
// referential-transparency property that a user with CanLeech=false attempting
// to leech always yields an error, regardless of swarm state.
func TestGUC_Church_CanLeechFalse_RejectsLeeching_Deterministically(t *testing.T) {
	w, _, _, _ := setupAnnounce(t)
	noLeechUser := NewUser(UserID(99), false, false) // CanLeech = false

	for i := 0; i < 3; i++ {
		req := newAnnounceReq("started", 1000)
		_, err := w.Announce(context.Background(), req, noLeechUser, net.ParseIP("10.0.0.1"), "", "")
		if err == nil {
			t.Errorf("call %d: expected error for CanLeech=false user trying to leech", i+1)
		}
	}
}

// TestGUC_Church_TwoUserAnnounces_StatsIndependent verifies side-effect
// isolation: two distinct users' seeding/leeching counters are independent,
// and stopping one user does not alter the other's counter.
func TestGUC_Church_TwoUserAnnounces_StatsIndependent(t *testing.T) {
	w, _, _, _ := setupAnnounce(t)

	u1 := NewUser(UserID(10), true, false)
	u2 := NewUser(UserID(11), true, false)
	ip1 := net.ParseIP("10.0.0.10")
	ip2 := net.ParseIP("10.0.0.11")

	pid1 := make([]byte, 20)
	copy(pid1, "-qB4000000000000001a")
	pid2 := make([]byte, 20)
	copy(pid2, "-qB4000000000000002b")

	req1 := &AnnounceRequest{
		InfoHash: "testhash000000000001",
		PeerID:   pid1,
		Port:     6881,
		Left:     0, // seeder
		Compact:  true,
		Event:    "started",
		NumWant:  50,
	}
	req2 := &AnnounceRequest{
		InfoHash: "testhash000000000001",
		PeerID:   pid2,
		Port:     6882,
		Left:     0, // seeder
		Compact:  true,
		Event:    "started",
		NumWant:  50,
	}

	if _, err := w.Announce(context.Background(), req1, u1, ip1, "", ""); err != nil {
		t.Fatalf("u1 announce: %v", err)
	}
	if _, err := w.Announce(context.Background(), req2, u2, ip2, "", ""); err != nil {
		t.Fatalf("u2 announce: %v", err)
	}
	if u1.Seeding.Load() != 1 {
		t.Errorf("u1.Seeding = %d, want 1", u1.Seeding.Load())
	}
	if u2.Seeding.Load() != 1 {
		t.Errorf("u2.Seeding = %d, want 1", u2.Seeding.Load())
	}

	// Stop u1; u2's counter must remain 1.
	stop1 := &AnnounceRequest{
		InfoHash: "testhash000000000001",
		PeerID:   pid1,
		Port:     6881,
		Left:     0,
		Compact:  true,
		Event:    "stopped",
		NumWant:  50,
	}
	if _, err := w.Announce(context.Background(), stop1, u1, ip1, "", ""); err != nil {
		t.Fatalf("u1 stop: %v", err)
	}

	if u1.Seeding.Load() != 0 {
		t.Errorf("u1.Seeding = %d after stop, want 0", u1.Seeding.Load())
	}
	if u2.Seeding.Load() != 1 {
		t.Errorf("u2.Seeding = %d after u1 stop, want 1 (independent)", u2.Seeding.Load())
	}
}

// ── Gödel tests ───────────────────────────────────────────────────────────────

// TestGUC_Godel_SnatchEmitted_CompletedWithLeftZero verifies the formal
// invariant: a "completed" announce with Left==0 must produce exactly one
// snatch record, regardless of prior announce state.
func TestGUC_Godel_SnatchEmitted_CompletedWithLeftZero(t *testing.T) {
	w, db, _, u := setupAnnounce(t)
	ip := net.ParseIP("10.0.0.1")

	// First announce as leecher.
	req1 := newAnnounceReq("started", 1000)
	if _, err := w.Announce(context.Background(), req1, u, ip, "", ""); err != nil {
		t.Fatalf("started announce: %v", err)
	}

	// Complete the download.
	req2 := newAnnounceReq("completed", 0)
	if _, err := w.Announce(context.Background(), req2, u, ip, "", ""); err != nil {
		t.Fatalf("completed announce: %v", err)
	}

	if db.snatchRecs != 1 {
		t.Errorf("snatch records = %d, want 1 for completed+left==0", db.snatchRecs)
	}
}

// TestGUC_Godel_AfterCompleted_PeerInSeedersNotLeechers verifies the mutual-
// exclusion invariant: after a "completed" announce a peer must appear in the
// Seeders map and must NOT appear in the Leechers map.
func TestGUC_Godel_AfterCompleted_PeerInSeedersNotLeechers(t *testing.T) {
	w, _, _, u := setupAnnounce(t)
	ip := net.ParseIP("10.0.0.1")

	req1 := newAnnounceReq("started", 1000)
	if _, err := w.Announce(context.Background(), req1, u, ip, "", ""); err != nil {
		t.Fatalf("started announce: %v", err)
	}

	req2 := newAnnounceReq("completed", 0)
	if _, err := w.Announce(context.Background(), req2, u, ip, "", ""); err != nil {
		t.Fatalf("completed announce: %v", err)
	}

	tor, _ := w.Torrents.Get(req1.InfoHash)
	peerKey := PeerKeyPrime(req1.PeerID, u.ID, tor.ID)

	if _, ok := tor.Seeders.Get(peerKey); !ok {
		t.Error("peer not found in Seeders after completed announce (invariant violated)")
	}
	if _, ok := tor.Leechers.Get(peerKey); ok {
		t.Error("peer still in Leechers after completed announce (mutual-exclusion violated)")
	}
}

// TestGUC_Godel_StoppedSeeder_RemovedFromSeeders verifies the invariant that a
// stopped seeder is unconditionally absent from the Seeders map after the call
// returns, leaving no impossible phantom entries.
func TestGUC_Godel_StoppedSeeder_RemovedFromSeeders(t *testing.T) {
	w, _, _, u := setupAnnounce(t)
	ip := net.ParseIP("10.0.0.1")

	reqJoin := newAnnounceReq("started", 0) // left=0 → seeder
	if _, err := w.Announce(context.Background(), reqJoin, u, ip, "", ""); err != nil {
		t.Fatalf("seeder join: %v", err)
	}
	tor, _ := w.Torrents.Get(reqJoin.InfoHash)
	if tor.Seeders.Size() != 1 {
		t.Fatalf("seeder count = %d before stop, want 1", tor.Seeders.Size())
	}

	reqStop := newAnnounceReq("stopped", 0)
	if _, err := w.Announce(context.Background(), reqStop, u, ip, "", ""); err != nil {
		t.Fatalf("seeder stop: %v", err)
	}

	if tor.Seeders.Size() != 0 {
		t.Errorf("seeder count = %d after stop, want 0 (phantom seeder)", tor.Seeders.Size())
	}
	if tor.Leechers.Size() != 0 {
		t.Errorf("leecher count = %d after stop, want 0", tor.Leechers.Size())
	}
}

// TestGUC_Godel_GlobalLeechers_ConsistentWithTorrentLeechers verifies the
// consistency invariant: the global Stats.Leechers counter must always equal
// the sum of all per-torrent leecher counts after a series of announces.
func TestGUC_Godel_GlobalLeechers_ConsistentWithTorrentLeechers(t *testing.T) {
	w, _, _, _ := setupAnnounce(t)
	tor, _ := w.Torrents.Get("testhash000000000001")

	for i := 0; i < 3; i++ {
		u := NewUser(UserID(100+i), true, false)
		pid := make([]byte, 20)
		copy(pid, "-qB4000000000000000x")
		pid[19] = byte(i + 1)
		req := &AnnounceRequest{
			InfoHash: "testhash000000000001",
			PeerID:   pid,
			Port:     uint16(6000 + i),
			Left:     1000, // leecher
			Compact:  true,
			Event:    "started",
			NumWant:  50,
		}
		if _, err := w.Announce(context.Background(), req, u, net.ParseIP("10.0.0.1"), "", ""); err != nil {
			t.Fatalf("announce for user %d: %v", i, err)
		}
	}

	globalLeechers := int(w.Stats.Leechers.Load())
	torrentLeechers := tor.Leechers.Size()

	if globalLeechers != torrentLeechers {
		t.Errorf("global Stats.Leechers=%d != torrent.Leechers.Size()=%d (consistency invariant violated)",
			globalLeechers, torrentLeechers)
	}
}

// TestGUC_Godel_PortZero_ParsedSuccessfully verifies that port=0 does not
// violate any type invariant in ParseAnnounceParams: the function must not
// return an error and must preserve req.Port == 0.
func TestGUC_Godel_PortZero_ParsedSuccessfully(t *testing.T) {
	params := url.Values{
		"info_hash": {"00000000000000000001"},
		"peer_id":   {"-qB40000000000000000"},
		"port":      {"0"},
		"compact":   {"1"},
	}
	req, err := ParseAnnounceParams(params, net.ParseIP("10.0.0.1"))
	if err != nil {
		t.Fatalf("ParseAnnounceParams with port=0: %v", err)
	}
	if req.Port != 0 {
		t.Errorf("req.Port = %d, want 0 (round-trip invariant)", req.Port)
	}
	// Verify CompactIPPort does not panic or return nil for port=0 + valid IPv4.
	compact := CompactIPPort(net.ParseIP("10.0.0.1"), 0)
	if compact == nil {
		t.Error("CompactIPPort returned nil for port=0 with valid IPv4 (type invariant violated)")
	}
	if len(compact) != 6 {
		t.Errorf("CompactIPPort len = %d, want 6", len(compact))
	}
	// Port bytes must both be zero.
	if compact[4] != 0 || compact[5] != 0 {
		t.Errorf("port bytes = [%d,%d], want [0,0] for port=0", compact[4], compact[5])
	}
}
