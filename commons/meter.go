package commons

import "time"

// ResourceMeter tracks the three measurement phases for each resource:
// Requested → Reserved → Measured (actual consumption).
//
// In tracker context:
//   - Requested: bytes the peer declared in the announce request
//   - Reserved:  bytes held against the budget prior to settlement
//   - Measured:  bytes actually transferred (derived from announce deltas)
type ResourceMeter struct {
	UserID    uint32
	TorrentID uint32

	// Per-resource counters in bytes (for bandwidth) or byte-seconds (for storage).
	Requested map[ResourceType]int64
	Reserved  map[ResourceType]int64
	Measured  map[ResourceType]int64

	StartTime time.Time
	EndTime   time.Time // zero until settled

	Settled bool
}

// NewResourceMeter creates a ResourceMeter for the given user and torrent.
func NewResourceMeter(userID, torrentID uint32) *ResourceMeter {
	return &ResourceMeter{
		UserID:    userID,
		TorrentID: torrentID,
		Requested: make(map[ResourceType]int64),
		Reserved:  make(map[ResourceType]int64),
		Measured:  make(map[ResourceType]int64),
		StartTime: time.Now(),
	}
}

// RecordMeasured adds delta bytes of rt to the measured counter.
func (rm *ResourceMeter) RecordMeasured(rt ResourceType, delta int64) {
	if delta <= 0 {
		return
	}
	rm.Measured[rt] += delta
}

// RecordRequested records a declared resource request.
func (rm *ResourceMeter) RecordRequested(rt ResourceType, bytes int64) {
	if bytes <= 0 {
		return
	}
	rm.Requested[rt] = bytes
}

// RecordReserved sets the reserved quantity for rt.
func (rm *ResourceMeter) RecordReserved(rt ResourceType, bytes int64) {
	if bytes < 0 {
		bytes = 0
	}
	rm.Reserved[rt] = bytes
}

// Settle marks the meter as settled and records the end time.
func (rm *ResourceMeter) Settle() {
	if !rm.Settled {
		rm.Settled = true
		rm.EndTime = time.Now()
	}
}

// DurationSec returns the metering duration in whole seconds.
func (rm *ResourceMeter) DurationSec() int64 {
	end := rm.EndTime
	if end.IsZero() {
		end = time.Now()
	}
	d := end.Sub(rm.StartTime).Seconds()
	if d < 0 {
		return 0
	}
	return int64(d)
}

// MeasurementAccuracy returns the ratio of measured/requested for rt as millis
// (1000 = 100% accurate). Returns 1000 if no request was made.
// This is used to compute accuracy bonuses/penalties.
func (rm *ResourceMeter) MeasurementAccuracy(rt ResourceType) int64 {
	req := rm.Requested[rt]
	if req == 0 {
		return 1000
	}
	measured := rm.Measured[rt]
	if measured <= 0 {
		return 0
	}
	acc := measured * 1000 / req
	if acc > 2000 {
		acc = 2000 // cap at 200% to avoid extreme outliers
	}
	return acc
}

// AnnounceStats holds the bandwidth deltas from a single announce interval.
// Separated from ResourceMeter so it can be passed without carrying the whole meter.
type AnnounceStats struct {
	UserID    uint32
	TorrentID uint32

	// RawUploaded is the total bytes this peer reported uploading since session start.
	// Used to compute the delta vs. the previous announce.
	RawUploaded int64

	// RawDownloaded is the total bytes this peer reported downloading.
	RawDownloaded int64

	// EffectiveUploaded is the upload delta that counts for ratio/CC accounting
	// after freeleech/token adjustments. Seeders earn CC on this amount.
	EffectiveUploaded int64

	// EffectiveDownloaded is the download delta that counts for CC accounting
	// after freeleech/token adjustments. Leechers pay CC on this amount.
	EffectiveDownloaded int64

	// Seeders / Leechers gives the current swarm composition for scarcity pricing.
	Seeders  int
	Leechers int

	// IsSeeder indicates the peer is currently seeding (Left == 0).
	IsSeeder bool

	// IsStopped indicates the peer sent event=stopped.
	IsStopped bool

	Timestamp time.Time
}
