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

func listQuery(scope Scope, page Page) (url.Values, error) {
	query := url.Values{}
	switch scope {
	case DirectChildren:
		query.Set("only_sub", "true")
	case Subtree:
		query.Set("only_sub", "false")
	default:
		return nil, fmt.Errorf("invalid scope %d: pass DirectChildren or Subtree", scope)
	}
	if page.LastRowKey != 0 {
		query.Set("last_row_key", strconv.FormatInt(page.LastRowKey, 10))
	}
	if page.PageSize != 0 {
		query.Set("page_size", strconv.Itoa(page.PageSize))
	}
	return query, nil
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
	path := fmt.Sprintf("/v2.0/cloud/space/%s", id)
	raw, err := c.client.Do(ctx, http.MethodGet, path, nil)
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
	path := fmt.Sprintf("/v2.0/cloud/space/%s", id)
	body, err := json.Marshal(struct {
		Name        string `json:"name,omitempty"`
		Description string `json:"description,omitempty"`
	}{Name: name, Description: description})
	if err != nil {
		return fmt.Errorf("failed to marshal space payload: %w", err)
	}
	raw, err := c.client.Do(ctx, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	var applied bool
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &applied); err != nil {
			return fmt.Errorf("failed to unmarshal the result of modifying space %s: %w", id, err)
		}
	}
	if !applied {
		return fmt.Errorf("modify space %s: %w", id, ErrNotApplied)
	}
	return nil
}

func (c *IoT) DeleteSpace(ctx context.Context, id SpaceID) error {
	path := fmt.Sprintf("/v2.0/cloud/space/%s", id)
	raw, err := c.client.Do(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	var applied bool
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &applied); err != nil {
			return fmt.Errorf("failed to unmarshal the result of deleting space %s: %w", id, err)
		}
	}
	if !applied {
		return fmt.Errorf("delete space %s: %w", id, ErrNotApplied)
	}
	return nil
}

func (c *IoT) SpaceResources(ctx context.Context, id SpaceID, scope Scope, page Page) ([]Resource, Page, error) {
	query, err := listQuery(scope, page)
	if err != nil {
		return nil, Page{}, err
	}
	path := fmt.Sprintf("/v2.0/cloud/space/%s/resource?%s", id, query.Encode())
	raw, err := c.client.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, Page{}, err
	}
	var body struct {
		Data []Resource `json:"data"`
		Page
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, Page{}, fmt.Errorf("failed to unmarshal the resources of space %s: %w", id, err)
		}
	}
	return body.Data, body.Page, nil
}

func (c *IoT) ListSpaces(ctx context.Context, id SpaceID, scope Scope, page Page) ([]SpaceID, Page, error) {
	query, err := listQuery(scope, page)
	if err != nil {
		return nil, Page{}, err
	}
	if id != 0 {
		query.Set("space_id", id.String())
	}
	path := fmt.Sprintf("/v2.0/cloud/space/child?%s", query.Encode())
	raw, err := c.client.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, Page{}, err
	}
	var body struct {
		Data []SpaceID `json:"data"`
		Page
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, Page{}, fmt.Errorf("failed to unmarshal space list: %w", err)
		}
	}
	return body.Data, body.Page, nil
}

func (c *IoT) SpaceRelation(ctx context.Context, parent, child SpaceID) (bool, error) {
	query := url.Values{}
	query.Set("parent_id", parent.String())
	query.Set("child_id", child.String())
	path := fmt.Sprintf("/v2.0/cloud/space/relation?%s", query.Encode())
	raw, err := c.client.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return false, err
	}
	var contains bool
	if err := json.Unmarshal(raw, &contains); err != nil {
		return false, fmt.Errorf("failed to unmarshal space relation: %w", err)
	}
	return contains, nil
}
