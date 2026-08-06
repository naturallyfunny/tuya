package cloud

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// tuyaStub stands in for a Tuya data center over a real HTTP server: it issues
// tokens and records the access_token each business call arrived with, so a test
// can tell whether a retry reused a token or fetched a new one.
type tuyaStub struct {
	mu sync.Mutex

	// tokensIssued counts calls to the token endpoint. New always causes one.
	tokensIssued int
	// businessTokens is the access_token header of every non-token request.
	businessTokens []string
	// rejectFirstCall makes the first business call answer code 1010, the way
	// Tuya reports a token it no longer accepts.
	rejectFirstCall bool
}

func (s *tuyaStub) handler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")

	if strings.HasPrefix(r.URL.Path, "/v1.0/token") {
		s.tokensIssued++
		// expire_time is a duration in seconds; 7200 puts the cached expiry far
		// enough in the future that a clock-based check would call it valid.
		fmt.Fprintf(w, `{"success":true,"t":1,"result":{"access_token":"tok-%d","expire_time":7200,"uid":"uid-1"}}`, s.tokensIssued)
		return
	}

	s.businessTokens = append(s.businessTokens, r.Header.Get("access_token"))
	if s.rejectFirstCall && len(s.businessTokens) == 1 {
		fmt.Fprint(w, `{"success":false,"code":1010,"msg":"token invalid"}`)
		return
	}
	fmt.Fprint(w, `{"success":true,"t":1,"result":{"ok":true}}`)
}

func newStubbedClient(t *testing.T, stub *tuyaStub) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(server.Close)

	client, err := New("access-id", "access-secret", server.URL, WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}
	return client
}

// Tuya rejecting a token it issued must force a refresh, even though the cached
// expiry still looks valid. Consulting that expiry would replay the very token
// Tuya just refused, burning a second request to fail the same way.
func TestDoRefreshesOnCode1010DespiteUnexpiredCachedToken(t *testing.T) {
	stub := &tuyaStub{rejectFirstCall: true}
	client := newStubbedClient(t, stub)

	if _, err := client.Do(context.Background(), http.MethodGet, "/v1.0/devices/dev-1/status", nil); err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}

	stub.mu.Lock()
	defer stub.mu.Unlock()

	if stub.tokensIssued != 2 {
		t.Errorf("token endpoint hit %d times, want 2 (one at New, one forced by code 1010)", stub.tokensIssued)
	}
	if len(stub.businessTokens) != 2 {
		t.Fatalf("business calls = %d, want 2 (the rejected one and its retry)", len(stub.businessTokens))
	}
	if stub.businessTokens[0] == stub.businessTokens[1] {
		t.Errorf("retry reused token %q; want the refreshed one", stub.businessTokens[1])
	}
}

// The happy path must not refresh: a valid cached token is reused, and New's
// prefetch stays the only token request.
func TestDoReusesCachedToken(t *testing.T) {
	stub := &tuyaStub{}
	client := newStubbedClient(t, stub)

	for range 2 {
		if _, err := client.Do(context.Background(), http.MethodGet, "/v1.0/devices/dev-1/status", nil); err != nil {
			t.Fatalf("Do: unexpected error: %v", err)
		}
	}

	stub.mu.Lock()
	defer stub.mu.Unlock()

	if stub.tokensIssued != 1 {
		t.Errorf("token endpoint hit %d times, want 1 (New's prefetch only)", stub.tokensIssued)
	}
	if len(stub.businessTokens) != 2 || stub.businessTokens[0] != stub.businessTokens[1] {
		t.Errorf("business calls used %v, want the same token twice", stub.businessTokens)
	}
}

// A second 1010, after the refresh, is a real failure and must surface as the
// Tuya error rather than looping.
func TestDoStopsAfterOneRefresh(t *testing.T) {
	// alwaysReject: every business call answers 1010.
	stub := &tuyaStub{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.mu.Lock()
		defer stub.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/v1.0/token") {
			stub.tokensIssued++
			fmt.Fprintf(w, `{"success":true,"t":1,"result":{"access_token":"tok-%d","expire_time":7200,"uid":"uid-1"}}`, stub.tokensIssued)
			return
		}
		stub.businessTokens = append(stub.businessTokens, r.Header.Get("access_token"))
		fmt.Fprint(w, `{"success":false,"code":1010,"msg":"token invalid"}`)
	}))
	t.Cleanup(server.Close)

	client, err := New("access-id", "access-secret", server.URL, WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}

	_, err = client.Do(context.Background(), http.MethodGet, "/v1.0/devices/dev-1/status", nil)
	if err == nil {
		t.Fatal("Do: got nil error, want the Tuya 1010 failure")
	}
	if !strings.Contains(err.Error(), "1010") {
		t.Errorf("error %q does not report the Tuya code", err)
	}

	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.businessTokens) != 2 {
		t.Errorf("business calls = %d, want exactly 2 (no retry loop)", len(stub.businessTokens))
	}
}
