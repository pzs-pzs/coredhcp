// Copyright 2018-present the CoreDHCP Authors. All rights reserved
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree.

package classify

import (
	"net"
	"strings"
	"sync"

	"github.com/coredhcp/coredhcp/logger"
	"github.com/insomniacslk/dhcp/dhcpv4"
)

var log4 = logger.GetLogger("plugins/classify/v4")

// clientClassMap stores the most recent classification for each MAC address
var clientClassMap = struct {
	sync.RWMutex
	m map[string]string
}{m: make(map[string]string)}

// Handler4 handles DHCPv4 packets for client classification
func (p *PluginState) Handler4(req, resp *dhcpv4.DHCPv4) (*dhcpv4.DHCPv4, bool) {
	// Extract client information
	clientInfo := p.extractClientInfoV4(req)

	// Log client info for debugging
	log4.Debugf("Client info: MAC=%v, VendorClass=%q, UserClasses=%q",
		clientInfo.MAC, clientInfo.VendorClass, clientInfo.UserClasses)

	// Match against classes
	className := p.matchClass(clientInfo)

	if className != "" {
		log4.Printf("Client %v classified as '%s'", clientInfo.MAC, className)
		// Store classification in the package-level map
		if clientInfo.MAC != nil {
			clientClassMap.Lock()
			clientClassMap.m[clientInfo.MAC.String()] = className
			clientClassMap.Unlock()
		}
	} else {
		log4.Debugf("Client %v did not match any class", clientInfo.MAC)
	}

	return resp, false
}

// extractClientInfoV4 extracts client identification information from a DHCPv4 request
func (p *PluginState) extractClientInfoV4(req *dhcpv4.DHCPv4) *ClientInfo {
	info := &ClientInfo{}

	// Get MAC address (ClientHWAddr)
	if req.ClientHWAddr != nil {
		info.MAC = req.ClientHWAddr
	}

	// Extract Vendor Class Identifier (Option 60)
	if vcOpt := req.Options.Get(dhcpv4.OptionVendorIdentifyingVendorClass); vcOpt != nil {
		info.VendorClass = string(vcOpt)
	}

	// Extract User Class (Option 77)
	if ucOpt := req.Options.Get(dhcpv4.OptionUserClassInformation); ucOpt != nil {
		// User Class in DHCPv4 can contain multiple user classes
		// For simplicity, we'll store the raw data as a string
		if len(ucOpt) > 0 {
			info.UserClasses = append(info.UserClasses, string(ucOpt))
		}
	}

	// Extract Client Identifier (Option 61)
	// This can be used as an alternative to MAC for identification
	if ciOpt := req.Options.Get(dhcpv4.OptionClientIdentifier); ciOpt != nil && len(ciOpt) > 1 {
		// Client Identifier format: Type (1 byte) + Identifier
		// Type 1 = Ethernet MAC address
		if ciOpt[0] == 1 && len(ciOpt) >= 7 {
			mac := make(net.HardwareAddr, 6)
			copy(mac, ciOpt[1:7])
			if info.MAC == nil {
				info.MAC = mac
			}
		}
	}

	// Get Remote Address (GIADDR or peer address)
	if req.GatewayIPAddr != nil && !req.GatewayIPAddr.IsUnspecified() {
		info.RemoteAddr = req.GatewayIPAddr.String()
	}

	return info
}

// GetClassForMAC retrieves the classification result for a MAC address
// This is intended to be called by other plugins that need to know the client class
func GetClassForMAC(mac net.HardwareAddr) string {
	if mac == nil {
		return ""
	}
	clientClassMap.RLock()
	defer clientClassMap.RUnlock()
	return clientClassMap.m[mac.String()]
}

// GetClassForMACString retrieves the classification result for a MAC address string
func GetClassForMACString(macStr string) string {
	clientClassMap.RLock()
	defer clientClassMap.RUnlock()
	return clientClassMap.m[strings.ToLower(macStr)]
}

// SetClassForMAC stores the classification result for a MAC address
func SetClassForMAC(mac net.HardwareAddr, className string) {
	if mac == nil {
		return
	}
	clientClassMap.Lock()
	clientClassMap.m[mac.String()] = className
	clientClassMap.Unlock()
}

// MatchClientByMAC is a convenience function to match a MAC address against configured classes
// This can be used by other plugins for quick lookups
func (p *PluginState) MatchClientByMAC(mac net.HardwareAddr) string {
	if mac == nil {
		return ""
	}

	info := &ClientInfo{
		MAC: mac,
	}

	return p.matchClass(info)
}

// MatchClientByVendorClass matches a client by vendor class string
func (p *PluginState) MatchClientByVendorClass(vc string) string {
	info := &ClientInfo{
		VendorClass: vc,
	}

	return p.matchClass(info)
}

// GetClassMAC returns the MAC address for a given class name
// This is useful when a plugin needs to know if a request belongs to a specific class
func (p *PluginState) GetClassMAC(className string) net.HardwareAddr {
	// This is a reverse lookup - find if the class name exists
	p.RLock()
	defer p.RUnlock()

	for _, class := range p.classes {
		if class.Name == className {
			// Return the first MAC prefix configured for this class as an identifier
			if len(class.Conditions.MACExact) > 0 {
				if mac, err := net.ParseMAC(class.Conditions.MACExact[0]); err == nil {
					return mac
				}
			}
			if len(class.Conditions.MACPrefix) > 0 {
				if mac, err := net.ParseMAC(class.Conditions.MACPrefix[0]); err == nil {
					return mac
				}
			}
		}
	}

	return nil
}

// MatchClientByDUID matches a client by DUID (hex string format)
func (p *PluginState) MatchClientByDUID(duidHex string) string {
	p.RLock()
	defer p.RUnlock()

	// Normalize the DUID hex string
	duidHex = strings.ToLower(strings.ReplaceAll(duidHex, ":", ""))

	for _, class := range p.classes {
		// Check DUID prefix matches
		for _, prefix := range class.Conditions.DUIDPrefix {
			normPrefix := strings.ToLower(strings.ReplaceAll(prefix, ":", ""))
			if strings.HasPrefix(duidHex, normPrefix) {
				return class.Name
			}
		}

		// Check DUID exact matches
		for _, exact := range class.Conditions.DUIDExact {
			normExact := strings.ToLower(strings.ReplaceAll(exact, ":", ""))
			if duidHex == normExact {
				return class.Name
			}
		}
	}

	return ""
}

// hasAnyPrefix checks if target starts with any of the prefixes (case-insensitive)
func hasAnyPrefix(target string, prefixes []string) bool {
	targetLower := strings.ToLower(target)
	for _, prefix := range prefixes {
		prefixLower := strings.ToLower(prefix)
		normPrefix := strings.ReplaceAll(prefixLower, ":", "")
		normTarget := strings.ReplaceAll(targetLower, ":", "")
		if strings.HasPrefix(normTarget, normPrefix) {
			return true
		}
	}
	return false
}

// isExactMatch checks if target exactly matches any of the values (case-insensitive)
func isExactMatch(target string, values []string) bool {
	targetLower := strings.ToLower(target)
	for _, value := range values {
		valueLower := strings.ToLower(value)
		if targetLower == valueLower {
			return true
		}
	}
	return false
}
