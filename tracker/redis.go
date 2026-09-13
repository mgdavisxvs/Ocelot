package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisBackend provides Redis-based shared state for multi-instance deployment
type RedisBackend struct {
	client  *redis.Client
	ctx     context.Context
	logger  *Logger
	metrics *MetricsRecorder
}

// RedisConfig holds Redis configuration
type RedisConfig struct {
	Addr     string
	Password string
	DB       int
	PoolSize int
}

// NewRedisBackend creates a new Redis backend
func NewRedisBackend(config RedisConfig) (*RedisBackend, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     config.Addr,
		Password: config.Password,
		DB:       config.DB,
		PoolSize: config.PoolSize,
	})

	ctx := context.Background()

	// Test connection
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &RedisBackend{
		client:  client,
		ctx:     ctx,
		logger:  GetDefaultLogger(),
		metrics: GetMetricsRecorder(),
	}, nil
}

// AddPeer stores a peer in Redis with TTL
func (r *RedisBackend) AddPeer(infoHash string, peer *Peer, ttl time.Duration) error {
	key := fmt.Sprintf("torrent:%s:peers", infoHash)
	peerData, err := json.Marshal(peer)
	if err != nil {
		return err
	}

	// Use HSET to store peer in hash
	err = r.client.HSet(r.ctx, key, peer.PeerID, peerData).Err()
	if err != nil {
		return err
	}

	// Set TTL on the hash
	r.client.Expire(r.ctx, key, ttl)

	r.logger.Debug("peer added to Redis",
		"info_hash", infoHash,
		"peer_id", string(peer.PeerID),
		"ttl", ttl,
	)

	return nil
}

// GetPeers retrieves all peers for a torrent from Redis
func (r *RedisBackend) GetPeers(infoHash string) ([]*Peer, error) {
	key := fmt.Sprintf("torrent:%s:peers", infoHash)

	// Get all peers from hash
	peerMap, err := r.client.HGetAll(r.ctx, key).Result()
	if err != nil {
		return nil, err
	}

	peers := make([]*Peer, 0, len(peerMap))
	for _, data := range peerMap {
		var peer Peer
		if err := json.Unmarshal([]byte(data), &peer); err != nil {
			r.logger.Warn("failed to unmarshal peer", "error", err)
			continue
		}
		peers = append(peers, &peer)
	}

	return peers, nil
}

// RemovePeer removes a peer from Redis
func (r *RedisBackend) RemovePeer(infoHash string, peerID []byte) error {
	key := fmt.Sprintf("torrent:%s:peers", infoHash)
	return r.client.HDel(r.ctx, key, string(peerID)).Err()
}

// GetTorrent retrieves torrent metadata from Redis cache
func (r *RedisBackend) GetTorrent(infoHash string) (*Torrent, error) {
	key := "torrent:" + infoHash
	data, err := r.client.Get(r.ctx, key).Result()
	if err == redis.Nil {
		return nil, nil // Not found
	}
	if err != nil {
		return nil, err
	}

	var torrent Torrent
	if err := json.Unmarshal([]byte(data), &torrent); err != nil {
		return nil, err
	}

	return &torrent, nil
}

// CacheTorrent stores torrent metadata in Redis with TTL
func (r *RedisBackend) CacheTorrent(infoHash string, torrent *Torrent, ttl time.Duration) error {
	key := "torrent:" + infoHash
	data, err := json.Marshal(torrent)
	if err != nil {
		return err
	}

	return r.client.Set(r.ctx, key, data, ttl).Err()
}

// IncrementSeeders atomically increments seeder count
func (r *RedisBackend) IncrementSeeders(infoHash string) error {
	key := fmt.Sprintf("torrent:%s:seeders", infoHash)
	return r.client.Incr(r.ctx, key).Err()
}

// DecrementSeeders atomically decrements seeder count
func (r *RedisBackend) DecrementSeeders(infoHash string) error {
	key := fmt.Sprintf("torrent:%s:seeders", infoHash)
	return r.client.Decr(r.ctx, key).Err()
}

// IncrementLeechers atomically increments leecher count
func (r *RedisBackend) IncrementLeechers(infoHash string) error {
	key := fmt.Sprintf("torrent:%s:leechers", infoHash)
	return r.client.Incr(r.ctx, key).Err()
}

// DecrementLeechers atomically decrements leecher count
func (r *RedisBackend) DecrementLeechers(infoHash string) error {
	key := fmt.Sprintf("torrent:%s:leechers", infoHash)
	return r.client.Decr(r.ctx, key).Err()
}

// GetSwarmStats retrieves seeder/leecher counts from Redis
func (r *RedisBackend) GetSwarmStats(infoHash string) (seeders, leechers int64, err error) {
	pipe := r.client.Pipeline()

	seederKey := fmt.Sprintf("torrent:%s:seeders", infoHash)
	leecherKey := fmt.Sprintf("torrent:%s:leechers", infoHash)

	seederCmd := pipe.Get(r.ctx, seederKey)
	leecherCmd := pipe.Get(r.ctx, leecherKey)

	_, err = pipe.Exec(r.ctx)
	if err != nil && err != redis.Nil {
		return 0, 0, err
	}

	seeders, _ = seederCmd.Int64()
	leechers, _ = leecherCmd.Int64()

	return seeders, leechers, nil
}

// PublishUpdate publishes an update event to all tracker instances
func (r *RedisBackend) PublishUpdate(channel string, message interface{}) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}

	return r.client.Publish(r.ctx, channel, data).Err()
}

// Subscribe subscribes to update events
func (r *RedisBackend) Subscribe(channel string) *redis.PubSub {
	return r.client.Subscribe(r.ctx, channel)
}

// Close closes the Redis connection
func (r *RedisBackend) Close() error {
	return r.client.Close()
}

// Ping tests the Redis connection
func (r *RedisBackend) Ping() error {
	return r.client.Ping(r.ctx).Err()
}

// FlushExpiredPeers removes peers that haven't announced recently
func (r *RedisBackend) FlushExpiredPeers(infoHash string, timeout time.Duration) error {
	key := fmt.Sprintf("torrent:%s:peers", infoHash)

	peers, err := r.GetPeers(infoHash)
	if err != nil {
		return err
	}

	now := time.Now()
	for _, peer := range peers {
		if now.Sub(peer.LastAnnounce) > timeout {
			r.client.HDel(r.ctx, key, string(peer.PeerID))
		}
	}

	return nil
}
