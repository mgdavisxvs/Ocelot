package domain

import (
	"strings"
	"testing"
)

func TestParse_Valid(t *testing.T) {
	cases := []struct {
		raw       string
		wantNS    string
		wantSvc   string
		wantInst  string
	}{
		{"vs://inference/llama/primary", "inference", "llama", "primary"},
		{"vs://tracker/ocelot/primary", "tracker", "ocelot", "primary"},
		{"vs://dex/clay/runtime", "dex", "clay", "runtime"},
		{"vs://batch/model-training/job-184", "batch", "model-training", "job-184"},
		{"vs://edge/ironbox-07/dex", "edge", "ironbox-07", "dex"},
		{"vs://a/b/c", "a", "b", "c"},
		{"vs://a1/b2/c3", "a1", "b2", "c3"},
		{"vs://ns/svc/inst-01", "ns", "svc", "inst-01"},
		// upper-case is canonicalized
		{"vs://INFERENCE/LLAMA/PRIMARY", "inference", "llama", "primary"},
	}
	for _, tc := range cases {
		p, err := Parse(tc.raw)
		if err != nil {
			t.Errorf("Parse(%q) unexpected error: %v", tc.raw, err)
			continue
		}
		if p.Namespace != tc.wantNS || p.Service != tc.wantSvc || p.Instance != tc.wantInst {
			t.Errorf("Parse(%q) = {%s %s %s}, want {%s %s %s}",
				tc.raw, p.Namespace, p.Service, p.Instance,
				tc.wantNS, tc.wantSvc, tc.wantInst)
		}
	}
}

func TestParse_Invalid(t *testing.T) {
	cases := []struct {
		raw    string
		reason string
	}{
		{"", "empty"},
		{"http://foo/bar/baz", "wrong scheme"},
		{"vs://", "no path"},
		{"vs://ns/svc", "only 2 segments"},
		{"vs://ns/svc/inst/extra", "4 segments"},
		{"vs://././ inst", "dots"},
		{"vs://./svc/inst", "dot namespace"},
		{"vs://../svc/inst", "dotdot namespace"},
		{"vs://ns/../inst", "dotdot service"},
		{"vs://ns/svc/.", "dot instance"},
		{"vs://ns/svc/..", "dotdot instance"},
		{"vs://ns svc/inst/x", "space in namespace"},
		{"vs://ns/svc/inst?foo=bar", "query string"},
		{"vs://ns/svc/inst#frag", "fragment"},
		{"vs://ns/svc/%61inst", "percent encoding"},
		{"vs://-ns/svc/inst", "leading dash namespace"},
		{"vs://ns/-svc/inst", "leading dash service"},
		{"vs://ns/svc/-inst", "leading dash instance"},
		{"vs://ns/svc/" + strings.Repeat("a", 64), "instance too long"},
		{"vs://" + strings.Repeat("a", 64) + "/svc/inst", "namespace too long"},
		{"vs://ns/svc/INST@2", "invalid char @"},
		{"vs://ns/svc/inst/", "trailing slash"},
		{"vs://ns/SVC!/inst", "bang in service"},
	}
	for _, tc := range cases {
		_, err := Parse(tc.raw)
		if err == nil {
			t.Errorf("Parse(%q) expected error (%s) but got none", tc.raw, tc.reason)
		}
	}
}

func TestVSPath_String_Roundtrip(t *testing.T) {
	raw := "vs://inference/llama/primary"
	p, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.String(); got != raw {
		t.Errorf("String() = %q, want %q", got, raw)
	}
}

func TestVSPath_TooLong(t *testing.T) {
	// build a path just over 255 chars
	seg := strings.Repeat("a", 63)
	raw := "vs://" + seg + "/" + seg + "/" + seg + "x"
	_, err := Parse(raw)
	if err == nil {
		t.Error("expected error for path > 255 chars")
	}
}

func TestVSPath_Validate_Direct(t *testing.T) {
	p := VSPath{Namespace: "good", Service: "svc", Instance: "inst"}
	if err := p.Validate(); err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
	bad := VSPath{Namespace: "", Service: "svc", Instance: "inst"}
	if err := bad.Validate(); err == nil {
		t.Error("expected error for empty namespace")
	}
}
