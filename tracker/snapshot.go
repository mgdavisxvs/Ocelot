package tracker

import (
	"encoding/gob"
	"net"
	"os"
	"time"
)

// snapshotPeer is the gob-serialisable DTO for a single peer.
type snapshotPeer struct {
	UserID          uint32
	IP              net.IP
	Port            uint16
	Uploaded        int64
	Downloaded      int64
	Left            int64
	Corrupt         int64
	FirstAnnounced  time.Time
	LastAnnounced   time.Time
	Announces       uint32
	Visible         bool
	Seeder          bool
	ConnectionTimes []time.Time
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

// SaveSnapshot serialises the live swarm state to path using gob encoding.
// Called on graceful shutdown so the next startup can skip the cold-start
// peer-list build period.
func SaveSnapshot(path string, torrents *TorrentList) error {
	snap := &peerSnapshot{
		Torrents: make(map[string]*snapshotTorrent),
	}

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
		}
		return true
	})

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return gob.NewEncoder(f).Encode(snap)
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
		UserID:          uint32(p.UserID),
		IP:              p.IP,
		Port:            p.Port,
		Uploaded:        p.Uploaded,
		Downloaded:      p.Downloaded,
		Left:            p.Left,
		Corrupt:         p.Corrupt,
		FirstAnnounced:  p.FirstAnnounced,
		LastAnnounced:   p.LastAnnounced,
		Announces:       p.Announces,
		Visible:         p.Visible,
		Seeder:          seeder,
		ConnectionTimes: p.ConnectionTimes,
	}
}

func snapToPeer(sp snapshotPeer) *Peer {
	p := &Peer{
		UserID:          UserID(sp.UserID),
		IP:              sp.IP,
		Port:            sp.Port,
		Uploaded:        sp.Uploaded,
		Downloaded:      sp.Downloaded,
		Left:            sp.Left,
		Corrupt:         sp.Corrupt,
		FirstAnnounced:  sp.FirstAnnounced,
		LastAnnounced:   sp.LastAnnounced,
		Announces:       sp.Announces,
		Visible:         sp.Visible,
		ConnectionTimes: sp.ConnectionTimes,
	}
	if sp.IP != nil {
		p.IPPort = CompactIPPort(sp.IP, sp.Port)
	}
	return p
}
