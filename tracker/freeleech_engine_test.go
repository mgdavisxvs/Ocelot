package tracker

import (
	"testing"
	"time"
)

// countingFreeleechComm counts NotifyFreeleech calls.
type countingFreeleechComm struct {
	NoOpSiteComm
	calls   int
	lastID  int64
	lastHrs int
}

func (c *countingFreeleechComm) NotifyFreeleech(torrentID int64, hours int) error {
	c.calls++
	c.lastID = torrentID
	c.lastHrs = hours
	return nil
}

// buildHeatmapWithNet builds a DemandHeatmap that records enough traffic from
// the given /16 to make it the modal network with a fraction above the surge
// threshold.
func buildHeatmapWithNet(net uint16, count int) *DemandHeatmap {
	h := NewDemandHeatmap(65536)
	ip := []byte{byte(net >> 8), byte(net & 0xff), 1, 1}
	for i := 0; i < count; i++ {
		h.Record(ip)
	}
	return h
}

// buildCacheWithCandidate builds a MarkovShadowCache pre-loaded with one
// freeleech candidate.
func buildCacheWithCandidate(torrentID int64, deadProb float64) *MarkovShadowCache {
	c := NewMarkovShadowCache(0)
	c.candidates.Store([]markovCandidate{
		{TorrentID: torrentID, PriorityScore: 0.9, DeadProb72h: deadProb},
	})
	return c
}

// TestFreeleechEngine_NoFireWhenDemandLow verifies that the engine does not
// call NotifyFreeleech when the heatmap fraction is below the surge threshold.
// Traffic is spread across 100 distinct /16 networks so no single network
// exceeds the surgeThreshold (each has fraction ≈ 1/100 = 0.01 < 0.15).
func TestFreeleechEngine_NoFireWhenDemandLow(t *testing.T) {
	h := NewDemandHeatmap(65536)
	for n := 0; n < 100; n++ {
		ip := []byte{byte(n + 1), 0, 1, 1}
		h.Record(ip)
	}
	cache := buildCacheWithCandidate(42, 0.9)
	comm := &countingFreeleechComm{}
	bus := NewBus(0)
	defer bus.Stop()

	eng := NewFreeleechEngine(h, cache, comm, bus, 0)
	eng.evaluate()

	if comm.calls != 0 {
		t.Fatalf("expected 0 NotifyFreeleech calls with low demand, got %d", comm.calls)
	}
}

// TestFreeleechEngine_NoFireWhenDeadProbLow verifies that a candidate with a
// dead-probability below the threshold does not trigger a freeleech.
func TestFreeleechEngine_NoFireWhenDeadProbLow(t *testing.T) {
	h := buildHeatmapWithNet(0x0202, 500) // high demand
	cache := buildCacheWithCandidate(43, 0.1) // low dead-prob
	comm := &countingFreeleechComm{}
	bus := NewBus(0)
	defer bus.Stop()

	eng := NewFreeleechEngine(h, cache, comm, bus, 0)
	eng.evaluate()

	if comm.calls != 0 {
		t.Fatalf("expected 0 NotifyFreeleech calls with low dead-prob, got %d", comm.calls)
	}
}

// TestFreeleechEngine_FiresWhenBothConditionsMet verifies the happy path:
// demand surging + dead-prob above threshold → NotifyFreeleech called.
func TestFreeleechEngine_FiresWhenBothConditionsMet(t *testing.T) {
	h := buildHeatmapWithNet(0x0303, 500)
	cache := buildCacheWithCandidate(44, 0.9)
	comm := &countingFreeleechComm{}
	bus := NewBus(0)
	defer bus.Stop()

	eng := NewFreeleechEngine(h, cache, comm, bus, 0)
	eng.evaluate()

	if comm.calls != 1 {
		t.Fatalf("expected 1 NotifyFreeleech call, got %d", comm.calls)
	}
	if comm.lastID != 44 {
		t.Errorf("lastID = %d, want 44", comm.lastID)
	}
	if comm.lastHrs < freeleechDefaultMinHours || comm.lastHrs > freeleechDefaultMaxHours {
		t.Errorf("hours %d out of [%d,%d]", comm.lastHrs,
			freeleechDefaultMinHours, freeleechDefaultMaxHours)
	}
}

// TestFreeleechEngine_CooldownPreventsDuplicate verifies that a second
// evaluate() call within the cooldown window does not re-fire.
func TestFreeleechEngine_CooldownPreventsDuplicate(t *testing.T) {
	h := buildHeatmapWithNet(0x0404, 500)
	cache := buildCacheWithCandidate(45, 0.9)
	comm := &countingFreeleechComm{}
	bus := NewBus(0)
	defer bus.Stop()

	eng := NewFreeleechEngine(h, cache, comm, bus, 0)
	eng.evaluate()
	eng.evaluate() // second call — still within cooldown

	if comm.calls != 1 {
		t.Fatalf("expected exactly 1 call within cooldown, got %d", comm.calls)
	}
}

// TestFreeleechEngine_CooldownExpiry verifies that after the cooldown elapses
// a second trigger fires again.
func TestFreeleechEngine_CooldownExpiry(t *testing.T) {
	h := buildHeatmapWithNet(0x0505, 500)
	cache := buildCacheWithCandidate(46, 0.9)
	comm := &countingFreeleechComm{}
	bus := NewBus(0)
	defer bus.Stop()

	eng := NewFreeleechEngine(h, cache, comm, bus, 0)
	eng.cooldown = 1 * time.Millisecond // shrink cooldown for test speed
	eng.evaluate()

	time.Sleep(5 * time.Millisecond) // let cooldown expire
	eng.evaluate()

	if comm.calls != 2 {
		t.Fatalf("expected 2 calls after cooldown expiry, got %d", comm.calls)
	}
}

// TestFreeleechEngine_EmitsGrantedEvent verifies that FreeleechGrantedEvent is
// published on the Bus when a freeleech is triggered.
func TestFreeleechEngine_EmitsGrantedEvent(t *testing.T) {
	h := buildHeatmapWithNet(0x0606, 500)
	cache := buildCacheWithCandidate(47, 0.9)
	comm := &countingFreeleechComm{}
	bus := NewBus(256)
	defer bus.Stop()

	var gotEvent bool
	bus.Subscribe("freeleech.granted", func(e Event) {
		if _, ok := e.(*FreeleechGrantedEvent); ok {
			gotEvent = true
		}
	})

	eng := NewFreeleechEngine(h, cache, comm, bus, 0)
	eng.evaluate()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if gotEvent {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !gotEvent {
		t.Fatal("expected FreeleechGrantedEvent on bus after fire")
	}
}

// TestFreeleechHours_Range verifies freeleechHours maps inputs to [min, max].
func TestFreeleechHours_Range(t *testing.T) {
	cases := []float64{0.55, 0.7, 0.85, 1.0}
	for _, dp := range cases {
		h := freeleechHours(dp)
		if h < freeleechDefaultMinHours || h > freeleechDefaultMaxHours {
			t.Errorf("freeleechHours(%v) = %d, want in [%d,%d]",
				dp, h, freeleechDefaultMinHours, freeleechDefaultMaxHours)
		}
	}
}

// TestFreeleechHours_Monotone verifies higher dead-prob yields more hours.
func TestFreeleechHours_Monotone(t *testing.T) {
	lo := freeleechHours(0.55)
	hi := freeleechHours(1.0)
	if hi < lo {
		t.Errorf("expected hours to increase with dead-prob: lo=%d hi=%d", lo, hi)
	}
}
