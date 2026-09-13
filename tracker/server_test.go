package tracker

import (
	"testing"
	"time"
)

// Note: These tests are simplified stubs. Full integration tests would require
// the actual database backend and complete server setup.

func TestConfigCreation(t *testing.T) {
	config := &Config{
		ListenAddr:       ":34000",
		AnnounceInterval: 1800,
		PeersTimeout:     7200,
		MaxMiddlemen:     10000,
		NumWantLimit:     50,
		KeepaliveTimeout: 60 * time.Second,
	}

	if config.ListenAddr != ":34000" {
		t.Errorf("Expected :34000, got %s", config.ListenAddr)
	}

	if config.AnnounceInterval != 1800 {
		t.Errorf("Expected interval 1800, got %d", config.AnnounceInterval)
	}
}

func TestTorrentCreation(t *testing.T) {
	torrent := NewTorrent(TorrentID(1))

	if torrent == nil {
		t.Fatal("Torrent creation failed")
	}

	if torrent.ID != TorrentID(1) {
		t.Errorf("Expected ID 1, got %d", torrent.ID)
	}

	if torrent.Seeders == nil {
		t.Error("Seeders list should not be nil")
	}

	if torrent.Leechers == nil {
		t.Error("Leechers list should not be nil")
	}
}

func TestPeerCreation(t *testing.T) {
	peer := &Peer{
		UserID:     UserID(1),
		Port:       6881,
		Uploaded:   0,
		Downloaded: 0,
		Left:       1073741824, // 1GB
		Visible:    true,
	}

	if peer.UserID != UserID(1) {
		t.Errorf("Expected UserID 1, got %d", peer.UserID)
	}

	if peer.Port != 6881 {
		t.Errorf("Expected port 6881, got %d", peer.Port)
	}
}
