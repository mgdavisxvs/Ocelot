package tracker

import (
	"log"
	"sync"
	"time"
)

// FlagRecord stores Markov score and when it was set.
type FlagRecord struct {
	Score     float64
	FlaggedAt time.Time
	Strikes   int
}

// FraudEnforcer listens for user.flagged / user.unflagged events and
// takes enforcement action (ban, leech-suspend) on repeated offenders.
type FraudEnforcer struct {
	mu      sync.Mutex
	flags   map[UserID]*FlagRecord
	users   *UserList
	bus     *Bus

	// Thresholds — configurable via env or defaults.
	strikesToBan   int
	scoreBanThresh float64
}

// NewFraudEnforcer creates the enforcer and subscribes to the bus.
func NewFraudEnforcer(bus *Bus, users *UserList) *FraudEnforcer {
	fe := &FraudEnforcer{
		flags:          make(map[UserID]*FlagRecord),
		bus:            bus,
		users:          users,
		strikesToBan:   3,
		scoreBanThresh: 0.90,
	}
	bus.Subscribe("user.flagged", fe.onFlagged)
	bus.Subscribe("user.unflagged", fe.onUnflagged)
	return fe
}

func (fe *FraudEnforcer) onFlagged(e Event) {
	ev, ok := e.(*UserFlaggedEvent)
	if !ok {
		return
	}

	fe.mu.Lock()
	rec, exists := fe.flags[ev.UserID]
	if !exists {
		rec = &FlagRecord{}
		fe.flags[ev.UserID] = rec
	}
	rec.Score = ev.Score
	rec.FlaggedAt = time.Now()
	rec.Strikes++
	strikes := rec.Strikes
	score := rec.Score
	fe.mu.Unlock()

	user, ok := fe.users.GetByID(ev.UserID)
	if !ok {
		return
	}

	if score >= fe.scoreBanThresh || strikes >= fe.strikesToBan {
		// Hard ban: mark deleted so no further announces are processed.
		user.Deleted.Store(true)
		fe.bus.Publish(NewUserBannedEvent(ev.TraceID(), ev.UserID,
			"fraud_enforcer: score_threshold_exceeded"))
		log.Printf("fraud_enforcer: banned user %d (score=%.3f strikes=%d)", ev.UserID, score, strikes)
		return
	}

	// Soft penalty: suspend leeching.
	user.CanLeech.Store(false)
	log.Printf("fraud_enforcer: leech-suspended user %d (score=%.3f strikes=%d)", ev.UserID, score, strikes)
}

func (fe *FraudEnforcer) onUnflagged(e Event) {
	ev, ok := e.(*UserUnflaggedEvent)
	if !ok {
		return
	}

	fe.mu.Lock()
	rec, exists := fe.flags[ev.UserID]
	if exists {
		rec.Strikes = max(0, rec.Strikes-1)
	}
	fe.mu.Unlock()

	// Restore leeching if not fully banned.
	user, ok := fe.users.GetByID(ev.UserID)
	if !ok {
		return
	}
	if !user.Deleted.Load() {
		user.CanLeech.Store(true)
		log.Printf("fraud_enforcer: leech-restored user %d", ev.UserID)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
