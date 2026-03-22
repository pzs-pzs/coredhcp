// Copyright 2018-present the CoreDHCP Authors. All rights reserved
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree.

// Package classify implements client classification based on various criteria
// such as DUID, MAC address, Vendor Class, User Class, and Architecture Type.
// Classification results are stored in the packet context for use by other plugins.
package classify

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/coredhcp/coredhcp/handler"
	"github.com/coredhcp/coredhcp/logger"
	"github.com/coredhcp/coredhcp/plugins"
	"github.com/insomniacslk/dhcp/dhcpv6"
	"gopkg.in/yaml.v3"
)

var log = logger.GetLogger("plugins/classify")

// Plugin wraps plugin registration information
var Plugin = plugins.Plugin{
	Name:   "classify",
	Setup6: setup6,
	Setup4: setup4,
}

// ClassConfig defines a single client class with matching conditions
type ClassConfig struct {
	Name       string            `yaml:"name"`
	Conditions ConditionConfig   `yaml:"conditions"`
	Actions    map[string]string `yaml:"actions,omitempty"` // Optional actions to apply
}

// ConditionConfig defines matching conditions for a class
type ConditionConfig struct {
	DUIDPrefix       []string `yaml:"duid_prefix,omitempty"`
	DUIDExact        []string `yaml:"duid_exact,omitempty"`
	MACPrefix        []string `yaml:"mac_prefix,omitempty"`
	MACExact         []string `yaml:"mac_exact,omitempty"`
	VendorClass      []string `yaml:"vendor_class,omitempty"`
	VendorClassMatch []string `yaml:"vendor_class_match,omitempty"` // regex/wildcard
	UserClass        []string `yaml:"user_class,omitempty"`
	ArchType         []uint16 `yaml:"arch_type,omitempty"`
	InterfaceID      []string `yaml:"interface_id,omitempty"`
	// Relay conditions (for relay scenarios)
	LinkAddress []string `yaml:"link_address,omitempty"` // Relay agent's link-address (IPv6)
	RemoteID    []string `yaml:"remote_id,omitempty"`    // Relay agent's remote ID
}

// Config is the top-level configuration for the classify plugin
type Config struct {
	Classes []ClassConfig `yaml:"classes"`
}

// PluginState holds the runtime state for the classify plugin
type PluginState struct {
	sync.RWMutex
	classes []ClassConfig
}

// contextKey is used to store classification results in the packet context
type contextKey string

const (
	// ClassContextKey is the key used to store the matched class name in packet context
	ClassContextKey contextKey = "classify.class"
)

// setup6 initializes the classify plugin for DHCPv6
func setup6(args ...string) (handler.Handler6, error) {
	if len(args) < 1 {
		return nil, errors.New("need a configuration file")
	}
	configFile := args[0]
	state, err := loadConfig(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}
	log.Printf("Loaded %d client classes from %s", len(state.classes), configFile)
	return state.Handler6, nil
}

// setup4 initializes the classify plugin for DHCPv4
func setup4(args ...string) (handler.Handler4, error) {
	if len(args) < 1 {
		return nil, errors.New("need a configuration file")
	}
	configFile := args[0]
	state, err := loadConfig(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}
	log.Printf("Loaded %d client classes from %s", len(state.classes), configFile)
	return state.Handler4, nil
}

// loadConfig reads and parses the classification configuration file
func loadConfig(configFile string) (*PluginState, error) {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if len(config.Classes) == 0 {
		return nil, errors.New("no classes defined in configuration")
	}

	// Validate class configurations
	for i, class := range config.Classes {
		if class.Name == "" {
			return nil, fmt.Errorf("class at index %d has no name", i)
		}
		// Validate MAC prefixes if specified
		for _, mac := range class.Conditions.MACPrefix {
			// MAC prefix can be partial (e.g., "00:00:0C" for Cisco)
			// Just validate it's valid hex format
			parts := strings.Split(mac, ":")
			if len(parts) < 1 || len(parts) > 6 {
				return nil, fmt.Errorf("invalid MAC prefix '%s' in class '%s': must have 1-6 octets", mac, class.Name)
			}
			for _, part := range parts {
				if len(part) != 2 {
					return nil, fmt.Errorf("invalid MAC prefix '%s' in class '%s': each octet must be 2 hex digits", mac, class.Name)
				}
				for _, c := range part {
					if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
						return nil, fmt.Errorf("invalid MAC prefix '%s' in class '%s': invalid hex digit", mac, class.Name)
					}
				}
			}
		}
		for _, mac := range class.Conditions.MACExact {
			if _, err := net.ParseMAC(mac); err != nil {
				return nil, fmt.Errorf("invalid MAC address '%s' in class '%s': %w", mac, class.Name, err)
			}
		}
	}

	return &PluginState{classes: config.Classes}, nil
}

// matchClass checks if the client matches any of the configured classes
// Returns the name of the first matching class, or empty string if no match
func (p *PluginState) matchClass(clientInfo *ClientInfo) string {
	p.RLock()
	defer p.RUnlock()

	for _, class := range p.classes {
		if p.matchesClass(clientInfo, &class) {
			log.Debugf("Client matched class '%s'", class.Name)
			return class.Name
		}
	}
	return ""
}

// matchesClass checks if a client matches a specific class
func (p *PluginState) matchesClass(clientInfo *ClientInfo, class *ClassConfig) bool {
	cond := &class.Conditions

	// Check DUID exact match (DHCPv6 only) - check first for priority
	if len(cond.DUIDExact) > 0 && clientInfo.DUID != nil {
		duidStr := fmt.Sprintf("%x", clientInfo.DUID.ToBytes())
		for _, exact := range cond.DUIDExact {
			if duidStr == exact {
				return true
			}
		}
	}

	// Check DUID prefix (DHCPv6 only)
	if len(cond.DUIDPrefix) > 0 && clientInfo.DUID != nil {
		duidStr := fmt.Sprintf("%x", clientInfo.DUID.ToBytes())
		for _, prefix := range cond.DUIDPrefix {
			if strings.HasPrefix(duidStr, prefix) {
				return true
			}
		}
	}

	// Check MAC prefix
	if len(cond.MACPrefix) > 0 && clientInfo.MAC != nil {
		macStr := clientInfo.MAC.String()
		for _, prefix := range cond.MACPrefix {
			if strings.HasPrefix(macStr, strings.ToLower(prefix)) ||
				strings.HasPrefix(macStr, strings.ToUpper(prefix)) {
				return true
			}
			// Also check with common MAC format variations
			normMAC := strings.ReplaceAll(strings.ToLower(prefix), ":", "")
			if strings.HasPrefix(strings.ReplaceAll(macStr, ":", ""), normMAC) {
				return true
			}
		}
	}

	// Check MAC exact match
	if len(cond.MACExact) > 0 && clientInfo.MAC != nil {
		macStr := clientInfo.MAC.String()
		for _, exact := range cond.MACExact {
			if macStr == strings.ToLower(exact) || macStr == strings.ToUpper(exact) {
				return true
			}
		}
	}

	// Check Vendor Class (DHCPv6)
	if len(cond.VendorClass) > 0 && clientInfo.VendorClass != "" {
		for _, vc := range cond.VendorClass {
			if clientInfo.VendorClass == vc {
				return true
			}
		}
	}

	// Check Vendor Class with pattern matching
	if len(cond.VendorClassMatch) > 0 && clientInfo.VendorClass != "" {
		for _, pattern := range cond.VendorClassMatch {
			if matchPattern(clientInfo.VendorClass, pattern) {
				return true
			}
		}
	}

	// Check User Class (DHCPv6)
	if len(cond.UserClass) > 0 {
		for _, uc := range cond.UserClass {
			for _, clientUC := range clientInfo.UserClasses {
				if clientUC == uc {
					return true
				}
			}
		}
	}

	// Check Architecture Type (DHCPv6)
	if len(cond.ArchType) > 0 && clientInfo.ArchType != nil {
		for _, arch := range cond.ArchType {
			if *clientInfo.ArchType == arch {
				return true
			}
		}
	}

	// Check Interface ID (DHCPv6)
	if len(cond.InterfaceID) > 0 && clientInfo.InterfaceID != nil {
		ifaceStr := fmt.Sprintf("%x", clientInfo.InterfaceID)
		for _, ifaceID := range cond.InterfaceID {
			if ifaceStr == ifaceID {
				return true
			}
		}
	}

	// Check Link Address (relay agent's address in relay scenarios)
	if len(cond.LinkAddress) > 0 && clientInfo.LinkAddress != nil {
		linkAddrStr := clientInfo.LinkAddress.String()
		for _, linkAddr := range cond.LinkAddress {
			// Support exact match or prefix match (e.g., "2001:db8:1::" matches any address in that subnet)
			if linkAddrStr == linkAddr || strings.HasPrefix(linkAddrStr, strings.TrimSuffix(linkAddr, "/")) {
				return true
			}
		}
	}

	// Check Remote ID (relay agent's remote ID option)
	if len(cond.RemoteID) > 0 && len(clientInfo.RemoteID) > 0 {
		remoteIDStr := fmt.Sprintf("%x", clientInfo.RemoteID)
		for _, remoteID := range cond.RemoteID {
			// Support hex string matching
			if strings.HasPrefix(remoteIDStr, strings.ToLower(remoteID)) {
				return true
			}
		}
	}

	return false
}

// matchPattern performs simple wildcard matching (* for any characters)
func matchPattern(text, pattern string) bool {
	// Empty pattern matches everything
	if pattern == "" {
		return true
	}
	// "*" matches everything
	if pattern == "*" {
		return true
	}

	patternParts := strings.Split(pattern, "*")
	if len(patternParts) == 1 {
		return text == pattern
	}

	// Check if text starts with the first part
	if !strings.HasPrefix(text, patternParts[0]) {
		return false
	}

	// Check if text ends with the last part
	if !strings.HasSuffix(text, patternParts[len(patternParts)-1]) {
		return false
	}

	// Check middle parts
	currentPos := len(patternParts[0])
	for i := 1; i < len(patternParts)-1; i++ {
		part := patternParts[i]
		idx := strings.Index(text[currentPos:], part)
		if idx == -1 {
			return false
		}
		currentPos += idx + len(part)
	}

	return true
}

// ClientInfo holds extracted client information for classification
type ClientInfo struct {
	DUID        dhcpv6.DUID
	MAC         net.HardwareAddr
	VendorClass string
	UserClasses []string
	ArchType    *uint16
	InterfaceID []byte
	RemoteAddr  string
	LinkAddress net.IP // Relay agent's link-address (for relay scenarios)
	RemoteID    []byte // Relay agent's remote ID option
}
