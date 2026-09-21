package api

import "github.com/mgdavisxvs/Ocelot/virtualserver/domain"

// ── Request DTOs ──────────────────────────────────────────────────────────────

// RegisterNodeRequest is the request body for POST /v1/nodes.
type RegisterNodeRequest struct {
	Name        string            `json:"name"`
	BackendType string            `json:"backendType"`
	Arch        string            `json:"arch"`
	OS          string            `json:"os,omitempty"`
	CPUThreads  int               `json:"cpuThreads"`
	TotalRAMMiB int64             `json:"totalRAMMB"`
	Labels      map[string]string `json:"labels,omitempty"`
	GPUDevices  []GPUDeviceDTO    `json:"gpuDevices,omitempty"`
}

// GPUDeviceDTO is the API representation of a GPU device.
type GPUDeviceDTO struct {
	Index    int    `json:"index"`
	Vendor   string `json:"vendor,omitempty"`
	Model    string `json:"model,omitempty"`
	VRAMMiB  int64  `json:"vramMB"`
}

// UpdateNodeStateRequest is the request body for PUT /v1/nodes/{id}/state.
type UpdateNodeStateRequest struct {
	State string `json:"state"`
}

// DeclareServiceRequest wraps a ServiceManifest for POST /v1/services.
type DeclareServiceRequest struct {
	Manifest domain.ServiceManifest `json:"manifest"`
}

// ── Response DTOs ─────────────────────────────────────────────────────────────

// NodeResponse is the API representation of a node.
type NodeResponse struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	BackendType string            `json:"backendType"`
	Arch        string            `json:"arch"`
	OS          string            `json:"os,omitempty"`
	CPUThreads  int               `json:"cpuThreads"`
	TotalRAMMiB int64             `json:"totalRAMMB"`
	AvailRAMMiB int64             `json:"availRAMMB"`
	GPUDevices  []GPUDeviceDTO    `json:"gpuDevices,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	State       string            `json:"state"`
}

// ServiceResponse is the API representation of a declared service.
type ServiceResponse struct {
	ID           int64                  `json:"id"`
	Manifest     domain.ServiceManifest `json:"manifest"`
	DesiredCount int                    `json:"desiredCount"`
	State        string                 `json:"state"`
}

// InstanceResponse is the API representation of a service instance.
type InstanceResponse struct {
	ID         string `json:"id"`
	ServiceID  int64  `json:"serviceId"`
	NodeID     string `json:"nodeId,omitempty"`
	VSPath     string `json:"vsPath"`
	State      string `json:"state"`
	RetryCount int    `json:"retryCount"`
}

// ErrorResponse is the standard error body.
type ErrorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	ReqID   string `json:"requestId,omitempty"`
}

// ── Mapping helpers ───────────────────────────────────────────────────────────

func nodeToResponse(n domain.Node) NodeResponse {
	r := NodeResponse{
		ID:          n.ID,
		Name:        n.Name,
		BackendType: n.BackendType,
		Arch:        n.Arch,
		OS:          n.OS,
		CPUThreads:  n.CPUThreads,
		TotalRAMMiB: n.TotalRAMMiB,
		AvailRAMMiB: n.AvailRAMMiB,
		Labels:      n.Labels,
		State:       string(n.State),
	}
	for _, g := range n.GPUDevices {
		r.GPUDevices = append(r.GPUDevices, GPUDeviceDTO{
			Index:   g.Index,
			Vendor:  g.Vendor,
			Model:   g.Model,
			VRAMMiB: g.VRAMMiB,
		})
	}
	return r
}

func serviceToResponse(s domain.Service) ServiceResponse {
	return ServiceResponse{
		ID:           s.ID,
		Manifest:     s.Manifest,
		DesiredCount: s.DesiredCount,
		State:        s.State,
	}
}

func instanceToResponse(i domain.ServiceInstance) InstanceResponse {
	return InstanceResponse{
		ID:         i.ID,
		ServiceID:  i.ServiceID,
		NodeID:     i.NodeID,
		VSPath:     i.VSPath.String(),
		State:      string(i.State),
		RetryCount: i.RetryCount,
	}
}
