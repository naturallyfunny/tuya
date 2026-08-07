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
}

// SpaceClient maps an owner onto the one Tuya space it is linked to. A zero
// space ID always means that space — never the cloud project's top level — so
// the common calls need no ID at all and no ID reachable from here names another
// owner's space by accident.
//
// The space operations do refuse a space outside the owner's subtree, because
// the IDs they take are owner-relative by construction: this door has no way to
// express an operation on someone else's space, and the check that enforces it
// is a single request Tuya answers directly. Devices are different — see
// ContainsDevice, which reports rather than refuses.
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

// ContainsSpace reports whether id is the owner's own space or sits somewhere
// below it. It is a fact for the caller to act on, not a refusal — the space
// operations on this door apply it themselves, but a caller with its own rules
// about who may reach where can ask directly.
//
// One request, and containment is transitive, so a true covers the whole subtree
// under id as well.
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

// ContainsDevice reports whether deviceID sits anywhere in the subtree under the
// owner's space. It is the spatial counterpart of AppAccountClient.HasDevice and
// carries the same meaning: a fact, not a verdict.
//
// Read the cost before building on it. Tuya has no "which space holds this
// device" lookup — GET /v2.0/cloud/thing/{device_id} carries no space or asset
// ID — so the only route is to enumerate the subtree's resources and look for
// the ID. It stops at the first match, so a lucky call is one request, but the
// page holding the device is not guaranteed to be the first, and the ceiling is
// deviceScanMaxPages pages of deviceScanPageSize resources. Unlike HasDevice,
// this grows with the size of the estate. A caller that already mirrors which
// space each device sits in will answer far faster from its own records, and
// should.
//
// The scan ends where Tuya ends it — an empty page with no cursor — and also on
// a cursor that stopped moving, with a cap on how many pages it reads. Only the
// first is Tuya's documented behaviour; the other two keep a surprise costing an
// error rather than an endless loop.
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
	deviceScanPageSize = 200
	deviceScanMaxPages = 50
)
