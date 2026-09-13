package config

import (
	"encoding/json"
	"os"
)

type Config struct {
	// SQLite — path to the active ocelot shard (e.g. /var/lib/ocelot/ocelot-2026-09.db)
	DBPath string `json:"db_path"`

	// Shared decay factor — kept for backwards compatibility.
	// Chain-specific factors below take precedence when set.
	DecayFactor      float64 `json:"decay_factor"`
	DecayEveryNPolls int     `json:"decay_every_n_polls"`

	// Chain-specific decay factors (UMM-02).
	// Each chain operates at a different behavioral timescale; a single shared λ
	// is statistically incorrect. Half-life reference at 900s model clock:
	//   peer:    λ=0.993 → ~24.6h  (fast: swarm composition changes quickly)
	//   torrent: λ=0.995 → ~34.6h  (medium: health bands shift over days)
	//   user:    λ=0.998 → ~86.5h  (slow: ratio dynamics span weeks)
	// Validate and tune against measured calibration before promoting from SHADOW.
	PeerDecayFactor    float64 `json:"peer_decay_factor"`
	TorrentDecayFactor float64 `json:"torrent_decay_factor"`
	UserDecayFactor    float64 `json:"user_decay_factor"`

	SmoothingAlpha     float64 `json:"smoothing_alpha"`      // Bayesian/Laplace prior α (default 1.0)
	PollIntervalSec    int     `json:"poll_interval_sec"`    // how often to fetch data from DB
	PersistIntervalSec int     `json:"persist_interval_sec"` // how often to flush state to DB
	PathHistoryLen     int     `json:"path_history_len"`     // per-user state path length for fraud

	// Fixed model clock — Markov step independent of announce/poll frequency.
	ModelClockSec int `json:"model_clock_sec"` // default 900 (15 min)

	// Ocelot timing — must match ocelot.conf values
	AnnounceIntervalSec int `json:"announce_interval_sec"` // announce_interval (default 1800)
	PeersTimeoutSec     int `json:"peers_timeout_sec"`     // peers_timeout (default 7200)

	// Adaptive announce interval bounds (FR-007, Req 5)
	MinAnnounceIntervalSec int     `json:"min_announce_interval_sec"` // floor (default 600)
	MaxAnnounceIntervalSec int     `json:"max_announce_interval_sec"` // ceiling (default 3600)
	HysteresisFactor       float64 `json:"hysteresis_factor"`         // fractional change required to update (default 0.1)

	// MinEvidenceForAdaptiveInterval (UMM-05).
	// Effective sample count threshold below which entropy is not trusted to
	// shorten the announce interval. High entropy from sparse data is
	// qualitatively different from high entropy in a well-observed process —
	// acting on it increases model-clock noise rather than detecting uncertainty.
	// Default 20.0. Set to 0 to disable (pure entropy-driven intervals).
	MinEvidenceForAdaptiveInterval float64 `json:"min_evidence_for_adaptive_interval"`

	// Forecast step counts relative to model clock (Req 4)
	ForecastSteps1h  int `json:"forecast_steps_1h"`
	ForecastSteps6h  int `json:"forecast_steps_6h"`
	ForecastSteps24h int `json:"forecast_steps_24h"`
	ForecastSteps72h int `json:"forecast_steps_72h"`

	// Fraud detection (Req 6)
	FraudThresholdSigma float64 `json:"fraud_threshold_sigma"` // anomaly MAD z-score cutoff (default 3.0)
	FraudPathMinLen     int     `json:"fraud_path_min_len"`    // minimum path length before scoring

	// Beta-Binomial peer quality scorer
	SuccessThresholdBytes int `json:"success_threshold_bytes"` // min uploaded bytes for a seeding success (default 1024)

	// Freeleech
	FreeleechTopN int `json:"freeleech_top_n"` // max candidates to write to DB

	// Shadow mode (Req 8) — when true, forecasts and recommendations are
	// recorded in the audit log but do NOT affect tracker behavior.
	ShadowMode bool `json:"shadow_mode"`

	// Model governance (Req 7)
	ModelSchemaVersion int `json:"model_schema_version"` // bumped when state schema changes (default 1)

	// HTTP API
	ListenAddr string `json:"listen_addr"` // e.g. ":9090"
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	cfg.applyDefaults()
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.DBPath == "" {
		c.DBPath = "/var/lib/ocelot/ocelot.db"
	}
	if c.DecayFactor == 0 {
		c.DecayFactor = 0.995
	}
	if c.DecayEveryNPolls == 0 {
		c.DecayEveryNPolls = 60
	}
	// Chain-specific decay defaults (UMM-02).
	if c.PeerDecayFactor == 0 {
		c.PeerDecayFactor = 0.993 // ~24.6h half-life at 900s clock
	}
	if c.TorrentDecayFactor == 0 {
		c.TorrentDecayFactor = 0.995 // ~34.6h half-life
	}
	if c.UserDecayFactor == 0 {
		c.UserDecayFactor = 0.998 // ~86.5h half-life
	}
	if c.SmoothingAlpha == 0 {
		c.SmoothingAlpha = 1.0
	}
	if c.PollIntervalSec == 0 {
		c.PollIntervalSec = 60
	}
	if c.PersistIntervalSec == 0 {
		c.PersistIntervalSec = 300
	}
	if c.PathHistoryLen == 0 {
		c.PathHistoryLen = 10
	}
	if c.ModelClockSec == 0 {
		c.ModelClockSec = 900
	}
	if c.AnnounceIntervalSec == 0 {
		c.AnnounceIntervalSec = 1800
	}
	if c.PeersTimeoutSec == 0 {
		c.PeersTimeoutSec = 7200
	}
	if c.MinAnnounceIntervalSec == 0 {
		c.MinAnnounceIntervalSec = 600
	}
	if c.MaxAnnounceIntervalSec == 0 {
		c.MaxAnnounceIntervalSec = 3600
	}
	if c.HysteresisFactor == 0 {
		c.HysteresisFactor = 0.1
	}
	if c.MinEvidenceForAdaptiveInterval == 0 {
		c.MinEvidenceForAdaptiveInterval = 20.0
	}
	clock := c.ModelClockSec
	if clock <= 0 {
		clock = 900
	}
	if c.ForecastSteps1h == 0 {
		c.ForecastSteps1h = (1 * 3600) / clock
	}
	if c.ForecastSteps6h == 0 {
		c.ForecastSteps6h = (6 * 3600) / clock
	}
	if c.ForecastSteps24h == 0 {
		c.ForecastSteps24h = (24 * 3600) / clock
	}
	if c.ForecastSteps72h == 0 {
		c.ForecastSteps72h = (72 * 3600) / clock
	}
	if c.FraudThresholdSigma == 0 {
		c.FraudThresholdSigma = 3.0
	}
	if c.FraudPathMinLen == 0 {
		c.FraudPathMinLen = 4
	}
	if c.SuccessThresholdBytes == 0 {
		c.SuccessThresholdBytes = 1024
	}
	if c.FreeleechTopN == 0 {
		c.FreeleechTopN = 20
	}
	if c.ModelSchemaVersion == 0 {
		c.ModelSchemaVersion = 1
	}
	if c.ListenAddr == "" {
		c.ListenAddr = ":9090"
	}
}
