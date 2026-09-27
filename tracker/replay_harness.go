package tracker

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"
)

// F-T1: Announce oracle / binary replay harness.
//
// The replay harness captures announce (request, response) pairs to a compact
// binary log, then replays them deterministically against a fresh Worker or mock
// backend. This enables:
//   - Deterministic regression testing with production traffic shapes
//   - Coverage of edge cases (regression, stopped, duplicate announces)
//   - Offline invariant checking via the AnnounceOracle interface

// ── Oracle ────────────────────────────────────────────────────────────────────

// AnnounceOracle validates an (AnnounceRequest, AnnounceResponse) pair against
// a set of invariants. Implementations may check ratio bounds, interval
// consistency, seeder/leecher sign conventions, etc.
type AnnounceOracle interface {
	// Check returns (valid, reason). When valid is false, reason explains the
	// invariant violation. May be called concurrently.
	Check(req *AnnounceRequest, resp *AnnounceResponse) (valid bool, reason string)
}

// OracleFunc adapts a function to the AnnounceOracle interface.
type OracleFunc func(req *AnnounceRequest, resp *AnnounceResponse) (bool, string)

func (f OracleFunc) Check(req *AnnounceRequest, resp *AnnounceResponse) (bool, string) {
	return f(req, resp)
}

// IntervalOracle checks that the response interval is within the configured
// [minSec, maxSec] band. Useful as a sanity check on Markov-tuned intervals.
func IntervalOracle(minSec, maxSec int32) AnnounceOracle {
	return OracleFunc(func(_ *AnnounceRequest, resp *AnnounceResponse) (bool, string) {
		if resp.Interval < minSec {
			return false, fmt.Sprintf("interval %d < minimum %d", resp.Interval, minSec)
		}
		if maxSec > 0 && resp.Interval > maxSec {
			return false, fmt.Sprintf("interval %d > maximum %d", resp.Interval, maxSec)
		}
		return true, ""
	})
}

// CompactPeerLengthOracle checks that the peer bytes are a multiple of 6 (IPv4
// compact format). Catches off-by-one bugs in peer serialization.
func CompactPeerLengthOracle() AnnounceOracle {
	return OracleFunc(func(_ *AnnounceRequest, resp *AnnounceResponse) (bool, string) {
		if len(resp.Peers)%6 != 0 {
			return false, fmt.Sprintf("peers length %d is not a multiple of 6", len(resp.Peers))
		}
		return true, ""
	})
}

// SwarmCountOracle checks that Complete+Incomplete > 0 unless the torrent just
// received its first peer (event == "started" with no prior peers).
func SwarmCountOracle() AnnounceOracle {
	return OracleFunc(func(req *AnnounceRequest, resp *AnnounceResponse) (bool, string) {
		if resp.Complete == 0 && resp.Incomplete == 0 && req.Event != "started" {
			return false, "swarm reports zero peers but event is not 'started'"
		}
		return true, ""
	})
}

// CompositeOracle runs several oracles in order and returns the first failure.
type CompositeOracle struct {
	oracles []AnnounceOracle
}

func NewCompositeOracle(oracles ...AnnounceOracle) *CompositeOracle {
	return &CompositeOracle{oracles: oracles}
}

func (c *CompositeOracle) Check(req *AnnounceRequest, resp *AnnounceResponse) (bool, string) {
	for _, o := range c.oracles {
		if ok, reason := o.Check(req, resp); !ok {
			return false, reason
		}
	}
	return true, ""
}

// ── Binary log format ─────────────────────────────────────────────────────────

// AnnounceRecord is one captured announce round-trip.
type AnnounceRecord struct {
	At        time.Time        `json:"at"`
	Request   *AnnounceRequest `json:"request"`
	Response  *AnnounceResponse `json:"response,omitempty"`
	ErrorText string           `json:"error,omitempty"`
}

// recordHeader is the fixed-length binary header for each log entry.
// Layout: [4]magic | [4]length (big-endian uint32 of JSON payload)
var recordMagic = [4]byte{0x4F, 0x43, 0x4C, 0x54} // "OCLT"

// ReplayHarness captures and replays announce traffic.
type ReplayHarness struct {
	oracle  AnnounceOracle
	records []AnnounceRecord
}

// NewReplayHarness creates an empty harness with the given oracle.
// oracle may be nil to skip invariant checking during replay.
func NewReplayHarness(oracle AnnounceOracle) *ReplayHarness {
	return &ReplayHarness{oracle: oracle}
}

// Record appends one round-trip to the harness. errText is empty on success.
func (h *ReplayHarness) Record(req *AnnounceRequest, resp *AnnounceResponse, errText string) {
	h.records = append(h.records, AnnounceRecord{
		At:        time.Now(),
		Request:   req,
		Response:  resp,
		ErrorText: errText,
	})
}

// Len returns the number of recorded round-trips.
func (h *ReplayHarness) Len() int { return len(h.records) }

// WriteTo serializes all records to w in a length-prefixed JSON binary format.
// Each frame: 4-byte magic | 4-byte big-endian JSON length | JSON payload.
func (h *ReplayHarness) WriteTo(w io.Writer) error {
	for i := range h.records {
		payload, err := json.Marshal(&h.records[i])
		if err != nil {
			return fmt.Errorf("marshal record %d: %w", i, err)
		}
		if _, err := w.Write(recordMagic[:]); err != nil {
			return err
		}
		var lenBuf [4]byte
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
		if _, err := w.Write(lenBuf[:]); err != nil {
			return err
		}
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

// ReadFrom deserializes records written by WriteTo into h (appends to existing).
func (h *ReplayHarness) ReadFrom(r io.Reader) error {
	var magic [4]byte
	var lenBuf [4]byte
	for {
		if _, err := io.ReadFull(r, magic[:]); err == io.EOF {
			return nil
		} else if err != nil {
			return fmt.Errorf("read magic: %w", err)
		}
		if magic != recordMagic {
			return fmt.Errorf("invalid record magic: %x", magic)
		}
		if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
			return fmt.Errorf("read length: %w", err)
		}
		n := binary.BigEndian.Uint32(lenBuf[:])
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return fmt.Errorf("read payload: %w", err)
		}
		var rec AnnounceRecord
		if err := json.Unmarshal(buf, &rec); err != nil {
			return fmt.Errorf("unmarshal record: %w", err)
		}
		h.records = append(h.records, rec)
	}
}

// ReplayResult holds the outcome of replaying one record.
type ReplayResult struct {
	Index     int
	Record    *AnnounceRecord
	GotResp   *AnnounceResponse
	GotErr    error
	OracleFail string // non-empty when oracle check failed
}

// Replay calls handler for each recorded request and compares the result
// against the oracle. Returns results for all records; does not stop on error.
func (h *ReplayHarness) Replay(handler func(req *AnnounceRequest) (*AnnounceResponse, error)) []ReplayResult {
	results := make([]ReplayResult, len(h.records))
	for i := range h.records {
		rec := &h.records[i]
		var req AnnounceRequest
		if rec.Request != nil {
			req = *rec.Request
			if req.IP == nil {
				req.IP = net.IPv4(127, 0, 0, 1)
			}
		}
		resp, err := handler(&req)
		result := ReplayResult{Index: i, Record: rec, GotResp: resp, GotErr: err}
		if h.oracle != nil && resp != nil {
			if ok, reason := h.oracle.Check(&req, resp); !ok {
				result.OracleFail = reason
			}
		}
		results[i] = result
	}
	return results
}

// DiffResults compares recorded responses against replayed ones.
// Returns a textual diff for any pair where interval or peer count diverges.
func DiffResults(results []ReplayResult) []string {
	var diffs []string
	for _, r := range results {
		if r.Record.Response == nil || r.GotResp == nil {
			continue
		}
		orig := r.Record.Response
		got := r.GotResp
		if orig.Interval != got.Interval {
			diffs = append(diffs, fmt.Sprintf(
				"[%d] interval: recorded=%d got=%d", r.Index, orig.Interval, got.Interval))
		}
		origPeers := len(orig.Peers) / 6
		gotPeers := len(got.Peers) / 6
		if origPeers != gotPeers {
			diffs = append(diffs, fmt.Sprintf(
				"[%d] peer_count: recorded=%d got=%d", r.Index, origPeers, gotPeers))
		}
		if r.OracleFail != "" {
			diffs = append(diffs, fmt.Sprintf("[%d] oracle: %s", r.Index, r.OracleFail))
		}
	}
	return diffs
}

// marshalRequestForLog serializes an AnnounceRequest for log storage; IP is
// written as a string to keep the JSON human-readable.
func marshalRequestForLog(req *AnnounceRequest) []byte {
	if req == nil {
		return []byte("null")
	}
	m := map[string]interface{}{
		"info_hash":  req.InfoHash,
		"port":       req.Port,
		"uploaded":   req.Uploaded,
		"downloaded": req.Downloaded,
		"left":       req.Left,
		"event":      req.Event,
		"numwant":    req.NumWant,
		"compact":    req.Compact,
		"ip":         req.IP.String(),
	}
	b, _ := json.Marshal(m)
	return b
}

// roundTripBytes is a helper used by tests to serialize and deserialize a
// harness through a bytes.Buffer, verifying the binary codec is symmetric.
func roundTripBytes(h *ReplayHarness) (*ReplayHarness, error) {
	var buf bytes.Buffer
	if err := h.WriteTo(&buf); err != nil {
		return nil, err
	}
	h2 := NewReplayHarness(nil)
	if err := h2.ReadFrom(&buf); err != nil {
		return nil, err
	}
	return h2, nil
}
