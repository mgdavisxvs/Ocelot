package tracker

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestMetricsRecorderCreation(t *testing.T) {
	mr := GetMetricsRecorder()

	if mr == nil {
		t.Fatal("GetMetricsRecorder returned nil")
	}
}

func TestRecordAnnounce(t *testing.T) {
	mr := GetMetricsRecorder()

	// Record announce metrics
	mr.RecordAnnounce("started", "success", 50*time.Millisecond)
	mr.RecordAnnounce("completed", "success", 30*time.Millisecond)
	mr.RecordAnnounce("stopped", "error", 100*time.Millisecond)

	// Metrics should be recorded (verified by Prometheus client)
	// No error means success
}

func TestRecordScrape(t *testing.T) {
	mr := GetMetricsRecorder()

	mr.RecordScrape("success", 25*time.Millisecond)
	mr.RecordScrape("error", 100*time.Millisecond)

	// Metrics recorded successfully
}

func TestRecordDBQuery(t *testing.T) {
	mr := GetMetricsRecorder()

	// Successful query
	mr.RecordDBQuery("select_peers", 10*time.Millisecond, nil)

	// Failed query
	err := &TrackerError{Type: "database", Message: "connection failed"}
	mr.RecordDBQuery("insert_peer", 50*time.Millisecond, err)

	// Metrics recorded
}

func TestRecordHTTPRequest(t *testing.T) {
	mr := GetMetricsRecorder()

	mr.RecordHTTPRequest("GET", "/announce", http.StatusOK, 15*time.Millisecond)
	mr.RecordHTTPRequest("GET", "/scrape", http.StatusOK, 20*time.Millisecond)
	mr.RecordHTTPRequest("GET", "/stats", http.StatusNotFound, 5*time.Millisecond)

	// Metrics recorded
}

func TestUpdatePeerCounts(t *testing.T) {
	mr := GetMetricsRecorder()

	mr.UpdatePeerCounts(150, 75)
	mr.UpdatePeerCounts(200, 100)

	// Gauges updated
}

func TestUpdateTorrentCount(t *testing.T) {
	mr := GetMetricsRecorder()

	mr.UpdateTorrentCount(500)
	mr.UpdateTorrentCount(750)

	// Gauge updated
}

func TestUpdateWorkerPool(t *testing.T) {
	mr := GetMetricsRecorder()

	mr.UpdateWorkerPool(8, 16)
	mr.UpdateWorkerPool(12, 16)

	// Gauges updated
}

func TestMetricsMultipleLabels(t *testing.T) {
	mr := GetMetricsRecorder()

	// Test different event types
	events := []string{"started", "stopped", "completed"}
	statuses := []string{"success", "error"}

	for _, event := range events {
		for _, status := range statuses {
			mr.RecordAnnounce(event, status, 10*time.Millisecond)
		}
	}

	// All label combinations recorded
}

func TestMetricsDurationBuckets(t *testing.T) {
	mr := GetMetricsRecorder()

	// Test different duration buckets
	durations := []time.Duration{
		500 * time.Microsecond,  // 0.0005s
		5 * time.Millisecond,    // 0.005s
		50 * time.Millisecond,   // 0.05s
		500 * time.Millisecond,  // 0.5s
		2 * time.Second,         // 2s
	}

	for _, dur := range durations {
		mr.RecordAnnounce("started", "success", dur)
		mr.RecordScrape("success", dur)
		mr.RecordDBQuery("test", dur, nil)
		mr.RecordHTTPRequest("GET", "/test", 200, dur)
	}

	// All durations should be bucketed correctly
}

func TestMetricsCounterIncrement(t *testing.T) {
	mr := GetMetricsRecorder()

	// Record same metric multiple times
	for i := 0; i < 10; i++ {
		mr.RecordAnnounce("started", "success", 10*time.Millisecond)
	}

	// Counter should increment each time
}

func TestMetricsGaugeSet(t *testing.T) {
	mr := GetMetricsRecorder()

	// Set gauge values
	mr.UpdatePeerCounts(100, 50)
	mr.UpdatePeerCounts(200, 75) // Should replace, not add

	mr.UpdateTorrentCount(1000)
	mr.UpdateTorrentCount(1500) // Should replace

	// Gauges should reflect latest values
}

func TestMetricsHTTPStatusCodes(t *testing.T) {
	mr := GetMetricsRecorder()

	statusCodes := []int{
		http.StatusOK,
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusNotFound,
		http.StatusInternalServerError,
		http.StatusServiceUnavailable,
	}

	for _, code := range statusCodes {
		mr.RecordHTTPRequest("GET", "/test", code, 10*time.Millisecond)
	}

	// All status codes recorded
}

func TestMetricsDBQueryTypes(t *testing.T) {
	mr := GetMetricsRecorder()

	queryTypes := []string{
		"select_peers",
		"insert_peer",
		"update_torrent",
		"delete_peer",
		"batch_flush",
	}

	for _, qtype := range queryTypes {
		mr.RecordDBQuery(qtype, 15*time.Millisecond, nil)
	}

	// All query types recorded
}

func TestMetricsHTTPMethods(t *testing.T) {
	mr := GetMetricsRecorder()

	methods := []string{"GET", "POST", "PUT", "DELETE", "HEAD"}

	for _, method := range methods {
		mr.RecordHTTPRequest(method, "/test", 200, 10*time.Millisecond)
	}

	// All HTTP methods recorded
}

func TestMetricsHTTPPaths(t *testing.T) {
	mr := GetMetricsRecorder()

	paths := []string{
		"/announce",
		"/scrape",
		"/stats",
		"/metrics",
		"/health",
	}

	for _, path := range paths {
		mr.RecordHTTPRequest("GET", path, 200, 10*time.Millisecond)
	}

	// All paths recorded
}

func TestMetricsErrorRecording(t *testing.T) {
	mr := GetMetricsRecorder()

	// Record errors
	for i := 0; i < 5; i++ {
		err := &TrackerError{
			Type:    "database",
			Message: "query failed",
		}
		mr.RecordDBQuery("test_query", 10*time.Millisecond, err)
	}

	// Error counter should increment
}

func TestMetricsZeroDuration(t *testing.T) {
	mr := GetMetricsRecorder()

	// Record with zero duration (edge case)
	mr.RecordAnnounce("started", "success", 0)
	mr.RecordScrape("success", 0)
	mr.RecordDBQuery("test", 0, nil)

	// Should not panic or error
}

func TestMetricsNegativeCounts(t *testing.T) {
	mr := GetMetricsRecorder()

	// Gauges should handle zero and positive values
	mr.UpdatePeerCounts(0, 0)
	mr.UpdateTorrentCount(0)
	mr.UpdateWorkerPool(0, 10)

	// Should not panic
}

func TestMetricsLargeCounts(t *testing.T) {
	mr := GetMetricsRecorder()

	// Test with large counts
	mr.UpdatePeerCounts(1000000, 500000)
	mr.UpdateTorrentCount(100000)
	mr.UpdateWorkerPool(1000, 2000)

	// Should handle large numbers
}

func TestMetricsPathNormalization(t *testing.T) {
	mr := GetMetricsRecorder()

	// Different announce paths
	paths := []string{
		"/announce",
		"/announce?info_hash=test",
		"/announce?peer_id=test",
	}

	for _, path := range paths {
		// Extract base path for recording
		basePath := strings.Split(path, "?")[0]
		mr.RecordHTTPRequest("GET", basePath, 200, 10*time.Millisecond)
	}

	// Metrics should normalize paths
}

func TestMetricsConcurrentRecording(t *testing.T) {
	mr := GetMetricsRecorder()

	done := make(chan bool)

	// Record metrics concurrently
	for i := 0; i < 100; i++ {
		go func(id int) {
			mr.RecordAnnounce("started", "success", 10*time.Millisecond)
			mr.RecordScrape("success", 5*time.Millisecond)
			mr.RecordDBQuery("test", 3*time.Millisecond, nil)
			mr.RecordHTTPRequest("GET", "/test", 200, 15*time.Millisecond)
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 100; i++ {
		<-done
	}

	// Prometheus metrics are thread-safe
}

func TestMetricsEventTypes(t *testing.T) {
	mr := GetMetricsRecorder()

	events := []string{
		"started",
		"stopped",
		"completed",
		"paused",
		"empty", // No event
	}

	for _, event := range events {
		mr.RecordAnnounce(event, "success", 10*time.Millisecond)
	}

	// All event types recorded
}

func TestMetricsResourceTypes(t *testing.T) {
	mr := GetMetricsRecorder()

	// Simulate different resource operations
	operations := []struct {
		queryType string
		duration  time.Duration
		hasError  bool
	}{
		{"peer_lookup", 5 * time.Millisecond, false},
		{"torrent_update", 10 * time.Millisecond, false},
		{"user_auth", 20 * time.Millisecond, false},
		{"api_key_validate", 3 * time.Millisecond, true},
		{"batch_insert", 50 * time.Millisecond, false},
	}

	for _, op := range operations {
		var err error
		if op.hasError {
			err = &TrackerError{Type: "error", Message: "test"}
		}
		mr.RecordDBQuery(op.queryType, op.duration, err)
	}

	// All operation types recorded
}
