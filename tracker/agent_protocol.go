package tracker

import "time"

// AgentProtocol defines the HTTP API contract between the Ocelot controller
// and managed nodes running ocelot-agent.
//
// Transport: HTTP/1.1 (TLS when config provides a cert).
// Authentication: Bearer token matching the node's scoped passkey.
// Base path: /agent/v1/ (served by ocelot-agent, not the tracker).
//
// Controller → Agent directives are delivered as POST /agent/v1/directives.
// Agent → Controller heartbeats arrive as POST /api/v1/agent/heartbeat on the
// control server (:34001).

// ── Controller → Agent ────────────────────────────────────────────────────────

// DirectivePayload is the body sent to POST /agent/v1/directives.
type DirectivePayload struct {
	Directive string `json:"directive"`  // "JOIN_SWARM" | "RETIRE_REPLICA"
	InfoHash  string `json:"info_hash"`
	Class     string `json:"class,omitempty"` // replica class for JOIN_SWARM
}

// DirectiveResponse is the agent's immediate ACK for a directive.
type DirectiveResponse struct {
	Accepted bool   `json:"accepted"`
	Message  string `json:"message,omitempty"`
}

// ── Agent → Controller ────────────────────────────────────────────────────────

// HeartbeatPayload is sent by the agent to POST /api/v1/agent/heartbeat.
type HeartbeatPayload struct {
	NodeID          uint64             `json:"node_id"`
	Passkey         string             `json:"passkey"`
	Hostname        string             `json:"hostname"`
	StorageFree     int64              `json:"storage_free_bytes"`
	StorageTotal    int64              `json:"storage_total_bytes"`
	CPUCount        int                `json:"cpu_count"`
	MemoryBytes     int64              `json:"memory_bytes"`
	BTClientVersion string             `json:"bt_client_version"`
	Inventory       []ReplicaInventory `json:"inventory"`

	// S-E2: optional geographic position and ASN for proximity scoring.
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`
	ASN       uint32  `json:"asn,omitempty"` // autonomous system number (0 = unknown)

	// S-E3: upload bytes delta since last heartbeat (for WAN budget tracking).
	UploadDeltaBytes int64 `json:"upload_delta_bytes,omitempty"`

	SentAt time.Time `json:"sent_at"`
}

// ReplicaInventory is one entry in a heartbeat's artifact inventory.
// The agent reports per-torrent state from its BT client's perspective.
type ReplicaInventory struct {
	InfoHash    string  `json:"info_hash"`
	State       string  `json:"state"`       // agent-local state: "swarming","complete","seeding","invalid"
	VerifyState string  `json:"verify_state,omitempty"` // "hash_ok" | "hash_fail" | ""
	Progress    float64 `json:"progress"`    // 0.0–1.0 download fraction
	UploadedBytes int64 `json:"uploaded_bytes"`
}

// HeartbeatResponse is the controller's reply to an agent heartbeat.
type HeartbeatResponse struct {
	Accepted bool   `json:"accepted"`
	ServerAt time.Time `json:"server_at"`
}

// ── BTClient adapter interface ────────────────────────────────────────────────
//
// BTClient is the pluggable interface the ocelot-agent uses to drive whichever
// BitTorrent client is installed on the managed node.
// Implementations: QBittorrentClient, TransmissionClient, DelugeClient, EmbeddedClient.

// TorrentStatus is the normalized representation of one torrent from a BT client.
type TorrentStatus struct {
	InfoHash      string
	State         string  // "downloading" | "seeding" | "paused" | "error" | "checking"
	Progress      float64 // 0.0–1.0
	UploadedBytes int64
	DownloadedBytes int64
	Seeds         int
	Leechers      int
}

// BTClient is the interface every BT client adapter must implement.
// All methods must be safe for concurrent use.
type BTClient interface {
	// AddTorrent instructs the client to join the swarm for infoHash using
	// the provided magnet link or .torrent file URL.
	AddTorrent(infoHash, magnetOrURL string) error

	// RemoveTorrent removes the torrent and (when deleteData is true) deletes
	// local data.
	RemoveTorrent(infoHash string, deleteData bool) error

	// Status returns the current status of all managed torrents.
	// Implementations must return ErrTorrentNotFound for unknown hashes.
	Status(infoHash string) (TorrentStatus, error)

	// ListAll returns status for every torrent the client knows about.
	ListAll() ([]TorrentStatus, error)

	// Version returns the BT client version string (e.g. "qbittorrent/4.6.2").
	Version() string
}
