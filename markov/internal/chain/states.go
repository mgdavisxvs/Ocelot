package chain

import "math"

// Peer lifecycle states per torrent swarm.
const (
	PeerLeeching  = 0 // remaining > 0, active announces
	PeerSeeding   = 1 // remaining = 0, uploading to others
	PeerDormant   = 2 // active = 0, within peers_timeout window
	PeerSnatched  = 3 // completed event received (xbt_snatched)
	PeerDead      = 4 // reaped or absent from xbt_files_users
	NumPeerStates = 5
)

// User ratio health bands, derived from users_main.Uploaded/Downloaded.
const (
	UserHealthy    = 0 // ratio >= 0.6
	UserWarning    = 1 // 0.4 <= ratio < 0.6
	UserProbation  = 2 // 0.1 <= ratio < 0.4
	UserBanned     = 3 // can_leech = '0' or ratio < 0.1
	UserFreeleech  = 4 // has active users_freeleeches row
	NumUserStates  = 5
)

// Torrent swarm health bands, derived from torrents.Seeders / Leechers.
const (
	TorrentThriving  = 0 // Seeders >= 10, S/L >= 2.0
	TorrentHealthy   = 1 // Seeders >= 3,  S/L >= 0.5
	TorrentAtRisk    = 2 // Seeders 1–2
	TorrentDying     = 3 // Seeders = 0, Leechers > 0
	TorrentDead      = 4 // Seeders = 0, Leechers = 0 (absorbing)
	NumTorrentStates = 5
)

var PeerStateNames = [NumPeerStates]string{
	"LEECHING", "SEEDING", "DORMANT", "SNATCHED", "DEAD",
}
var UserStateNames = [NumUserStates]string{
	"HEALTHY", "WARNING", "PROBATION", "BANNED", "FREELEECH",
}
var TorrentStateNames = [NumTorrentStates]string{
	"THRIVING", "HEALTHY", "AT_RISK", "DYING", "DEAD",
}

// MaxEntropy returns the theoretical maximum Shannon entropy for n equiprobable states.
func MaxEntropy(n int) float64 {
	return math.Log2(float64(n))
}

// TorrentHealthState computes the health band from seeder/leecher counts.
func TorrentHealthState(seeders, leechers int64) int {
	switch {
	case seeders == 0 && leechers == 0:
		return TorrentDead
	case seeders == 0:
		return TorrentDying
	case seeders <= 2:
		return TorrentAtRisk
	case seeders >= 3 && (leechers == 0 || float64(seeders)/float64(leechers) >= 0.5):
		if seeders >= 10 && (leechers == 0 || float64(seeders)/float64(leechers) >= 2.0) {
			return TorrentThriving
		}
		return TorrentHealthy
	default:
		return TorrentHealthy
	}
}

// UserRatioState computes the ratio band from upload/download totals.
// canLeech = false means ratio-banned; hasFreeleech = true takes priority.
func UserRatioState(uploaded, downloaded int64, canLeech, hasFreeleech bool) int {
	if hasFreeleech {
		return UserFreeleech
	}
	if !canLeech {
		return UserBanned
	}
	if downloaded == 0 {
		if uploaded > 0 {
			return UserHealthy
		}
		return UserWarning // new user, no activity
	}
	ratio := float64(uploaded) / float64(downloaded)
	switch {
	case ratio >= 0.6:
		return UserHealthy
	case ratio >= 0.4:
		return UserWarning
	case ratio >= 0.1:
		return UserProbation
	default:
		return UserBanned
	}
}

// PeerActivityState determines peer state from xbt_files_users fields.
// nowUnix is the current Unix timestamp; peersTimeout is ocelot's peers_timeout.
func PeerActivityState(active bool, remaining int64, mtime, nowUnix, peersTimeout int64) int {
	if active {
		if remaining == 0 {
			return PeerSeeding
		}
		return PeerLeeching
	}
	if nowUnix-mtime < peersTimeout {
		return PeerDormant
	}
	return PeerDead
}
