package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
	"github.com/mgdavisxvs/Ocelot/virtualserver/store"
)

// HandlerStore is the subset of VSStore the HTTP handlers require.
type HandlerStore interface {
	// Namespaces
	CreateNamespace(ctx context.Context, name, description string) (int64, error)
	ListNamespaces(ctx context.Context) ([]string, error)

	// Nodes
	CreateNode(ctx context.Context, n domain.Node) (string, error)
	GetNode(ctx context.Context, id string) (*domain.Node, error)
	ListNodes(ctx context.Context, stateFilter string) ([]domain.Node, error)
	UpdateNodeState(ctx context.Context, id string, to domain.NodeState) error

	// Services
	CreateService(ctx context.Context, m domain.ServiceManifest) (int64, error)
	GetService(ctx context.Context, id int64) (*domain.Service, error)
	ListServices(ctx context.Context, namespace string) ([]domain.Service, error)
	DeleteService(ctx context.Context, id int64) error

	// Instances
	GetInstance(ctx context.Context, id string) (*domain.ServiceInstance, error)
	ListInstancesByService(ctx context.Context, serviceID int64) ([]domain.ServiceInstance, error)
	ListInstances(ctx context.Context, stateFilter string) ([]domain.ServiceInstance, error)
}

// Handlers groups all VS HTTP route handlers.
type Handlers struct {
	store HandlerStore
}

// NewHandlers creates a Handlers bound to the given store.
func NewHandlers(s HandlerStore) *Handlers {
	return &Handlers{store: s}
}

// ── Namespace handlers ────────────────────────────────────────────────────────

// POST /v1/namespaces
func (h *Handlers) CreateNamespace(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := domain.ValidateSegment(req.Name); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid namespace: "+err.Error(), "INVALID_NAME")
		return
	}
	if _, err := h.store.CreateNamespace(r.Context(), req.Name, ""); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, r, http.StatusConflict, "namespace already exists", "CONFLICT")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
}

// GET /v1/namespaces
func (h *Handlers) ListNamespaces(w http.ResponseWriter, r *http.Request) {
	ns, err := h.store.ListNamespaces(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"namespaces": ns})
}

// ── Node handlers ─────────────────────────────────────────────────────────────

// POST /v1/nodes
func (h *Handlers) RegisterNode(w http.ResponseWriter, r *http.Request) {
	var req RegisterNodeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" || req.Arch == "" {
		writeError(w, r, http.StatusBadRequest, "name and arch are required", "INVALID_REQUEST")
		return
	}

	n := domain.Node{
		Name:        req.Name,
		BackendType: req.BackendType,
		Arch:        req.Arch,
		OS:          req.OS,
		CPUThreads:  req.CPUThreads,
		TotalRAMMiB: req.TotalRAMMiB,
		AvailRAMMiB: req.TotalRAMMiB,
		Labels:      req.Labels,
		State:       domain.NodeDiscovered,
	}
	for _, g := range req.GPUDevices {
		n.GPUDevices = append(n.GPUDevices, domain.GPUDevice{
			Index:   g.Index,
			Vendor:  g.Vendor,
			Model:   g.Model,
			VRAMMiB: g.VRAMMiB,
		})
	}

	id, err := h.store.CreateNode(r.Context(), n)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, r, http.StatusConflict, "node name already registered", "CONFLICT")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	n.ID = id
	writeJSON(w, http.StatusCreated, nodeToResponse(n))
}

// GET /v1/nodes
func (h *Handlers) ListNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := h.store.ListNodes(r.Context(), "")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	resp := make([]NodeResponse, len(nodes))
	for i, n := range nodes {
		resp[i] = nodeToResponse(n)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"nodes": resp})
}

// GET /v1/nodes/{id}
func (h *Handlers) GetNode(w http.ResponseWriter, r *http.Request) {
	id := pathSegment(r.URL.Path, "nodes")
	if id == "" {
		writeError(w, r, http.StatusBadRequest, "missing node id", "INVALID_REQUEST")
		return
	}
	n, err := h.store.GetNode(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "node not found", "NOT_FOUND")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	writeJSON(w, http.StatusOK, nodeToResponse(*n))
}

// PUT /v1/nodes/{id}/state
func (h *Handlers) UpdateNodeState(w http.ResponseWriter, r *http.Request) {
	id := pathSegmentBefore(r.URL.Path, "state")
	if id == "" {
		writeError(w, r, http.StatusBadRequest, "missing node id", "INVALID_REQUEST")
		return
	}
	var req UpdateNodeStateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := h.store.UpdateNodeState(r.Context(), id, domain.NodeState(req.State)); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "node not found", "NOT_FOUND")
			return
		}
		writeError(w, r, http.StatusUnprocessableEntity, "state transition not allowed", "INVALID_TRANSITION")
		return
	}
	n, _ := h.store.GetNode(r.Context(), id)
	if n != nil {
		writeJSON(w, http.StatusOK, nodeToResponse(*n))
	} else {
		w.WriteHeader(http.StatusOK)
	}
}

// ── Service handlers ──────────────────────────────────────────────────────────

// POST /v1/services
func (h *Handlers) DeclareService(w http.ResponseWriter, r *http.Request) {
	var req DeclareServiceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := req.Manifest.Validate(); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error(), "INVALID_MANIFEST")
		return
	}
	id, err := h.store.CreateService(r.Context(), req.Manifest)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, r, http.StatusConflict, "service already declared", "CONFLICT")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	svc, err := h.store.GetService(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
		return
	}
	writeJSON(w, http.StatusCreated, serviceToResponse(*svc))
}

// GET /v1/services
func (h *Handlers) ListServices(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	svcs, err := h.store.ListServices(r.Context(), ns)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	resp := make([]ServiceResponse, len(svcs))
	for i, s := range svcs {
		resp[i] = serviceToResponse(s)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"services": resp})
}

// GET /v1/services/{id}
func (h *Handlers) GetService(w http.ResponseWriter, r *http.Request) {
	idStr := pathSegment(r.URL.Path, "services")
	if idStr == "" {
		writeError(w, r, http.StatusBadRequest, "missing service id", "INVALID_REQUEST")
		return
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "service id must be numeric", "INVALID_REQUEST")
		return
	}
	svc, err := h.store.GetService(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "service not found", "NOT_FOUND")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	writeJSON(w, http.StatusOK, serviceToResponse(*svc))
}

// DELETE /v1/services/{id}
func (h *Handlers) DeleteService(w http.ResponseWriter, r *http.Request) {
	idStr := pathSegment(r.URL.Path, "services")
	if idStr == "" {
		writeError(w, r, http.StatusBadRequest, "missing service id", "INVALID_REQUEST")
		return
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "service id must be numeric", "INVALID_REQUEST")
		return
	}
	if err := h.store.DeleteService(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "service not found", "NOT_FOUND")
			return
		}
		writeError(w, r, http.StatusConflict, "service has active instances", "CONFLICT")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Instance handlers ─────────────────────────────────────────────────────────

// GET /v1/instances
func (h *Handlers) ListInstances(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	insts, err := h.store.ListInstances(r.Context(), state)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	resp := make([]InstanceResponse, len(insts))
	for i, inst := range insts {
		resp[i] = instanceToResponse(inst)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"instances": resp})
}

// GET /v1/instances/{id}
func (h *Handlers) GetInstance(w http.ResponseWriter, r *http.Request) {
	id := pathSegment(r.URL.Path, "instances")
	if id == "" {
		writeError(w, r, http.StatusBadRequest, "missing instance id", "INVALID_REQUEST")
		return
	}
	inst, err := h.store.GetInstance(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "instance not found", "NOT_FOUND")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	writeJSON(w, http.StatusOK, instanceToResponse(*inst))
}

// GET /v1/services/{id}/instances
func (h *Handlers) ListServiceInstances(w http.ResponseWriter, r *http.Request) {
	idStr := pathSegmentBefore(r.URL.Path, "instances")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "service id must be numeric", "INVALID_REQUEST")
		return
	}
	insts, err := h.store.ListInstancesByService(r.Context(), id)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	resp := make([]InstanceResponse, len(insts))
	for i, inst := range insts {
		resp[i] = instanceToResponse(inst)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"instances": resp})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// decodeJSON decodes the request body into v. Returns false and writes an error if it fails.
func decodeJSON(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid JSON: "+err.Error(), "INVALID_JSON")
		return false
	}
	return true
}

// pathSegment extracts the segment following "/<collection>/" in the URL path.
// e.g. pathSegment("/v1/nodes/abc", "nodes") → "abc"
func pathSegment(path, collection string) string {
	needle := "/" + collection + "/"
	idx := strings.Index(path, needle)
	if idx < 0 {
		return ""
	}
	rest := path[idx+len(needle):]
	// Return only the next segment
	if i := strings.Index(rest, "/"); i >= 0 {
		return rest[:i]
	}
	return rest
}

// pathSegmentBefore returns the segment immediately before "/<suffix>" in path.
// e.g. pathSegmentBefore("/v1/nodes/abc/state", "state") → "abc"
func pathSegmentBefore(path, suffix string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, p := range parts {
		if p == suffix && i > 0 {
			return parts[i-1]
		}
	}
	return ""
}
