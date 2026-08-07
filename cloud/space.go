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

// SpaceID is a Tuya space identifier. Tuya is inconsistent about its JSON type —
// space creation answers with a number, space lookup with a string — so this
// accepts either and always renders as a number. It is a Long: never route one
// through any or float64, which lose precision above 2^53.
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

type Space struct {
	ID       SpaceID
	Name     string
	ParentID SpaceID
	RootID   SpaceID
}

type ResourceType int

const ResourceDevice ResourceType = 0

type Resource struct {
	ID   string
	Type ResourceType
}

// Page is one page of a listing. Tuya does not document how to detect the last
// page: its reference example shows last_row_key 0 for a page that fits in one
// response, and never says whether an exhausted cursor comes back as 0, as the
// key it was given, or alongside an empty data slice. A caller that pages to the
// end must therefore stop on all three — an empty slice, a zero LastRowKey, or a
// LastRowKey equal to the one it just sent — or risk looping forever.
type Page struct {
	LastRowKey int64
	PageSize   int
}

// Scope selects how deep a listing reaches. It is a required argument rather
// than an option because Tuya's default for only_sub is undocumented, and a
// listing that silently covers the wrong depth is exactly what a caller
// building an ownership check must not get.
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

// Tuya's reference tables and its own example requests disagree on how these
// query parameters are spelled — the tables say only_sub, last_row_key,
// page_size and space_id, the examples say onlySub, lastRowKey, pageSize and
// spaceId. Both spellings are sent with the same value: Tuya binds whichever
// name it knows and ignores the other. Sending one spelling and guessing wrong
// would leave the parameter silently at a server-side default, which for
// only_sub means querying a different depth than the caller asked for. Drop the
// losing spelling once a live call proves which one Tuya reads.
// /v2.0/cloud/space/relation is unaffected: its table and example agree.
func setBothCasings(query url.Values, snake, camel, value string) {
	query.Set(snake, value)
	query.Set(camel, value)
}

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
	setBothCasings(query, "only_sub", "onlySub", onlySub)
	if page.lastRowKey != nil {
		setBothCasings(query, "last_row_key", "lastRowKey", strconv.FormatInt(*page.lastRowKey, 10))
	}
	if page.pageSize != nil {
		setBothCasings(query, "page_size", "pageSize", strconv.Itoa(*page.pageSize))
	}
	return query, nil
}

// Tuya answers the space API in two casings: the paged wrapper is snake_case
// while the objects inside data are camelCase, and its reference tables spell
// both the other way round. Only multi-word names can differ, and those are read
// under either spelling so a rename cannot silently blank a field.
func decodeField(fields map[string]json.RawMessage, target any, names ...string) error {
	for _, name := range names {
		raw, ok := fields[name]
		if !ok {
			continue
		}
		if err := json.Unmarshal(raw, target); err != nil {
			return fmt.Errorf("failed to decode %s: %w", name, err)
		}
		return nil
	}
	return nil
}

func objectFields(data []byte) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}

func (s *Space) UnmarshalJSON(data []byte) error {
	fields, err := objectFields(data)
	if err != nil {
		return fmt.Errorf("failed to decode space: %w", err)
	}
	return errors.Join(
		decodeField(fields, &s.ID, "id"),
		decodeField(fields, &s.Name, "name"),
		decodeField(fields, &s.ParentID, "parent_id", "parentId"),
		decodeField(fields, &s.RootID, "root_id", "rootId"),
	)
}

func (r *Resource) UnmarshalJSON(data []byte) error {
	fields, err := objectFields(data)
	if err != nil {
		return fmt.Errorf("failed to decode resource: %w", err)
	}
	return errors.Join(
		decodeField(fields, &r.ID, "res_id", "resId"),
		decodeField(fields, &r.Type, "res_type", "resType"),
	)
}

func decodePage(raw json.RawMessage, data any) (Page, error) {
	if len(raw) == 0 {
		return Page{}, nil
	}
	fields, err := objectFields(raw)
	if err != nil {
		return Page{}, err
	}
	var page Page
	if err := errors.Join(
		decodeField(fields, data, "data"),
		decodeField(fields, &page.LastRowKey, "last_row_key", "lastRowKey"),
		decodeField(fields, &page.PageSize, "page_size", "pageSize"),
	); err != nil {
		return Page{}, err
	}
	return page, nil
}

// Tuya answers modify and delete with a bare boolean, and Do hands back that
// raw result as soon as success is true — so result:false would otherwise read
// as a success that never happened.
func assertApplied(raw json.RawMessage) error {
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
	setBothCasings(query, "space_id", "spaceId", id.String())
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

// SpaceContains reports whether child sits under parent. Tuya does not document
// whether it answers for descendants at any depth or only for direct children;
// a guard built on it is therefore fail-closed either way, but nested spaces
// need the transitive reading to work. Verify before relying on depth.
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
