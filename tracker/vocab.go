package tracker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// VocabularyConfig is the root JSON config for a domain.
// It lets any deployer rename all tracker concepts without writing Go.
//
// Example file: domains/bittorrent.json
type VocabularyConfig struct {
	// Domain slug, used in routing and metrics labels.
	Domain string `json:"domain"`

	// Human-readable display name.
	DisplayName string `json:"display_name"`

	Actions ActionVocab `json:"actions"`

	// Resource is the "torrent" equivalent.
	Resource ResourceVocab `json:"resource"`

	// Participant is the "peer" equivalent.
	Participant ParticipantVocab `json:"participant"`

	Roles RoleVocab `json:"roles"`

	// Delta labels for event numeric fields.
	Delta DeltaVocab `json:"delta"`

	// Event lifecycle labels.
	EventTypes EventTypeVocab `json:"event_types"`

	// Access policy labels.
	Policy PolicyVocab `json:"policy"`

	// Auth configuration.
	Auth AuthVocab `json:"auth"`

	// Wire format: "bencode" or "json".
	WireFormat WireFormatVocab `json:"wire_format"`

	// Metrics domain label used in Prometheus.
	Metrics MetricsVocab `json:"metrics"`
}

// ActionVocab names the URL path segments for event and query endpoints.
type ActionVocab struct {
	// URL segment that maps to an event (default "announce").
	Event string `json:"event"`
	// URL segment that maps to a query (default "scrape"). Empty to disable.
	Query string `json:"query"`
}

// ResourceVocab names the "torrent" concept in this domain.
type ResourceVocab struct {
	// Singular noun, lowercase (e.g. "torrent", "initiative", "task").
	Noun string `json:"noun"`
	// Plural noun (e.g. "torrents", "initiatives", "tasks").
	Plural string `json:"plural"`
	// HTTP key used when listing resources (e.g. "info_hash").
	KeyParam string `json:"key_param"`
}

// ParticipantVocab names the "peer" concept in this domain.
type ParticipantVocab struct {
	Noun   string `json:"noun"`
	Plural string `json:"plural"`
}

// RoleVocab names the consumer and provider roles.
type RoleVocab struct {
	Consumer string `json:"consumer"` // e.g. "leecher", "reader", "participant"
	Provider string `json:"provider"` // e.g. "seeder", "author", "owner"
}

// DeltaVocab maps generic delta fields to domain language.
type DeltaVocab struct {
	Produced  string `json:"produced"`  // e.g. "uploaded", "contributed", "points_earned"
	Consumed  string `json:"consumed"`  // e.g. "downloaded", "received", "points_spent"
	Remaining string `json:"remaining"` // e.g. "left", "pending", "outstanding"
	Corrupt   string `json:"corrupt"`   // e.g. "corrupt", "invalid", "rejected"
}

// EventTypeVocab maps lifecycle stages to domain labels.
type EventTypeVocab struct {
	Join      string `json:"join"`      // e.g. "started", "registered", "enrolled"
	Progress  string `json:"progress"`  // e.g. "", "progressed", "checked_in"
	Complete  string `json:"complete"`  // e.g. "completed", "finished", "graduated"
	Withdraw  string `json:"withdraw"`  // e.g. "stopped", "withdrew", "resigned"
	Heartbeat string `json:"heartbeat"` // e.g. "", "heartbeat", "ping"
}

// PolicyVocab names access states.
type PolicyVocab struct {
	Open       string `json:"open"`
	Restricted string `json:"restricted"`
	Blocked    string `json:"blocked"`
}

// AuthVocab governs how client identity is validated.
type AuthVocab struct {
	// Validation strategy: "prefix_trie" (BT peer_id whitelist), "jwt_claim", "none".
	Strategy string `json:"strategy"`

	// For "prefix_trie": list of allowed peer_id prefixes.
	AllowedPrefixes []string `json:"allowed_prefixes,omitempty"`

	// For "jwt_claim": the claim name that must be present.
	JWTClaim string `json:"jwt_claim,omitempty"`

	// PasskeyLength is the expected passkey/token length in URL path (default 32).
	PasskeyLength int `json:"passkey_length,omitempty"`
}

// WireFormatVocab governs encoding on the wire.
type WireFormatVocab struct {
	// "bencode" for BitTorrent, "json" for all other domains.
	Format string `json:"format"`
}

// MetricsVocab governs Prometheus label values for this domain.
type MetricsVocab struct {
	// Value for the `domain` label in all metrics (e.g. "bittorrent").
	DomainLabel string `json:"domain_label"`
}

// Validate checks required fields and sets defaults.
func (v *VocabularyConfig) Validate() error {
	if v.Domain == "" {
		return fmt.Errorf("vocab: domain slug is required")
	}
	if v.Actions.Event == "" {
		v.Actions.Event = "announce"
	}
	if v.Resource.Noun == "" {
		v.Resource.Noun = "resource"
	}
	if v.Resource.Plural == "" {
		v.Resource.Plural = v.Resource.Noun + "s"
	}
	if v.Resource.KeyParam == "" {
		v.Resource.KeyParam = "info_hash"
	}
	if v.Participant.Noun == "" {
		v.Participant.Noun = "participant"
	}
	if v.Participant.Plural == "" {
		v.Participant.Plural = v.Participant.Noun + "s"
	}
	if v.Roles.Consumer == "" {
		v.Roles.Consumer = "consumer"
	}
	if v.Roles.Provider == "" {
		v.Roles.Provider = "provider"
	}
	if v.Delta.Produced == "" {
		v.Delta.Produced = "produced"
	}
	if v.Delta.Consumed == "" {
		v.Delta.Consumed = "consumed"
	}
	if v.Delta.Remaining == "" {
		v.Delta.Remaining = "remaining"
	}
	if v.Delta.Corrupt == "" {
		v.Delta.Corrupt = "corrupt"
	}
	if v.EventTypes.Join == "" {
		v.EventTypes.Join = "join"
	}
	if v.EventTypes.Complete == "" {
		v.EventTypes.Complete = "complete"
	}
	if v.EventTypes.Withdraw == "" {
		v.EventTypes.Withdraw = "withdraw"
	}
	if v.Auth.Strategy == "" {
		v.Auth.Strategy = "none"
	}
	if v.Auth.PasskeyLength == 0 {
		v.Auth.PasskeyLength = 32
	}
	if v.WireFormat.Format == "" {
		v.WireFormat.Format = "json"
	}
	if v.Metrics.DomainLabel == "" {
		v.Metrics.DomainLabel = v.Domain
	}
	return nil
}

// EventTypeFromString maps a domain event string to an EventType constant.
// Returns EventTypeHeartbeat for unrecognized strings (empty event field).
func (v *VocabularyConfig) EventTypeFromString(s string) EventType {
	s = strings.ToLower(s)
	switch {
	case s == strings.ToLower(v.EventTypes.Join):
		return EventTypeJoin
	case s == strings.ToLower(v.EventTypes.Complete):
		return EventTypeComplete
	case s == strings.ToLower(v.EventTypes.Withdraw):
		return EventTypeWithdraw
	case v.EventTypes.Progress != "" && s == strings.ToLower(v.EventTypes.Progress):
		return EventTypeProgress
	case v.EventTypes.Heartbeat != "" && s == strings.ToLower(v.EventTypes.Heartbeat):
		return EventTypeHeartbeat
	default:
		return EventTypeHeartbeat
	}
}

// LoadVocabularyConfig reads and validates a single JSON vocab file.
func LoadVocabularyConfig(path string) (*VocabularyConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("vocab: read %s: %w", path, err)
	}
	var cfg VocabularyConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("vocab: parse %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// LoadAllVocabConfigs reads every *.json file in dir and returns a slice
// of validated configs. Non-JSON files are silently skipped.
func LoadAllVocabConfigs(dir string) ([]*VocabularyConfig, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("vocab: list %s: %w", dir, err)
	}

	var configs []*VocabularyConfig
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		cfg, err := LoadVocabularyConfig(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		configs = append(configs, cfg)
	}
	return configs, nil
}
