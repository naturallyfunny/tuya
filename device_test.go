package tuya

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestSpaceDevicesAlwaysSendsAPageSize(t *testing.T) {
	client, stub := newSpaceClient(t, `[]`)
	if _, err := client.SpaceDevices(context.Background(), []int64{1}, true, nil, nil, "", 0); err != nil {
		t.Fatalf("SpaceDevices: unexpected error: %v", err)
	}
	calls := stub.calls()
	if len(calls) != 1 {
		t.Fatalf("got %d requests, want 1", len(calls))
	}
	got := calls[0].query.Get("page_size")
	if got != strconv.Itoa(SpaceDevicePageSizeMax) {
		t.Errorf("page_size = %q, want %d: Tuya answers 1110 when the parameter is missing", got, SpaceDevicePageSizeMax)
	}
}

func TestSpaceDevicesKeepsTheRequestedPageSize(t *testing.T) {
	client, stub := newSpaceClient(t, `[]`)
	if _, err := client.SpaceDevices(context.Background(), []int64{1}, true, nil, nil, "", 5); err != nil {
		t.Fatalf("SpaceDevices: unexpected error: %v", err)
	}
	if got := stub.calls()[0].query.Get("page_size"); got != "5" {
		t.Errorf("page_size = %q, want 5", got)
	}
}

func TestChannelNamesReportsEveryFailingDevice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1.0/token"):
			fmt.Fprint(w, `{"success":true,"t":1,"result":{"access_token":"tok","expire_time":7200}}`)
		case strings.Contains(r.URL.Path, "/ok-1/"):
			fmt.Fprint(w, `{"success":true,"t":1,"result":[{"identifier":"switch_1","name":"Kitchen"}]}`)
		default:
			fmt.Fprint(w, `{"success":false,"t":1,"code":1106,"msg":"permission deny"}`)
		}
	}))
	t.Cleanup(server.Close)
	client, err := New("access-id", "access-secret", server.URL, WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}

	named, err := client.ChannelNames(context.Background(), []Device{
		{ID: "ok-1", Category: "kg"},
		{ID: "bad-1", Category: "kg"},
		{ID: "bad-2", Category: "cz"},
	})
	if err == nil {
		t.Fatal("ChannelNames: got nil error, want both failures")
	}
	for _, deviceID := range []string{"bad-1", "bad-2"} {
		if !strings.Contains(err.Error(), deviceID) {
			t.Errorf("error %q does not name %s — a failure stopped the fan-out early", err, deviceID)
		}
	}
	if len(named["ok-1"]) != 1 || named["ok-1"][0].Name != "Kitchen" {
		t.Errorf("ok-1 = %+v, want its channel kept despite the other failures", named["ok-1"])
	}
}

func TestChannelNamesOnlyAsksAboutMultiChannelDevices(t *testing.T) {
	stub := &spaceStub{result: `[{"identifier":"switch_1","name":"Kitchen"}]`}
	server := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(server.Close)
	client, err := New("access-id", "access-secret", server.URL, WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}

	named, err := client.ChannelNames(context.Background(), []Device{
		{ID: "switch-1", Category: "KG"},
		{ID: "curtain-1", Category: "clkg"},
		{ID: "sensor-1", Category: "wsdcg"},
		{ID: "lock-1", Category: "ms"},
		{ID: "", Category: "kg"},
	})
	if err != nil {
		t.Fatalf("ChannelNames: unexpected error: %v", err)
	}
	if len(named) != 2 || len(named["switch-1"]) != 1 || len(named["curtain-1"]) != 1 {
		t.Errorf("named = %+v, want switch-1 and curtain-1", named)
	}
	var asked []string
	for _, call := range stub.calls() {
		asked = append(asked, call.path)
	}
	if len(asked) != 2 {
		t.Fatalf("asked %v, want 2 requests: a single-channel device costs one", asked)
	}
	for _, deviceID := range []string{"switch-1", "curtain-1"} {
		if !strings.Contains(strings.Join(asked, " "), deviceID) {
			t.Errorf("asked %v, want %s among them", asked, deviceID)
		}
	}
}

func TestUserHasDeviceAbsentIsFalseNotError(t *testing.T) {
	client, stub := newSpaceClient(t, `[{"id":"someone-elses-device"}]`)
	ok, err := client.UserHasDevice(context.Background(), "uid-1", "dev-1")
	if err != nil {
		t.Fatalf("UserHasDevice: got error %v, want a plain false", err)
	}
	if ok {
		t.Error("UserHasDevice: got true for a device the account does not list")
	}
	if len(stub.calls()) != 1 {
		t.Errorf("got %d requests, want 1: the answer is list-then-contains", len(stub.calls()))
	}
}

func TestUserHasDevicePresent(t *testing.T) {
	client, _ := newSpaceClient(t, `[{"id":"dev-1"}]`)
	ok, err := client.UserHasDevice(context.Background(), "uid-1", "dev-1")
	if err != nil {
		t.Fatalf("UserHasDevice: unexpected error: %v", err)
	}
	if !ok {
		t.Error("UserHasDevice: got false for a device the account lists")
	}
}
