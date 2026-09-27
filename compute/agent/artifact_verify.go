package agent

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// VerifyStatus tracks the lifecycle of a Merkle piece verification job.
type VerifyStatus uint8

const (
	VerifyUnknown   VerifyStatus = iota // not yet started
	VerifyFetching                      // data is being fetched from storage
	VerifyInProgress                    // SHA-1 piece hashes being checked
	VerifyPassed                        // all pieces verified; artifact is Available
	VerifyFailed                        // one or more pieces corrupt or missing
)

func (s VerifyStatus) String() string {
	switch s {
	case VerifyFetching:
		return "Fetching"
	case VerifyInProgress:
		return "Verifying"
	case VerifyPassed:
		return "Available"
	case VerifyFailed:
		return "Failed"
	default:
		return "Unknown"
	}
}

// PieceVerifyResult holds the outcome of verifying one SHA-1 piece.
type PieceVerifyResult struct {
	Index    int
	Expected string // hex-encoded SHA-1
	Got      string // hex-encoded SHA-1 of actual data
	OK       bool
}

// ArtifactVerifier performs Merkle piece verification for a locally-held artifact.
// It reads piece data from a storage directory and compares each piece's SHA-1
// against the expected hash list supplied from the torrent metadata.
type ArtifactVerifier struct {
	dataDir    string
	pieceSize  int64  // bytes per piece
	totalSize  int64  // total artifact bytes
	infoHash   string
	status     atomic.Uint32 // VerifyStatus stored as uint32
	lastResult []PieceVerifyResult
	lastError  error
	lastRun    time.Time
}

// NewArtifactVerifier creates a verifier for the artifact identified by infoHash
// stored under dataDir.  pieceSize and totalSize must match the torrent metadata.
func NewArtifactVerifier(dataDir, infoHash string, pieceSize, totalSize int64) *ArtifactVerifier {
	v := &ArtifactVerifier{
		dataDir:   dataDir,
		infoHash:  infoHash,
		pieceSize: pieceSize,
		totalSize: totalSize,
	}
	v.status.Store(uint32(VerifyUnknown))
	return v
}

// Status returns the current verification status.
func (v *ArtifactVerifier) Status() VerifyStatus {
	return VerifyStatus(v.status.Load())
}

// LastResult returns the piece-level results from the most recent verification.
func (v *ArtifactVerifier) LastResult() []PieceVerifyResult { return v.lastResult }

// LastError returns the error (if any) from the most recent verification.
func (v *ArtifactVerifier) LastError() error { return v.lastError }

// Verify runs a full piece verification in the calling goroutine, updating
// the status atomically.  ctx can be cancelled to abort mid-run.
//
// expectedHashes must be a slice of hex-encoded SHA-1 hashes (20 bytes each),
// one per piece in piece-index order, as found in the torrent's info.pieces field.
func (v *ArtifactVerifier) Verify(ctx context.Context, expectedHashes []string) error {
	v.status.Store(uint32(VerifyFetching))
	v.lastRun = time.Now()

	artifactPath := filepath.Join(v.dataDir, v.infoHash)
	f, err := os.Open(artifactPath)
	if err != nil {
		v.status.Store(uint32(VerifyFailed))
		v.lastError = fmt.Errorf("open artifact %s: %w", v.infoHash, err)
		return v.lastError
	}
	defer f.Close()

	v.status.Store(uint32(VerifyInProgress))

	nPieces := len(expectedHashes)
	results := make([]PieceVerifyResult, 0, nPieces)
	allOK := true
	buf := make([]byte, v.pieceSize)

	for i := 0; i < nPieces; i++ {
		if err := ctx.Err(); err != nil {
			v.status.Store(uint32(VerifyFailed))
			v.lastError = errors.New("verification cancelled")
			return v.lastError
		}

		n, readErr := io.ReadFull(f, buf)
		if readErr != nil && readErr != io.ErrUnexpectedEOF {
			v.status.Store(uint32(VerifyFailed))
			v.lastError = fmt.Errorf("read piece %d: %w", i, readErr)
			return v.lastError
		}

		sum := sha1.Sum(buf[:n])
		got := hex.EncodeToString(sum[:])
		expected := expectedHashes[i]
		ok := got == expected

		results = append(results, PieceVerifyResult{
			Index:    i,
			Expected: expected,
			Got:      got,
			OK:       ok,
		})
		if !ok {
			allOK = false
		}
	}

	v.lastResult = results
	v.lastError = nil
	if allOK {
		v.status.Store(uint32(VerifyPassed))
	} else {
		v.status.Store(uint32(VerifyFailed))
		v.lastError = fmt.Errorf("%d piece(s) failed hash check", countFailed(results))
	}
	return v.lastError
}

// VerifyAsync runs Verify in a goroutine, sending the result on the returned channel.
func (v *ArtifactVerifier) VerifyAsync(ctx context.Context, expectedHashes []string) <-chan error {
	ch := make(chan error, 1)
	go func() { ch <- v.Verify(ctx, expectedHashes) }()
	return ch
}

func countFailed(results []PieceVerifyResult) int {
	n := 0
	for _, r := range results {
		if !r.OK {
			n++
		}
	}
	return n
}

// VerifyArtifact is a convenience function for one-shot verification.
// Returns (true, nil) when all pieces pass, (false, nil) when some fail, or
// (false, err) on I/O errors.
func VerifyArtifact(ctx context.Context, dataDir, infoHash string, pieceSize, totalSize int64, expectedHashes []string) (bool, error) {
	v := NewArtifactVerifier(dataDir, infoHash, pieceSize, totalSize)
	if err := v.Verify(ctx, expectedHashes); err != nil {
		return false, err
	}
	return v.Status() == VerifyPassed, nil
}
