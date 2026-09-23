package tracker

import (
	"net"
	"testing"

	"github.com/mgdavisxvs/Ocelot/commons"
)

// buildTorrentWithPeers creates a Torrent with numSeeders seeders (public IPs)
// and numLeechers leechers. The requesting user ID is excluded from both lists.
func buildTorrentWithPeers(torrentID TorrentID, numSeeders, numLeechers int, excludeUserID UserID) *Torrent {
	tor := NewTorrent(torrentID)
	for i := 0; i < numSeeders; i++ {
		uid := UserID(100 + i)
		if uid == excludeUserID {
			uid = UserID(200 + i)
		}
		p := &Peer{
			UserID:  uid,
			IP:      net.ParseIP("5.5.5.5"),
			Port:    uint16(6881 + i),
			Visible: true,
			IPPort:  []byte{5, 5, 5, 5, byte(6881 >> 8), byte(6881 & 0xff)},
		}
		p.Announces = 3
		tor.Seeders.Set(string(rune('a'+i)), p)
	}
	for i := 0; i < numLeechers; i++ {
		uid := UserID(300 + i)
		if uid == excludeUserID {
			uid = UserID(400 + i)
		}
		p := &Peer{
			UserID:  uid,
			IP:      net.ParseIP("6.6.6.6"),
			Port:    uint16(7000 + i),
			Visible: true,
			IPPort:  []byte{6, 6, 6, 6, byte(7000 >> 8), byte(7000 & 0xff)},
		}
		tor.Leechers.Set(string(rune('A'+i)), p)
	}
	return tor
}

func newEconomicWorker(mc *MockCommons) *Worker {
	return &Worker{
		Config: &Config{AnnounceInterval: 1800},
		DB:     &mockDB{},
		Commons: mc,
	}
}

// ── selectPeersWithEconomics ──────────────────────────────────────────────────

func TestSelectPeersWithEconomics_CallsEvaluatePeers(t *testing.T) {
	mc := newMockCommons()
	w := newEconomicWorker(mc)

	tor := buildTorrentWithPeers(1, 3, 0, 99)
	peers := w.selectPeersWithEconomics(tor, nil, 99, 5, true)

	if len(mc.Evaluated) == 0 {
		t.Fatal("EvaluatePeers was not called")
	}
	req := mc.Evaluated[0]
	if req.LeecherUserID != 99 {
		t.Errorf("LeecherUserID = %d, want 99", req.LeecherUserID)
	}
	if len(req.Candidates) != 3 {
		t.Errorf("Candidates len = %d, want 3", len(req.Candidates))
	}
	if len(peers) == 0 {
		t.Error("expected non-empty peer bytes")
	}
}

func TestSelectPeersWithEconomics_RejectedDecision_EmptyBytes(t *testing.T) {
	mc := newMockCommons()
	mc.EvalDecision = &commons.AllocationDecision{
		Accepted: false,
	}
	w := newEconomicWorker(mc)

	tor := buildTorrentWithPeers(2, 3, 0, 99)
	result := w.selectPeersWithEconomics(tor, nil, 99, 5, true)

	if len(result) != 0 {
		t.Errorf("expected empty bytes on rejection, got %d bytes", len(result))
	}
}

func TestSelectPeersWithEconomics_NilCommons_FallsBack(t *testing.T) {
	w := &Worker{
		Config:  &Config{AnnounceInterval: 1800},
		DB:      &mockDB{},
		Commons: nil,
	}

	tor := buildTorrentWithPeers(3, 2, 0, 99)
	// With no commons, SelectPeersOptimized is called — should return seeder bytes
	result := w.selectPeersWithEconomics(tor, nil, 99, 5, true)
	// Two seeders with valid 6-byte IPPort → 12 bytes expected
	if len(result) != 12 {
		t.Errorf("fallback: expected 12 bytes (2 seeders), got %d", len(result))
	}
}

func TestSelectPeersWithEconomics_Seeder_SkipsEconomics(t *testing.T) {
	mc := newMockCommons()
	w := newEconomicWorker(mc)

	tor := buildTorrentWithPeers(4, 0, 3, 99)
	// isLeecher=false → seeder path, EvaluatePeers should not be called
	w.selectPeersWithEconomics(tor, nil, 99, 5, false)

	if len(mc.Evaluated) != 0 {
		t.Error("EvaluatePeers should not be called for seeder announces")
	}
}

func TestSelectPeersWithEconomics_NoSeeders_FallsBack(t *testing.T) {
	mc := newMockCommons()
	w := newEconomicWorker(mc)

	// No seeders → should fall back without calling EvaluatePeers
	tor := buildTorrentWithPeers(5, 0, 2, 99)
	w.selectPeersWithEconomics(tor, nil, 99, 5, true)

	if len(mc.Evaluated) != 0 {
		t.Error("EvaluatePeers should not be called when no seeders available")
	}
}

func TestSelectPeersWithEconomics_RankedOrder(t *testing.T) {
	mc := newMockCommons()
	w := newEconomicWorker(mc)

	tor := buildTorrentWithPeers(6, 5, 0, 99)

	result := w.selectPeersWithEconomics(tor, nil, 99, 3, true)
	// numwant=3 → at most 3 × 6 = 18 bytes
	if len(result) > 18 {
		t.Errorf("got %d bytes, want ≤ 18 (3 peers × 6 bytes)", len(result))
	}
	if len(result) == 0 {
		t.Error("expected non-empty result")
	}
}

func TestSelectPeersWithEconomics_NumwantZero(t *testing.T) {
	mc := newMockCommons()
	w := newEconomicWorker(mc)

	tor := buildTorrentWithPeers(7, 3, 0, 99)
	result := w.selectPeersWithEconomics(tor, nil, 99, 0, true)

	if len(result) != 0 {
		t.Errorf("numwant=0 should return empty bytes, got %d", len(result))
	}
}

func TestSelectPeersWithEconomics_LeecherFill_ReservoirSampling(t *testing.T) {
	mc := newMockCommons()
	w := newEconomicWorker(mc)

	// 1 seeder + 2 leechers. numwant=5 → seeder loop fills 1, remaining=4 > 0
	// → leecher reservoir sampling fires, covering the for/if/append block.
	tor := buildTorrentWithPeers(8, 1, 2, 99)
	result := w.selectPeersWithEconomics(tor, nil, 99, 5, true)

	// Expect seeder (6 bytes) + up to 2 leechers (12 bytes) = up to 18 bytes
	if len(result) == 0 {
		t.Error("expected non-empty result with seeder + leechers")
	}
	if len(result)%6 != 0 {
		t.Errorf("result length %d is not a multiple of 6", len(result))
	}
}

func TestSelectPeersWithEconomics_RankedSeederNotInMap_Continue(t *testing.T) {
	mc := newMockCommons()
	// Return a RankedSeeders list containing a UserID not present in peerByUserID,
	// followed by the real seeder. The unknown UserID hits the !ok → continue branch.
	mc.EvalDecision = &commons.AllocationDecision{
		Accepted: true,
		RankedSeeders: []*commons.SeederCandidate{
			{UserID: 9999}, // not in peerByUserID → continue
			{UserID: 100},  // valid seeder (first seeder UID from buildTorrentWithPeers)
		},
	}
	w := newEconomicWorker(mc)

	tor := buildTorrentWithPeers(9, 1, 0, 99)
	result := w.selectPeersWithEconomics(tor, nil, 99, 5, true)

	// The real seeder (UserID 100) contributes 6 bytes; the unknown UID is skipped.
	if len(result) != 6 {
		t.Errorf("expected 6 bytes (1 valid seeder), got %d", len(result))
	}
}
