package tuya

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.naturallyfunny.dev/tuya/cloud"
)

type Space struct {
	Owner     string        `json:"owner"`
	SpaceID   cloud.SpaceID `json:"space_id"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

var (
	ErrSpaceNotLinked      = errors.New("tuya: no space linked to owner")
	ErrSpaceNotOwned       = errors.New("tuya: space does not belong to owner")
	ErrOwnerSpaceProtected = errors.New("tuya: refusing to delete the space the owner is linked to")
)

type SpaceStore interface {
	Get(ctx context.Context, owner string) (Space, error)
	Link(ctx context.Context, owner string, spaceID cloud.SpaceID) (Space, error)
	Unlink(ctx context.Context, owner string) error
}

type SpaceIoT interface {
	CreateSpace(ctx context.Context, name string, parentID cloud.SpaceID, description string) (cloud.SpaceID, error)
	Space(ctx context.Context, id cloud.SpaceID) (cloud.Space, error)
	ModifySpace(ctx context.Context, id cloud.SpaceID, name, description string) error
	DeleteSpace(ctx context.Context, id cloud.SpaceID) error
	ChildSpaces(ctx context.Context, id cloud.SpaceID, scope cloud.Scope, opts ...cloud.PageOption) ([]cloud.SpaceID, cloud.Page, error)
	SpaceResources(ctx context.Context, id cloud.SpaceID, scope cloud.Scope, opts ...cloud.PageOption) ([]cloud.Resource, cloud.Page, error)
	SpaceContains(ctx context.Context, parent, child cloud.SpaceID) (bool, error)
}

type SpaceClient struct {
	iot   SpaceIoT
	store SpaceStore
}

func NewSpaceClient(iot SpaceIoT, store SpaceStore) *SpaceClient {
	return &SpaceClient{iot: iot, store: store}
}

func (c *SpaceClient) SpaceOf(ctx context.Context, owner string) (Space, error) {
	return c.store.Get(ctx, owner)
}

func (c *SpaceClient) CreateSpace(ctx context.Context, owner, name string, parentID cloud.SpaceID, description string) (cloud.SpaceID, error) {
	ownerSpace, parent, err := c.resolve(ctx, owner, parentID)
	if err != nil {
		return 0, err
	}
	if err := c.assertSpaceOwned(ctx, ownerSpace, parent); err != nil {
		return 0, err
	}
	return c.iot.CreateSpace(ctx, name, parent, description)
}

func (c *SpaceClient) Space(ctx context.Context, owner string, id cloud.SpaceID) (cloud.Space, error) {
	ownerSpace, target, err := c.resolve(ctx, owner, id)
	if err != nil {
		return cloud.Space{}, err
	}
	if err := c.assertSpaceOwned(ctx, ownerSpace, target); err != nil {
		return cloud.Space{}, err
	}
	return c.iot.Space(ctx, target)
}

func (c *SpaceClient) ModifySpace(ctx context.Context, owner string, id cloud.SpaceID, name, description string) error {
	ownerSpace, target, err := c.resolve(ctx, owner, id)
	if err != nil {
		return err
	}
	if err := c.assertSpaceOwned(ctx, ownerSpace, target); err != nil {
		return err
	}
	return c.iot.ModifySpace(ctx, target, name, description)
}

func (c *SpaceClient) DeleteSpace(ctx context.Context, owner string, id cloud.SpaceID) error {
	ownerSpace, target, err := c.resolve(ctx, owner, id)
	if err != nil {
		return err
	}
	if target == ownerSpace {
		return ErrOwnerSpaceProtected
	}
	if err := c.assertSpaceOwned(ctx, ownerSpace, target); err != nil {
		return err
	}
	return c.iot.DeleteSpace(ctx, target)
}

func (c *SpaceClient) ChildSpaces(ctx context.Context, owner string, id cloud.SpaceID, scope cloud.Scope, opts ...cloud.PageOption) ([]cloud.SpaceID, cloud.Page, error) {
	ownerSpace, target, err := c.resolve(ctx, owner, id)
	if err != nil {
		return nil, cloud.Page{}, err
	}
	if err := c.assertSpaceOwned(ctx, ownerSpace, target); err != nil {
		return nil, cloud.Page{}, err
	}
	return c.iot.ChildSpaces(ctx, target, scope, opts...)
}

func (c *SpaceClient) SpaceResources(ctx context.Context, owner string, id cloud.SpaceID, scope cloud.Scope, opts ...cloud.PageOption) ([]cloud.Resource, cloud.Page, error) {
	ownerSpace, target, err := c.resolve(ctx, owner, id)
	if err != nil {
		return nil, cloud.Page{}, err
	}
	if err := c.assertSpaceOwned(ctx, ownerSpace, target); err != nil {
		return nil, cloud.Page{}, err
	}
	return c.iot.SpaceResources(ctx, target, scope, opts...)
}

func (c *SpaceClient) ContainsSpace(ctx context.Context, owner string, id cloud.SpaceID) (bool, error) {
	ownerSpace, target, err := c.resolve(ctx, owner, id)
	if err != nil {
		return false, err
	}
	if err := c.assertSpaceOwned(ctx, ownerSpace, target); err != nil {
		if errors.Is(err, ErrSpaceNotOwned) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c *SpaceClient) ContainsDevice(ctx context.Context, owner, deviceID string) (bool, error) {
	ownerSpace, err := c.ownerSpace(ctx, owner)
	if err != nil {
		return false, err
	}
	var cursor int64
	for range deviceScanMaxPages {
		opts := []cloud.PageOption{cloud.WithPageSize(deviceScanPageSize)}
		if cursor != 0 {
			opts = append(opts, cloud.WithLastRowKey(cursor))
		}
		resources, page, err := c.iot.SpaceResources(ctx, ownerSpace, cloud.Subtree, opts...)
		if err != nil {
			return false, fmt.Errorf("scan resources of space %s: %w", ownerSpace, err)
		}
		for _, resource := range resources {
			if resource.Type == cloud.ResourceDevice && resource.ID == deviceID {
				return true, nil
			}
		}
		if len(resources) == 0 || page.LastRowKey == 0 || page.LastRowKey == cursor {
			return false, nil
		}
		cursor = page.LastRowKey
	}
	return false, fmt.Errorf("scan resources of space %s: did not end after %d pages", ownerSpace, deviceScanMaxPages)
}

func (c *SpaceClient) ownerSpace(ctx context.Context, owner string) (cloud.SpaceID, error) {
	space, err := c.store.Get(ctx, owner)
	if err != nil {
		return 0, err
	}
	if space.SpaceID == 0 {
		return 0, ErrSpaceNotLinked
	}
	return space.SpaceID, nil
}

func (c *SpaceClient) resolve(ctx context.Context, owner string, id cloud.SpaceID) (ownerSpace, target cloud.SpaceID, err error) {
	ownerSpace, err = c.ownerSpace(ctx, owner)
	if err != nil {
		return 0, 0, err
	}
	if id == 0 {
		return ownerSpace, ownerSpace, nil
	}
	return ownerSpace, id, nil
}

func (c *SpaceClient) assertSpaceOwned(ctx context.Context, ownerSpace, target cloud.SpaceID) error {
	if target == ownerSpace {
		return nil
	}
	contains, err := c.iot.SpaceContains(ctx, ownerSpace, target)
	if err != nil {
		var apiErr *cloud.APIError
		if errors.As(err, &apiErr) && apiErr.Code == cloud.CodeNoSpacePermission {
			return ErrSpaceNotOwned
		}
		return fmt.Errorf("verify space ownership: %w", err)
	}
	if !contains {
		return ErrSpaceNotOwned
	}
	return nil
}

const (
	deviceScanPageSize = 200
	deviceScanMaxPages = 50
)
