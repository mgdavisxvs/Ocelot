package tracker

import (
	"testing"
	"time"
)

func TestMetricsRecorder_RecordAnnounce(t *testing.T) {
	m := GetMetricsRecorder()
	m.RecordAnnounce("started", "ok", 5*time.Millisecond)
	m.RecordAnnounce("completed", "error", 2*time.Millisecond)
}

func TestMetricsRecorder_RecordScrape(t *testing.T) {
	m := GetMetricsRecorder()
	m.RecordScrape("ok", 3*time.Millisecond)
	m.RecordScrape("error", 1*time.Millisecond)
}

func TestMetricsRecorder_RecordHTTPRequest(t *testing.T) {
	m := GetMetricsRecorder()
	m.RecordHTTPRequest("GET", "/announce", 200, 10*time.Millisecond)
	m.RecordHTTPRequest("POST", "/scrape", 500, 2*time.Millisecond)
}

func TestMetricsRecorder_UpdatePeerCounts(t *testing.T) {
	m := GetMetricsRecorder()
	m.UpdatePeerCounts(10, 5)
	m.UpdatePeerCounts(0, 0)
}

func TestMetricsRecorder_UpdateTorrentCount(t *testing.T) {
	m := GetMetricsRecorder()
	m.UpdateTorrentCount(42)
	m.UpdateTorrentCount(0)
}

func TestMetricsRecorder_UpdateWorkerPool(t *testing.T) {
	m := GetMetricsRecorder()
	m.UpdateWorkerPool(8, 16)
	m.UpdateWorkerPool(0, 0)
}

func TestGetMetricsRecorder_ReturnsNonNil(t *testing.T) {
	if GetMetricsRecorder() == nil {
		t.Fatal("GetMetricsRecorder() returned nil")
	}
}

// TestStartMetricsServer_InvalidAddr covers the logger.Info + http.Handle +
// http.ListenAndServe path by passing a syntactically invalid address that
// fails immediately.
func TestStartMetricsServer_InvalidAddr_ReturnsError(t *testing.T) {
	err := StartMetricsServer("invalid-addr-no-colon")
	if err == nil {
		t.Error("expected error for invalid listen address, got nil")
	}
}
