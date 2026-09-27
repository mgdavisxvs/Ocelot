package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)


// markovCandidate mirrors the JSON shape returned by the Markov engine's
// GET /freeleech endpoint.
type markovCandidate struct {
	TorrentID     int64   `json:"torrent_id"`
	PriorityScore float64 `json:"priority_score"`
	DeadProb72h   float64 `json:"dead_prob_72h"`
}

// markovMetrics mirrors the JSON shape returned by GET /metrics.
type markovMetrics struct {
	TrackedPeers    int64 `json:"tracked_peers"`
	TrackedTorrents int64 `json:"tracked_torrents"`
	TrackedUsers    int64 `json:"tracked_users"`
	PollCount       int64 `json:"poll_count"`
}

// MarkovConfig holds configuration for the Markov engine client.
type MarkovConfig struct {
	// BaseURL is the HTTP base URL for the Markov sidecar (e.g. "http://127.0.0.1:9090").
	BaseURL string
	// PageRankDamping is the damping factor d used in the seeder PageRank formula:
	// PR(v) = (1-d) + d * Σ PR(u)/L(u). Must be in (0, 1); values ≥ 1.0 cause
	// divergence on zero-out-degree (dead torrent) nodes.
	PageRankDamping float64
}

// MarkovClient is a thin HTTP client for the Markov engine API.
type MarkovClient struct {
	baseURL string
	http    *http.Client
}

// NewMarkovClient creates a client pointing at baseURL (e.g. "http://127.0.0.1:9090").
func NewMarkovClient(baseURL string) *MarkovClient {
	return &MarkovClient{
		baseURL: baseURL,
		http: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// NewMarkovClientWithConfig creates a client from a MarkovConfig, validating the
// PageRankDamping factor. Returns an error if damping >= 1.0.
func NewMarkovClientWithConfig(cfg MarkovConfig) (*MarkovClient, error) {
	if cfg.PageRankDamping != 0 && cfg.PageRankDamping >= 1.0 {
		return nil, fmt.Errorf("markov: PageRankDamping must be < 1.0, got %.4f", cfg.PageRankDamping)
	}
	return &MarkovClient{
		baseURL: cfg.BaseURL,
		http: &http.Client{
			Timeout: 5 * time.Second,
		},
	}, nil
}

// FreeleechCandidates calls GET /freeleech and returns the candidate list.
func (c *MarkovClient) FreeleechCandidates(ctx context.Context) ([]markovCandidate, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/freeleech", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("markov /freeleech: HTTP %d", resp.StatusCode)
	}
	var out []markovCandidate
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

// Metrics calls GET /metrics and returns aggregate engine stats.
func (c *MarkovClient) Metrics(ctx context.Context) (*markovMetrics, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/metrics", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("markov /metrics: HTTP %d", resp.StatusCode)
	}
	var out markovMetrics
	return &out, json.NewDecoder(resp.Body).Decode(&out)
}

// markovIntervalRow mirrors the JSON shape returned by GET /intervals.
type markovIntervalRow struct {
	TorrentID          int64 `json:"torrent_id"`
	RecommendedInterval int   `json:"recommended_interval"`
}

// Intervals calls GET /intervals and returns the per-torrent interval recommendations.
func (c *MarkovClient) Intervals(ctx context.Context) ([]markovIntervalRow, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/intervals", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("markov /intervals: HTTP %d", resp.StatusCode)
	}
	var out []markovIntervalRow
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

// IntervalPoller runs a background goroutine that periodically fetches
// per-torrent interval recommendations from the Markov engine and publishes
// an IntervalUpdateEvent on the bus for each one.  Cancel the context to stop.
func IntervalPoller(ctx context.Context, client *MarkovClient, bus EventPublisher, intervalSec int) {
	go func() {
		ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rows, err := client.Intervals(ctx)
				if err != nil {
					continue
				}
				for _, r := range rows {
					bus.Publish(NewIntervalUpdateEvent("", TorrentID(r.TorrentID), r.RecommendedInterval))
				}
			}
		}
	}()
}

// FreeleechPoller runs a background goroutine that periodically fetches
// freeleech candidates from the Markov engine and calls siteComm.NotifyFreeleech
// for each one.  It returns immediately; cancel the context to stop it.
func FreeleechPoller(ctx context.Context, client *MarkovClient, siteComm SiteCommInterface, intervalSec, notifyHours int) {
	go func() {
		ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				candidates, err := client.FreeleechCandidates(ctx)
				if err != nil {
					continue
				}
				for _, c := range candidates {
					if err := siteComm.NotifyFreeleech(c.TorrentID, notifyHours); err != nil {
						GetDefaultLogger().Warn("freeleech notify failed",
							"torrent_id", c.TorrentID, "err", err)
					}
				}
			}
		}
	}()
}
