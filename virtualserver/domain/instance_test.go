package domain

import "testing"

func TestValidateInstanceTransition_Legal(t *testing.T) {
	cases := [][2]InstanceState{
		{InstanceDeclared, InstanceScheduled},
		{InstanceDeclared, InstanceFailed},
		{InstanceScheduled, InstanceProvisioning},
		{InstanceScheduled, InstanceFailed},
		{InstanceProvisioning, InstanceStarting},
		{InstanceProvisioning, InstanceFailed},
		{InstanceStarting, InstanceRunning},
		{InstanceStarting, InstanceFailed},
		{InstanceRunning, InstanceDegraded},
		{InstanceRunning, InstanceStopping},
		{InstanceDegraded, InstanceRunning},
		{InstanceDegraded, InstanceStopping},
		{InstanceDegraded, InstanceFailed},
		{InstanceStopping, InstanceTerminated},
		{InstanceFailed, InstanceDeclared},
		{InstanceFailed, InstanceTerminated},
	}
	for _, c := range cases {
		if err := ValidateInstanceTransition(c[0], c[1]); err != nil {
			t.Errorf("Expected legal transition %s→%s, got: %v", c[0], c[1], err)
		}
	}
}

func TestValidateInstanceTransition_Illegal(t *testing.T) {
	cases := [][2]InstanceState{
		// terminal is terminal
		{InstanceTerminated, InstanceRunning},
		{InstanceTerminated, InstanceDeclared},
		// cannot skip states
		{InstanceDeclared, InstanceRunning},
		{InstanceDeclared, InstanceProvisioning},
		{InstanceRunning, InstanceDeclared},
		{InstanceRunning, InstanceFailed}, // must go through degraded or stopping
		{InstanceScheduled, InstanceRunning},
		{InstanceStarting, InstanceStopping},
	}
	for _, c := range cases {
		if err := ValidateInstanceTransition(c[0], c[1]); err == nil {
			t.Errorf("Expected illegal transition %s→%s to fail, got nil", c[0], c[1])
		}
	}
}

func TestIsTerminalInstanceState(t *testing.T) {
	if !IsTerminalInstanceState(InstanceTerminated) {
		t.Error("terminated should be terminal")
	}
	for _, s := range []InstanceState{
		InstanceDeclared, InstanceScheduled, InstanceProvisioning,
		InstanceStarting, InstanceRunning, InstanceDegraded,
		InstanceStopping, InstanceFailed,
	} {
		if IsTerminalInstanceState(s) {
			t.Errorf("%s should not be terminal", s)
		}
	}
}
