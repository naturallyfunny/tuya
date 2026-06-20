// Package cloud is the pure-Tuya layer of the library: it speaks the Tuya Cloud
// OpenAPI and is keyed by Tuya UID, with no notion of an app-level owner.
//
// It speaks Tuya at the app (project) level — a single access ID/secret yields
// an access token that Client caches and refreshes on its own. Two orthogonal
// pieces compose here: Client is the transport (signing, token, Do); IoT
// wraps a Client and performs device operations against a Tuya account UID.
// IoT carries no tenant guard — mapping an opaque owner ID to a human's
// Tuya UID and asserting ownership is the root tuya package's concern.
package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
)

// response is the envelope every Tuya Cloud OpenAPI call returns.
type response struct {
	Success bool   `json:"success"`
	T       int64  `json:"t"`
	Tid     string `json:"tid"`

	Result json.RawMessage `json:"result,omitempty"`

	Code int    `json:"code,omitempty"`
	Msg  string `json:"msg,omitempty"`
}

// Client talks to the Tuya Cloud OpenAPI at the app level. The access token is
// cached in memory and refreshed on demand (lazily on expiry, and reactively
// when Tuya reports code 1010); there is no per-user token store because the
// credential is project-wide.
type Client struct {
	accessID     string
	accessSecret string
	baseURL      string
	httpClient   *http.Client
	token        *token
	tokenLock    sync.RWMutex
}

// Option configures a Client at construction time. The zero-configuration Client
// is fully usable; options only override defaults (currently just the HTTP
// client). New options can be added without breaking the New signature.
type Option func(*Client)

// WithHTTPClient sets the http.Client used for every Tuya request, controlling
// timeouts and transport. Without it, New uses http.DefaultClient.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

// New builds a Client. accessID and accessSecret are the Tuya Cloud project
// credentials; baseURL selects the regional data-center endpoint (e.g.
// https://openapi.tuyaus.com for the US, tuyaeu/tuyacn/tuyain for EU/China/India).
// Behaviour is tuned with Options such as WithHTTPClient. New prefetches an
// access token so a bad credential or unreachable region fails here, at wiring
// time, not on the first device call. Wrap the Client with NewIoT to perform
// device operations.
func New(accessID, accessSecret, baseURL string, opts ...Option) (*Client, error) {
	client := &Client{
		accessID:     accessID,
		accessSecret: accessSecret,
		baseURL:      baseURL,
		httpClient:   http.DefaultClient,
	}

	for _, opt := range opts {
		opt(client)
	}
	if client.httpClient == nil {
		client.httpClient = http.DefaultClient
	}

	if err := client.ensureValidToken(context.Background()); err != nil {
		return nil, fmt.Errorf("tuya: New: prefetch token: %w", err)
	}

	return client, nil
}

// Do performs a signed, authenticated request against the Tuya Cloud OpenAPI. On
// a token-expired response (code 1010) it refreshes once and retries, so callers
// never see a stale-token failure.
func (c *Client) Do(ctx context.Context, method, path string, body []byte) (json.RawMessage, error) {
	const maxIoTRequestAttempts = 2
	for attempt := 0; attempt < maxIoTRequestAttempts; attempt++ {
		fullURL := c.baseURL + path

		sig, err := c.signBusinessRequest(method, path, body)
		if err != nil {
			return nil, fmt.Errorf("failed to generate signature: %w", err)
		}

		bodyReader := bytes.NewReader(body)
		httpReq, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
		if err != nil {
			return nil, fmt.Errorf("failed to create request to %s: %w", fullURL, err)
		}

		if len(body) > 0 {
			httpReq.Header.Set("Content-Type", "application/json")
		}
		setAuthHeaders(httpReq, c.accessID, sig)

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("request to %s failed: %w", fullURL, err)
		}
		respBodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read response from %s: %w", fullURL, err)
		}

		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("request to %s returned non-200 status code: %d, body: %s", fullURL, resp.StatusCode, string(respBodyBytes))
		}

		var tuyaResp response
		if err := json.Unmarshal(respBodyBytes, &tuyaResp); err != nil {
			return nil, fmt.Errorf("failed to decode response from %s: %w", fullURL, err)
		}

		if tuyaResp.Success {
			return tuyaResp.Result, nil
		}

		const tokenExpiredTuyaErrorCode = 1010
		if tuyaResp.Code == tokenExpiredTuyaErrorCode && attempt == 0 {
			if err := c.ensureValidToken(ctx); err != nil {
				return nil, fmt.Errorf("failed to refresh token after Tuya error %d: %w", tuyaResp.Code, err)
			}
			continue
		}

		return nil, fmt.Errorf("tuya api error %d: %s", tuyaResp.Code, tuyaResp.Msg)
	}

	return nil, fmt.Errorf("failed to execute request to %s after retrying with a refreshed token", path)
}

// IoT is a trusted, device-addressed facade over a transport Client. Domain
// operations attach to it, organized per domain (device.go, and future
// home.go / space.go). It carries no tenant guard — ownership is the concern of
// tuya.Client, the single door an untrusted caller goes through. Client.Do is a
// raw escape hatch for endpoints not yet wrapped.
type IoT struct {
	client *Client
}

// NewIoT wraps a transport Client with IoT operations.
func NewIoT(c *Client) *IoT {
	return &IoT{client: c}
}
