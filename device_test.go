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
	if _, err := client.SpaceDevices(context.Background(), []int64{1}, 0, true, nil, nil, ""); err != nil {
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
	if _, err := client.SpaceDevices(context.Background(), []int64{1}, 5, true, nil, nil, ""); err != nil {
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

func TestSpaceHasDevicePresent(t *testing.T) {
	client, stub := newSpaceClient(t, `{"data":[{"res_id":"dev-1","res_type":0}]}`)
	ok, err := client.SpaceHasDevice(context.Background(), 42, "dev-1")
	if err != nil {
		t.Fatalf("SpaceHasDevice: unexpected error: %v", err)
	}
	if !ok {
		t.Error("SpaceHasDevice: got false for a device the space reports")
	}
	if len(stub.calls()) != 1 {
		t.Errorf("got %d requests, want 1: the device is on the first page", len(stub.calls()))
	}
}

func TestSpaceHasDeviceAbsentIsFalseNotError(t *testing.T) {
	client, _ := newSpaceClient(t, `{"data":[{"res_id":"someone-elses-device","res_type":0}]}`)
	ok, err := client.SpaceHasDevice(context.Background(), 42, "dev-1")
	if err != nil {
		t.Fatalf("SpaceHasDevice: got error %v, want a plain false", err)
	}
	if ok {
		t.Error("SpaceHasDevice: got true for a device the space does not report")
	}
}

func TestSpaceHasDeviceGivesUpRatherThanPageForever(t *testing.T) {
	var pages int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/v1.0/token") {
			fmt.Fprint(w, `{"success":true,"t":1,"result":{"access_token":"tok","expire_time":7200}}`)
			return
		}
		pages++
		fmt.Fprintf(w, `{"success":true,"t":1,"result":{"data":[{"res_id":"dev-other","res_type":0}],"last_row_key":%d}}`, pages)
	}))
	t.Cleanup(server.Close)
	client, err := New("access-id", "access-secret", server.URL, WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}

	ok, err := client.SpaceHasDevice(context.Background(), 42, "dev-absent")
	if err == nil {
		t.Fatal("SpaceHasDevice: got nil error, want the scan to give up")
	}
	if ok {
		t.Error("SpaceHasDevice: got true alongside an error")
	}
	if pages != deviceScanMaxPages {
		t.Errorf("read %d pages, want the scan capped at %d", pages, deviceScanMaxPages)
	}
}

func TestDevicePropertiesDecodesTheLiveFieldNames(t *testing.T) {
	client, _ := newSpaceClient(t, `{"properties":[{"code":"switch_on","custom_name":"cole_red","name":"Switch","type":"bool","dp_id":1,"time":1786177926576,"value":true}]}`)
	properties, err := client.DeviceProperties(context.Background(), "dev-1", nil)
	if err != nil {
		t.Fatalf("DeviceProperties: unexpected error: %v", err)
	}
	if len(properties) != 1 {
		t.Fatalf("got %d properties, want 1", len(properties))
	}
	want := Property{Code: "switch_on", Value: true, Type: "bool", Name: "Switch", CustomName: "cole_red", DPID: 1, Time: 1786177926576}
	if properties[0] != want {
		t.Errorf("property = %+v, want %+v", properties[0], want)
	}
}

func TestDevicePropertiesSeparatesCodesWithAnUnescapedComma(t *testing.T) {
	client, stub := newSpaceClient(t, `{"properties":[]}`)
	if _, err := client.DeviceProperties(context.Background(), "dev-1", []string{"switch_on", "countdown"}); err != nil {
		t.Fatalf("DeviceProperties: unexpected error: %v", err)
	}
	calls := stub.calls()
	if len(calls) != 1 {
		t.Fatalf("got %d requests, want 1", len(calls))
	}
	if got := calls[0].rawQuery; got != "codes=switch_on,countdown" {
		t.Errorf("query = %q, want the comma unescaped: Tuya answers 1004 sign invalid on %%2C", got)
	}
}

func TestDevicePropertiesWithoutCodesAsksForEveryProperty(t *testing.T) {
	client, stub := newSpaceClient(t, `{"properties":[]}`)
	if _, err := client.DeviceProperties(context.Background(), "dev-1", nil); err != nil {
		t.Fatalf("DeviceProperties: unexpected error: %v", err)
	}
	if got := stub.calls()[0].rawQuery; got != "codes=" {
		t.Errorf("query = %q, want an empty codes=: Tuya reads that as no filter", got)
	}
}

func TestSpaceDevicesSeparatesIDsWithAnUnescapedComma(t *testing.T) {
	client, stub := newSpaceClient(t, `[]`)
	if _, err := client.SpaceDevices(context.Background(), []int64{1, 2}, 0, true, []string{"p1", "p2"}, []string{"kg", "cz"}, ""); err != nil {
		t.Fatalf("SpaceDevices: unexpected error: %v", err)
	}
	if got := stub.calls()[0].rawQuery; strings.Contains(got, "%2C") {
		t.Errorf("query = %q, want the commas unescaped: Tuya answers 1004 sign invalid on %%2C", got)
	}
}

func TestUserDevicesCostsOneRequestWithoutTheOption(t *testing.T) {
	client, stub := newSpaceClient(t, `[{"id":"switch-1","category":"kg"}]`)
	devices, err := client.UserDevices(context.Background(), "uid-1")
	if err != nil {
		t.Fatalf("UserDevices: unexpected error: %v", err)
	}
	if devices[0].Channels != nil {
		t.Errorf("Channels = %+v, want nothing: the caller never asked", devices[0].Channels)
	}
	if len(stub.calls()) != 1 {
		t.Errorf("got %d requests, want 1", len(stub.calls()))
	}
}

func TestUserDevicesWithChannelNames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1.0/token"):
			fmt.Fprint(w, `{"success":true,"t":1,"result":{"access_token":"tok","expire_time":7200}}`)
		case strings.HasSuffix(r.URL.Path, "/multiple-names"):
			fmt.Fprint(w, `{"success":true,"t":1,"result":[{"identifier":"switch_1","name":"Kitchen"}]}`)
		default:
			fmt.Fprint(w, `{"success":true,"t":1,"result":[{"id":"switch-1","category":"kg"},{"id":"sensor-1","category":"wsdcg"}]}`)
		}
	}))
	t.Cleanup(server.Close)
	client, err := New("access-id", "access-secret", server.URL, WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}

	devices, err := client.UserDevices(context.Background(), "uid-1", WithChannelNames())
	if err != nil {
		t.Fatalf("UserDevices: unexpected error: %v", err)
	}
	byID := map[string][]Channel{}
	for _, device := range devices {
		byID[device.ID] = device.Channels
	}
	if len(byID["switch-1"]) != 1 || byID["switch-1"][0].Name != "Kitchen" {
		t.Errorf("switch-1 channels = %+v, want Kitchen", byID["switch-1"])
	}
	if byID["sensor-1"] != nil {
		t.Errorf("sensor-1 channels = %+v, want nothing: it cannot carry channels", byID["sensor-1"])
	}
}
