package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// DataPoint is a single Tuya data point (DP): a capability code paired with its
// value. It is both how a device reports its state and how it is told to change
// — e.g. {Code: "switch_1", Value: true}.
type DataPoint struct {
	Code  string `json:"code"`
	Value any    `json:"value"`
}

// Channel names one switch/outlet of a multi-gang device, mapping its DP
// identifier (e.g. "switch_1") to the human-given label (e.g. "Kitchen light").
type Channel struct {
	Identifier string `json:"identifier"`
	Name       string `json:"name"`
}

// Device is a Tuya device with its current state.
//
// CodeNameMapping is never populated by ListDevices. Tuya serves channel labels
// from a separate endpoint (DeviceChannelNames), and deciding which devices are
// worth that extra request is a judgement about Tuya's catalogue that this layer
// does not make. It is left nil for callers that compose the two — see
// tuya.Client.ListDevices.
type Device struct {
	ID              string      `json:"id"`
	Category        string      `json:"category"`
	Name            string      `json:"name"`
	Status          []DataPoint `json:"status"`
	CodeNameMapping []Channel   `json:"code_name_mapping"`
}

// ListDevices returns every device on the account, each with its current
// status. One request: GET /v1.0/users/{uid}/devices.
func (c *IoT) ListDevices(ctx context.Context, tuyaUID string) ([]Device, error) {
	path := fmt.Sprintf("/v1.0/users/%s/devices", tuyaUID)
	raw, err := c.client.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var devices []Device
	if err := json.Unmarshal(raw, &devices); err != nil {
		return nil, fmt.Errorf("failed to unmarshal device list: %w", err)
	}
	return devices, nil
}

// DeviceStatus reads the current status (DPs) of a device. This layer is
// trusted and device-addressed: the caller is responsible for verifying the
// device belongs to whoever asked (see tuya.Client).
func (c *IoT) DeviceStatus(ctx context.Context, deviceID string) ([]DataPoint, error) {
	path := fmt.Sprintf("/v1.0/iot-03/devices/%s/status", deviceID)
	raw, err := c.client.Do(ctx, http.MethodGet, path, nil)
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

// SendCommands sends DP commands to a device. This layer is trusted and
// device-addressed: the caller is responsible for verifying the device belongs
// to whoever asked (see tuya.Client).
func (c *IoT) SendCommands(ctx context.Context, deviceID string, commands []DataPoint) error {
	path := fmt.Sprintf("/v1.0/iot-03/devices/%s/commands", deviceID)
	body, err := json.Marshal(struct {
		Commands []DataPoint `json:"commands"`
	}{Commands: commands})
	if err != nil {
		return fmt.Errorf("failed to marshal command payload: %w", err)
	}
	if _, err := c.client.Do(ctx, http.MethodPost, path, body); err != nil {
		return fmt.Errorf("failed to send commands: %w", err)
	}
	return nil
}

// DeviceChannelNames returns the human-given label for each switch/outlet
// channel of a device. One request: GET /v1.0/devices/{device_id}/multiple-names.
// Devices that have no channels come back with an empty list rather than an
// error, so a caller may ask about any device.
func (c *IoT) DeviceChannelNames(ctx context.Context, deviceID string) ([]Channel, error) {
	path := fmt.Sprintf("/v1.0/devices/%s/multiple-names", deviceID)
	raw, err := c.client.Do(ctx, http.MethodGet, path, nil)
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
