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

// SeederPageRank calls GET /pagerank and returns a map of user-level PageRank
// scores computed by the Markov engine from seeder upload/ratio behaviour.
func (c *MarkovClient) SeederPageRank(ctx context.Context) (map[UserID]float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/pagerank", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("markov /pagerank: HTTP %d", resp.StatusCode)
	}
	// Markov returns {"user_id": score, ...} — keys are string-encoded integers.
	var raw map[string]float64
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make(map[UserID]float64, len(raw))
	for k, v := range raw {
		var id int64
		if _, err := fmt.Sscanf(k, "%d", &id); err == nil {
			out[UserID(id)] = v
		}
	}
	return out, nil
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
