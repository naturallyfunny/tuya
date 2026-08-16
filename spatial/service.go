// Package spatial bridges an application's own user identity to Tuya's space
// tree, for integrations where a user is tied to a place rather than to an
// account — a smart hotel app, where a guest gets a room and the room holds the
// devices. An owner is whatever your application calls a user; the door resolves
// it to a space and answers what that subtree contains.
//
// Unlike the root package this one may offer what Tuya has no single endpoint
// for, as long as it is useful and its cost is written down.
package spatial

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.naturallyfunny.dev/tuya"
)

type Space struct {
	Owner     string    `json:"owner"`
	SpaceID   int64     `json:"space_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

var (
	ErrNotLinked           = errors.New("tuya: no space linked to owner")
	ErrNotOwned            = errors.New("tuya: space does not belong to owner")
	ErrOwnerSpaceProtected = errors.New("tuya: refusing to delete the space the owner is linked to")
)

type Store interface {
	Get(ctx context.Context, owner string) (Space, error)
	Link(ctx context.Context, owner string, spaceID int64) (Space, error)
	Unlink(ctx context.Context, owner string) error
}

type Client interface {
	CreateSpace(ctx context.Context, name string, parentID int64, description string) (int64, error)
	Space(ctx context.Context, id int64) (tuya.Space, error)
	ModifySpace(ctx context.Context, id int64, name, description string) error
	DeleteSpace(ctx context.Context, id int64) error
	ListSpaces(ctx context.Context, id int64, onlySub bool, page tuya.Page) ([]int64, tuya.Page, error)
	SpaceResources(ctx context.Context, id int64, onlySub bool, page tuya.Page) ([]tuya.Resource, tuya.Page, error)
	SpaceDevices(ctx context.Context, spaceIDs []int64, recursive bool, productIDs, categories []string, lastID string, pageSize int, opts ...tuya.DeviceOption) ([]tuya.SpaceDevice, error)
	SpaceRelation(ctx context.Context, parent, child int64) (bool, error)
}

type Service struct {
	client Client
	store  Store
}

func NewService(client Client, store Store) *Service {
	return &Service{client: client, store: store}
}

func (s *Service) SpaceOf(ctx context.Context, owner string) (Space, error) {
	return s.store.Get(ctx, owner)
}

func (s *Service) CreateSpace(ctx context.Context, owner, name string, parentID int64, description string) (int64, error) {
	ownerSpace, parent, err := s.resolve(ctx, owner, parentID)
	if err != nil {
		return 0, err
	}
	if err := s.assertSpaceOwned(ctx, ownerSpace, parent); err != nil {
		return 0, err
	}
	return s.client.CreateSpace(ctx, name, parent, description)
}

func (s *Service) Space(ctx context.Context, owner string, id int64) (tuya.Space, error) {
	ownerSpace, target, err := s.resolve(ctx, owner, id)
	if err != nil {
		return tuya.Space{}, err
	}
	if err := s.assertSpaceOwned(ctx, ownerSpace, target); err != nil {
		return tuya.Space{}, err
	}
	return s.client.Space(ctx, target)
}

func (s *Service) ModifySpace(ctx context.Context, owner string, id int64, name, description string) error {
	ownerSpace, target, err := s.resolve(ctx, owner, id)
	if err != nil {
		return err
	}
	if err := s.assertSpaceOwned(ctx, ownerSpace, target); err != nil {
		return err
	}
	return s.client.ModifySpace(ctx, target, name, description)
}

func (s *Service) DeleteSpace(ctx context.Context, owner string, id int64) error {
	ownerSpace, target, err := s.resolve(ctx, owner, id)
	if err != nil {
		return err
	}
	if target == ownerSpace {
		return ErrOwnerSpaceProtected
	}
	if err := s.assertSpaceOwned(ctx, ownerSpace, target); err != nil {
		return err
	}
	return s.client.DeleteSpace(ctx, target)
}

func (s *Service) ChildSpaces(ctx context.Context, owner string, id int64, onlySub bool, page tuya.Page) ([]int64, tuya.Page, error) {
	ownerSpace, target, err := s.resolve(ctx, owner, id)
	if err != nil {
		return nil, tuya.Page{}, err
	}
	if err := s.assertSpaceOwned(ctx, ownerSpace, target); err != nil {
		return nil, tuya.Page{}, err
	}
	return s.client.ListSpaces(ctx, target, onlySub, page)
}

func (s *Service) SpaceResources(ctx context.Context, owner string, id int64, onlySub bool, page tuya.Page) ([]tuya.Resource, tuya.Page, error) {
	ownerSpace, target, err := s.resolve(ctx, owner, id)
	if err != nil {
		return nil, tuya.Page{}, err
	}
	if err := s.assertSpaceOwned(ctx, ownerSpace, target); err != nil {
		return nil, tuya.Page{}, err
	}
	return s.client.SpaceResources(ctx, target, onlySub, page)
}

func (s *Service) SpaceDevices(ctx context.Context, owner string, id int64, lastID string, pageSize int) ([]tuya.SpaceDevice, error) {
	ownerSpace, target, err := s.resolve(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	if err := s.assertSpaceOwned(ctx, ownerSpace, target); err != nil {
		return nil, err
	}
	// recursive is false because Tuya's is_recursion does nothing here: a device is only ever
	// listed by the one space it is bound to, so the door has no truthful recursive form to offer.
	return s.client.SpaceDevices(ctx, []int64{target}, false, nil, nil, lastID, pageSize)
}

func (s *Service) ContainsSpace(ctx context.Context, owner string, id int64) (bool, error) {
	ownerSpace, target, err := s.resolve(ctx, owner, id)
	if err != nil {
		return false, err
	}
	if err := s.assertSpaceOwned(ctx, ownerSpace, target); err != nil {
		if errors.Is(err, ErrNotOwned) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

const (
	deviceScanPageSize = 200
	deviceScanMaxPages = 50
)

func (s *Service) ContainsDevice(ctx context.Context, owner, deviceID string) (bool, error) {
	ownerSpace, err := s.ownerSpace(ctx, owner)
	if err != nil {
		return false, err
	}
	page := tuya.Page{PageSize: deviceScanPageSize}
	for range deviceScanMaxPages {
		resources, next, err := s.client.SpaceResources(ctx, ownerSpace, false, page)
		if err != nil {
			return false, fmt.Errorf("scan resources of space %d: %w", ownerSpace, err)
		}
		for _, resource := range resources {
			if resource.Type == tuya.SpaceResourceDevice && resource.ID == deviceID {
				return true, nil
			}
		}
		if len(resources) == 0 || next.LastRowKey == 0 || next.LastRowKey == page.LastRowKey {
			return false, nil
		}
		page.LastRowKey = next.LastRowKey
	}
	return false, fmt.Errorf("scan resources of space %d: did not end after %d pages", ownerSpace, deviceScanMaxPages)
}

func (s *Service) ownerSpace(ctx context.Context, owner string) (int64, error) {
	space, err := s.store.Get(ctx, owner)
	if err != nil {
		return 0, err
	}
	if space.SpaceID == 0 {
		return 0, ErrNotLinked
	}
	return space.SpaceID, nil
}

func (s *Service) resolve(ctx context.Context, owner string, id int64) (ownerSpace, target int64, err error) {
	ownerSpace, err = s.ownerSpace(ctx, owner)
	if err != nil {
		return 0, 0, err
	}
	if id == 0 {
		return ownerSpace, ownerSpace, nil
	}
	return ownerSpace, id, nil
}

const CodeNoSpacePermission = 40001900

func (s *Service) assertSpaceOwned(ctx context.Context, ownerSpace, target int64) error {
	if target == ownerSpace {
		return nil
	}
	contains, err := s.client.SpaceRelation(ctx, ownerSpace, target)
	if err != nil {
		var apiErr *tuya.APIError
		if errors.As(err, &apiErr) && apiErr.Code == CodeNoSpacePermission {
			return ErrNotOwned
		}
		return fmt.Errorf("verify space ownership: %w", err)
	}
	if !contains {
		return ErrNotOwned
	}
	return nil
}
