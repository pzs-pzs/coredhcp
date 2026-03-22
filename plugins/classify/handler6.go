// Copyright 2018-present the CoreDHCP Authors. All rights reserved
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree.

package classify

import (
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/coredhcp/coredhcp/logger"
	"github.com/insomniacslk/dhcp/dhcpv6"
)

var log6 = logger.GetLogger("plugins/classify/v6")

// clientClassMapV6 stores the most recent classification for each DUID
var clientClassMapV6 = struct {
	sync.RWMutex
	m map[string]string
}{m: make(map[string]string)}

// Handler6 handles DHCPv6 packets for client classification
func (p *PluginState) Handler6(req, resp dhcpv6.DHCPv6) (dhcpv6.DHCPv6, bool) {
	msg, err := req.GetInnerMessage()
	if err != nil {
		log6.Errorf("Failed to get inner message: %v", err)
		return resp, false
	}

	// Extract client information
	clientInfo := p.extractClientInfoV6(req, msg)

	// Log client info for debugging
	if clientInfo != nil {
		log6.Debugf("Client info: DUID=%v, MAC=%v, VendorClass=%q, ArchType=%v",
			clientInfo.DUID, clientInfo.MAC, clientInfo.VendorClass, clientInfo.ArchType)
	}

	// Match against classes
	className := p.matchClass(clientInfo)

	if className != "" {
		log6.Printf("Client classified as '%s'", className)
		// Store classification in the package-level map
		if clientInfo.DUID != nil {
			duidStr := strings.ToLower(fmt.Sprintf("%x", clientInfo.DUID.ToBytes()))
			clientClassMapV6.Lock()
			clientClassMapV6.m[duidStr] = className
			clientClassMapV6.Unlock()
		}
	} else {
		log6.Debug("Client did not match any class")
	}

	return resp, false
}

// extractClientInfoV6 extracts client identification information from a DHCPv6 request
func (p *PluginState) extractClientInfoV6(req dhcpv6.DHCPv6, msg *dhcpv6.Message) *ClientInfo {
	info := &ClientInfo{}

	// Extract DUID (ClientID)
	info.DUID = msg.Options.ClientID()

	// Extract MAC from DUID or link-layer address option
	if info.DUID != nil {
		if mac := extractMACFromDUID(info.DUID); mac != nil {
			info.MAC = mac
		}
	}

	// Try to extract MAC from relay info if available
	if info.MAC == nil {
		if mac, err := dhcpv6.ExtractMAC(req); err == nil && mac != nil {
			info.MAC = mac
		}
	}

	// Extract relay information if this is a relayed message
	if relayMsg, ok := req.(*dhcpv6.RelayMessage); ok {
		// Extract link-address (the relay agent's address)
		info.LinkAddress = relayMsg.LinkAddr

		// Extract peer-address (the client's address that was used by the relay agent)
		// This could be useful for identifying the client network
		if relayMsg.PeerAddr != nil {
			info.RemoteAddr = relayMsg.PeerAddr.String()
		}
	}

	// Extract Vendor Class (Option 16)
	if vcOpts := msg.Options.Get(dhcpv6.OptionVendorClass); len(vcOpts) > 0 {
		// The option is *dhcpv6.OptVendorClass with Data field containing the vendor class strings
		if vc, ok := vcOpts[0].(*dhcpv6.OptVendorClass); ok && len(vc.Data) > 0 {
			// Data is [][]byte, use the first vendor class data
			if len(vc.Data[0]) > 0 {
				info.VendorClass = string(vc.Data[0])
			}
		}
	}

	// Extract User Class (Option 15)
	if ucOpts := msg.Options.Get(dhcpv6.OptionUserClass); len(ucOpts) > 0 {
		// User Class format: one or more opaque strings
		for _, opt := range ucOpts {
			data := opt.ToBytes()
			if len(data) > 0 {
				info.UserClasses = append(info.UserClasses, string(data))
			}
		}
	}

	// Extract Client Architecture Type (Option 61)
	if archOpts := msg.Options.Get(dhcpv6.OptionClientArchType); len(archOpts) > 0 {
		data := archOpts[0].ToBytes()
		if len(data) >= 2 {
			archType := uint16(data[0])<<8 | uint16(data[1])
			info.ArchType = &archType
		}
	}

	// Extract Interface ID (Option 18)
	if ifaceID := msg.Options.Get(dhcpv6.OptionInterfaceID); len(ifaceID) > 0 {
		info.InterfaceID = ifaceID[0].ToBytes()
	}

	// Extract Remote ID (Option 37) - relay agent information
	// This option is typically added by relay agents to identify themselves
	if remoteIDOpts := msg.Options.Get(37); len(remoteIDOpts) > 0 {
		data := remoteIDOpts[0].ToBytes()
		if len(data) >= 4 { // Minimum: Enterprise Number (4 bytes)
			info.RemoteID = data
		}
	}

	return info
}

// extractMACFromDUID attempts to extract a MAC address from various DUID types
func extractMACFromDUID(duid dhcpv6.DUID) net.HardwareAddr {
	if duid == nil {
		return nil
	}

	duidBytes := duid.ToBytes()
	if len(duidBytes) < 4 {
		return nil
	}

	// DUID-LLT (Type 1): Type(2) + HWType(2) + Time(4) + LLAddr
	if duidBytes[0] == 0 && duidBytes[1] == 1 {
		if len(duidBytes) >= 14 { // 2+2+4+6 = 14 minimum for Ethernet MAC
			mac := make(net.HardwareAddr, 6)
			copy(mac, duidBytes[8:14])
			return mac
		}
	}

	// DUID-LL (Type 3): Type(2) + HWType(2) + LLAddr
	if duidBytes[0] == 0 && duidBytes[1] == 3 {
		if len(duidBytes) >= 10 { // 2+2+6 = 10 for Ethernet MAC
			mac := make(net.HardwareAddr, 6)
			copy(mac, duidBytes[4:10])
			return mac
		}
	}

	// DUID-UUID (Type 4): Type(2) + UUID(16)
	// No MAC address here

	return nil
}

// GetClassForDUID retrieves the classification result for a DUID
// This is intended to be called by other plugins that need to know the client class
func GetClassForDUID(duid dhcpv6.DUID) string {
	if duid == nil {
		return ""
	}
	duidStr := strings.ToLower(fmt.Sprintf("%x", duid.ToBytes()))
	clientClassMapV6.RLock()
	defer clientClassMapV6.RUnlock()
	return clientClassMapV6.m[duidStr]
}

// GetClassForDUIDString retrieves the classification result for a DUID hex string
func GetClassForDUIDString(duidHex string) string {
	clientClassMapV6.RLock()
	defer clientClassMapV6.RUnlock()
	return clientClassMapV6.m[strings.ToLower(duidHex)]
}

// SetClassForDUID stores the classification result for a DUID
func SetClassForDUID(duid dhcpv6.DUID, className string) {
	if duid == nil {
		return
	}
	duidStr := strings.ToLower(fmt.Sprintf("%x", duid.ToBytes()))
	clientClassMapV6.Lock()
	clientClassMapV6.m[duidStr] = className
	clientClassMapV6.Unlock()
}
