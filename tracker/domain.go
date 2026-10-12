package tracker

import (
	"net"
	"net/http"
	"time"
)

// Role classifies a Participant's relationship to a Resource.
type Role uint8

const (
	RoleConsumer  Role = iota // downloads / consumes
	RoleProvider              // uploads / produces
	RoleNeutral               // contributes both equally
	RoleObserver              // spectator only
)

// EventType classifies an Event lifecycle transition.
type EventType uint8

const (
	EventTypeJoin      EventType = iota // first contact / started
	EventTypeProgress                   // ongoing delta report
	EventTypeComplete                   // finished consuming
	EventTypeWithdraw                   // graceful departure
	EventTypeHeartbeat                  // no change, keep-alive
)

// AccessPolicy governs whether a Participant may consume.
type AccessPolicy uint8

const (
	PolicyOpen        AccessPolicy = iota // anyone may consume
	PolicyRestricted                      // must have explicit grant
	PolicyBlocked                         // consuming forbidden
)

// Delta holds the numeric changes reported in a single Event.
type Delta struct {
	Produced  int64 // bytes uploaded / work contributed
	Consumed  int64 // bytes downloaded / work received
	Remaining int64 // bytes left / work pending
	Corrupt   int64 // corrupt data detected
}

// DomainEvent is the generic equivalent of AnnounceRequest.
// Every domain maps its wire format into this struct.
type DomainEvent struct {
	ResourceKey string    // info_hash or domain equivalent
	AgentID     []byte    // peer_id or domain equivalent (opaque token)
	Port        uint16    // listen port
	Delta       Delta     // numeric changes since last event
	Type        EventType // join / progress / complete / withdraw / heartbeat
	IP          net.IP    // reported IP (may be overridden by server)
	NumWant     int32     // how many peers to return
	Compact     bool      // BEP-23 compact flag; ignored for JSON domains
	UserAgent   string    // HTTP User-Agent header
}

// Participant is the generic equivalent of Peer.
type Participant struct {
	UserID         UserID
	Produced       int64
	Consumed       int64
	Corrupt        int64
	Remaining      int64
	LastSeen       time.Time
	FirstSeen      time.Time
	Events         uint32
	Port           uint16
	IP             net.IP
	IPPort         []byte
	Role           Role
	Visible        bool
	InvalidIP      bool
}

// EventResponse is the generic equivalent of AnnounceResponse.
type EventResponse struct {
	Interval    int32
	MinInterval int32
	Providers   int32  // seeders count
	Consumers   int32  // leechers count
	Peers       []byte // compact 6-byte peer list
	Warning     string
}

// QueryEntry is one resource in a QueryResponse (scrape equivalent).
type QueryEntry struct {
	ResourceKey string
	Providers   int32
	Consumers   int32
	Completed   uint32
}

// QueryResponse is the generic equivalent of a scrape response.
type QueryResponse struct {
	Entries []QueryEntry
}

// ClientOpts carries per-request auth tokens parsed from the URL path.
type ClientOpts struct {
	Passkey   string
	UserAgent string
	ClientIP  net.IP
}

// DomainAdapter is the seam between the generic tracker core and a
// domain-specific wire protocol. Register one per domain.
type DomainAdapter interface {
	// DomainName is the unique slug used in routing (e.g. "bittorrent").
	DomainName() string

	// EventAction returns the URL path segment that triggers an event
	// (e.g. "announce"). Server routes /<passkey>/<EventAction()> here.
	EventAction() string

	// QueryAction returns the URL path segment that triggers a query
	// (e.g. "scrape"). Empty string disables query handling.
	QueryAction() string

	// ParseEvent converts an HTTP request into a domain-neutral DomainEvent.
	ParseEvent(req *http.Request, opts ClientOpts) (*DomainEvent, error)

	// ParseQuery converts an HTTP request into resource keys to query.
	ParseQuery(req *http.Request, opts ClientOpts) ([]string, error)

	// ValidateAgent returns true if the agent token is permitted.
	// peerID is the raw AgentID bytes from the Event.
	ValidateAgent(agentID []byte) bool

	// FormatEventResponse serializes an EventResponse to the wire format.
	FormatEventResponse(resp *EventResponse, httpClose bool) []byte

	// FormatQueryResponse serializes a QueryResponse to the wire format.
	FormatQueryResponse(resp *QueryResponse, httpClose bool) []byte

	// FormatError serializes an error message to the wire format.
	FormatError(msg string, httpClose bool) []byte

	// RoleNames returns human-readable labels for [consumer, provider].
	RoleNames() [2]string
}
