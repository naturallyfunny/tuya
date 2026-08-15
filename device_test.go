package tuya

import (
	"context"
	"strconv"
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
