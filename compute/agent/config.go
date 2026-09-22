package agent

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
)

// Config holds all agent configuration. Values come from flags or a JSON file.
type Config struct {
	ControlPlaneURL string            `json:"control_plane_url"`
	BootstrapToken  string            `json:"bootstrap_token"`
	DataDir         string            `json:"data_dir"`
	DisplayName     string            `json:"display_name,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	TLSSkipVerify   bool              `json:"tls_skip_verify"` // never true in production
}

func ParseFlags() (*Config, error) {
	cfg := &Config{
		DataDir: "/var/lib/ocelot-agent",
		Labels:  make(map[string]string),
	}

	var configFile string
	var labelPairs string

	flag.StringVar(&configFile, "config", "", "path to JSON config file")
	flag.StringVar(&cfg.ControlPlaneURL, "control-plane", "", "control plane base URL (e.g. https://ocelot.home.internal)")
	flag.StringVar(&cfg.BootstrapToken, "bootstrap-token", "", "bootstrap token for first registration")
	flag.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "persistent data directory")
	flag.StringVar(&cfg.DisplayName, "display-name", "", "human-readable node name")
	flag.StringVar(&labelPairs, "labels", "", "comma-separated key=value labels (e.g. rack=01,tier=compute)")
	flag.BoolVar(&cfg.TLSSkipVerify, "tls-skip-verify", false, "disable TLS verification (NEVER use in production)")
	flag.Parse()

	if configFile != "" {
		if err := cfg.loadFile(configFile); err != nil {
			return nil, err
		}
	}

	// Allow env var overrides for secrets (safer than command-line flags).
	if v := os.Getenv("OCELOT_CONTROL_PLANE"); v != "" && cfg.ControlPlaneURL == "" {
		cfg.ControlPlaneURL = v
	}
	if v := os.Getenv("OCELOT_BOOTSTRAP_TOKEN"); v != "" && cfg.BootstrapToken == "" {
		cfg.BootstrapToken = v
	}

	if labelPairs != "" {
		for _, pair := range strings.Split(labelPairs, ",") {
			parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
			if len(parts) == 2 {
				cfg.Labels[parts[0]] = parts[1]
			}
		}
	}

	if cfg.ControlPlaneURL == "" {
		return nil, fmt.Errorf("control plane URL is required (--control-plane or OCELOT_CONTROL_PLANE)")
	}
	cfg.ControlPlaneURL = strings.TrimRight(cfg.ControlPlaneURL, "/")

	return cfg, nil
}

func (c *Config) loadFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("config: read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, c); err != nil {
		return fmt.Errorf("config: parse %s: %w", path, err)
	}
	if c.Labels == nil {
		c.Labels = make(map[string]string)
	}
	return nil
}
