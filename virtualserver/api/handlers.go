package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
	"github.com/mgdavisxvs/Ocelot/virtualserver/storage"
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
	Heartbeat(ctx context.Context, id string, availRAMMiB int64, availCPU int, seq uint64) error

	// Services
	CreateService(ctx context.Context, m domain.ServiceManifest) (int64, error)
	GetService(ctx context.Context, id int64) (*domain.Service, error)
	ListServices(ctx context.Context, namespace string) ([]domain.Service, error)
	DeleteService(ctx context.Context, id int64) error
	UpdateService(ctx context.Context, id int64, m domain.ServiceManifest) error

	// Instances
	GetInstance(ctx context.Context, id string) (*domain.ServiceInstance, error)
	ListInstancesByService(ctx context.Context, serviceID int64) ([]domain.ServiceInstance, error)
	ListInstances(ctx context.Context, stateFilter string) ([]domain.ServiceInstance, error)

	// Volumes
	CreateVolume(ctx context.Context, v domain.Volume) (string, error)
	GetVolume(ctx context.Context, id string) (*domain.Volume, error)
	ListVolumes(ctx context.Context, namespace string) ([]domain.Volume, error)
	UpdateVolumeState(ctx context.Context, id string, to domain.VolumeState) error
	ListActiveMountsByVolume(ctx context.Context, volumeID string) ([]domain.VolumeMount, error)
	StartVolumeRelease(ctx context.Context, id string) error
	DeleteVolume(ctx context.Context, id string) error
	CreateSnapshot(ctx context.Context, snap domain.VolumeSnapshot) error
	GetSnapshot(ctx context.Context, id string) (*domain.VolumeSnapshot, error)
	ListSnapshots(ctx context.Context, volumeID string) ([]domain.VolumeSnapshot, error)
	UpdateSnapshotState(ctx context.Context, id string, state domain.SnapshotState, driverRef string, sizeMiB int64) error
	VerifySnapshotChain(ctx context.Context, volumeID string) (string, error)

	// Operations
	GetOperation(ctx context.Context, id string) (*domain.Operation, error)
	ListInstanceOperations(ctx context.Context, instanceID string) ([]domain.Operation, error)

	// Health observations (VS-F-T1: instance state trace)
	ListHealthObservations(ctx context.Context, instanceID string) ([]domain.HealthObservation, error)
}

// VolumeHandlerDrivers gives the volume handlers access to storage drivers
// so they can call Snapshot directly.
type VolumeHandlerDrivers interface {
	DriverForClass(class string) (storage.StorageDriver, bool)
}

// Handlers groups all VS HTTP route handlers.
type Handlers struct {
	store   HandlerStore
	drivers VolumeHandlerDrivers // may be nil when no storage classes configured
}

// NewHandlers creates a Handlers bound to the given store.
func NewHandlers(s HandlerStore) *Handlers {
	return &Handlers{store: s}
}

// NewHandlersWithDrivers creates a Handlers with access to storage drivers for snapshot calls.
func NewHandlersWithDrivers(s HandlerStore, d VolumeHandlerDrivers) *Handlers {
	return &Handlers{store: s, drivers: d}
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

// POST /v1/nodes/{id}/heartbeat
func (h *Handlers) NodeHeartbeat(w http.ResponseWriter, r *http.Request) {
	id := pathSegmentBefore(r.URL.Path, "heartbeat")
	if id == "" {
		writeError(w, r, http.StatusBadRequest, "missing node id", "INVALID_REQUEST")
		return
	}
	var req NodeHeartbeatRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := h.store.Heartbeat(r.Context(), id, req.AvailRAMMiB, req.AvailCPUThreads, req.Sequence); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "node not found", "NOT_FOUND")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	n, err := h.store.GetNode(r.Context(), id)
	if err != nil {
		w.WriteHeader(http.StatusOK)
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

// PUT /v1/services/{id}
func (h *Handlers) UpdateService(w http.ResponseWriter, r *http.Request) {
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
	var req UpdateServiceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := req.Manifest.Validate(); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error(), "INVALID_MANIFEST")
		return
	}
	if err := h.store.UpdateService(r.Context(), id, req.Manifest); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "service not found", "NOT_FOUND")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	svc, err := h.store.GetService(r.Context(), id)
	if err != nil {
		w.WriteHeader(http.StatusOK)
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

// ── Volume handlers ───────────────────────────────────────────────────────────

// POST /v1/volumes
func (h *Handlers) DeclareVolume(w http.ResponseWriter, r *http.Request) {
	var req DeclareVolumeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := req.Manifest.Validate(); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error(), "INVALID_MANIFEST")
		return
	}
	v := domain.Volume{
		ID:       uuid.New().String(),
		Manifest: req.Manifest,
		State:    domain.VolumeDeclared,
	}
	id, err := h.store.CreateVolume(r.Context(), v)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, r, http.StatusConflict, "volume already declared", "CONFLICT")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	v.ID = id
	v.CreatedAt = time.Now()
	v.UpdatedAt = v.CreatedAt
	writeJSON(w, http.StatusCreated, volumeToResponse(v))
}

// GET /v1/volumes
func (h *Handlers) ListVolumes(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	vols, err := h.store.ListVolumes(r.Context(), ns)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	resp := make([]VolumeResponse, len(vols))
	for i, v := range vols {
		resp[i] = volumeToResponse(v)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"volumes": resp})
}

// GET /v1/volumes/{id}
func (h *Handlers) GetVolume(w http.ResponseWriter, r *http.Request) {
	id := pathSegment(r.URL.Path, "volumes")
	if id == "" {
		writeError(w, r, http.StatusBadRequest, "missing volume id", "INVALID_REQUEST")
		return
	}
	v, err := h.store.GetVolume(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "volume not found", "NOT_FOUND")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	writeJSON(w, http.StatusOK, volumeToResponse(*v))
}

// DELETE /v1/volumes/{id}
// Transitions the volume to releasing then released (synchronous delete for admin use).
func (h *Handlers) DeleteVolume(w http.ResponseWriter, r *http.Request) {
	id := pathSegment(r.URL.Path, "volumes")
	if id == "" {
		writeError(w, r, http.StatusBadRequest, "missing volume id", "INVALID_REQUEST")
		return
	}
	// StartVolumeRelease atomically checks for active mounts and transitions to
	// releasing, preventing the TOCTOU race between mount-check and state update.
	if err := h.store.StartVolumeRelease(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, r, http.StatusConflict, "volume has active mounts", "CONFLICT")
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "volume not found", "NOT_FOUND")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// GET /v1/volumes/{id}/mounts
func (h *Handlers) ListVolumeMounts(w http.ResponseWriter, r *http.Request) {
	id := pathSegment(r.URL.Path, "volumes")
	if id == "" {
		writeError(w, r, http.StatusBadRequest, "missing volume id", "INVALID_REQUEST")
		return
	}
	if _, err := h.store.GetVolume(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "volume not found", "NOT_FOUND")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	mounts, err := h.store.ListActiveMountsByVolume(r.Context(), id)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	resp := make([]VolumeMountResponse, len(mounts))
	for i, m := range mounts {
		resp[i] = VolumeMountResponse{
			ID:          m.ID,
			VolumeID:    m.VolumeID,
			InstanceID:  m.InstanceID,
			TargetPath:  m.TargetPath,
			ReadOnly:    m.ReadOnly,
			State:       string(m.State),
			MountedAt:   m.MountedAt,
			UnmountedAt: m.UnmountedAt,
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"mounts": resp})
}

// POST /v1/volumes/{id}/snapshots
func (h *Handlers) CreateSnapshot(w http.ResponseWriter, r *http.Request) {
	id := pathSegment(r.URL.Path, "volumes")
	if id == "" {
		writeError(w, r, http.StatusBadRequest, "missing volume id", "INVALID_REQUEST")
		return
	}
	var req CreateSnapshotRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	v, err := h.store.GetVolume(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "volume not found", "NOT_FOUND")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	if v.State != domain.VolumeReady && v.State != domain.VolumeBound {
		writeError(w, r, http.StatusUnprocessableEntity, "volume is not ready", "INVALID_STATE")
		return
	}

	snap := domain.VolumeSnapshot{
		ID:        uuid.New().String(),
		VolumeID:  id,
		Label:     req.Label,
		State:     domain.SnapshotPending,
		CreatedAt: time.Now(),
	}
	if err := h.store.CreateSnapshot(r.Context(), snap); err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}

	// Attempt synchronous snapshot via driver if available.
	if h.drivers != nil {
		if drv, ok := h.drivers.DriverForClass(v.Manifest.Spec.Class); ok {
			ref, snapErr := drv.Snapshot(r.Context(), v.DriverHandle, req.Label)
			if snapErr == nil {
				now := time.Now()
				snap.State = domain.SnapshotReady
				snap.DriverRef = ref
				snap.CompletedAt = &now
				h.store.UpdateSnapshotState(r.Context(), snap.ID, domain.SnapshotReady, ref, 0) //nolint:errcheck
			} else {
				snap.State = domain.SnapshotFailed
				h.store.UpdateSnapshotState(r.Context(), snap.ID, domain.SnapshotFailed, "", 0) //nolint:errcheck
			}
		}
	}

	final, err := h.store.GetSnapshot(r.Context(), snap.ID)
	var resp SnapshotResponse
	if err != nil {
		resp = snapshotToResponse(snap)
	} else {
		resp = snapshotToResponse(*final)
	}
	// VS-F-K3: verify chain integrity after a completed snapshot.
	if resp.State == string(domain.SnapshotReady) {
		if violationAt, verErr := h.store.VerifySnapshotChain(r.Context(), id); verErr == nil && violationAt != "" {
			resp.ChainViolationAt = violationAt
		}
	}
	writeJSON(w, http.StatusCreated, resp)
}

// GET /v1/volumes/{id}/verify-chain
// Walks all ready snapshots for the volume and re-derives each Merkle chain hash (VS-F-K3).
func (h *Handlers) VerifyChain(w http.ResponseWriter, r *http.Request) {
	id := pathSegmentBefore(r.URL.Path, "verify-chain")
	if id == "" {
		writeError(w, r, http.StatusBadRequest, "missing volume id", "BAD_REQUEST")
		return
	}
	if _, err := h.store.GetVolume(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "volume not found", "NOT_FOUND")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	violationAt, err := h.store.VerifySnapshotChain(r.Context(), id)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "chain verification error: "+err.Error(), "INTERNAL")
		return
	}
	writeJSON(w, http.StatusOK, ChainVerificationResponse{
		VolumeID:         id,
		Intact:           violationAt == "",
		ChainViolationAt: violationAt,
	})
}

// GET /v1/volumes/{id}/snapshots
func (h *Handlers) ListSnapshots(w http.ResponseWriter, r *http.Request) {
	id := pathSegment(r.URL.Path, "volumes")
	if id == "" {
		writeError(w, r, http.StatusBadRequest, "missing volume id", "INVALID_REQUEST")
		return
	}
	snaps, err := h.store.ListSnapshots(r.Context(), id)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal error", "INTERNAL")
		return
	}
	resp := make([]SnapshotResponse, len(snaps))
	for i, s := range snaps {
		resp[i] = snapshotToResponse(s)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"snapshots": resp})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// decodeJSON decodes the request body into v. Returns false and writes an error if it fails.
// GET /v1/operations/{id}
func (h *Handlers) GetOperation(w http.ResponseWriter, r *http.Request) {
	id := pathSegment(r.URL.Path, "operations")
	if id == "" {
		writeError(w, r, http.StatusBadRequest, "missing operation id", "BAD_REQUEST")
		return
	}
	op, err := h.store.GetOperation(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "operation not found", "NOT_FOUND")
			return
		}
		writeError(w, r, http.StatusInternalServerError, err.Error(), "INTERNAL")
		return
	}
	writeJSON(w, http.StatusOK, operationToResponse(*op))
}

// GET /v1/instances/{id}/operations
func (h *Handlers) ListInstanceOperations(w http.ResponseWriter, r *http.Request) {
	id := pathSegmentBefore(r.URL.Path, "operations")
	if id == "" {
		writeError(w, r, http.StatusBadRequest, "missing instance id", "BAD_REQUEST")
		return
	}
	ops, err := h.store.ListInstanceOperations(r.Context(), id)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error(), "INTERNAL")
		return
	}
	out := make([]OperationResponse, len(ops))
	for i, op := range ops {
		out[i] = operationToResponse(op)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"operations": out})
}

// GET /v1/instances/{id}/trace
// Returns a chronological unified trace of all state transitions, operation events,
// and health observations for an instance (VS-F-T1).
func (h *Handlers) GetInstanceTrace(w http.ResponseWriter, r *http.Request) {
	id := pathSegmentBefore(r.URL.Path, "trace")
	if id == "" {
		writeError(w, r, http.StatusBadRequest, "missing instance id", "BAD_REQUEST")
		return
	}

	ops, err := h.store.ListInstanceOperations(r.Context(), id)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error(), "INTERNAL")
		return
	}
	health, err := h.store.ListHealthObservations(r.Context(), id)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error(), "INTERNAL")
		return
	}

	var events []domain.InstanceTraceEvent

	for _, op := range ops {
		events = append(events, domain.InstanceTraceEvent{
			At:      op.CreatedAt,
			Kind:    "op_state",
			Source:  op.ID,
			Summary: string(op.Type) + " → " + string(op.State),
			Detail:  map[string]interface{}{"adapter": op.Adapter},
		})
		for _, ev := range op.Events {
			events = append(events, domain.InstanceTraceEvent{
				At:      ev.CreatedAt,
				Kind:    "op_event",
				Source:  op.ID,
				Summary: ev.EventType + ": " + ev.Message,
				Detail:  ev.Payload,
			})
		}
		if op.CompletedAt != nil {
			events = append(events, domain.InstanceTraceEvent{
				At:      *op.CompletedAt,
				Kind:    "op_state",
				Source:  op.ID,
				Summary: string(op.Type) + " completed → " + string(op.State),
			})
		}
	}

	for _, h := range health {
		events = append(events, domain.InstanceTraceEvent{
			At:      h.ObservedAt,
			Kind:    "health",
			Source:  "health_probe",
			Summary: h.Status + ": " + h.Message,
			Detail:  map[string]interface{}{"node_id": h.NodeID},
		})
	}

	// Sort by time ascending.
	sortTraceEvents(events)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"instanceId": id,
		"events":     events,
	})
}

// sortTraceEvents sorts a slice of InstanceTraceEvent by time ascending in place.
func sortTraceEvents(events []domain.InstanceTraceEvent) {
	for i := 1; i < len(events); i++ {
		for j := i; j > 0 && events[j].At.Before(events[j-1].At); j-- {
			events[j], events[j-1] = events[j-1], events[j]
		}
	}
}

// POST /v1/volumes/{id}/restore
// Creates a new volume whose initial contents are restored from a snapshot.
func (h *Handlers) RestoreVolume(w http.ResponseWriter, r *http.Request) {
	if h.drivers == nil {
		writeError(w, r, http.StatusServiceUnavailable, "no storage drivers configured", "NO_DRIVER")
		return
	}
	sourceID := pathSegmentBefore(r.URL.Path, "restore")
	if sourceID == "" {
		writeError(w, r, http.StatusBadRequest, "missing volume id", "BAD_REQUEST")
		return
	}

	var req RestoreVolumeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.SnapshotID == "" || req.Namespace == "" || req.Name == "" {
		writeError(w, r, http.StatusBadRequest, "snapshotId, namespace and name are required", "BAD_REQUEST")
		return
	}

	snap, err := h.store.GetSnapshot(r.Context(), req.SnapshotID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "snapshot not found", "NOT_FOUND")
			return
		}
		writeError(w, r, http.StatusInternalServerError, err.Error(), "INTERNAL")
		return
	}
	if snap.State != domain.SnapshotReady {
		writeError(w, r, http.StatusConflict, "snapshot is not in ready state", "CONFLICT")
		return
	}

	src, err := h.store.GetVolume(r.Context(), sourceID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "volume not found", "NOT_FOUND")
			return
		}
		writeError(w, r, http.StatusInternalServerError, err.Error(), "INTERNAL")
		return
	}

	drv, ok := h.drivers.DriverForClass(src.Manifest.Spec.Class)
	if !ok {
		writeError(w, r, http.StatusUnprocessableEntity, "no driver for class "+src.Manifest.Spec.Class, "NO_DRIVER")
		return
	}

	handle, err := drv.RestoreFrom(r.Context(), snap.DriverRef, req.Namespace, req.Name, src.Manifest.Spec)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "restore failed: "+err.Error(), "RESTORE_FAILED")
		return
	}

	newManifest := src.Manifest
	newManifest.Metadata.Namespace = req.Namespace
	newManifest.Metadata.Name = req.Name

	newVol := domain.Volume{
		Manifest:     newManifest,
		State:        domain.VolumeReady,
		BoundNodeID:  handle["nodeID"],
		DriverHandle: handle,
	}
	newID, err := h.store.CreateVolume(r.Context(), newVol)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error(), "INTERNAL")
		return
	}

	created, err := h.store.GetVolume(r.Context(), newID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error(), "INTERNAL")
		return
	}
	writeJSON(w, http.StatusCreated, volumeToResponse(*created))
}

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
