package commons

import (
	"testing"
)

func TestDefaultPriceTableNotNil(t *testing.T) {
	pt := DefaultPriceTable()
	if pt == nil {
		t.Fatal("nil price table")
	}
	for _, rt := range AllResourceTypes {
		if _, ok := pt.Prices[rt]; !ok {
			t.Errorf("missing price for %s", rt)
		}
	}
}

func TestScarcityZeroSeeders(t *testing.T) {
	pt := DefaultPriceTable()
	// 0 seeders, 10 leechers → high scarcity → price near max
	price := pt.Effective(ResourceDownload, 0, 10)
	if price <= pt.Prices[ResourceDownload].Base {
		t.Fatalf("expected scarcity premium, got %s (base %s)", price, pt.Prices[ResourceDownload].Base)
	}
}

func TestScarcityManySeedersFewLeechers(t *testing.T) {
	pt := DefaultPriceTable()
	// 100 seeders, 1 leecher → almost no scarcity → price near base
	price := pt.Effective(ResourceDownload, 100, 1)
	base := pt.Prices[ResourceDownload].Base
	if price > base.MulMillis(1100) {
		t.Fatalf("expected near-base price, got %s (base %s)", price, base)
	}
}

func TestPriceBoundsAlwaysRespected(t *testing.T) {
	pt := DefaultPriceTable()
	extremeCases := [][2]int{
		{0, 0}, {0, 1000}, {1000, 0}, {1, 1}, {500, 500},
	}
	for _, rt := range AllResourceTypes {
		rp := pt.Prices[rt]
		for _, pair := range extremeCases {
			p := pt.Effective(rt, pair[0], pair[1])
			if p < rp.Min {
				t.Errorf("%s with seeders=%d leechers=%d: price %s < min %s",
					rt, pair[0], pair[1], p, rp.Min)
			}
			if p > rp.Max {
				t.Errorf("%s with seeders=%d leechers=%d: price %s > max %s",
					rt, pair[0], pair[1], p, rp.Max)
			}
		}
	}
}

func TestPriceDeterministic(t *testing.T) {
	pt := DefaultPriceTable()
	// Same inputs must always produce the same output.
	p1 := pt.Effective(ResourceDownload, 10, 5)
	p2 := pt.Effective(ResourceDownload, 10, 5)
	if p1 != p2 {
		t.Fatal("price not deterministic")
	}
}

func TestChargeForDownload(t *testing.T) {
	pt := DefaultPriceTable()
	// 1 GB at base price (no scarcity: 1000 seeders, 1 leecher)
	charge := pt.ChargeForDownload(int64(ResourceGB), 1000, 1)
	base := pt.Prices[ResourceDownload].Base
	// With essentially zero scarcity, charge should be approximately base.
	if charge < base.MulMillis(950) || charge > base.MulMillis(1100) {
		t.Errorf("expected ~base charge %s, got %s", base, charge)
	}
}

func TestChargeForDownloadZeroBytes(t *testing.T) {
	pt := DefaultPriceTable()
	if pt.ChargeForDownload(0, 10, 5) != 0 {
		t.Fatal("zero bytes should produce zero charge")
	}
	if pt.ChargeForDownload(-1, 10, 5) != 0 {
		t.Fatal("negative bytes should produce zero charge")
	}
}

func TestCreditForUpload(t *testing.T) {
	pt := DefaultPriceTable()
	credit := pt.CreditForUpload(int64(ResourceGB), 1, 10)
	if !credit.IsPositive() {
		t.Fatal("upload credit should be positive")
	}
}

func TestScarcityNonNegativeMultiplier(t *testing.T) {
	pt := DefaultPriceTable()
	for seeders := 0; seeders <= 10; seeders++ {
		for leechers := 0; leechers <= 20; leechers++ {
			m := pt.scarcityMultiplierMillis(seeders, leechers)
			if m < 1000 {
				t.Errorf("multiplier should be >= 1000, got %d (s=%d l=%d)", m, seeders, leechers)
			}
			if m > pt.MaxScarcityMultiplier {
				t.Errorf("multiplier %d exceeds max %d (s=%d l=%d)",
					m, pt.MaxScarcityMultiplier, seeders, leechers)
			}
		}
	}
}

func TestScarcityMonotonicallyIncreasing(t *testing.T) {
	// For fixed seeders, more leechers should never decrease the multiplier.
	pt := DefaultPriceTable()
	const seeders = 5
	prev := int64(0)
	for leechers := 0; leechers <= 30; leechers++ {
		m := pt.scarcityMultiplierMillis(seeders, leechers)
		if m < prev {
			t.Errorf("multiplier decreased from %d to %d at leechers=%d", prev, m, leechers)
		}
		prev = m
	}
}
