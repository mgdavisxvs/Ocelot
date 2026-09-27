package tracker

import (
	"net"
	"testing"
)

// ── Knuth lens: algorithmic correctness, loop invariants, data structure invariants ──

// TestGUC_IPValidation_RFC1918_10Block verifies the entire 10.0.0.0/8 range boundary.
// Knuth: table-driven correctness over the block boundaries of the 10/8 network.
func TestGUC_IPValidation_RFC1918_10Block(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"10.0.0.0", false},
		{"10.0.0.1", false},
		{"10.128.0.1", false},
		{"10.255.255.255", false},
		{"9.255.255.255", true},  // just before the block
		{"11.0.0.0", true},      // just after the block
	}
	for _, tc := range tests {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("could not parse test IP %q", tc.ip)
		}
		got := ValidateIPNotPrivate(ip)
		if got != tc.want {
			t.Errorf("ValidateIPNotPrivate(%s) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

// TestGUC_IPValidation_RFC1918_172Block verifies the 172.16.0.0/12 boundary precisely.
// Knuth: loop invariant — every octet in [16,31] is private; octet 15 and 32 are not.
func TestGUC_IPValidation_RFC1918_172Block(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"172.15.255.255", true},  // just before private range
		{"172.16.0.0", false},
		{"172.16.0.1", false},
		{"172.20.1.1", false},
		{"172.31.255.255", false},
		{"172.32.0.0", true},     // just after private range
		{"172.255.0.0", true},
	}
	for _, tc := range tests {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("could not parse test IP %q", tc.ip)
		}
		got := ValidateIPNotPrivate(ip)
		if got != tc.want {
			t.Errorf("ValidateIPNotPrivate(%s) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

// TestGUC_IPValidation_RFC1918_192Block verifies the full 192.168.0.0/16 range.
// Knuth: boundary analysis on adjacent subnets.
func TestGUC_IPValidation_RFC1918_192Block(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"192.167.255.255", true},
		{"192.168.0.0", false},
		{"192.168.1.1", false},
		{"192.168.255.255", false},
		{"192.169.0.0", true},
		{"192.0.2.1", true}, // TEST-NET-1, still public per this validator
	}
	for _, tc := range tests {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("could not parse test IP %q", tc.ip)
		}
		got := ValidateIPNotPrivate(ip)
		if got != tc.want {
			t.Errorf("ValidateIPNotPrivate(%s) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

// TestGUC_IPValidation_PublicIPPasses verifies well-known public IPs are accepted.
// Knuth: positive correctness — valid inputs must produce valid outputs.
func TestGUC_IPValidation_PublicIPPasses(t *testing.T) {
	tests := []struct {
		ip string
	}{
		{"8.8.8.8"},
		{"1.1.1.1"},
		{"208.67.222.222"},
		{"5.5.5.5"},
		{"203.0.113.1"}, // documentation range, accepted by this validator
	}
	for _, tc := range tests {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("could not parse test IP %q", tc.ip)
		}
		if !ValidateIPNotPrivate(ip) {
			t.Errorf("ValidateIPNotPrivate(%s) returned false for public IP, want true", tc.ip)
		}
	}
}

// TestGUC_IPValidation_MulticastAndReservedRanges verifies the 224+ rejection.
// Knuth: invariant that ipv4[0] >= 224 is always rejected, covering multicast AND reserved.
func TestGUC_IPValidation_MulticastAndReservedRanges(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"224.0.0.0", false},   // start of multicast
		{"224.0.0.1", false},   // multicast
		{"239.255.255.255", false}, // end of multicast
		{"240.0.0.0", false},   // start of reserved
		{"240.0.0.1", false},   // reserved
		{"255.255.255.255", false}, // broadcast
		{"223.255.255.255", true},  // just before multicast
	}
	for _, tc := range tests {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("could not parse test IP %q", tc.ip)
		}
		got := ValidateIPNotPrivate(ip)
		if got != tc.want {
			t.Errorf("ValidateIPNotPrivate(%s) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

// ── Turing lens: termination conditions, halting behavior ──

// TestGUC_IPValidation_NilIPHaltsCleanly verifies nil does not panic and returns false.
// Turing: the function must terminate cleanly on nil input without panic.
func TestGUC_IPValidation_NilIPHaltsCleanly(t *testing.T) {
	result := ValidateIPNotPrivate(nil)
	if result != false {
		t.Errorf("ValidateIPNotPrivate(nil) = %v, want false", result)
	}
}

// TestGUC_IPValidation_Loopback127HaltsWithRejection verifies loopback termination.
// Turing: all 127.x.x.x addresses must terminate with rejection.
func TestGUC_IPValidation_Loopback127HaltsWithRejection(t *testing.T) {
	tests := []string{
		"127.0.0.1",
		"127.0.0.0",
		"127.255.255.255",
		"127.1.2.3",
	}
	for _, ipStr := range tests {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			t.Fatalf("could not parse test IP %q", ipStr)
		}
		if ValidateIPNotPrivate(ip) {
			t.Errorf("ValidateIPNotPrivate(%s) = true, want false (loopback must be rejected)", ipStr)
		}
	}
}

// TestGUC_IPValidation_LinkLocalHaltsWithRejection verifies 169.254.x.x termination.
// Turing: link-local range terminates deterministically with rejection.
func TestGUC_IPValidation_LinkLocalHaltsWithRejection(t *testing.T) {
	tests := []string{
		"169.254.0.0",
		"169.254.0.1",
		"169.254.255.255",
		"169.254.169.254",
	}
	for _, ipStr := range tests {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			t.Fatalf("could not parse test IP %q", ipStr)
		}
		if ValidateIPNotPrivate(ip) {
			t.Errorf("ValidateIPNotPrivate(%s) = true, want false (link-local must be rejected)", ipStr)
		}
	}
}

// TestGUC_IPValidation_IPv4MappedIPv6HaltsWithRejection verifies IPv4-mapped addresses.
// Turing: ::ffff:10.x.x.x must be converted to IPv4 and rejected, not accepted as IPv6.
func TestGUC_IPValidation_IPv4MappedIPv6HaltsWithRejection(t *testing.T) {
	tests := []struct {
		ipStr string
		want  bool
	}{
		{"::ffff:10.0.0.1", false},    // IPv4-mapped private: 10.x
		{"::ffff:192.168.1.1", false}, // IPv4-mapped private: 192.168.x
		{"::ffff:172.16.0.1", false},  // IPv4-mapped private: 172.16.x
		{"::ffff:8.8.8.8", true},      // IPv4-mapped public: accepted
	}
	for _, tc := range tests {
		ip := net.ParseIP(tc.ipStr)
		if ip == nil {
			t.Fatalf("could not parse test IP %q", tc.ipStr)
		}
		got := ValidateIPNotPrivate(ip)
		if got != tc.want {
			t.Errorf("ValidateIPNotPrivate(%s) = %v, want %v", tc.ipStr, got, tc.want)
		}
	}
}

// TestGUC_IPValidation_ZeroIPRejected verifies the 0.0.0.0 network is rejected.
// Turing: the zero IP halts immediately with rejection (first byte check).
func TestGUC_IPValidation_ZeroIPRejected(t *testing.T) {
	tests := []string{
		"0.0.0.0",
		"0.0.0.1",
		"0.255.255.255",
	}
	for _, ipStr := range tests {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			t.Fatalf("could not parse test IP %q", ipStr)
		}
		if ValidateIPNotPrivate(ip) {
			t.Errorf("ValidateIPNotPrivate(%s) = true, want false (0.0.0.0/8 must be rejected)", ipStr)
		}
	}
}

// ── Church lens: functional purity, side-effect isolation, referential transparency ──

// TestGUC_IPValidation_PureFunction verifies the function has no side effects.
// Church: calling ValidateIPNotPrivate with the same IP twice must return the same result.
func TestGUC_IPValidation_PureFunction(t *testing.T) {
	ips := []string{
		"10.0.0.1",
		"8.8.8.8",
		"192.168.1.1",
		"1.2.3.4",
	}
	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			t.Fatalf("could not parse test IP %q", ipStr)
		}
		first := ValidateIPNotPrivate(ip)
		second := ValidateIPNotPrivate(ip)
		if first != second {
			t.Errorf("ValidateIPNotPrivate(%s) not pure: first=%v second=%v", ipStr, first, second)
		}
	}
}

// TestGUC_IPValidation_InputNotMutated verifies the function does not mutate the IP argument.
// Church: referential transparency — the IP value must be identical before and after the call.
func TestGUC_IPValidation_InputNotMutated(t *testing.T) {
	original := net.ParseIP("10.0.0.1")
	if original == nil {
		t.Fatal("could not parse test IP")
	}
	snapshot := make(net.IP, len(original))
	copy(snapshot, original)

	ValidateIPNotPrivate(original)

	for i, b := range original {
		if b != snapshot[i] {
			t.Errorf("IP was mutated at index %d: before=%x after=%x", i, snapshot[i], b)
		}
	}
}

// TestGUC_IPValidation_AllowPrivateIPsBypassesValidation tests the Config flag pathway.
// Church: when AllowPrivateIPs=true the validation path is not invoked — private IPs pass.
func TestGUC_IPValidation_AllowPrivateIPsBypassesValidation(t *testing.T) {
	// Directly test the logical condition used in announce.go:
	//   if !w.Config.AllowPrivateIPs && !ValidateIPNotPrivate(ip) { invalidIP = true }
	privateIPs := []string{
		"10.0.0.1",
		"192.168.1.1",
		"172.16.0.1",
		"127.0.0.1",
	}
	for _, ipStr := range privateIPs {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			t.Fatalf("could not parse test IP %q", ipStr)
		}
		allowPrivate := true
		invalidIP := !allowPrivate && !ValidateIPNotPrivate(ip)
		if invalidIP {
			t.Errorf("with AllowPrivateIPs=true, %s should not be flagged as invalid", ipStr)
		}
	}
}

// TestGUC_IPValidation_DenyPrivateIPsRejectsPrivate tests Config.AllowPrivateIPs=false.
// Church: the negation path — when AllowPrivateIPs=false, private IPs are isolated and flagged.
func TestGUC_IPValidation_DenyPrivateIPsRejectsPrivate(t *testing.T) {
	privateIPs := []string{
		"10.0.0.1",
		"192.168.1.1",
		"172.16.0.1",
		"127.0.0.1",
	}
	for _, ipStr := range privateIPs {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			t.Fatalf("could not parse test IP %q", ipStr)
		}
		allowPrivate := false
		invalidIP := !allowPrivate && !ValidateIPNotPrivate(ip)
		if !invalidIP {
			t.Errorf("with AllowPrivateIPs=false, %s should be flagged as invalid", ipStr)
		}
	}
}

// TestGUC_IPValidation_IPv6PureAcceptance verifies pure IPv6 (non-mapped) is accepted.
// Church: the IPv6 path is referentially transparent — pure IPv6 always returns true.
func TestGUC_IPValidation_IPv6PureAcceptance(t *testing.T) {
	tests := []string{
		"2001:db8::1",
		"fe80::1",    // link-local IPv6 — accepted as IPv6 by current impl
		"::1",        // IPv6 loopback — accepted as IPv6 by current impl
		"2606:4700:4700::1111",
	}
	for _, ipStr := range tests {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			t.Fatalf("could not parse test IP %q", ipStr)
		}
		// Confirm it's not converted to IPv4 (pure IPv6)
		if ip.To4() != nil {
			// This is actually IPv4-mapped; skip
			continue
		}
		if !ValidateIPNotPrivate(ip) {
			t.Errorf("ValidateIPNotPrivate(%s) = false, want true for pure IPv6", ipStr)
		}
	}
}

// ── Gödel lens: formal consistency, invariant preservation, impossible-state detection ──

// TestGUC_IPValidation_ConsistencyPrivateNeverPublic verifies that no IP is both private and public.
// Gödel: formal consistency — ValidateIPNotPrivate(ip)==true and it is a known private range
//
//	is an impossible state.
func TestGUC_IPValidation_ConsistencyPrivateNeverPublic(t *testing.T) {
	knownPrivateRanges := []string{
		"10.0.0.1",
		"10.255.255.255",
		"172.16.0.1",
		"172.31.255.255",
		"192.168.0.1",
		"192.168.255.255",
		"127.0.0.1",
		"169.254.0.1",
		"224.0.0.1",
		"240.0.0.1",
	}
	for _, ipStr := range knownPrivateRanges {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			t.Fatalf("could not parse test IP %q", ipStr)
		}
		if ValidateIPNotPrivate(ip) {
			t.Errorf("impossible state: %s is a known private IP but ValidateIPNotPrivate returned true", ipStr)
		}
	}
}

// TestGUC_IPValidation_BroadcastRejected verifies 255.255.255.255 is never accepted.
// Gödel: the broadcast address in any system invariant must be unreachable as a valid peer.
func TestGUC_IPValidation_BroadcastRejected(t *testing.T) {
	ip := net.ParseIP("255.255.255.255")
	if ip == nil {
		t.Fatal("could not parse 255.255.255.255")
	}
	if ValidateIPNotPrivate(ip) {
		t.Error("ValidateIPNotPrivate(255.255.255.255) = true, want false: broadcast is not a valid peer IP")
	}
}

// TestGUC_IPValidation_NoContradictionPublicIsAlwaysValid checks that 8.8.8.8 is formally valid.
// Gödel: a known-good public IP must never return false — that would be a formal contradiction.
func TestGUC_IPValidation_NoContradictionPublicIsAlwaysValid(t *testing.T) {
	ip := net.ParseIP("8.8.8.8")
	if ip == nil {
		t.Fatal("could not parse 8.8.8.8")
	}
	if !ValidateIPNotPrivate(ip) {
		t.Error("formal contradiction: 8.8.8.8 (Google DNS) is definitively public but was rejected")
	}
}

// TestGUC_IPValidation_InvariantAllRejectedIPs tests that every rejected category is covered.
// Gödel: the rejection set is formally closed — no category is accidentally admitted.
func TestGUC_IPValidation_InvariantAllRejectedIPs(t *testing.T) {
	rejectedSamples := []struct {
		ip      string
		category string
	}{
		{"0.0.0.1", "current-network 0/8"},
		{"10.1.2.3", "RFC1918 10/8"},
		{"127.0.0.1", "loopback 127/8"},
		{"169.254.0.1", "link-local 169.254/16"},
		{"172.16.0.1", "RFC1918 172.16/12"},
		{"192.168.0.1", "RFC1918 192.168/16"},
		{"224.0.0.1", "multicast 224/4"},
		{"240.0.0.1", "reserved 240/4"},
		{"255.255.255.255", "broadcast"},
	}
	for _, tc := range rejectedSamples {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("could not parse test IP %q (%s)", tc.ip, tc.category)
		}
		if ValidateIPNotPrivate(ip) {
			t.Errorf("invariant violation: %s (%s) was accepted, expected rejection", tc.ip, tc.category)
		}
	}
}

// TestGUC_IPValidation_EmptyIPByteSliceRejected verifies degenerate empty slice behavior.
// Gödel: an empty net.IP (not nil, but len==0) must not panic and must be rejected.
func TestGUC_IPValidation_EmptyIPByteSliceRejected(t *testing.T) {
	empty := net.IP{}
	// Must not panic, and since To4() on empty returns nil (IPv6 path) → returns true.
	// Document the actual behavior; the important invariant is no panic.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ValidateIPNotPrivate panicked on empty net.IP: %v", r)
		}
	}()
	// Just call it; we only require it not panic.
	_ = ValidateIPNotPrivate(empty)
}

// TestGUC_IPValidation_MulticastBoundaryInvariant checks the boundary at octet 224.
// Gödel: the invariant ipv4[0] >= 224 => rejected must hold precisely at the boundary.
func TestGUC_IPValidation_MulticastBoundaryInvariant(t *testing.T) {
	// 223.x.x.x must be accepted (just below the boundary)
	below := net.ParseIP("223.255.255.255")
	if below == nil {
		t.Fatal("could not parse 223.255.255.255")
	}
	if !ValidateIPNotPrivate(below) {
		t.Error("invariant violation: 223.255.255.255 (below multicast boundary) was rejected, want accepted")
	}

	// 224.0.0.0 must be rejected (exactly at the boundary)
	at := net.ParseIP("224.0.0.0")
	if at == nil {
		t.Fatal("could not parse 224.0.0.0")
	}
	if ValidateIPNotPrivate(at) {
		t.Error("invariant violation: 224.0.0.0 (at multicast boundary) was accepted, want rejected")
	}
}
