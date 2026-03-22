// Copyright 2018-present the CoreDHCP Authors. All rights reserved
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree.

package rangeplugin

import (
	"encoding/hex"
	"fmt"
	"net"
	"time"

	"github.com/coredhcp/coredhcp/plugins/classify"
	"github.com/insomniacslk/dhcp/dhcpv6"
	dhcpIana "github.com/insomniacslk/dhcp/iana"
)

// recordKey computes the key for the Recordsv6 array from the client DUID
func recordKey6(duid dhcpv6.DUID) string {
	return hex.EncodeToString(duid.ToBytes())
}

// Handler6 handles DHCPv6 packets for the range plugin (IA_NA address allocation)
func (p *PluginState) Handler6(req, resp dhcpv6.DHCPv6) (dhcpv6.DHCPv6, bool) {
	msg, err := req.GetInnerMessage()
	if err != nil {
		log.Errorf("BUG: could not decapsulate: %v", err)
		return nil, true
	}

	client := msg.Options.ClientID()
	if client == nil {
		log.Error("Invalid packet received, no clientID")
		return nil, true
	}

	// Only process packets with IA_NA (Identity Association for Non-temporary Addresses)
	ianaOpts := msg.Options.IANA()
	if len(ianaOpts) == 0 {
		log.Debug("No IA_NA requested")
		return resp, false
	}

	// Log the message type for debugging
	msgType := msg.MessageType
	log.Debugf("Received %s message from DUID %s", msgType, recordKey6(client))

	// Process each IA_NA option
	for _, iana := range ianaOpts {
		ianaResp := &dhcpv6.OptIANA{
			IaId: iana.IaId,
		}

		p.Lock()
		key := recordKey6(client)
		record, ok := p.Recordsv6[key]

		// Handle RELEASE and DECLINE messages
		isRelease := msgType == dhcpv6.MessageTypeRelease || msgType == dhcpv6.MessageTypeDecline
		if isRelease && ok {
			return p.handleRelease6(req, resp, record, client, iana)
		}

		// Handle SOLICIT, REQUEST, RENEW, REBIND
		if !ok {
			// New client - allocate address
			return p.handleNewClient6(req, resp, client, iana, ianaResp, key, msgType)
		}

		// Existing client - handle renewal
		err = p.handleRenewal6(req, resp, record, client, iana, ianaResp, key, msgType)
		if err != nil {
			log.Errorf("Failed to handle renewal for DUID %s: %v", key, err)
			p.Unlock()
			// Add status code to response
			ianaResp.Options.Add(&dhcpv6.OptStatusCode{
				StatusCode: dhcpIana.StatusNotOnLink,
			})
			resp.AddOption(ianaResp)
			continue
		}

		p.Unlock()

		// Add address to response
		lifetime := p.LeaseTime.Round(time.Second)
		ianaResp.Options.Add(&dhcpv6.OptIAAddress{
			IPv6Addr:          record.IP.To16(),
			PreferredLifetime: lifetime,
			ValidLifetime:     lifetime,
		})

		resp.AddOption(ianaResp)
	}

	return resp, false
}

// handleNewClient6 handles new clients (no existing lease)
func (p *PluginState) handleNewClient6(req, resp dhcpv6.DHCPv6, client dhcpv6.DUID, iana *dhcpv6.OptIANA, ianaResp *dhcpv6.OptIANA, key string, msgType dhcpv6.MessageType) (dhcpv6.DHCPv6, bool) {
	// Log based on message type
	switch msgType {
	case dhcpv6.MessageTypeSolicit:
		log.Printf("DUID %s is new (SOLICIT), leasing new IPv6 address", key)
	case dhcpv6.MessageTypeRequest:
		log.Printf("DUID %s is new (REQUEST), leasing new IPv6 address", key)
	case dhcpv6.MessageTypeRenew, dhcpv6.MessageTypeRebind:
		log.Printf("DUID %s sent %s but has no lease, treating as new client", key, msgType)
	default:
		log.Printf("DUID %s is new (%s), leasing new IPv6 address", key, msgType)
	}

	// Get client class if classified
	clientClass := classify.GetClassForDUID(client)
	if clientClass != "" {
		log.Printf("Client DUID %s classified as '%s'", key, clientClass)
	}

	// Get the appropriate allocator for this client class
	allocator := p.getAllocator6(clientClass)
	if allocator == nil {
		log.Errorf("No allocator available for client class '%s' and no default allocator", clientClass)
		p.Unlock()
		ianaResp.Options.Add(&dhcpv6.OptStatusCode{
			StatusCode: dhcpIana.StatusNoAddrsAvail,
		})
		resp.AddOption(ianaResp)
		return resp, false
	}

	// Check if the client has a hint in the IA_NA options
	var hint net.IPNet
	if addrs := iana.Options.Addresses(); len(addrs) > 0 {
		hint.IP = addrs[0].IPv6Addr
		log.Debugf("Client requested address %s", hint.IP)
	}

	ip, err := allocator.Allocate(hint)
	if err != nil {
		log.Errorf("Could not allocate IPv6 for DUID %s: %v", key, err)
		p.Unlock()
		ianaResp.Options.Add(&dhcpv6.OptStatusCode{
			StatusCode: dhcpIana.StatusNoAddrsAvail,
		})
		resp.AddOption(ianaResp)
		return resp, false
	}

	rec := Record{
		IP:       ip.IP.To16(),
		expires:  int(time.Now().Add(p.LeaseTime).Unix()),
		hostname: "",
	}

	err = p.saveIPAddress6(client.ToBytes(), &rec)
	if err != nil {
		log.Errorf("SaveIPAddress6 for DUID %s failed: %v", key, err)
	}
	p.Recordsv6[key] = &rec

	log.Printf("Allocated IPv6 address %s to DUID %s (class: %s)", rec.IP, key, clientClass)
	p.Unlock()

	// Add address to response
	lifetime := p.LeaseTime.Round(time.Second)
	ianaResp.Options.Add(&dhcpv6.OptIAAddress{
		IPv6Addr:          rec.IP.To16(),
		PreferredLifetime: lifetime,
		ValidLifetime:     lifetime,
	})
	resp.AddOption(ianaResp)

	return resp, false
}

// handleRenewal6 handles existing client renewals
func (p *PluginState) handleRenewal6(req, resp dhcpv6.DHCPv6, record *Record, client dhcpv6.DUID, iana *dhcpv6.OptIANA, ianaResp *dhcpv6.OptIANA, key string, msgType dhcpv6.MessageType) error {
	// Get client class for logging
	clientClass := classify.GetClassForDUID(client)

	// Verify the address in IA_NA matches our record (if client specified one)
	if addrs := iana.Options.Addresses(); len(addrs) > 0 {
		requestedAddr := addrs[0].IPv6Addr
		if !requestedAddr.Equal(record.IP) {
			log.Warnf("Client %s requested %s but has lease for %s (msgType: %s, class: %s)",
				key, requestedAddr, record.IP, msgType, clientClass)
			// Per RFC 9915, we should return the address we have, not the one requested
			// But we should indicate this via status code in some cases
		}
	}

	// Check current lease expiry
	expiry := time.Unix(int64(record.expires), 0)
	now := time.Now()
	timeUntilExpiry := expiry.Sub(now)

	// Extend lease if needed
	extendLease := false
	if expiry.Before(now.Add(p.LeaseTime)) {
		extendLease = true
	} else if timeUntilExpiry < p.LeaseTime/2 {
		// Extend if less than half the lease time remains (recommended practice)
		extendLease = true
	}

	if extendLease {
		record.expires = int(now.Add(p.LeaseTime).Round(time.Second).Unix())
		err := p.saveIPAddress6(client.ToBytes(), record)
		if err != nil {
			return fmt.Errorf("could not persist lease for DUID %s: %w", key, err)
		}
		log.Printf("Extended lease for DUID %s: %s (msgType: %s, class: %s, new expiry: %s)",
			key, record.IP, msgType, clientClass, time.Unix(int64(record.expires), 0).Format("2006-01-02 15:04:05"))
	} else {
		log.Debugf("Lease for DUID %s: %s still valid (expires in %v, msgType: %s, class: %s)",
			key, record.IP, timeUntilExpiry.Round(time.Second), msgType, clientClass)
	}

	// Add success status code for RENEW/REBIND
	switch msgType {
	case dhcpv6.MessageTypeRenew, dhcpv6.MessageTypeRebind:
		ianaResp.Options.Add(&dhcpv6.OptStatusCode{
			StatusCode: dhcpIana.StatusSuccess,
		})
	}

	return nil
}

func (p *PluginState) handleRelease6(req, resp dhcpv6.DHCPv6, record *Record, client dhcpv6.DUID, iana *dhcpv6.OptIANA) (dhcpv6.DHCPv6, bool) {
	key := recordKey6(client)

	// Get client class to determine which allocator to use
	clientClass := classify.GetClassForDUID(client)
	allocator := p.getAllocator6(clientClass)
	if allocator == nil {
		log.Errorf("No allocator found for releasing IPv6 %s (class: %s)", record.IP.String(), clientClass)
		return nil, true
	}

	// Remove lease from storage
	if freeErr := p.freeIPAddress6(client.ToBytes(), record); freeErr != nil {
		log.Errorf("Could not remove lease from storage for DUID %s: %v", key, freeErr)
		return nil, true
	}

	// Remove from in-memory map
	delete(p.Recordsv6, key)

	// Release the IP address from the correct allocator
	if freeErr := allocator.Free(net.IPNet{IP: record.IP}); freeErr != nil {
		log.Errorf("Could not free IPv6 %s for DUID %s: %v", record.IP.String(), key, freeErr)
		return nil, true
	}

	log.Printf("Released IPv6 address %s for DUID %s (class: %s)", record.IP.String(), key, clientClass)

	// Send a successful status code for the release
	ianaResp := &dhcpv6.OptIANA{
		IaId: iana.IaId,
	}
	ianaResp.Options.Add(&dhcpv6.OptStatusCode{
		StatusCode: dhcpIana.StatusSuccess,
	})
	resp.AddOption(ianaResp)

	return resp, true
}
