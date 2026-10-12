package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const credentialsFile = "credentials.json"

type credentials struct {
	NodeID     string `json:"node_id"`
	NodeSecret string `json:"node_secret"`
}

func credPath(dataDir string) string {
	return filepath.Join(dataDir, credentialsFile)
}

func loadCredentials(dataDir string) (*credentials, error) {
	data, err := os.ReadFile(credPath(dataDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("storage: read credentials: %w", err)
	}
	var c credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("storage: parse credentials: %w", err)
	}
	if c.NodeID == "" || c.NodeSecret == "" {
		return nil, nil
	}
	return &c, nil
}

func saveCredentials(dataDir string, c *credentials) error {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("storage: mkdir %s: %w", dataDir, err)
	}
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("storage: marshal credentials: %w", err)
	}
	path := credPath(dataDir)
	// Write to a temp file then rename for atomicity.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("storage: write credentials: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("storage: rename credentials: %w", err)
	}
	return nil
}
