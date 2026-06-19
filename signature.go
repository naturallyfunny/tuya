package tuya

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type signature struct {
	Sign       string
	Timestamp  string
	Nonce      string
	SignMethod string
}

// generateSignature computes the Tuya Cloud OpenAPI request signature. The
// string-to-sign is method + content-SHA256 + (empty headers) + path, prefixed
// with accessID + accessToken + timestamp + nonce and HMAC'd with the secret.
func sign(accessID, accessSecret, accessToken, method, path string, body []byte) (*signature, error) {
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
		Sign:       sign,
		Timestamp:  timestamp,
		Nonce:      nonce,
		SignMethod: "HMAC-SHA256",
	}, nil
}
