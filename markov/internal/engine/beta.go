package engine

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/mgdavisxvs/ocelot/markov/internal/db"
)

// BetaEngine tracks per-(user, torrent) upload reliability using a Beta-Binomial model.
// Success event: seeder peer has leechers present AND uploaded > threshold.
// E[p] = α/(α+β); global reliability = Σα / Σ(α+β) across all user torrents.
type BetaEngine struct {
	mu       sync.RWMutex
	alpha    map[peerKey]float64
	betaVal  map[peerKey]float64
	obsCount map[peerKey]int
}

func newBetaEngine() *BetaEngine {
	return &BetaEngine{
		alpha:    make(map[peerKey]float64),
		betaVal:  make(map[peerKey]float64),
		obsCount: make(map[peerKey]int),
	}
}

// loadFromDB restores Beta state from peer_quality table on startup.
func (b *BetaEngine) loadFromDB(recs []db.PeerQualityRecord) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, r := range recs {
		k := peerKey{TorrentID: r.TorrentID, UID: r.UID}
		b.alpha[k] = r.Alpha
		b.betaVal[k] = r.Beta
		b.obsCount[k] = r.ObsCount
	}
}

// observe updates Beta(α,β) for each active seeder peer that has leechers present.
// thresholdBytes is the minimum uploaded bytes to count as a success event.
func (b *BetaEngine) observe(peers []db.PeerRow, leechersByTorrent map[int64]int, thresholdBytes int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, p := range peers {
		if p.InvalidIP || !p.Active || p.Remaining > 0 {
			continue
		}
		if leechersByTorrent[p.FID] == 0 {
			continue
		}
		k := peerKey{TorrentID: p.FID, UID: p.UID}
		if _, ok := b.alpha[k]; !ok {
			b.alpha[k] = 1.0 // Beta(1,1) uniform prior
			b.betaVal[k] = 1.0
		}
		if p.Uploaded > thresholdBytes {
			b.alpha[k]++
		} else {
			b.betaVal[k]++
		}
		b.obsCount[k]++
	}
}

// UserGlobalReliability returns E[p] = Σα/Σ(α+β) and total obs across all torrents for a user.
func (b *BetaEngine) UserGlobalReliability(uid int64) (globalP float64, obsCount int) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var sumAlpha, sumTotal float64
	for k, a := range b.alpha {
		if k.UID != uid {
			continue
		}
		bv := b.betaVal[k]
		sumAlpha += a
		sumTotal += a + bv
		obsCount += b.obsCount[k]
	}
	if sumTotal == 0 {
		return 0.5, 0
	}
	return sumAlpha / sumTotal, obsCount
}

// AllUserGlobalReliability computes E[p] for every user in one O(|beta|) pass.
// Used by buildSeederAssignments to avoid per-user scans of the full Beta map.
func (b *BetaEngine) AllUserGlobalReliability() map[int64]float64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	sumAlpha := make(map[int64]float64, len(b.alpha)/4)
	sumTotal := make(map[int64]float64, len(b.alpha)/4)
	for k, a := range b.alpha {
		bv := b.betaVal[k]
		sumAlpha[k.UID] += a
		sumTotal[k.UID] += a + bv
	}
	out := make(map[int64]float64, len(sumTotal))
	for uid, total := range sumTotal {
		if total == 0 {
			out[uid] = 0.5
		} else {
			out[uid] = sumAlpha[uid] / total
		}
	}
	return out
}

// PeerQuality returns (α, β, obsCount, ok) for a specific (uid, torrentID) pair.
func (b *BetaEngine) PeerQuality(uid, torrentID int64) (alpha, betaV float64, obsCount int, ok bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	k := peerKey{TorrentID: torrentID, UID: uid}
	a, exists := b.alpha[k]
	if !exists {
		return 1.0, 1.0, 0, false
	}
	return a, b.betaVal[k], b.obsCount[k], true
}

// BetaCI95 computes an approximate 95% credible interval for E[p] via normal approximation.
func BetaCI95(alpha, beta float64) (lo, hi float64) {
	p := alpha / (alpha + beta)
	n := alpha + beta
	se := math.Sqrt(p * (1 - p) / n)
	return math.Max(0, p-1.96*se), math.Min(1, p+1.96*se)
}

// persist flushes all in-memory Beta state to the peer_quality table.
func (b *BetaEngine) persist(ctx context.Context, d *db.DB) error {
	b.mu.RLock()
	recs := make([]db.PeerQualityRecord, 0, len(b.alpha))
	now := time.Now().Unix()
	for k, a := range b.alpha {
		recs = append(recs, db.PeerQualityRecord{
			UID:       k.UID,
			TorrentID: k.TorrentID,
			Alpha:     a,
			Beta:      b.betaVal[k],
			ObsCount:  b.obsCount[k],
			UpdatedAt: now,
		})
	}
	b.mu.RUnlock()
	return d.UpsertPeerQuality(ctx, recs)
}
