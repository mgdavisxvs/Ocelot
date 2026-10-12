package domain

import (
	"testing"
)

func TestValidateNodeTransition_Legal(t *testing.T) {
	cases := [][2]NodeState{
		{NodeDiscovered, NodeReady},
		{NodeDiscovered, NodeQuarantined},
		{NodeReady, NodeDegraded},
		{NodeReady, NodeDraining},
		{NodeReady, NodeMaintenance},
		{NodeReady, NodeQuarantined},
		{NodeDegraded, NodeReady},
		{NodeDegraded, NodeDraining},
		{NodeDegraded, NodeQuarantined},
		{NodeDraining, NodeMaintenance},
		{NodeDraining, NodeQuarantined},
		{NodeDraining, NodeRetired},
		{NodeMaintenance, NodeReady},
		{NodeMaintenance, NodeQuarantined},
		{NodeQuarantined, NodeMaintenance},
		{NodeQuarantined, NodeRetired},
	}
	for _, c := range cases {
		if err := ValidateNodeTransition(c[0], c[1]); err != nil {
			t.Errorf("Expected legal transition %s→%s to pass, got: %v", c[0], c[1], err)
		}
	}
}

func TestValidateNodeTransition_Illegal(t *testing.T) {
	cases := [][2]NodeState{
		// quarantined cannot go directly to ready
		{NodeQuarantined, NodeReady},
		// retired is terminal
		{NodeRetired, NodeReady},
		{NodeRetired, NodeMaintenance},
		{NodeRetired, NodeDiscovered},
		// cannot go backwards from draining to ready directly
		{NodeDraining, NodeReady},
		// discovered cannot drain
		{NodeDiscovered, NodeDraining},
		{NodeDiscovered, NodeMaintenance},
	}
	for _, c := range cases {
		if err := ValidateNodeTransition(c[0], c[1]); err == nil {
			t.Errorf("Expected illegal transition %s→%s to fail, got nil error", c[0], c[1])
		}
	}
}

func TestIsProtectedNodeState(t *testing.T) {
	protected := []NodeState{NodeQuarantined, NodeRetired, NodeDraining, NodeMaintenance}
	for _, s := range protected {
		if !IsProtectedNodeState(s) {
			t.Errorf("Expected %s to be protected", s)
		}
	}
	notProtected := []NodeState{NodeDiscovered, NodeReady, NodeDegraded}
	for _, s := range notProtected {
		if IsProtectedNodeState(s) {
			t.Errorf("Expected %s to NOT be protected", s)
		}
	}
}

func TestNode_EligibleGPUs(t *testing.T) {
	n := Node{
		GPUDevices: []GPUDevice{
			{Index: 0, VRAMMiB: 8192, Allocated: false},
			{Index: 1, VRAMMiB: 16384, Allocated: false},
			{Index: 2, VRAMMiB: 16384, Allocated: true}, // allocated
			{Index: 3, VRAMMiB: 4096, Allocated: false},
		},
	}
	// require 16 GB — only index 1 qualifies (index 2 is allocated)
	eligible := n.EligibleGPUs(16384)
	if len(eligible) != 1 {
		t.Fatalf("expected 1 eligible GPU, got %d", len(eligible))
	}
	if eligible[0].Index != 1 {
		t.Errorf("expected device index 1, got %d", eligible[0].Index)
	}
}

func TestNode_EligibleGPUs_NoneQualify(t *testing.T) {
	n := Node{
		GPUDevices: []GPUDevice{
			{Index: 0, VRAMMiB: 8192},
			{Index: 1, VRAMMiB: 8192},
		},
	}
	// two 8 GB GPUs do NOT satisfy a 16 GB per-device requirement
	eligible := n.EligibleGPUs(16384)
	if len(eligible) != 0 {
		t.Errorf("expected 0 eligible GPUs for 16 GB requirement with 8 GB devices, got %d", len(eligible))
	}
}
