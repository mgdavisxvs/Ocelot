package tracker

import "time"

// CircuitBreakerDB wraps a DatabaseInterface and executes every call through
// a CircuitBreaker. When the breaker trips (consecutive DB failures), non-read
// writes return ErrCircuitOpen immediately, shedding load until the DB recovers.
type CircuitBreakerDB struct {
	db DatabaseInterface
	cb *CircuitBreaker
}

// NewCircuitBreakerDB wraps inner with circuit-breaker protection.
func NewCircuitBreakerDB(inner DatabaseInterface, cfg CircuitBreakerConfig) *CircuitBreakerDB {
	if cfg.Name == "" {
		cfg.Name = "db"
	}
	return &CircuitBreakerDB{db: inner, cb: NewCircuitBreaker(cfg)}
}

func (c *CircuitBreakerDB) RecordPeer(userID UserID, torrentID TorrentID, active int, uploaded, downloaded, upSpeed, downSpeed, left, corrupt int64, announceTime, announces uint32, ip, peerID, userAgent string) error {
	return c.cb.Execute(func() error {
		return c.db.RecordPeer(userID, torrentID, active, uploaded, downloaded, upSpeed, downSpeed, left, corrupt, announceTime, announces, ip, peerID, userAgent)
	})
}

func (c *CircuitBreakerDB) RecordPeerLight(userID UserID, torrentID TorrentID, announceTime, announces uint32, peerID string) error {
	return c.cb.Execute(func() error {
		return c.db.RecordPeerLight(userID, torrentID, announceTime, announces, peerID)
	})
}

func (c *CircuitBreakerDB) RecordUserStats(userID UserID, uploaded, downloaded int64) error {
	return c.cb.Execute(func() error {
		return c.db.RecordUserStats(userID, uploaded, downloaded)
	})
}

func (c *CircuitBreakerDB) RecordTorrent(torrentID TorrentID, seeders, leechers uint32, snatched int, balance int64) error {
	return c.cb.Execute(func() error {
		return c.db.RecordTorrent(torrentID, seeders, leechers, snatched, balance)
	})
}

func (c *CircuitBreakerDB) RecordSnatch(userID UserID, torrentID TorrentID, t time.Time, ip string) error {
	return c.cb.Execute(func() error {
		return c.db.RecordSnatch(userID, torrentID, t, ip)
	})
}

func (c *CircuitBreakerDB) RecordToken(userID UserID, torrentID TorrentID, downloaded int64) error {
	return c.cb.Execute(func() error {
		return c.db.RecordToken(userID, torrentID, downloaded)
	})
}

func (c *CircuitBreakerDB) RecordTorrentHash(id TorrentID, infoHash string) error {
	return c.cb.Execute(func() error {
		return c.db.RecordTorrentHash(id, infoHash)
	})
}

func (c *CircuitBreakerDB) RecordUserPasskey(id UserID, passkey string, canLeech, protectIP bool) error {
	return c.cb.Execute(func() error {
		return c.db.RecordUserPasskey(id, passkey, canLeech, protectIP)
	})
}

func (c *CircuitBreakerDB) AddWhitelistEntry(prefix string) error {
	return c.cb.Execute(func() error {
		return c.db.AddWhitelistEntry(prefix)
	})
}

func (c *CircuitBreakerDB) RemoveWhitelistEntry(prefix string) error {
	return c.cb.Execute(func() error {
		return c.db.RemoveWhitelistEntry(prefix)
	})
}

func (c *CircuitBreakerDB) LoadTorrents() ([]torrentLoadRow, error) {
	var result []torrentLoadRow
	err := c.cb.Execute(func() error {
		var e error
		result, e = c.db.LoadTorrents()
		return e
	})
	return result, err
}

func (c *CircuitBreakerDB) LoadUsers() ([]userLoadRow, error) {
	var result []userLoadRow
	err := c.cb.Execute(func() error {
		var e error
		result, e = c.db.LoadUsers()
		return e
	})
	return result, err
}

func (c *CircuitBreakerDB) LoadWhitelist() ([]string, error) {
	var result []string
	err := c.cb.Execute(func() error {
		var e error
		result, e = c.db.LoadWhitelist()
		return e
	})
	return result, err
}

func (c *CircuitBreakerDB) LoadTokens() (map[string][]UserID, error) {
	var result map[string][]UserID
	err := c.cb.Execute(func() error {
		var e error
		result, e = c.db.LoadTokens()
		return e
	})
	return result, err
}

func (c *CircuitBreakerDB) CheckpointWAL() error {
	return c.cb.Execute(func() error {
		return c.db.CheckpointWAL()
	})
}

func (c *CircuitBreakerDB) CheckRotation() error {
	return c.cb.Execute(func() error {
		return c.db.CheckRotation()
	})
}

// Close bypasses the circuit breaker — always attempt DB cleanup.
func (c *CircuitBreakerDB) Close() error {
	return c.db.Close()
}

// CBState returns the circuit breaker's current state string for monitoring.
func (c *CircuitBreakerDB) CBState() string {
	return c.cb.GetStateString()
}
