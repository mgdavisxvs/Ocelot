package tracker

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// ControlServerExt holds optional swarm-coordination dependencies.
// These fields are nil when the tracker is run without a policy controller.
// All handlers below return 501 when the field they require is nil.
type ControlServerExt struct {
	controller *SwarmPolicyController
	nodes      *NodeRegistry
	replicas   *NodeReplicaMap
	admission  *SwarmAdmissionPolicy
}

// AttachControllerDeps wires the swarm coordination layer into an existing
// ControlServer. Must be called before ListenAndServe (routes are registered
// at construction, so new handlers are added to the mux here).
//
// Call order in main.go:
//  1. cs = NewControlServer(config, worker)
//  2. cs.AttachControllerDeps(nodes, replicas, admission, controller)
//  3. go cs.ListenAndServe()
func (cs *ControlServer) AttachControllerDeps(
	nodes *NodeRegistry,
	replicas *NodeReplicaMap,
	admission *SwarmAdmissionPolicy,
	controller *SwarmPolicyController,
) {
	cs.ext = &ControlServerExt{
		controller: controller,
		nodes:      nodes,
		replicas:   replicas,
		admission:  admission,
	}
	// Register additional routes on the existing mux.
	mux := cs.httpSrv.Handler.(*http.ServeMux)
	mux.HandleFunc("/api/v1/nodes", cs.authMiddleware(cs.handleNodes))
	mux.HandleFunc("/api/v1/admission", cs.authMiddleware(cs.handleAdmission))
	mux.HandleFunc("/api/v1/compute/ensure", cs.authMiddleware(cs.handleComputeEnsure))
	mux.HandleFunc("/api/v1/replicas", cs.authMiddleware(cs.handleReplicas))
}

// ── /api/v1/nodes ─────────────────────────────────────────────────────────────

func (cs *ControlServer) handleNodes(w http.ResponseWriter, r *http.Request) {
	if cs.ext == nil || cs.ext.nodes == nil {
		writeErr(w, http.StatusNotImplemented, "swarm controller not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		cs.handleNodesList(w, r)
	case http.MethodPost:
		cs.handleNodeRegister(w, r)
	case http.MethodDelete:
		cs.handleNodeUnregister(w, r)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (cs *ControlServer) handleNodesList(w http.ResponseWriter, r *http.Request) {
	type nodeJSON struct {
		NodeID      uint64 `json:"node_id"`
		Hostname    string `json:"hostname"`
		LastIP      string `json:"last_ip"`
		Kind        string `json:"kind"`
		Unreachable bool   `json:"unreachable"`
		StorageFree int64  `json:"storage_free_bytes"`
		Rack        string `json:"rack,omitempty"`
		Site        string `json:"site,omitempty"`
	}
	nodes := cs.ext.nodes.ReachableNodes()
	resp := make([]nodeJSON, 0, len(nodes))
	for _, n := range nodes {
		n.mu.RLock()
		resp = append(resp, nodeJSON{
			NodeID:      n.NodeID,
			Hostname:    n.Hostname,
			LastIP:      n.LastIP,
			Kind:        n.Kind.String(),
			Unreachable: n.Unreachable,
			StorageFree: n.Caps.StorageFreeBytes,
			Rack:        n.Domains.Rack,
			Site:        n.Domains.Site,
		})
		n.mu.RUnlock()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (cs *ControlServer) handleNodeRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NodeID   uint64 `json:"node_id"`
		Hostname string `json:"hostname"`
		Passkey  string `json:"passkey"`
		Rack     string `json:"rack"`
		Site     string `json:"site"`
		Host     string `json:"host"`
		Network  string `json:"network"`
		Power    string `json:"power"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.NodeID == 0 || req.Hostname == "" {
		writeErr(w, http.StatusBadRequest, "node_id and hostname are required")
		return
	}
	n := NewNodeIdentity(req.NodeID, req.Hostname, req.Passkey, FailureDomainLabels{
		Host:    req.Host,
		Rack:    req.Rack,
		Site:    req.Site,
		Network: req.Network,
		Power:   req.Power,
	})
	cs.ext.nodes.Register(n)
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"status":  "ok",
		"node_id": req.NodeID,
		"message": "node registered",
	})
}

func (cs *ControlServer) handleNodeUnregister(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("node_id")
	if idStr == "" {
		writeErr(w, http.StatusBadRequest, "node_id query param required")
		return
	}
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid node_id")
		return
	}
	cs.ext.nodes.Unregister(id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "node unregistered"})
}

// ── /api/v1/admission ─────────────────────────────────────────────────────────

func (cs *ControlServer) handleAdmission(w http.ResponseWriter, r *http.Request) {
	if cs.ext == nil || cs.ext.admission == nil {
		writeErr(w, http.StatusNotImplemented, "swarm controller not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		infoHash := r.URL.Query().Get("info_hash")
		if infoHash == "" {
			writeErr(w, http.StatusBadRequest, "info_hash query param required")
			return
		}
		pks, isOpen := cs.ext.admission.AllowList(infoHash)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"info_hash": infoHash,
			"open":      isOpen,
			"allow":     pks,
		})

	case http.MethodPost:
		var req struct {
			InfoHash string   `json:"info_hash"`
			Passkeys []string `json:"passkeys"` // empty = open
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.InfoHash == "" {
			writeErr(w, http.StatusBadRequest, "info_hash required")
			return
		}
		cs.ext.admission.SetACL(req.InfoHash, req.Passkeys)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "ACL updated"})

	case http.MethodDelete:
		infoHash := r.URL.Query().Get("info_hash")
		if infoHash == "" {
			writeErr(w, http.StatusBadRequest, "info_hash query param required")
			return
		}
		passkey := r.URL.Query().Get("passkey")
		if passkey == "" {
			cs.ext.admission.SetOpen(infoHash)
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "ACL cleared"})
		} else {
			cs.ext.admission.RemovePasskey(infoHash, passkey)
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "passkey removed from ACL"})
		}

	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ── /api/v1/compute/ensure ────────────────────────────────────────────────────

func (cs *ControlServer) handleComputeEnsure(w http.ResponseWriter, r *http.Request) {
	if cs.ext == nil || cs.ext.controller == nil {
		writeErr(w, http.StatusNotImplemented, "swarm controller not configured")
		return
	}
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req EnsureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.InfoHash == "" || req.NodeID == 0 {
		writeErr(w, http.StatusBadRequest, "info_hash and node_id are required")
		return
	}
	result, err := cs.ext.controller.EnsureArtifact(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// ── /api/v1/replicas ─────────────────────────────────────────────────────────

func (cs *ControlServer) handleReplicas(w http.ResponseWriter, r *http.Request) {
	if cs.ext == nil || cs.ext.replicas == nil {
		writeErr(w, http.StatusNotImplemented, "swarm controller not configured")
		return
	}
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	infoHash := r.URL.Query().Get("info_hash")
	if infoHash == "" {
		writeErr(w, http.StatusBadRequest, "info_hash query param required")
		return
	}
	type replicaJSON struct {
		NodeID   uint64 `json:"node_id"`
		State    string `json:"state"`
		Class    string `json:"class"`
		Verified bool   `json:"verified"`
	}
	out := make([]replicaJSON, 0)
	cs.ext.replicas.ForHash(infoHash, func(rep *NodeReplica) bool {
		state := rep.GetState()
		out = append(out, replicaJSON{
			NodeID:   rep.NodeID,
			State:    state.String(),
			Class:    rep.Class.String(),
			Verified: state.CountsAsVerified(),
		})
		return true
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"info_hash": infoHash,
		"verified":  cs.ext.replicas.VerifiedCount(infoHash),
		"replicas":  out,
	})
}
