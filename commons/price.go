package commons

// ResourcePrice holds the configured pricing parameters for one resource type.
// All values are ComputeCredit (int64 fixed-point, CreditScale = 1_000_000).
type ResourcePrice struct {
	Base ComputeCredit // base price per unit at zero scarcity
	Min  ComputeCredit // floor — effective price never drops below this
	Max  ComputeCredit // ceiling — effective price never exceeds this
}

// PriceTable is the complete set of resource prices, one entry per ResourceType.
type PriceTable struct {
	Prices map[ResourceType]*ResourcePrice
	// ScarcityFactor controls how steeply prices rise with utilisation.
	// Units: millis (1000 = 1.0x at 100% utilisation before ceiling).
	// Default 2000 → at 50% utilisation, price is ~2x base.
	ScarcityFactor int64
	// MaxScarcityMultiplier is the ceiling on the scarcity multiplier (millis).
	// Default 3000 → prices never exceed 3x base due to scarcity alone.
	MaxScarcityMultiplier int64
}

// DefaultPriceTable builds the factory-default price table.
func DefaultPriceTable() *PriceTable {
	bases := DefaultBasePrices()
	mins := DefaultMinPrices()
	maxs := DefaultMaxPrices()
	prices := make(map[ResourceType]*ResourcePrice, len(AllResourceTypes))
	for _, rt := range AllResourceTypes {
		prices[rt] = &ResourcePrice{
			Base: bases[rt],
			Min:  mins[rt],
			Max:  maxs[rt],
		}
	}
	return &PriceTable{
		Prices:                prices,
		ScarcityFactor:        2000,
		MaxScarcityMultiplier: 3000,
	}
}

// Effective returns the scarcity-adjusted price for rt given current swarm
// utilisation. seeders and leechers must both be >= 0.
//
// Scarcity model (integer-only):
//
//	utilisation_millis = leechers * 1000 / (seeders + leechers + 1)
//	raw_mult_millis    = 1000 + ScarcityFactor * util / (1000 - util)
//	mult_millis        = clamp(raw_mult_millis, 1000, MaxScarcityMultiplier)
//	effective_price    = clamp(base * mult_millis / 1000, min, max)
//
// The formula is deterministic, inspectable and testable with no float64.
func (pt *PriceTable) Effective(rt ResourceType, seeders, leechers int) ComputeCredit {
	rp, ok := pt.Prices[rt]
	if !ok {
		return 0
	}
	mult := pt.scarcityMultiplierMillis(seeders, leechers)
	effective := rp.Base.MulMillis(mult)
	return effective.Clamp(rp.Min, rp.Max)
}

// scarcityMultiplierMillis returns the scarcity multiplier in millis (1000 = 1.0x).
func (pt *PriceTable) scarcityMultiplierMillis(seeders, leechers int) int64 {
	total := int64(seeders) + int64(leechers) + 1 // +1 prevents division by zero
	util := int64(leechers) * 1000 / total        // 0–999 millis of utilisation

	// Avoid division by zero when util approaches 1000.
	denominator := int64(1000) - util
	if denominator <= 0 {
		denominator = 1
	}

	rawMult := int64(1000) + pt.ScarcityFactor*util/denominator

	max := pt.MaxScarcityMultiplier
	if max <= 1000 {
		max = 3000
	}
	if rawMult > max {
		rawMult = max
	}
	if rawMult < 1000 {
		rawMult = 1000
	}
	return rawMult
}

// EffectiveForUpload returns the price a seeder earns per GB uploaded
// (negative charge = credit earned).
func (pt *PriceTable) EffectiveForUpload(seeders, leechers int) ComputeCredit {
	return pt.Effective(ResourceUpload, seeders, leechers)
}

// EffectiveForDownload returns the price a leecher pays per GB downloaded.
func (pt *PriceTable) EffectiveForDownload(seeders, leechers int) ComputeCredit {
	return pt.Effective(ResourceDownload, seeders, leechers)
}

// EffectiveForSeeding returns the CC reward rate per GB-hour of seeding.
func (pt *PriceTable) EffectiveForSeeding(seeders, leechers int) ComputeCredit {
	return pt.Effective(ResourceSeeding, seeders, leechers)
}

// ChargeForDownload calculates the total CC charge for downloadBytes bytes
// consumed at the current scarcity level.
func (pt *PriceTable) ChargeForDownload(downloadBytes int64, seeders, leechers int) ComputeCredit {
	if downloadBytes <= 0 {
		return 0
	}
	pricePerGB := pt.EffectiveForDownload(seeders, leechers)
	// charge = price_per_GB * download_bytes / GB
	return pricePerGB.MulFrac(downloadBytes, ResourceGB)
}

// CreditForUpload calculates the total CC credit for uploadBytes bytes
// provided at the current scarcity level. Returns a positive ComputeCredit
// (it will be applied as a negative ledger charge, i.e., credit earned).
func (pt *PriceTable) CreditForUpload(uploadBytes int64, seeders, leechers int) ComputeCredit {
	if uploadBytes <= 0 {
		return 0
	}
	pricePerGB := pt.EffectiveForUpload(seeders, leechers)
	return pricePerGB.MulFrac(uploadBytes, ResourceGB)
}

// CreditForSeeding calculates the total CC credit for seeding seedBytes bytes
// for durationSec seconds at the current scarcity level.
func (pt *PriceTable) CreditForSeeding(seedBytes int64, durationSec int64, seeders, leechers int) ComputeCredit {
	if seedBytes <= 0 || durationSec <= 0 {
		return 0
	}
	pricePerGBHour := pt.EffectiveForSeeding(seeders, leechers)
	gbHours := seedBytes / ResourceGB * durationSec / ResourceHour
	if gbHours <= 0 {
		// Sub-GB or sub-hour — still charge proportionally.
		return pricePerGBHour.MulFrac(seedBytes, ResourceGB).MulFrac(durationSec, ResourceHour)
	}
	return pricePerGBHour.MulFrac(gbHours, 1)
}
