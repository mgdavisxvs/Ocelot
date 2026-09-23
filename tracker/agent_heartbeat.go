package tracker

import (
	"encoding/json"
	"net/http"
	"time"
)

// RegisterAgentRoutes wires the agent heartbeat endpoint into the control server's mux.
// Must be called after NewControlServer and before ListenAndServe.
// Requires AttachControllerDeps to have been called first (nodes must be non-nil).
func (cs *ControlServer) RegisterAgentRoutes() {
	mux := cs.httpSrv.Handler.(*http.ServeMux)
	mux.HandleFunc("/api/v1/agent/heartbeat", cs.authMiddleware(cs.handleAgentHeartbeat))
	mux.HandleFunc("/api/v1/agent/state", cs.authMiddleware(cs.handleAgentStateUpdate))
}

// handleAgentHeartbeat processes POST /api/v1/agent/heartbeat.
// The agent sends this on every heartbeat interval with its node identity,
// capabilities, and artifact inventory.
func (cs *ControlServer) handleAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if cs.ext == nil || cs.ext.nodes == nil {
		writeErr(w, http.StatusNotImplemented, "swarm controller not configured")
		return
	}

	var payload HeartbeatPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if payload.NodeID == 0 {
		writeErr(w, http.StatusBadRequest, "node_id required")
		return
	}

	node, ok := cs.ext.nodes.Get(payload.NodeID)
	if !ok {
		writeErr(w, http.StatusNotFound, "node not registered; call POST /api/v1/nodes first")
		return
	}

	caps := NodeCapabilities{
		StorageFreeBytes:  payload.StorageFree,
		StorageTotalBytes: payload.StorageTotal,
		CPUCount:          payload.CPUCount,
		MemoryBytes:       payload.MemoryBytes,
		BTClientVersion:   payload.BTClientVersion,
	}
	geo := GeoCoord{Lat: payload.Latitude, Lon: payload.Longitude}
	node.HeartbeatWithGeo(clientIP(r), caps, geo, payload.ASN)

	// S-E3: Record WAN upload delta toward daily budget.
	if payload.UploadDeltaBytes > 0 {
		node.mu.Lock()
		node.WAN.RecordUpload(payload.UploadDeltaBytes)
		node.mu.Unlock()
	}

	// Reconcile reported inventory against replica state map.
	if cs.ext.replicas != nil {
		for _, inv := range payload.Inventory {
			cs.reconcileInventory(payload.NodeID, inv)
		}
	}

	writeJSON(w, http.StatusOK, HeartbeatResponse{
		Accepted: true,
		ServerAt: time.Now(),
	})
}

// reconcileInventory advances the per-node replica FSM based on agent-reported state.
func (cs *ControlServer) reconcileInventory(nodeID uint64, inv ReplicaInventory) {
	r, ok := cs.ext.replicas.Get(nodeID, inv.InfoHash)
	if !ok {
		return
	}
	current := r.GetState()
	switch inv.State {
	case "swarming":
		if current == ReplicaStateRequested {
			r.Transition(ReplicaStateSwarming) //nolint:errcheck
		}
	case "complete":
		if current == ReplicaStateSwarming {
			r.Transition(ReplicaStateBTComplete) //nolint:errcheck
		}
		if current == ReplicaStateBTComplete {
			r.Transition(ReplicaStateHashVerify) //nolint:errcheck
		}
	case "seeding":
		if current == ReplicaStateHashVerify {
			switch inv.VerifyState {
			case "hash_ok":
				r.Transition(ReplicaStateVerified)  //nolint:errcheck
				r.Transition(ReplicaStateSeeding)   //nolint:errcheck
			case "hash_fail":
				r.Transition(ReplicaStateInvalid) //nolint:errcheck
			}
		} else if current == ReplicaStateVerified {
			r.Transition(ReplicaStateSeeding) //nolint:errcheck
		}
	case "invalid", "error":
		if current != ReplicaStateAbsent && current != ReplicaStatePurge {
			r.Transition(ReplicaStateInvalid) //nolint:errcheck
		}
	}
}

// handleAgentStateUpdate processes POST /api/v1/agent/state — a lighter-weight
// endpoint for the agent to push a single replica state transition without a
// full heartbeat (e.g., hash verification complete).
func (cs *ControlServer) handleAgentStateUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if cs.ext == nil || cs.ext.replicas == nil {
		writeErr(w, http.StatusNotImplemented, "swarm controller not configured")
		return
	}

	var req struct {
		NodeID   uint64 `json:"node_id"`
		InfoHash string `json:"info_hash"`
		State    string `json:"state"`       // new state name
		VerifyOK *bool  `json:"verify_ok"`   // optional hash result
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.NodeID == 0 || req.InfoHash == "" || req.State == "" {
		writeErr(w, http.StatusBadRequest, "node_id, info_hash, and state are required")
		return
	}

	inv := ReplicaInventory{
		InfoHash: req.InfoHash,
		State:    req.State,
	}
	if req.VerifyOK != nil {
		if *req.VerifyOK {
			inv.VerifyState = "hash_ok"
		} else {
			inv.VerifyState = "hash_fail"
		}
	}
	cs.reconcileInventory(req.NodeID, inv)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// clientIP extracts the remote IP from the request, stripping the port.
func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == ':' {
			return host[:i]
		}
	}
	return host
}
