// Package tuya is a signed client for the Tuya Cloud OpenAPI, for anyone with a
// Tuya cloud project. Client handles token lifecycle, HMAC-SHA256 signing and
// retries; every other method on it is one-to-one with a Tuya device or space
// endpoint. Nothing here is a capability Tuya does not have natively.
//
// Package appaccount builds on this one for applications that carry their own
// user identity; postgres and firestore store what it needs.
package tuya

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
)

type Client struct {
	accessID     string
	accessSecret string
	baseURL      string
	httpClient   *http.Client
	accessToken  string
	tokenLock    sync.RWMutex
}

type Option func(*Client)

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
	if err := client.refreshToken(context.Background()); err != nil {
		return nil, fmt.Errorf("tuya: New: prefetch token: %w", err)
	}
	return client, nil
}

func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

type APIError struct {
	Code int
	Msg  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("tuya api error %d: %s", e.Code, e.Msg)
}

const CodeNoSpacePermission = 40001900

type response struct {
	Success bool            `json:"success"`
	T       int64           `json:"t"`
	Tid     string          `json:"tid"`
	Result  json.RawMessage `json:"result"`
	Code    int             `json:"code"`
	Msg     string          `json:"msg"`
}

func (c *Client) refreshToken(ctx context.Context) error {
	c.tokenLock.Lock()
	defer c.tokenLock.Unlock()
	return c.updateToken(ctx)
}

func (c *Client) signBusinessRequest(method, path string, body []byte) (*signature, error) {
	c.tokenLock.RLock()
	accessToken := c.accessToken
	c.tokenLock.RUnlock()
	return hmacSign(c.accessID, c.accessSecret, accessToken, method, path, body)
}

func (c *Client) Do(ctx context.Context, method, path string, body []byte) (json.RawMessage, error) {
	const maxIoTRequestAttempts = 2
	for attempt := range maxIoTRequestAttempts {
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
		c.setAuthHeaders(httpReq, sig)
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
			if err := c.refreshToken(ctx); err != nil {
				return nil, fmt.Errorf("failed to refresh token after Tuya error %d: %w", tuyaResp.Code, err)
			}
			continue
		}
		return nil, &APIError{Code: tuyaResp.Code, Msg: tuyaResp.Msg}
	}
	return nil, fmt.Errorf("failed to execute request to %s after retrying with a refreshed token", path)
}
