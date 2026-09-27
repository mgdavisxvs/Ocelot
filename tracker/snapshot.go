package tracker

import (
	"context"
	"encoding/binary"
	"encoding/gob"
	"fmt"
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

// snapshotMagic is a 4-byte file identifier written at the start of every
// snapshot file.  A mismatch on load causes an immediate error rather than a
// confusing gob decode failure.
// RULING-09 / F-10 mitigation: version-tagged snapshot format.
var snapshotMagic = [4]byte{'O', 'C', 'L', 'T'}

// snapshotVersion is incremented whenever the on-disk layout changes.
const snapshotVersion uint32 = 2

// snapshotHeader is the binary header prepended to every snapshot file.
type snapshotHeader struct {
	Magic   [4]byte
	Version uint32
	SavedAt int64 // unix timestamp (seconds)
}

// SaveSnapshot serialises the live swarm state to path using an atomic
// write (tmp-then-rename) so the file is never partially written.
// A binary header (magic + version + timestamp) is prepended before the
// gob-encoded payload.
//
// RULING-09 / F-10 mitigation: atomic write + version header.
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

	// Write to a temp file on the same filesystem, then atomically rename.
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("snapshot: create tmp: %w", err)
	}

	hdr := snapshotHeader{
		Magic:   snapshotMagic,
		Version: snapshotVersion,
		SavedAt: time.Now().Unix(),
	}
	if err := binary.Write(f, binary.LittleEndian, hdr); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("snapshot: write header: %w", err)
	}
	if err := gob.NewEncoder(f).Encode(snap); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("snapshot: encode: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("snapshot: sync: %w", err)
	}
	f.Close()
	return os.Rename(tmp, path)
}

// LoadSnapshot deserialises a previous snapshot and merges live peers into
// torrents that are already registered in the TorrentList.  Torrents that are
// not in the list (removed since last run) are silently skipped.
//
// The file header is validated before decoding; a mismatched magic or
// unsupported version returns an error so the caller can log and start clean.
func LoadSnapshot(path string, torrents *TorrentList) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no snapshot yet — silent no-op
		}
		return err
	}
	defer f.Close()

	// Validate header.
	var hdr snapshotHeader
	if err := binary.Read(f, binary.LittleEndian, &hdr); err != nil {
		return fmt.Errorf("snapshot: read header: %w", err)
	}
	if hdr.Magic != snapshotMagic {
		return fmt.Errorf("snapshot: invalid magic %v (not an Ocelot snapshot?)", hdr.Magic)
	}
	if hdr.Version != snapshotVersion {
		return fmt.Errorf("snapshot: unsupported version %d (want %d)", hdr.Version, snapshotVersion)
	}

	var snap peerSnapshot
	if err := gob.NewDecoder(f).Decode(&snap); err != nil {
		return fmt.Errorf("snapshot: decode: %w", err)
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

// ReconcileSnapshotWithDB removes any peer from the in-memory swarm whose
// UserID has been banned or deleted in the database since the snapshot was
// saved.  Must be called after LoadSnapshot and before serving traffic.
//
// RULING-10 / F-10 mitigation: snapshot/DB consistency sweep on startup.
func ReconcileSnapshotWithDB(torrents *TorrentList, db DatabaseInterface) {
	// Load the current set of known user IDs from the DB.
	rows, err := db.LoadUsers()
	if err != nil {
		return // best-effort; DB unavailable — skip reconciliation
	}
	validUsers := make(map[UserID]bool, len(rows))
	for _, r := range rows {
		validUsers[r.id] = true
	}

	torrents.ForEach(func(_ string, t *Torrent) bool {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.Seeders.ForEach(func(key string, p *Peer) bool {
			if !validUsers[p.UserID] {
				t.Seeders.Delete(key)
			}
			return true
		})
		t.Leechers.ForEach(func(key string, p *Peer) bool {
			if !validUsers[p.UserID] {
				t.Leechers.Delete(key)
			}
			return true
		})
		return true
	})
}

// StartPeriodicSnapshot launches a background goroutine that saves a snapshot
// of the current swarm state every interval.  This bounds data loss on an
// unclean shutdown to at most one interval (rather than the full uptime).
//
// RULING-10 / F-10 mitigation: not only saving on shutdown but also periodically.
func StartPeriodicSnapshot(ctx context.Context, path string, torrents *TorrentList, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := SaveSnapshot(path, torrents); err != nil {
					GetDefaultLogger().Warn("periodic snapshot failed", "error", err.Error())
				}
			case <-ctx.Done():
				return
			}
		}
	}()
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
