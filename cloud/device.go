package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type Channel struct {
	Identifier string `json:"identifier"`
	Name       string `json:"name"`
}

type DataPoint struct {
	Code  string `json:"code"`
	Value any    `json:"value"`
}

type Device struct {
	ID              string      `json:"id"`
	Category        string      `json:"category"`
	Name            string      `json:"name"`
	Status          []DataPoint `json:"status"`
	CodeNameMapping []Channel   `json:"code_name_mapping"`
}

func (c *IoT) ListDevices(ctx context.Context, tuyaUID string) ([]Device, error) {
	raw, err := c.client.Do(ctx, http.MethodGet, fmt.Sprintf("/v1.0/users/%s/devices", tuyaUID), nil)
	if err != nil {
		return nil, err
	}
	var devices []Device
	if err := json.Unmarshal(raw, &devices); err != nil {
		return nil, fmt.Errorf("failed to unmarshal device list: %w", err)
	}
	return devices, nil
}

func (c *IoT) DeviceStatus(ctx context.Context, deviceID string) ([]DataPoint, error) {
	raw, err := c.client.Do(ctx, http.MethodGet, fmt.Sprintf("/v1.0/iot-03/devices/%s/status", deviceID), nil)
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

func (c *IoT) SendCommands(ctx context.Context, deviceID string, commands []DataPoint) error {
	body, err := json.Marshal(struct {
		Commands []DataPoint `json:"commands"`
	}{Commands: commands})
	if err != nil {
		return fmt.Errorf("failed to marshal command payload: %w", err)
	}
	if _, err := c.client.Do(ctx, http.MethodPost, fmt.Sprintf("/v1.0/iot-03/devices/%s/commands", deviceID), body); err != nil {
		return fmt.Errorf("failed to send commands: %w", err)
	}
	return nil
}

func (c *IoT) DeviceChannelNames(ctx context.Context, deviceID string) ([]Channel, error) {
	raw, err := c.client.Do(ctx, http.MethodGet, fmt.Sprintf("/v1.0/devices/%s/multiple-names", deviceID), nil)
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
