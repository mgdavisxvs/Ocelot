package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// VolumeAccessMode restricts how a volume can be simultaneously mounted.
type VolumeAccessMode string

const (
	VolumeAccessRWO VolumeAccessMode = "RWO" // ReadWriteOnce — single writer, single node
	VolumeAccessROX VolumeAccessMode = "ROX" // ReadOnlyMany  — many readers
	VolumeAccessRWX VolumeAccessMode = "RWX" // ReadWriteMany — many readers and writers
)

// VolumeState is the lifecycle state of a Volume.
type VolumeState string

const (
	VolumeDeclared      VolumeState = "declared"
	VolumeProvisioning  VolumeState = "provisioning"
	VolumeReady         VolumeState = "ready"
	VolumeBound         VolumeState = "bound"
	VolumeReleasing     VolumeState = "releasing"
	VolumeReleased      VolumeState = "released"
	VolumeFailed        VolumeState = "failed"
	VolumeQuotaExceeded VolumeState = "quota_exceeded"
)

var allowedVolumeTransitions = map[VolumeState]map[VolumeState]bool{
	VolumeDeclared:      {VolumeProvisioning: true, VolumeReleasing: true},
	VolumeProvisioning:  {VolumeReady: true, VolumeFailed: true},
	VolumeReady:         {VolumeBound: true, VolumeReleasing: true},
	VolumeBound:         {VolumeReady: true, VolumeReleasing: true, VolumeQuotaExceeded: true},
	VolumeReleasing:     {VolumeReleased: true, VolumeFailed: true},
	VolumeReleased:      {},
	VolumeFailed:        {VolumeDeclared: true, VolumeReleasing: true},
	VolumeQuotaExceeded: {VolumeBound: true, VolumeReleasing: true},
}

// IsTerminalVolumeState returns true when the volume has reached a terminal state.
func IsTerminalVolumeState(s VolumeState) bool {
	return s == VolumeReleased
}

// ValidateVolumeTransition returns nil if from→to is a legal volume state transition.
func ValidateVolumeTransition(from, to VolumeState) error {
	allowed, ok := allowedVolumeTransitions[from]
	if !ok {
		return fmt.Errorf("unknown source volume state: %q", from)
	}
	if !allowed[to] {
		return fmt.Errorf("invalid volume state transition: %s → %s", from, to)
	}
	return nil
}

// VolumeMountState tracks the lifecycle of a single mount binding.
type VolumeMountState string

const (
	MountPending   VolumeMountState = "pending"
	MountActive    VolumeMountState = "active"
	MountReleasing VolumeMountState = "releasing"
	MountReleased  VolumeMountState = "released"
	MountFailed    VolumeMountState = "failed"
)

// SnapshotState tracks the lifecycle of a volume snapshot.
type SnapshotState string

const (
	SnapshotPending SnapshotState = "pending"
	SnapshotReady   SnapshotState = "ready"
	SnapshotFailed  SnapshotState = "failed"
)

// VolumeMetadata identifies a volume within a namespace.
type VolumeMetadata struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// VolumeSpec is the desired-state specification for a volume.
type VolumeSpec struct {
	Class       string           `json:"class"`
	CapacityMiB int64            `json:"capacityMiB"`
	AccessMode  VolumeAccessMode `json:"accessMode"`
}

// VolumeManifest is the top-level declaration of a desired volume.
type VolumeManifest struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Metadata   VolumeMetadata `json:"metadata"`
	Spec       VolumeSpec     `json:"spec"`
}

// Validate checks the manifest for structural and semantic correctness.
func (m VolumeManifest) Validate() error {
	var errs []string
	if m.APIVersion != "virtualserver/v1" {
		errs = append(errs, fmt.Sprintf("apiVersion must be 'virtualserver/v1', got %q", m.APIVersion))
	}
	if m.Kind != "Volume" {
		errs = append(errs, fmt.Sprintf("kind must be 'Volume', got %q", m.Kind))
	}
	if err := validateSegment(m.Metadata.Namespace, "metadata.namespace"); err != nil {
		errs = append(errs, err.Error())
	}
	if err := validateSegment(m.Metadata.Name, "metadata.name"); err != nil {
		errs = append(errs, err.Error())
	}
	if m.Spec.Class == "" {
		errs = append(errs, "spec.class must not be empty")
	}
	if m.Spec.CapacityMiB <= 0 {
		errs = append(errs, "spec.capacityMiB must be > 0")
	}
	switch m.Spec.AccessMode {
	case VolumeAccessRWO, VolumeAccessROX, VolumeAccessRWX:
	case "":
		errs = append(errs, "spec.accessMode must be one of RWO, ROX, RWX")
	default:
		errs = append(errs, fmt.Sprintf("spec.accessMode %q is not valid (use RWO/ROX/RWX)", m.Spec.AccessMode))
	}
	if len(errs) > 0 {
		return fmt.Errorf("volume manifest validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

// ParseVolumeManifest decodes JSON into a VolumeManifest and validates it atomically.
func ParseVolumeManifest(data []byte) (VolumeManifest, error) {
	var m VolumeManifest
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return VolumeManifest{}, fmt.Errorf("volume manifest JSON decode: %w", err)
	}
	if err := m.Validate(); err != nil {
		return VolumeManifest{}, err
	}
	return m, nil
}

// Volume is the persisted record of a declared volume.
type Volume struct {
	ID            string
	Manifest      VolumeManifest
	State         VolumeState
	BoundNodeID   string
	DriverHandle  map[string]string
	FailureReason string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// VolumeMount links a Volume to an Instance at a target path.
type VolumeMount struct {
	ID          int64
	VolumeID    string
	InstanceID  string
	TargetPath  string
	ReadOnly    bool
	State       VolumeMountState
	MountedAt   *time.Time
	UnmountedAt *time.Time
}

// VolumeSnapshot is a point-in-time capture of a volume (crash-consistent).
type VolumeSnapshot struct {
	ID          string
	VolumeID    string
	Label       string
	State       SnapshotState
	DriverRef   string
	SizeMiB     int64
	CreatedAt   time.Time
	CompletedAt *time.Time
}

// ServiceVolumeMount is a volume mount declaration inside a ServiceManifest.
type ServiceVolumeMount struct {
	VolumeName string `json:"volumeName"`
	TargetPath string `json:"targetPath"`
	ReadOnly   bool   `json:"readOnly,omitempty"`
}
