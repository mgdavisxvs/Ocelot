package tracker

import (
	"fmt"
	"net"
)

// F-C1: Announce pipeline as a pure function chain.
//
// The existing Announce() method is large and difficult to test in isolation.
// This file provides an explicit pipeline abstraction: each stage is a pure
// AnnounceStep function that takes a pointer to an AnnounceCtx and returns an
// error, making stages individually testable and composable.
//
// Callers may assemble a custom pipeline via AnnouncePipeline.Run(ctx); the
// existing Announce() method is unchanged and continues to be the primary path.

// AnnounceCtx carries all state through the announce pipeline. It is populated
// incrementally — each stage reads from and writes to this struct. Stages must
// not hold references to ctx after returning.
type AnnounceCtx struct {
	// Inputs (set by caller before Run)
	Req       *AnnounceRequest
	User      *User
	ClientIP  net.IP
	UserAgent string
	Passkey   string

	// Intermediate results (set by pipeline stages)
	Role    PeerRole
	Torrent *Torrent // resolved torrent, set by LookupTorrentStep
	Peer    *Peer    // resolved or created peer, set by ResolvePeerStep

	// Output (set by ResponseStep)
	Response *AnnounceResponse
}

// AnnounceStep is a pure pipeline stage function. It receives the shared
// AnnounceCtx, performs its operation, and either returns nil to continue
// or an error to abort the pipeline.
type AnnounceStep func(ctx *AnnounceCtx) error

// AnnouncePipeline is an ordered sequence of AnnounceStep functions.
// Run executes them in order; the first error aborts the chain.
type AnnouncePipeline struct {
	steps []AnnounceStep
}

// NewAnnouncePipeline builds a pipeline from the given steps.
func NewAnnouncePipeline(steps ...AnnounceStep) *AnnouncePipeline {
	return &AnnouncePipeline{steps: steps}
}

// Run executes each step in order. Returns the first error encountered.
func (p *AnnouncePipeline) Run(ctx *AnnounceCtx) error {
	for _, step := range p.steps {
		if err := step(ctx); err != nil {
			return err
		}
	}
	return nil
}

// ── Named pipeline stages ─────────────────────────────────────────────────────

// ValidateRequestStep checks compact support, peer ID length, and basic field
// sanity. No side effects; safe to run multiple times.
func ValidateRequestStep() AnnounceStep {
	return func(ctx *AnnounceCtx) error {
		req := ctx.Req
		if req == nil {
			return fmt.Errorf("nil announce request")
		}
		if !req.Compact {
			return fmt.Errorf("your client does not support compact announces")
		}
		if len(req.PeerID) != 20 {
			return fmt.Errorf("invalid peer ID length: %d", len(req.PeerID))
		}
		if req.Port == 0 {
			return fmt.Errorf("invalid port 0")
		}
		return nil
	}
}

// DeriveRoleStep assigns ctx.Role from the request fields. Pure — no I/O.
func DeriveRoleStep() AnnounceStep {
	return func(ctx *AnnounceCtx) error {
		ctx.Role = peerRoleFromRequest(ctx.Req)
		return nil
	}
}

// WhitelistStep checks the client peer-id against the whitelist.
func WhitelistStep(wl *Whitelist) AnnounceStep {
	return func(ctx *AnnounceCtx) error {
		if wl != nil && !wl.IsAllowed(ctx.Req.PeerID) {
			return fmt.Errorf("your client is not on the whitelist")
		}
		return nil
	}
}

// AdmissionStep checks the swarm admission policy (passkey-level).
func AdmissionStep(adm *SwarmAdmissionPolicy) AnnounceStep {
	return func(ctx *AnnounceCtx) error {
		if adm != nil && !adm.IsAdmitted(ctx.Req.InfoHash, ctx.Passkey) {
			return fmt.Errorf("not admitted to swarm")
		}
		return nil
	}
}

// LookupTorrentStep resolves the torrent from the in-memory index and writes
// it to ctx.Torrent. Returns an error when the torrent is not registered.
func LookupTorrentStep(torrents *TorrentList) AnnounceStep {
	return func(ctx *AnnounceCtx) error {
		t, ok := torrents.Get(ctx.Req.InfoHash)
		if !ok {
			return fmt.Errorf("unregistered torrent")
		}
		ctx.Torrent = t
		return nil
	}
}

// DefaultAnnouncePipeline returns the canonical ordering of announce pipeline
// stages used by the tracker. Caller is responsible for the peer-resolution and
// response stages, which require Worker-level state and DB access.
func DefaultAnnouncePipeline(wl *Whitelist, adm *SwarmAdmissionPolicy, torrents *TorrentList) *AnnouncePipeline {
	return NewAnnouncePipeline(
		ValidateRequestStep(),
		AdmissionStep(adm),
		WhitelistStep(wl),
		DeriveRoleStep(),
		LookupTorrentStep(torrents),
	)
}
