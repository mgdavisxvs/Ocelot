package tracker

import (
	"log"
	"sync"
	"time"
)

// SuspicionLevel is the DFA state for a user under fraud scrutiny (F-T4).
// Transitions are strictly monotonic upward on flags and reversible downward
// on clearances, with hard-ban being an absorbing state.
type SuspicionLevel int

const (
	SuspicionNone    SuspicionLevel = iota // no record of fraud
	SuspicionWatched                       // first flag — observing
	SuspicionSoft                          // repeated flags — leeching suspended
	SuspicionHard                          // score breach or strike limit — banned (absorbing)
)

// dfaTransition returns the next DFA state given the current state, the anomaly
// score, and the cumulative strike count. It is a pure function (F-T4).
func dfaTransition(current SuspicionLevel, score float64, strikes int,
	scoreBanThresh float64, strikesToBan int) SuspicionLevel {
	if current == SuspicionHard {
		return SuspicionHard // absorbing state
	}
	if score >= scoreBanThresh || strikes >= strikesToBan {
		return SuspicionHard
	}
	if strikes >= 2 {
		return SuspicionSoft
	}
	if strikes >= 1 {
		return SuspicionWatched
	}
	return SuspicionNone
}

// FlagRecord stores the DFA state, Markov score, and when it was last updated.
type FlagRecord struct {
	State     SuspicionLevel
	Score     float64
	FlaggedAt time.Time
	Strikes   int
}

// FraudEnforcer listens for user.flagged / user.unflagged events and
// takes enforcement action using an explicit DFA over suspicion levels (F-T4).
type FraudEnforcer struct {
	mu    sync.Mutex
	flags map[UserID]*FlagRecord
	users *UserList
	bus   *Bus

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
	next := dfaTransition(rec.State, rec.Score, rec.Strikes,
		fe.scoreBanThresh, fe.strikesToBan)
	prev := rec.State
	rec.State = next
	strikes := rec.Strikes
	fe.mu.Unlock()

	if next == prev {
		return // no state change — no action needed
	}

	user, ok := fe.users.GetByID(ev.UserID)
	if !ok {
		return
	}

	switch next {
	case SuspicionHard:
		user.Deleted.Store(true)
		fe.bus.Publish(NewUserBannedEvent(ev.TraceID(), ev.UserID,
			"fraud_enforcer: score_threshold_exceeded"))
		log.Printf("fraud_enforcer: banned user %d (score=%.3f strikes=%d)", ev.UserID, ev.Score, strikes)
	case SuspicionSoft:
		user.CanLeech.Store(false)
		log.Printf("fraud_enforcer: leech-suspended user %d (score=%.3f strikes=%d)", ev.UserID, ev.Score, strikes)
	case SuspicionWatched:
		log.Printf("fraud_enforcer: watching user %d (score=%.3f strikes=%d)", ev.UserID, ev.Score, strikes)
	}
}

func (fe *FraudEnforcer) onUnflagged(e Event) {
	ev, ok := e.(*UserUnflaggedEvent)
	if !ok {
		return
	}

	fe.mu.Lock()
	rec, exists := fe.flags[ev.UserID]
	if exists && rec.State != SuspicionHard {
		rec.Strikes = max(0, rec.Strikes-1)
		rec.State = dfaTransition(SuspicionNone, rec.Score, rec.Strikes,
			fe.scoreBanThresh, fe.strikesToBan)
	}
	fe.mu.Unlock()

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
