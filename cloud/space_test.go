package cloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
)

type spaceStub struct {
	mu       sync.Mutex
	result   string
	requests []recordedRequest
}

type recordedRequest struct {
	method   string
	path     string
	query    url.Values
	rawQuery string
	body     string
}

func (s *spaceStub) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if strings.HasPrefix(r.URL.Path, "/v1.0/token") {
		fmt.Fprint(w, `{"success":true,"t":1,"result":{"access_token":"tok","expire_time":7200,"uid":"uid-1"}}`)
		return
	}
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.requests = append(s.requests, recordedRequest{
		method:   r.Method,
		path:     r.URL.Path,
		query:    r.URL.Query(),
		rawQuery: r.URL.RawQuery,
		body:     string(body),
	})
	s.mu.Unlock()
	if s.result == "" {
		fmt.Fprint(w, `{"success":true,"t":1}`)
		return
	}
	fmt.Fprintf(w, `{"success":true,"t":1,"result":%s}`, s.result)
}

func (s *spaceStub) calls() []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedRequest(nil), s.requests...)
}

func newSpaceIoT(t *testing.T, result string) (*IoT, *spaceStub) {
	t.Helper()
	stub := &spaceStub{result: result}
	server := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(server.Close)
	client, err := New("access-id", "access-secret", server.URL, WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}
	return NewIoT(client), stub
}

func TestSpaceIDAcceptsNumberAndString(t *testing.T) {
	for _, raw := range []string{`{"id":150000001,"parent_id":"150000002"}`, `{"id":"150000001","parent_id":150000002}`} {
		iot, _ := newSpaceIoT(t, raw)
		space, err := iot.Space(context.Background(), 1)
		if err != nil {
			t.Fatalf("Space(%s): unexpected error: %v", raw, err)
		}
		if space.ID != 150000001 || space.ParentID != 150000002 {
			t.Errorf("Space(%s) = id %d, parent %d; want 150000001 and 150000002", raw, space.ID, space.ParentID)
		}
	}
}

func TestSpaceIDSurvivesBeyondFloat64Precision(t *testing.T) {
	const beyond2Pow53 = 9007199254740993
	iot, _ := newSpaceIoT(t, fmt.Sprintf(`{"id":%d}`, beyond2Pow53))
	space, err := iot.Space(context.Background(), 1)
	if err != nil {
		t.Fatalf("Space: unexpected error: %v", err)
	}
	if space.ID != beyond2Pow53 {
		t.Errorf("space id = %d, want %d — the id went through a float", space.ID, beyond2Pow53)
	}
}

func TestSpaceDecodesTheLiveFieldNames(t *testing.T) {
	iot, _ := newSpaceIoT(t, `{"id":1,"name":"Lobby","parent_id":2,"root_id":3}`)
	space, err := iot.Space(context.Background(), 1)
	if err != nil {
		t.Fatalf("Space: unexpected error: %v", err)
	}
	want := Space{ID: 1, Name: "Lobby", ParentID: 2, RootID: 3}
	if space != want {
		t.Errorf("Space = %+v, want %+v", space, want)
	}
}

func TestSpaceResourcesDecodesTheLiveFieldNames(t *testing.T) {
	iot, _ := newSpaceIoT(t, `{"last_row_key":2036356138623278,"data":[{"res_type":0,"res_id":"vdevo-1"}],"page_size":3}`)
	resources, page, err := iot.SpaceResources(context.Background(), 15, Subtree)
	if err != nil {
		t.Fatalf("SpaceResources: unexpected error: %v", err)
	}
	if len(resources) != 1 || resources[0].ID != "vdevo-1" || resources[0].Type != ResourceDevice {
		t.Errorf("resources = %+v, want one device vdevo-1", resources)
	}
	if page.LastRowKey != 2036356138623278 || page.PageSize != 3 {
		t.Errorf("page = %+v, want cursor 2036356138623278 and size 3", page)
	}
}

func TestTheLastPageComesBackWithoutACursor(t *testing.T) {
	iot, _ := newSpaceIoT(t, `{"data":[],"page_size":3}`)
	resources, page, err := iot.SpaceResources(context.Background(), 15, Subtree)
	if err != nil {
		t.Fatalf("SpaceResources: unexpected error: %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("resources = %+v, want none", resources)
	}
	if page.LastRowKey != 0 {
		t.Errorf("cursor = %d, want 0 so a walk can stop", page.LastRowKey)
	}
}

func TestListingSendsTheParametersTuyaBinds(t *testing.T) {
	iot, stub := newSpaceIoT(t, `{"data":[],"page_size":200}`)
	if _, _, err := iot.SpaceResources(context.Background(), 15, Subtree, WithPageSize(100), WithLastRowKey(5)); err != nil {
		t.Fatalf("SpaceResources: unexpected error: %v", err)
	}
	calls := stub.calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	for name, want := range map[string]string{"only_sub": "false", "page_size": "100", "last_row_key": "5"} {
		if got := calls[0].query.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	for _, ignored := range []string{"onlySub", "pageSize", "lastRowKey"} {
		if calls[0].query.Has(ignored) {
			t.Errorf("sent %s, which Tuya ignores", ignored)
		}
	}
	if got := calls[0].path; got != "/v2.0/cloud/space/15/resource" {
		t.Errorf("path = %q", got)
	}
}

func TestQueryParametersGoOutInASCIIOrder(t *testing.T) {
	iot, stub := newSpaceIoT(t, `{"data":[],"page_size":200}`)
	if _, _, err := iot.ChildSpaces(context.Background(), 15, Subtree, WithPageSize(100), WithLastRowKey(5)); err != nil {
		t.Fatalf("ChildSpaces: unexpected error: %v", err)
	}
	raw := stub.calls()[0].rawQuery
	if !sort.StringsAreSorted(strings.Split(raw, "&")) {
		t.Errorf("query %q is not in ASCII order", raw)
	}
}

func TestDirectChildrenNarrowsTheListing(t *testing.T) {
	iot, stub := newSpaceIoT(t, `{"data":[],"last_row_key":0,"page_size":200}`)
	if _, _, err := iot.ChildSpaces(context.Background(), 15, DirectChildren); err != nil {
		t.Fatalf("ChildSpaces: unexpected error: %v", err)
	}
	calls := stub.calls()
	if got := calls[0].query.Get("only_sub"); got != "true" {
		t.Errorf("only_sub = %q, want true for DirectChildren", got)
	}
	if got := calls[0].query.Get("space_id"); got != "15" {
		t.Errorf("space_id = %q, want 15", got)
	}
}

func TestListingRejectsAnUnsetScope(t *testing.T) {
	iot, stub := newSpaceIoT(t, `{"data":[]}`)
	if _, _, err := iot.SpaceResources(context.Background(), 15, Scope(0)); err == nil {
		t.Error("SpaceResources: got nil error for the zero Scope, want a rejection")
	}
	if _, _, err := iot.ChildSpaces(context.Background(), 15, Scope(0)); err == nil {
		t.Error("ChildSpaces: got nil error for the zero Scope, want a rejection")
	}
	if calls := stub.calls(); len(calls) != 0 {
		t.Errorf("made %d requests with an unset scope, want none", len(calls))
	}
}

func TestChildSpacesRejectsTheZeroSpaceID(t *testing.T) {
	iot, stub := newSpaceIoT(t, `{"data":[]}`)
	if _, _, err := iot.ChildSpaces(context.Background(), 0, DirectChildren); err == nil {
		t.Error("ChildSpaces(0): got nil error, want a rejection")
	}
	if calls := stub.calls(); len(calls) != 0 {
		t.Errorf("made %d requests, want none", len(calls))
	}
	if _, _, err := iot.RootSpaces(context.Background(), DirectChildren); err != nil {
		t.Fatalf("RootSpaces: unexpected error: %v", err)
	}
	calls := stub.calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	if calls[0].query.Has("space_id") || calls[0].query.Has("spaceId") {
		t.Errorf("RootSpaces sent a space id: %v", calls[0].query)
	}
}

func TestChildSpacesDecodesIDList(t *testing.T) {
	iot, _ := newSpaceIoT(t, `{"last_row_key":15000003,"data":[15000002,"15000003"],"page_size":200}`)
	ids, page, err := iot.ChildSpaces(context.Background(), 15, DirectChildren)
	if err != nil {
		t.Fatalf("ChildSpaces: unexpected error: %v", err)
	}
	if len(ids) != 2 || ids[0] != 15000002 || ids[1] != 15000003 {
		t.Errorf("ids = %v, want [15000002 15000003]", ids)
	}
	if page.LastRowKey != 15000003 {
		t.Errorf("cursor = %d, want 15000003", page.LastRowKey)
	}
}

func TestAMissingSpaceIsReportedAsSuch(t *testing.T) {
	for _, result := range []string{``, `null`} {
		iot, _ := newSpaceIoT(t, result)
		_, err := iot.Space(context.Background(), 15)
		if !errors.Is(err, ErrSpaceNotFound) {
			t.Errorf("Space with result %q: error = %v, want ErrSpaceNotFound", result, err)
		}
	}
}

func TestAMissingResultIsNotAConfirmation(t *testing.T) {
	iot, _ := newSpaceIoT(t, `null`)
	if err := iot.DeleteSpace(context.Background(), 15); !errors.Is(err, ErrNotApplied) {
		t.Errorf("DeleteSpace error = %v, want ErrNotApplied", err)
	}
	if err := iot.ModifySpace(context.Background(), 15, "Lobby", ""); !errors.Is(err, ErrNotApplied) {
		t.Errorf("ModifySpace error = %v, want ErrNotApplied", err)
	}
}

func TestModifyAndDeleteRejectResultFalse(t *testing.T) {
	iot, _ := newSpaceIoT(t, `false`)
	if err := iot.ModifySpace(context.Background(), 15, "Lobby", ""); !errors.Is(err, ErrNotApplied) {
		t.Errorf("ModifySpace error = %v, want ErrNotApplied", err)
	}
	if err := iot.DeleteSpace(context.Background(), 15); !errors.Is(err, ErrNotApplied) {
		t.Errorf("DeleteSpace error = %v, want ErrNotApplied", err)
	}
}

func TestModifyAndDeleteAcceptResultTrue(t *testing.T) {
	iot, stub := newSpaceIoT(t, `true`)
	if err := iot.ModifySpace(context.Background(), 15, "Lobby", "front desk"); err != nil {
		t.Fatalf("ModifySpace: unexpected error: %v", err)
	}
	if err := iot.DeleteSpace(context.Background(), 15); err != nil {
		t.Fatalf("DeleteSpace: unexpected error: %v", err)
	}
	calls := stub.calls()
	if calls[0].method != http.MethodPut || calls[0].path != "/v2.0/cloud/space/15" {
		t.Errorf("modify sent %s %s", calls[0].method, calls[0].path)
	}
	if calls[0].body != `{"name":"Lobby","description":"front desk"}` {
		t.Errorf("modify body = %s", calls[0].body)
	}
	if calls[1].method != http.MethodDelete || calls[1].path != "/v2.0/cloud/space/15" {
		t.Errorf("delete sent %s %s", calls[1].method, calls[1].path)
	}
}

func TestCreateSpaceOmitsTheZeroParent(t *testing.T) {
	iot, stub := newSpaceIoT(t, `150000001`)
	id, err := iot.CreateSpace(context.Background(), "Hotel", 0, "")
	if err != nil {
		t.Fatalf("CreateSpace: unexpected error: %v", err)
	}
	if id != 150000001 {
		t.Errorf("id = %d, want 150000001", id)
	}
	if _, err := iot.CreateSpace(context.Background(), "Room 1", 150000001, "twin"); err != nil {
		t.Fatalf("CreateSpace: unexpected error: %v", err)
	}
	calls := stub.calls()
	if calls[0].body != `{"name":"Hotel"}` {
		t.Errorf("first body = %s, want no parent_id and no description", calls[0].body)
	}
	if calls[1].body != `{"name":"Room 1","parent_id":150000001,"description":"twin"}` {
		t.Errorf("second body = %s", calls[1].body)
	}
}

func TestSpaceContainsReportsFalseAsData(t *testing.T) {
	iot, stub := newSpaceIoT(t, `false`)
	contains, err := iot.SpaceContains(context.Background(), 15, 16)
	if err != nil {
		t.Fatalf("SpaceContains: unexpected error: %v", err)
	}
	if contains {
		t.Error("SpaceContains = true, want false")
	}
	calls := stub.calls()
	if calls[0].path != "/v2.0/cloud/space/relation" {
		t.Errorf("path = %q", calls[0].path)
	}
	if calls[0].query.Get("parent_id") != "15" || calls[0].query.Get("child_id") != "16" {
		t.Errorf("query = %v, want parent_id 15 and child_id 16", calls[0].query)
	}
}
