package tracker

import (
	"sync"
	"time"
)

// ── MockDB ────────────────────────────────────────────────────────────────────

type recordedPeer struct {
	UserID, TorrentID                                    uint32
	Active                                               int
	Uploaded, Downloaded, UpSpeed, DownSpeed, Left, Corrupt int64
	AnnounceTime, Announces                              uint32
	IP, PeerID, UserAgent                                string
}

type recordedPeerLight struct {
	UserID, TorrentID   uint32
	AnnounceTime, Announces uint32
	PeerID              string
}

type recordedUserStats struct {
	UserID             uint32
	Uploaded, Downloaded int64
}

type recordedTorrent struct {
	TorrentID              uint32
	Seeders, Leechers      uint32
	Snatched               int
	Balance                int64
}

type recordedSnatch struct {
	UserID, TorrentID uint32
	T                 time.Time
	IP                string
}

type recordedToken struct {
	UserID, TorrentID uint32
	Downloaded        int64
}

// MockDB implements DatabaseInterface, recording all calls for test assertions.
type MockDB struct {
	mu sync.Mutex

	Peers      []recordedPeer
	PeersLight []recordedPeerLight
	UserStats  []recordedUserStats
	Torrents   []recordedTorrent
	Snatches   []recordedSnatch
	Tokens     []recordedToken

	TorrentHashes  []string
	UserPasskeys   []string
	WhitelistAdded []string
	WhitelistRemoved []string

	CheckpointCount int
	RotationCount   int
	CloseCount      int

	// Configurable returns for load methods
	TorrentRows  []torrentLoadRow
	UserRows     []userLoadRow
	WhitelistRows []string
	TokenMapData map[string][]UserID

	ReturnErr error // if non-nil, all mutating calls return this
}

func newMockDB() *MockDB { return &MockDB{} }

func (m *MockDB) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Peers = nil
	m.PeersLight = nil
	m.UserStats = nil
	m.Torrents = nil
	m.Snatches = nil
	m.Tokens = nil
	m.TorrentHashes = nil
	m.UserPasskeys = nil
	m.WhitelistAdded = nil
	m.WhitelistRemoved = nil
	m.CheckpointCount = 0
	m.RotationCount = 0
	m.CloseCount = 0
}

func (m *MockDB) RecordPeer(userID UserID, torrentID TorrentID, active int,
	uploaded, downloaded, upSpeed, downSpeed, left, corrupt int64,
	announceTime, announces uint32, ip, peerID, userAgent string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ReturnErr != nil {
		return m.ReturnErr
	}
	m.Peers = append(m.Peers, recordedPeer{
		UserID: uint32(userID), TorrentID: uint32(torrentID), Active: active,
		Uploaded: uploaded, Downloaded: downloaded, UpSpeed: upSpeed,
		DownSpeed: downSpeed, Left: left, Corrupt: corrupt,
		AnnounceTime: announceTime, Announces: announces,
		IP: ip, PeerID: peerID, UserAgent: userAgent,
	})
	return nil
}

func (m *MockDB) RecordPeerLight(userID UserID, torrentID TorrentID, announceTime, announces uint32, peerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ReturnErr != nil {
		return m.ReturnErr
	}
	m.PeersLight = append(m.PeersLight, recordedPeerLight{
		UserID: uint32(userID), TorrentID: uint32(torrentID),
		AnnounceTime: announceTime, Announces: announces, PeerID: peerID,
	})
	return nil
}

func (m *MockDB) RecordUserStats(userID UserID, uploaded, downloaded int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ReturnErr != nil {
		return m.ReturnErr
	}
	m.UserStats = append(m.UserStats, recordedUserStats{
		UserID: uint32(userID), Uploaded: uploaded, Downloaded: downloaded,
	})
	return nil
}

func (m *MockDB) RecordTorrent(torrentID TorrentID, seeders, leechers uint32, snatched int, balance int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ReturnErr != nil {
		return m.ReturnErr
	}
	m.Torrents = append(m.Torrents, recordedTorrent{
		TorrentID: uint32(torrentID), Seeders: seeders, Leechers: leechers,
		Snatched: snatched, Balance: balance,
	})
	return nil
}

func (m *MockDB) RecordSnatch(userID UserID, torrentID TorrentID, t time.Time, ip string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ReturnErr != nil {
		return m.ReturnErr
	}
	m.Snatches = append(m.Snatches, recordedSnatch{
		UserID: uint32(userID), TorrentID: uint32(torrentID), T: t, IP: ip,
	})
	return nil
}

func (m *MockDB) RecordToken(userID UserID, torrentID TorrentID, downloaded int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ReturnErr != nil {
		return m.ReturnErr
	}
	m.Tokens = append(m.Tokens, recordedToken{
		UserID: uint32(userID), TorrentID: uint32(torrentID), Downloaded: downloaded,
	})
	return nil
}

func (m *MockDB) RecordTorrentHash(id TorrentID, infoHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ReturnErr != nil {
		return m.ReturnErr
	}
	m.TorrentHashes = append(m.TorrentHashes, infoHash)
	return nil
}

func (m *MockDB) RecordUserPasskey(id UserID, passkey string, canLeech, protectIP bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ReturnErr != nil {
		return m.ReturnErr
	}
	m.UserPasskeys = append(m.UserPasskeys, passkey)
	return nil
}

func (m *MockDB) AddWhitelistEntry(prefix string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ReturnErr != nil {
		return m.ReturnErr
	}
	m.WhitelistAdded = append(m.WhitelistAdded, prefix)
	return nil
}

func (m *MockDB) RemoveWhitelistEntry(prefix string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ReturnErr != nil {
		return m.ReturnErr
	}
	m.WhitelistRemoved = append(m.WhitelistRemoved, prefix)
	return nil
}

func (m *MockDB) LoadTorrents() ([]torrentLoadRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.TorrentRows, m.ReturnErr
}

func (m *MockDB) LoadUsers() ([]userLoadRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.UserRows, m.ReturnErr
}

func (m *MockDB) LoadWhitelist() ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.WhitelistRows, m.ReturnErr
}

func (m *MockDB) LoadTokens() (map[string][]UserID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.TokenMapData != nil {
		return m.TokenMapData, m.ReturnErr
	}
	return make(map[string][]UserID), m.ReturnErr
}

func (m *MockDB) CheckpointWAL() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CheckpointCount++
	return m.ReturnErr
}

func (m *MockDB) CheckRotation() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RotationCount++
	return m.ReturnErr
}

func (m *MockDB) DeleteToken(userID UserID, torrentID TorrentID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ReturnErr
}

func (m *MockDB) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CloseCount++
	return m.ReturnErr
}

// ── MockSiteComm ──────────────────────────────────────────────────────────────

type tokenExpiry struct {
	TorrentID UserID
	UserID    UserID
}

// MockSiteComm implements SiteCommInterface, recording ExpireToken calls.
type MockSiteComm struct {
	mu      sync.Mutex
	Expired []tokenExpiry
}

func newMockSiteComm() *MockSiteComm { return &MockSiteComm{} }

func (m *MockSiteComm) ExpireToken(torrentID TorrentID, userID UserID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Expired = append(m.Expired, tokenExpiry{TorrentID: UserID(torrentID), UserID: userID})
}

func (m *MockSiteComm) BanUser(_ int64) error                          { return nil }
func (m *MockSiteComm) UnbanUser(_ int64) error                        { return nil }
func (m *MockSiteComm) NotifyFreeleech(_ int64, _ int) error           { return nil }
func (m *MockSiteComm) ReportAnomaly(_ int64, _ float64) error         { return nil }
func (m *MockSiteComm) UpdateStats(_ int64, _ int64, _ int64) error    { return nil }

func (m *MockSiteComm) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Expired = nil
}
