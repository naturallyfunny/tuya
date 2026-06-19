package tuya

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// token is an app-level Tuya access token. ExpireTime is normalized to an
// absolute Unix timestamp once stored (Tuya returns it as a duration in
// seconds).
type token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpireTime   int64  `json:"expire_time"`
	UID          string `json:"uid"`
}

func (c *Client) fetchToken(ctx context.Context) (*response, error) {
	const path = "/v1.0/token?grant_type=1"
	fullURL := c.baseURL + path

	signature, err := sign(c.accessID, c.accessSecret, "", http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to generate token signature: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
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
		return nil, fmt.Errorf("failed to decode token response from %s: %w", fullURL, err) }

	return &tuyaResp, nil
}

func (c *Client) updateToken(ctx context.Context) error {
	resp, err := c.fetchToken(ctx)
	if err != nil {
		return fmt.Errorf("failed to get token: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("Tuya token request failed with code %d: %s", resp.Code, resp.Msg) }

	var newToken token
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

