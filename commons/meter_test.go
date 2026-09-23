package commons

import (
	"testing"
	"time"
)

func TestNewResourceMeter(t *testing.T) {
	rm := NewResourceMeter(1, 42)
	if rm.UserID != 1 {
		t.Errorf("UserID = %d, want 1", rm.UserID)
	}
	if rm.TorrentID != 42 {
		t.Errorf("TorrentID = %d, want 42", rm.TorrentID)
	}
	if rm.StartTime.IsZero() {
		t.Error("StartTime should be set")
	}
	if rm.Settled {
		t.Error("should not be settled on creation")
	}
	if len(rm.Measured) != 0 || len(rm.Requested) != 0 || len(rm.Reserved) != 0 {
		t.Error("maps should be empty on creation")
	}
}

func TestRecordMeasured(t *testing.T) {
	rm := NewResourceMeter(1, 1)
	rm.RecordMeasured(ResourceDownload, 1000)
	rm.RecordMeasured(ResourceDownload, 500)
	if rm.Measured[ResourceDownload] != 1500 {
		t.Errorf("Measured = %d, want 1500", rm.Measured[ResourceDownload])
	}
	// zero/negative are ignored
	rm.RecordMeasured(ResourceDownload, 0)
	rm.RecordMeasured(ResourceDownload, -100)
	if rm.Measured[ResourceDownload] != 1500 {
		t.Errorf("Measured after zero/neg = %d, want 1500", rm.Measured[ResourceDownload])
	}
}

func TestRecordRequested(t *testing.T) {
	rm := NewResourceMeter(2, 2)
	rm.RecordRequested(ResourceUpload, 2048)
	if rm.Requested[ResourceUpload] != 2048 {
		t.Errorf("Requested = %d, want 2048", rm.Requested[ResourceUpload])
	}
	// negative is ignored
	rm.RecordRequested(ResourceUpload, -1)
	if rm.Requested[ResourceUpload] != 2048 {
		t.Error("negative request should be ignored")
	}
}

func TestRecordReserved(t *testing.T) {
	rm := NewResourceMeter(3, 3)
	rm.RecordReserved(ResourceDownload, 500)
	if rm.Reserved[ResourceDownload] != 500 {
		t.Errorf("Reserved = %d, want 500", rm.Reserved[ResourceDownload])
	}
	// negative clamped to 0
	rm.RecordReserved(ResourceDownload, -10)
	if rm.Reserved[ResourceDownload] != 0 {
		t.Errorf("Reserved after negative = %d, want 0", rm.Reserved[ResourceDownload])
	}
}

func TestMeterSettle(t *testing.T) {
	rm := NewResourceMeter(4, 4)
	rm.Settle()
	if !rm.Settled {
		t.Error("should be marked settled")
	}
	if rm.EndTime.IsZero() {
		t.Error("EndTime should be set after Settle")
	}
	// Second call should not overwrite EndTime
	first := rm.EndTime
	time.Sleep(2 * time.Millisecond)
	rm.Settle()
	if rm.EndTime != first {
		t.Error("second Settle should not change EndTime")
	}
}

func TestDurationSec_Settled(t *testing.T) {
	rm := NewResourceMeter(5, 5)
	time.Sleep(10 * time.Millisecond)
	rm.Settle()
	d := rm.DurationSec()
	// Very short duration: 0 or 1 second is correct for 10ms
	if d < 0 {
		t.Errorf("DurationSec = %d, want >= 0", d)
	}
}

func TestDurationSec_Unsettled(t *testing.T) {
	rm := NewResourceMeter(6, 6)
	d := rm.DurationSec()
	// EndTime is zero so it uses time.Now(); should be 0 seconds for a just-created meter
	if d < 0 {
		t.Errorf("DurationSec unsettled = %d, want >= 0", d)
	}
}

func TestMeasurementAccuracy_PerfectMatch(t *testing.T) {
	rm := NewResourceMeter(7, 7)
	rm.RecordRequested(ResourceDownload, 1000)
	rm.RecordMeasured(ResourceDownload, 1000)
	acc := rm.MeasurementAccuracy(ResourceDownload)
	if acc != 1000 {
		t.Errorf("accuracy = %d, want 1000 (100%%)", acc)
	}
}

func TestMeasurementAccuracy_NoRequest(t *testing.T) {
	rm := NewResourceMeter(8, 8)
	acc := rm.MeasurementAccuracy(ResourceDownload)
	if acc != 1000 {
		t.Errorf("accuracy with no request = %d, want 1000", acc)
	}
}

func TestMeasurementAccuracy_ZeroMeasured(t *testing.T) {
	rm := NewResourceMeter(9, 9)
	rm.RecordRequested(ResourceDownload, 1000)
	acc := rm.MeasurementAccuracy(ResourceDownload)
	if acc != 0 {
		t.Errorf("accuracy with zero measured = %d, want 0", acc)
	}
}

func TestMeasurementAccuracy_Capped(t *testing.T) {
	rm := NewResourceMeter(10, 10)
	rm.RecordRequested(ResourceDownload, 100)
	rm.RecordMeasured(ResourceDownload, 1_000_000) // hugely over-reported
	acc := rm.MeasurementAccuracy(ResourceDownload)
	if acc != 2000 {
		t.Errorf("accuracy cap = %d, want 2000 (200%%)", acc)
	}
}
