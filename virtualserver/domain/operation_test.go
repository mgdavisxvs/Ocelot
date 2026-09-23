package domain

import "testing"

func TestValidateOperationTransition_Legal(t *testing.T) {
	cases := [][2]OperationState{
		{OperationPending, OperationRunning},
		{OperationPending, OperationCancelled},
		{OperationRunning, OperationSucceeded},
		{OperationRunning, OperationFailed},
		{OperationRunning, OperationCancelled},
	}
	for _, c := range cases {
		if err := ValidateOperationTransition(c[0], c[1]); err != nil {
			t.Errorf("legal %s→%s rejected: %v", c[0], c[1], err)
		}
	}
}

func TestValidateOperationTransition_Illegal(t *testing.T) {
	cases := [][2]OperationState{
		{OperationSucceeded, OperationRunning},   // terminal
		{OperationFailed, OperationPending},      // terminal
		{OperationCancelled, OperationRunning},   // terminal
		{OperationPending, OperationSucceeded},   // skip running
		{OperationPending, OperationFailed},      // skip running
	}
	for _, c := range cases {
		if err := ValidateOperationTransition(c[0], c[1]); err == nil {
			t.Errorf("illegal %s→%s accepted", c[0], c[1])
		}
	}
}

func TestValidateOperationTransition_UnknownSource(t *testing.T) {
	if err := ValidateOperationTransition("badstate", OperationRunning); err == nil {
		t.Error("expected error for unknown source state")
	}
}
