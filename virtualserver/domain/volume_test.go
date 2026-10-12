package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

// ── IsTerminalVolumeState ─────────────────────────────────────────────────────

func TestIsTerminalVolumeState_Released(t *testing.T) {
	if !IsTerminalVolumeState(VolumeReleased) {
		t.Error("released should be terminal")
	}
}

func TestIsTerminalVolumeState_NonTerminal(t *testing.T) {
	for _, s := range []VolumeState{
		VolumeDeclared, VolumeProvisioning, VolumeReady,
		VolumeBound, VolumeReleasing, VolumeFailed, VolumeQuotaExceeded,
	} {
		if IsTerminalVolumeState(s) {
			t.Errorf("%q should not be terminal", s)
		}
	}
}

// ── ValidateVolumeTransition ──────────────────────────────────────────────────

func TestValidateVolumeTransition_Legal(t *testing.T) {
	cases := [][2]VolumeState{
		{VolumeDeclared, VolumeProvisioning},
		{VolumeProvisioning, VolumeReady},
		{VolumeProvisioning, VolumeFailed},
		{VolumeReady, VolumeBound},
		{VolumeReady, VolumeReleasing},
		{VolumeBound, VolumeReady},
		{VolumeBound, VolumeReleasing},
		{VolumeBound, VolumeQuotaExceeded},
		{VolumeReleasing, VolumeReleased},
		{VolumeReleasing, VolumeFailed},
		{VolumeFailed, VolumeDeclared},
		{VolumeQuotaExceeded, VolumeBound},
	}
	for _, c := range cases {
		if err := ValidateVolumeTransition(c[0], c[1]); err != nil {
			t.Errorf("legal %s→%s rejected: %v", c[0], c[1], err)
		}
	}
}

func TestValidateVolumeTransition_Illegal(t *testing.T) {
	cases := [][2]VolumeState{
		{VolumeReleased, VolumeDeclared},   // terminal is terminal
		{VolumeDeclared, VolumeReady},      // skip provisioning
		{VolumeReady, VolumeDeclared},      // backward skip
		{VolumeBound, VolumeDeclared},      // illegal back-arc
		{VolumeProvisioning, VolumeBound},  // skip ready
	}
	for _, c := range cases {
		if err := ValidateVolumeTransition(c[0], c[1]); err == nil {
			t.Errorf("illegal %s→%s accepted", c[0], c[1])
		}
	}
}

func TestValidateVolumeTransition_UnknownSource(t *testing.T) {
	if err := ValidateVolumeTransition("nonexistent", VolumeReady); err == nil {
		t.Error("expected error for unknown source state")
	}
}

// ── VolumeManifest.Validate ───────────────────────────────────────────────────

func validVolumeManifest() VolumeManifest {
	return VolumeManifest{
		APIVersion: "virtualserver/v1",
		Kind:       "Volume",
		Metadata:   VolumeMetadata{Namespace: "ns", Name: "vol"},
		Spec:       VolumeSpec{Class: "local", CapacityMiB: 1024, AccessMode: VolumeAccessRWO},
	}
}

func TestVolumeManifest_Validate_Valid(t *testing.T) {
	if err := validVolumeManifest().Validate(); err != nil {
		t.Errorf("expected valid manifest, got: %v", err)
	}
}

func TestVolumeManifest_Validate_WrongAPIVersion(t *testing.T) {
	m := validVolumeManifest()
	m.APIVersion = "v2"
	if err := m.Validate(); err == nil {
		t.Error("expected error for wrong apiVersion")
	}
}

func TestVolumeManifest_Validate_WrongKind(t *testing.T) {
	m := validVolumeManifest()
	m.Kind = "Service"
	if err := m.Validate(); err == nil {
		t.Error("expected error for wrong kind")
	}
}

func TestVolumeManifest_Validate_EmptyNamespace(t *testing.T) {
	m := validVolumeManifest()
	m.Metadata.Namespace = ""
	if err := m.Validate(); err == nil {
		t.Error("expected error for empty namespace")
	}
}

func TestVolumeManifest_Validate_EmptyName(t *testing.T) {
	m := validVolumeManifest()
	m.Metadata.Name = ""
	if err := m.Validate(); err == nil {
		t.Error("expected error for empty name")
	}
}

func TestVolumeManifest_Validate_EmptyClass(t *testing.T) {
	m := validVolumeManifest()
	m.Spec.Class = ""
	if err := m.Validate(); err == nil {
		t.Error("expected error for empty class")
	}
}

func TestVolumeManifest_Validate_ZeroCapacity(t *testing.T) {
	m := validVolumeManifest()
	m.Spec.CapacityMiB = 0
	if err := m.Validate(); err == nil {
		t.Error("expected error for zero capacity")
	}
}

func TestVolumeManifest_Validate_NegativeCapacity(t *testing.T) {
	m := validVolumeManifest()
	m.Spec.CapacityMiB = -1
	if err := m.Validate(); err == nil {
		t.Error("expected error for negative capacity")
	}
}

func TestVolumeManifest_Validate_EmptyAccessMode(t *testing.T) {
	m := validVolumeManifest()
	m.Spec.AccessMode = ""
	if err := m.Validate(); err == nil {
		t.Error("expected error for empty access mode")
	}
}

func TestVolumeManifest_Validate_InvalidAccessMode(t *testing.T) {
	m := validVolumeManifest()
	m.Spec.AccessMode = "READWRITE"
	if err := m.Validate(); err == nil {
		t.Error("expected error for invalid access mode")
	}
}

func TestVolumeManifest_Validate_AllAccessModes(t *testing.T) {
	for _, mode := range []VolumeAccessMode{VolumeAccessRWO, VolumeAccessROX, VolumeAccessRWX} {
		m := validVolumeManifest()
		m.Spec.AccessMode = mode
		if err := m.Validate(); err != nil {
			t.Errorf("mode %q should be valid, got: %v", mode, err)
		}
	}
}

// ── ParseVolumeManifest ───────────────────────────────────────────────────────

func TestParseVolumeManifest_Valid(t *testing.T) {
	raw, _ := json.Marshal(validVolumeManifest())
	m, err := ParseVolumeManifest(raw)
	if err != nil {
		t.Fatalf("expected valid parse, got: %v", err)
	}
	if m.Metadata.Name != "vol" {
		t.Errorf("unexpected name: %q", m.Metadata.Name)
	}
}

func TestParseVolumeManifest_InvalidJSON(t *testing.T) {
	if _, err := ParseVolumeManifest([]byte("{bad json")); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseVolumeManifest_UnknownField(t *testing.T) {
	raw := []byte(`{"apiVersion":"virtualserver/v1","kind":"Volume","metadata":{"namespace":"ns","name":"v"},"spec":{"class":"local","capacityMiB":512,"accessMode":"RWO"},"extra":"oops"}`)
	if _, err := ParseVolumeManifest(raw); err == nil {
		t.Error("expected error for unknown field (DisallowUnknownFields)")
	}
}

func TestParseVolumeManifest_FailsValidation(t *testing.T) {
	m := validVolumeManifest()
	m.Spec.CapacityMiB = 0
	raw, _ := json.Marshal(m)
	if _, err := ParseVolumeManifest(raw); err == nil {
		t.Error("expected validation error for zero capacity")
	}
}

func TestParseVolumeManifest_ErrorContainsDetail(t *testing.T) {
	m := validVolumeManifest()
	m.APIVersion = "wrong"
	m.Spec.CapacityMiB = -1
	raw, _ := json.Marshal(m)
	_, err := ParseVolumeManifest(raw)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "apiVersion") {
		t.Errorf("error should mention apiVersion, got: %s", msg)
	}
}
