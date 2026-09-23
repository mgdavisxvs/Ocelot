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

// NotifyFreeleech notifies Gazelle to activate freeleech for a torrent.
func (g *GazelleSiteComm) NotifyFreeleech(torrentID int64, hours int) error {
	params := url.Values{
		"action":    {"notify_freeleech"},
		"torrentid": {fmt.Sprintf("%d", torrentID)},
		"hours":     {fmt.Sprintf("%d", hours)},
	}
	return g.post(params)
}

// ReportAnomaly reports a suspicious user to Gazelle.
func (g *GazelleSiteComm) ReportAnomaly(userID int64, score float64) error {
	params := url.Values{
		"action": {"report_anomaly"},
		"userid": {fmt.Sprintf("%d", userID)},
		"score":  {fmt.Sprintf("%.4f", score)},
	}
	return g.post(params)
}

// UpdateStats pushes aggregate seeder/leecher/completed counts to Gazelle.
func (g *GazelleSiteComm) UpdateStats(seeders, leechers, completed int64) error {
	params := url.Values{
		"action":    {"update_stats"},
		"seeders":   {fmt.Sprintf("%d", seeders)},
		"leechers":  {fmt.Sprintf("%d", leechers)},
		"completed": {fmt.Sprintf("%d", completed)},
	}
	return g.post(params)
}

// BanUser instructs Gazelle to ban a user.
func (g *GazelleSiteComm) BanUser(userID int64) error {
	params := url.Values{
		"action": {"ban_user"},
		"userid": {fmt.Sprintf("%d", userID)},
	}
	return g.post(params)
}

// UnbanUser instructs Gazelle to lift a user ban.
func (g *GazelleSiteComm) UnbanUser(userID int64) error {
	params := url.Values{
		"action": {"unban_user"},
		"userid": {fmt.Sprintf("%d", userID)},
	}
	return g.post(params)
}

func (g *GazelleSiteComm) GrantFreeleech(torrentID TorrentID) {
	params := url.Values{
		"action":    {"grant_freeleech"},
		"torrentid": {fmt.Sprintf("%d", torrentID)},
	}
	_ = g.post(params)
}

func (g *GazelleSiteComm) post(params url.Values) error {
	params.Set("password", g.password)
	return RetryWithBackoff(func() error {
		resp, err := g.client.PostForm(g.baseURL, params)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode >= 500 {
			// 5xx → transient; 4xx → permanent (bad request/auth)
			return fmt.Errorf("HTTP %d from Gazelle", resp.StatusCode)
		}
		if resp.StatusCode >= 400 {
			return fmt.Errorf("HTTP %d from Gazelle", resp.StatusCode)
		}
		return nil
	}, RetryConfig{
		MaxRetries:  2,
		InitialWait: 200 * time.Millisecond,
		MaxWait:     2 * time.Second,
		Multiplier:  2.0,
	})
}

// NoOpSiteComm is used when no Gazelle URL is configured.
type NoOpSiteComm struct{}

func (n *NoOpSiteComm) ExpireToken(torrentID TorrentID, userID UserID) {
	log.Printf("site_comm (noop): expire_token torrent=%d user=%d", torrentID, userID)
}

func (n *NoOpSiteComm) NotifyFreeleech(torrentID int64, hours int) error {
	log.Printf("site_comm (noop): notify_freeleech torrent=%d hours=%d", torrentID, hours)
	return nil
}

func (n *NoOpSiteComm) ReportAnomaly(userID int64, score float64) error {
	log.Printf("site_comm (noop): report_anomaly user=%d score=%.4f", userID, score)
	return nil
}

func (n *NoOpSiteComm) UpdateStats(seeders, leechers, completed int64) error {
	log.Printf("site_comm (noop): update_stats seeders=%d leechers=%d completed=%d", seeders, leechers, completed)
	return nil
}

func (n *NoOpSiteComm) BanUser(userID int64) error {
	log.Printf("site_comm (noop): ban_user user=%d", userID)
	return nil
}

func (n *NoOpSiteComm) UnbanUser(userID int64) error {
	log.Printf("site_comm (noop): unban_user user=%d", userID)
	return nil
}

func (n *NoOpSiteComm) GrantFreeleech(torrentID TorrentID) {
	log.Printf("site_comm (noop): grant_freeleech torrent=%d", torrentID)
}
