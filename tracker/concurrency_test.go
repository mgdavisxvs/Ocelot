package tracker

import (
	"fmt"
	"net"
	"sync"
	"testing"
)

// TestConcurrentAnnounce fires 8 goroutines each doing 100 announces on the
// same torrent.  Run with -race to verify absence of data races.
func TestConcurrentAnnounce(t *testing.T) {
	const goroutines = 8
	const announcesPerGoroutine = 100

	f := newTestFixture()

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()

			peerID := fmt.Sprintf("peer%04d0000000000000000", g)
			if len(peerID) < 20 {
				peerID = peerID + string(make([]byte, 20-len(peerID)))
			}
			peerID = peerID[:20]

			ip := net.ParseIP(testIP)
			user, _ := f.worker.Users.Get(testPasskey)

			for i := 0; i < announcesPerGoroutine; i++ {
				req := newAnnounceReqFull(testInfoHash, peerID, "started", 0, 0, 1000)
				if i > 0 {
					req.Event = ""
					req.Uploaded = int64(i) * 1024
					req.Downloaded = int64(i) * 512
				}
				f.worker.Announce(req, user, ip, "test-client")
			}
		}()
	}

	wg.Wait()

	// Torrent should have at most goroutines seeders+leechers (peers may have
	// been deduplicated by peerKey).
	tor, ok := f.worker.Torrents.Get(testInfoHash)
	if !ok {
		t.Fatal("torrent disappeared")
	}
	total := tor.Seeders.Size() + tor.Leechers.Size()
	if total > goroutines {
		t.Errorf("peer count %d exceeds goroutine count %d", total, goroutines)
	}
}

// TestConcurrentTorrentListAccess exercises concurrent reads and writes to
// TorrentList (add/get/delete) to surface map concurrency bugs.
func TestConcurrentTorrentListAccess(t *testing.T) {
	tl := NewTorrentList()
	const n = 50

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		i := i
		wg.Add(3)
		go func() {
			defer wg.Done()
			hash := fmt.Sprintf("hash%040d", i)
			tl.Set(hash, NewTorrent(TorrentID(i)))
		}()
		go func() {
			defer wg.Done()
			hash := fmt.Sprintf("hash%040d", i)
			tl.Get(hash)
		}()
		go func() {
			defer wg.Done()
			tl.Size()
		}()
	}
	wg.Wait()
}

// TestConcurrentUserListAccess exercises concurrent reads and writes to UserList.
func TestConcurrentUserListAccess(t *testing.T) {
	ul := NewUserList()
	const n = 50

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		i := i
		wg.Add(3)
		go func() {
			defer wg.Done()
			pk := fmt.Sprintf("passkey%033d", i)
			ul.Set(pk, NewUser(UserID(i), true, false))
		}()
		go func() {
			defer wg.Done()
			pk := fmt.Sprintf("passkey%033d", i)
			ul.Get(pk)
		}()
		go func() {
			defer wg.Done()
			ul.Size()
		}()
	}
	wg.Wait()
}

// TestConcurrentWhitelistCheck exercises concurrent Whitelist reads/writes.
func TestConcurrentWhitelistCheck(t *testing.T) {
	wl := NewWhitelist()
	wl.Add("-qB")
	wl.Add("-TR")

	var wg sync.WaitGroup
	const n = 100
	for i := 0; i < n; i++ {
		i := i
		wg.Add(2)
		go func() {
			defer wg.Done()
			peerID := []byte(fmt.Sprintf("-qB%017d", i))
			wl.IsAllowed(peerID)
		}()
		go func() {
			defer wg.Done()
			if i%10 == 0 {
				wl.Add("-AZ")
			}
		}()
	}
	wg.Wait()
}

// TestConcurrentStatsCounters verifies atomic stat updates under concurrency.
func TestConcurrentStatsCounters(t *testing.T) {
	f := newTestFixture()
	const n = 200

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			f.worker.Stats.Requests.Add(1)
			f.worker.Stats.Announcements.Add(1)
		}()
	}
	wg.Wait()

	if f.worker.Stats.Requests.Load() != n {
		t.Errorf("Requests = %d, want %d", f.worker.Stats.Requests.Load(), n)
	}
}
