package tracker

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// LiveSegmentRegistry tracks active live stream segments.
type LiveSegmentRegistry struct {
	mu       sync.RWMutex
	segments map[string][]liveSegment // stream_id → ordered segment list
}

type liveSegment struct {
	Seq      uint64    `json:"seq"`
	InfoHash string    `json:"info_hash"`
	AddedAt  time.Time `json:"-"`
}

// globalLiveRegistry is the process-wide live segment registry.
var globalLiveRegistry = &LiveSegmentRegistry{
	segments: make(map[string][]liveSegment),
}

// add registers a new segment and evicts segments older than maxAge.
func (lr *LiveSegmentRegistry) add(streamID string, seg liveSegment, maxAge time.Duration) {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	segs := lr.segments[streamID]
	segs = append(segs, seg)
	// Evict expired segments.
	cutoff := time.Now().Add(-maxAge)
	start := 0
	for start < len(segs) && segs[start].AddedAt.Before(cutoff) {
		start++
	}
	lr.segments[streamID] = segs[start:]
}

// playlist returns the current segment list for a stream.
func (lr *LiveSegmentRegistry) playlist(streamID string) []liveSegment {
	lr.mu.RLock()
	defer lr.mu.RUnlock()
	segs := lr.segments[streamID]
	out := make([]liveSegment, len(segs))
	copy(out, segs)
	return out
}

// handleLiveSegment processes POST /live/segment — ingester calls this per segment.
func (s *Server) handleLiveSegment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamID string `json:"stream_id"`
		Seq      uint64 `json:"seq"`
		InfoHash string `json:"info_hash"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
		req.StreamID == "" || req.InfoHash == "" {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	// Register torrent with live-stream announce interval settings.
	if _, ok := s.worker.Torrents.Get(req.InfoHash); !ok {
		t := NewTorrent(TorrentID(hashToID(req.InfoHash)))
		t.FreeType = FreeFree
		s.worker.Torrents.Set(req.InfoHash, t)
	}

	seg := liveSegment{Seq: req.Seq, InfoHash: req.InfoHash, AddedAt: time.Now()}
	// Keep last 90 seconds of segments (3 × 30s window).
	globalLiveRegistry.add(req.StreamID, seg, 90*time.Second)

	// Redis pub/sub: notify edge nodes that a new segment is ready.
	if s.worker.Redis != nil {
		s.worker.Redis.PublishUpdate("live:"+req.StreamID, map[string]any{
			"seq":       req.Seq,
			"info_hash": req.InfoHash,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "seq": req.Seq})
}

// handleLivePlaylist processes GET /live/playlist?stream_id=<id>.
func (s *Server) handleLivePlaylist(w http.ResponseWriter, r *http.Request) {
	streamID := r.URL.Query().Get("stream_id")
	if streamID == "" {
		http.Error(w, `{"error":"missing stream_id"}`, http.StatusBadRequest)
		return
	}
	segs := globalLiveRegistry.playlist(streamID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"stream_id": streamID,
		"segments":  segs,
		"count":     len(segs),
	})
}
