package tracker

import (
	"context"
	"net"
	"testing"
)

// ── Feature 2: Admission Policy Enforcement tests ─────────────────────────────

// newCompactAnnounceReqFor returns a minimal compact announce request for infoHash.
func newCompactAnnounceReqFor(infoHash string) *AnnounceRequest {
	return &AnnounceRequest{
		InfoHash: infoHash,
		PeerID:   []byte("-qB0000-00000000ADMN"),
		Port:     6881,
		Compact:  true,
		Left:     0,
	}
}

func TestAdmissionAllowsWhenPolicyNil(t *testing.T) {
	f := newTestFixture()
	f.worker.Admission = nil // default — nil means all admitted

	req := newCompactAnnounceReqFor(testInfoHash)
	u, _ := f.worker.Users.Get(testPasskey)
	_, err := f.worker.Announce(context.Background(), req, u, net.ParseIP(testIP), "", testPasskey)
	if err != nil {
		t.Errorf("nil Admission should admit all: got err %v", err)
	}
}

func TestAdmissionBlocksUnlistedPasskey(t *testing.T) {
	f := newTestFixture()
	pol := NewSwarmAdmissionPolicy()
	pol.AddPasskey(testInfoHash, "some-other-passkey00000000000000")
	f.worker.Admission = pol

	req := newCompactAnnounceReqFor(testInfoHash)
	u, _ := f.worker.Users.Get(testPasskey)
	_, err := f.worker.Announce(context.Background(), req, u, net.ParseIP(testIP), "", testPasskey)
	if err == nil {
		t.Error("non-admitted passkey should be rejected")
	}
}

func TestAdmissionAllowsListedPasskey(t *testing.T) {
	f := newTestFixture()
	pol := NewSwarmAdmissionPolicy()
	pol.AddPasskey(testInfoHash, testPasskey)
	f.worker.Admission = pol

	req := newCompactAnnounceReqFor(testInfoHash)
	u, _ := f.worker.Users.Get(testPasskey)
	_, err := f.worker.Announce(context.Background(), req, u, net.ParseIP(testIP), "", testPasskey)
	if err != nil {
		t.Errorf("admitted passkey should succeed: %v", err)
	}
}

func TestAdmissionOpenSwarmAdmitsAll(t *testing.T) {
	f := newTestFixture()
	pol := NewSwarmAdmissionPolicy()
	// No AddPasskey calls — policy object exists but no ACL for testInfoHash → open default.
	f.worker.Admission = pol

	req := newCompactAnnounceReqFor(testInfoHash)
	u, _ := f.worker.Users.Get(testPasskey)
	_, err := f.worker.Announce(context.Background(), req, u, net.ParseIP(testIP), "", testPasskey)
	if err != nil {
		t.Errorf("open swarm (no ACL) should admit any passkey: %v", err)
	}
}
