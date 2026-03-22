// Copyright 2018-present the CoreDHCP Authors. All rights reserved
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree.

package classify

import (
	"net"
	"testing"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv6"
	dhcpIana "github.com/insomniacslk/dhcp/iana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	// Test loading a valid configuration
	state, err := loadConfig("config.example.yaml")
	assert.NoError(t, err, "Should load example config")
	assert.NotNil(t, state, "State should not be nil")
	assert.NotEmpty(t, state.classes, "Should have loaded classes")
}

func TestLoadConfigInvalidFile(t *testing.T) {
	_, err := loadConfig("nonexistent.yaml")
	assert.Error(t, err, "Should error on nonexistent file")
}

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		pattern  string
		expected bool
	}{
		{"exact match", "android", "android", true},
		{"prefix wildcard", "android-dhcp-7", "android*", true},
		{"suffix wildcard", "my-android", "*android", true},
		{"middle wildcard", "android-test-7", "android*7", true},
		{"multiple wildcards", "a-b-c", "*-*-*", true},
		{"no match", "iphone", "android*", false},
		{"empty pattern", "test", "", true},
		{"match all", "anything", "*", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchPattern(tt.text, tt.pattern)
			assert.Equal(t, tt.expected, result, "Pattern matching result")
		})
	}
}

func TestMatchClassByMAC(t *testing.T) {
	state := &PluginState{
		classes: []ClassConfig{
			{
				Name: "android",
				Conditions: ConditionConfig{
					VendorClassMatch: []string{"android*"},
				},
			},
			{
				Name: "test-mac",
				Conditions: ConditionConfig{
					MACExact: []string{"aa:bb:cc:dd:ee:ff"},
				},
			},
			{
				Name: "test-prefix",
				Conditions: ConditionConfig{
					MACPrefix: []string{"02:42"},
				},
			},
		},
	}

	tests := []struct {
		name     string
		mac      string
		expected string
	}{
		{"exact match", "aa:bb:cc:dd:ee:ff", "test-mac"},
		{"prefix match", "02:42:ac:11:00:02", "test-prefix"},
		{"case insensitive exact", "AA:BB:CC:DD:EE:FF", "test-mac"},
		{"case insensitive prefix", "02:42:ac:11:00:02", "test-prefix"},
		{"no match", "00:11:22:33:44:55", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mac, err := net.ParseMAC(tt.mac)
			require.NoError(t, err, "Should parse MAC address")
			result := state.MatchClientByMAC(mac)
			assert.Equal(t, tt.expected, result, "Class name should match")
		})
	}
}

func TestMatchClassByVendorClass(t *testing.T) {
	state := &PluginState{
		classes: []ClassConfig{
			{
				Name: "android",
				Conditions: ConditionConfig{
					VendorClassMatch: []string{"android*"},
				},
			},
			{
				Name: "windows",
				Conditions: ConditionConfig{
					VendorClass: []string{"MSFT"},
				},
			},
		},
	}

	tests := []struct {
		name        string
		vendorClass string
		expected    string
	}{
		{"android wildcard", "android-dhcp-7", "android"},
		{"android exact", "android", "android"},
		{"windows exact", "MSFT", "windows"},
		{"no match", "apple", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := state.MatchClientByVendorClass(tt.vendorClass)
			assert.Equal(t, tt.expected, result, "Class name should match")
		})
	}
}

func TestMatchClassByDUID(t *testing.T) {
	state := &PluginState{
		classes: []ClassConfig{
			{
				Name: "test-prefix",
				Conditions: ConditionConfig{
					DUIDPrefix: []string{"00010001"},
				},
			},
			{
				Name: "test-exact",
				Conditions: ConditionConfig{
					DUIDExact: []string{"0001000123456789"},
				},
			},
		},
	}

	tests := []struct {
		name     string
		duidHex  string
		expected string
	}{
		{"prefix match", "000100012345", "test-prefix"},
		{"exact match", "0001000123456789", "test-prefix"}, // test-prefix comes first, so it matches by prefix
		{"case insensitive", "000100012345", "test-prefix"},
		{"no match", "00030001", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := state.MatchClientByDUID(tt.duidHex)
			assert.Equal(t, tt.expected, result, "Class name should match")
		})
	}
}

func TestHandler6(t *testing.T) {
	state := &PluginState{
		classes: []ClassConfig{
			{
				Name: "android",
				Conditions: ConditionConfig{
					VendorClassMatch: []string{"android*"},
				},
			},
		},
	}

	// Create a DHCPv6 request with Android vendor class
	req, err := dhcpv6.NewMessage()
	require.NoError(t, err, "Should create message")

	// Add vendor class option with enterprise number + data
	req.AddOption(&dhcpv6.OptVendorClass{
		EnterpriseNumber: 0,
		Data:             [][]byte{[]byte("android-dhcp-7")},
	})

	// Add ClientID
	req.AddOption(dhcpv6.OptClientID(&dhcpv6.DUIDLL{
		HWType:        dhcpIana.HWTypeEthernet,
		LinkLayerAddr: net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
	}))

	resp, err := dhcpv6.NewMessage()
	require.NoError(t, err, "Should create response")

	result, stop := state.Handler6(req, resp)
	assert.NotNil(t, result, "Result should not be nil")
	assert.False(t, stop, "Should not stop processing")

	// Check that classification was stored by looking at the map
	// DUID-LL format: Type(2) + HWType(2) + LLAddr(6) = 00 03 00 01 aa bb cc dd ee ff
	duid, err := dhcpv6.DUIDFromBytes([]byte{0, 0x03, 0, 0x01, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff})
	require.NoError(t, err)
	className := GetClassForDUID(duid)
	assert.Equal(t, "android", className, "Class name should match")
}

func TestHandler4(t *testing.T) {
	state := &PluginState{
		classes: []ClassConfig{
			{
				Name: "test-mac",
				Conditions: ConditionConfig{
					MACExact: []string{"aa:bb:cc:dd:ee:ff"},
				},
			},
		},
	}

	req := &dhcpv4.DHCPv4{
		ClientHWAddr: net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
	}

	resp := &dhcpv4.DHCPv4{}

	result, stop := state.Handler4(req, resp)
	assert.NotNil(t, result, "Result should not be nil")
	assert.False(t, stop, "Should not stop processing")

	// Check that classification was stored by looking at the map
	className := GetClassForMAC(req.ClientHWAddr)
	assert.Equal(t, "test-mac", className, "Class name should match")
}

func TestExtractMACFromDUID(t *testing.T) {
	tests := []struct {
		name     string
		duid     []byte
		expected string
	}{
		{
			name:     "DUID-LLT",
			duid:     []byte{0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
			expected: "aa:bb:cc:dd:ee:ff",
		},
		{
			name:     "DUID-LL",
			duid:     []byte{0x00, 0x03, 0x00, 0x01, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
			expected: "aa:bb:cc:dd:ee:ff",
		},
		{
			name:     "DUID-UUID",
			duid:     []byte{0x00, 0x04, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x10},
			expected: "",
		},
		{
			name:     "DUID-LLT too short for MAC",
			duid:     []byte{0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0xaa, 0xbb}, // Valid DUID-LLT but MAC is incomplete
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			duid, err := dhcpv6.DUIDFromBytes(tt.duid)
			require.NoError(t, err, "Should create DUID")
			result := extractMACFromDUID(duid)
			if tt.expected == "" {
				assert.Nil(t, result, "Should not extract MAC")
			} else {
				assert.NotNil(t, result, "Should extract MAC")
				assert.Equal(t, tt.expected, result.String(), "MAC should match")
			}
		})
	}
}

func TestExtractMACFromInvalidDUID(t *testing.T) {
	// Test that extractMACFromDUID handles nil DUID gracefully
	result := extractMACFromDUID(nil)
	assert.Nil(t, result, "Should return nil for nil DUID")
}

func TestPluginRegistration(t *testing.T) {
	assert.Equal(t, "classify", Plugin.Name, "Plugin name should match")
	assert.NotNil(t, Plugin.Setup6, "Setup6 should be defined")
	assert.NotNil(t, Plugin.Setup4, "Setup4 should be defined")
}

func TestMatchClassPriority(t *testing.T) {
	state := &PluginState{
		classes: []ClassConfig{
			{
				Name: "first",
				Conditions: ConditionConfig{
					VendorClass: []string{"test"},
				},
			},
			{
				Name: "second",
				Conditions: ConditionConfig{
					VendorClass: []string{"test"},
				},
			},
		},
	}

	info := &ClientInfo{
		VendorClass: "test",
	}

	result := state.matchClass(info)
	assert.Equal(t, "first", result, "Should match first class")
}

func TestGetClassMAC(t *testing.T) {
	state := &PluginState{
		classes: []ClassConfig{
			{
				Name: "test",
				Conditions: ConditionConfig{
					MACExact: []string{"aa:bb:cc:dd:ee:ff"},
				},
			},
		},
	}

	result := state.GetClassMAC("test")
	assert.NotNil(t, result, "Should return MAC")
	assert.Equal(t, "aa:bb:cc:dd:ee:ff", result.String(), "MAC should match")

	// Test non-existent class
	result = state.GetClassMAC("nonexistent")
	assert.Nil(t, result, "Should return nil for non-existent class")
}

func TestMatchClassWithMultipleConditions(t *testing.T) {
	state := &PluginState{
		classes: []ClassConfig{
			{
				Name: "multi-condition",
				Conditions: ConditionConfig{
					VendorClass: []string{"test"},
					MACPrefix:   []string{"aa:bb"},
				},
			},
		},
	}

	// Should match if any condition is true (OR logic)
	info1 := &ClientInfo{
		VendorClass: "test",
		MAC:         net.HardwareAddr{0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11},
	}
	result := state.matchClass(info1)
	assert.Equal(t, "multi-condition", result, "Should match with vendor class")

	info2 := &ClientInfo{
		VendorClass: "other",
		MAC:         net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
	}
	result = state.matchClass(info2)
	assert.Equal(t, "multi-condition", result, "Should match with MAC prefix")

	// No match
	info3 := &ClientInfo{
		VendorClass: "other",
		MAC:         net.HardwareAddr{0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11},
	}
	result = state.matchClass(info3)
	assert.Equal(t, "", result, "Should not match")
}

func TestIsExactMatch(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		values   []string
		expected bool
	}{
		{"match", "test", []string{"test", "other"}, true},
		{"case insensitive", "TEST", []string{"test"}, true},
		{"no match", "other", []string{"test", "value"}, false},
		{"empty list", "test", []string{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isExactMatch(tt.target, tt.values)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMatchClassWithUserClasses(t *testing.T) {
	state := &PluginState{
		classes: []ClassConfig{
			{
				Name: "pxe-client",
				Conditions: ConditionConfig{
					UserClass: []string{"PXEClient"},
				},
			},
		},
	}

	info := &ClientInfo{
		UserClasses: []string{"PXEClient", "other"},
	}

	result := state.matchClass(info)
	assert.Equal(t, "pxe-client", result, "Should match user class")
}

func TestMatchClassWithArchType(t *testing.T) {
	archType := uint16(182) // ARM64
	state := &PluginState{
		classes: []ClassConfig{
			{
				Name: "arm64",
				Conditions: ConditionConfig{
					ArchType: []uint16{182},
				},
			},
		},
	}

	info := &ClientInfo{
		ArchType: &archType,
	}

	result := state.matchClass(info)
	assert.Equal(t, "arm64", result, "Should match architecture type")
}

func TestMatchClassWithInterfaceID(t *testing.T) {
	state := &PluginState{
		classes: []ClassConfig{
			{
				Name: "interface-specific",
				Conditions: ConditionConfig{
					InterfaceID: []string{"0000abcd"},
				},
			},
		},
	}

	info := &ClientInfo{
		InterfaceID: []byte{0x00, 0x00, 0xab, 0xcd},
	}

	result := state.matchClass(info)
	assert.Equal(t, "interface-specific", result, "Should match interface ID")
}

func TestGetClassForMACString(t *testing.T) {
	mac := net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	SetClassForMAC(mac, "test-class")

	result := GetClassForMACString("aa:bb:cc:dd:ee:ff")
	assert.Equal(t, "test-class", result, "Should retrieve class by MAC string")

	// Test case insensitive lookup
	result = GetClassForMACString("AA:BB:CC:DD:EE:FF")
	assert.Equal(t, "test-class", result, "Should retrieve class with uppercase MAC string")
}

func TestGetClassForDUIDString(t *testing.T) {
	duid, err := dhcpv6.DUIDFromBytes([]byte{0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01})
	require.NoError(t, err)
	SetClassForDUID(duid, "test-class")

	result := GetClassForDUIDString("0001000100000001")
	assert.Equal(t, "test-class", result, "Should retrieve class by DUID string")
}
