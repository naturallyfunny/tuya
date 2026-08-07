package tuya

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.naturallyfunny.dev/tuya/cloud"
)

type SpaceTenant struct {
	Owner       string        `json:"owner"`
	RootSpaceID cloud.SpaceID `json:"root_space_id"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

var (
	ErrSpaceNotLinked = errors.New("tuya: no root space linked to owner")
	ErrSpaceNotOwned  = errors.New("tuya: space does not belong to owner")

	// ErrRootSpaceProtected refuses a delete of the tenant boundary itself.
	// Tuya deletes a space together with its subspaces, so deleting the root
	// would take the whole tenancy with it and leave the mapping dangling.
	ErrRootSpaceProtected = errors.New("tuya: refusing to delete the tenant's own root space")
)

type SpaceStore interface {
	Get(ctx context.Context, owner string) (SpaceTenant, error)
	Link(ctx context.Context, owner string, rootSpaceID cloud.SpaceID) (SpaceTenant, error)
	Unlink(ctx context.Context, owner string) error
}

// SpaceIoT is the slice of *cloud.IoT this door drives. RootSpaces is
// deliberately absent: it lists every tenant's spaces, and nothing reachable
// from an owner may be able to ask for that.
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

// SpaceClient is the owner-scoped door for Tuya's spatial tenancy model: a
// tenant is a root space, and everything it owns hangs in the subtree below.
// Every method resolves the owner first, then refuses any space outside that
// subtree. A zero space ID always means the tenant's own root — never Tuya's
// project root.
type SpaceClient struct {
	iot   SpaceIoT
	store SpaceStore
}

func NewSpaceClient(iot SpaceIoT, store SpaceStore) *SpaceClient {
	return &SpaceClient{iot: iot, store: store}
}

func (c *SpaceClient) Tenant(ctx context.Context, owner string) (SpaceTenant, error) {
	return c.store.Get(ctx, owner)
}

// CreateSpace adds a space under parentID. A zero parentID puts it directly
// under the tenant's root, which is also the only place Tuya's own first-level
// spaces could otherwise be created from.
func (c *SpaceClient) CreateSpace(ctx context.Context, owner, name string, parentID cloud.SpaceID, description string) (cloud.SpaceID, error) {
	root, parent, err := c.resolve(ctx, owner, parentID)
	if err != nil {
		return 0, err
	}
	if err := c.assertSpaceOwned(ctx, root, parent); err != nil {
		return 0, err
	}
	return c.iot.CreateSpace(ctx, name, parent, description)
}

func (c *SpaceClient) Space(ctx context.Context, owner string, id cloud.SpaceID) (cloud.Space, error) {
	root, target, err := c.resolve(ctx, owner, id)
	if err != nil {
		return cloud.Space{}, err
	}
	if err := c.assertSpaceOwned(ctx, root, target); err != nil {
		return cloud.Space{}, err
	}
	return c.iot.Space(ctx, target)
}

func (c *SpaceClient) ModifySpace(ctx context.Context, owner string, id cloud.SpaceID, name, description string) error {
	root, target, err := c.resolve(ctx, owner, id)
	if err != nil {
		return err
	}
	if err := c.assertSpaceOwned(ctx, root, target); err != nil {
		return err
	}
	return c.iot.ModifySpace(ctx, target, name, description)
}

func (c *SpaceClient) DeleteSpace(ctx context.Context, owner string, id cloud.SpaceID) error {
	root, target, err := c.resolve(ctx, owner, id)
	if err != nil {
		return err
	}
	if target == root {
		return ErrRootSpaceProtected
	}
	if err := c.assertSpaceOwned(ctx, root, target); err != nil {
		return err
	}
	return c.iot.DeleteSpace(ctx, target)
}

// ChildSpaces lists one page of the spaces under id, the tenant's root by
// default. Paging to the end is the caller's, under the stop conditions
// documented on cloud.Page.
func (c *SpaceClient) ChildSpaces(ctx context.Context, owner string, id cloud.SpaceID, scope cloud.Scope, opts ...cloud.PageOption) ([]cloud.SpaceID, cloud.Page, error) {
	root, target, err := c.resolve(ctx, owner, id)
	if err != nil {
		return nil, cloud.Page{}, err
	}
	if err := c.assertSpaceOwned(ctx, root, target); err != nil {
		return nil, cloud.Page{}, err
	}
	return c.iot.ChildSpaces(ctx, target, scope, opts...)
}

// SpaceResources lists one page of what a space holds, devices among them.
func (c *SpaceClient) SpaceResources(ctx context.Context, owner string, id cloud.SpaceID, scope cloud.Scope, opts ...cloud.PageOption) ([]cloud.Resource, cloud.Page, error) {
	root, target, err := c.resolve(ctx, owner, id)
	if err != nil {
		return nil, cloud.Page{}, err
	}
	if err := c.assertSpaceOwned(ctx, root, target); err != nil {
		return nil, cloud.Page{}, err
	}
	return c.iot.SpaceResources(ctx, target, scope, opts...)
}

func (c *SpaceClient) DeviceStatus(ctx context.Context, owner, deviceID string) ([]cloud.DataPoint, error) {
	root, err := c.tenantRoot(ctx, owner)
	if err != nil {
		return nil, err
	}
	if err := c.assertDeviceOwned(ctx, root, deviceID); err != nil {
		return nil, err
	}
	return c.iot.DeviceStatus(ctx, deviceID)
}

func (c *SpaceClient) SendCommands(ctx context.Context, owner, deviceID string, cmds []cloud.DataPoint) error {
	root, err := c.tenantRoot(ctx, owner)
	if err != nil {
		return err
	}
	if err := c.assertDeviceOwned(ctx, root, deviceID); err != nil {
		return err
	}
	return c.iot.SendCommands(ctx, deviceID, cmds)
}

func (c *SpaceClient) tenantRoot(ctx context.Context, owner string) (cloud.SpaceID, error) {
	tenant, err := c.store.Get(ctx, owner)
	if err != nil {
		return 0, err
	}
	if tenant.RootSpaceID == 0 {
		return 0, ErrSpaceNotLinked
	}
	return tenant.RootSpaceID, nil
}

func (c *SpaceClient) resolve(ctx context.Context, owner string, id cloud.SpaceID) (root, target cloud.SpaceID, err error) {
	root, err = c.tenantRoot(ctx, owner)
	if err != nil {
		return 0, 0, err
	}
	if id == 0 {
		return root, root, nil
	}
	return root, id, nil
}

// assertSpaceOwned costs one request: containment is transitive, so a space
// that sits under the tenant's root has its whole subtree under it too.
func (c *SpaceClient) assertSpaceOwned(ctx context.Context, root, target cloud.SpaceID) error {
	if target == root {
		return nil
	}
	contains, err := c.iot.SpaceContains(ctx, root, target)
	if err != nil {
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
// space holds this device" lookup, so placing a device inside a tenant means
// walking the resources of the whole subtree. It runs before every guarded
// device call and stops at the first match, so the usual cost is one request.
//
// Tuya documents no end-of-listing signal, so the walk treats each plausible
// one as the end — no rows, a zero cursor, a cursor that did not move — and
// caps the pages it will read. An undocumented answer therefore costs a
// refusal, never an endless loop.
func (c *SpaceClient) assertDeviceOwned(ctx context.Context, root cloud.SpaceID, deviceID string) error {
	var cursor int64
	for range deviceGuardMaxPages {
		opts := []cloud.PageOption{cloud.WithPageSize(deviceGuardPageSize)}
		if cursor != 0 {
			opts = append(opts, cloud.WithLastRowKey(cursor))
		}
		resources, page, err := c.iot.SpaceResources(ctx, root, cloud.Subtree, opts...)
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
	return fmt.Errorf("verify device ownership: space %s did not end after %d pages", root, deviceGuardMaxPages)
}
