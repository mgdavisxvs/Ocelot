package commons

// ResourceType identifies a metered resource in the tracker economy.
type ResourceType string

const (
	// ResourceUpload charges are incurred by peers receiving uploads from seeders.
	// Seeders earn CC via ResourceUpload; leechers pay via ResourceDownload.
	ResourceUpload ResourceType = "upload"

	// ResourceDownload is the CC charge to a downloading peer (leecher).
	ResourceDownload ResourceType = "download"

	// ResourceSeeding rewards peers that remain online as seeders (GB-hours).
	ResourceSeeding ResourceType = "seeding"

	// ResourceStorage accounts for persistent storage costs (GB-hours).
	ResourceStorage ResourceType = "storage"

	// ResourceNetwork charges for raw network transfer (cross-node, inter-region).
	ResourceNetwork ResourceType = "network"
)

// AllResourceTypes lists every ResourceType the system knows about.
var AllResourceTypes = []ResourceType{
	ResourceUpload,
	ResourceDownload,
	ResourceSeeding,
	ResourceStorage,
	ResourceNetwork,
}

// ResourceGB is one gibibyte in bytes. All bandwidth quantities use bytes.
const ResourceGB int64 = 1 << 30

// ResourceHour is one hour in seconds. All time quantities use seconds.
const ResourceHour int64 = 3600

// DefaultBasePrices returns the baseline CC prices per unit (per GB or per GB-hour).
// Values are in base CC units (divide by CreditScale for human-readable CC).
//
// Economics rationale (calibrated to a healthy upload/download ratio):
//   - Seeders earn upload CC at 10 CC/GB they provide.
//   - Leechers pay download CC at 50 CC/GB they consume.
//   - The 5:1 ratio incentivises seeding without making downloads unaffordable.
//   - Seeding reward (5 CC/GB-hour) further rewards sustained availability.
func DefaultBasePrices() map[ResourceType]ComputeCredit {
	return map[ResourceType]ComputeCredit{
		ResourceUpload:   FromCC(10), // seeder earns 10 CC per GB uploaded
		ResourceDownload: FromCC(50), // leecher pays 50 CC per GB downloaded
		ResourceSeeding:  FromCC(5),  // seeder earns 5 CC per GB-hour online
		ResourceStorage:  FromCC(2),  // storage cost 2 CC per GB-hour
		ResourceNetwork:  FromCC(20), // transfer cost 20 CC per GB
	}
}

// DefaultMinPrices returns price floors — prices never drop below these values.
func DefaultMinPrices() map[ResourceType]ComputeCredit {
	return map[ResourceType]ComputeCredit{
		ResourceUpload:   FromCC(5),
		ResourceDownload: FromCC(10),
		ResourceSeeding:  FromCC(1),
		ResourceStorage:  FromCC(1),
		ResourceNetwork:  FromCC(5),
	}
}

// DefaultMaxPrices returns price ceilings — prices never exceed these values.
// Caps prevent extreme scarcity from making downloads unaffordably expensive.
func DefaultMaxPrices() map[ResourceType]ComputeCredit {
	return map[ResourceType]ComputeCredit{
		ResourceUpload:   FromCC(100),
		ResourceDownload: FromCC(500),
		ResourceSeeding:  FromCC(50),
		ResourceStorage:  FromCC(20),
		ResourceNetwork:  FromCC(200),
	}
}

// DefaultStartingBalance is the initial CC balance granted to new accounts.
const DefaultStartingBalance = ComputeCredit(1000 * CreditScale) // 1000 CC

// DefaultOpportunisticBalance is the balance for P3 accounts.
const DefaultOpportunisticBalance = ComputeCredit(100 * CreditScale) // 100 CC
