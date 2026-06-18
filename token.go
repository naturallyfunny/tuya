package tuya

import (
	"context"
	"fmt"
	"net/http"
)

const tokenEndpoint = "/v1.0/token"

// Token is an app-level Tuya access token. ExpireTime is normalized to an
// absolute Unix timestamp once stored (Tuya returns it as a duration in
// seconds).
type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpireTime   int64  `json:"expire_time"`
	UID          string `json:"uid"`
}

func (c *Client) getToken(ctx context.Context) (*response, error) {
	path := fmt.Sprintf("%s?grant_type=1", tokenEndpoint)
	return c.doTokenRequest(ctx, http.MethodGet, path)
}
