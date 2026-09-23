package tracker

import (
	"time"
)

// Event is the base interface every bus event implements.
type Event interface {
	Topic() string
	OccurredAt() time.Time
	TraceID() string
}

// baseEvent holds the fields common to all events.
type baseEvent struct {
	topic     string
	occurredAt time.Time
	traceID   string
}

func (b baseEvent) Topic() string         { return b.topic }
func (b baseEvent) OccurredAt() time.Time { return b.occurredAt }
func (b baseEvent) TraceID() string       { return b.traceID }

func newBase(topic, traceID string) baseEvent {
	return baseEvent{topic: topic, occurredAt: time.Now(), traceID: traceID}
}

// ── announce.* ───────────────────────────────────────────────────────────────

// AnnounceEvent is published on every successful announce.
type AnnounceEvent struct {
	baseEvent
	UserID    UserID
	TorrentID TorrentID
	Event     string // "started", "completed", "stopped", ""
	Uploaded  int64
	Downloaded int64
	Left      int64
	Seeders   int
	Leechers  int
}

func NewAnnounceEvent(traceID string, userID UserID, torrentID TorrentID, event string,
	uploaded, downloaded, left int64, seeders, leechers int) *AnnounceEvent {
	return &AnnounceEvent{
		baseEvent:  newBase("announce.success", traceID),
		UserID:     userID,
		TorrentID:  torrentID,
		Event:      event,
		Uploaded:   uploaded,
		Downloaded: downloaded,
		Left:       left,
		Seeders:    seeders,
		Leechers:   leechers,
	}
}

// ── anomaly.* ────────────────────────────────────────────────────────────────

// ClientAnomalyEvent is published when the client detector blocks a peer.
type ClientAnomalyEvent struct {
	baseEvent
	UserID    UserID
	TorrentID TorrentID
	PeerID    string
	UserAgent string
	Reason    string
}

func NewClientAnomalyEvent(traceID string, userID UserID, torrentID TorrentID, peerID, userAgent, reason string) *ClientAnomalyEvent {
	return &ClientAnomalyEvent{
		baseEvent: newBase("anomaly.client", traceID),
		UserID:    userID,
		TorrentID: torrentID,
		PeerID:    peerID,
		UserAgent: userAgent,
		Reason:    reason,
	}
}

// BehaviourAnomalyEvent is published when the behaviour detector blocks a peer.
type BehaviourAnomalyEvent struct {
	baseEvent
	UserID    UserID
	TorrentID TorrentID
	Reason    string
	Uploaded  int64
	Downloaded int64
}

func NewBehaviourAnomalyEvent(traceID string, userID UserID, torrentID TorrentID, reason string, uploaded, downloaded int64) *BehaviourAnomalyEvent {
	return &BehaviourAnomalyEvent{
		baseEvent:  newBase("anomaly.behaviour", traceID),
		UserID:     userID,
		TorrentID:  torrentID,
		Reason:     reason,
		Uploaded:   uploaded,
		Downloaded: downloaded,
	}
}

// ── user.* ───────────────────────────────────────────────────────────────────

// UserFlaggedEvent is published when Markov flags a user as anomalous.
type UserFlaggedEvent struct {
	baseEvent
	UserID UserID
	Score  float64
}

func NewUserFlaggedEvent(traceID string, userID UserID, score float64) *UserFlaggedEvent {
	return &UserFlaggedEvent{
		baseEvent: newBase("user.flagged", traceID),
		UserID:    userID,
		Score:     score,
	}
}

// UserUnflaggedEvent is published when a previously-flagged user clears.
type UserUnflaggedEvent struct {
	baseEvent
	UserID UserID
}

func NewUserUnflaggedEvent(traceID string, userID UserID) *UserUnflaggedEvent {
	return &UserUnflaggedEvent{
		baseEvent: newBase("user.unflagged", traceID),
		UserID:    userID,
	}
}

// UserBannedEvent is published when the fraud enforcer bans a user.
type UserBannedEvent struct {
	baseEvent
	UserID UserID
	Reason string
}

func NewUserBannedEvent(traceID string, userID UserID, reason string) *UserBannedEvent {
	return &UserBannedEvent{
		baseEvent: newBase("user.banned", traceID),
		UserID:    userID,
		Reason:    reason,
	}
}

// ── torrent.* ────────────────────────────────────────────────────────────────

// TorrentCompletedEvent is published when a peer sends event=completed.
type TorrentCompletedEvent struct {
	baseEvent
	UserID    UserID
	TorrentID TorrentID
}

func NewTorrentCompletedEvent(traceID string, userID UserID, torrentID TorrentID) *TorrentCompletedEvent {
	return &TorrentCompletedEvent{
		baseEvent: newBase("torrent.completed", traceID),
		UserID:    userID,
		TorrentID: torrentID,
	}
}

// ── swarm.* ──────────────────────────────────────────────────────────────────

// SwarmUpdatedEvent is published after every announce that changes seeder/leecher counts.
type SwarmUpdatedEvent struct {
	baseEvent
	TorrentID TorrentID
	Seeders   int
	Leechers  int
}

func NewSwarmUpdatedEvent(traceID string, torrentID TorrentID, seeders, leechers int) *SwarmUpdatedEvent {
	return &SwarmUpdatedEvent{
		baseEvent: newBase("swarm.updated", traceID),
		TorrentID: torrentID,
		Seeders:   seeders,
		Leechers:  leechers,
	}
}

// ── freeleech.* ──────────────────────────────────────────────────────────────

// FreeleechRecommendedEvent is published when Markov recommends a freeleech grant.
type FreeleechRecommendedEvent struct {
	baseEvent
	TorrentID     TorrentID
	PriorityScore float64
	DeadProb72h   float64
}

func NewFreeleechRecommendedEvent(traceID string, torrentID TorrentID, priorityScore, deadProb72h float64) *FreeleechRecommendedEvent {
	return &FreeleechRecommendedEvent{
		baseEvent:     newBase("freeleech.recommended", traceID),
		TorrentID:     torrentID,
		PriorityScore: priorityScore,
		DeadProb72h:   deadProb72h,
	}
}

// FreeleechGrantedEvent is published when the actuator successfully grants freeleech.
type FreeleechGrantedEvent struct {
	baseEvent
	TorrentID TorrentID
}

func NewFreeleechGrantedEvent(traceID string, torrentID TorrentID) *FreeleechGrantedEvent {
	return &FreeleechGrantedEvent{
		baseEvent: newBase("freeleech.granted", traceID),
		TorrentID: torrentID,
	}
}

// ── interval.* ───────────────────────────────────────────────────────────────

// IntervalUpdateEvent is published when Markov computes a recommended interval for a torrent.
type IntervalUpdateEvent struct {
	baseEvent
	TorrentID           TorrentID
	RecommendedInterval int // seconds
}

func NewIntervalUpdateEvent(traceID string, torrentID TorrentID, interval int) *IntervalUpdateEvent {
	return &IntervalUpdateEvent{
		baseEvent:           newBase("interval.update", traceID),
		TorrentID:           torrentID,
		RecommendedInterval: interval,
	}
}

// ── infra.* ──────────────────────────────────────────────────────────────────

// BusDropEvent is published (on a best-effort basis) when events are dropped.
type BusDropEvent struct {
	baseEvent
	DroppedTopic string
	TotalDrops   uint64
}

func NewBusDropEvent(droppedTopic string, totalDrops uint64) *BusDropEvent {
	return &BusDropEvent{
		baseEvent:    newBase("infra.bus_drop", ""),
		DroppedTopic: droppedTopic,
		TotalDrops:   totalDrops,
	}
}
