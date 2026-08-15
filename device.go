package tuya

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type DataPoint struct {
	Code  string `json:"code"`
	Value any    `json:"value"`
}

type UserDevice struct {
	ID string `json:"id"`
	// Name here is what the user renamed the device to; SpaceDevice.Name is the factory name
	// and puts the rename in CustomName instead.
	Name      string      `json:"name"`
	Category  string      `json:"category"`
	ProductID string      `json:"product_id"`
	Sub       bool        `json:"sub"`
	Online    bool        `json:"online"`
	Status    []DataPoint `json:"status"`
}

func (c *Client) UserDevices(ctx context.Context, tuyaUID string) ([]UserDevice, error) {
	raw, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("/v1.0/users/%s/devices", tuyaUID), nil)
	if err != nil {
		return nil, err
	}
	var devices []UserDevice
	if err := json.Unmarshal(raw, &devices); err != nil {
		return nil, fmt.Errorf("failed to unmarshal device list: %w", err)
	}
	return devices, nil
}

type SpaceDevice struct {
	ID string `json:"id"`
	// Name here is the factory name; the user's rename is CustomName. UserDevice.Name is the
	// other way round.
	Name       string `json:"name"`
	CustomName string `json:"customName"`
	Category   string `json:"category"`
	ProductID  string `json:"productId"`
	// BindSpaceID is which of the requested spaces the device sits in. Arrives as a string
	// even though every other space id in this API is a number.
	BindSpaceID string `json:"bindSpaceId"`
	Sub         bool   `json:"sub"`
	IsOnline    bool   `json:"isOnline"`
}

func (c *Client) SpaceDevices(ctx context.Context, spaceIDs []int64, recursive bool, productIDs, categories []string, lastID string, pageSize int) ([]SpaceDevice, error) {
	ids := make([]string, len(spaceIDs))
	for i, id := range spaceIDs {
		ids[i] = strconv.FormatInt(id, 10)
	}
	query := url.Values{}
	query.Set("space_ids", strings.Join(ids, ","))
	query.Set("is_recursion", strconv.FormatBool(recursive))
	if len(productIDs) > 0 {
		query.Set("product_ids", strings.Join(productIDs, ","))
	}
	if len(categories) > 0 {
		query.Set("categories", strings.Join(categories, ","))
	}
	if lastID != "" {
		query.Set("last_id", lastID)
	}
	if pageSize != 0 {
		query.Set("page_size", strconv.Itoa(pageSize))
	}
	path := fmt.Sprintf("/v2.0/cloud/thing/space/device?%s", query.Encode())
	raw, err := c.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var devices []SpaceDevice
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &devices); err != nil {
			return nil, fmt.Errorf("failed to unmarshal the device list of spaces %s: %w", query.Get("space_ids"), err)
		}
	}
	return devices, nil
}

func (c *Client) DeviceStatus(ctx context.Context, deviceID string) ([]DataPoint, error) {
	raw, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("/v1.0/iot-03/devices/%s/status", deviceID), nil)
	if err != nil {
		return nil, err
	}
	var status []DataPoint
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &status); err != nil {
			return nil, fmt.Errorf("failed to unmarshal device status: %w", err)
		}
	}
	return status, nil
}

func (c *Client) SendCommands(ctx context.Context, deviceID string, commands []DataPoint) error {
	body, err := json.Marshal(struct {
		Commands []DataPoint `json:"commands"`
	}{Commands: commands})
	if err != nil {
		return fmt.Errorf("failed to marshal command payload: %w", err)
	}
	if _, err := c.Do(ctx, http.MethodPost, fmt.Sprintf("/v1.0/iot-03/devices/%s/commands", deviceID), body); err != nil {
		return fmt.Errorf("failed to send commands: %w", err)
	}
	return nil
}

type Channel struct {
	Identifier string `json:"identifier"`
	Name       string `json:"name"`
}

func (c *Client) DeviceChannelNames(ctx context.Context, deviceID string) ([]Channel, error) {
	raw, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("/v1.0/devices/%s/multiple-names", deviceID), nil)
	if err != nil {
		return nil, err
	}
	var channels []Channel
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &channels); err != nil {
			return nil, fmt.Errorf("failed to decode channels for device %s: %w", deviceID, err)
		}
	}
	return channels, nil
}
