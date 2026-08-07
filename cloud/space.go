package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ErrNotApplied reports that Tuya accepted a request but answered result:false.
// success:true alone does not mean the operation took effect.
var ErrNotApplied = errors.New("tuya: operation was not applied")

// ErrSpaceNotFound reports that Tuya has no such space. It does not say so:
// asking for a deleted space answers success:true with no result at all.
var ErrSpaceNotFound = errors.New("tuya: space not found")

// SpaceID is a Tuya space identifier. Live responses carry it as a JSON number,
// but Tuya's reference shows it quoted, so this accepts either and always
// renders as a number. It is a Long: never route one through any or float64,
// which lose precision above 2^53.
type SpaceID int64

func (s SpaceID) String() string { return strconv.FormatInt(int64(s), 10) }

func (s SpaceID) MarshalJSON() ([]byte, error) { return []byte(s.String()), nil }

func (s *SpaceID) UnmarshalJSON(data []byte) error {
	text := strings.Trim(string(data), `"`)
	if text == "" || text == "null" {
		*s = 0
		return nil
	}
	id, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to parse space id %s: %w", data, err)
	}
	*s = SpaceID(id)
	return nil
}

// Space is a node of the tenancy tree. A root space carries no parent_id.
type Space struct {
	ID       SpaceID `json:"id"`
	Name     string  `json:"name"`
	ParentID SpaceID `json:"parent_id"`
	RootID   SpaceID `json:"root_id"`
}

type ResourceType int

const ResourceDevice ResourceType = 0

type Resource struct {
	ID   string       `json:"res_id"`
	Type ResourceType `json:"res_type"`
}

// Page is one page of a listing. The last page arrives as an empty data slice
// with no cursor at all, so a LastRowKey of zero means the walk is over. A
// cursor that repeats the one just sent means the same thing; a caller paging
// to the end should stop on either rather than trust one signal.
type Page struct {
	LastRowKey int64 `json:"last_row_key"`
	PageSize   int   `json:"page_size"`
}

// Scope selects how deep a listing reaches. It is a required argument rather
// than an option because Tuya documents no default for only_sub, and a listing
// that silently covers the wrong depth is exactly what a caller building an
// ownership check must not get.
type Scope int

const (
	DirectChildren Scope = iota + 1
	Subtree
)

type PageOption func(*pageQuery)

type pageQuery struct {
	lastRowKey *int64
	pageSize   *int
}

// WithLastRowKey resumes a listing at the cursor Tuya returned in Page.LastRowKey.
func WithLastRowKey(cursor int64) PageOption {
	return func(q *pageQuery) { q.lastRowKey = &cursor }
}

// WithPageSize caps the entries Tuya returns per page. Tuya documents no
// maximum: its reference example answers page_size 200 to a request for 100.
func WithPageSize(size int) PageOption {
	return func(q *pageQuery) { q.pageSize = &size }
}

// Tuya's example requests spell these parameters onlySub, lastRowKey, pageSize
// and spaceId, but the live API binds only the snake_case names its reference
// tables list — a camelCase page_size is ignored and the server default applies
// silently. Encode also matters: Tuya sorts query parameters before checking
// the signature, so url.Values keeps them in the one order it accepts.
func listQuery(scope Scope, opts []PageOption) (url.Values, error) {
	var onlySub string
	switch scope {
	case DirectChildren:
		onlySub = "true"
	case Subtree:
		onlySub = "false"
	default:
		return nil, fmt.Errorf("invalid scope %d: pass DirectChildren or Subtree", scope)
	}
	var page pageQuery
	for _, opt := range opts {
		opt(&page)
	}
	query := url.Values{}
	query.Set("only_sub", onlySub)
	if page.lastRowKey != nil {
		query.Set("last_row_key", strconv.FormatInt(*page.lastRowKey, 10))
	}
	if page.pageSize != nil {
		query.Set("page_size", strconv.Itoa(*page.pageSize))
	}
	return query, nil
}

// A listing arrives as its rows plus the cursor fields beside them; the last
// one carries no cursor at all, which is what leaves Page zeroed.
func decodePage(raw json.RawMessage, data any) (Page, error) {
	if len(raw) == 0 {
		return Page{}, nil
	}
	body := struct {
		Data any `json:"data"`
		Page
	}{Data: data}
	if err := json.Unmarshal(raw, &body); err != nil {
		return Page{}, err
	}
	return body.Page, nil
}

// Tuya answers modify and delete with a bare boolean, and Do hands back that
// raw result as soon as success is true — so result:false would otherwise read
// as a success that never happened.
func assertApplied(raw json.RawMessage) error {
	// No result at all is not a confirmation, so it is refused like a false one.
	if len(raw) == 0 || string(raw) == "null" {
		return ErrNotApplied
	}
	var applied bool
	if err := json.Unmarshal(raw, &applied); err != nil {
		return fmt.Errorf("failed to unmarshal result: %w", err)
	}
	if !applied {
		return ErrNotApplied
	}
	return nil
}

// CreateSpace creates a space and returns its ID. A zero parentID creates a
// first-level space.
func (c *IoT) CreateSpace(ctx context.Context, name string, parentID SpaceID, description string) (SpaceID, error) {
	body, err := json.Marshal(struct {
		Name        string  `json:"name"`
		ParentID    SpaceID `json:"parent_id,omitempty"`
		Description string  `json:"description,omitempty"`
	}{Name: name, ParentID: parentID, Description: description})
	if err != nil {
		return 0, fmt.Errorf("failed to marshal space payload: %w", err)
	}
	raw, err := c.client.Do(ctx, http.MethodPost, "/v2.0/cloud/space/creation", body)
	if err != nil {
		return 0, err
	}
	var id SpaceID
	if err := json.Unmarshal(raw, &id); err != nil {
		return 0, fmt.Errorf("failed to unmarshal created space id: %w", err)
	}
	return id, nil
}

func (c *IoT) Space(ctx context.Context, id SpaceID) (Space, error) {
	raw, err := c.client.Do(ctx, http.MethodGet, "/v2.0/cloud/space/"+id.String(), nil)
	if err != nil {
		return Space{}, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return Space{}, fmt.Errorf("space %s: %w", id, ErrSpaceNotFound)
	}
	var space Space
	if err := json.Unmarshal(raw, &space); err != nil {
		return Space{}, fmt.Errorf("failed to unmarshal space %s: %w", id, err)
	}
	return space, nil
}

func (c *IoT) ModifySpace(ctx context.Context, id SpaceID, name, description string) error {
	body, err := json.Marshal(struct {
		Name        string `json:"name,omitempty"`
		Description string `json:"description,omitempty"`
	}{Name: name, Description: description})
	if err != nil {
		return fmt.Errorf("failed to marshal space payload: %w", err)
	}
	raw, err := c.client.Do(ctx, http.MethodPut, "/v2.0/cloud/space/"+id.String(), body)
	if err != nil {
		return err
	}
	if err := assertApplied(raw); err != nil {
		return fmt.Errorf("modify space %s: %w", id, err)
	}
	return nil
}

// DeleteSpace deletes a space. Tuya deletes its subspaces along with it.
func (c *IoT) DeleteSpace(ctx context.Context, id SpaceID) error {
	raw, err := c.client.Do(ctx, http.MethodDelete, "/v2.0/cloud/space/"+id.String(), nil)
	if err != nil {
		return err
	}
	if err := assertApplied(raw); err != nil {
		return fmt.Errorf("delete space %s: %w", id, err)
	}
	return nil
}

// SpaceResources returns one page of the resources held by a space, devices
// among them. It is one request: paging to the end is the caller's composition,
// under the stop conditions documented on Page.
func (c *IoT) SpaceResources(ctx context.Context, id SpaceID, scope Scope, opts ...PageOption) ([]Resource, Page, error) {
	query, err := listQuery(scope, opts)
	if err != nil {
		return nil, Page{}, err
	}
	path := "/v2.0/cloud/space/" + id.String() + "/resource?" + query.Encode()
	raw, err := c.client.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, Page{}, err
	}
	var resources []Resource
	page, err := decodePage(raw, &resources)
	if err != nil {
		return nil, Page{}, fmt.Errorf("failed to unmarshal resources of space %s: %w", id, err)
	}
	return resources, page, nil
}

// ChildSpaces returns one page of the spaces below id. A zero id is rejected:
// Tuya reads that as the whole project, which RootSpaces asks for deliberately.
func (c *IoT) ChildSpaces(ctx context.Context, id SpaceID, scope Scope, opts ...PageOption) ([]SpaceID, Page, error) {
	if id == 0 {
		return nil, Page{}, errors.New("ChildSpaces needs a space id; call RootSpaces for the project's top level")
	}
	query, err := listQuery(scope, opts)
	if err != nil {
		return nil, Page{}, err
	}
	query.Set("space_id", id.String())
	return c.childSpaces(ctx, query)
}

// RootSpaces returns one page of the spaces at the root of the cloud project —
// every tenant's, not one tenant's. Never reach it from a tenant-scoped path.
func (c *IoT) RootSpaces(ctx context.Context, scope Scope, opts ...PageOption) ([]SpaceID, Page, error) {
	query, err := listQuery(scope, opts)
	if err != nil {
		return nil, Page{}, err
	}
	return c.childSpaces(ctx, query)
}

func (c *IoT) childSpaces(ctx context.Context, query url.Values) ([]SpaceID, Page, error) {
	raw, err := c.client.Do(ctx, http.MethodGet, "/v2.0/cloud/space/child?"+query.Encode(), nil)
	if err != nil {
		return nil, Page{}, err
	}
	var ids []SpaceID
	page, err := decodePage(raw, &ids)
	if err != nil {
		return nil, Page{}, fmt.Errorf("failed to unmarshal child spaces: %w", err)
	}
	return ids, page, nil
}

// SpaceContains reports whether child sits under parent, at any depth — Tuya
// answers true for a grandchild. Two edges it does not document: a space
// compared against itself answers false, and a space outside the project is
// refused as CodeNoSpacePermission rather than answered false.
func (c *IoT) SpaceContains(ctx context.Context, parent, child SpaceID) (bool, error) {
	query := url.Values{}
	query.Set("parent_id", parent.String())
	query.Set("child_id", child.String())
	raw, err := c.client.Do(ctx, http.MethodGet, "/v2.0/cloud/space/relation?"+query.Encode(), nil)
	if err != nil {
		return false, err
	}
	var contains bool
	if err := json.Unmarshal(raw, &contains); err != nil {
		return false, fmt.Errorf("failed to unmarshal space relation: %w", err)
	}
	return contains, nil
}
