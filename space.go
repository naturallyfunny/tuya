package tuya

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

func (c *Client) CreateSpace(ctx context.Context, name string, parentID int64, description string) (int64, error) {
	body, err := json.Marshal(struct {
		Name        string `json:"name"`
		ParentID    int64  `json:"parent_id,omitempty"`
		Description string `json:"description,omitempty"`
	}{Name: name, ParentID: parentID, Description: description})
	if err != nil {
		return 0, fmt.Errorf("marshal space payload: %w", err)
	}
	raw, err := c.Do(ctx, http.MethodPost, "/v2.0/cloud/space/creation", body)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := json.Unmarshal(raw, &id); err != nil {
		return 0, fmt.Errorf("unmarshal created space id: %w", err)
	}
	return id, nil
}

var ErrSpaceNotFound = errors.New("tuya: space not found")

func (c *Client) Space(ctx context.Context, id int64) (Space, error) {
	raw, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("/v2.0/cloud/space/%d", id), nil)
	if err != nil {
		return Space{}, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return Space{}, fmt.Errorf("space %d: %w", id, ErrSpaceNotFound)
	}
	var space Space
	if err := json.Unmarshal(raw, &space); err != nil {
		return Space{}, fmt.Errorf("unmarshal space %d: %w", id, err)
	}
	return space, nil
}

var ErrNotApplied = errors.New("tuya: operation was not applied")

func (c *Client) ModifySpace(ctx context.Context, id int64, name, description string) error {
	body, err := json.Marshal(struct {
		Name        string `json:"name,omitempty"`
		Description string `json:"description,omitempty"`
	}{Name: name, Description: description})
	if err != nil {
		return fmt.Errorf("marshal space payload: %w", err)
	}
	raw, err := c.Do(ctx, http.MethodPut, fmt.Sprintf("/v2.0/cloud/space/%d", id), body)
	if err != nil {
		return err
	}
	var applied bool
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &applied); err != nil {
			return fmt.Errorf("unmarshal result of modifying space %d: %w", id, err)
		}
	}
	if !applied {
		return fmt.Errorf("modify space %d: %w", id, ErrNotApplied)
	}
	return nil
}

func (c *Client) DeleteSpace(ctx context.Context, id int64) error {
	raw, err := c.Do(ctx, http.MethodDelete, fmt.Sprintf("/v2.0/cloud/space/%d", id), nil)
	if err != nil {
		return err
	}
	var applied bool
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &applied); err != nil {
			return fmt.Errorf("unmarshal result of deleting space %d: %w", id, err)
		}
	}
	if !applied {
		return fmt.Errorf("delete space %d: %w", id, ErrNotApplied)
	}
	return nil
}

type SpaceResourceType int

type Resource struct {
	ID   string            `json:"res_id"`
	Type SpaceResourceType `json:"res_type"`
}

const SpaceResourceDevice SpaceResourceType = 0

func (c *Client) SpaceResources(ctx context.Context, id int64, onlySub bool, lastRowKey int64, pageSize int) ([]Resource, int64, error) {
	params := url.Values{}
	params.Set("only_sub", strconv.FormatBool(onlySub))
	if lastRowKey != 0 {
		params.Set("last_row_key", strconv.FormatInt(lastRowKey, 10))
	}
	if pageSize != 0 {
		params.Set("page_size", strconv.Itoa(pageSize))
	}
	raw, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("/v2.0/cloud/space/%d/resource?%s", id, params.Encode()), nil)
	if err != nil {
		return nil, 0, err
	}
	var body struct {
		Data       []Resource `json:"data"`
		LastRowKey int64      `json:"last_row_key"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, 0, fmt.Errorf("unmarshal resources of space %d: %w", id, err)
		}
	}
	return body.Data, body.LastRowKey, nil
}

func (c *Client) ListSpaces(ctx context.Context, id int64, onlySub bool, lastRowKey int64, pageSize int) ([]int64, int64, error) {
	params := url.Values{}
	if id != 0 {
		params.Set("space_id", strconv.FormatInt(id, 10))
	}
	params.Set("only_sub", strconv.FormatBool(onlySub))
	if lastRowKey != 0 {
		params.Set("last_row_key", strconv.FormatInt(lastRowKey, 10))
	}
	if pageSize != 0 {
		params.Set("page_size", strconv.Itoa(pageSize))
	}
	raw, err := c.Do(ctx, http.MethodGet, "/v2.0/cloud/space/child?"+params.Encode(), nil)
	if err != nil {
		return nil, 0, err
	}
	var body struct {
		Data       []int64 `json:"data"`
		LastRowKey int64   `json:"last_row_key"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, 0, fmt.Errorf("unmarshal space list: %w", err)
		}
	}
	return body.Data, body.LastRowKey, nil
}

func (c *Client) SpaceRelation(ctx context.Context, parent, child int64) (bool, error) {
	params := url.Values{}
	params.Set("parent_id", strconv.FormatInt(parent, 10))
	params.Set("child_id", strconv.FormatInt(child, 10))
	raw, err := c.Do(ctx, http.MethodGet, "/v2.0/cloud/space/relation?"+params.Encode(), nil)
	if err != nil {
		return false, err
	}
	var contains bool
	if err := json.Unmarshal(raw, &contains); err != nil {
		return false, fmt.Errorf("unmarshal space relation: %w", err)
	}
	return contains, nil
}
