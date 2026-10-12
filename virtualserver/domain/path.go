package domain

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

const (
	vsScheme     = "vs"
	maxPathLen   = 255
	maxSegLen    = 63
	minSegLen    = 1
	pathSegments = 3
)

// VSPath is a parsed, validated, canonical vs:// path.
// Format: vs://<namespace>/<service>/<instance>
type VSPath struct {
	Namespace string
	Service   string
	Instance  string
}

// Parse parses and validates a raw vs:// string, returning a canonical VSPath.
func Parse(raw string) (VSPath, error) {
	if len(raw) > maxPathLen {
		return VSPath{}, fmt.Errorf("vs path too long: %d chars (max %d)", len(raw), maxPathLen)
	}
	if !strings.HasPrefix(raw, "vs://") {
		return VSPath{}, fmt.Errorf("vs path must start with vs://")
	}
	rest := raw[len("vs://"):]
	if rest == "" {
		return VSPath{}, fmt.Errorf("vs path has no path component after vs://")
	}
	// Reject query strings or fragments
	if strings.ContainsAny(rest, "?#") {
		return VSPath{}, fmt.Errorf("vs path must not contain query string or fragment")
	}
	// Reject percent-encoding
	if strings.Contains(rest, "%") {
		if _, err := url.PathUnescape(rest); err != nil {
			return VSPath{}, fmt.Errorf("vs path contains invalid percent-encoding")
		}
		return VSPath{}, fmt.Errorf("vs path must not contain percent-encoded characters")
	}
	parts := strings.Split(rest, "/")
	if len(parts) != pathSegments {
		return VSPath{}, fmt.Errorf("vs path must have exactly 3 segments (namespace/service/instance), got %d", len(parts))
	}
	p := VSPath{
		Namespace: strings.ToLower(parts[0]),
		Service:   strings.ToLower(parts[1]),
		Instance:  strings.ToLower(parts[2]),
	}
	for i, seg := range []string{p.Namespace, p.Service, p.Instance} {
		name := [3]string{"namespace", "service", "instance"}[i]
		if err := validateSegment(seg, name); err != nil {
			return VSPath{}, err
		}
	}
	return p, nil
}

// String returns the canonical vs:// representation.
func (p VSPath) String() string {
	return "vs://" + p.Namespace + "/" + p.Service + "/" + p.Instance
}

// Validate checks a VSPath already constructed (e.g., via direct struct literal).
func (p VSPath) Validate() error {
	for i, seg := range []string{p.Namespace, p.Service, p.Instance} {
		name := [3]string{"namespace", "service", "instance"}[i]
		if err := validateSegment(seg, name); err != nil {
			return err
		}
	}
	return nil
}

// ValidateSegment is the exported form of validateSegment for use outside the domain package.
func ValidateSegment(seg string) error {
	return validateSegment(seg, "segment")
}

// validateSegment enforces the safe character set and length rules for a single path segment.
// Allowed: [a-z0-9][a-z0-9_-]{0,62}
func validateSegment(seg, name string) error {
	if len(seg) < minSegLen {
		return fmt.Errorf("vs path %s must not be empty", name)
	}
	if seg == "." || seg == ".." {
		return fmt.Errorf("vs path %s must not be '.' or '..'", name)
	}
	if len(seg) > maxSegLen {
		return fmt.Errorf("vs path %s too long: %d chars (max %d)", name, len(seg), maxSegLen)
	}
	for i, c := range seg {
		if unicode.IsControl(c) {
			return fmt.Errorf("vs path %s contains control character at position %d", name, i)
		}
		if c >= 'a' && c <= 'z' {
			continue
		}
		if c >= '0' && c <= '9' {
			continue
		}
		if c == '_' || c == '-' {
			continue
		}
		return fmt.Errorf("vs path %s contains invalid character %q at position %d (allowed: [a-z0-9_-])", name, c, i)
	}
	if len(seg) > 0 && seg[0] == '-' {
		return fmt.Errorf("vs path %s must not start with '-'", name)
	}
	return nil
}
