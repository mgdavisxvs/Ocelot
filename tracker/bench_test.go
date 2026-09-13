package tracker

import (
	"fmt"
	"net"
	"testing"
)

// ── Announce hot-path benchmarks ──────────────────────────────────────────────

func BenchmarkAnnounce_Leecher(b *testing.B) {
	w, _, _ := newTestWorker()
	tor := NewTorrent(1)
	w.Torrents.Set("benchhash000000000001", tor)

	ip := net.ParseIP("10.0.0.1")
	peerID := make([]byte, 20)
	copy(peerID, "-qB4bench00000000000")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		u := NewUser(UserID(i+1), true, false)
		req := &AnnounceRequest{
			InfoHash:   "benchhash000000000001",
			PeerID:     peerID,
			Port:       6881,
			Uploaded:   int64(i * 1024),
			Downloaded: int64(i * 512),
			Left:       10000,
			Compact:    true,
			Event:      "started",
			NumWant:    50,
		}
		_, _ = w.Announce(req, u, ip, "-qB4bench")
	}
}

func BenchmarkAnnounce_Seeder(b *testing.B) {
	w, _, _ := newTestWorker()
	tor := NewTorrent(1)
	w.Torrents.Set("benchhash000000000002", tor)

	ip := net.ParseIP("10.0.0.2")
	peerID := make([]byte, 20)
	copy(peerID, "-qB4bench00000000001")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		u := NewUser(UserID(i+1), true, false)
		req := &AnnounceRequest{
			InfoHash:   "benchhash000000000002",
			PeerID:     peerID,
			Port:       51413,
			Uploaded:   int64(i * 4096),
			Downloaded: 0,
			Left:       0,
			Compact:    true,
			Event:      "started",
			NumWant:    50,
		}
		_, _ = w.Announce(req, u, ip, "-qB4bench")
	}
}

// BenchmarkAnnounce_WithPeers simulates a swarm of N peers to measure
// peer-selection cost at realistic swarm sizes.
func BenchmarkAnnounce_WithPeers(b *testing.B) {
	for _, swarmSize := range []int{10, 100, 500} {
		swarmSize := swarmSize
		b.Run(fmt.Sprintf("swarm%d", swarmSize), func(b *testing.B) {
			w, _, _ := newTestWorker()
			tor := NewTorrent(1)
			infoHash := fmt.Sprintf("benchswarm%010d", swarmSize)
			w.Torrents.Set(infoHash, tor)

			// Pre-populate seeders
			for i := 0; i < swarmSize; i++ {
				p := &Peer{
					UserID:  UserID(i + 1000),
					IP:      net.ParseIP("192.168.1.1"),
					Port:    uint16(6000 + i),
					Visible: true,
				}
				p.IPPort = CompactIPPort(p.IP, p.Port)
				tor.Seeders.Set(fmt.Sprintf("seeder%d", i), p)
			}

			ip := net.ParseIP("10.1.0.1")
			peerID := make([]byte, 20)
			copy(peerID, "-qB4leecher000000000")

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				u := NewUser(UserID(i+1), true, false)
				req := &AnnounceRequest{
					InfoHash: infoHash,
					PeerID:   peerID,
					Port:     6881,
					Left:     1000,
					Compact:  true,
					Event:    "started",
					NumWant:  50,
				}
				_, _ = w.Announce(req, u, ip, "-qB4bench")
			}
		})
	}
}

// ── selectPeers micro-benchmark ───────────────────────────────────────────────

func BenchmarkSelectPeers(b *testing.B) {
	for _, n := range []int{10, 50, 200} {
		n := n
		b.Run(fmt.Sprintf("n%d", n), func(b *testing.B) {
			w, _, _ := newTestWorker()
			tor := NewTorrent(1)
			for i := 0; i < n; i++ {
				p := &Peer{
					UserID:  UserID(i + 1),
					IP:      net.ParseIP("1.2.3.4"),
					Port:    uint16(6000 + i),
					Visible: true,
				}
				p.IPPort = CompactIPPort(p.IP, p.Port)
				tor.Seeders.Set(fmt.Sprintf("k%d", i), p)
			}
			self := &Peer{UserID: 9999}

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = w.selectPeers(tor, self, 9999, 50, true)
			}
		})
	}
}

// ── Bencoding micro-benchmarks ────────────────────────────────────────────────

func BenchmarkBencodeDict_Announce(b *testing.B) {
	peers := make([]byte, 6*50) // 50 compact peers
	m := map[string]string{
		"complete":     BencodeInt(42),
		"incomplete":   BencodeInt(7),
		"interval":     BencodeInt(1800),
		"min interval": BencodeInt(1800),
		"peers":        BencodeString(string(peers)),
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = BencodeDict(m)
	}
}

func BenchmarkBencodeString(b *testing.B) {
	s := string(make([]byte, 300))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = BencodeString(s)
	}
}

// ── PeerList concurrent-access benchmark ─────────────────────────────────────

func BenchmarkPeerList_ConcurrentSetGet(b *testing.B) {
	pl := NewPeerList()
	p := &Peer{UserID: 1, IP: net.ParseIP("1.2.3.4"), Port: 6881}

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("k%d", i%256)
			if i%2 == 0 {
				pl.Set(key, p)
			} else {
				pl.Get(key)
			}
			i++
		}
	})
}

// ── ParseAnnounceParams benchmark ────────────────────────────────────────────

func BenchmarkParseAnnounceParams(b *testing.B) {
	params := buildRawValues()
	ip := net.ParseIP("10.0.0.1")
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = ParseAnnounceParams(params, ip)
	}
}

func buildRawValues() map[string][]string {
	return map[string][]string{
		"info_hash":  {"\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f\x10\x11\x12\x13\x14"},
		"peer_id":    {"-qB40000000000000000"},
		"port":       {"6881"},
		"uploaded":   {"1048576"},
		"downloaded": {"2097152"},
		"left":       {"10000"},
		"compact":    {"1"},
		"event":      {"started"},
		"numwant":    {"50"},
	}
}
