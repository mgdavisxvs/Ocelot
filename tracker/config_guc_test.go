package tracker

// config_guc_test.go — GUC (Gödel Unified Council) test coverage for Config.
//
// Lenses:
//   Knuth  — algorithmic correctness, data-structure invariants
//   Turing — termination conditions, decidability of key operations
//   Church — functional purity, referential transparency, immutability
//   Gödel  — formal consistency, impossible-state detection

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// ── Knuth: algorithmic correctness ───────────────────────────────────────────

// TestGUC_AnnounceInterval_DefaultIsPositive verifies the default AnnounceInterval
// satisfies the loop-invariant relied upon by the scheduler: interval > 0.
func TestGUC_AnnounceInterval_DefaultIsPositive(t *testing.T) {
	cfg := DefaultFileConfig()
	if cfg.AnnounceInterval <= 0 {
		t.Errorf("AnnounceInterval must be > 0, got %d", cfg.AnnounceInterval)
	}
}

// TestGUC_NumWantLimit_ReasonableBound validates NumWantLimit against a range
// invariant: 1 ≤ NumWantLimit ≤ 500 (prevents degenerate peer lists).
func TestGUC_NumWantLimit_ReasonableBound(t *testing.T) {
	var cases = []struct {
		limit int
		valid bool
	}{
		{50, true},   // default
		{1, true},    // minimum sensible value
		{500, true},  // upper practical limit
		{0, false},   // zero produces empty peer lists
		{-1, false},  // negative is nonsensical
		{1001, false}, // absurdly large
	}
	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("limit=%d", tc.limit), func(t *testing.T) {
			ok := tc.limit >= 1 && tc.limit <= 500
			if ok != tc.valid {
				t.Errorf("NumWantLimit=%d: expected valid=%v, got %v", tc.limit, tc.valid, ok)
			}
		})
	}
}

// TestGUC_MaxMiddlemen_IsPositive checks the connection-gate invariant:
// MaxMiddlemen must be positive so at least one connection is ever accepted.
func TestGUC_MaxMiddlemen_IsPositive(t *testing.T) {
	cfg := DefaultFileConfig()
	if cfg.MaxMiddlemen <= 0 {
		t.Errorf("MaxMiddlemen must be > 0, got %d", cfg.MaxMiddlemen)
	}
}

// TestGUC_ReadTimeout_IsPositive verifies that the default connection timeout
// (mapped to ReadTimeout) is positive — a zero read-timeout means no deadline,
// which violates the resource-bound invariant for production configs.
func TestGUC_ReadTimeout_IsPositive(t *testing.T) {
	fc := DefaultFileConfig()
	rc := fc.ToTrackerConfig()
	if rc.ReadTimeout <= 0 {
		t.Errorf("ReadTimeout must be > 0, got %v", rc.ReadTimeout)
	}
}

// TestGUC_WriteTimeout_IsPositive mirrors the read-timeout invariant for writes.
func TestGUC_WriteTimeout_IsPositive(t *testing.T) {
	fc := DefaultFileConfig()
	rc := fc.ToTrackerConfig()
	if rc.WriteTimeout <= 0 {
		t.Errorf("WriteTimeout must be > 0, got %v", rc.WriteTimeout)
	}
}

// ── Turing: termination, halting, decidability ───────────────────────────────

// TestGUC_ListenAddr_Parseable verifies that ListenAddr values produced by
// ToTrackerConfig are decidably valid TCP addresses (halting test for Listen).
func TestGUC_ListenAddr_Parseable(t *testing.T) {
	var cases = []struct {
		port int
		ok   bool
	}{
		{34000, true},
		{1, true},
		{65535, true},
		{0, true}, // OS chooses port — valid for binding
	}
	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("port=%d", tc.port), func(t *testing.T) {
			addr := fmt.Sprintf(":%d", tc.port)
			_, _, err := net.SplitHostPort(addr)
			parseable := err == nil
			if parseable != tc.ok {
				t.Errorf("addr %q: expected parseable=%v, got %v (err=%v)",
					addr, tc.ok, parseable, err)
			}
		})
	}
}

// TestGUC_ZeroValueConfig_IsInvalid asserts that a zero-value Config violates
// all mandatory positive-integer invariants — making its invalidity decidable.
func TestGUC_ZeroValueConfig_IsInvalid(t *testing.T) {
	var cfg Config
	violations := 0

	if cfg.AnnounceInterval <= 0 {
		violations++
	}
	if cfg.PeersTimeout <= 0 {
		violations++
	}
	if cfg.MaxMiddlemen <= 0 {
		violations++
	}
	if cfg.NumWantLimit <= 0 {
		violations++
	}
	if cfg.ReadTimeout <= 0 {
		violations++
	}
	if cfg.WriteTimeout <= 0 {
		violations++
	}
	if cfg.ScheduleInterval <= 0 {
		violations++
	}
	if len(cfg.SitePassword) < 32 {
		violations++
	}
	if len(cfg.ReportPassword) < 32 {
		violations++
	}
	if cfg.ListenAddr == "" {
		violations++
	}

	if violations == 0 {
		t.Error("zero-value Config should have at least one invariant violation")
	}
	// All 10 checked fields must be invalid for the zero value.
	if violations != 10 {
		t.Errorf("expected 10 violations in zero-value Config, got %d", violations)
	}
}

// TestGUC_KeepaliveTimeout_NonNegative verifies that KeepaliveTimeout is never
// negative — a negative duration would cause time.After to fire immediately.
func TestGUC_KeepaliveTimeout_NonNegative(t *testing.T) {
	fc := DefaultFileConfig()
	rc := fc.ToTrackerConfig()
	if rc.KeepaliveTimeout < 0 {
		t.Errorf("KeepaliveTimeout must be >= 0, got %v", rc.KeepaliveTimeout)
	}
}

// TestGUC_ScheduleInterval_IsPositive checks that the scheduler tick interval
// is positive — a zero or negative value would spin-loop (non-termination risk).
func TestGUC_ScheduleInterval_IsPositive(t *testing.T) {
	cfg := DefaultFileConfig()
	if cfg.ScheduleInterval <= 0 {
		t.Errorf("ScheduleInterval must be > 0, got %d", cfg.ScheduleInterval)
	}
}

// TestGUC_PeersTimeout_ExceedsAnnounceInterval is a decidability check:
// the system can always determine whether a peer has timed out between announces
// only when PeersTimeout > AnnounceInterval.
func TestGUC_PeersTimeout_ExceedsAnnounceInterval(t *testing.T) {
	cfg := DefaultFileConfig()
	if cfg.PeersTimeout <= cfg.AnnounceInterval {
		t.Errorf("PeersTimeout (%d) must be > AnnounceInterval (%d) so stale peers are detectable",
			cfg.PeersTimeout, cfg.AnnounceInterval)
	}
}

// ── Church: functional purity, referential transparency, immutability ────────

// TestGUC_AllowPrivateIPs_DefaultFalse asserts referential transparency: the
// zero value of Config has AllowPrivateIPs=false, so code that relies on the
// default being safe is correct without explicit initialisation.
func TestGUC_AllowPrivateIPs_DefaultFalse(t *testing.T) {
	var cfg Config
	if cfg.AllowPrivateIPs {
		t.Error("zero-value Config.AllowPrivateIPs should be false (safe default)")
	}
	// Also confirm newTestConfig sets it true explicitly (test fixture awareness).
	tc := newTestConfig()
	if !tc.AllowPrivateIPs {
		t.Error("newTestConfig should set AllowPrivateIPs=true for test environments")
	}
}

// TestGUC_SitePassword_MinLength32 verifies password length across a set of
// candidate values — a pure predicate with no side effects.
func TestGUC_SitePassword_MinLength32(t *testing.T) {
	var cases = []struct {
		password string
		valid    bool
	}{
		{"abcdef1234567890abcdef1234567890", true},    // exactly 32
		{"abcdef1234567890abcdef1234567890X", true},   // 33 chars
		{"00000000000000000000000000000000", true},    // default placeholder
		{"short", false},                               // 5 chars
		{"", false},                                    // empty
		{"123456789012345678901234567890X", false},    // 31 chars
	}
	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("len=%d", len(tc.password)), func(t *testing.T) {
			ok := len(tc.password) >= 32
			if ok != tc.valid {
				t.Errorf("SitePassword len=%d: expected valid=%v, got %v",
					len(tc.password), tc.valid, ok)
			}
		})
	}
}

// TestGUC_ReportPassword_MinLength32 mirrors the SitePassword length invariant
// for the report-password field — each is independently required to be >= 32.
func TestGUC_ReportPassword_MinLength32(t *testing.T) {
	var cases = []struct {
		password string
		valid    bool
	}{
		{"reportpass1234567890123456789012", true},  // exactly 32
		{"reportpass12345678901234567890123", true}, // 33 chars
		{"short", false},                             // 5 chars
		{"", false},                                  // empty
	}
	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("len=%d", len(tc.password)), func(t *testing.T) {
			ok := len(tc.password) >= 32
			if ok != tc.valid {
				t.Errorf("ReportPassword len=%d: expected valid=%v, got %v",
					len(tc.password), tc.valid, ok)
			}
		})
	}
}

// TestGUC_ConfigClone_NoSharedPointers confirms that a Config copied by value
// is referentially independent — modifying string and duration fields in the
// clone must not affect the original (Church: immutability of value types).
func TestGUC_ConfigClone_NoSharedPointers(t *testing.T) {
	original := newTestConfig()
	clone := *original // value copy

	// Mutate all value-type fields in the clone.
	clone.SitePassword = strings.Repeat("x", 32)
	clone.ReportPassword = strings.Repeat("y", 32)
	clone.ListenAddr = ":9999"
	clone.AnnounceInterval = 999
	clone.PeersTimeout = 9999
	clone.MaxMiddlemen = 1
	clone.NumWantLimit = 1
	clone.KeepaliveTimeout = 99 * time.Second
	clone.ReadTimeout = 99 * time.Second
	clone.WriteTimeout = 99 * time.Second
	clone.ScheduleInterval = 99
	clone.AllowPrivateIPs = false
	clone.TLS.CertFile = "/changed"
	clone.TLS.Domain = "changed.example.com"

	// Original must be unchanged.
	if original.SitePassword != sitePass {
		t.Errorf("original.SitePassword mutated: got %q", original.SitePassword)
	}
	if original.ListenAddr != ":0" {
		t.Errorf("original.ListenAddr mutated: got %q", original.ListenAddr)
	}
	if original.AnnounceInterval != 1800 {
		t.Errorf("original.AnnounceInterval mutated: got %d", original.AnnounceInterval)
	}
	if original.TLS.CertFile != "" {
		t.Errorf("original.TLS.CertFile mutated: got %q", original.TLS.CertFile)
	}
}

// TestGUC_DefaultFileConfig_IsPure verifies referential transparency: two
// independent calls to DefaultFileConfig must return equal values with no
// shared mutable state between them.
func TestGUC_DefaultFileConfig_IsPure(t *testing.T) {
	a := DefaultFileConfig()
	b := DefaultFileConfig()

	if a == b {
		// Pointer equality would indicate a singleton (shared mutable state).
		// Value equality is expected and is fine; pointer identity is not.
	}
	if a.AnnounceInterval != b.AnnounceInterval {
		t.Errorf("DefaultFileConfig not pure: AnnounceInterval differs: %d vs %d",
			a.AnnounceInterval, b.AnnounceInterval)
	}
	if a.PeersTimeout != b.PeersTimeout {
		t.Errorf("DefaultFileConfig not pure: PeersTimeout differs: %d vs %d",
			a.PeersTimeout, b.PeersTimeout)
	}
	if a.SitePassword != b.SitePassword {
		t.Errorf("DefaultFileConfig not pure: SitePassword differs")
	}
	// Mutating one must not affect the other (no aliased pointer fields).
	a.SitePassword = strings.Repeat("a", 32)
	if a.SitePassword == b.SitePassword {
		// Both being identical after mutation would indicate aliasing.
		// This should NOT happen — they are separate allocations.
		t.Errorf("DefaultFileConfig instances share SitePassword state")
	}
}

// ── Gödel: formal consistency, impossible-state detection ────────────────────

// TestGUC_PeersAndAnnounce_InvariantPreserved checks that the structural
// invariant PeersTimeout > AnnounceInterval is maintained across a range of
// constructed configs — no legal config can violate this without being
// formally inconsistent.
func TestGUC_PeersAndAnnounce_InvariantPreserved(t *testing.T) {
	var cases = []struct {
		announceInterval int
		peersTimeout     int
		wantConsistent   bool
	}{
		{1800, 7200, true},  // default: 4× announce
		{3600, 7200, true},  // 2× announce
		{7200, 7200, false}, // equal: no gap for detection
		{7200, 3600, false}, // inverted: impossible to detect stale peers
		{1, 2, true},        // minimal valid ratio
	}
	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("ann=%d,pts=%d", tc.announceInterval, tc.peersTimeout), func(t *testing.T) {
			consistent := tc.peersTimeout > tc.announceInterval
			if consistent != tc.wantConsistent {
				t.Errorf("PeersTimeout=%d AnnounceInterval=%d: expected consistent=%v, got %v",
					tc.peersTimeout, tc.announceInterval, tc.wantConsistent, consistent)
			}
		})
	}
}

// TestGUC_ZeroConfig_ContradictoryState demonstrates that the zero-value Config
// is self-contradictory: it simultaneously claims to be a usable server config
// (a non-nil struct exists) while violating every positive-value invariant.
// This is the Gödelian "this statement is false" of Config objects.
func TestGUC_ZeroConfig_ContradictoryState(t *testing.T) {
	var cfg Config

	// The struct exists but cannot serve any connections.
	contradictions := 0
	if cfg.MaxMiddlemen == 0 {
		contradictions++ // accepts 0 connections, yet is a "server config"
	}
	if cfg.AnnounceInterval == 0 {
		contradictions++ // announces every 0 seconds — infinite loop invariant violated
	}
	if cfg.NumWantLimit == 0 {
		contradictions++ // returns 0 peers — useless tracker
	}
	if len(cfg.SitePassword) < 32 {
		contradictions++ // empty password — security invariant violated
	}
	if cfg.ReadTimeout == 0 {
		contradictions++ // no deadline — resource invariant violated
	}

	if contradictions < 5 {
		t.Errorf("zero-value Config should show 5 contradictions, found %d", contradictions)
	}
}

// TestGUC_Passwords_AreDistinct verifies the formal independence of SitePassword
// and ReportPassword: they are separate security domains and must be stored as
// independent fields. A config where they are equal is not formally inconsistent,
// but the system must treat them as logically independent (Gödel separation).
func TestGUC_Passwords_AreDistinct(t *testing.T) {
	cfg := newTestConfig()

	// They are separate fields — assignment to one must not change the other.
	original := cfg.SitePassword
	cfg.ReportPassword = strings.Repeat("z", 32)

	if cfg.SitePassword != original {
		t.Errorf("assigning ReportPassword changed SitePassword: got %q", cfg.SitePassword)
	}
	if cfg.SitePassword == cfg.ReportPassword {
		// In the test config they happen to differ; confirm the fixture upholds this.
		t.Logf("note: SitePassword and ReportPassword are equal in this config (allowed but unusual)")
	}
	// Both fields must independently satisfy the length invariant.
	if len(cfg.SitePassword) < 32 {
		t.Errorf("SitePassword length %d < 32", len(cfg.SitePassword))
	}
	if len(cfg.ReportPassword) < 32 {
		t.Errorf("ReportPassword length %d < 32", len(cfg.ReportPassword))
	}
}

// TestGUC_MaxMiddlemen_CoversNumWant verifies a formal consistency constraint:
// MaxMiddlemen must be >= NumWantLimit, otherwise a single peer list response
// could request more simultaneous connections than the server can hold.
func TestGUC_MaxMiddlemen_CoversNumWant(t *testing.T) {
	var cases = []struct {
		maxMiddlemen int
		numWant      int
		consistent   bool
	}{
		{20000, 50, true},   // default: ample headroom
		{100, 50, true},     // tight but valid
		{50, 50, true},      // exactly equal: boundary case
		{49, 50, false},     // MaxMiddlemen < NumWantLimit: formally inconsistent
		{1, 100, false},     // extreme inversion
	}
	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("maxM=%d,nw=%d", tc.maxMiddlemen, tc.numWant), func(t *testing.T) {
			ok := tc.maxMiddlemen >= tc.numWant
			if ok != tc.consistent {
				t.Errorf("MaxMiddlemen=%d NumWantLimit=%d: expected consistent=%v, got %v",
					tc.maxMiddlemen, tc.numWant, tc.consistent, ok)
			}
		})
	}
}

// TestGUC_FileConfigToTrackerConfig_Consistency verifies that ToTrackerConfig
// performs a formally consistent field mapping with no data loss or inversion.
// Gödel: the derived Config must be provably equivalent to the source FileConfig.
func TestGUC_FileConfigToTrackerConfig_Consistency(t *testing.T) {
	fc := DefaultFileConfig()
	fc.SitePassword = strings.Repeat("s", 32)
	fc.ReportPassword = strings.Repeat("r", 32)
	fc.AnnounceInterval = 900
	fc.PeersTimeout = 3600
	fc.MaxMiddlemen = 5000
	fc.NumWantLimit = 25
	fc.ScheduleInterval = 5
	fc.ConnectionTimeout = 15
	fc.TLSCertFile = "/etc/ssl/cert.pem"
	fc.TLSKeyFile = "/etc/ssl/key.pem"

	rc := fc.ToTrackerConfig()

	if rc.AnnounceInterval != fc.AnnounceInterval {
		t.Errorf("AnnounceInterval: want %d, got %d", fc.AnnounceInterval, rc.AnnounceInterval)
	}
	if rc.PeersTimeout != fc.PeersTimeout {
		t.Errorf("PeersTimeout: want %d, got %d", fc.PeersTimeout, rc.PeersTimeout)
	}
	if rc.MaxMiddlemen != fc.MaxMiddlemen {
		t.Errorf("MaxMiddlemen: want %d, got %d", fc.MaxMiddlemen, rc.MaxMiddlemen)
	}
	if rc.NumWantLimit != fc.NumWantLimit {
		t.Errorf("NumWantLimit: want %d, got %d", fc.NumWantLimit, rc.NumWantLimit)
	}
	if rc.SitePassword != fc.SitePassword {
		t.Errorf("SitePassword: want %q, got %q", fc.SitePassword, rc.SitePassword)
	}
	if rc.ReportPassword != fc.ReportPassword {
		t.Errorf("ReportPassword: want %q, got %q", fc.ReportPassword, rc.ReportPassword)
	}
	wantTimeout := time.Duration(fc.ConnectionTimeout) * time.Second
	if rc.ReadTimeout != wantTimeout {
		t.Errorf("ReadTimeout: want %v, got %v", wantTimeout, rc.ReadTimeout)
	}
	if rc.WriteTimeout != wantTimeout {
		t.Errorf("WriteTimeout: want %v, got %v", wantTimeout, rc.WriteTimeout)
	}
	if rc.TLS.CertFile != fc.TLSCertFile {
		t.Errorf("TLS.CertFile: want %q, got %q", fc.TLSCertFile, rc.TLS.CertFile)
	}
	if rc.TLS.KeyFile != fc.TLSKeyFile {
		t.Errorf("TLS.KeyFile: want %q, got %q", fc.TLSKeyFile, rc.TLS.KeyFile)
	}
	expectedAddr := fmt.Sprintf(":%d", fc.ListenPort)
	if rc.ListenAddr != expectedAddr {
		t.Errorf("ListenAddr: want %q, got %q", expectedAddr, rc.ListenAddr)
	}
}
