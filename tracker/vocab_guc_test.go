package tracker

import (
	"strings"
	"testing"
)

// ── Knuth: algorithmic correctness, loop invariants, data structure invariants ─

// TestGUC_WireFormat_ValidFormatsTableDriven checks every valid wire_format.format value
// is accepted by Validate (table-driven).
func TestGUC_WireFormat_ValidFormatsTableDriven(t *testing.T) {
	tests := []struct {
		name   string
		format string
	}{
		{"bencode", "bencode"},
		{"json", "json"},
		{"msgpack", "msgpack"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cfg := &VocabularyConfig{
				Domain:     "test",
				WireFormat: WireFormatVocab{Format: tc.format},
				Auth:       AuthVocab{Strategy: "none"},
			}
			if err := cfg.Validate(); err != nil {
				t.Errorf("format %q should be valid, got error: %v", tc.format, err)
			}
		})
	}
}

// TestGUC_AuthStrategy_ValidStrategiesTableDriven checks every valid auth.strategy value
// is accepted by Validate (table-driven).
func TestGUC_AuthStrategy_ValidStrategiesTableDriven(t *testing.T) {
	tests := []struct {
		name     string
		strategy string
	}{
		{"none", "none"},
		{"prefix_trie", "prefix_trie"},
		{"jwt_claim", "jwt_claim"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cfg := &VocabularyConfig{
				Domain:     "test",
				WireFormat: WireFormatVocab{Format: "json"},
				Auth:       AuthVocab{Strategy: tc.strategy},
			}
			if err := cfg.Validate(); err != nil {
				t.Errorf("strategy %q should be valid, got error: %v", tc.strategy, err)
			}
		})
	}
}

// TestGUC_Defaults_ResourcePluralDerivedFromNoun verifies the invariant that
// Resource.Plural = Resource.Noun + "s" when Plural is not supplied.
func TestGUC_Defaults_ResourcePluralDerivedFromNoun(t *testing.T) {
	cfg := &VocabularyConfig{
		Domain:   "test",
		Resource: ResourceVocab{Noun: "widget"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	want := "widgets"
	if cfg.Resource.Plural != want {
		t.Errorf("Resource.Plural = %q, want %q", cfg.Resource.Plural, want)
	}
}

// TestGUC_EventTypeFromString_AllTypesMap verifies the algorithmic correctness of
// EventTypeFromString for every mapped event type using a table.
func TestGUC_EventTypeFromString_AllTypesMap(t *testing.T) {
	cfg := &VocabularyConfig{
		Domain: "test",
		EventTypes: EventTypeVocab{
			Join:      "started",
			Progress:  "progressed",
			Complete:  "completed",
			Withdraw:  "stopped",
			Heartbeat: "ping",
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	tests := []struct {
		input string
		want  EventType
	}{
		{"started", EventTypeJoin},
		{"completed", EventTypeComplete},
		{"stopped", EventTypeWithdraw},
		{"progressed", EventTypeProgress},
		{"ping", EventTypeHeartbeat},
	}
	for _, tc := range tests {
		got := cfg.EventTypeFromString(tc.input)
		if got != tc.want {
			t.Errorf("EventTypeFromString(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// TestGUC_Defaults_AllMissingFieldsGetDefaults verifies that every default value is
// populated correctly when none of the optional fields are provided.
func TestGUC_Defaults_AllMissingFieldsGetDefaults(t *testing.T) {
	cfg := &VocabularyConfig{Domain: "mydomain"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	checks := []struct {
		name string
		got  string
		want string
	}{
		{"Actions.Event", cfg.Actions.Event, "announce"},
		{"Resource.Noun", cfg.Resource.Noun, "resource"},
		{"Resource.Plural", cfg.Resource.Plural, "resources"},
		{"Resource.KeyParam", cfg.Resource.KeyParam, "info_hash"},
		{"Participant.Noun", cfg.Participant.Noun, "participant"},
		{"Participant.Plural", cfg.Participant.Plural, "participants"},
		{"Roles.Consumer", cfg.Roles.Consumer, "consumer"},
		{"Roles.Provider", cfg.Roles.Provider, "provider"},
		{"Delta.Produced", cfg.Delta.Produced, "produced"},
		{"Delta.Consumed", cfg.Delta.Consumed, "consumed"},
		{"Delta.Remaining", cfg.Delta.Remaining, "remaining"},
		{"Delta.Corrupt", cfg.Delta.Corrupt, "corrupt"},
		{"EventTypes.Join", cfg.EventTypes.Join, "join"},
		{"EventTypes.Complete", cfg.EventTypes.Complete, "complete"},
		{"EventTypes.Withdraw", cfg.EventTypes.Withdraw, "withdraw"},
		{"Auth.Strategy", cfg.Auth.Strategy, "none"},
		{"WireFormat.Format", cfg.WireFormat.Format, "json"},
		{"Metrics.DomainLabel", cfg.Metrics.DomainLabel, "mydomain"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if cfg.Auth.PasskeyLength != 32 {
		t.Errorf("Auth.PasskeyLength = %d, want 32", cfg.Auth.PasskeyLength)
	}
}

// ── Turing: termination conditions, halting behavior ─────────────────────────

// TestGUC_Validate_EmptyDomainReturnsError verifies the function halts with an error
// when the required domain slug is absent.
func TestGUC_Validate_EmptyDomainReturnsError(t *testing.T) {
	cfg := &VocabularyConfig{}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for empty domain slug, got nil")
	}
}

// TestGUC_WireFormat_InvalidXmlRejected verifies validation halts with an error when
// wire_format.format is "xml", which is not in the allowed set.
func TestGUC_WireFormat_InvalidXmlRejected(t *testing.T) {
	cfg := &VocabularyConfig{
		Domain:     "test",
		WireFormat: WireFormatVocab{Format: "xml"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for wire_format.format \"xml\", got nil")
	}
}

// TestGUC_AuthStrategy_InvalidOauth3Rejected verifies validation halts with an error
// when auth.strategy is "oauth3", which is not in the allowed set.
func TestGUC_AuthStrategy_InvalidOauth3Rejected(t *testing.T) {
	cfg := &VocabularyConfig{
		Domain:     "test",
		WireFormat: WireFormatVocab{Format: "json"},
		Auth:       AuthVocab{Strategy: "oauth3"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for auth.strategy \"oauth3\", got nil")
	}
}

// TestGUC_AuthStrategy_PasskeyRejectedAsStrategy verifies that "passkey" is not a
// valid auth.strategy value. PasskeyLength is a separate integer field; the Strategy
// field only accepts prefix_trie, jwt_claim, or none.
func TestGUC_AuthStrategy_PasskeyRejectedAsStrategy(t *testing.T) {
	cfg := &VocabularyConfig{
		Domain:     "test",
		WireFormat: WireFormatVocab{Format: "json"},
		Auth:       AuthVocab{Strategy: "passkey"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for auth.strategy \"passkey\" (not a valid strategy); " +
			"valid strategies are: prefix_trie, jwt_claim, none")
	}
}

// TestGUC_Validate_ZeroValueConfigTerminates verifies that Validate on a zero-value
// config terminates and returns a non-nil error rather than panicking or looping.
func TestGUC_Validate_ZeroValueConfigTerminates(t *testing.T) {
	cfg := &VocabularyConfig{}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for zero-value config (domain is required), got nil")
	}
}

// ── Church: functional purity, side-effect isolation, referential transparency ─

// TestGUC_Validation_IsPure_SameErrorOnRepeatedCall verifies that calling Validate
// twice on the same invalid config returns the same error, showing no hidden mutation.
func TestGUC_Validation_IsPure_SameErrorOnRepeatedCall(t *testing.T) {
	cfg := &VocabularyConfig{
		Domain:     "test",
		WireFormat: WireFormatVocab{Format: "xml"},
	}
	err1 := cfg.Validate()
	err2 := cfg.Validate()
	if err1 == nil || err2 == nil {
		t.Fatal("expected both calls to return errors")
	}
	if err1.Error() != err2.Error() {
		t.Errorf("validation not pure: call 1 returned %q, call 2 returned %q",
			err1.Error(), err2.Error())
	}
}

// TestGUC_Validate_RevalidationAfterCorrectionSucceeds verifies that fixing the
// invalid field and re-validating succeeds, showing the function is stateless.
func TestGUC_Validate_RevalidationAfterCorrectionSucceeds(t *testing.T) {
	cfg := &VocabularyConfig{}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected initial validation to fail for empty domain")
	}
	cfg.Domain = "corrected"
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected re-validation after correction to succeed, got: %v", err)
	}
}

// TestGUC_Validate_IndependentInstances verifies that validating one config does not
// alter any field of a separately created config.
func TestGUC_Validate_IndependentInstances(t *testing.T) {
	cfg1 := &VocabularyConfig{Domain: "domain-a"}
	cfg2 := &VocabularyConfig{Domain: "domain-b"}
	if err := cfg1.Validate(); err != nil {
		t.Fatalf("cfg1 validation failed: %v", err)
	}
	if err := cfg2.Validate(); err != nil {
		t.Fatalf("cfg2 validation failed: %v", err)
	}
	if cfg1.Domain != "domain-a" {
		t.Errorf("cfg1.Domain mutated to %q after cfg2 was validated", cfg1.Domain)
	}
	if cfg1.Metrics.DomainLabel != "domain-a" {
		t.Errorf("cfg1.Metrics.DomainLabel = %q, want \"domain-a\"", cfg1.Metrics.DomainLabel)
	}
	if cfg2.Metrics.DomainLabel != "domain-b" {
		t.Errorf("cfg2.Metrics.DomainLabel = %q, want \"domain-b\"", cfg2.Metrics.DomainLabel)
	}
}

// TestGUC_WireFormat_EmptyDefaultsToJson verifies the referentially transparent
// default: an absent wire_format.format always resolves to "json".
func TestGUC_WireFormat_EmptyDefaultsToJson(t *testing.T) {
	cfg := &VocabularyConfig{Domain: "test"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if cfg.WireFormat.Format != "json" {
		t.Errorf("WireFormat.Format = %q after defaulting, want \"json\"", cfg.WireFormat.Format)
	}
}

// TestGUC_EventTypeFromString_CaseInsensitive verifies that the mapping function
// applies case folding without mutating the underlying vocab strings.
func TestGUC_EventTypeFromString_CaseInsensitive(t *testing.T) {
	cfg := &VocabularyConfig{Domain: "test"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	// After validation the defaults are "join", "complete", "withdraw".
	tests := []struct {
		input string
		want  EventType
	}{
		{"JOIN", EventTypeJoin},
		{"Join", EventTypeJoin},
		{"join", EventTypeJoin},
		{"COMPLETE", EventTypeComplete},
		{"Complete", EventTypeComplete},
		{"WITHDRAW", EventTypeWithdraw},
		{"Withdraw", EventTypeWithdraw},
	}
	for _, tc := range tests {
		got := cfg.EventTypeFromString(tc.input)
		if got != tc.want {
			t.Errorf("EventTypeFromString(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// ── Gödel: formal consistency, invariant preservation, impossible-state detection ─

// TestGUC_ErrorMessages_IncludeFieldName verifies that error messages name the
// offending field so callers can identify which config key is invalid.
func TestGUC_ErrorMessages_IncludeFieldName(t *testing.T) {
	tests := []struct {
		name       string
		cfg        *VocabularyConfig
		wantSubstr string
	}{
		{
			name:       "invalid wire format",
			cfg:        &VocabularyConfig{Domain: "test", WireFormat: WireFormatVocab{Format: "xml"}},
			wantSubstr: "wire_format",
		},
		{
			name: "invalid auth strategy",
			cfg: &VocabularyConfig{
				Domain:     "test",
				WireFormat: WireFormatVocab{Format: "json"},
				Auth:       AuthVocab{Strategy: "oauth3"},
			},
			wantSubstr: "auth.strategy",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error %q does not mention field %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}

// TestGUC_ErrorMessages_IncludeDomainName verifies that domain-scoped validation errors
// embed the domain slug, preserving traceability in multi-domain deployments.
func TestGUC_ErrorMessages_IncludeDomainName(t *testing.T) {
	const slug = "mycustomdomain"
	cfg := &VocabularyConfig{
		Domain:     slug,
		WireFormat: WireFormatVocab{Format: "xml"},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid wire_format.format, got nil")
	}
	if !strings.Contains(err.Error(), slug) {
		t.Errorf("error %q does not contain domain slug %q", err.Error(), slug)
	}
}

// TestGUC_MultipleInvalidFields_ReturnsFirstError verifies the consistency of
// Validate's early-return contract: when both Domain and WireFormat are invalid,
// the domain error is surfaced first.
func TestGUC_MultipleInvalidFields_ReturnsFirstError(t *testing.T) {
	cfg := &VocabularyConfig{
		// Domain intentionally empty (required), WireFormat also invalid.
		WireFormat: WireFormatVocab{Format: "xml"},
		Auth:       AuthVocab{Strategy: "oauth3"},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for multiple invalid fields, got nil")
	}
	// Domain is checked first; the error must reference it.
	if !strings.Contains(err.Error(), "domain") {
		t.Errorf("expected first error to mention \"domain\", got: %q", err.Error())
	}
}

// TestGUC_Invariant_DomainLabelMatchesDomain verifies the formal invariant that
// Metrics.DomainLabel == Domain when DomainLabel is absent in the input.
func TestGUC_Invariant_DomainLabelMatchesDomain(t *testing.T) {
	const slug = "invariant-domain"
	cfg := &VocabularyConfig{Domain: slug}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if cfg.Metrics.DomainLabel != slug {
		t.Errorf("Metrics.DomainLabel = %q, want %q (same as Domain)",
			cfg.Metrics.DomainLabel, slug)
	}
}

// TestGUC_Invariant_PasskeyLength_DefaultIs32 verifies the numeric invariant that
// Auth.PasskeyLength is always 32 when not explicitly set, matching the expected
// passkey length used throughout the tracker.
func TestGUC_Invariant_PasskeyLength_DefaultIs32(t *testing.T) {
	cfg := &VocabularyConfig{Domain: "test"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	const wantLength = 32
	if cfg.Auth.PasskeyLength != wantLength {
		t.Errorf("Auth.PasskeyLength = %d, want %d", cfg.Auth.PasskeyLength, wantLength)
	}
	// Cross-check: the fixture passkey is exactly 32 bytes, consistent with this invariant.
	if len(testPasskey) != wantLength {
		t.Errorf("fixture testPasskey length = %d, want %d (must match PasskeyLength invariant)",
			len(testPasskey), wantLength)
	}
}
