package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/mgdavisxvs/Ocelot/virtualserver/domain"
)

// VerifySnapshotChain walks all ready snapshots for a volume in completion
// order and re-derives each chain_hash. Returns the ID of the first snapshot
// whose stored hash diverges from the expected value, or an empty string when
// the chain is intact. Pre-migration snapshots (chain_hash == "") are skipped.
func (s *VSStore) VerifySnapshotChain(ctx context.Context, volumeID string) (string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, driver_ref, chain_hash
		FROM virtualserver_volume_snapshots
		WHERE volume_id=? AND state=? AND chain_hash != ''
		ORDER BY completed_at ASC`,
		volumeID, string(domain.SnapshotReady),
	)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var prevHash string
	for rows.Next() {
		var id, driverRef, storedHash string
		if err := rows.Scan(&id, &driverRef, &storedHash); err != nil {
			return "", err
		}
		expected := computeChainHash(driverRef, prevHash)
		if storedHash != expected {
			return id, nil
		}
		prevHash = storedHash
	}
	return "", rows.Err()
}

// computeChainHash returns hex(SHA256(driverRef + "|" + prevHash)).
// The separator "|" prevents length-extension ambiguity between the two fields.
func computeChainHash(driverRef, prevHash string) string {
	h := sha256.New()
	h.Write([]byte(driverRef))
	h.Write([]byte("|"))
	h.Write([]byte(prevHash))
	return hex.EncodeToString(h.Sum(nil))
}
