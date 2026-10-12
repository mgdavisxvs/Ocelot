package tracker

import (
	"context"
	"fmt"
	"log"
)

// AuditSubscriber writes security-relevant events to the audit log.
type AuditSubscriber struct {
	audit *AuditLogger
}

// NewAuditSubscriber creates the subscriber and registers it on the bus.
// Returns nil when audit is nil (audit logging disabled).
func NewAuditSubscriber(bus *Bus, audit *AuditLogger) *AuditSubscriber {
	if audit == nil {
		return nil
	}
	s := &AuditSubscriber{audit: audit}
	bus.Subscribe("anomaly.*", s.onAnomaly)
	bus.Subscribe("user.banned", s.onBanned)
	bus.Subscribe("freeleech.granted", s.onFreeleech)
	return s
}

func (s *AuditSubscriber) onAnomaly(e Event) {
	ctx := context.Background()
	switch ev := e.(type) {
	case *ClientAnomalyEvent:
		s.audit.Log(ctx, "anomaly_client", "peer",
			fmt.Sprintf("%d", ev.UserID), false,
			fmt.Errorf("%s", ev.Reason))
	case *BehaviourAnomalyEvent:
		s.audit.Log(ctx, "anomaly_behaviour", "peer",
			fmt.Sprintf("%d", ev.UserID), false,
			fmt.Errorf("%s", ev.Reason))
	}
}

func (s *AuditSubscriber) onBanned(e Event) {
	ev, ok := e.(*UserBannedEvent)
	if !ok {
		return
	}
	ctx := context.Background()
	s.audit.Log(ctx, "user_banned", "user",
		fmt.Sprintf("%d", ev.UserID), true,
		fmt.Errorf("%s", ev.Reason))
}

func (s *AuditSubscriber) onFreeleech(e Event) {
	ev, ok := e.(*FreeleechGrantedEvent)
	if !ok {
		return
	}
	ctx := context.Background()
	s.audit.Log(ctx, "freeleech_granted", "torrent",
		fmt.Sprintf("%d", ev.TorrentID), true, nil)
}

// MetricsSubscriber updates Prometheus counters from bus events.
type MetricsSubscriber struct {
	metrics *MetricsRecorder
}

// NewMetricsSubscriber creates the subscriber and registers it on the bus.
// Returns nil when metrics is nil.
func NewMetricsSubscriber(bus *Bus, metrics *MetricsRecorder) *MetricsSubscriber {
	if metrics == nil {
		return nil
	}
	s := &MetricsSubscriber{metrics: metrics}
	bus.Subscribe("anomaly.client", s.onClientAnomaly)
	bus.Subscribe("anomaly.behaviour", s.onBehaviourAnomaly)
	bus.Subscribe("user.banned", s.onBanned)
	bus.Subscribe("freeleech.granted", s.onFreeleech)
	bus.Subscribe("infra.bus_drop", s.onBusDrop)
	return s
}

func (s *MetricsSubscriber) onClientAnomaly(_ Event) {
	s.metrics.RecordClientAnomaly()
}

func (s *MetricsSubscriber) onBehaviourAnomaly(_ Event) {
	s.metrics.RecordBehaviourAnomaly()
}

func (s *MetricsSubscriber) onBanned(_ Event) {
	s.metrics.RecordUserBanned()
}

func (s *MetricsSubscriber) onFreeleech(_ Event) {
	s.metrics.RecordFreeleechGranted()
}

func (s *MetricsSubscriber) onBusDrop(e Event) {
	ev, ok := e.(*BusDropEvent)
	if !ok {
		return
	}
	log.Printf("eventbus: drop #%d (topic=%s)", ev.TotalDrops, ev.DroppedTopic)
	s.metrics.RecordBusDrop()
}
