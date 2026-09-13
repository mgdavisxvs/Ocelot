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

func (g *GazelleSiteComm) post(params url.Values) error {
	params.Set("password", g.password)
	resp, err := g.client.PostForm(g.baseURL, params)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d from Gazelle", resp.StatusCode)
	}
	return nil
}

// NoOpSiteComm is used when no Gazelle URL is configured.
type NoOpSiteComm struct{}

func (n *NoOpSiteComm) ExpireToken(torrentID TorrentID, userID UserID) {
	log.Printf("site_comm (noop): expire_token torrent=%d user=%d", torrentID, userID)
}
