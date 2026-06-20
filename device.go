package tuya

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
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

// Device is a Tuya device with its current state. CodeNameMapping is populated
// for multi-gang switches/outlets so each channel's DP code carries the human's
// label; it is empty for devices where it doesn't apply.
type Device struct {
	ID              string      `json:"id"`
	Category        string      `json:"category"`
	Name            string      `json:"name"`
	Status          []DataPoint `json:"status"`
	CodeNameMapping []Channel   `json:"code_name_mapping"`
}

// ListDevices returns every device on the account, each with its current
// status. Multi-gang switches/outlets are enriched with per-channel names.
func (c *IoTClient) ListDevices(ctx context.Context, tuyaUID string) ([]Device, error) {
	devices, err := c.listDevices(ctx, tuyaUID)
	if err != nil {
		return nil, err
	}

	if len(devices) == 0 {
		return []Device{}, nil
	}

	if err := c.enrichDevices(ctx, devices); err != nil {
		return nil, fmt.Errorf("failed to enrich devices: %w", err)
	}

	return devices, nil
}

// DeviceStatus reads the current status (DPs) of a device. The caller is
// responsible for verifying ownership before calling (see app.Client).
func (c *IoTClient) DeviceStatus(ctx context.Context, deviceID string) ([]DataPoint, error) {
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

// SendCommands sends DP commands to a device. The caller is responsible for
// verifying ownership before calling (see app.Client).
func (c *IoTClient) SendCommands(ctx context.Context, deviceID string, commands []DataPoint) error {
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

// listDevices fetches the raw device list for a Tuya UID.
func (c *IoTClient) listDevices(ctx context.Context, tuyaUID string) ([]Device, error) {
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

// HasDevice reports whether deviceID appears in the account's device list.
// It uses the raw (unenriched) list — no channel-name fetches — so it is lean
// enough for an ownership membership check.
func (c *IoTClient) HasDevice(ctx context.Context, tuyaUID, deviceID string) (bool, error) {
	devices, err := c.listDevices(ctx, tuyaUID)
	if err != nil {
		return false, fmt.Errorf("verify device ownership: %w", err)
	}
	for _, d := range devices {
		if d.ID == deviceID {
			return true, nil
		}
	}
	return false, nil
}

// enrichDevices fills CodeNameMapping for multi-gang switches/outlets (category
// "kg" or "cz*") by fetching each one's channel names concurrently. Other
// devices get an empty, non-nil mapping.
func (c *IoTClient) enrichDevices(ctx context.Context, devices []Device) error {
	var devicesToEnrich []*Device
	for idx := range devices {
		device := &devices[idx]
		category := strings.ToLower(device.Category)
		device.CodeNameMapping = []Channel{}

		if (category == "kg" || strings.HasPrefix(category, "cz")) && device.ID != "" {
			devicesToEnrich = append(devicesToEnrich, device)
		}
	}

	if len(devicesToEnrich) == 0 {
		return nil
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	for _, device := range devicesToEnrich {
		wg.Go(func() {
			path := fmt.Sprintf("/v1.0/devices/%s/multiple-names", device.ID)
			raw, err := c.client.Do(ctx, http.MethodGet, path, nil)
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("failed to get channel name for device %s: %w", device.ID, err))
				mu.Unlock()
				return
			}

			var channels []Channel
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &channels); err != nil {
					mu.Lock()
					errs = append(errs, fmt.Errorf("failed to decode channels for device %s: %w", device.ID, err))
					mu.Unlock()
					return
				}
			}
			device.CodeNameMapping = channels
		})
	}

	wg.Wait()

	return errors.Join(errs...)
}
