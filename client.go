// Package tuya is a reusable library for the Tuya Cloud OpenAPI, built to be
// driven as a tool by an AI agent: low traffic, one action per user intent
// (list devices, read a device's state, send it a command).
//
// The library speaks Tuya at the app (project) level — a single access
// ID/secret yields an access token that the Client caches and refreshes on its
// own. It is not tied to one database or one application: the per-user mapping
// from an opaque owner ID to that human's Tuya account UID lives behind the
// Repository interface, with a ready-made PostgreSQL implementation in the
// postgres subpackage.
package tuya

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// ErrAccountNotLinked indicates the owner has no Tuya account linked, i.e. there
// is no owner-ID → Tuya-UID mapping. Repository implementations return this from
// Get/GetTuyaUID when no row exists, so consumers can route the human into the
// account-linking flow.
var ErrAccountNotLinked = errors.New("tuya: no tuya account linked to owner")

// ErrDeviceNotOwned indicates the targeted device does not belong to the owner's
// Tuya account. Returned by device commands before anything is sent, so an agent
// can never drive a device that isn't the human's.
var ErrDeviceNotOwned = errors.New("tuya: device does not belong to owner")

const (
	tokenExpiredTuyaErrorCode = 1010
	maxIoTRequestAttempts     = 2
)

// Repository resolves an opaque owner ID to that human's Tuya account. It is a
// consumer-defined interface (a ready-made PostgreSQL implementation lives in
// the postgres subpackage); the library never assumes what an owner ID means or
// where the mapping is stored.
type Repository interface {
	Get(ctx context.Context, ownerID string) (Account, error)
	GetTuyaUID(ctx context.Context, ownerID string) (string, error)
}

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
	store        Repository
	accessID     string
	accessSecret string
	baseURL      string
	httpClient   *http.Client
	token        *Token
	tokenLock    sync.RWMutex
}

// New builds a Client. store resolves owner IDs to Tuya UIDs; accessID and
// accessSecret are the Tuya Cloud project credentials; baseURL selects the
// regional data-center endpoint (e.g. https://openapi.tuyaus.com for the US,
// tuyaeu/tuyacn/tuyain for EU/China/India). New prefetches an access token so a
// bad credential or unreachable region fails here, at wiring time, not on the
// first device call.
func New(store Repository, accessID, accessSecret, baseURL string) (*Client, error) {
	if store == nil {
		return nil, errors.New("tuya: New: store must not be nil")
	}

	client := &Client{
		store:        store,
		accessID:     accessID,
		accessSecret: accessSecret,
		baseURL:      baseURL,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
		token:        &Token{},
		tokenLock:    sync.RWMutex{},
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
	for attempt := 0; attempt < maxIoTRequestAttempts; attempt++ {
		fullURL := c.baseURL + path

		var accessToken string
		c.tokenLock.RLock()
		if c.token != nil {
			accessToken = c.token.AccessToken
		}
		c.tokenLock.RUnlock()

		signature, err := generateSignature(c.accessID, c.accessSecret, accessToken, method, path, body)
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
		httpReq.Header.Set("client_id", c.accessID)
		httpReq.Header.Set("sign", signature.Sign)
		httpReq.Header.Set("t", signature.Timestamp)
		httpReq.Header.Set("sign_method", signature.SignMethod)
		httpReq.Header.Set("access_token", accessToken)
		httpReq.Header.Set("nonce", signature.Nonce)

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("request to %s failed: %w", fullURL, err)
		}
		defer resp.Body.Close()

		respBodyBytes, err := io.ReadAll(resp.Body)
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

func (c *Client) doTokenRequest(ctx context.Context, method, path string) (*response, error) {
	fullURL := c.baseURL + path

	signature, err := generateSignature(c.accessID, c.accessSecret, "", method, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to generate token signature: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create token request to %s: %w", fullURL, err)
	}

	httpReq.Header.Set("client_id", c.accessID)
	httpReq.Header.Set("sign", signature.Sign)
	httpReq.Header.Set("t", signature.Timestamp)
	httpReq.Header.Set("sign_method", signature.SignMethod)
	httpReq.Header.Set("access_token", "")
	httpReq.Header.Set("nonce", signature.Nonce)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("token request to %s failed: %w", fullURL, err)
	}
	defer resp.Body.Close()

	respBodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token response from %s: %w", fullURL, err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("token request to %s returned non-200 status code: %d, body: %s", fullURL, resp.StatusCode, string(respBodyBytes))
	}

	var tuyaResp response
	if err := json.Unmarshal(respBodyBytes, &tuyaResp); err != nil {
		return nil, fmt.Errorf("failed to decode token response from %s: %w", fullURL, err)
	}

	return &tuyaResp, nil
}

func (c *Client) updateToken(ctx context.Context) error {
	resp, err := c.getToken(ctx)
	if err != nil {
		return fmt.Errorf("failed to get token: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("Tuya token request failed with code %d: %s", resp.Code, resp.Msg)
	}

	var newToken Token
	if err := json.Unmarshal(resp.Result, &newToken); err != nil {
		return fmt.Errorf("failed to unmarshal token result: %w", err)
	}

	newToken.ExpireTime = time.Now().Unix() + newToken.ExpireTime

	c.token = &newToken
	return nil
}

func (c *Client) ensureValidToken(ctx context.Context) error {
	c.tokenLock.Lock()
	defer c.tokenLock.Unlock()

	if c.token != nil && c.token.ExpireTime > time.Now().Unix() {
		return nil
	}

	return c.updateToken(ctx)
}
