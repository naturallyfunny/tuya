package tuya

import (
	"context"
	"time"
)

// Account is the link between an opaque owner ID (whatever the consumer uses to
// identify a human) and that human's Tuya account UID. Devices are listed and
// controlled under the UID.
type Account struct {
	OwnerID   string    `json:"owner_id"`
	TuyaUID   string    `json:"tuya_uid"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Account returns the Tuya account linked to ownerID, or ErrAccountNotLinked if
// the human hasn't linked one yet.
func (c *Client) Account(ctx context.Context, ownerID string) (Account, error) {
	return c.accountStore.Get(ctx, ownerID)
}
