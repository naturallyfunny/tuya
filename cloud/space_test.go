package cloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

type spaceStub struct {
	mu sync.Mutex

	result   string
	requests []recordedRequest
}

type recordedRequest struct {
	method string
	path   string
	query  url.Values
	body   string
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
		method: r.Method,
		path:   r.URL.Path,
		query:  r.URL.Query(),
		body:   string(body),
	})
	s.mu.Unlock()

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
	// Tuya answers creation with a number and lookup with a string.
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

func TestSpaceDecodesEitherFieldCasing(t *testing.T) {
	cases := map[string]string{
		"snake": `{"id":"1","name":"Lobby","parent_id":"2","root_id":"3"}`,
		"camel": `{"id":"1","name":"Lobby","parentId":"2","rootId":"3"}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			iot, _ := newSpaceIoT(t, raw)
			space, err := iot.Space(context.Background(), 1)
			if err != nil {
				t.Fatalf("Space: unexpected error: %v", err)
			}
			want := Space{ID: 1, Name: "Lobby", ParentID: 2, RootID: 3}
			if space != want {
				t.Errorf("Space = %+v, want %+v", space, want)
			}
		})
	}
}

func TestSpaceResourcesDecodesEitherFieldCasing(t *testing.T) {
	cases := map[string]string{
		"camel data, snake wrapper": `{"last_row_key":7,"data":[{"resType":0,"resId":"vdevo-1"}],"page_size":200}`,
		"snake data, camel wrapper": `{"lastRowKey":7,"data":[{"res_type":0,"res_id":"vdevo-1"}],"pageSize":200}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			iot, _ := newSpaceIoT(t, raw)
			resources, page, err := iot.SpaceResources(context.Background(), 15, Subtree)
			if err != nil {
				t.Fatalf("SpaceResources: unexpected error: %v", err)
			}
			if len(resources) != 1 || resources[0].ID != "vdevo-1" || resources[0].Type != ResourceDevice {
				t.Errorf("resources = %+v, want one device vdevo-1", resources)
			}
			if page.LastRowKey != 7 || page.PageSize != 200 {
				t.Errorf("page = %+v, want cursor 7 and size 200", page)
			}
		})
	}
}

func TestListingSendsBothParameterSpellings(t *testing.T) {
	iot, stub := newSpaceIoT(t, `{"data":[],"last_row_key":0,"page_size":200}`)

	if _, _, err := iot.SpaceResources(context.Background(), 15, Subtree, WithPageSize(100), WithLastRowKey(5)); err != nil {
		t.Fatalf("SpaceResources: unexpected error: %v", err)
	}

	calls := stub.calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	// Tuya's reference tables and examples disagree on the spelling; whichever
	// one it binds must carry the value the caller asked for.
	for _, pair := range [][2]string{
		{"only_sub", "onlySub"},
		{"page_size", "pageSize"},
		{"last_row_key", "lastRowKey"},
	} {
		snake, camel := calls[0].query.Get(pair[0]), calls[0].query.Get(pair[1])
		if snake == "" || camel == "" || snake != camel {
			t.Errorf("query has %s=%q and %s=%q; want both spellings with one value", pair[0], snake, pair[1], camel)
		}
	}
	if got := calls[0].query.Get("only_sub"); got != "false" {
		t.Errorf("only_sub = %q, want false for Subtree", got)
	}
	if got := calls[0].path; got != "/v2.0/cloud/space/15/resource" {
		t.Errorf("path = %q", got)
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
	for _, spelling := range []string{"space_id", "spaceId"} {
		if got := calls[0].query.Get(spelling); got != "15" {
			t.Errorf("%s = %q, want 15", spelling, got)
		}
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

	// Zero would make Tuya list the whole project; only RootSpaces asks for that.
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

func TestModifyAndDeleteRejectResultFalse(t *testing.T) {
	// success:true with result:false is Tuya saying "accepted, not applied".
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
	// A Long, never a quoted string: Tuya's own field is numeric here.
	if calls[1].body != `{"name":"Room 1","parent_id":150000001,"description":"twin"}` {
		t.Errorf("second body = %s", calls[1].body)
	}
}

func TestSpaceContainsReportsFalseAsData(t *testing.T) {
	// Here the boolean is the answer, not a status: false must not be an error.
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
