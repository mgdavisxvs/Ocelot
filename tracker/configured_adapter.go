package tracker

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// ConfiguredAdapter implements DomainAdapter from a VocabularyConfig.
// No Go code is needed to add a new domain — only a JSON file.
type ConfiguredAdapter struct {
	vocab   *VocabularyConfig
	wl      *Whitelist // only populated for prefix_trie strategy
	server  *Server    // back-reference for response helpers
}

// NewConfiguredAdapter constructs an adapter for the given vocab config.
// wl may be nil for non-BT domains that use "none" auth strategy.
func NewConfiguredAdapter(vocab *VocabularyConfig, wl *Whitelist, srv *Server) *ConfiguredAdapter {
	return &ConfiguredAdapter{vocab: vocab, wl: wl, server: srv}
}

func (a *ConfiguredAdapter) DomainName() string  { return a.vocab.Domain }
func (a *ConfiguredAdapter) EventAction() string  { return a.vocab.Actions.Event }
func (a *ConfiguredAdapter) QueryAction() string  { return a.vocab.Actions.Query }
func (a *ConfiguredAdapter) RoleNames() [2]string { return [2]string{a.vocab.Roles.Consumer, a.vocab.Roles.Provider} }

// ValidateAgent checks agentID against the configured auth strategy.
func (a *ConfiguredAdapter) ValidateAgent(agentID []byte) bool {
	switch a.vocab.Auth.Strategy {
	case "prefix_trie":
		if a.wl == nil {
			return true
		}
		return a.wl.IsAllowed(agentID)
	case "none":
		return true
	default:
		return true
	}
}

// ParseEvent converts an HTTP request to a domain-neutral Event.
// Bencode domains use URL query params (BitTorrent); JSON domains use body.
func (a *ConfiguredAdapter) ParseEvent(req *http.Request, opts ClientOpts) (*DomainEvent, error) {
	if a.vocab.WireFormat.Format == "bencode" {
		return a.parseBencodeEvent(req, opts)
	}
	return a.parseJSONEvent(req, opts)
}

func (a *ConfiguredAdapter) parseBencodeEvent(req *http.Request, opts ClientOpts) (*DomainEvent, error) {
	params := req.URL.Query()
	v := a.vocab

	keyParam := v.Resource.KeyParam
	resourceKey := params.Get(keyParam)
	if resourceKey == "" {
		return nil, fmt.Errorf("missing %s", keyParam)
	}

	peerIDStr := params.Get("peer_id")
	if peerIDStr == "" {
		return nil, fmt.Errorf("missing peer_id")
	}
	agentID := []byte(peerIDStr)

	portStr := params.Get("port")
	if portStr == "" {
		return nil, fmt.Errorf("missing port")
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("invalid port")
	}

	// Use vocab-defined query parameter names; fall back to BT defaults.
	producedKey := "uploaded"
	if v.Delta.Produced != "" {
		producedKey = v.Delta.Produced
	}
	consumedKey := "downloaded"
	if v.Delta.Consumed != "" {
		consumedKey = v.Delta.Consumed
	}
	remainingKey := "left"
	if v.Delta.Remaining != "" {
		remainingKey = v.Delta.Remaining
	}
	corruptKey := "corrupt"
	if v.Delta.Corrupt != "" {
		corruptKey = v.Delta.Corrupt
	}

	produced := parseInt64(params.Get(producedKey))
	consumed := parseInt64(params.Get(consumedKey))
	remaining := parseInt64(params.Get(remainingKey))
	corrupt := parseInt64(params.Get(corruptKey))

	eventStr := params.Get("event")
	eventType := v.EventTypeFromString(eventStr)

	numwant := int32(parseInt64(params.Get("numwant")))
	compact := params.Get("compact") == "1"

	var ip net.IP
	if ipParam := params.Get("ip"); ipParam != "" {
		ip = net.ParseIP(ipParam)
	} else if ipv4Param := params.Get("ipv4"); ipv4Param != "" {
		ip = net.ParseIP(ipv4Param)
	}
	if ip == nil {
		ip = opts.ClientIP
	}

	return &DomainEvent{
		ResourceKey: resourceKey,
		AgentID:     agentID,
		Port:        uint16(port),
		Delta: Delta{
			Produced:  produced,
			Consumed:  consumed,
			Remaining: remaining,
			Corrupt:   corrupt,
		},
		Type:      eventType,
		IP:        ip,
		NumWant:   numwant,
		Compact:   compact,
		UserAgent: opts.UserAgent,
	}, nil
}

// jsonEventRequest is the expected JSON body for non-BT domains.
type jsonEventRequest struct {
	ResourceKey string  `json:"resource_key"`
	AgentID     string  `json:"agent_id"`
	Port        uint16  `json:"port"`
	Produced    int64   `json:"produced"`
	Consumed    int64   `json:"consumed"`
	Remaining   int64   `json:"remaining"`
	Corrupt     int64   `json:"corrupt"`
	Event       string  `json:"event"`
	NumWant     int32   `json:"num_want"`
	IP          string  `json:"ip,omitempty"`
}

func (a *ConfiguredAdapter) parseJSONEvent(req *http.Request, opts ClientOpts) (*DomainEvent, error) {
	var body jsonEventRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if body.ResourceKey == "" {
		return nil, fmt.Errorf("missing resource_key")
	}
	if body.AgentID == "" {
		return nil, fmt.Errorf("missing agent_id")
	}

	var ip net.IP
	if body.IP != "" {
		ip = net.ParseIP(body.IP)
	}
	if ip == nil {
		ip = opts.ClientIP
	}

	return &DomainEvent{
		ResourceKey: body.ResourceKey,
		AgentID:     []byte(body.AgentID),
		Port:        body.Port,
		Delta: Delta{
			Produced:  body.Produced,
			Consumed:  body.Consumed,
			Remaining: body.Remaining,
			Corrupt:   body.Corrupt,
		},
		Type:      a.vocab.EventTypeFromString(body.Event),
		IP:        ip,
		NumWant:   body.NumWant,
		Compact:   false,
		UserAgent: opts.UserAgent,
	}, nil
}

// ParseQuery extracts resource keys for a query (scrape equivalent).
func (a *ConfiguredAdapter) ParseQuery(req *http.Request, opts ClientOpts) ([]string, error) {
	param := a.vocab.Resource.KeyParam
	keys := req.URL.Query()[param]
	if len(keys) == 0 {
		return nil, fmt.Errorf("no %s parameters", param)
	}
	return keys, nil
}

// FormatEventResponse encodes the response in the domain wire format.
func (a *ConfiguredAdapter) FormatEventResponse(resp *EventResponse, httpClose bool) []byte {
	if a.vocab.WireFormat.Format == "bencode" {
		return a.formatBencodeEventResponse(resp, httpClose)
	}
	return a.formatJSONEventResponse(resp, httpClose)
}

func (a *ConfiguredAdapter) formatBencodeEventResponse(resp *EventResponse, httpClose bool) []byte {
	var b strings.Builder
	b.Grow(350)
	b.WriteString("d8:completei")
	b.WriteString(strconv.FormatInt(int64(resp.Providers), 10))
	b.WriteString("e10:incompletei")
	b.WriteString(strconv.FormatInt(int64(resp.Consumers), 10))
	b.WriteString("e8:intervali")
	b.WriteString(strconv.FormatInt(int64(resp.Interval), 10))
	b.WriteString("e12:min intervali")
	b.WriteString(strconv.FormatInt(int64(resp.MinInterval), 10))
	b.WriteString("e5:peers")
	if len(resp.Peers) == 0 {
		b.WriteString("0:")
	} else {
		b.WriteString(strconv.Itoa(len(resp.Peers)))
		b.WriteString(":")
		b.Write(resp.Peers)
	}
	if resp.Warning != "" {
		b.WriteString("15:warning message")
		b.WriteString(strconv.Itoa(len(resp.Warning)))
		b.WriteString(":")
		b.WriteString(resp.Warning)
	}
	b.WriteString("e")
	return a.server.response(b.String(), httpClose, false)
}

func (a *ConfiguredAdapter) formatJSONEventResponse(resp *EventResponse, httpClose bool) []byte {
	v := a.vocab
	body := map[string]interface{}{
		"interval":     resp.Interval,
		"min_interval": resp.MinInterval,
		v.Roles.Provider + "s": resp.Providers,
		v.Roles.Consumer + "s": resp.Consumers,
		"peer_count":   len(resp.Peers) / 6,
	}
	if resp.Warning != "" {
		body["warning"] = resp.Warning
	}
	data, _ := json.Marshal(body)
	return a.server.jsonResponse(data, httpClose)
}

// FormatQueryResponse encodes a query (scrape) response.
func (a *ConfiguredAdapter) FormatQueryResponse(resp *QueryResponse, httpClose bool) []byte {
	if a.vocab.WireFormat.Format == "bencode" {
		return a.formatBencodeQueryResponse(resp, httpClose)
	}
	return a.formatJSONQueryResponse(resp, httpClose)
}

func (a *ConfiguredAdapter) formatBencodeQueryResponse(resp *QueryResponse, httpClose bool) []byte {
	var b strings.Builder
	b.WriteString("d5:filesd")
	for _, e := range resp.Entries {
		b.WriteString(fmt.Sprintf("%d:%s", len(e.ResourceKey), e.ResourceKey))
		b.WriteString(fmt.Sprintf("d8:completei%de10:incompletei%de10:downloadedi%dee",
			e.Providers, e.Consumers, e.Completed))
	}
	b.WriteString("ee")
	return a.server.response(b.String(), httpClose, false)
}

func (a *ConfiguredAdapter) formatJSONQueryResponse(resp *QueryResponse, httpClose bool) []byte {
	v := a.vocab
	entries := make([]map[string]interface{}, 0, len(resp.Entries))
	for _, e := range resp.Entries {
		entries = append(entries, map[string]interface{}{
			"key":                    e.ResourceKey,
			v.Roles.Provider + "s":  e.Providers,
			v.Roles.Consumer + "s":  e.Consumers,
			"completed":              e.Completed,
		})
	}
	data, _ := json.Marshal(map[string]interface{}{v.Resource.Plural: entries})
	return a.server.jsonResponse(data, httpClose)
}

// FormatError encodes an error in the domain wire format.
func (a *ConfiguredAdapter) FormatError(msg string, httpClose bool) []byte {
	if a.vocab.WireFormat.Format == "bencode" {
		resp := fmt.Sprintf("d14:failure reason%d:%se", len(msg), msg)
		return a.server.response(resp, httpClose, false)
	}
	data, _ := json.Marshal(map[string]string{"error": msg})
	return a.server.jsonResponse(data, httpClose)
}

// EventToAnnounceRequest converts a domain-neutral Event back to the
// BT-specific AnnounceRequest so the existing Worker.Announce can be reused.
func EventToAnnounceRequest(e *DomainEvent, v *VocabularyConfig) *AnnounceRequest {
	var eventStr string
	switch e.Type {
	case EventTypeJoin:
		eventStr = "started"
	case EventTypeComplete:
		eventStr = "completed"
	case EventTypeWithdraw:
		eventStr = "stopped"
	default:
		eventStr = ""
	}
	return &AnnounceRequest{
		InfoHash:   e.ResourceKey,
		PeerID:     e.AgentID,
		Port:       e.Port,
		Uploaded:   e.Delta.Produced,
		Downloaded: e.Delta.Consumed,
		Left:       e.Delta.Remaining,
		Corrupt:    e.Delta.Corrupt,
		Compact:    true, // always compact internally
		Event:      eventStr,
		IP:         e.IP,
		NumWant:    e.NumWant,
	}
}

// EventResponseFromAnnounce converts a BT AnnounceResponse to the generic type.
func EventResponseFromAnnounce(resp *AnnounceResponse) *EventResponse {
	return &EventResponse{
		Interval:    resp.Interval,
		MinInterval: resp.MinInterval,
		Providers:   resp.Complete,
		Consumers:   resp.Incomplete,
		Peers:       resp.Peers,
		Warning:     resp.Warning,
	}
}
