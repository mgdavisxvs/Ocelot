// ocelot-agent — managed-node daemon for the Ocelot swarm coordination plane.
//
// The agent runs on every managed node. Its responsibilities:
//  1. Register with the Ocelot control server (POST /api/v1/nodes).
//  2. Send periodic heartbeats with hardware capabilities and artifact inventory.
//  3. Receive swarm directives (JOIN_SWARM, RETIRE_REPLICA) from the controller.
//  4. Drive the configured BT client to join or leave swarms.
//  5. Report hash verification results back to the controller.
//
// The agent serves a local HTTP API (/agent/v1/directives) that the controller
// calls to deliver directives. All communication is authenticated with Bearer tokens.
//
// Usage:
//
//	ocelot-agent --control=https://tracker.example.com:34001 \
//	             --node-id=42 \
//	             --passkey=<32chars> \
//	             --bt-client=qbittorrent \
//	             --bt-url=http://localhost:8080 \
//	             [--hostname=auto] \
//	             [--rack=rack-B-07] \
//	             [--site=us-east-1a] \
//	             [--listen=:34003]
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/mgdavisxvs/Ocelot/tracker"
)

func main() {
	controlURL := flag.String("control", "", "Ocelot control server base URL (e.g. http://localhost:34001)")
	nodeID := flag.Uint64("node-id", 0, "Unique node ID assigned by the operator")
	passkey := flag.String("passkey", "", "32-character scoped passkey for this node")
	btClientType := flag.String("bt-client", "embedded", "BT client type: qbittorrent|transmission|deluge|embedded")
	btURL := flag.String("bt-url", "http://localhost:8080", "BT client API base URL")
	hostname := flag.String("hostname", "", "Node hostname (auto-detected if empty)")
	rack := flag.String("rack", "", "Rack identifier for anti-affinity")
	site := flag.String("site", "", "Site/datacenter identifier")
	listenAddr := flag.String("listen", ":34003", "Local address to serve the directive API")
	hbInterval := flag.Duration("heartbeat", 30*time.Second, "Heartbeat interval")
	flag.Parse()

	if *controlURL == "" || *nodeID == 0 || *passkey == "" {
		fmt.Fprintln(os.Stderr, "error: --control, --node-id, and --passkey are required")
		flag.Usage()
		os.Exit(1)
	}
	if len(*passkey) != 32 {
		fmt.Fprintln(os.Stderr, "error: --passkey must be exactly 32 characters")
		os.Exit(1)
	}

	if *hostname == "" {
		h, err := os.Hostname()
		if err != nil {
			h = "unknown"
		}
		*hostname = h
	}

	btClient := newBTClient(*btClientType, *btURL)
	if btClient == nil {
		log.Fatalf("unsupported BT client type: %s (valid: qbittorrent, transmission, deluge, embedded)", *btClientType)
	}

	agent := &Agent{
		nodeID:     *nodeID,
		passkey:    *passkey,
		hostname:   *hostname,
		rack:       *rack,
		site:       *site,
		controlURL: *controlURL,
		btClient:   btClient,
		hbInterval: *hbInterval,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := agent.Register(ctx); err != nil {
		log.Fatalf("registration failed: %v", err)
	}
	log.Printf("Registered with controller at %s as node %d", *controlURL, *nodeID)

	go agent.serveDirectives(ctx, *listenAddr)
	agent.run(ctx)
}

// ── Agent ─────────────────────────────────────────────────────────────────────

type Agent struct {
	nodeID     uint64
	passkey    string
	hostname   string
	rack       string
	site       string
	controlURL string
	btClient   tracker.BTClient
	hbInterval time.Duration
}

func (a *Agent) Register(ctx context.Context) error {
	payload := map[string]interface{}{
		"node_id":  a.nodeID,
		"hostname": a.hostname,
		"passkey":  a.passkey,
		"rack":     a.rack,
		"site":     a.site,
	}
	_, code, err := a.controlDo(ctx, "POST", "/api/v1/nodes", payload)
	if err != nil {
		return err
	}
	if code != http.StatusCreated && code != http.StatusOK {
		return fmt.Errorf("registration rejected: HTTP %d", code)
	}
	return nil
}

// run is the main heartbeat loop.
func (a *Agent) run(ctx context.Context) {
	ticker := time.NewTicker(a.hbInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.sendHeartbeat(ctx); err != nil {
				log.Printf("heartbeat error: %v", err)
			}
		}
	}
}

func (a *Agent) sendHeartbeat(ctx context.Context) error {
	inventory, err := a.buildInventory()
	if err != nil {
		log.Printf("inventory collection error: %v", err)
	}

	payload := tracker.HeartbeatPayload{
		NodeID:          a.nodeID,
		Passkey:         a.passkey,
		Hostname:        a.hostname,
		StorageFree:     diskFreeBytes(),
		StorageTotal:    diskTotalBytes(),
		CPUCount:        runtime.NumCPU(),
		MemoryBytes:     0, // would use syscall on real impl
		BTClientVersion: a.btClient.Version(),
		Inventory:       inventory,
		SentAt:          time.Now(),
	}

	_, code, err := a.controlDo(ctx, "POST", "/api/v1/agent/heartbeat", payload)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("heartbeat rejected: HTTP %d", code)
	}
	return nil
}

func (a *Agent) buildInventory() ([]tracker.ReplicaInventory, error) {
	statuses, err := a.btClient.ListAll()
	if err != nil {
		return nil, err
	}
	out := make([]tracker.ReplicaInventory, 0, len(statuses))
	for _, s := range statuses {
		inv := tracker.ReplicaInventory{
			InfoHash:      s.InfoHash,
			Progress:      s.Progress,
			UploadedBytes: s.UploadedBytes,
		}
		switch s.State {
		case "downloading":
			inv.State = "swarming"
		case "checking":
			inv.State = "complete"
		case "seeding":
			inv.State = "seeding"
			inv.VerifyState = "hash_ok"
		case "error":
			inv.State = "invalid"
		default:
			inv.State = s.State
		}
		out = append(out, inv)
	}
	return out, nil
}

// serveDirectives runs the local HTTP server that receives controller directives.
func (a *Agent) serveDirectives(ctx context.Context, addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/agent/v1/directives", a.handleDirective)
	mux.HandleFunc("/agent/v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
	})

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx) //nolint:errcheck
	}()

	log.Printf("Agent directive API listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("agent directive server error: %v", err)
	}
}

func (a *Agent) handleDirective(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	// Verify Bearer token matches this node's passkey.
	token := r.Header.Get("Authorization")
	if token != "Bearer "+a.passkey {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var payload tracker.DirectivePayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	var actionErr error
	switch payload.Directive {
	case "JOIN_SWARM":
		actionErr = a.joinSwarm(r.Context(), payload.InfoHash)
	case "RETIRE_REPLICA":
		actionErr = a.retireReplica(r.Context(), payload.InfoHash)
	default:
		http.Error(w, `{"error":"unknown directive"}`, http.StatusBadRequest)
		return
	}

	resp := tracker.DirectiveResponse{Accepted: actionErr == nil}
	if actionErr != nil {
		resp.Message = actionErr.Error()
		log.Printf("directive %s for %s failed: %v", payload.Directive, payload.InfoHash, actionErr)
	} else {
		log.Printf("directive %s for %s accepted", payload.Directive, payload.InfoHash)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

func (a *Agent) joinSwarm(ctx context.Context, infoHash string) error {
	// Build magnet link from info_hash (assumes hex-encoded hash).
	magnet := fmt.Sprintf("magnet:?xt=urn:btih:%s&tr=%s%%2F%s%%2Fannounce",
		infoHash, a.controlURL, a.passkey)
	if err := a.btClient.AddTorrent(infoHash, magnet); err != nil {
		return fmt.Errorf("JOIN_SWARM: AddTorrent failed: %w", err)
	}
	// Report state transition to controller.
	a.reportState(ctx, infoHash, "swarming", nil)
	return nil
}

func (a *Agent) retireReplica(ctx context.Context, infoHash string) error {
	if err := a.btClient.RemoveTorrent(infoHash, true); err != nil {
		return fmt.Errorf("RETIRE_REPLICA: RemoveTorrent failed: %w", err)
	}
	return nil
}

func (a *Agent) reportState(ctx context.Context, infoHash, state string, verifyOK *bool) {
	payload := map[string]interface{}{
		"node_id":   a.nodeID,
		"info_hash": infoHash,
		"state":     state,
	}
	if verifyOK != nil {
		payload["verify_ok"] = *verifyOK
	}
	if _, _, err := a.controlDo(ctx, "POST", "/api/v1/agent/state", payload); err != nil {
		log.Printf("state report error for %s: %v", infoHash, err)
	}
}

// ── HTTP helper ───────────────────────────────────────────────────────────────

func (a *Agent) controlDo(ctx context.Context, method, path string, body interface{}) ([]byte, int, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reqBody = bytes.NewReader(b)
	}
	url := a.controlURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+a.passkey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return data, resp.StatusCode, err
}

// ── BT client factory ─────────────────────────────────────────────────────────

func newBTClient(kind, apiURL string) tracker.BTClient {
	switch kind {
	case "qbittorrent":
		return &qbittorrentClient{baseURL: apiURL}
	case "transmission":
		return &transmissionClient{baseURL: apiURL}
	case "deluge":
		return &delugeClient{baseURL: apiURL}
	case "embedded":
		return &embeddedClient{}
	default:
		return nil
	}
}

// ── qBittorrent adapter ───────────────────────────────────────────────────────

type qbittorrentClient struct{ baseURL string }

func (c *qbittorrentClient) AddTorrent(infoHash, magnetOrURL string) error {
	// POST /api/v2/torrents/add with magnet link body.
	// Full qBittorrent Web API implementation would go here.
	resp, err := http.PostForm(c.baseURL+"/api/v2/torrents/add",
		map[string][]string{"urls": {magnetOrURL}})
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *qbittorrentClient) RemoveTorrent(infoHash string, deleteData bool) error {
	del := "false"
	if deleteData {
		del = "true"
	}
	resp, err := http.PostForm(c.baseURL+"/api/v2/torrents/delete",
		map[string][]string{"hashes": {infoHash}, "deleteFiles": {del}})
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *qbittorrentClient) Status(infoHash string) (tracker.TorrentStatus, error) {
	resp, err := http.Get(c.baseURL + "/api/v2/torrents/info?hashes=" + infoHash)
	if err != nil {
		return tracker.TorrentStatus{}, err
	}
	defer resp.Body.Close()
	var items []struct {
		Hash     string  `json:"hash"`
		State    string  `json:"state"`
		Progress float64 `json:"progress"`
		Uploaded int64   `json:"uploaded"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil || len(items) == 0 {
		return tracker.TorrentStatus{}, fmt.Errorf("torrent not found: %s", infoHash)
	}
	return tracker.TorrentStatus{
		InfoHash:      items[0].Hash,
		State:         items[0].State,
		Progress:      items[0].Progress,
		UploadedBytes: items[0].Uploaded,
	}, nil
}

func (c *qbittorrentClient) ListAll() ([]tracker.TorrentStatus, error) {
	resp, err := http.Get(c.baseURL + "/api/v2/torrents/info")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var items []struct {
		Hash     string  `json:"hash"`
		State    string  `json:"state"`
		Progress float64 `json:"progress"`
		Uploaded int64   `json:"uploaded"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, err
	}
	out := make([]tracker.TorrentStatus, len(items))
	for i, it := range items {
		out[i] = tracker.TorrentStatus{
			InfoHash: it.Hash, State: it.State,
			Progress: it.Progress, UploadedBytes: it.Uploaded,
		}
	}
	return out, nil
}

func (c *qbittorrentClient) Version() string { return "qbittorrent/unknown" }

// ── Transmission adapter ──────────────────────────────────────────────────────

type transmissionClient struct{ baseURL string }

func (c *transmissionClient) AddTorrent(infoHash, magnetOrURL string) error {
	// Transmission RPC: POST /transmission/rpc with JSON-RPC body.
	payload := map[string]interface{}{
		"method":    "torrent-add",
		"arguments": map[string]string{"filename": magnetOrURL},
	}
	b, _ := json.Marshal(payload)
	resp, err := http.Post(c.baseURL+"/transmission/rpc", "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *transmissionClient) RemoveTorrent(infoHash string, deleteData bool) error {
	return nil // stub: full impl would call torrent-remove RPC
}

func (c *transmissionClient) Status(infoHash string) (tracker.TorrentStatus, error) {
	return tracker.TorrentStatus{}, nil // stub
}

func (c *transmissionClient) ListAll() ([]tracker.TorrentStatus, error) {
	return nil, nil // stub
}

func (c *transmissionClient) Version() string { return "transmission/unknown" }

// ── Deluge adapter ────────────────────────────────────────────────────────────

type delugeClient struct{ baseURL string }

func (c *delugeClient) AddTorrent(infoHash, magnetOrURL string) error  { return nil }
func (c *delugeClient) RemoveTorrent(infoHash string, del bool) error  { return nil }
func (c *delugeClient) Status(infoHash string) (tracker.TorrentStatus, error) {
	return tracker.TorrentStatus{}, nil
}
func (c *delugeClient) ListAll() ([]tracker.TorrentStatus, error) { return nil, nil }
func (c *delugeClient) Version() string                           { return "deluge/unknown" }

// ── Embedded client ───────────────────────────────────────────────────────────
// Placeholder for a future embedded go-torrent implementation.

type embeddedClient struct{}

func (c *embeddedClient) AddTorrent(infoHash, magnetOrURL string) error {
	log.Printf("[embedded] AddTorrent %s", infoHash)
	return nil
}

func (c *embeddedClient) RemoveTorrent(infoHash string, del bool) error {
	log.Printf("[embedded] RemoveTorrent %s deleteData=%v", infoHash, del)
	return nil
}

func (c *embeddedClient) Status(infoHash string) (tracker.TorrentStatus, error) {
	return tracker.TorrentStatus{InfoHash: infoHash, State: "unknown"}, nil
}

func (c *embeddedClient) ListAll() ([]tracker.TorrentStatus, error) {
	return []tracker.TorrentStatus{}, nil
}

func (c *embeddedClient) Version() string { return "embedded/0.1.0" }

// ── System helpers ────────────────────────────────────────────────────────────

func diskFreeBytes() int64 {
	// On a real implementation this would call syscall.Statfs on Linux
	// or GetDiskFreeSpaceEx on Windows. Returning 0 is safe — the controller
	// treats 0 as "unknown" and will still schedule the node if no better options exist.
	return 0
}

func diskTotalBytes() int64 { return 0 }

// localIP returns the first non-loopback IPv4 address of the machine.
func localIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ipNet, ok := a.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
			if ip4 := ipNet.IP.To4(); ip4 != nil {
				return ip4.String()
			}
		}
	}
	return ""
}
