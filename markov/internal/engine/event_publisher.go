package engine

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis channel names — must match tracker/eventbus_redis.go constants.
const (
	redisChannelUserFlagged   = "ocelot:user:flagged"
	redisChannelUserUnflagged = "ocelot:user:unflagged"
	redisChannelFreeleech     = "ocelot:freeleech:recommended"
	redisChannelInterval      = "ocelot:interval:update"
)

// EventPublisher publishes Markov results to Redis so the tracker EventBus
// can consume them via the RedisBridge.
type EventPublisher struct {
	client *redis.Client
	ctx    context.Context
}

// NewEventPublisher creates a publisher. If client is nil, all Publish calls
// are no-ops (safe when Redis is not configured).
func NewEventPublisher(client *redis.Client) *EventPublisher {
	return &EventPublisher{client: client, ctx: context.Background()}
}

type redisEnvelope struct {
	Topic   string          `json:"topic"`
	TraceID string          `json:"trace_id"`
	OccAt   int64           `json:"occ_at"` // Unix nanoseconds
	Payload json.RawMessage `json:"payload"`
}

func (p *EventPublisher) publish(channel, topic string, payload any) {
	if p.client == nil {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		slog.Error("event_publisher: marshal", "err", err)
		return
	}
	env := redisEnvelope{
		Topic:   topic,
		OccAt:   time.Now().UnixNano(),
		Payload: raw,
	}
	data, _ := json.Marshal(env)
	if err := p.client.Publish(p.ctx, channel, data).Err(); err != nil {
		slog.Warn("event_publisher: publish failed", "channel", channel, "err", err)
	}
}

// PublishUserFlagged publishes a user.flagged event.
func (p *EventPublisher) PublishUserFlagged(uid int64, score float64) {
	p.publish(redisChannelUserFlagged, "user.flagged", map[string]any{
		"UserID": uid,
		"Score":  score,
	})
}

// PublishUserUnflagged publishes a user.unflagged event.
func (p *EventPublisher) PublishUserUnflagged(uid int64) {
	p.publish(redisChannelUserUnflagged, "user.unflagged", map[string]any{
		"UserID": uid,
	})
}

// PublishFreeleechRecommended publishes a freeleech.recommended event.
func (p *EventPublisher) PublishFreeleechRecommended(torrentID int64, priorityScore, deadProb72h float64) {
	p.publish(redisChannelFreeleech, "freeleech.recommended", map[string]any{
		"TorrentID":     torrentID,
		"PriorityScore": priorityScore,
		"DeadProb72h":   deadProb72h,
	})
}

// PublishIntervalUpdate publishes an interval.update event.
func (p *EventPublisher) PublishIntervalUpdate(torrentID int64, recommendedInterval int) {
	p.publish(redisChannelInterval, "interval.update", map[string]any{
		"TorrentID":           torrentID,
		"RecommendedInterval": recommendedInterval,
	})
}

// publishRecommendedInterval publishes an interval.update event using the
// RecommendedInterval already computed by buildPredictions.
func (p *EventPublisher) publishRecommendedInterval(pred *TorrentPrediction, baseIntervalSec int) {
	if pred == nil || pred.RecommendedInterval == baseIntervalSec {
		return
	}
	p.PublishIntervalUpdate(pred.TorrentID, pred.RecommendedInterval)
}

// WireEventPublisher wires the EventPublisher into the Engine's persist cycle.
// Call once after creating both the Engine and the EventPublisher.
func (e *Engine) WireEventPublisher(pub *EventPublisher, baseIntervalSec int) {
	e.eventPub = pub
	e.baseIntervalSec = baseIntervalSec
}

// publishAnomalyEvents publishes flagged/unflagged events for the anomaly list.
func (e *Engine) publishAnomalyEvents(ctx context.Context, anomalies []AnomalyResult) {
	if e.eventPub == nil {
		return
	}
	for _, a := range anomalies {
		if a.Flagged {
			e.eventPub.PublishUserFlagged(a.UID, a.AnomalyScore)
		} else if e.wasPreviouslyFlagged(ctx, a.UID) {
			e.eventPub.PublishUserUnflagged(a.UID)
		}
	}
}

// publishPredictionEvents publishes freeleech and interval events.
func (e *Engine) publishPredictionEvents(predictions []TorrentPrediction, candidates []FreeleechCandidate) {
	if e.eventPub == nil {
		return
	}
	for _, c := range candidates {
		e.eventPub.PublishFreeleechRecommended(c.TorrentID, c.PriorityScore, c.DeadProb72h)
	}
	for i := range predictions {
		e.eventPub.publishRecommendedInterval(&predictions[i], e.baseIntervalSec)
	}
}

// wasPreviouslyFlagged checks the DB to see if user was flagged in the prior run.
func (e *Engine) wasPreviouslyFlagged(ctx context.Context, uid int64) bool {
	row, err := e.db.LoadUserAnomaly(ctx, uid)
	if err != nil || row == nil {
		return false
	}
	return row.Flagged
}
