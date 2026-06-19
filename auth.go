package tuya

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// setAuthHeaders sets all Tuya authentication headers on req from sig. Shared by
// both the token and business request flows.
func setAuthHeaders(req *http.Request, accessID string, sig *signature) {
	req.Header.Set("client_id", accessID)
	req.Header.Set("sign", sig.Sign)
	req.Header.Set("t", sig.Timestamp)
	req.Header.Set("sign_method", sig.SignMethod)
	req.Header.Set("access_token", sig.AccessToken)
	req.Header.Set("nonce", sig.Nonce)
}

// --- Request signing ---

type signature struct {
	Sign        string
	Timestamp   string
	Nonce       string
	SignMethod  string
	AccessToken string
}

// hmacSign is the shared HMAC-SHA256 core. It builds the Tuya tuyaStr
// (accessID + accessToken + timestamp + nonce + stringToSign) and returns a
// fully populated signature. accessToken is "" for token requests.
func hmacSign(accessID, accessSecret, accessToken, method, path string, body []byte) (*signature, error) {
	timestamp := strconv.FormatInt(time.Now().UnixNano()/int64(time.Millisecond), 10)

	hash := sha256.New()
	hash.Write(body)
	contentSha256 := hex.EncodeToString(hash.Sum(nil))

	stringToSign := method + "\n" + contentSha256 + "\n\n" + path

	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}
	nonce := hex.EncodeToString(nonceBytes)

	tuyaStr := accessID + accessToken + timestamp + nonce + stringToSign

	mac := hmac.New(sha256.New, []byte(accessSecret))
	mac.Write([]byte(tuyaStr))
	sign := strings.ToUpper(hex.EncodeToString(mac.Sum(nil)))

	return &signature{
		Sign:        sign,
		Timestamp:   timestamp,
		Nonce:       nonce,
		SignMethod:  "HMAC-SHA256",
		AccessToken: accessToken,
	}, nil
}

// signTokenRequest signs a token-acquisition request. The access token is
// intentionally absent from the string-to-sign, as Tuya specifies for
// grant_type=1 calls. Must NOT read c.token or c.tokenLock — this is called
// from fetchToken while the write lock is already held.
func (c *Client) signTokenRequest(method, path string, body []byte) (*signature, error) {
	return hmacSign(c.accessID, c.accessSecret, "", method, path, body)
}

// signBusinessRequest signs a normal (authenticated) API request. It reads the
// current access token under RLock and embeds it in both the HMAC string and
// the returned AccessToken field, ensuring the header and the signature always
// use the same value.
func (c *Client) signBusinessRequest(method, path string, body []byte) (*signature, error) {
	var accessToken string
	c.tokenLock.RLock()
	if c.token != nil {
		accessToken = c.token.AccessToken
	}
	c.tokenLock.RUnlock()
	return hmacSign(c.accessID, c.accessSecret, accessToken, method, path, body)
}

// --- Token lifecycle ---

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

	sig, err := c.signTokenRequest(http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to generate token signature: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create token request to %s: %w", fullURL, err)
	}

	setAuthHeaders(httpReq, c.accessID, sig)

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
	resp, err := c.fetchToken(ctx)
	if err != nil {
		return fmt.Errorf("failed to get token: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("Tuya token request failed with code %d: %s", resp.Code, resp.Msg)
	}

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
