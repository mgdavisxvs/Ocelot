package agent

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

// client is an HTTP client that attaches HMAC-SHA256 authentication to every
// request per AGENT_PROTOCOL.md §2.
type client struct {
	http    *http.Client
	baseURL string
	nodeID  string
	secret  []byte // raw 32-byte HMAC secret decoded from hex
}

func newClient(baseURL string, nodeID string, secretHex string, skipTLS bool) (*client, error) {
	secret, err := hex.DecodeString(secretHex)
	if err != nil {
		return nil, fmt.Errorf("client: decode secret: %w", err)
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if skipTLS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
		slog.Warn("TLS verification disabled — do not use in production")
	}

	return &client{
		http:    &http.Client{Timeout: 30 * time.Second, Transport: transport},
		baseURL: baseURL,
		nodeID:  nodeID,
		secret:  secret,
	}, nil
}

// newBootstrapClient creates an unauthenticated client for the registration
// endpoint, using a Bearer token instead of HMAC (§2.4).
func newBootstrapClient(baseURL string, bootstrapToken string, skipTLS bool) *bootstrapClient {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if skipTLS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}
	return &bootstrapClient{
		http:    &http.Client{Timeout: 30 * time.Second, Transport: transport},
		baseURL: baseURL,
		token:   bootstrapToken,
	}
}

type bootstrapClient struct {
	http    *http.Client
	baseURL string
	token   string
}

func (bc *bootstrapClient) post(ctx context.Context, path string, req, resp interface{}) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("bootstrap: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, bc.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("bootstrap: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+bc.token)

	return bc.do(httpReq, resp)
}

func (bc *bootstrapClient) do(req *http.Request, resp interface{}) error {
	r, err := bc.http.Do(req)
	if err != nil {
		return fmt.Errorf("bootstrap: http: %w", err)
	}
	defer r.Body.Close()
	return decodeResponse(r, resp)
}

// sign attaches HMAC authentication headers to an outbound request (§2.2).
// The request body is fully buffered so we can hash it.
func (c *client) sign(req *http.Request, bodyBytes []byte) {
	tsMs := strconv.FormatInt(time.Now().UnixMilli(), 10)

	bodyHash := sha256.Sum256(bodyBytes)
	bodyHashHex := hex.EncodeToString(bodyHash[:])

	message := c.nodeID + ":" + tsMs + ":" + bodyHashHex
	mac := hmac.New(sha256.New, c.secret)
	mac.Write([]byte(message))
	sig := hex.EncodeToString(mac.Sum(nil))

	req.Header.Set("X-Ocelot-Node-ID", c.nodeID)
	req.Header.Set("X-Ocelot-Timestamp", tsMs)
	req.Header.Set("X-Ocelot-Signature", sig)
}

// get performs a signed GET with retry per §6.
func (c *client) get(ctx context.Context, path string, resp interface{}) error {
	return c.withRetry(ctx, "GET "+path, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
		if err != nil {
			return fmt.Errorf("build GET: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		c.sign(req, nil)

		r, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("http: %w", err)
		}
		defer r.Body.Close()
		return decodeResponse(r, resp)
	})
}

// post performs a signed POST with retry per §6.
func (c *client) post(ctx context.Context, path string, reqBody, resp interface{}) error {
	return c.withRetry(ctx, "POST "+path, func() error {
		body, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("build POST: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		c.sign(req, body)

		r, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("http: %w", err)
		}
		defer r.Body.Close()
		return decodeResponse(r, resp)
	})
}

// retryDelays are the base delays per §6 (index = attempt number, 0-based).
var retryDelays = []time.Duration{2, 4, 8, 16, 30}

// withRetry executes fn up to maxRetries=10 times with exponential backoff
// and jitter per §6. Returns a permanent error after 10 failures but continues
// to be callable — the agent never exits on transient failures.
func (c *client) withRetry(ctx context.Context, label string, fn func() error) error {
	const maxAttempts = 10
	for attempt := 0; attempt < maxAttempts; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}

		if isContextDone(ctx) {
			return ctx.Err()
		}

		base := retryDelays[min(attempt, len(retryDelays)-1)]
		jitter := time.Duration(rand.Int63n(int64(base/2))) //nolint:gosec
		delay := base*time.Second + jitter

		slog.Warn("agent request failed",
			"op", label,
			"attempt", attempt+1,
			"err", err,
			"retry_in", delay,
		)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return fmt.Errorf("%s: failed after %d attempts", label, maxAttempts)
}

func decodeResponse(r *http.Response, out interface{}) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	if r.StatusCode == http.StatusNotModified {
		return nil
	}

	if r.StatusCode >= 400 {
		// Try to decode error envelope.
		var errResp struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				Retry   bool   `json:"retry"`
			} `json:"error"`
		}
		if jsonErr := json.Unmarshal(body, &errResp); jsonErr == nil && errResp.Error.Code != "" {
			return &apiError{
				statusCode: r.StatusCode,
				code:       errResp.Error.Code,
				message:    errResp.Error.Message,
				retry:      errResp.Error.Retry,
			}
		}
		return fmt.Errorf("http %d: %s", r.StatusCode, body)
	}

	if out != nil && len(body) > 0 {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

type apiError struct {
	statusCode int
	code       string
	message    string
	retry      bool
}

func (e *apiError) Error() string {
	return fmt.Sprintf("api error %d %s: %s", e.statusCode, e.code, e.message)
}

func (e *apiError) IsGone() bool     { return e.statusCode == http.StatusGone }
func (e *apiError) IsUnauth() bool   { return e.statusCode == http.StatusUnauthorized }
func (e *apiError) IsRetryable() bool { return e.retry || e.statusCode >= 500 }

func isContextDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
