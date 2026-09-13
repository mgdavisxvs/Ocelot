package tracker

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"
)

// GazelleSiteComm sends authenticated HTTP callbacks to the Gazelle web app,
// replacing the C++ site_comm class (site_comm.cpp).
type GazelleSiteComm struct {
	baseURL  string
	password string
	client   *http.Client
}

// NewGazelleSiteComm creates a SiteCommInterface backed by real HTTP calls.
// baseURL is the Gazelle tracker callback endpoint,
// e.g. "http://gazelle.example.com/tracker/callback.php".
func NewGazelleSiteComm(baseURL, password string) *GazelleSiteComm {
	return &GazelleSiteComm{
		baseURL:  baseURL,
		password: password,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// ExpireToken notifies Gazelle that a freeleech token was consumed.
func (g *GazelleSiteComm) ExpireToken(torrentID TorrentID, userID UserID) {
	params := url.Values{
		"action":    {"expire_token"},
		"torrentid": {fmt.Sprintf("%d", torrentID)},
		"userid":    {fmt.Sprintf("%d", userID)},
	}
	if err := g.post(params); err != nil {
		log.Printf("site_comm: expire_token torrent=%d user=%d: %v", torrentID, userID, err)
	}
}

const siteCommMaxRetries = 3

// siteCommBaseDelay is the initial retry back-off. Declared as var so tests
// can set it to zero to avoid sleeping.
var siteCommBaseDelay = 2 * time.Second

func (g *GazelleSiteComm) post(params url.Values) error {
	params.Set("password", g.password)
	var lastErr error
	for attempt := 0; attempt < siteCommMaxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(siteCommBaseDelay << (attempt - 1)) // 2s, 4s
		}
		resp, err := g.client.PostForm(g.baseURL, params)
		if err != nil {
			lastErr = err
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("HTTP %d from Gazelle", resp.StatusCode)
			continue // transient server error; retry
		}
		if resp.StatusCode >= 400 {
			return fmt.Errorf("HTTP %d from Gazelle (client error)", resp.StatusCode)
		}
		return nil
	}
	return fmt.Errorf("site_comm: %d attempts exhausted: %w", siteCommMaxRetries, lastErr)
}

// NoOpSiteComm is used when no Gazelle URL is configured.
type NoOpSiteComm struct{}

func (n *NoOpSiteComm) ExpireToken(torrentID TorrentID, userID UserID) {
	log.Printf("site_comm (noop): expire_token torrent=%d user=%d", torrentID, userID)
}
