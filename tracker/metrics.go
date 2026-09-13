package tracker

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Prometheus metrics
var (
	// Announce metrics
	announceTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ocelot_announces_total",
			Help: "Total number of announce requests",
		},
		[]string{"event", "status"},
	)

	announceDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ocelot_announce_duration_seconds",
			Help:    "Announce request duration in seconds",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1},
		},
		[]string{"event"},
	)

	// Scrape metrics
	scrapeTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ocelot_scrapes_total",
			Help: "Total number of scrape requests",
		},
		[]string{"status"},
	)

	scrapeDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "ocelot_scrape_duration_seconds",
			Help:    "Scrape request duration in seconds",
			Buckets: []float64{.001, .005, .01, .025, .05, .1},
		},
	)

	// Peer metrics
	activePeers = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "ocelot_active_peers",
			Help: "Number of active peers per torrent",
		},
		[]string{"torrent_id"},
	)

	totalSeeders = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "ocelot_total_seeders",
			Help: "Total number of seeders across all torrents",
		},
	)

	totalLeechers = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "ocelot_total_leechers",
			Help: "Total number of leechers across all torrents",
		},
	)

	// Torrent metrics
	activeTorrents = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "ocelot_active_torrents",
			Help: "Number of active torrents",
		},
	)

	// Database metrics
	dbQueryDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ocelot_db_query_duration_seconds",
			Help:    "Database query duration in seconds",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5},
		},
		[]string{"query_type"},
	)

	dbErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ocelot_db_errors_total",
			Help: "Total number of database errors",
		},
		[]string{"query_type"},
	)

	// HTTP metrics
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ocelot_http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ocelot_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		},
		[]string{"method", "path"},
	)

	// Rate limiting metrics
	rateLimitExceeded = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ocelot_rate_limit_exceeded_total",
			Help: "Total number of rate limit exceeded events",
		},
		[]string{"ip"},
	)

	// Worker pool metrics
	workerPoolActive = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "ocelot_worker_pool_active",
			Help: "Number of active workers in the pool",
		},
	)

	workerPoolCapacity = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "ocelot_worker_pool_capacity",
			Help: "Total capacity of the worker pool",
		},
	)
)

// MetricsRecorder provides convenience methods for recording metrics
type MetricsRecorder struct{}

// RecordAnnounce records an announce request
func (m *MetricsRecorder) RecordAnnounce(event, status string, duration time.Duration) {
	announceTotal.WithLabelValues(event, status).Inc()
	announceDuration.WithLabelValues(event).Observe(duration.Seconds())
}

// RecordScrape records a scrape request
func (m *MetricsRecorder) RecordScrape(status string, duration time.Duration) {
	scrapeTotal.WithLabelValues(status).Inc()
	scrapeDuration.Observe(duration.Seconds())
}

// RecordDBQuery records a database query
func (m *MetricsRecorder) RecordDBQuery(queryType string, duration time.Duration, err error) {
	dbQueryDuration.WithLabelValues(queryType).Observe(duration.Seconds())
	if err != nil {
		dbErrors.WithLabelValues(queryType).Inc()
	}
}

// RecordHTTPRequest records an HTTP request
func (m *MetricsRecorder) RecordHTTPRequest(method, path string, status int, duration time.Duration) {
	httpRequestsTotal.WithLabelValues(method, path, http.StatusText(status)).Inc()
	httpRequestDuration.WithLabelValues(method, path).Observe(duration.Seconds())
}

// UpdatePeerCounts updates peer count gauges
func (m *MetricsRecorder) UpdatePeerCounts(seeders, leechers int) {
	totalSeeders.Set(float64(seeders))
	totalLeechers.Set(float64(leechers))
}

// UpdateTorrentCount updates active torrent count
func (m *MetricsRecorder) UpdateTorrentCount(count int) {
	activeTorrents.Set(float64(count))
}

// UpdateWorkerPool updates worker pool metrics
func (m *MetricsRecorder) UpdateWorkerPool(active, capacity int) {
	workerPoolActive.Set(float64(active))
	workerPoolCapacity.Set(float64(capacity))
}

// StartMetricsServer starts the admin HTTP server exposing Prometheus metrics
// and, when health is non-nil, the liveness/readiness/startup probes.
func StartMetricsServer(addr string, health *HealthChecker) error {
	logger := GetDefaultLogger()
	logger.Info("starting metrics server", "addr", addr)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	if health != nil {
		health.RegisterHandlers(mux)
	}

	return http.ListenAndServe(addr, mux)
}

// Global metrics recorder
var defaultMetrics = &MetricsRecorder{}

// GetMetricsRecorder returns the default metrics recorder
func GetMetricsRecorder() *MetricsRecorder {
	return defaultMetrics
}
