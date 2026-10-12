package domain

import (
	"encoding/json"
	"testing"
)

func validManifest() ServiceManifest {
	return ServiceManifest{
		APIVersion: "virtualserver/v1",
		Kind:       "Service",
		Metadata:   ServiceMetadata{Namespace: "inference", Name: "granite-runtime"},
		Spec: ServiceSpec{
			Artifact:  ArtifactReference{Type: ArtifactTypeOcelot, InfoHash: "0123456789abcdef0123456789abcdef01234567"},
			Runtime:   "managed-process",
			Instances: 1,
			Resources: ResourceRequest{CPUThreads: 8, RAMMiB: 16384},
			Restart:   RestartPolicy{Policy: "on-failure", MaximumAttempts: 3},
		},
	}
}

func TestServiceManifest_Valid(t *testing.T) {
	if err := validManifest().Validate(); err != nil {
		t.Fatalf("expected valid manifest, got: %v", err)
	}
}

func TestServiceManifest_Invalid_APIVersion(t *testing.T) {
	m := validManifest()
	m.APIVersion = "v2"
	if err := m.Validate(); err == nil {
		t.Error("expected error for wrong apiVersion")
	}
}

func TestServiceManifest_Invalid_Kind(t *testing.T) {
	m := validManifest()
	m.Kind = "Deployment"
	if err := m.Validate(); err == nil {
		t.Error("expected error for wrong kind")
	}
}

func TestServiceManifest_Invalid_Namespace(t *testing.T) {
	m := validManifest()
	m.Metadata.Namespace = ""
	if err := m.Validate(); err == nil {
		t.Error("expected error for empty namespace")
	}
}

func TestServiceManifest_Invalid_InfoHash(t *testing.T) {
	m := validManifest()
	m.Spec.Artifact.InfoHash = "tooshort"
	if err := m.Validate(); err == nil {
		t.Error("expected error for invalid infoHash")
	}
}

func TestServiceManifest_Invalid_ZeroInstances(t *testing.T) {
	m := validManifest()
	m.Spec.Instances = 0
	if err := m.Validate(); err == nil {
		t.Error("expected error for instances=0")
	}
}

func TestServiceManifest_GPU_Valid(t *testing.T) {
	m := validManifest()
	m.Spec.Resources.GPU = GPURequest{Required: true, Count: 1, MinVRAMMiB: 16384}
	if err := m.Validate(); err != nil {
		t.Fatalf("expected valid GPU manifest, got: %v", err)
	}
}

func TestServiceManifest_GPU_InvalidCount(t *testing.T) {
	m := validManifest()
	m.Spec.Resources.GPU = GPURequest{Required: true, Count: 0, MinVRAMMiB: 16384}
	if err := m.Validate(); err == nil {
		t.Error("expected error for gpu.count=0 when required=true")
	}
}

func TestServiceManifest_Invalid_RestartPolicy(t *testing.T) {
	m := validManifest()
	m.Spec.Restart.Policy = "sometimes"
	if err := m.Validate(); err == nil {
		t.Error("expected error for unknown restart policy")
	}
}

func TestParseManifest_JSON(t *testing.T) {
	raw := `{
		"apiVersion": "virtualserver/v1",
		"kind": "Service",
		"metadata": {"namespace": "inference", "name": "llama"},
		"spec": {
			"artifact": {"type": "ocelot", "infoHash": "0123456789abcdef0123456789abcdef01234567"},
			"runtime": "managed-process",
			"instances": 1,
			"resources": {"cpuThreads": 4, "ramMB": 8192},
			"restart": {"policy": "on-failure", "maximumAttempts": 3}
		}
	}`
	m, err := ParseManifest([]byte(raw))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.Metadata.Name != "llama" {
		t.Errorf("expected name=llama, got %q", m.Metadata.Name)
	}
}

func TestParseManifest_UnknownFields_Rejected(t *testing.T) {
	raw := `{
		"apiVersion": "virtualserver/v1",
		"kind": "Service",
		"metadata": {"namespace": "ns", "name": "svc"},
		"spec": {
			"artifact": {"type": "ocelot", "infoHash": "0123456789abcdef0123456789abcdef01234567"},
			"runtime": "managed-process",
			"instances": 1,
			"resources": {}
		},
		"unknownField": "should fail"
	}`
	_, err := ParseManifest([]byte(raw))
	if err == nil {
		t.Error("expected error for unknown field")
	}
}

func TestParseManifest_InvalidJSON(t *testing.T) {
	_, err := ParseManifest([]byte("{not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestArtifactReference_Validate_URL(t *testing.T) {
	a := ArtifactReference{Type: ArtifactTypeURL, URL: "https://example.com/model.bin"}
	if err := a.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	bad := ArtifactReference{Type: ArtifactTypeURL, URL: ""}
	if err := bad.Validate(); err == nil {
		t.Error("expected error for empty URL")
	}
}

func TestServiceManifest_JSONRoundtrip(t *testing.T) {
	m := validManifest()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := ParseManifest(b)
	if err != nil {
		t.Fatalf("roundtrip ParseManifest: %v", err)
	}
	if m2.Metadata.Name != m.Metadata.Name {
		t.Error("roundtrip name mismatch")
	}
}
