package commons

// PricingFn is a first-class function type for computing resource charges and
// credits. It replaces ad-hoc pricing branches with composable strategies.
//
// F-C3: elevating PricingFn to a first-class type lets callers inject freeleech
// windows, token multipliers, or promotional rates as a single substitution at
// the announce boundary, keeping SettleAnnounce free of conditional branches.
//
// Arguments:
//   - rt: the resource being priced
//   - bytes: volume transferred (positive)
//   - seeders, leechers: current swarm composition for scarcity adjustment
//
// Returns the charge magnitude (positive = cost, caller applies sign convention).
// Returning 0 means the resource is free for this invocation.
type PricingFn func(rt ResourceType, bytes int64, seeders, leechers int) ComputeCredit

// StandardPricingFn returns a PricingFn backed by the given PriceTable using
// the normal scarcity-adjusted model. pt must not be nil.
func StandardPricingFn(pt *PriceTable) PricingFn {
	return func(rt ResourceType, bytes int64, seeders, leechers int) ComputeCredit {
		if bytes <= 0 {
			return 0
		}
		pricePerUnit := pt.Effective(rt, seeders, leechers)
		return pricePerUnit.MulFrac(bytes, ResourceGB)
	}
}

// FreeleechPricingFn returns a PricingFn where downloads are free and uploads
// earn normal credit. Seeders still receive CC for traffic contributed.
// The priceTable is used for upload-side credit; download charges are zeroed.
func FreeleechPricingFn(pt *PriceTable) PricingFn {
	return func(rt ResourceType, bytes int64, seeders, leechers int) ComputeCredit {
		if rt == ResourceDownload {
			return 0 // freeleech: leechers pay nothing
		}
		if bytes <= 0 {
			return 0
		}
		pricePerUnit := pt.Effective(rt, seeders, leechers)
		return pricePerUnit.MulFrac(bytes, ResourceGB)
	}
}

// TokenMultiplierPricingFn returns a PricingFn that applies a fixed multiplier
// (in millis, 1000 = 1.0x) to the standard price. Values > 1000 increase charges;
// values < 1000 discount them. multiplierMillis must be > 0.
func TokenMultiplierPricingFn(pt *PriceTable, multiplierMillis int64) PricingFn {
	if multiplierMillis <= 0 {
		multiplierMillis = 1000
	}
	return func(rt ResourceType, bytes int64, seeders, leechers int) ComputeCredit {
		if bytes <= 0 {
			return 0
		}
		pricePerUnit := pt.Effective(rt, seeders, leechers)
		base := pricePerUnit.MulFrac(bytes, ResourceGB)
		return base.MulMillis(multiplierMillis)
	}
}

// ForAnnounceStats selects the appropriate PricingFn for a given AnnounceStats
// snapshot. When FreeType is true, the freeleech strategy is returned so that
// SettleAnnounce need not inspect the FreeType flag itself.
func ForAnnounceStats(stats AnnounceStats, pt *PriceTable) PricingFn {
	if stats.FreeType {
		return FreeleechPricingFn(pt)
	}
	return StandardPricingFn(pt)
}
