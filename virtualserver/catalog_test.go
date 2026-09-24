package virtualserver

import "testing"

// ── ClassifyArtifact ──────────────────────────────────────────────────────────

type stubCatalog struct{ seeders int }

func (s *stubCatalog) SeederCount(_ string) int { return s.seeders }

func TestClassifyArtifact(t *testing.T) {
	cases := []struct {
		seeders int
		want    ArtifactStatus
	}{
		{0, ArtifactUnavailable},
		{1, ArtifactDegraded},
		{2, ArtifactDegraded},
		{3, ArtifactAvailable},
		{10, ArtifactAvailable},
	}
	for _, tc := range cases {
		got := ClassifyArtifact(&stubCatalog{tc.seeders}, "infohash")
		if got != tc.want {
			t.Errorf("seeders=%d: got %s want %s", tc.seeders, got, tc.want)
		}
	}
}

// ── OcelotCatalogAdapter ──────────────────────────────────────────────────────

func TestOcelotCatalogAdapter(t *testing.T) {
	store := map[string]int{
		"abc": 5,
		"xyz": 1,
	}
	adapter := NewOcelotCatalogAdapter(func(h string) int {
		return store[h]
	})

	if adapter.SeederCount("abc") != 5 {
		t.Fatalf("expected 5 seeders for abc")
	}
	if adapter.SeederCount("xyz") != 1 {
		t.Fatalf("expected 1 seeder for xyz")
	}
	if adapter.SeederCount("unknown") != 0 {
		t.Fatalf("expected 0 seeders for unknown")
	}
}

func TestOcelotCatalogAdapter_NilFn(t *testing.T) {
	a := NewOcelotCatalogAdapter(nil)
	if a.SeederCount("anything") != 0 {
		t.Fatal("nil function should return 0")
	}
}

func TestClassifyArtifact_Boundaries(t *testing.T) {
	// 0 → unavailable
	if got := ClassifyArtifact(&stubCatalog{0}, "h"); got != ArtifactUnavailable {
		t.Fatalf("0 seeders: got %s", got)
	}
	// exactly 3 → available (not degraded)
	if got := ClassifyArtifact(&stubCatalog{3}, "h"); got != ArtifactAvailable {
		t.Fatalf("3 seeders: got %s", got)
	}
}
