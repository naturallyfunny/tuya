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

var ErrNotApplied = errors.New("tuya: operation was not applied")

var ErrSpaceNotFound = errors.New("tuya: space not found")

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

type Page struct {
	LastRowKey int64 `json:"last_row_key"`
	PageSize   int   `json:"page_size"`
}

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

func WithLastRowKey(cursor int64) PageOption {
	return func(q *pageQuery) { q.lastRowKey = &cursor }
}

func WithPageSize(size int) PageOption {
	return func(q *pageQuery) { q.pageSize = &size }
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
	query.Set("only_sub", onlySub)
	if page.lastRowKey != nil {
		query.Set("last_row_key", strconv.FormatInt(*page.lastRowKey, 10))
	}
	if page.pageSize != nil {
		query.Set("page_size", strconv.Itoa(*page.pageSize))
	}
	return query, nil
}

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

func assertApplied(raw json.RawMessage) error {
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
