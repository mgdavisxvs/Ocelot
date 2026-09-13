package tracker

import (
	"net"
	"testing"
	"time"
)

// ── reapPeerList unit tests ───────────────────────────────────────────────────

func TestReapPeerList_StaleRemoved(t *testing.T) {
	pl := NewPeerList()
	ip := net.ParseIP("1.2.3.4")

	staleTime := time.Now().Add(-2 * time.Hour)
	freshTime := time.Now().Add(-1 * time.Minute)

	pl.Set("stale", &Peer{LastAnnounced: staleTime, IP: ip})
	pl.Set("fresh", &Peer{LastAnnounced: freshTime, IP: ip})
	pl.Set("zero", &Peer{LastAnnounced: time.Time{}, IP: ip}) // zero → not reaped

	cutoff := time.Now().Add(-1 * time.Hour)
	reapPeerList(pl, cutoff)

	if _, ok := pl.Get("stale"); ok {
		t.Error("stale peer should have been reaped")
	}
	if _, ok := pl.Get("fresh"); !ok {
		t.Error("fresh peer should NOT have been reaped")
	}
	if _, ok := pl.Get("zero"); !ok {
		t.Error("zero-time peer should NOT have been reaped (never announced)")
	}
}

func TestReapPeerList_AllFresh(t *testing.T) {
	pl := NewPeerList()
	for i := 0; i < 5; i++ {
		pl.Set(itoa(i), &Peer{LastAnnounced: time.Now()})
	}
	cutoff := time.Now().Add(-time.Hour)
	reapPeerList(pl, cutoff)
	if pl.Size() != 5 {
		t.Errorf("Size = %d after reap, want 5 (all fresh)", pl.Size())
	}
}

func TestReapPeerList_AllStale(t *testing.T) {
	pl := NewPeerList()
	old := time.Now().Add(-24 * time.Hour)
	for i := 0; i < 4; i++ {
		pl.Set(itoa(i), &Peer{LastAnnounced: old})
	}
	cutoff := time.Now().Add(-time.Hour)
	reapPeerList(pl, cutoff)
	if pl.Size() != 0 {
		t.Errorf("Size = %d after reap, want 0 (all stale)", pl.Size())
	}
}

// ── Reaper.reap() integration ─────────────────────────────────────────────────

func TestReaper_Reap_RemovesStalePeers(t *testing.T) {
	torrents := NewTorrentList()
	tor := NewTorrent(TorrentID(1))

	old := time.Now().Add(-3 * time.Hour)
	fresh := time.Now().Add(-1 * time.Minute)
	ip := net.ParseIP("10.0.0.1")

	tor.Seeders.Set("old-seed", &Peer{LastAnnounced: old, IP: ip})
	tor.Seeders.Set("new-seed", &Peer{LastAnnounced: fresh, IP: ip})
	tor.Leechers.Set("old-leech", &Peer{LastAnnounced: old, IP: ip})
	tor.Leechers.Set("new-leech", &Peer{LastAnnounced: fresh, IP: ip})
	torrents.Set("h1", tor)

	r := NewReaper(torrents, 9999, 3600) // 1-hour timeout
	r.reap()

	if _, ok := tor.Seeders.Get("old-seed"); ok {
		t.Error("old seeder should be reaped")
	}
	if _, ok := tor.Seeders.Get("new-seed"); !ok {
		t.Error("new seeder should survive")
	}
	if _, ok := tor.Leechers.Get("old-leech"); ok {
		t.Error("old leecher should be reaped")
	}
	if _, ok := tor.Leechers.Get("new-leech"); !ok {
		t.Error("new leecher should survive")
	}
}

func TestReaper_Reap_MultipleTorrents(t *testing.T) {
	torrents := NewTorrentList()
	old := time.Now().Add(-2 * time.Hour)

	for i := 0; i < 3; i++ {
		tor := NewTorrent(TorrentID(i))
		tor.Seeders.Set("s", &Peer{LastAnnounced: old})
		tor.Leechers.Set("l", &Peer{LastAnnounced: old})
		torrents.Set(itoa(i), tor)
	}

	r := NewReaper(torrents, 9999, 3600)
	r.reap()

	torrents.ForEach(func(_ string, tor *Torrent) bool {
		if tor.Seeders.Size() != 0 || tor.Leechers.Size() != 0 {
			t.Errorf("torrent %d still has peers after full reap", tor.ID)
		}
		return true
	})
}

func TestReaper_Reap_EmptyTorrents(t *testing.T) {
	torrents := NewTorrentList()
	r := NewReaper(torrents, 9999, 3600)
	r.reap() // must not panic on empty list
}

// ── Reaper start/stop lifecycle ───────────────────────────────────────────────

func TestReaper_StartStop(t *testing.T) {
	torrents := NewTorrentList()
	// Use a very long interval so it never fires during the test
	r := NewReaper(torrents, 9999, 3600)
	r.Start()
	// Small delay to ensure goroutine is running
	time.Sleep(10 * time.Millisecond)
	r.Stop()
	// Stop should not deadlock; if we reach here the goroutine exited cleanly
}

// ── NoOpSiteComm ──────────────────────────────────────────────────────────────

func TestNoOpSiteComm_ExpireToken(t *testing.T) {
	var sc NoOpSiteComm
	// Must not panic and satisfies SiteCommInterface
	sc.ExpireToken(TorrentID(1), UserID(2))
}

func TestNoOpSiteComm_ImplementsInterface(t *testing.T) {
	// Compile-time check that *NoOpSiteComm satisfies SiteCommInterface
	var _ SiteCommInterface = (*NoOpSiteComm)(nil)
}
