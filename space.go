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
	ErrSpaceNotLinked = errors.New("tuya: no space linked to owner")
	ErrSpaceNotOwned  = errors.New("tuya: space does not belong to owner")

	// ErrOwnerSpaceProtected refuses a delete of the space the owner is linked
	// to. Tuya deletes a space together with everything below it, so deleting
	// that one would take the owner's whole reach with it and leave the store
	// pointing at a space that no longer exists.
	ErrOwnerSpaceProtected = errors.New("tuya: refusing to delete the space the owner is linked to")
)

type SpaceStore interface {
	Get(ctx context.Context, owner string) (Space, error)
	Link(ctx context.Context, owner string, spaceID cloud.SpaceID) (Space, error)
	Unlink(ctx context.Context, owner string) error
}

// SpaceIoT is the slice of *cloud.IoT this door drives. RootSpaces is
// deliberately absent: it lists the cloud project's whole top level, across
// every owner, and nothing reachable from an owner may be able to ask for that.
type SpaceIoT interface {
	CreateSpace(ctx context.Context, name string, parentID cloud.SpaceID, description string) (cloud.SpaceID, error)
	Space(ctx context.Context, id cloud.SpaceID) (cloud.Space, error)
	ModifySpace(ctx context.Context, id cloud.SpaceID, name, description string) error
	DeleteSpace(ctx context.Context, id cloud.SpaceID) error
	ChildSpaces(ctx context.Context, id cloud.SpaceID, scope cloud.Scope, opts ...cloud.PageOption) ([]cloud.SpaceID, cloud.Page, error)
	SpaceResources(ctx context.Context, id cloud.SpaceID, scope cloud.Scope, opts ...cloud.PageOption) ([]cloud.Resource, cloud.Page, error)
	SpaceContains(ctx context.Context, parent, child cloud.SpaceID) (bool, error)
	DeviceStatus(ctx context.Context, deviceID string) ([]cloud.DataPoint, error)
	SendCommands(ctx context.Context, deviceID string, commands []cloud.DataPoint) error
}

// SpaceClient is the owner-scoped door for Tuya spaces. An owner is linked to
// one space, and every method resolves that link first, then refuses anything
// that is neither that space nor inside it. A zero space ID always means the
// owner's own space — never the cloud project's top level.
type SpaceClient struct {
	iot   SpaceIoT
	store SpaceStore
}

func NewSpaceClient(iot SpaceIoT, store SpaceStore) *SpaceClient {
	return &SpaceClient{iot: iot, store: store}
}

// SpaceOf reports which space an owner is linked to. The door only reads that
// mapping; writing it is the store's Link and Unlink.
func (c *SpaceClient) SpaceOf(ctx context.Context, owner string) (Space, error) {
	return c.store.Get(ctx, owner)
}

// CreateSpace adds a space under parentID. A zero parentID puts it directly
// under the owner's own space.
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

// ChildSpaces lists one page of the spaces under id, the owner's own space by
// default. Paging to the end is the caller's, under the stop conditions
// documented on cloud.Page.
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

// SpaceResources lists one page of what a space holds, devices among them.
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

func (c *SpaceClient) DeviceStatus(ctx context.Context, owner, deviceID string) ([]cloud.DataPoint, error) {
	ownerSpace, err := c.ownerSpace(ctx, owner)
	if err != nil {
		return nil, err
	}
	if err := c.assertDeviceOwned(ctx, ownerSpace, deviceID); err != nil {
		return nil, err
	}
	return c.iot.DeviceStatus(ctx, deviceID)
}

func (c *SpaceClient) SendCommands(ctx context.Context, owner, deviceID string, cmds []cloud.DataPoint) error {
	ownerSpace, err := c.ownerSpace(ctx, owner)
	if err != nil {
		return err
	}
	if err := c.assertDeviceOwned(ctx, ownerSpace, deviceID); err != nil {
		return err
	}
	return c.iot.SendCommands(ctx, deviceID, cmds)
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

// assertSpaceOwned costs one request: containment is transitive, so a space that
// sits inside the owner's own space has its whole subtree inside it too.
//
// The owner's own space is granted without asking, and not only to save the
// request: Tuya answers false when a space is compared against itself, so asking
// would refuse an owner their own space. A space the project cannot see at all
// is a refusal rather than a false, and means the same thing here.
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
	deviceGuardPageSize = 200
	deviceGuardMaxPages = 50
)

// assertDeviceOwned is the expensive half of the asymmetry: Tuya has no "which
// space holds this device" lookup, so checking a device means walking the
// resources of the owner's whole subtree. It runs before every guarded device
// call and stops at the first match, so the usual cost is one request.
//
// The walk ends where Tuya ends it — an empty page with no cursor — and also on
// a cursor that stopped moving, with a cap on how many pages it will read. Only
// the first of those is Tuya's documented behaviour; the other two keep a
// surprise costing a refusal rather than an endless loop.
func (c *SpaceClient) assertDeviceOwned(ctx context.Context, ownerSpace cloud.SpaceID, deviceID string) error {
	var cursor int64
	for range deviceGuardMaxPages {
		opts := []cloud.PageOption{cloud.WithPageSize(deviceGuardPageSize)}
		if cursor != 0 {
			opts = append(opts, cloud.WithLastRowKey(cursor))
		}
		resources, page, err := c.iot.SpaceResources(ctx, ownerSpace, cloud.Subtree, opts...)
		if err != nil {
			return fmt.Errorf("verify device ownership: %w", err)
		}
		for _, resource := range resources {
			if resource.Type == cloud.ResourceDevice && resource.ID == deviceID {
				return nil
			}
		}
		if len(resources) == 0 || page.LastRowKey == 0 || page.LastRowKey == cursor {
			return ErrDeviceNotOwned
		}
		cursor = page.LastRowKey
	}
	return fmt.Errorf("verify device ownership: space %s did not end after %d pages", ownerSpace, deviceGuardMaxPages)
}
