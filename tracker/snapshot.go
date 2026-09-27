package tracker

import (
	"encoding/gob"
	"encoding/json"
	"net"
	"os"
	"time"
)

// snapshotPeer stores only peer presence (IP/port/timestamps). Counters
// (Uploaded/Downloaded/Left/Corrupt) are excluded: they belong to the
// authoritative database record and must not be merged from a potentially
// stale snapshot (D-T4 merge-strategy fix).
type snapshotPeer struct {
	UserID         uint32
	IP             net.IP
	Port           uint16
	FirstAnnounced time.Time
	LastAnnounced  time.Time
	Announces      uint32
	Visible        bool
	Seeder         bool
}

// snapshotManifest records metadata alongside the snapshot file so that
// startup code can validate freshness before applying the snapshot (D-G5).
type snapshotManifest struct {
	CreatedAt   time.Time `json:"created_at"`
	TorrentCount int      `json:"torrent_count"`
	PeerCount   int       `json:"peer_count"`
	SnapshotFile string   `json:"snapshot_file"`
}

type snapshotEntry struct {
	Key  string
	Peer snapshotPeer
}

type snapshotTorrent struct {
	Peers []snapshotEntry
}

type peerSnapshot struct {
	Torrents map[string]*snapshotTorrent // info_hash → torrent peer state
}

// SaveSnapshot serialises the live swarm state to path using gob encoding
// and writes a JSON manifest file at path+".manifest" recording metadata
// for startup validation (D-G5).
func SaveSnapshot(path string, torrents *TorrentList) error {
	snap := &peerSnapshot{
		Torrents: make(map[string]*snapshotTorrent),
	}
	totalPeers := 0

	torrents.ForEach(func(hash string, t *Torrent) bool {
		st := &snapshotTorrent{}
		t.Seeders.ForEach(func(key string, p *Peer) bool {
			st.Peers = append(st.Peers, snapshotEntry{Key: key, Peer: peerToSnap(p, true)})
			return true
		})
		t.Leechers.ForEach(func(key string, p *Peer) bool {
			st.Peers = append(st.Peers, snapshotEntry{Key: key, Peer: peerToSnap(p, false)})
			return true
		})
		if len(st.Peers) > 0 {
			snap.Torrents[hash] = st
			totalPeers += len(st.Peers)
		}
		return true
	})

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := gob.NewEncoder(f).Encode(snap); err != nil {
		f.Close()
		return err
	}
	f.Close()

	manifest := snapshotManifest{
		CreatedAt:    time.Now().UTC(),
		TorrentCount: len(snap.Torrents),
		PeerCount:    totalPeers,
		SnapshotFile: path,
	}
	mf, err := os.Create(path + ".manifest")
	if err != nil {
		return err
	}
	defer mf.Close()
	return json.NewEncoder(mf).Encode(manifest)
}

// LoadSnapshot deserialises a previous snapshot and merges live peers into
// torrents that are already registered in the TorrentList.  Torrents that are
// not in the list are silently skipped (they may have been removed since the
// last run).
func LoadSnapshot(path string, torrents *TorrentList) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no snapshot yet — silent no-op
		}
		return err
	}
	defer f.Close()

	var snap peerSnapshot
	if err := gob.NewDecoder(f).Decode(&snap); err != nil {
		return err
	}

	for hash, st := range snap.Torrents {
		tor, ok := torrents.Get(hash)
		if !ok {
			continue
		}
		tor.mu.Lock()
		for _, entry := range st.Peers {
			p := snapToPeer(entry.Peer)
			if entry.Peer.Seeder {
				tor.Seeders.Set(entry.Key, p)
			} else {
				tor.Leechers.Set(entry.Key, p)
			}
		}
		tor.mu.Unlock()
	}
	return nil
}

func peerToSnap(p *Peer, seeder bool) snapshotPeer {
	return snapshotPeer{
		UserID:         uint32(p.UserID),
		IP:             p.IP,
		Port:           p.Port,
		FirstAnnounced: p.FirstAnnounced,
		LastAnnounced:  p.LastAnnounced,
		Announces:      p.Announces,
		Visible:        p.Visible,
		Seeder:         seeder,
	}
}

func snapToPeer(sp snapshotPeer) *Peer {
	p := &Peer{
		UserID:         UserID(sp.UserID),
		IP:             sp.IP,
		Port:           sp.Port,
		FirstAnnounced: sp.FirstAnnounced,
		LastAnnounced:  sp.LastAnnounced,
		Announces:      sp.Announces,
		Visible:        sp.Visible,
	}
	if sp.IP != nil {
		p.IPPort = CompactIPPort(sp.IP, sp.Port)
	}
	return p
}
