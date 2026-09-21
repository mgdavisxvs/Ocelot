package domain

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var infoHashRe = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// ArtifactType enumerates supported artifact backing stores.
type ArtifactType string

const (
	ArtifactTypeOcelot ArtifactType = "ocelot"
	ArtifactTypeURL    ArtifactType = "url"
)

// ArtifactReference points to a distributable artifact.
type ArtifactReference struct {
	Type     ArtifactType `json:"type"`
	InfoHash string       `json:"infoHash,omitempty"` // 40-char hex, ocelot type
	URL      string       `json:"url,omitempty"`      // url type
}

func (a ArtifactReference) Validate() error {
	switch a.Type {
	case ArtifactTypeOcelot:
		if !infoHashRe.MatchString(a.InfoHash) {
			return fmt.Errorf("artifact.infoHash must be a 40-character hex string")
		}
	case ArtifactTypeURL:
		if a.URL == "" {
			return fmt.Errorf("artifact.url must not be empty for type=url")
		}
	default:
		return fmt.Errorf("artifact.type %q is not supported (use ocelot or url)", a.Type)
	}
	return nil
}

// GPURequest expresses a GPU resource requirement.
type GPURequest struct {
	Required    bool  `json:"required"`
	Count       int   `json:"count"`
	MinVRAMMiB  int64 `json:"minimumVRAMMB"`
}

// ResourceRequest describes compute resources needed by a service instance.
type ResourceRequest struct {
	CPUThreads int        `json:"cpuThreads"`
	RAMMiB     int64      `json:"ramMB"`
	GPU        GPURequest `json:"gpu,omitempty"`
}

// PlacementPolicy constrains which nodes may host this service.
type PlacementPolicy struct {
	RequiredLabels  map[string]string `json:"requiredLabels,omitempty"`
	PreferredNodes  []string          `json:"preferredNodes,omitempty"`
	ExcludedNodes   []string          `json:"excludedNodes,omitempty"`
	RequiredArch    string            `json:"arch,omitempty"`
	PreferredLocation string          `json:"preferredLocation,omitempty"`
}

// RestartPolicy controls instance restart behavior.
type RestartPolicy struct {
	Policy         string `json:"policy"`          // "always", "on-failure", "never"
	MaximumAttempts int   `json:"maximumAttempts"`
}

// ServiceSpec is the desired-state specification for a service.
type ServiceSpec struct {
	Artifact          ArtifactReference `json:"artifact"`
	Runtime           string            `json:"runtime"`
	Instances         int               `json:"instances"`
	Resources         ResourceRequest   `json:"resources"`
	Placement         PlacementPolicy   `json:"placement,omitempty"`
	Restart           RestartPolicy     `json:"restart,omitempty"`
	MigrationAllowed  bool              `json:"migrationAllowed"`
	HealthCheck       map[string]string `json:"healthCheck,omitempty"`
}

// ServiceManifest is the top-level declaration of a desired service.
type ServiceManifest struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Metadata   ServiceMetadata `json:"metadata"`
	Spec       ServiceSpec     `json:"spec"`
}

// ServiceMetadata identifies a service within a namespace.
type ServiceMetadata struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// Validate checks the manifest for structural and semantic correctness.
// Returns a detailed error listing all invalid fields.
func (m ServiceManifest) Validate() error {
	var errs []string
	if m.APIVersion != "virtualserver/v1" {
		errs = append(errs, fmt.Sprintf("apiVersion must be 'virtualserver/v1', got %q", m.APIVersion))
	}
	if m.Kind != "Service" {
		errs = append(errs, fmt.Sprintf("kind must be 'Service', got %q", m.Kind))
	}
	if err := validateSegment(m.Metadata.Namespace, "metadata.namespace"); err != nil {
		errs = append(errs, err.Error())
	}
	if err := validateSegment(m.Metadata.Name, "metadata.name"); err != nil {
		errs = append(errs, err.Error())
	}
	if err := m.Spec.Artifact.Validate(); err != nil {
		errs = append(errs, "spec.artifact: "+err.Error())
	}
	if m.Spec.Runtime == "" {
		errs = append(errs, "spec.runtime must not be empty")
	}
	if m.Spec.Instances < 1 {
		errs = append(errs, "spec.instances must be >= 1")
	}
	if m.Spec.Resources.CPUThreads < 0 {
		errs = append(errs, "spec.resources.cpuThreads must be >= 0")
	}
	if m.Spec.Resources.RAMMiB < 0 {
		errs = append(errs, "spec.resources.ramMB must be >= 0")
	}
	if m.Spec.Resources.GPU.Required {
		if m.Spec.Resources.GPU.Count < 1 {
			errs = append(errs, "spec.resources.gpu.count must be >= 1 when gpu.required=true")
		}
		if m.Spec.Resources.GPU.MinVRAMMiB < 0 {
			errs = append(errs, "spec.resources.gpu.minimumVRAMMB must be >= 0")
		}
	}
	if m.Spec.Restart.Policy != "" {
		switch m.Spec.Restart.Policy {
		case "always", "on-failure", "never":
		default:
			errs = append(errs, fmt.Sprintf("spec.restart.policy %q is not valid (use always/on-failure/never)", m.Spec.Restart.Policy))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("manifest validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

// ParseManifest decodes JSON into a ServiceManifest and validates it atomically.
func ParseManifest(data []byte) (ServiceManifest, error) {
	var m ServiceManifest
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return ServiceManifest{}, fmt.Errorf("manifest JSON decode: %w", err)
	}
	if err := m.Validate(); err != nil {
		return ServiceManifest{}, err
	}
	return m, nil
}

// Service is the persisted record of a declared service.
type Service struct {
	ID           int64
	Manifest     ServiceManifest
	DesiredCount int
	State        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
