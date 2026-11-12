package provider_test

import (
	"regexp"
	"testing"

	"github.com/Fabianoshz/terraform-provider-homeassistant/internal/testutil"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestEntityResource_basic(t *testing.T) {
	deviceID := "device-1"
	srv := testutil.NewMockHAServer(t, testutil.MockServerConfig{
		Token: "test-token",
		EntityRegistry: []testutil.MockEntityRegistry{
			{EntityID: "sensor.temperature", DeviceID: &deviceID, Platform: "esphome"},
		},
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories(),
		Steps: []resource.TestStep{
			{
				Config: providerConfig(srv.URL, "test-token") + `
resource "homeassistant_entity" "temp" {
  device_id = "device-1"
  entity_id = "sensor.temperature"
  name      = "Living Room Temperature"
  icon      = "mdi:thermometer"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("homeassistant_entity.temp", "id", "sensor.temperature"),
					resource.TestCheckResourceAttr("homeassistant_entity.temp", "name", "Living Room Temperature"),
					resource.TestCheckResourceAttr("homeassistant_entity.temp", "icon", "mdi:thermometer"),
					resource.TestCheckResourceAttr("homeassistant_entity.temp", "enabled", "true"),
				),
			},
		},
	})
}

func TestEntityResource_withArea(t *testing.T) {
	deviceID := "device-1"
	srv := testutil.NewMockHAServer(t, testutil.MockServerConfig{
		Token: "test-token",
		Areas: []testutil.MockArea{
			{AreaID: "kitchen", Name: "Kitchen"},
		},
		EntityRegistry: []testutil.MockEntityRegistry{
			{EntityID: "sensor.temperature", DeviceID: &deviceID, Platform: "esphome"},
		},
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories(),
		Steps: []resource.TestStep{
			{
				Config: providerConfig(srv.URL, "test-token") + `
resource "homeassistant_entity" "temp" {
  device_id = "device-1"
  entity_id = "sensor.temperature"
  area_id   = "kitchen"
}
`,
				Check: resource.TestCheckResourceAttr("homeassistant_entity.temp", "area_id", "kitchen"),
			},
		},
	})
}

func TestEntityResource_disable(t *testing.T) {
	deviceID := "device-1"
	srv := testutil.NewMockHAServer(t, testutil.MockServerConfig{
		Token: "test-token",
		EntityRegistry: []testutil.MockEntityRegistry{
			{EntityID: "sensor.temperature", DeviceID: &deviceID, Platform: "esphome"},
		},
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories(),
		Steps: []resource.TestStep{
			{
				Config: providerConfig(srv.URL, "test-token") + `
resource "homeassistant_entity" "temp" {
  device_id = "device-1"
  entity_id = "sensor.temperature"
  enabled   = false
}
`,
				Check: resource.TestCheckResourceAttr("homeassistant_entity.temp", "enabled", "false"),
			},
		},
	})
}

func TestEntityResource_devicelessExposure(t *testing.T) {
	// A script has no device; device_id is omitted and exposure is managed.
	srv := testutil.NewMockHAServer(t, testutil.MockServerConfig{
		Token: "test-token",
		EntityRegistry: []testutil.MockEntityRegistry{
			{EntityID: "script.bedtime", Platform: "script"},
		},
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories(),
		Steps: []resource.TestStep{
			{
				Config: providerConfig(srv.URL, "test-token") + `
resource "homeassistant_entity" "script" {
  entity_id  = "script.bedtime"
  exposed_to = ["conversation"]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("homeassistant_entity.script", "entity_id", "script.bedtime"),
					resource.TestCheckResourceAttr("homeassistant_entity.script", "exposed_to.#", "1"),
					resource.TestCheckResourceAttr("homeassistant_entity.script", "exposed_to.0", "conversation"),
				),
			},
		},
	})
}

func TestEntityResource_update(t *testing.T) {
	deviceID := "device-1"
	srv := testutil.NewMockHAServer(t, testutil.MockServerConfig{
		Token: "test-token",
		EntityRegistry: []testutil.MockEntityRegistry{
			{EntityID: "sensor.temperature", DeviceID: &deviceID, Platform: "esphome"},
		},
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories(),
		Steps: []resource.TestStep{
			{
				Config: providerConfig(srv.URL, "test-token") + `
resource "homeassistant_entity" "temp" {
  device_id = "device-1"
  entity_id = "sensor.temperature"
  name      = "Old Name"
}
`,
				Check: resource.TestCheckResourceAttr("homeassistant_entity.temp", "name", "Old Name"),
			},
			{
				Config: providerConfig(srv.URL, "test-token") + `
resource "homeassistant_entity" "temp" {
  device_id = "device-1"
  entity_id = "sensor.temperature"
  name      = "New Name"
  icon      = "mdi:thermometer"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("homeassistant_entity.temp", "name", "New Name"),
					resource.TestCheckResourceAttr("homeassistant_entity.temp", "icon", "mdi:thermometer"),
				),
			},
		},
	})
}

func TestEntityResource_value(t *testing.T) {
	deviceID := "device-1"
	srv := testutil.NewMockHAServer(t, testutil.MockServerConfig{
		Token: "test-token",
		EntityRegistry: []testutil.MockEntityRegistry{
			{EntityID: "number.humidity_offset", DeviceID: &deviceID, Platform: "esphome"},
			{EntityID: "select.sensitivity", DeviceID: &deviceID, Platform: "zha"},
		},
		EntityStates: []testutil.MockEntityState{
			{EntityID: "number.humidity_offset", State: "0.0"},
			{EntityID: "select.sensitivity", State: "High"},
		},
	})

	config := func(offset, sensitivity string) string {
		return providerConfig(srv.URL, "test-token") + `
resource "homeassistant_entity" "offset" {
  device_id = "device-1"
  entity_id = "number.humidity_offset"
  value     = "` + offset + `"
}

resource "homeassistant_entity" "sensitivity" {
  device_id = "device-1"
  entity_id = "select.sensitivity"
  value     = "` + sensitivity + `"
}
`
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories(),
		Steps: []resource.TestStep{
			{
				Config: config("-2.6", "Medium"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("homeassistant_entity.offset", "value", "-2.6"),
					resource.TestCheckResourceAttr("homeassistant_entity.sensitivity", "value", "Medium"),
				),
			},
			{
				Config: config("1", "Low"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("homeassistant_entity.offset", "value", "1"),
					resource.TestCheckResourceAttr("homeassistant_entity.sensitivity", "value", "Low"),
				),
			},
		},
	})
}

// HA formatting a number differently from the config is not drift.
func TestEntityResource_valueNumericFormat(t *testing.T) {
	deviceID := "device-1"
	srv := testutil.NewMockHAServer(t, testutil.MockServerConfig{
		Token: "test-token",
		EntityRegistry: []testutil.MockEntityRegistry{
			{EntityID: "number.humidity_offset", DeviceID: &deviceID, Platform: "esphome"},
		},
		EntityStates: []testutil.MockEntityState{
			{EntityID: "number.humidity_offset", State: "-2.60"},
		},
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories(),
		Steps: []resource.TestStep{
			{
				Config: providerConfig(srv.URL, "test-token") + `
resource "homeassistant_entity" "offset" {
  device_id = "device-1"
  entity_id = "number.humidity_offset"
  value     = "-2.6"
}
`,
				Check: resource.TestCheckResourceAttr("homeassistant_entity.offset", "value", "-2.6"),
			},
		},
	})
}

func TestEntityResource_valueUnsupportedDomain(t *testing.T) {
	deviceID := "device-1"
	srv := testutil.NewMockHAServer(t, testutil.MockServerConfig{
		Token: "test-token",
		EntityRegistry: []testutil.MockEntityRegistry{
			{EntityID: "sensor.temperature", DeviceID: &deviceID, Platform: "esphome"},
		},
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providerFactories(),
		Steps: []resource.TestStep{
			{
				Config: providerConfig(srv.URL, "test-token") + `
resource "homeassistant_entity" "temp" {
  device_id = "device-1"
  entity_id = "sensor.temperature"
  value     = "21"
}
`,
				ExpectError: regexp.MustCompile(`value can't be set on "sensor" entities`),
			},
		},
	})
}
