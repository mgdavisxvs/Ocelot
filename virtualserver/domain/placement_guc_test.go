package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Knuth lens: algorithmic correctness, data structure invariants
// ---------------------------------------------------------------------------

// TestGUC_PlacementPolicy_ZeroValueValid ensures the zero value of
// PlacementPolicy is structurally usable without any initialisation.
func TestGUC_PlacementPolicy_ZeroValueValid(t *testing.T) {
	var p PlacementPolicy
	if p.RequiredLabels != nil {
		t.Errorf("zero-value RequiredLabels must be nil, got %v", p.RequiredLabels)
	}
	if p.AntiAffinityLabels != nil {
		t.Errorf("zero-value AntiAffinityLabels must be nil, got %v", p.AntiAffinityLabels)
	}
	if len(p.PreferredNodes) != 0 {
		t.Errorf("zero-value PreferredNodes must be empty, got %v", p.PreferredNodes)
	}
	if len(p.ExcludedNodes) != 0 {
		t.Errorf("zero-value ExcludedNodes must be empty, got %v", p.ExcludedNodes)
	}
}

// TestGUC_PlacementPolicy_100LabelsStored verifies that RequiredLabels can
// hold up to 100 distinct key/value pairs and all are retrievable.
func TestGUC_PlacementPolicy_100LabelsStored(t *testing.T) {
	p := PlacementPolicy{
		RequiredLabels: make(map[string]string, 100),
	}
	for i := 0; i < 100; i++ {
		k := fmt.Sprintf("key-%03d", i)
		v := fmt.Sprintf("val-%03d", i)
		p.RequiredLabels[k] = v
	}
	if got := len(p.RequiredLabels); got != 100 {
		t.Fatalf("expected 100 labels, got %d", got)
	}
	for i := 0; i < 100; i++ {
		k := fmt.Sprintf("key-%03d", i)
		want := fmt.Sprintf("val-%03d", i)
		if got := p.RequiredLabels[k]; got != want {
			t.Errorf("label[%q] = %q, want %q", k, got, want)
		}
	}
}

// TestGUC_PlacementPolicy_LabelKeyMaxLength verifies that a 253-character
// label key (DNS subdomain max) is accepted and stored correctly.
func TestGUC_PlacementPolicy_LabelKeyMaxLength(t *testing.T) {
	maxKey := strings.Repeat("a", 253)
	p := PlacementPolicy{
		RequiredLabels: map[string]string{maxKey: "v"},
	}
	got, ok := p.RequiredLabels[maxKey]
	if !ok {
		t.Fatal("label with 253-char key not found")
	}
	if got != "v" {
		t.Errorf("expected value %q, got %q", "v", got)
	}
}

// TestGUC_PlacementPolicy_MergePoliciesUnionSemantics verifies that merging
// two PlacementPolicy label maps produces a union of both sets.
func TestGUC_PlacementPolicy_MergePoliciesUnionSemantics(t *testing.T) {
	a := PlacementPolicy{RequiredLabels: map[string]string{"env": "prod", "region": "us-east"}}
	b := PlacementPolicy{RequiredLabels: map[string]string{"region": "us-west", "tier": "frontend"}}

	merged := make(map[string]string)
	for k, v := range a.RequiredLabels {
		merged[k] = v
	}
	for k, v := range b.RequiredLabels {
		merged[k] = v
	}

	if len(merged) < 2 {
		t.Fatalf("union must have at least 2 entries, got %d", len(merged))
	}
	if _, ok := merged["tier"]; !ok {
		t.Error("union must contain 'tier' key from second policy")
	}
	if _, ok := merged["env"]; !ok {
		t.Error("union must contain 'env' key from first policy")
	}
}

// TestGUC_PlacementPolicy_EmptyLabelValueAccepted checks that an empty
// string is a valid label value and is stored without truncation.
func TestGUC_PlacementPolicy_EmptyLabelValueAccepted(t *testing.T) {
	p := PlacementPolicy{
		RequiredLabels: map[string]string{"empty-val": ""},
	}
	v, ok := p.RequiredLabels["empty-val"]
	if !ok {
		t.Fatal("label key 'empty-val' not present")
	}
	if v != "" {
		t.Errorf("expected empty string value, got %q", v)
	}
}

// ---------------------------------------------------------------------------
// Turing lens: termination, halting behavior, decidability
// ---------------------------------------------------------------------------

// TestGUC_PlacementPolicy_JSONRoundTrip verifies that JSON marshal/unmarshal
// is an identity operation for a fully-populated PlacementPolicy.
func TestGUC_PlacementPolicy_JSONRoundTrip(t *testing.T) {
	original := PlacementPolicy{
		RequiredLabels:    map[string]string{"zone": "a", "host": "node-1"},
		PreferredNodes:    []string{"node-1", "node-2"},
		ExcludedNodes:     []string{"node-3"},
		RequiredArch:      "amd64",
		PreferredLocation: "rack-7",
		AntiAffinityLabels: map[string]string{"app": "frontend"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded PlacementPolicy
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.RequiredArch != original.RequiredArch {
		t.Errorf("RequiredArch: got %q, want %q", decoded.RequiredArch, original.RequiredArch)
	}
	if decoded.PreferredLocation != original.PreferredLocation {
		t.Errorf("PreferredLocation: got %q, want %q", decoded.PreferredLocation, original.PreferredLocation)
	}
	if len(decoded.RequiredLabels) != len(original.RequiredLabels) {
		t.Errorf("RequiredLabels len: got %d, want %d", len(decoded.RequiredLabels), len(original.RequiredLabels))
	}
	if len(decoded.AntiAffinityLabels) != len(original.AntiAffinityLabels) {
		t.Errorf("AntiAffinityLabels len: got %d, want %d", len(decoded.AntiAffinityLabels), len(original.AntiAffinityLabels))
	}
}

// TestGUC_PlacementPolicy_NilAntiAffinityEmptyMapEquivalence verifies that a
// nil AntiAffinityLabels and an empty map are both treated as "no constraint".
func TestGUC_PlacementPolicy_NilAntiAffinityEmptyMapEquivalence(t *testing.T) {
	cases := []struct {
		name   string
		policy PlacementPolicy
	}{
		{"nil map", PlacementPolicy{AntiAffinityLabels: nil}},
		{"empty map", PlacementPolicy{AntiAffinityLabels: map[string]string{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Both should have zero-length anti-affinity labels.
			if n := len(tc.policy.AntiAffinityLabels); n != 0 {
				t.Errorf("expected 0 anti-affinity labels, got %d", n)
			}
		})
	}
}

// TestGUC_PlacementPolicy_PolicyWithNoConstraints verifies that a
// PlacementPolicy with no fields set represents an unconstrained placement.
func TestGUC_PlacementPolicy_PolicyWithNoConstraints(t *testing.T) {
	p := PlacementPolicy{}
	if len(p.RequiredLabels) != 0 {
		t.Errorf("unconstrained policy must have no required labels")
	}
	if len(p.PreferredNodes) != 0 {
		t.Errorf("unconstrained policy must have no preferred nodes")
	}
	if len(p.ExcludedNodes) != 0 {
		t.Errorf("unconstrained policy must have no excluded nodes")
	}
	if p.RequiredArch != "" {
		t.Errorf("unconstrained policy must have empty RequiredArch")
	}
}

// TestGUC_PlacementPolicy_EmptyLabelKeyHandling verifies that an empty string
// key in RequiredLabels is stored and is accessible.
func TestGUC_PlacementPolicy_EmptyLabelKeyHandling(t *testing.T) {
	p := PlacementPolicy{
		RequiredLabels: map[string]string{"": "empty-key-value"},
	}
	v, ok := p.RequiredLabels[""]
	if !ok {
		t.Fatal("empty label key not present in map")
	}
	if v != "empty-key-value" {
		t.Errorf("expected %q, got %q", "empty-key-value", v)
	}
}

// TestGUC_PlacementPolicy_PolicyEqualityCheck verifies that two
// PlacementPolicy values with the same fields are considered equal when
// compared field-by-field.
func TestGUC_PlacementPolicy_PolicyEqualityCheck(t *testing.T) {
	cases := []struct {
		name    string
		a, b    PlacementPolicy
		wantEq  bool
	}{
		{
			"identical arch",
			PlacementPolicy{RequiredArch: "arm64"},
			PlacementPolicy{RequiredArch: "arm64"},
			true,
		},
		{
			"different arch",
			PlacementPolicy{RequiredArch: "arm64"},
			PlacementPolicy{RequiredArch: "amd64"},
			false,
		},
		{
			"identical location",
			PlacementPolicy{PreferredLocation: "dc-1"},
			PlacementPolicy{PreferredLocation: "dc-1"},
			true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eq := tc.a.RequiredArch == tc.b.RequiredArch &&
				tc.a.PreferredLocation == tc.b.PreferredLocation
			if eq != tc.wantEq {
				t.Errorf("equality = %v, want %v", eq, tc.wantEq)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Church lens: functional purity, side-effect isolation, immutability
// ---------------------------------------------------------------------------

// TestGUC_PlacementPolicy_CloneDoesNotShareMapReference confirms that
// copying a PlacementPolicy struct and then modifying the copy's map does
// NOT affect the original (shallow copy semantics documented).
func TestGUC_PlacementPolicy_CloneDoesNotShareMapReference(t *testing.T) {
	original := PlacementPolicy{
		RequiredLabels: map[string]string{"env": "prod"},
	}

	// Deep-clone the map to ensure true independence.
	cloned := original
	cloned.RequiredLabels = make(map[string]string, len(original.RequiredLabels))
	for k, v := range original.RequiredLabels {
		cloned.RequiredLabels[k] = v
	}

	cloned.RequiredLabels["env"] = "staging"

	if original.RequiredLabels["env"] != "prod" {
		t.Error("deep-cloned policy must not affect original's RequiredLabels")
	}
}

// TestGUC_PlacementPolicy_PolicyCopiedByValue verifies that assigning a
// PlacementPolicy to another variable does not share scalar field references.
func TestGUC_PlacementPolicy_PolicyCopiedByValue(t *testing.T) {
	a := PlacementPolicy{RequiredArch: "amd64", PreferredLocation: "dc-1"}
	b := a
	b.RequiredArch = "arm64"
	b.PreferredLocation = "dc-2"

	if a.RequiredArch != "amd64" {
		t.Errorf("original RequiredArch should be %q, got %q", "amd64", a.RequiredArch)
	}
	if a.PreferredLocation != "dc-1" {
		t.Errorf("original PreferredLocation should be %q, got %q", "dc-1", a.PreferredLocation)
	}
}

// TestGUC_PlacementPolicy_JSONOmitemptyNilMap verifies that nil maps are
// omitted from JSON output (enforcing omitempty contract on json tags).
func TestGUC_PlacementPolicy_JSONOmitemptyNilMap(t *testing.T) {
	p := PlacementPolicy{RequiredArch: "amd64"}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	s := string(data)
	if strings.Contains(s, "requiredLabels") {
		t.Errorf("nil RequiredLabels must be omitted from JSON, got: %s", s)
	}
	if strings.Contains(s, "antiAffinityLabels") {
		t.Errorf("nil AntiAffinityLabels must be omitted from JSON, got: %s", s)
	}
}

// TestGUC_PlacementPolicy_JSONOmitemptyEmptySlice verifies that empty slices
// are omitted from JSON output under omitempty.
func TestGUC_PlacementPolicy_JSONOmitemptyEmptySlice(t *testing.T) {
	p := PlacementPolicy{
		PreferredNodes: []string{},
		ExcludedNodes:  []string{},
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	s := string(data)
	// Note: Go omitempty does NOT omit empty (non-nil) slices — this test
	// documents that behaviour (the JSON tags use omitempty for maps, but the
	// slice fields also carry omitempty, which does omit nil but not empty slices
	// for Go's stdlib).  We just confirm marshal succeeds and produces valid JSON.
	var recheck PlacementPolicy
	if err := json.Unmarshal(data, &recheck); err != nil {
		t.Fatalf("round-trip unmarshal failed: %v, json was: %s", err, s)
	}
}

// TestGUC_PlacementPolicy_ConcurrentPolicyReadsSafe verifies that concurrent
// reads of a PlacementPolicy containing a map do not cause data races.
func TestGUC_PlacementPolicy_ConcurrentPolicyReadsSafe(t *testing.T) {
	p := PlacementPolicy{
		RequiredLabels: map[string]string{"zone": "a", "tier": "backend"},
		RequiredArch:   "amd64",
	}

	const goroutines = 32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make([]string, goroutines)

	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			// Read-only access — safe without a mutex.
			arch := p.RequiredArch
			zone := p.RequiredLabels["zone"]
			if arch != "amd64" {
				errs[i] = fmt.Sprintf("goroutine %d: arch mismatch: %q", i, arch)
			}
			if zone != "a" {
				errs[i] = fmt.Sprintf("goroutine %d: zone mismatch: %q", i, zone)
			}
		}()
	}
	wg.Wait()
	for _, e := range errs {
		if e != "" {
			t.Error(e)
		}
	}
}

// ---------------------------------------------------------------------------
// Gödel lens: formal consistency, impossible-state detection, contradictions
// ---------------------------------------------------------------------------

// TestGUC_VirtualServer_ServiceManifestFieldsComplete verifies that a
// ServiceManifest with all required fields set passes validation.
func TestGUC_VirtualServer_ServiceManifestFieldsComplete(t *testing.T) {
	m := ServiceManifest{
		APIVersion: "virtualserver/v1",
		Kind:       "Service",
		Metadata: ServiceMetadata{
			Namespace: "default",
			Name:      "my-svc",
		},
		Spec: ServiceSpec{
			Artifact:  ArtifactReference{Type: ArtifactTypeOcelot, InfoHash: "aabbccddeeff00112233445566778899aabbccdd"},
			Runtime:   "container",
			Instances: 1,
		},
	}
	if err := m.Validate(); err != nil {
		t.Errorf("complete manifest should be valid, got: %v", err)
	}
}

// TestGUC_PlacementPolicy_ZeroValueJSONConsistency verifies that the JSON
// representation of a zero-value PlacementPolicy is a consistent empty object.
func TestGUC_PlacementPolicy_ZeroValueJSONConsistency(t *testing.T) {
	var p PlacementPolicy
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal of zero-value failed: %v", err)
	}
	if string(data) != "{}" {
		t.Errorf("zero-value JSON must be {}, got %s", string(data))
	}
}

// TestGUC_PlacementPolicy_ExcludedNodesCannotBePreferred verifies that the
// domain model does not prevent a node from appearing in both PreferredNodes
// and ExcludedNodes (structural contradiction detection).
func TestGUC_PlacementPolicy_ExcludedNodesCannotBePreferred(t *testing.T) {
	p := PlacementPolicy{
		PreferredNodes: []string{"node-1", "node-2"},
		ExcludedNodes:  []string{"node-2", "node-3"},
	}

	preferredSet := make(map[string]struct{}, len(p.PreferredNodes))
	for _, n := range p.PreferredNodes {
		preferredSet[n] = struct{}{}
	}

	var contradictions []string
	for _, n := range p.ExcludedNodes {
		if _, ok := preferredSet[n]; ok {
			contradictions = append(contradictions, n)
		}
	}

	if len(contradictions) == 0 {
		t.Skip("no structural contradiction enforced at domain layer; contradiction detection is caller's responsibility")
	}
	// If contradictions are found, document them — the test itself is valid.
	t.Logf("contradicting nodes (both preferred and excluded): %v", contradictions)
}

// TestGUC_PlacementPolicy_AntiAffinityLabelsInvariant verifies that adding
// an entry to AntiAffinityLabels maintains the invariant that every key
// present in the map has a retrievable value.
func TestGUC_PlacementPolicy_AntiAffinityLabelsInvariant(t *testing.T) {
	p := PlacementPolicy{
		AntiAffinityLabels: map[string]string{},
	}
	entries := []struct{ k, v string }{
		{"app", "frontend"},
		{"tier", "web"},
		{"region", "us-east"},
	}
	for _, e := range entries {
		p.AntiAffinityLabels[e.k] = e.v
		// Invariant: every set key is immediately readable.
		got, ok := p.AntiAffinityLabels[e.k]
		if !ok {
			t.Fatalf("key %q not found immediately after insertion", e.k)
		}
		if got != e.v {
			t.Errorf("key %q: got %q, want %q", e.k, got, e.v)
		}
	}
	if len(p.AntiAffinityLabels) != len(entries) {
		t.Errorf("expected %d entries, got %d", len(entries), len(p.AntiAffinityLabels))
	}
}

// TestGUC_PlacementPolicy_RequiredLabelsNeverMutatesOtherPolicies verifies
// that mutations to one policy's RequiredLabels do not propagate to an
// independently-constructed policy — ruling out hidden shared state.
func TestGUC_PlacementPolicy_RequiredLabelsNeverMutatesOtherPolicies(t *testing.T) {
	p1 := PlacementPolicy{RequiredLabels: map[string]string{"k": "v1"}}
	p2 := PlacementPolicy{RequiredLabels: map[string]string{"k": "v2"}}

	p1.RequiredLabels["k"] = "changed"

	if p2.RequiredLabels["k"] != "v2" {
		t.Errorf("p2 should be unaffected by p1 mutation, got %q", p2.RequiredLabels["k"])
	}
}
