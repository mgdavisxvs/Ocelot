package tracker

import "fmt"

// Loader restores in-memory state from the SQLite database on startup
// and performs merge-reloads on SIGUSR1 without wiping live peer lists.
type Loader struct {
	db        *SQLiteShardManager
	torrents  *TorrentList
	users     *UserList
	whitelist *Whitelist
}

func NewLoader(db *SQLiteShardManager, torrents *TorrentList, users *UserList, wl *Whitelist) *Loader {
	return &Loader{db: db, torrents: torrents, users: users, whitelist: wl}
}

// CreateSchemaIfNeeded is a no-op: schema is initialized in NewSQLiteShardManager.
func (l *Loader) CreateSchemaIfNeeded() error { return nil }

// LoadAll populates all in-memory maps from DB. Call once at startup.
func (l *Loader) LoadAll() error {
	if err := l.loadTorrents(); err != nil {
		return fmt.Errorf("load torrents: %w", err)
	}
	if err := l.loadUsers(); err != nil {
		return fmt.Errorf("load users: %w", err)
	}
	if err := l.loadWhitelist(); err != nil {
		return fmt.Errorf("load whitelist: %w", err)
	}
	if err := l.loadTokens(); err != nil {
		return fmt.Errorf("load tokens: %w", err)
	}
	return nil
}

func (l *Loader) loadTorrents() error {
	rows, err := l.db.LoadTorrents()
	if err != nil {
		return err
	}
	for _, r := range rows {
		t := NewTorrent(r.id)
		t.Completed = r.completed
		t.Balance = r.balance
		t.FreeType = r.freeType
		l.torrents.Set(r.infoHash, t)
	}
	return nil
}

func (l *Loader) loadUsers() error {
	rows, err := l.db.LoadUsers()
	if err != nil {
		return err
	}
	for _, r := range rows {
		u := NewUser(r.id, r.canLeech, r.protectIP)
		l.users.Set(r.passkey, u)
	}
	return nil
}

func (l *Loader) loadWhitelist() error {
	prefixes, err := l.db.LoadWhitelist()
	if err != nil {
		return err
	}
	l.whitelist.Reset(prefixes)
	return nil
}

func (l *Loader) loadTokens() error {
	tokens, err := l.db.LoadTokens()
	if err != nil {
		return err
	}
	for infoHash, userIDs := range tokens {
		t, ok := l.torrents.Get(infoHash)
		if !ok {
			continue
		}
		t.mu.Lock()
		for _, uid := range userIDs {
			t.TokenedUsers[uid] = struct{}{}
		}
		t.mu.Unlock()
	}
	return nil
}

// Reload performs a merge-reload: updates metadata on existing torrents and users,
// inserts new ones, and replaces the whitelist — without touching live peer lists.
// Safe to call concurrently via SIGUSR1 handler.
func (l *Loader) Reload() error {
	// ── Torrents ──────────────────────────────────────────────────────────────
	tRows, err := l.db.LoadTorrents()
	if err != nil {
		return fmt.Errorf("reload torrents: %w", err)
	}
	for _, r := range tRows {
		if existing, ok := l.torrents.Get(r.infoHash); ok {
			existing.mu.Lock()
			existing.Completed = r.completed
			existing.Balance = r.balance
			existing.FreeType = r.freeType
			existing.mu.Unlock()
		} else {
			t := NewTorrent(r.id)
			t.Completed = r.completed
			t.Balance = r.balance
			t.FreeType = r.freeType
			l.torrents.Set(r.infoHash, t)
		}
	}

	// ── Users ─────────────────────────────────────────────────────────────────
	uRows, err := l.db.LoadUsers()
	if err != nil {
		return fmt.Errorf("reload users: %w", err)
	}
	for _, r := range uRows {
		// Users are keyed by passkey; we don't have a reverse passkey→user scan
		// so build a fresh user for new entries and update atomics for existing.
		// The user passkey itself acts as the lookup key, so a full scan via
		// ForEach would be O(n); instead we re-set — CanLeech/ProtectIP are
		// the only mutable fields we reload.
		if existing, ok := l.users.Get(r.passkey); ok {
			existing.CanLeech.Store(r.canLeech)
			existing.ProtectIP.Store(r.protectIP)
		} else {
			l.users.Set(r.passkey, NewUser(r.id, r.canLeech, r.protectIP))
		}
	}

	// ── Whitelist (full replace) ───────────────────────────────────────────────
	if err := l.loadWhitelist(); err != nil {
		return fmt.Errorf("reload whitelist: %w", err)
	}

	// ── Tokens (merge) ────────────────────────────────────────────────────────
	if err := l.loadTokens(); err != nil {
		return fmt.Errorf("reload tokens: %w", err)
	}

	return nil
}
