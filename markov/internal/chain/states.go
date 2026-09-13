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

// User ratio bands — pure accounting state from upload/download ratio.
//
// UMM-01: These states model ONLY ratio dynamics. Account status (enabled/
// disabled) and accounting mode (normal/freeleech/tokened) are orthogonal
// dimensions tracked as per-observation metadata and MUST NOT pollute the
// chain state. Conflating policy outcomes (Banned/Disabled) or accounting
// regimes (Freeleech) with ratio dynamics produces a chain that partially
// models administrative decisions rather than actual user behavior, which
// corrupts every forecast and anomaly score derived from it.
const (
	UserSurplus       = 0 // ratio >= 2.0 (strong contributor)
	UserHealthy       = 1 // 0.6 <= ratio < 2.0
	UserMarginal      = 2 // 0.3 <= ratio < 0.6
	UserDeficit       = 3 // 0.1 <= ratio < 0.3
	UserSevereDeficit = 4 // ratio < 0.1 or no download data
	NumUserStates     = 5
)

// UserAccountingMode classifies the accounting regime active for a user.
// This is orthogonal to ratio state — it is observation metadata, not a
// Markov chain state.
type UserAccountingMode int

const (
	AccountingNormal    UserAccountingMode = iota // standard ratio tracking
	AccountingFreeleech                           // users_freeleeches row active
	AccountingTokened                             // per-torrent freeleech token
)

// Torrent swarm health bands.
//
// UMM-03: TorrentUnavailable (state 4, seeders=0 AND leechers=0) is NOT an
// absorbing state. A torrent is "unavailable" when no complete source is
// observed, but it CAN revive when a seeder re-announces. The absorbing-state
// property applies only to models where revival is definitionally impossible
// (e.g. administrative termination). Operators who need a hard terminal state
// must apply that policy externally — this layer observes empirical reality,
// which includes spontaneous revival from cache seeds, re-uploads, and
// magnet-link resurrectors.
const (
	TorrentThriving    = 0 // Seeders >= 10, S/L >= 2.0
	TorrentHealthy     = 1 // Seeders >= 3,  S/L >= 0.5
	TorrentAtRisk      = 2 // Seeders 1–2
	TorrentDying       = 3 // Seeders = 0, Leechers > 0 (no complete source)
	TorrentUnavailable = 4 // Seeders = 0, Leechers = 0 (currently empty; can revive)
	NumTorrentStates   = 5
)

var PeerStateNames = [NumPeerStates]string{
	"LEECHING", "SEEDING", "DORMANT", "SNATCHED", "DEAD",
}
var UserStateNames = [NumUserStates]string{
	"SURPLUS", "HEALTHY", "MARGINAL", "DEFICIT", "SEVERE_DEFICIT",
}
var TorrentStateNames = [NumTorrentStates]string{
	"THRIVING", "HEALTHY", "AT_RISK", "DYING", "UNAVAILABLE",
}

// MaxEntropy returns the theoretical maximum Shannon entropy for n equiprobable states.
func MaxEntropy(n int) float64 {
	return math.Log2(float64(n))
}

// TorrentHealthState computes the health band from seeder/leecher counts.
func TorrentHealthState(seeders, leechers int64) int {
	switch {
	case seeders == 0 && leechers == 0:
		return TorrentUnavailable
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

// UserRatioState computes the pure ratio band from upload/download totals.
//
// UMM-01: canLeech and hasFreeleech are intentionally NOT parameters. canLeech
// is a policy outcome derived from ratio; hasFreeleech is an accounting mode.
// Neither belongs in the chain state. Callers must track UserAccountingMode
// separately and skip freeleech users from chain observations (see
// UserEngine.observe). This ensures the chain models ratio dynamics rather than
// Ocelot's enforcement decisions.
func UserRatioState(uploaded, downloaded int64) int {
	if downloaded == 0 {
		if uploaded > 0 {
			return UserSurplus
		}
		return UserSevereDeficit // new user, no activity data
	}
	ratio := float64(uploaded) / float64(downloaded)
	switch {
	case ratio >= 2.0:
		return UserSurplus
	case ratio >= 0.6:
		return UserHealthy
	case ratio >= 0.3:
		return UserMarginal
	case ratio >= 0.1:
		return UserDeficit
	default:
		return UserSevereDeficit
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
