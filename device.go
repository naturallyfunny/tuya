package tuya

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

type DataPoint struct {
	Code  string `json:"code"`
	Value any    `json:"value"`
}

// The two fields every device endpoint spells the same way. ProductID is not among them:
// UserDevices returns product_id, thing/space/device returns productId.
type Device struct {
	ID       string `json:"id"`
	Category string `json:"category"`
}

type UserDevice struct {
	Device
	// Name here is what the user renamed the device to; SpaceDevice.Name is the factory name
	// and puts the rename in CustomName instead.
	Name      string      `json:"name"`
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
	Device
	// Name here is the factory name; the user's rename is CustomName. UserDevice.Name is the
	// other way round.
	Name       string `json:"name"`
	CustomName string `json:"customName"`
	ProductID  string `json:"productId"`
	// BindSpaceID is which of the requested spaces the device sits in. Arrives as a string
	// even though every other space id in this API is a number.
	BindSpaceID string `json:"bindSpaceId"`
	Sub         bool   `json:"sub"`
	IsOnline    bool   `json:"isOnline"`
}

const SpaceDevicePageSizeMax = 20

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
	if pageSize == 0 {
		pageSize = SpaceDevicePageSizeMax
	}
	query.Set("page_size", strconv.Itoa(pageSize))
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

func (c *Client) UserHasDevice(ctx context.Context, tuyaUID, deviceID string) (bool, error) {
	devices, err := c.UserDevices(ctx, tuyaUID)
	if err != nil {
		return false, fmt.Errorf("list devices of user %s: %w", tuyaUID, err)
	}
	for _, device := range devices {
		if device.ID == deviceID {
			return true, nil
		}
	}
	return false, nil
}

func canHaveMultipleChannels(category string) bool {
	switch strings.ToLower(category) {
	case DeviceCategorySwitch,
		DeviceCategorySocket,
		DeviceCategoryPowerStrip,
		DeviceCategoryTDQ,
		DeviceCategoryIrrigator,
		DeviceCategorySceneSwitch,
		DeviceCategoryGarageDoorOpener,
		DeviceCategoryTemperatureHumiditySwitch,
		DeviceCategoryDimmerSwitch,
		DeviceCategoryDimmer,
		DeviceCategoryCurtain,
		DeviceCategoryCurtainSwitch,
		DeviceCategoryWirelessSwitch:
		return true
	}
	return false
}

func (c *Client) ChannelNames(ctx context.Context, devices []Device) (map[string][]Channel, error) {
	var targets []string
	for _, device := range devices {
		if device.ID != "" && canHaveMultipleChannels(device.Category) {
			targets = append(targets, device.ID)
		}
	}
	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		named = make(map[string][]Channel, len(targets))
		errs  []error
	)
	for _, deviceID := range targets {
		wg.Go(func() {
			channels, err := c.DeviceChannelNames(ctx, deviceID)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Errorf("device %s: %w", deviceID, err))
				return
			}
			if channels == nil {
				return
			}
			named[deviceID] = channels
		})
	}
	wg.Wait()
	return named, errors.Join(errs...)
}

const (
	DeviceCategoryACCharger                          = "qccdz"
	DeviceCategoryAccessControl                      = "mk"
	DeviceCategoryAirConditioner                     = "kt"
	DeviceCategoryAirConditionerController           = "ktkzq"
	DeviceCategoryAirFryer                           = "kqzg"
	DeviceCategoryAirPurifier                        = "kj"
	DeviceCategoryAirQualityMonitor                  = "hjjcy"
	DeviceCategoryAlarmHost                          = "mal"
	DeviceCategoryAmbianceLight                      = "fwd"
	DeviceCategoryAmbientLight                       = "fwl"
	DeviceCategoryAudioVideoLock                     = "photolock"
	DeviceCategoryBathroomHeater                     = "yb"
	DeviceCategoryBathtub                            = "yg"
	DeviceCategoryBentoBox                           = "znfh"
	DeviceCategoryBodyFatScale                       = "tzc1"
	DeviceCategoryBottleWarmer                       = "nnq"
	DeviceCategoryBreadMaker                         = "mb"
	DeviceCategoryBusinessLock                       = "gyms"
	DeviceCategoryCO2Detector                        = "co2bj"
	DeviceCategoryCODetector                         = "cobj"
	DeviceCategoryCardSwitch                         = "ckqdkg"
	DeviceCategoryCatToilet                          = "msp"
	DeviceCategoryCeilingFanLight                    = "fsd"
	DeviceCategoryCeilingLight                       = "xdd"
	DeviceCategoryCircuitBreaker                     = "dlq"
	DeviceCategoryCoffeeMaker                        = "kfj"
	DeviceCategoryContactSensor                      = "mcs"
	DeviceCategoryCookingThermometer                 = "swtz"
	DeviceCategoryCurtain                            = "cl"
	DeviceCategoryCurtainRobot                       = "jdcljqr"
	DeviceCategoryCurtainSwitch                      = "clkg"
	DeviceCategoryDehumidifier                       = "cs"
	DeviceCategoryDiffuser                           = "xxj"
	DeviceCategoryDimmer                             = "tgq"
	DeviceCategoryDimmerSwitch                       = "tgkg"
	DeviceCategoryDoorWindowController               = "mc"
	DeviceCategoryDryingRack                         = "lyj"
	DeviceCategoryElectricBlanket                    = "dr"
	DeviceCategoryElectricDesk                       = "sjz"
	DeviceCategoryElectricFireplace                  = "dbl"
	DeviceCategoryElectricityMeter                   = "zndb"
	DeviceCategoryEmergencyButton                    = "sos"
	DeviceCategoryFan                                = "fs"
	DeviceCategoryFanWallSwitch                      = "fskg"
	DeviceCategoryFilamentLight                      = "dsd"
	DeviceCategoryFingerbot                          = "szjqr"
	DeviceCategoryFormaldehydeDetector               = "jqbj"
	DeviceCategoryGarageDoorOpener                   = "ckmkzq"
	DeviceCategoryGasAlarm                           = "rqbj"
	DeviceCategoryGatewayControl                     = "wg2"
	DeviceCategoryHVAC                               = "ntq"
	DeviceCategoryHeater                             = "qn"
	DeviceCategoryHotelLock                          = "hotelms"
	DeviceCategoryHumanPresenceSensor                = "hps"
	DeviceCategoryHumidifier                         = "jsq"
	DeviceCategoryInductionCooker                    = "dcl"
	DeviceCategoryIrrigator                          = "ggq"
	DeviceCategoryJumpRope                           = "ts"
	DeviceCategoryLight                              = "dj"
	DeviceCategoryLockAccessory                      = "ms_category"
	DeviceCategoryLockWithCamera                     = "videolock"
	DeviceCategoryLowPowerCamera                     = "dghsxj"
	DeviceCategoryLuminanceSensor                    = "ldcg"
	DeviceCategoryMassageChair                       = "amy"
	DeviceCategoryMethaneDetector                    = "jwbj"
	DeviceCategoryMicroInverter                      = "znnbq"
	DeviceCategoryMicroStorageInverter               = "xnyjcn"
	DeviceCategoryMilkDispenser                      = "cn"
	DeviceCategoryMilkKettle                         = "tnq"
	DeviceCategoryMotionSensor                       = "pir"
	DeviceCategoryMotionSensorLight                  = "gyd"
	DeviceCategoryMultiFunctionalAlarm               = "dgnbj"
	DeviceCategoryOutdoorFloodLight                  = "tyd"
	DeviceCategoryPM25Detector                       = "pm25"
	DeviceCategoryPetBallThrower                     = "cwwqfsq"
	DeviceCategoryPetFeeder                          = "cwwsq"
	DeviceCategoryPetFountain                        = "cwysj"
	DeviceCategoryPetOdorEliminator                  = "cwjwq"
	DeviceCategoryPetTreatFeeder                     = "cwtswsq"
	DeviceCategoryPhysiotherapyProduct               = "liliao"
	DeviceCategoryPillBox                            = "znyh"
	DeviceCategoryPoolHeatPump                       = "znrb"
	DeviceCategoryPowerStrip                         = "pc"
	DeviceCategoryPressureSensor                     = "ylcg"
	DeviceCategoryProjector                          = "tyy"
	DeviceCategoryRefrigerator                       = "bx"
	DeviceCategoryRemoteControl                      = "ykq"
	DeviceCategoryResidentialLock                    = "ms"
	DeviceCategoryResidentialLockPro                 = "jtmspro"
	DeviceCategoryRiceCabinet                        = "mg"
	DeviceCategoryRobotVacuum                        = "sd"
	DeviceCategorySafeBox                            = "bxx"
	DeviceCategorySceneSwitch                        = "cjkg"
	DeviceCategorySinglePhasePowerMeter              = "aqcz"
	DeviceCategorySirenAlarm                         = "sgbj"
	DeviceCategorySmartCamera                        = "sp"
	DeviceCategorySmartIndoorGarden                  = "sz"
	DeviceCategorySmartKettle                        = "bh"
	DeviceCategorySmartLockKeepAlive                 = "jtmsbh"
	DeviceCategorySmokeAlarm                         = "ywbj"
	DeviceCategorySocket                             = "cz"
	DeviceCategorySofa                               = "sf"
	DeviceCategorySoilSensor                         = "zwjcy"
	DeviceCategorySolarLight                         = "tyndj"
	DeviceCategorySousVideCooker                     = "mzj"
	DeviceCategoryStringLights                       = "dc"
	DeviceCategoryStripLights                        = "dd"
	DeviceCategorySwitch                             = "kg"
	DeviceCategoryTVSet                              = "ds"
	DeviceCategoryTankLevelSensor                    = "ywcgq"
	DeviceCategoryTDQ                                = "tdq"
	DeviceCategoryTemperatureHumiditySensor          = "wsdcg"
	DeviceCategoryTemperatureHumiditySensorWithProbe = "qxj"
	DeviceCategoryTemperatureHumiditySwitch          = "wkcz"
	DeviceCategoryThermostat                         = "wk"
	DeviceCategoryThermostaticRadiatorValve          = "wkf"
	DeviceCategoryTowelRack                          = "mjj"
	DeviceCategoryTowerFan                           = "ks"
	DeviceCategoryTracker                            = "tracker"
	DeviceCategoryVOCDetector                        = "voc"
	DeviceCategoryVentilationSystem                  = "xfj"
	DeviceCategoryVibrationSensor                    = "zd"
	DeviceCategoryWakeUpLight                        = "hxd"
	DeviceCategoryWallHungBoiler                     = "bgl"
	DeviceCategoryWashingMachine                     = "xy"
	DeviceCategoryWatchBand                          = "sb"
	DeviceCategoryWaterHeater                        = "rs"
	DeviceCategoryWaterLeakDetector                  = "sj"
	DeviceCategoryWaterMeter                         = "znsb"
	DeviceCategoryWaterPurifier                      = "js"
	DeviceCategoryWaterTester                        = "szjcy"
	DeviceCategoryWaterTimer                         = "sfkzq"
	DeviceCategoryWiFiIRRemote                       = "wnykq"
	DeviceCategoryWirelessSwitch                     = "wxkg"
)
