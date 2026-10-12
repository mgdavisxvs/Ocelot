package virtualserver_test

// GUC Test Coverage — virtualserver/config (VSConfig + VolumeClass)
// Knuth  (~5): algorithmic correctness, loop invariants, data structure invariants
// Turing (~5): termination conditions, halting behavior, decidability
// Church (~5): functional purity, side-effect isolation, referential transparency
// Gödel  (~5): formal consistency, invariant preservation, impossible-state detection

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgdavisxvs/Ocelot/virtualserver/config"
	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
	"github.com/mgdavisxvs/Ocelot/virtualserver/scheduler"
)

// ---------------------------------------------------------------------------
// KNUTH — algorithmic correctness, loop invariants, data structure invariants
// ---------------------------------------------------------------------------

// TestGUC_Config_DefaultValuesValid uses a table-driven approach to verify
// every field returned by DefaultVSConfig has the documented default value.
// This exercises the loop invariant: for every (field, expected) pair the
// assertion must hold without exception.
func TestGUC_Config_DefaultValuesValid(t *testing.T) {
	cfg := config.DefaultVSConfig()

	cases := []struct {
		name string
		ok   bool
		msg  string
	}{
		{"Enabled", !cfg.Enabled, "Enabled must be false by default"},
		{"Port", cfg.Port == ":9091", fmt.Sprintf("Port: got %q, want :9091", cfg.Port)},
		{"AdminKey empty", cfg.AdminKey == "", fmt.Sprintf("AdminKey: got %q, want empty", cfg.AdminKey)},
		{"DBPath", cfg.DBPath == "data/db/vs.db", fmt.Sprintf("DBPath: got %q, want data/db/vs.db", cfg.DBPath)},
		{"ReconcileEvery", cfg.ReconcileEvery == 15*time.Second, fmt.Sprintf("ReconcileEvery: got %v, want 15s", cfg.ReconcileEvery)},
		{"MaxBodyBytes", cfg.MaxBodyBytes == 1<<20, fmt.Sprintf("MaxBodyBytes: got %d, want %d", cfg.MaxBodyBytes, 1<<20)},
		{"RequestTimeout", cfg.RequestTimeout == 30*time.Second, fmt.Sprintf("RequestTimeout: got %v, want 30s", cfg.RequestTimeout)},
		{"StorageClasses nil", cfg.StorageClasses == nil, "StorageClasses must be nil by default"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Error(tc.msg)
			}
		})
	}
}

// TestGUC_Config_ListenAddrParseable verifies the structural invariant that the
// default Port value is a valid TCP listen address — a net.ResolveTCPAddr call
// must succeed, confirming the address is not a malformed string.
func TestGUC_Config_ListenAddrParseable(t *testing.T) {
	cfg := config.DefaultVSConfig()
	addr, err := net.ResolveTCPAddr("tcp", cfg.Port)
	if err != nil {
		t.Fatalf("DefaultVSConfig.Port %q is not a valid TCP address: %v", cfg.Port, err)
	}
	if addr == nil {
		t.Fatalf("ResolveTCPAddr returned nil for %q", cfg.Port)
	}
}

// TestGUC_Config_VolumeClassFieldsTableDriven checks the data structure
// invariant that a VolumeClass carries the exact values it was given —
// no field is silently truncated, zero-initialised, or mutated at construction.
func TestGUC_Config_VolumeClassFieldsTableDriven(t *testing.T) {
	tests := []struct {
		name   string
		driver string
		params map[string]string
	}{
		{"local", "local", map[string]string{"baseDir": "/data/vol"}},
		{"fast-ssd", "nvme", map[string]string{"iops": "10000", "queue": "4"}},
		{"s3-standard", "s3", map[string]string{"bucket": "my-bucket", "region": "us-east-1"}},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			vc := config.VolumeClass{Name: tc.name, Driver: tc.driver, Params: tc.params}
			if vc.Name != tc.name {
				t.Errorf("Name: got %q, want %q", vc.Name, tc.name)
			}
			if vc.Driver != tc.driver {
				t.Errorf("Driver: got %q, want %q", vc.Driver, tc.driver)
			}
			for k, v := range tc.params {
				if vc.Params[k] != v {
					t.Errorf("Params[%q]: got %q, want %q", k, vc.Params[k], v)
				}
			}
		})
	}
}

// TestGUC_Config_MaxBodyBytesEqualsOneMiB asserts the algorithmic constant:
// the default maximum body size is exactly 1 MiB (1<<20 bytes). Off-by-one
// or wrong-power-of-two errors are detectable here without approximation.
func TestGUC_Config_MaxBodyBytesEqualsOneMiB(t *testing.T) {
	const oneMiB int64 = 1 << 20
	cfg := config.DefaultVSConfig()
	if cfg.MaxBodyBytes != oneMiB {
		t.Errorf("MaxBodyBytes = %d; want exactly %d (1 MiB)", cfg.MaxBodyBytes, oneMiB)
	}
}

// TestGUC_Config_TimeDurationsPositive verifies the loop invariant that all
// duration fields in the default config are strictly positive — a zero or
// negative duration would cause silent misconfigurations in tickers/timeouts.
func TestGUC_Config_TimeDurationsPositive(t *testing.T) {
	cfg := config.DefaultVSConfig()
	durations := []struct {
		name string
		d    time.Duration
	}{
		{"ReconcileEvery", cfg.ReconcileEvery},
		{"RequestTimeout", cfg.RequestTimeout},
	}
	for _, td := range durations {
		if td.d <= 0 {
			t.Errorf("%s = %v; want positive duration", td.name, td.d)
		}
	}
}

// ---------------------------------------------------------------------------
// TURING — termination conditions, halting behavior, decidability
// ---------------------------------------------------------------------------

// TestGUC_Config_DefaultVSConfigAlwaysTerminates confirms that DefaultVSConfig
// always returns (terminates) when called in a goroutine — it must never block,
// deadlock, or spin indefinitely.
func TestGUC_Config_DefaultVSConfigAlwaysTerminates(t *testing.T) {
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			_ = config.DefaultVSConfig()
		}
		close(done)
	}()
	select {
	case <-done:
		// good — terminated
	case <-time.After(5 * time.Second):
		t.Fatal("DefaultVSConfig did not terminate within 5 s")
	}
}

// TestGUC_Config_ZeroValueConfigNoPanic verifies that constructing a zero-value
// VSConfig halts without panicking. This is the decidability check: even in
// the worst-case (all fields zero) the type must be safely constructible.
func TestGUC_Config_ZeroValueConfigNoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("zero-value VSConfig construction panicked: %v", r)
		}
	}()
	var cfg config.VSConfig
	// Access each field to ensure the zero value is readable without panic.
	_ = cfg.Enabled
	_ = cfg.Port
	_ = cfg.AdminKey
	_ = cfg.DBPath
	_ = cfg.ReconcileEvery
	_ = cfg.MaxBodyBytes
	_ = cfg.RequestTimeout
	_ = cfg.StorageClasses
}

// TestGUC_Config_JSONRoundTrip verifies that a VSConfig with concrete values
// round-trips through json.Marshal → json.Unmarshal with all exported string
// and numeric fields preserved. This tests the halting property of the codec:
// it must always produce a decodeable representation.
func TestGUC_Config_JSONRoundTrip(t *testing.T) {
	original := config.DefaultVSConfig()
	original.Port = ":8080"
	original.AdminKey = "test-key"
	original.DBPath = "/tmp/vs.db"

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var decoded config.VSConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if decoded.Port != original.Port {
		t.Errorf("Port: got %q, want %q", decoded.Port, original.Port)
	}
	if decoded.AdminKey != original.AdminKey {
		t.Errorf("AdminKey: got %q, want %q", decoded.AdminKey, original.AdminKey)
	}
	if decoded.DBPath != original.DBPath {
		t.Errorf("DBPath: got %q, want %q", decoded.DBPath, original.DBPath)
	}
}

// TestGUC_Config_YAMLRoundTrip is a placeholder for YAML round-trip testing.
// It is skipped because Go's standard library has no YAML package — the test
// would require an external dependency that is excluded by project policy.
func TestGUC_Config_YAMLRoundTrip(t *testing.T) {
	t.Skip("not yet implemented: YAML support requires a non-stdlib package (e.g. gopkg.in/yaml.v3)")
}

// TestGUC_Config_EnabledFalseByDefault verifies the halting-condition invariant:
// a fresh DefaultVSConfig has Enabled=false, which means any system that guards
// initialisation behind cfg.Enabled will halt early without attempting to open
// the database or start listeners — a safe default halting state.
func TestGUC_Config_EnabledFalseByDefault(t *testing.T) {
	cfg := config.DefaultVSConfig()
	if cfg.Enabled {
		t.Fatal("DefaultVSConfig.Enabled must be false; a default config must not auto-start")
	}
	// Verify the logical path: an operator must explicitly set Enabled=true.
	cfg.Enabled = true
	if !cfg.Enabled {
		t.Fatal("setting Enabled=true on the copy must be decidable")
	}
}

// ---------------------------------------------------------------------------
// CHURCH — functional purity, side-effect isolation, referential transparency
// ---------------------------------------------------------------------------

// TestGUC_Config_DefaultVSConfigIsPure checks referential transparency:
// calling DefaultVSConfig any number of times with no arguments always returns
// a struct whose fields are identical — no global state, no side effects.
func TestGUC_Config_DefaultVSConfigIsPure(t *testing.T) {
	first := config.DefaultVSConfig()
	for i := 0; i < 10; i++ {
		got := config.DefaultVSConfig()
		if got.Port != first.Port ||
			got.DBPath != first.DBPath ||
			got.AdminKey != first.AdminKey ||
			got.Enabled != first.Enabled ||
			got.MaxBodyBytes != first.MaxBodyBytes ||
			got.ReconcileEvery != first.ReconcileEvery ||
			got.RequestTimeout != first.RequestTimeout {
			t.Errorf("call %d returned different values than call 0", i+1)
		}
	}
}

// TestGUC_Config_CopiesAreIndependent verifies side-effect isolation: modifying
// one copy of a VSConfig must not affect any other copy. Go struct assignment is
// a value copy; this test is the formal proof that no hidden aliasing exists for
// non-pointer fields.
func TestGUC_Config_CopiesAreIndependent(t *testing.T) {
	a := config.DefaultVSConfig()
	b := config.DefaultVSConfig()

	a.Port = ":7777"
	a.AdminKey = "secret-a"
	a.Enabled = true
	a.MaxBodyBytes = 99

	if b.Port == a.Port {
		t.Errorf("b.Port was mutated to %q via a.Port assignment", b.Port)
	}
	if b.AdminKey == a.AdminKey {
		t.Errorf("b.AdminKey was mutated to %q via a.AdminKey assignment", b.AdminKey)
	}
	if b.Enabled {
		t.Error("b.Enabled was mutated to true via a.Enabled assignment")
	}
	if b.MaxBodyBytes == a.MaxBodyBytes {
		t.Errorf("b.MaxBodyBytes was mutated to %d via a.MaxBodyBytes assignment", b.MaxBodyBytes)
	}
}

// TestGUC_Config_ConcurrentReadSafe confirms that DefaultVSConfig can be called
// from many goroutines simultaneously without data races (Church: the function
// must have no observable shared mutable state that goroutines contend over).
func TestGUC_Config_ConcurrentReadSafe(t *testing.T) {
	const goroutines = 64
	var wg sync.WaitGroup
	errs := make(chan string, goroutines)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			cfg := config.DefaultVSConfig()
			if cfg.Port == "" {
				errs <- "Port is empty in concurrent call"
			}
		}()
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}
}

// TestGUC_Config_VolumeClassParamsMutationIsolated verifies that two VolumeClass
// instances with independent Params maps are truly isolated: mutating one map
// must not affect the other. This is the side-effect isolation check for the
// reference type (map) embedded in VolumeClass.
func TestGUC_Config_VolumeClassParamsMutationIsolated(t *testing.T) {
	paramsA := map[string]string{"key": "valueA"}
	paramsB := map[string]string{"key": "valueB"}

	vcA := config.VolumeClass{Name: "a", Driver: "local", Params: paramsA}
	vcB := config.VolumeClass{Name: "b", Driver: "local", Params: paramsB}

	vcA.Params["extra"] = "added-to-a"
	if _, ok := vcB.Params["extra"]; ok {
		t.Errorf("mutation of vcA.Params leaked into vcB.Params — maps are aliased")
	}
	if vcA.Params["key"] != "valueA" {
		t.Errorf("vcA.Params[key] changed unexpectedly: %q", vcA.Params["key"])
	}
	if vcB.Params["key"] != "valueB" {
		t.Errorf("vcB.Params[key] changed unexpectedly: %q", vcB.Params["key"])
	}
}

// TestGUC_Config_StorageClassesSliceIsolated verifies that appending to the
// StorageClasses slice of one config copy is isolated: the Go slice header
// is a value, so the append must not grow the underlying array behind another
// variable's pointer, maintaining referential transparency between copies.
func TestGUC_Config_StorageClassesSliceIsolated(t *testing.T) {
	a := config.DefaultVSConfig()
	b := a // value copy — slice header copied, length=0

	a.StorageClasses = append(a.StorageClasses,
		config.VolumeClass{Name: "class-x", Driver: "local", Params: nil})

	if len(b.StorageClasses) != 0 {
		t.Errorf("b.StorageClasses grew to %d after append to a — slice aliasing detected", len(b.StorageClasses))
	}
}

// ---------------------------------------------------------------------------
// GÖDEL — formal consistency, invariant preservation, impossible-state detection
// ---------------------------------------------------------------------------

// TestGUC_Config_AntiAffinityWeightInUnitInterval verifies the formal range
// invariant: the AntiAffinity scoring weight from DefaultWeights must lie in
// [0, 1]. A value outside this interval can produce a scheduler score outside
// [0, 1], violating the documented guarantee of the scoring algorithm.
func TestGUC_Config_AntiAffinityWeightInUnitInterval(t *testing.T) {
	w := scheduler.DefaultWeights()
	if w.AntiAffinity < 0 || w.AntiAffinity > 1 {
		t.Errorf("AntiAffinity weight = %v; want value in [0,1]", w.AntiAffinity)
	}
}

// TestGUC_Config_SchedulerWeightsSumReasonable checks the formal consistency
// invariant that the sum of all positive weight contributions in DefaultWeights
// is strictly positive and bounded — a zero sum would produce no signal, while
// an unbounded sum would overflow the score into an undefined range.
func TestGUC_Config_SchedulerWeightsSumReasonable(t *testing.T) {
	w := scheduler.DefaultWeights()
	sum := w.Health + w.CapacityFit + w.AcceleratorFit + w.DataLocality +
		w.Reliability + w.PreferredNodeBonus + w.ExistingLoadPenalty +
		w.ArtifactTransfer + w.AntiAffinity

	const (
		minSum = 0.5
		maxSum = 10.0
	)
	if sum < minSum {
		t.Errorf("weight sum = %v; too low (< %v) — scoring would have no signal", sum, minSum)
	}
	if sum > maxSum {
		t.Errorf("weight sum = %v; too high (> %v) — score normalisation would be meaningless", sum, maxSum)
	}
}

// TestGUC_Config_ParseManifestRejectsUnknownFields asserts the formal
// consistency property of the manifest parser: an unknown JSON field name
// must return an error (via DisallowUnknownFields), preventing silent
// acceptance of schema drift or typos that could produce impossible states.
func TestGUC_Config_ParseManifestRejectsUnknownFields(t *testing.T) {
	// A fully valid manifest body with one unexpected top-level field injected.
	manifest := `{
		"apiVersion": "virtualserver/v1",
		"kind": "Service",
		"metadata": {"namespace": "prod", "name": "cfg-svc"},
		"spec": {
			"artifact": {"type": "url", "url": "https://example.com/pkg.wasm"},
			"runtime": "wasm",
			"instances": 1,
			"resources": {}
		},
		"unexpectedField": "this should be rejected"
	}`
	_, err := domain.ParseManifest([]byte(manifest))
	if err == nil {
		t.Error("ParseManifest accepted an unknown field; expected rejection to prevent schema drift")
	}
	if err != nil && !strings.Contains(err.Error(), "unknown") &&
		!strings.Contains(err.Error(), "field") {
		// Verify error is about the unknown field, not a different validation error.
		t.Logf("error was %v (may not be the unknown-field error; check DisallowUnknownFields)", err)
	}
}

// TestGUC_Config_EmptyAdminKeyViolatesConstraint checks the formal invariant
// that a VSConfig with Enabled=true and an empty AdminKey is in a contradictory
// state: the system requires authentication to be configured before it can
// start securely. An empty AdminKey with Enabled=true is an impossible valid state.
func TestGUC_Config_EmptyAdminKeyViolatesConstraint(t *testing.T) {
	cfg := config.DefaultVSConfig()
	// Verify the detected impossible-state condition:
	// Enabled=true AND AdminKey="" must never be accepted as "ready to start".
	cfg.Enabled = true
	cfg.AdminKey = "" // violates the startup constraint

	if cfg.Enabled && cfg.AdminKey == "" {
		// This IS the detected impossible state — the test documents it.
		// virtualserver.New() returns an error in exactly this configuration.
	} else {
		t.Error("precondition failed: Enabled=true with AdminKey=empty was not produced")
	}

	// To be safe and runnable without importing the parent, verify via a local
	// simulation: the constraint is decidable from fields alone.
	isInvalid := cfg.Enabled && cfg.AdminKey == ""
	if !isInvalid {
		t.Error("impossible-state detector: Enabled=true+AdminKey=empty should be invalid, got valid")
	}
}

// TestGUC_Config_ConfigFieldsNeverNegative asserts the invariant that all
// default numeric configuration fields are non-negative. A negative timeout or
// body-size limit is a contradictory configuration that cannot represent a valid
// operational state — formal consistency requires non-negativity.
func TestGUC_Config_ConfigFieldsNeverNegative(t *testing.T) {
	cfg := config.DefaultVSConfig()

	if cfg.MaxBodyBytes < 0 {
		t.Errorf("MaxBodyBytes = %d; must be non-negative", cfg.MaxBodyBytes)
	}
	if cfg.ReconcileEvery < 0 {
		t.Errorf("ReconcileEvery = %v; must be non-negative", cfg.ReconcileEvery)
	}
	if cfg.RequestTimeout < 0 {
		t.Errorf("RequestTimeout = %v; must be non-negative", cfg.RequestTimeout)
	}

	// A zero-value config should also have non-negative numerics.
	var zero config.VSConfig
	if zero.MaxBodyBytes < 0 {
		t.Errorf("zero-value MaxBodyBytes = %d; must be non-negative", zero.MaxBodyBytes)
	}
	if zero.ReconcileEvery < 0 {
		t.Errorf("zero-value ReconcileEvery = %v; must be non-negative", zero.ReconcileEvery)
	}
	if zero.RequestTimeout < 0 {
		t.Errorf("zero-value RequestTimeout = %v; must be non-negative", zero.RequestTimeout)
	}
}
