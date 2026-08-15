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

type signature struct {
	Sign        string
	Timestamp   string
	Nonce       string
	SignMethod  string
	AccessToken string
}

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

func (c *Client) setAuthHeaders(req *http.Request, sig *signature) {
	req.Header.Set("client_id", c.accessID)
	req.Header.Set("sign", sig.Sign)
	req.Header.Set("t", sig.Timestamp)
	req.Header.Set("sign_method", sig.SignMethod)
	req.Header.Set("access_token", sig.AccessToken)
	req.Header.Set("nonce", sig.Nonce)
}

type token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpireTime   int64  `json:"expire_time"`
	UID          string `json:"uid"`
}

func (c *Client) fetchToken(ctx context.Context) (*response, error) {
	const path = "/v1.0/token?grant_type=1"
	fullURL := c.baseURL + path
	sig, err := hmacSign(c.accessID, c.accessSecret, "", http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to generate token signature: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create token request to %s: %w", fullURL, err)
	}
	c.setAuthHeaders(httpReq, sig)
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
		return fmt.Errorf("tuya token request failed with code %d: %s", resp.Code, resp.Msg)
	}
	var newToken token
	if err := json.Unmarshal(resp.Result, &newToken); err != nil {
		return fmt.Errorf("failed to unmarshal token result: %w", err)
	}
	newToken.ExpireTime = time.Now().Unix() + newToken.ExpireTime
	c.token = &newToken
	return nil
}
