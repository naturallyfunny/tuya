package tuya

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type tuyaStub struct {
	mu              sync.Mutex
	tokensIssued    int
	businessTokens  []string
	rejectFirstCall bool
}

func (s *tuyaStub) handler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if strings.HasPrefix(r.URL.Path, "/v1.0/token") {
		s.tokensIssued++
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

func TestDoStopsAfterOneRefresh(t *testing.T) {
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
