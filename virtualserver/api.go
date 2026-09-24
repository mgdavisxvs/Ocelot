package virtualserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mgdavisxvs/Ocelot/virtualserver/storage"
)

// APIServer is the HTTP control-plane for the virtual server.
type APIServer struct {
	db         *db
	adapters   *AdapterRegistry
	storage    *storage.Registry
	metrics    *VSMetrics
	bearerToken string
	mux        *http.ServeMux
}

func newAPIServer(
	d *db,
	adapters *AdapterRegistry,
	stor *storage.Registry,
	metrics *VSMetrics,
	bearerToken string,
) *APIServer {
	a := &APIServer{
		db: d, adapters: adapters, storage: stor,
		metrics: metrics, bearerToken: bearerToken,
		mux: http.NewServeMux(),
	}
	a.registerRoutes()
	return a
}

func (a *APIServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route, path := a.routeKey(r)
	t0 := time.Now()
	wrapped := &statusWriter{ResponseWriter: w, code: 200}
	a.mux.ServeHTTP(wrapped, r)
	a.metrics.APIRequests.WithLabelValues(r.Method, path, fmt.Sprintf("%d", wrapped.code)).Inc()
	a.metrics.APIDuration.WithLabelValues(r.Method, route).Observe(time.Since(t0).Seconds())
}

// routeKey returns the matched route pattern and normalised path for metrics.
func (a *APIServer) routeKey(r *http.Request) (route, path string) {
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/v1/namespaces/") && strings.Count(p, "/") == 3:
		return "/v1/namespaces/{name}", p
	case strings.HasPrefix(p, "/v1/nodes/") && strings.Count(p, "/") == 3:
		return "/v1/nodes/{id}", p
	case strings.HasPrefix(p, "/v1/services/") && strings.Count(p, "/") == 3:
		return "/v1/services/{id}", p
	case strings.HasPrefix(p, "/v1/instances/") && strings.Count(p, "/") == 3:
		return "/v1/instances/{id}", p
	case strings.HasPrefix(p, "/v1/instances/") && strings.HasSuffix(p, "/stop"):
		return "/v1/instances/{id}/stop", p
	case strings.HasPrefix(p, "/v1/operations/") && strings.Count(p, "/") == 3:
		return "/v1/operations/{id}", p
	case strings.HasPrefix(p, "/v1/volumes/") && strings.HasSuffix(p, "/snapshot"):
		return "/v1/volumes/{id}/snapshot", p
	case strings.HasPrefix(p, "/v1/volumes/") && strings.Contains(p, "/restore/"):
		return "/v1/volumes/{id}/restore/{snapshot_id}", p
	case strings.HasPrefix(p, "/v1/volumes/") && strings.Count(p, "/") == 3:
		return "/v1/volumes/{id}", p
	default:
		return p, p
	}
}

func (a *APIServer) registerRoutes() {
	a.mux.HandleFunc("/v1/namespaces", a.auth(a.handleNamespaces))
	a.mux.HandleFunc("/v1/namespaces/", a.auth(a.handleNamespace))
	a.mux.HandleFunc("/v1/nodes", a.auth(a.handleNodes))
	a.mux.HandleFunc("/v1/nodes/", a.auth(a.handleNode))
	a.mux.HandleFunc("/v1/services", a.auth(a.handleServices))
	a.mux.HandleFunc("/v1/services/", a.auth(a.handleService))
	a.mux.HandleFunc("/v1/instances", a.auth(a.handleInstances))
	a.mux.HandleFunc("/v1/instances/", a.auth(a.handleInstance))
	a.mux.HandleFunc("/v1/operations/", a.auth(a.handleOperation))
	a.mux.HandleFunc("/v1/volumes", a.auth(a.handleVolumes))
	a.mux.HandleFunc("/v1/volumes/", a.auth(a.handleVolume))
}

// ── auth middleware ───────────────────────────────────────────────────────────

func (a *APIServer) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.bearerToken != "" {
			hdr := r.Header.Get("Authorization")
			token := strings.TrimPrefix(hdr, "Bearer ")
			if token != a.bearerToken {
				writeErr(w, http.StatusUnauthorized, "invalid or missing bearer token")
				return
			}
		}
		next(w, r)
	}
}

// ── namespace handlers ────────────────────────────────────────────────────────

func (a *APIServer) handleNamespaces(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		nss, err := a.db.listNamespaces()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, nss)
	case http.MethodPost:
		var ns Namespace
		if err := json.NewDecoder(r.Body).Decode(&ns); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if ns.Name == "" {
			writeErr(w, http.StatusBadRequest, "name required")
			return
		}
		ns.CreatedAt = time.Now().UTC()
		if err := a.db.createNamespace(&ns); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.db.audit("api", "create_namespace", ns.Name, true, "")
		writeJSON(w, http.StatusCreated, ns)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "")
	}
}

func (a *APIServer) handleNamespace(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/v1/namespaces/")
	if name == "" {
		a.handleNamespaces(w, r)
		return
	}
	if r.Method == http.MethodDelete {
		if err := a.db.deleteNamespace(name); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.db.audit("api", "delete_namespace", name, true, "")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeErr(w, http.StatusMethodNotAllowed, "")
}

// ── node handlers ─────────────────────────────────────────────────────────────

func (a *APIServer) handleNodes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		nodes, err := a.db.listNodes()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, nodes)
	case http.MethodPost:
		var n Node
		if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if n.ID == "" {
			n.ID = uuid.NewString()
		}
		n.CreatedAt = time.Now().UTC()
		if n.LastHeartbeat.IsZero() {
			n.LastHeartbeat = n.CreatedAt
		}
		if n.State == "" {
			n.State = NodeStateReady
		}
		if err := a.db.upsertNode(&n); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.db.audit("api", "register_node", n.ID, true, n.Name)
		writeJSON(w, http.StatusCreated, n)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "")
	}
}

func (a *APIServer) handleNode(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/nodes/")
	if id == "" {
		a.handleNodes(w, r)
		return
	}
	// Strip sub-paths if any (e.g. /heartbeat).
	if idx := strings.Index(id, "/"); idx >= 0 {
		sub := id[idx+1:]
		id = id[:idx]
		if sub == "heartbeat" && r.Method == http.MethodPut {
			a.handleHeartbeat(w, r, id)
			return
		}
	}
	switch r.Method {
	case http.MethodGet:
		n, err := a.db.getNode(id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, n)
	case http.MethodPut:
		var n Node
		if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		n.ID = id
		if err := a.db.upsertNode(&n); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, n)
	case http.MethodDelete:
		if err := a.db.deleteNode(id); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.db.audit("api", "delete_node", id, true, "")
		w.WriteHeader(http.StatusNoContent)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "")
	}
}

func (a *APIServer) handleHeartbeat(w http.ResponseWriter, _ *http.Request, id string) {
	n, err := a.db.getNode(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	n.LastHeartbeat = time.Now().UTC()
	if err := a.db.upsertNode(n); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ── service handlers ──────────────────────────────────────────────────────────

func (a *APIServer) handleServices(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	switch r.Method {
	case http.MethodGet:
		svcs, err := a.db.listServices(ns)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, svcs)
	case http.MethodPost:
		var svc Service
		if err := json.NewDecoder(r.Body).Decode(&svc); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if svc.Namespace == "" {
			writeErr(w, http.StatusBadRequest, "namespace required")
			return
		}
		if svc.ID == "" {
			svc.ID = uuid.NewString()
		}
		now := time.Now().UTC()
		svc.CreatedAt = now
		svc.UpdatedAt = now
		if err := a.db.upsertService(&svc); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.db.audit("api", "create_service", svc.ID, true, svc.Name)
		writeJSON(w, http.StatusCreated, svc)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "")
	}
}

func (a *APIServer) handleService(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/services/")
	if id == "" {
		a.handleServices(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		svc, err := a.db.getService(id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, svc)
	case http.MethodPut:
		svc, err := a.db.getService(id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		var patch Service
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		patch.ID = id
		patch.Namespace = svc.Namespace
		patch.CreatedAt = svc.CreatedAt
		patch.UpdatedAt = time.Now().UTC()
		if err := a.db.upsertService(&patch); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, patch)
	case http.MethodDelete:
		if err := a.db.deleteService(id); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.db.audit("api", "delete_service", id, true, "")
		w.WriteHeader(http.StatusNoContent)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "")
	}
}

// ── instance handlers ─────────────────────────────────────────────────────────

func (a *APIServer) handleInstances(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	switch r.Method {
	case http.MethodGet:
		insts, err := a.db.listInstances(ns)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, insts)
	case http.MethodPost:
		var req struct {
			ServiceID string `json:"service_id"`
			Namespace string `json:"namespace"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		svc, err := a.db.getService(req.ServiceID)
		if err != nil {
			writeErr(w, http.StatusNotFound, fmt.Sprintf("service %q: %v", req.ServiceID, err))
			return
		}
		ns := req.Namespace
		if ns == "" {
			ns = svc.Namespace
		}
		now := time.Now().UTC()
		inst := &Instance{
			ID:        uuid.NewString(),
			ServiceID: svc.ID,
			Namespace: ns,
			State:     InstanceStateScheduled,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := a.db.upsertInstance(inst); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.db.audit("api", "create_instance", inst.ID, true, svc.Name)
		writeJSON(w, http.StatusCreated, inst)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "")
	}
}

func (a *APIServer) handleInstance(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/instances/")
	if rest == "" {
		a.handleInstances(w, r)
		return
	}
	// Detect sub-resource: /v1/instances/{id}/stop
	id, sub := rest, ""
	if idx := strings.Index(rest, "/"); idx >= 0 {
		id = rest[:idx]
		sub = rest[idx+1:]
	}
	if sub == "stop" && r.Method == http.MethodPost {
		inst, err := a.db.getInstance(id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		if inst.State != InstanceStateRunning && inst.State != InstanceStateStarting {
			writeErr(w, http.StatusConflict, fmt.Sprintf("instance in state %s cannot be stopped", inst.State))
			return
		}
		inst.State = InstanceStateStopping
		inst.UpdatedAt = time.Now().UTC()
		a.db.upsertInstance(inst)
		a.db.audit("api", "stop_instance", id, true, "")
		writeJSON(w, http.StatusAccepted, inst)
		return
	}
	switch r.Method {
	case http.MethodGet:
		inst, err := a.db.getInstance(id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, inst)
	case http.MethodDelete:
		inst, err := a.db.getInstance(id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		// If running, transition to stopping first.
		if inst.State == InstanceStateRunning || inst.State == InstanceStateStarting {
			inst.State = InstanceStateStopping
			inst.UpdatedAt = time.Now().UTC()
			a.db.upsertInstance(inst)
			a.db.audit("api", "delete_instance", id, true, "queued stop")
			writeJSON(w, http.StatusAccepted, inst)
			return
		}
		a.db.deleteInstance(id)
		a.db.audit("api", "delete_instance", id, true, "")
		w.WriteHeader(http.StatusNoContent)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "")
	}
}

// ── operation handlers ────────────────────────────────────────────────────────

func (a *APIServer) handleOperation(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/operations/")
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "")
		return
	}
	op, err := a.db.getOperation(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, op)
}

// ── volume handlers ───────────────────────────────────────────────────────────

func (a *APIServer) handleVolumes(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	switch r.Method {
	case http.MethodGet:
		vols, err := a.db.listVolumes(ns)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, vols)
	case http.MethodPost:
		var vol Volume
		if err := json.NewDecoder(r.Body).Decode(&vol); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if vol.Namespace == "" || vol.DriverName == "" {
			writeErr(w, http.StatusBadRequest, "namespace and driver_name required")
			return
		}
		drv, err := a.storage.Get(vol.DriverName)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("driver %q: %v", vol.DriverName, err))
			return
		}
		if vol.ID == "" {
			vol.ID = uuid.NewString()
		}
		vol.CreatedAt = time.Now().UTC()

		t0 := time.Now()
		handle, err := drv.Create(context.Background(), vol.ID, vol.SizeBytes)
		dur := time.Since(t0).Seconds()
		if err != nil {
			a.metrics.VolumeOps.WithLabelValues(vol.DriverName, "create", "error").Inc()
			a.metrics.VolumeDuration.WithLabelValues(vol.DriverName, "create").Observe(dur)
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.metrics.VolumeOps.WithLabelValues(vol.DriverName, "create", "ok").Inc()
		a.metrics.VolumeDuration.WithLabelValues(vol.DriverName, "create").Observe(dur)

		vol.Handle = string(handle)
		if err := a.db.upsertVolume(&vol); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.db.audit("api", "create_volume", vol.ID, true, vol.Name)
		writeJSON(w, http.StatusCreated, vol)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "")
	}
}

func (a *APIServer) handleVolume(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/volumes/")
	if rest == "" {
		a.handleVolumes(w, r)
		return
	}

	// Route sub-paths: /snapshot, /restore/{snapshotID}
	id, sub := rest, ""
	if idx := strings.Index(rest, "/"); idx >= 0 {
		id = rest[:idx]
		sub = rest[idx+1:]
	}

	if sub == "snapshot" && r.Method == http.MethodPost {
		a.handleVolumeSnapshot(w, r, id)
		return
	}
	if strings.HasPrefix(sub, "restore/") && r.Method == http.MethodPost {
		snapshotID := strings.TrimPrefix(sub, "restore/")
		a.handleVolumeRestore(w, r, id, snapshotID)
		return
	}
	if sub == "snapshots" && r.Method == http.MethodGet {
		snaps, err := a.db.listSnapshots(id)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, snaps)
		return
	}

	switch r.Method {
	case http.MethodGet:
		vol, err := a.db.getVolume(id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, vol)
	case http.MethodDelete:
		vol, err := a.db.getVolume(id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		drv, err := a.storage.Get(vol.DriverName)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		t0 := time.Now()
		err = drv.Delete(context.Background(), storage.VolumeHandle(vol.Handle))
		dur := time.Since(t0).Seconds()
		result := "ok"
		if err != nil {
			result = "error"
			a.metrics.VolumeOps.WithLabelValues(vol.DriverName, "delete", result).Inc()
			a.metrics.VolumeDuration.WithLabelValues(vol.DriverName, "delete").Observe(dur)
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.metrics.VolumeOps.WithLabelValues(vol.DriverName, "delete", result).Inc()
		a.metrics.VolumeDuration.WithLabelValues(vol.DriverName, "delete").Observe(dur)
		a.db.deleteVolume(id)
		a.db.audit("api", "delete_volume", id, true, "")
		w.WriteHeader(http.StatusNoContent)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "")
	}
}

func (a *APIServer) handleVolumeSnapshot(w http.ResponseWriter, r *http.Request, volumeID string) {
	vol, err := a.db.getVolume(volumeID)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	drv, err := a.storage.Get(vol.DriverName)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	opID := uuid.NewString()
	op := &Operation{
		ID: opID, Type: "snapshot", ResourceID: volumeID,
		State: OpStatePending, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	a.db.createOperation(op)

	go func() {
		a.db.updateOperation(opID, OpStateRunning, "")
		snapshotID := uuid.NewString()
		t0 := time.Now()
		snapHandle, err := drv.Snapshot(context.Background(), storage.VolumeHandle(vol.Handle))
		dur := time.Since(t0).Seconds()
		if err != nil {
			a.metrics.VolumeOps.WithLabelValues(vol.DriverName, "snapshot", "error").Inc()
			a.metrics.VolumeDuration.WithLabelValues(vol.DriverName, "snapshot").Observe(dur)
			a.db.updateOperation(opID, OpStateFailed, err.Error())
			return
		}
		a.metrics.VolumeOps.WithLabelValues(vol.DriverName, "snapshot", "ok").Inc()
		a.metrics.VolumeDuration.WithLabelValues(vol.DriverName, "snapshot").Observe(dur)

		snap := &Snapshot{
			ID: snapshotID, VolumeID: volumeID,
			SnapshotHandle: string(snapHandle),
			CreatedAt:      time.Now().UTC(),
		}
		a.db.createSnapshot(snap)
		a.db.audit("api", "snapshot_volume", volumeID, true, snapshotID)
		a.db.updateOperation(opID, OpStateSucceeded, snapshotID)
	}()

	writeJSON(w, http.StatusAccepted, op)
}

func (a *APIServer) handleVolumeRestore(w http.ResponseWriter, r *http.Request, volumeID, snapshotID string) {
	vol, err := a.db.getVolume(volumeID)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	snaps, err := a.db.listSnapshots(volumeID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var snap *Snapshot
	for _, s := range snaps {
		if s.ID == snapshotID {
			snap = s
			break
		}
	}
	if snap == nil {
		writeErr(w, http.StatusNotFound, fmt.Sprintf("snapshot %q not found", snapshotID))
		return
	}
	drv, err := a.storage.Get(vol.DriverName)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	opID := uuid.NewString()
	op := &Operation{
		ID: opID, Type: "restore", ResourceID: volumeID,
		State: OpStatePending, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	a.db.createOperation(op)

	go func() {
		a.db.updateOperation(opID, OpStateRunning, "")
		newVolumeID := uuid.NewString()
		t0 := time.Now()
		newHandle, err := drv.RestoreFrom(context.Background(), storage.VolumeHandle(snap.SnapshotHandle))
		dur := time.Since(t0).Seconds()
		if err != nil {
			a.metrics.VolumeOps.WithLabelValues(vol.DriverName, "restore", "error").Inc()
			a.metrics.VolumeDuration.WithLabelValues(vol.DriverName, "restore").Observe(dur)
			a.db.updateOperation(opID, OpStateFailed, err.Error())
			return
		}
		a.metrics.VolumeOps.WithLabelValues(vol.DriverName, "restore", "ok").Inc()
		a.metrics.VolumeDuration.WithLabelValues(vol.DriverName, "restore").Observe(dur)

		newVol := &Volume{
			ID:         newVolumeID,
			Namespace:  vol.Namespace,
			Name:       vol.Name + "-restored",
			DriverName: vol.DriverName,
			Handle:     string(newHandle),
			CreatedAt:  time.Now().UTC(),
		}
		a.db.upsertVolume(newVol)
		a.db.audit("api", "restore_volume", volumeID, true, newVolumeID)
		a.db.updateOperation(opID, OpStateSucceeded, newVolumeID)
	}()

	writeJSON(w, http.StatusAccepted, op)
}

// ── response helpers ──────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// statusWriter wraps http.ResponseWriter to capture the status code for metrics.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.code = code
	sw.ResponseWriter.WriteHeader(code)
}
