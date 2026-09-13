package tracker

import (
	"net"
	"sync"
	"testing"
	"time"
)

// addPeer inserts a peer that last announced at the given time.
func addPeer(t *testing.T, list *PeerList, key string, userID UserID, lastAnnounced time.Time) {
	t.Helper()

	list.Set(key, &Peer{
		UserID:        userID,
		LastAnnounced: lastAnnounced,
		Port:          6881,
		IP:            net.ParseIP("10.0.0.1"),
		Visible:       true,
	})
}

func TestReaperRemovesExpiredPeers(t *testing.T) {
	h := newTestHarness(t)
	now := time.Now()

	addPeer(t, h.torrent.Leechers, "stale", 1, now.Add(-3*time.Hour))
	addPeer(t, h.torrent.Leechers, "fresh", 2, now.Add(-1*time.Minute))

	reaper := NewReaper(h.worker.Torrents, h.db, h.worker.Stats, 2*time.Hour, time.Minute)
	result := reaper.ReapOnce()

	if result.LeechersRemoved != 1 {
		t.Errorf("LeechersRemoved = %d, want 1", result.LeechersRemoved)
	}
	if h.torrent.Leechers.Size() != 1 {
		t.Errorf("leechers remaining = %d, want 1", h.torrent.Leechers.Size())
	}
	if _, ok := h.torrent.Leechers.Get("stale"); ok {
		t.Error("expired peer is still in the swarm")
	}
	if _, ok := h.torrent.Leechers.Get("fresh"); !ok {
		t.Error("reaper removed a peer that announced recently")
	}
}

func TestReaperRemovesExpiredSeeders(t *testing.T) {
	h := newTestHarness(t)
	now := time.Now()

	addPeer(t, h.torrent.Seeders, "stale-seeder", 1, now.Add(-5*time.Hour))
	addPeer(t, h.torrent.Seeders, "live-seeder", 2, now)

	reaper := NewReaper(h.worker.Torrents, h.db, h.worker.Stats, 2*time.Hour, time.Minute)
	result := reaper.ReapOnce()

	if result.SeedersRemoved != 1 {
		t.Errorf("SeedersRemoved = %d, want 1", result.SeedersRemoved)
	}
	if h.torrent.Seeders.Size() != 1 {
		t.Errorf("seeders remaining = %d, want 1", h.torrent.Seeders.Size())
	}
}

func TestReaperSparesPeersThatNeverAnnounced(t *testing.T) {
	h := newTestHarness(t)

	// A zero timestamp means the peer was created but has not completed its
	// first announce; reaping it immediately would drop a joining peer.
	addPeer(t, h.torrent.Leechers, "joining", 1, time.Time{})

	reaper := NewReaper(h.worker.Torrents, h.db, h.worker.Stats, time.Hour, time.Minute)
	result := reaper.ReapOnce()

	if result.LeechersRemoved != 0 {
		t.Errorf("LeechersRemoved = %d, want 0", result.LeechersRemoved)
	}
	if h.torrent.Leechers.Size() != 1 {
		t.Error("reaper dropped a peer that has not announced yet")
	}
}

func TestReaperSparesPeerExactlyAtBoundary(t *testing.T) {
	h := newTestHarness(t)

	// Just inside the timeout: must survive.
	addPeer(t, h.torrent.Leechers, "borderline", 1, time.Now().Add(-59*time.Minute))

	reaper := NewReaper(h.worker.Torrents, h.db, h.worker.Stats, time.Hour, time.Minute)
	reaper.ReapOnce()

	if h.torrent.Leechers.Size() != 1 {
		t.Error("peer just inside the timeout was reaped")
	}
}

func TestReaperDecrementsStats(t *testing.T) {
	h := newTestHarness(t)
	now := time.Now()

	// Match the counters an announce would have incremented.
	h.worker.Stats.Seeders.Store(2)
	h.worker.Stats.Leechers.Store(3)

	addPeer(t, h.torrent.Seeders, "s1", 1, now.Add(-4*time.Hour))
	addPeer(t, h.torrent.Seeders, "s2", 2, now)
	addPeer(t, h.torrent.Leechers, "l1", 3, now.Add(-4*time.Hour))
	addPeer(t, h.torrent.Leechers, "l2", 4, now.Add(-4*time.Hour))
	addPeer(t, h.torrent.Leechers, "l3", 5, now)

	reaper := NewReaper(h.worker.Torrents, h.db, h.worker.Stats, 2*time.Hour, time.Minute)
	reaper.ReapOnce()

	if got := h.worker.Stats.Seeders.Load(); got != 1 {
		t.Errorf("Stats.Seeders = %d, want 1", got)
	}
	if got := h.worker.Stats.Leechers.Load(); got != 1 {
		t.Errorf("Stats.Leechers = %d, want 1", got)
	}
}

func TestReaperSweepsEveryTorrent(t *testing.T) {
	h := newTestHarness(t)
	now := time.Now()

	second := NewTorrent(TorrentID(2))
	h.worker.Torrents.Set(testInfoHash(0xCC), second)

	addPeer(t, h.torrent.Leechers, "a", 1, now.Add(-4*time.Hour))
	addPeer(t, second.Leechers, "b", 2, now.Add(-4*time.Hour))

	reaper := NewReaper(h.worker.Torrents, h.db, h.worker.Stats, time.Hour, time.Minute)
	result := reaper.ReapOnce()

	if result.TorrentsSwept != 2 {
		t.Errorf("TorrentsSwept = %d, want 2", result.TorrentsSwept)
	}
	if result.LeechersRemoved != 2 {
		t.Errorf("LeechersRemoved = %d, want 2", result.LeechersRemoved)
	}
}

func TestReaperDeactivatesReapedPeersInStorage(t *testing.T) {
	h := newTestHarness(t)

	addPeer(t, h.torrent.Leechers, "gone", 42, time.Now().Add(-4*time.Hour))

	reaper := NewReaper(h.worker.Torrents, h.db, h.worker.Stats, time.Hour, time.Minute)
	reaper.ReapOnce()

	h.db.mu.Lock()
	defer h.db.mu.Unlock()

	if len(h.db.deactivated) != 1 {
		t.Fatalf("DeactivatePeers received %d refs, want 1", len(h.db.deactivated))
	}
	ref := h.db.deactivated[0]
	if ref.UserID != 42 {
		t.Errorf("deactivated UserID = %d, want 42", ref.UserID)
	}
	if ref.TorrentID != h.torrent.ID {
		t.Errorf("deactivated TorrentID = %d, want %d", ref.TorrentID, h.torrent.ID)
	}
}

func TestReaperSkipsStorageWhenNothingExpired(t *testing.T) {
	h := newTestHarness(t)

	addPeer(t, h.torrent.Leechers, "fresh", 1, time.Now())

	reaper := NewReaper(h.worker.Torrents, h.db, h.worker.Stats, time.Hour, time.Minute)
	reaper.ReapOnce()

	h.db.mu.Lock()
	defer h.db.mu.Unlock()

	if len(h.db.deactivated) != 0 {
		t.Errorf("expected no storage writes, got %d refs", len(h.db.deactivated))
	}
	if len(h.db.torrents) != 0 {
		t.Errorf("expected no torrent flush, got %d", len(h.db.torrents))
	}
}

func TestReaperToleratesNilDatabase(t *testing.T) {
	h := newTestHarness(t)

	addPeer(t, h.torrent.Leechers, "stale", 1, time.Now().Add(-4*time.Hour))

	reaper := NewReaper(h.worker.Torrents, nil, h.worker.Stats, time.Hour, time.Minute)
	result := reaper.ReapOnce()

	if result.LeechersRemoved != 1 {
		t.Errorf("LeechersRemoved = %d, want 1", result.LeechersRemoved)
	}
}

func TestReaperEmptyTorrentListIsNoOp(t *testing.T) {
	reaper := NewReaper(NewTorrentList(), nil, &Stats{}, time.Hour, time.Minute)
	result := reaper.ReapOnce()

	if result.TorrentsSwept != 0 || result.SeedersRemoved != 0 || result.LeechersRemoved != 0 {
		t.Errorf("expected an empty sweep, got %+v", result)
	}
}

func TestReaperStartStop(t *testing.T) {
	h := newTestHarness(t)

	addPeer(t, h.torrent.Leechers, "stale", 1, time.Now().Add(-4*time.Hour))

	reaper := NewReaper(h.worker.Torrents, h.db, h.worker.Stats, time.Hour, 20*time.Millisecond)
	reaper.Start()

	deadline := time.After(2 * time.Second)
	for h.torrent.Leechers.Size() > 0 {
		select {
		case <-deadline:
			t.Fatal("background reaper did not remove the expired peer")
		case <-time.After(10 * time.Millisecond):
		}
	}

	reaper.Stop()
	reaper.Stop() // must be idempotent
}

func TestReaperConcurrentWithAnnounces(t *testing.T) {
	h := newTestHarness(t)
	user, _ := h.addUser(t, 1, true)

	reaper := NewReaper(h.worker.Torrents, h.db, h.worker.Stats, 50*time.Millisecond, 5*time.Millisecond)
	reaper.Start()
	defer reaper.Stop()

	// Announce continuously while the reaper sweeps the same torrent.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				req := announceParams(h.infoHash, testPeerID(string(rune('a'+id))+"eeeeeee"), 6881, 1<<30, "")
				h.worker.Announce(req, user, net.ParseIP("10.0.0.1"), "qB")
			}
		}(i)
	}
	wg.Wait()
}

// The bug this feature exists to fix: a peer that stops announcing must stop
// being handed to other peers.
func TestReapedPeerIsNoLongerAnnounced(t *testing.T) {
	h := newTestHarness(t)
	seeder, _ := h.addUser(t, 1, true)
	leecher, _ := h.addUser(t, 2, true)

	seed := announceParams(h.infoHash, testPeerID("seed0001"), 51413, 0, "started")
	seed.IP = net.ParseIP("192.0.2.10")
	if _, err := h.worker.Announce(seed, seeder, seed.IP, "qB"); err != nil {
		t.Fatalf("seeder announce failed: %v", err)
	}

	leech := announceParams(h.infoHash, testPeerID("peer0002"), 6881, 1<<30, "started")
	resp, err := h.worker.Announce(leech, leecher, net.ParseIP("10.0.0.2"), "qB")
	if err != nil {
		t.Fatalf("leecher announce failed: %v", err)
	}
	if len(resp.Peers) != 6 {
		t.Fatalf("precondition failed: expected the seeder in the peer list, got %d bytes", len(resp.Peers))
	}

	// The seeder goes away without sending event=stopped.
	h.torrent.Seeders.ForEach(func(_ string, peer *Peer) bool {
		peer.LastAnnounced = time.Now().Add(-4 * time.Hour)
		return true
	})

	reaper := NewReaper(h.worker.Torrents, h.db, h.worker.Stats, time.Hour, time.Minute)
	reaper.ReapOnce()

	resp, err = h.worker.Announce(leech, leecher, net.ParseIP("10.0.0.2"), "qB")
	if err != nil {
		t.Fatalf("second leecher announce failed: %v", err)
	}

	if len(resp.Peers) != 0 {
		t.Errorf("a departed peer is still being announced: %d bytes of peers", len(resp.Peers))
	}
	if resp.Complete != 0 {
		t.Errorf("complete = %d, want 0 after the seeder was reaped", resp.Complete)
	}
}
