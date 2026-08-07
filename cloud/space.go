package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

type Space struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	ParentID int64  `json:"parent_id"`
	RootID   int64  `json:"root_id"`
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

func listQuery(onlySub bool, page Page) url.Values {
	query := url.Values{}
	query.Set("only_sub", strconv.FormatBool(onlySub))
	if page.LastRowKey != 0 {
		query.Set("last_row_key", strconv.FormatInt(page.LastRowKey, 10))
	}
	if page.PageSize != 0 {
		query.Set("page_size", strconv.Itoa(page.PageSize))
	}
	return query
}

func (c *IoT) CreateSpace(ctx context.Context, name string, parentID int64, description string) (int64, error) {
	body, err := json.Marshal(struct {
		Name        string `json:"name"`
		ParentID    int64  `json:"parent_id,omitempty"`
		Description string `json:"description,omitempty"`
	}{Name: name, ParentID: parentID, Description: description})
	if err != nil {
		return 0, fmt.Errorf("failed to marshal space payload: %w", err)
	}
	raw, err := c.client.Do(ctx, http.MethodPost, "/v2.0/cloud/space/creation", body)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := json.Unmarshal(raw, &id); err != nil {
		return 0, fmt.Errorf("failed to unmarshal created space id: %w", err)
	}
	return id, nil
}

var ErrSpaceNotFound = errors.New("tuya: space not found")

func (c *IoT) Space(ctx context.Context, id int64) (Space, error) {
	path := fmt.Sprintf("/v2.0/cloud/space/%d", id)
	raw, err := c.client.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return Space{}, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return Space{}, fmt.Errorf("space %d: %w", id, ErrSpaceNotFound)
	}
	var space Space
	if err := json.Unmarshal(raw, &space); err != nil {
		return Space{}, fmt.Errorf("failed to unmarshal space %d: %w", id, err)
	}
	return space, nil
}

var ErrNotApplied = errors.New("tuya: operation was not applied")

func (c *IoT) ModifySpace(ctx context.Context, id int64, name, description string) error {
	path := fmt.Sprintf("/v2.0/cloud/space/%d", id)
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
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &applied); err != nil {
			return fmt.Errorf("failed to unmarshal the result of modifying space %d: %w", id, err)
		}
	}
	if !applied {
		return fmt.Errorf("modify space %d: %w", id, ErrNotApplied)
	}
	return nil
}

func (c *IoT) DeleteSpace(ctx context.Context, id int64) error {
	path := fmt.Sprintf("/v2.0/cloud/space/%d", id)
	raw, err := c.client.Do(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	var applied bool
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &applied); err != nil {
			return fmt.Errorf("failed to unmarshal the result of deleting space %d: %w", id, err)
		}
	}
	if !applied {
		return fmt.Errorf("delete space %d: %w", id, ErrNotApplied)
	}
	return nil
}

func (c *IoT) SpaceResources(ctx context.Context, id int64, onlySub bool, page Page) ([]Resource, Page, error) {
	path := fmt.Sprintf("/v2.0/cloud/space/%d/resource?%s", id, listQuery(onlySub, page).Encode())
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
			return nil, Page{}, fmt.Errorf("failed to unmarshal the resources of space %d: %w", id, err)
		}
	}
	return body.Data, body.Page, nil
}

func (c *IoT) ListSpaces(ctx context.Context, id int64, onlySub bool, page Page) ([]int64, Page, error) {
	query := listQuery(onlySub, page)
	if id != 0 {
		query.Set("space_id", strconv.FormatInt(id, 10))
	}
	path := fmt.Sprintf("/v2.0/cloud/space/child?%s", query.Encode())
	raw, err := c.client.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, Page{}, err
	}
	var body struct {
		Data []int64 `json:"data"`
		Page
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, Page{}, fmt.Errorf("failed to unmarshal space list: %w", err)
		}
	}
	return body.Data, body.Page, nil
}

func (c *IoT) SpaceRelation(ctx context.Context, parent, child int64) (bool, error) {
	query := url.Values{}
	query.Set("parent_id", strconv.FormatInt(parent, 10))
	query.Set("child_id", strconv.FormatInt(child, 10))
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
