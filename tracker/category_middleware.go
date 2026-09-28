package tracker

// AnnounceMiddlewareFn is a function that wraps an AnnounceRequest before it
// reaches the core Announce handler. It may modify the request or return early.
// Returning a non-nil error short-circuits the announce pipeline.
type AnnounceMiddlewareFn func(req *AnnounceRequest, t *Torrent, u *User) error

// categoryMiddlewareTable maps TorrentCategory to its announce-time gate function.
// Each gate performs the category-specific check (rollout, quota, BAA, health gate…).
// Gates that need DB access capture the DB pointer at construction time via closures.
var categoryMiddlewareTable = map[TorrentCategory]AnnounceMiddlewareFn{
	CategoryDefault: nil, // no extra gate
}

// RegisterCategoryMiddleware registers a per-category middleware gate.
// Call from main.go after DB and Worker are initialized to install category gates.
func RegisterCategoryMiddleware(cat TorrentCategory, fn AnnounceMiddlewareFn) {
	categoryMiddlewareTable[cat] = fn
}

// runCategoryMiddleware executes the registered gate for the torrent's category.
// Returns an error if the announce should be rejected, or nil to proceed.
func runCategoryMiddleware(req *AnnounceRequest, t *Torrent, u *User) error {
	fn := categoryMiddlewareTable[t.Category]
	if fn == nil {
		return nil
	}
	return fn(req, t, u)
}
