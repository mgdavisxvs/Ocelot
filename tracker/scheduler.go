package tracker

import (
	"strings"
	"time"
)

// PurgeExpiredTorrents removes torrents whose TTLSeconds has elapsed from the
// in-memory TorrentList. Called periodically from the main UC cleanup goroutine.
func PurgeExpiredTorrents(_ interface{}, torrents *TorrentList) {
	now := time.Now()
	var expired []string
	torrents.ForEach(func(hash string, t *Torrent) bool {
		if t.TTLSeconds > 0 && !t.CreatedAt.IsZero() &&
			now.Sub(t.CreatedAt) > time.Duration(t.TTLSeconds)*time.Second {
			expired = append(expired, hash)
		}
		return true
	})
	for _, h := range expired {
		torrents.Delete(h)
	}
}

// isNoTableError returns true when err indicates the table does not exist yet.
func isNoTableError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "no such table")
}
