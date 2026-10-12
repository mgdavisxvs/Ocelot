package engine

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/mgdavisxvs/ocelot/markov/internal/chain"
	"github.com/mgdavisxvs/ocelot/markov/internal/db"
)

// atRiskTorrent is a torrent requiring seeder recruitment, pre-ranked by urgency.
type atRiskTorrent struct {
	TorrentID    int64
	HealthState  int
	Seeders      int64
	UrgencyScore float64
}

// buildAtRiskList filters and ranks predictions to the top-N most urgent at-risk
// torrents. Bounded by topN to keep the per-persist assignment work O(topN × users).
func buildAtRiskList(predictions []TorrentPrediction, torrentSeeders map[int64]int64, topN int, minUrgency float64) []atRiskTorrent {
	out := make([]atRiskTorrent, 0, topN)
	for _, p := range predictions {
		if p.HealthState == chain.TorrentThriving || p.HealthState == chain.TorrentHealthy {
			continue
		}
		urgency := p.UnavailableProb72h * (1.0 + p.Entropy)
		if urgency < minUrgency {
			continue
		}
		out = append(out, atRiskTorrent{
			TorrentID:    p.TorrentID,
			HealthState:  p.HealthState,
			Seeders:      torrentSeeders[p.TorrentID],
			UrgencyScore: urgency,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UrgencyScore > out[j].UrgencyScore })
	if len(out) > topN {
		out = out[:topN]
	}
	return out
}

// buildSeederAssignments generates precision seeder recruitment assignments.
//
// For each at-risk torrent (bounded to FreeleechTopN by urgency), eligible users
// are scored on two axes:
//   - Capacity: ratio state (UserSurplus=1.0, UserHealthy=0.7; deficit users excluded)
//   - Propensity: E[p] from Beta-Binomial quality model, per-torrent if observed,
//     else the user's global reliability average
//
// New assignments are de-duplicated against active (unfulfilled, unexpired) ones
// and capped per user by MaxAssignmentsPerUser.
func (e *Engine) buildSeederAssignments(ctx context.Context, predictions []TorrentPrediction, now int64) error {
	// Load active assignments for de-dup and per-user count enforcement.
	active, err := e.db.LoadActiveAssignments(ctx, now)
	if err != nil {
		return err
	}
	alreadyAssigned := make(map[db.AssignPair]struct{}, len(active))
	userAssignCount := make(map[int64]int, len(active))
	for _, a := range active {
		alreadyAssigned[db.AssignPair{UserID: a.UserID, TorrentID: a.TorrentID}] = struct{}{}
		userAssignCount[a.UserID]++
	}

	// Build seeder count index from last fetched torrent rows.
	torrentSeeders := make(map[int64]int64, len(e.lastTorrents))
	for _, t := range e.lastTorrents {
		torrentSeeders[t.ID] = t.Seeders
	}

	// Rank and bound at-risk torrents.
	atRisk := buildAtRiskList(predictions, torrentSeeders, e.cfg.FreeleechTopN, e.cfg.SeederMinUrgencyScore)
	if len(atRisk) == 0 {
		return nil
	}

	// Pre-compute eligible users and global reliability in one pass each.
	// This avoids O(users × torrents) Beta map scans during the inner loop.
	userStates := e.users.userStates()
	globalP := e.beta.AllUserGlobalReliability()

	type eligibleUser struct {
		uid            int64
		capacityWeight float64
		globalP        float64
	}
	eligible := make([]eligibleUser, 0, len(userStates))
	for uid, state := range userStates {
		var w float64
		switch state {
		case chain.UserSurplus:
			w = 1.0
		case chain.UserHealthy:
			w = 0.7
		default:
			continue // deficit users are excluded from seeding obligations
		}
		p := globalP[uid]
		if p == 0 {
			p = 0.5 // Beta(1,1) prior mean for unobserved users
		}
		eligible = append(eligible, eligibleUser{uid: uid, capacityWeight: w, globalP: p})
	}
	if len(eligible) == 0 {
		return nil
	}

	expiresAt := now + int64(e.cfg.SeederAssignmentTTLSec)

	type candidate struct {
		uid   int64
		score float64
	}

	var newAssignments []db.SeederAssignment

	for _, t := range atRisk {
		deficit := seedersNeeded(t.HealthState, int(t.Seeders))
		if deficit <= 0 {
			continue
		}

		candidates := make([]candidate, 0, len(eligible))
		for _, u := range eligible {
			if _, exists := alreadyAssigned[db.AssignPair{UserID: u.uid, TorrentID: t.TorrentID}]; exists {
				continue
			}
			if userAssignCount[u.uid] >= e.cfg.MaxAssignmentsPerUser {
				continue
			}
			// Per-torrent Beta quality takes precedence over global reliability.
			propensity := u.globalP
			if alpha, betaV, _, ok := e.beta.PeerQuality(u.uid, t.TorrentID); ok {
				propensity = alpha / (alpha + betaV)
			}
			candidates = append(candidates, candidate{
				uid:   u.uid,
				score: t.UrgencyScore * propensity * u.capacityWeight,
			})
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })

		limit := e.cfg.MaxSeedersPerAssignment
		if deficit < limit {
			limit = deficit
		}
		for i := 0; i < limit && i < len(candidates); i++ {
			c := candidates[i]
			newAssignments = append(newAssignments, db.SeederAssignment{
				UserID:       c.uid,
				TorrentID:    t.TorrentID,
				UrgencyScore: t.UrgencyScore,
				AssignedAt:   now,
				ExpiresAt:    expiresAt,
			})
			// Update in-memory state so subsequent torrent iterations see the
			// updated counts without a round-trip to the DB.
			userAssignCount[c.uid]++
			alreadyAssigned[db.AssignPair{UserID: c.uid, TorrentID: t.TorrentID}] = struct{}{}
		}
	}

	if len(newAssignments) == 0 {
		return nil
	}
	if err := e.db.BulkInsertSeederAssignments(ctx, newAssignments); err != nil {
		return err
	}
	slog.Debug("seeder assignments created", "count", len(newAssignments))
	return nil
}

// checkAssignmentFulfillment marks assignments as fulfilled for users currently
// observed seeding their assigned torrent. Uses the live peer staging data
// (e.lastPeers) so no additional DB fetch is required.
func (e *Engine) checkAssignmentFulfillment(ctx context.Context, now int64) error {
	// Build map: torrentID → []seeding UIDs from live peer data.
	seedingByTorrent := make(map[int64][]int64)
	for _, p := range e.lastPeers {
		if p.Active && p.Remaining == 0 {
			seedingByTorrent[p.FID] = append(seedingByTorrent[p.FID], p.UID)
		}
	}
	if len(seedingByTorrent) == 0 {
		return nil
	}

	// Load active assignments and group by torrent.
	active, err := e.db.LoadActiveAssignments(ctx, now)
	if err != nil {
		return err
	}
	type uidSet map[int64]struct{}
	assignedByTorrent := make(map[int64]uidSet, len(active))
	for _, a := range active {
		if assignedByTorrent[a.TorrentID] == nil {
			assignedByTorrent[a.TorrentID] = make(uidSet)
		}
		assignedByTorrent[a.TorrentID][a.UserID] = struct{}{}
	}

	var totalFulfilled int
	for torrentID, seeders := range seedingByTorrent {
		assigned, ok := assignedByTorrent[torrentID]
		if !ok {
			continue
		}
		var fulfilled []int64
		for _, uid := range seeders {
			if _, isAssigned := assigned[uid]; isAssigned {
				fulfilled = append(fulfilled, uid)
			}
		}
		if len(fulfilled) == 0 {
			continue
		}
		if err := e.db.FulfillSeederAssignments(ctx, torrentID, fulfilled, now); err != nil {
			slog.Error("FulfillSeederAssignments", "torrent_id", torrentID, "err", err)
			continue
		}
		totalFulfilled += len(fulfilled)
	}
	if totalFulfilled > 0 {
		slog.Debug("seeder assignments fulfilled", "count", totalFulfilled)
	}
	return nil
}

// SeederRecommendationsForUser returns active seeding assignments for a user.
// Advisory only — the model never enforces seeding requirements.
func (e *Engine) SeederRecommendationsForUser(ctx context.Context, uid int64) ([]db.SeederAssignment, error) {
	return e.db.LoadUserSeederAssignments(ctx, uid, time.Now().Unix())
}

// seedersNeeded returns the number of additional seeders required to push the
// torrent toward TorrentHealthy (≥3 seeders with S/L≥0.5). Based on the state
// thresholds in chain/states.go — not the transition matrix.
func seedersNeeded(state, currentSeeders int) int {
	switch state {
	case chain.TorrentUnavailable: // 0 seeders, 0 leechers — any seeder revives
		return 1
	case chain.TorrentDying: // 0 seeders, leechers > 0 — any seeder unblocks download
		return 1
	case chain.TorrentAtRisk: // 1–2 seeders; 3 needed for TorrentHealthy
		need := 3 - currentSeeders
		if need < 1 {
			return 0
		}
		return need
	default:
		return 0
	}
}
