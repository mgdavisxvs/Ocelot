package tracker

import "sync"

// SwarmAdmissionPolicy controls which passkeys are allowed to join the swarm
// for a given info_hash. It is enforced outside the hot announce path —
// the tracker checks admission before adding a peer.
//
// Design invariant: the policy is per-artifact (info_hash), not global.
// An empty allow-list means "open to all authenticated users" (default).
// A non-empty allow-list is an explicit ACL: only listed passkeys are admitted.
type SwarmAdmissionPolicy struct {
	mu       sync.RWMutex
	policies map[string]*artifactACL // info_hash → ACL
}

type artifactACL struct {
	// allowAll means any authenticated passkey is admitted.
	allowAll bool
	// allow is the explicit passkey set when allowAll is false.
	allow map[string]struct{}
}

func NewSwarmAdmissionPolicy() *SwarmAdmissionPolicy {
	return &SwarmAdmissionPolicy{
		policies: make(map[string]*artifactACL),
	}
}

// IsAdmitted returns true if the passkey is allowed to join the swarm for infoHash.
// Decision logic:
//  1. If no policy is configured for infoHash → admitted (open default).
//  2. If allowAll is set → admitted.
//  3. Otherwise → passkey must appear in the allow list.
func (p *SwarmAdmissionPolicy) IsAdmitted(infoHash, passkey string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	acl, ok := p.policies[infoHash]
	if !ok {
		return true // open default
	}
	if acl.allowAll {
		return true
	}
	_, allowed := acl.allow[passkey]
	return allowed
}

// SetOpen marks infoHash as open to all authenticated passkeys (removes any ACL).
func (p *SwarmAdmissionPolicy) SetOpen(infoHash string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.policies, infoHash)
}

// SetACL replaces the per-artifact allow-list with the given passkeys.
// An empty passkeys slice is equivalent to SetOpen.
func (p *SwarmAdmissionPolicy) SetACL(infoHash string, passkeys []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(passkeys) == 0 {
		delete(p.policies, infoHash)
		return
	}
	acl := &artifactACL{allow: make(map[string]struct{}, len(passkeys))}
	for _, pk := range passkeys {
		acl.allow[pk] = struct{}{}
	}
	p.policies[infoHash] = acl
}

// AddPasskey adds a single passkey to the per-artifact allow-list.
// Creates the ACL if it doesn't exist yet (transitions from open to restricted).
func (p *SwarmAdmissionPolicy) AddPasskey(infoHash, passkey string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	acl, ok := p.policies[infoHash]
	if !ok {
		acl = &artifactACL{allow: make(map[string]struct{})}
		p.policies[infoHash] = acl
	}
	if !acl.allowAll {
		acl.allow[passkey] = struct{}{}
	}
}

// RemovePasskey removes a passkey from the per-artifact allow-list.
func (p *SwarmAdmissionPolicy) RemovePasskey(infoHash, passkey string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	acl, ok := p.policies[infoHash]
	if !ok {
		return
	}
	delete(acl.allow, passkey)
	if len(acl.allow) == 0 && !acl.allowAll {
		// ACL with no passkeys: keep as closed (no one admitted) until explicitly opened
	}
}

// AllowList returns the current passkey set for infoHash, and whether it's open.
func (p *SwarmAdmissionPolicy) AllowList(infoHash string) (passkeys []string, isOpen bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	acl, ok := p.policies[infoHash]
	if !ok {
		return nil, true
	}
	if acl.allowAll {
		return nil, true
	}
	out := make([]string, 0, len(acl.allow))
	for pk := range acl.allow {
		out = append(out, pk)
	}
	return out, false
}
