package tracker

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis channel names used for cross-process event bridging.
const (
	RedisChannelAnomalyClient    = "ocelot:anomaly:client"
	RedisChannelAnomalyBehaviour = "ocelot:anomaly:behaviour"
	RedisChannelUserFlagged      = "ocelot:user:flagged"
	RedisChannelUserUnflagged    = "ocelot:user:unflagged"
	RedisChannelFreeleech        = "ocelot:freeleech:recommended"
	RedisChannelInterval         = "ocelot:interval:update"
	RedisChannelSSE              = "ocelot:sse:events"
)

// redisEnvelope is the wire format for cross-process events.
type redisEnvelope struct {
	Topic   string          `json:"topic"`
	TraceID string          `json:"trace_id"`
	OccAt   int64           `json:"occ_at"` // Unix nanoseconds
	Payload json.RawMessage `json:"payload"`
}

// RedisBridge fans events from the in-process Bus out to Redis channels and
// subscribes to inbound Redis channels, injecting them back into the local Bus.
type RedisBridge struct {
	client *redis.Client
	bus    *Bus
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewRedisBridge creates the bridge. Call Start() to activate.
func NewRedisBridge(client *redis.Client, bus *Bus) *RedisBridge {
	ctx, cancel := context.WithCancel(context.Background())
	return &RedisBridge{
		client: client,
		bus:    bus,
		ctx:    ctx,
		cancel: cancel,
	}
}

// Start wires outbound subscribers onto the bus and launches inbound listener.
func (r *RedisBridge) Start() {
	// Outbound: publish anomaly and announce events to Redis for admin SSE.
	r.bus.Subscribe("anomaly.*", r.publishToRedis(RedisChannelSSE))
	r.bus.Subscribe("user.flagged", r.publishToRedis(RedisChannelSSE))
	r.bus.Subscribe("user.banned", r.publishToRedis(RedisChannelSSE))
	r.bus.Subscribe("freeleech.granted", r.publishToRedis(RedisChannelSSE))

	// Inbound: subscribe to channels written by Markov sidecar.
	inboundChannels := []string{
		RedisChannelUserFlagged,
		RedisChannelUserUnflagged,
		RedisChannelFreeleech,
		RedisChannelInterval,
	}
	r.wg.Add(1)
	go r.inboundLoop(inboundChannels)
}

// Stop cancels the inbound listener and waits for it to exit.
func (r *RedisBridge) Stop() {
	r.cancel()
	r.wg.Wait()
}

func (r *RedisBridge) publishToRedis(channel string) Handler {
	return func(e Event) {
		payload, err := json.Marshal(e)
		if err != nil {
			log.Printf("eventbus_redis: marshal error topic=%s: %v", e.Topic(), err)
			return
		}
		env := redisEnvelope{
			Topic:   e.Topic(),
			TraceID: e.TraceID(),
			OccAt:   e.OccurredAt().UnixNano(),
			Payload: payload,
		}
		data, _ := json.Marshal(env)
		if err := r.client.Publish(r.ctx, channel, data).Err(); err != nil {
			log.Printf("eventbus_redis: publish to %s: %v", channel, err)
		}
	}
}

func (r *RedisBridge) inboundLoop(channels []string) {
	defer r.wg.Done()
	pubsub := r.client.Subscribe(r.ctx, channels...)
	defer pubsub.Close()

	for {
		select {
		case <-r.ctx.Done():
			return
		default:
		}

		msg, err := pubsub.ReceiveTimeout(r.ctx, 5*time.Second)
		if err != nil {
			if r.ctx.Err() != nil {
				return
			}
			continue
		}

		m, ok := msg.(*redis.Message)
		if !ok {
			continue
		}

		var env redisEnvelope
		if err := json.Unmarshal([]byte(m.Payload), &env); err != nil {
			log.Printf("eventbus_redis: unmarshal inbound on %s: %v", m.Channel, err)
			continue
		}

		event := r.decodeInbound(env)
		if event != nil {
			r.bus.Publish(event)
		}
	}
}

func (r *RedisBridge) decodeInbound(env redisEnvelope) Event {
	base := baseEvent{
		topic:      env.Topic,
		traceID:    env.TraceID,
		occurredAt: time.Unix(0, env.OccAt),
	}

	switch env.Topic {
	case "user.flagged":
		var p struct {
			UserID uint32  `json:"UserID"`
			Score  float64 `json:"Score"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return nil
		}
		return &UserFlaggedEvent{baseEvent: base, UserID: UserID(p.UserID), Score: p.Score}

	case "user.unflagged":
		var p struct {
			UserID uint32 `json:"UserID"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return nil
		}
		return &UserUnflaggedEvent{baseEvent: base, UserID: UserID(p.UserID)}

	case "freeleech.recommended":
		var p struct {
			TorrentID     uint32  `json:"TorrentID"`
			PriorityScore float64 `json:"PriorityScore"`
			DeadProb72h   float64 `json:"DeadProb72h"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return nil
		}
		return &FreeleechRecommendedEvent{
			baseEvent:     base,
			TorrentID:     TorrentID(p.TorrentID),
			PriorityScore: p.PriorityScore,
			DeadProb72h:   p.DeadProb72h,
		}

	case "interval.update":
		var p struct {
			TorrentID           uint32 `json:"TorrentID"`
			RecommendedInterval int    `json:"RecommendedInterval"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return nil
		}
		return &IntervalUpdateEvent{
			baseEvent:           base,
			TorrentID:           TorrentID(p.TorrentID),
			RecommendedInterval: p.RecommendedInterval,
		}
	}
	return nil
}
