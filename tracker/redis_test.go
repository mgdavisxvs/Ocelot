package tracker

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// newDisconnectedRedisBackend builds a *RedisBackend with a client pointed at
// an unreachable address (port 1), bypassing the Ping check in NewRedisBackend.
// Each method call will execute its pre-network statements and then fail at the
// first Redis command, giving the coverage tool a chance to mark those lines.
func newDisconnectedRedisBackend() *RedisBackend {
	return &RedisBackend{
		client:  redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}),
		ctx:     context.Background(),
		logger:  GetDefaultLogger(),
		metrics: GetMetricsRecorder(),
	}
}

func TestNewRedisBackend_InvalidServer_ReturnsError(t *testing.T) {
	_, err := NewRedisBackend(RedisConfig{Addr: "127.0.0.1:1"})
	if err == nil {
		t.Fatal("expected connection error from Ping, got nil")
	}
}

func TestRedisBackend_AddPeer_Fails(t *testing.T) {
	rb := newDisconnectedRedisBackend()
	defer rb.Close()
	peer := &Peer{UserID: 1, Port: 6881}
	if err := rb.AddPeer("aabbccdd", "peerid01", peer, time.Minute); err == nil {
		t.Fatal("expected network error from AddPeer on disconnected Redis")
	}
}

func TestRedisBackend_GetPeers_Fails(t *testing.T) {
	rb := newDisconnectedRedisBackend()
	defer rb.Close()
	if _, err := rb.GetPeers("aabbccdd"); err == nil {
		t.Fatal("expected network error from GetPeers on disconnected Redis")
	}
}

func TestRedisBackend_RemovePeer_Fails(t *testing.T) {
	rb := newDisconnectedRedisBackend()
	defer rb.Close()
	if err := rb.RemovePeer("aabbccdd", []byte("peerid01")); err == nil {
		t.Fatal("expected network error from RemovePeer on disconnected Redis")
	}
}

func TestRedisBackend_GetTorrent_Fails(t *testing.T) {
	rb := newDisconnectedRedisBackend()
	defer rb.Close()
	// ECONNREFUSED is not redis.Nil, so GetTorrent returns the error rather
	// than the "not found" nil-nil path.
	if _, err := rb.GetTorrent("aabbccdd"); err == nil {
		t.Fatal("expected network error from GetTorrent on disconnected Redis")
	}
}

func TestRedisBackend_CacheTorrent_Fails(t *testing.T) {
	rb := newDisconnectedRedisBackend()
	defer rb.Close()
	torrent := NewTorrent(TorrentID(1))
	if err := rb.CacheTorrent("aabbccdd", torrent, time.Minute); err == nil {
		t.Fatal("expected network error from CacheTorrent on disconnected Redis")
	}
}

func TestRedisBackend_IncrementSeeders_Fails(t *testing.T) {
	rb := newDisconnectedRedisBackend()
	defer rb.Close()
	if err := rb.IncrementSeeders("aabbccdd"); err == nil {
		t.Fatal("expected network error from IncrementSeeders on disconnected Redis")
	}
}

func TestRedisBackend_DecrementSeeders_Fails(t *testing.T) {
	rb := newDisconnectedRedisBackend()
	defer rb.Close()
	if err := rb.DecrementSeeders("aabbccdd"); err == nil {
		t.Fatal("expected network error from DecrementSeeders on disconnected Redis")
	}
}

func TestRedisBackend_IncrementLeechers_Fails(t *testing.T) {
	rb := newDisconnectedRedisBackend()
	defer rb.Close()
	if err := rb.IncrementLeechers("aabbccdd"); err == nil {
		t.Fatal("expected network error from IncrementLeechers on disconnected Redis")
	}
}

func TestRedisBackend_DecrementLeechers_Fails(t *testing.T) {
	rb := newDisconnectedRedisBackend()
	defer rb.Close()
	if err := rb.DecrementLeechers("aabbccdd"); err == nil {
		t.Fatal("expected network error from DecrementLeechers on disconnected Redis")
	}
}

func TestRedisBackend_GetSwarmStats_Fails(t *testing.T) {
	rb := newDisconnectedRedisBackend()
	defer rb.Close()
	// pipe.Exec returns ECONNREFUSED (not redis.Nil), so GetSwarmStats propagates it.
	if _, _, err := rb.GetSwarmStats("aabbccdd"); err == nil {
		t.Fatal("expected network error from GetSwarmStats on disconnected Redis")
	}
}

func TestRedisBackend_PublishUpdate_Fails(t *testing.T) {
	rb := newDisconnectedRedisBackend()
	defer rb.Close()
	if err := rb.PublishUpdate("updates", map[string]string{"event": "announce"}); err == nil {
		t.Fatal("expected network error from PublishUpdate on disconnected Redis")
	}
}

func TestRedisBackend_Subscribe_ReturnsNonNil(t *testing.T) {
	rb := newDisconnectedRedisBackend()
	defer rb.Close()
	// Subscribe is lazy in go-redis — no network call until Receive.
	ps := rb.Subscribe("updates")
	if ps == nil {
		t.Fatal("Subscribe returned nil PubSub")
	}
	ps.Close()
}

func TestRedisBackend_Close(t *testing.T) {
	rb := newDisconnectedRedisBackend()
	if err := rb.Close(); err != nil {
		t.Fatalf("Close returned unexpected error: %v", err)
	}
}

func TestRedisBackend_Ping_Fails(t *testing.T) {
	rb := newDisconnectedRedisBackend()
	defer rb.Close()
	if err := rb.Ping(); err == nil {
		t.Fatal("expected network error from Ping on disconnected Redis")
	}
}

func TestRedisBackend_FlushExpiredPeers_Fails(t *testing.T) {
	rb := newDisconnectedRedisBackend()
	defer rb.Close()
	// FlushExpiredPeers calls GetPeers internally; with a disconnected client
	// HGetAll fails and the error propagates.
	if err := rb.FlushExpiredPeers("aabbccdd", time.Minute); err == nil {
		t.Fatal("expected network error from FlushExpiredPeers on disconnected Redis")
	}
}
