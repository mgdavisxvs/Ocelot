package config

import (
	"encoding/json"
	"os"
)

type Config struct {
	// MySQL — must match ocelot.conf mysql_* settings
	DBHost string `json:"db_host"`
	DBPort int    `json:"db_port"`
	DBName string `json:"db_name"`
	DBUser string `json:"db_user"`
	DBPass string `json:"db_pass"`

	// Markov engine tuning
	DecayFactor        float64 `json:"decay_factor"`          // per-poll counts decay (e.g. 0.995)
	DecayEveryNPolls   int     `json:"decay_every_n_polls"`   // apply decay every N polls
	PollIntervalSec    int     `json:"poll_interval_sec"`     // how often to observe transitions
	PersistIntervalSec int     `json:"persist_interval_sec"`  // how often to flush state to DB
	PathHistoryLen     int     `json:"path_history_len"`      // per-user state path length for fraud

	// Ocelot timing — must match ocelot.conf values
	AnnounceIntervalSec int `json:"announce_interval_sec"` // announce_interval (default 1800)
	PeersTimeoutSec     int `json:"peers_timeout_sec"`     // peers_timeout (default 7200)

	// Predictions
	ForecastSteps24h int `json:"forecast_steps_24h"` // k for pi_24h (steps = 24h/poll_interval)
	ForecastSteps72h int `json:"forecast_steps_72h"` // k for pi_72h

	// Fraud detection
	FraudThresholdSigma float64 `json:"fraud_threshold_sigma"` // anomaly z-score cutoff (default 3.0)
	FraudPathMinLen     int     `json:"fraud_path_min_len"`    // minimum path length before scoring

	// Freeleech
	FreeleechTopN int `json:"freeleech_top_n"` // max candidates to write to DB

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
	if c.DBHost == "" {
		c.DBHost = "127.0.0.1"
	}
	if c.DBPort == 0 {
		c.DBPort = 3306
	}
	if c.DBName == "" {
		c.DBName = "gazelle"
	}
	if c.DecayFactor == 0 {
		c.DecayFactor = 0.995
	}
	if c.DecayEveryNPolls == 0 {
		c.DecayEveryNPolls = 60 // decay every hour if poll=60s
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
	if c.AnnounceIntervalSec == 0 {
		c.AnnounceIntervalSec = 1800
	}
	if c.PeersTimeoutSec == 0 {
		c.PeersTimeoutSec = 7200
	}
	if c.ForecastSteps24h == 0 {
		c.ForecastSteps24h = (24 * 3600) / c.PollIntervalSec
	}
	if c.ForecastSteps72h == 0 {
		c.ForecastSteps72h = (72 * 3600) / c.PollIntervalSec
	}
	if c.FraudThresholdSigma == 0 {
		c.FraudThresholdSigma = 3.0
	}
	if c.FraudPathMinLen == 0 {
		c.FraudPathMinLen = 4
	}
	if c.FreeleechTopN == 0 {
		c.FreeleechTopN = 20
	}
	if c.ListenAddr == "" {
		c.ListenAddr = ":9090"
	}
}
