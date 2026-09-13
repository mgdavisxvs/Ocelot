package tracker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func makeTestWorker() *Worker {
	return &Worker{
		Config:   &Config{},
		Torrents: NewTorrentList(),
		Users:    NewUserList(),
	}
}

// addTorrent inserts a torrent with pre-set peer counts into the worker.
func addTorrent(w *Worker, hash string, id TorrentID, seeders, leechers int, completed uint32, minReplicas uint32) *Torrent {
	t := NewTorrent(id)
	t.Completed = completed
	t.MinReplicas = minReplicas
	for i := 0; i < seeders; i++ {
		ip := make([]byte, 4)
		ip[3] = byte(i + 1)
		p := &Peer{UserID: UserID(i + 1), IPPort: CompactIPPort(ip, 6881), Visible: true}
		t.Seeders.Set(string(rune(i)), p)
	}
	for i := 0; i < leechers; i++ {
		ip := make([]byte, 4)
		ip[3] = byte(100 + i)
		p := &Peer{UserID: UserID(100 + i + 1), IPPort: CompactIPPort(ip, 6882), Left: 1, Visible: true}
		t.Leechers.Set(string(rune(100+i)), p)
	}
	w.Torrents.Set(hash, t)
	return t
}

func TestSwarmHealthDaemon_Classifications(t *testing.T) {
	w := makeTestWorker()

	// dark: leechers > 0, seeders == 0
	addTorrent(w, "dark1", 1, 0, 2, 5, 0)

	// spof: seeders == 1, completed > 0
	addTorrent(w, "spof1", 2, 1, 0, 10, 0)

	// degraded: minReplicas=3, seeders=1
	addTorrent(w, "degr1", 3, 1, 0, 0, 3)

	// ok: seeders=5, leechers=2
	addTorrent(w, "ok1", 4, 5, 2, 100, 0)

	d := NewSwarmHealthDaemon(w, time.Hour)
	d.scan()
	snap := d.Latest()
	if snap == nil {
		t.Fatal("expected snapshot after scan")
	}

	if snap.TotalTorrents != 4 {
		t.Errorf("total torrents: got %d, want 4", snap.TotalTorrents)
	}
	if len(snap.Dark) != 1 || snap.Dark[0].InfoHash != "dark1" {
		t.Errorf("dark classification wrong: %+v", snap.Dark)
	}
	if len(snap.SPOF) != 1 || snap.SPOF[0].InfoHash != "spof1" {
		t.Errorf("spof classification wrong: %+v", snap.SPOF)
	}
	if len(snap.Degraded) != 1 || snap.Degraded[0].InfoHash != "degr1" {
		t.Errorf("degraded classification wrong: %+v", snap.Degraded)
	}
	if snap.HealthyCount != 1 {
		t.Errorf("healthy count: got %d, want 1", snap.HealthyCount)
	}
}

func TestSwarmHealthDaemon_DarkPriorityOverSPOF(t *testing.T) {
	// A torrent with 0 seeders, 1 leecher, completed=5 should be dark, not spof.
	// (dark rule is checked first)
	w := makeTestWorker()
	addTorrent(w, "ambig", 1, 0, 1, 5, 0)

	d := NewSwarmHealthDaemon(w, time.Hour)
	d.scan()
	snap := d.Latest()

	if len(snap.Dark) != 1 || snap.Dark[0].InfoHash != "ambig" {
		t.Errorf("expected dark, got dark=%v spof=%v", snap.Dark, snap.SPOF)
	}
	if len(snap.SPOF) != 0 {
		t.Errorf("expected no spof for dark torrent")
	}
}

func TestSwarmHealthDaemon_EmptyCatalog(t *testing.T) {
	w := makeTestWorker()
	d := NewSwarmHealthDaemon(w, time.Hour)
	d.scan()
	snap := d.Latest()

	if snap.TotalTorrents != 0 {
		t.Errorf("expected 0 torrents, got %d", snap.TotalTorrents)
	}
	if len(snap.Dark)+len(snap.SPOF)+len(snap.Degraded) != 0 {
		t.Error("expected no unhealthy torrents in empty catalog")
	}
}

func TestSwarmHealthDaemon_PublishesEvent(t *testing.T) {
	w := makeTestWorker()
	bus := NewEventBus()
	w.Bus = bus
	sub := bus.Subscribe("test", 4)

	addTorrent(w, "h1", 1, 0, 1, 0, 0) // dark

	d := NewSwarmHealthDaemon(w, time.Hour)
	d.scan()

	select {
	case ev := <-sub:
		if ev.Type != EventTorrentHealth {
			t.Errorf("expected torrent_health event, got %q", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for health event")
	}
}

func TestSwarmHealthHandler_BeforeScan(t *testing.T) {
	w := makeTestWorker()
	d := NewSwarmHealthDaemon(w, time.Hour)
	// Do NOT call scan — latest should be nil.

	req := httptest.NewRequest(http.MethodGet, "/health/swarms", nil)
	rec := httptest.NewRecorder()
	SwarmHealthHandler(d)(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

func TestSwarmHealthHandler_JSON(t *testing.T) {
	w := makeTestWorker()
	addTorrent(w, "t1", 1, 0, 3, 0, 0) // dark

	d := NewSwarmHealthDaemon(w, time.Hour)
	d.scan()

	req := httptest.NewRequest(http.MethodGet, "/health/swarms", nil)
	rec := httptest.NewRecorder()
	SwarmHealthHandler(d)(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	var snap SwarmHealthSnapshot
	if err := json.NewDecoder(rec.Body).Decode(&snap); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(snap.Dark) != 1 {
		t.Errorf("expected 1 dark torrent in response, got %d", len(snap.Dark))
	}
}

func TestSwarmHealthDaemon_MinReplicas(t *testing.T) {
	w := makeTestWorker()
	// minReplicas=5, seeders=3 → degraded
	addTorrent(w, "dr1", 1, 3, 0, 0, 5)
	// minReplicas=3, seeders=3 → ok (exactly at floor)
	addTorrent(w, "ok1", 2, 3, 0, 0, 3)

	d := NewSwarmHealthDaemon(w, time.Hour)
	d.scan()
	snap := d.Latest()

	if len(snap.Degraded) != 1 || snap.Degraded[0].InfoHash != "dr1" {
		t.Errorf("expected dr1 degraded, got %+v", snap.Degraded)
	}
	if snap.HealthyCount != 1 {
		t.Errorf("expected ok1 healthy, got healthy=%d", snap.HealthyCount)
	}
}
