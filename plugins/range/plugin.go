// Copyright 2018-present the CoreDHCP Authors. All rights reserved
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree.

package rangeplugin

import (
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/coredhcp/coredhcp/handler"
	"github.com/coredhcp/coredhcp/logger"
	"github.com/coredhcp/coredhcp/plugins"
	"github.com/coredhcp/coredhcp/plugins/allocators"
	"github.com/coredhcp/coredhcp/plugins/allocators/bitmap"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"gopkg.in/yaml.v3"
)

var log = logger.GetLogger("plugins/range")

// Plugin wraps plugin registration information
var Plugin = plugins.Plugin{
	Name:   "range",
	Setup6: setupRange6,
	Setup4: setupRange,
}

// Record holds an IP lease record
type Record struct {
	IP       net.IP
	expires  int
	hostname string
}

// IPRange defines an IP address range
type IPRange struct {
	Start net.IP `yaml:"start"`
	End   net.IP `yaml:"end"`
}

// ClassRange defines a range for a specific client class
type ClassRange struct {
	Name  string  `yaml:"name"` // Class name from classify plugin
	Range IPRange `yaml:"range"`
}

// RangeConfig is the YAML configuration for class-based ranges
type RangeConfig struct {
	Database  string `yaml:"database"`
	LeaseTime string `yaml:"lease_time"`
	// For backward compatibility with single range
	DefaultRange *IPRange `yaml:"default_range,omitempty"`
	// Multiple class-specific ranges
	ClassRanges []ClassRange `yaml:"class_ranges,omitempty"`
}

// PluginState is the data held by an instance of the range plugin
type PluginState struct {
	// Rough lock for the whole plugin, we'll get better performance once we use leasestorage
	sync.Mutex
	// Recordsv4 holds a MAC -> IP address and lease time mapping
	Recordsv4 map[string]*Record
	// Recordsv6 holds a DUID (hex string) -> IP address and lease time mapping
	Recordsv6 map[string]*Record
	LeaseTime time.Duration
	leasedb   *sql.DB
	// allocator4 is the IPv4 address allocator (used for DHCPv4)
	allocator4 allocators.Allocator
	// allocator6 is the IPv6 address allocator (used for DHCPv6)
	allocator6 allocators.Allocator
	// allocators6ByClass holds IPv6 allocators for each client class
	allocators6ByClass map[string]allocators.Allocator
	// allocators4ByClass holds IPv4 allocators for each client class
	allocators4ByClass map[string]allocators.Allocator
	// config holds the parsed configuration
	config *RangeConfig
}

// Handler4 handles DHCPv4 packets for the range plugin
func (p *PluginState) Handler4(req, resp *dhcpv4.DHCPv4) (*dhcpv4.DHCPv4, bool) {
	if req.MessageType() == dhcpv4.MessageTypeInform {
		return resp, false
	}
	p.Lock()
	defer p.Unlock()
	record, ok := p.Recordsv4[req.ClientHWAddr.String()]
	hostname := req.HostName()

	if ok && req.MessageType() == dhcpv4.MessageTypeRelease {
		return p.handleRelease(req, resp, record)
	}

	if !ok {
		// Allocating new address since there isn't one allocated
		log.Printf("MAC address %s is new, leasing new IPv4 address", req.ClientHWAddr.String())
		ip, err := p.allocator4.Allocate(net.IPNet{})
		if err != nil {
			log.Errorf("Could not allocate IP for MAC %s: %v", req.ClientHWAddr.String(), err)
			return nil, true
		}
		rec := Record{
			IP:       ip.IP.To4(),
			expires:  int(time.Now().Add(p.LeaseTime).Unix()),
			hostname: hostname,
		}
		err = p.saveIPAddress(req.ClientHWAddr, &rec)
		if err != nil {
			log.Errorf("SaveIPAddress for MAC %s failed: %v", req.ClientHWAddr.String(), err)
		}
		p.Recordsv4[req.ClientHWAddr.String()] = &rec
		record = &rec
	} else {
		// Ensure we extend the existing lease at least past when the one we're giving expires
		expiry := time.Unix(int64(record.expires), 0)
		if expiry.Before(time.Now().Add(p.LeaseTime)) {
			record.expires = int(time.Now().Add(p.LeaseTime).Round(time.Second).Unix())
			record.hostname = hostname
			err := p.saveIPAddress(req.ClientHWAddr, record)
			if err != nil {
				log.Errorf("Could not persist lease for MAC %s: %v", req.ClientHWAddr.String(), err)
			}
		}
	}
	resp.YourIPAddr = record.IP
	resp.Options.Update(dhcpv4.OptIPAddressLeaseTime(p.LeaseTime.Round(time.Second)))
	log.Printf("found IP address %s for MAC %s", record.IP, req.ClientHWAddr.String())
	return resp, false
}

func (p *PluginState) handleRelease(req, _ *dhcpv4.DHCPv4, record *Record) (*dhcpv4.DHCPv4, bool) {
	// Remove lease from storage
	if freeErr := p.freeIPAddress(req.ClientHWAddr, record); freeErr != nil {
		log.Errorf("Could not remove lease from storage for MAC %s: %v", req.ClientHWAddr.String(), freeErr)
		return nil, true
	}

	// Remove from in-memory map
	delete(p.Recordsv4, req.ClientHWAddr.String())

	// Release the IP address from allocator
	if freeErr := p.allocator4.Free(net.IPNet{IP: record.IP}); freeErr != nil {
		log.Errorf("Could not free IP %s for MAC %s: %v", record.IP.String(), req.ClientHWAddr.String(), freeErr)
		return nil, true
	}

	log.Printf("Released IP address %s for MAC %s", record.IP.String(), req.ClientHWAddr.String())
	return nil, true
}

func setupRange(args ...string) (handler.Handler4, error) {
	var (
		err error
		p   PluginState
	)

	p.allocators4ByClass = make(map[string]allocators.Allocator)

	// Support two formats:
	// 1. Old format: database start_ip end_ip lease_time
	// 2. New format: config.yaml (single YAML config file)
	if len(args) == 1 {
		// YAML config format
		return setupRangeWithConfig(args[0])
	}

	if len(args) < 4 {
		return nil, fmt.Errorf("invalid number of arguments. Use: <config.yaml> OR <database> <start_ip> <end_ip> <lease_time>, got: %d", len(args))
	}

	// Old format - backward compatibility
	filename := args[0]
	if filename == "" {
		return nil, errors.New("file name cannot be empty")
	}
	ipRangeStart := net.ParseIP(args[1])
	if ipRangeStart.To4() == nil {
		return nil, fmt.Errorf("invalid IPv4 address: %v", args[1])
	}
	ipRangeEnd := net.ParseIP(args[2])
	if ipRangeEnd.To4() == nil {
		return nil, fmt.Errorf("invalid IPv4 address: %v", args[2])
	}
	if binary.BigEndian.Uint32(ipRangeStart.To4()) > binary.BigEndian.Uint32(ipRangeEnd.To4()) {
		return nil, errors.New("start of IP range has to be lower than or equal to the end of an IP range")
	}

	p.allocator4, err = bitmap.NewIPv4Allocator(ipRangeStart, ipRangeEnd)
	if err != nil {
		return nil, fmt.Errorf("could not create an allocator: %w", err)
	}

	p.LeaseTime, err = time.ParseDuration(args[3])
	if err != nil {
		return nil, fmt.Errorf("invalid lease duration: %v", args[3])
	}

	if err := p.registerBackingDB(filename); err != nil {
		return nil, fmt.Errorf("could not setup lease storage: %w", err)
	}
	p.Recordsv4, err = loadRecords(p.leasedb)
	if err != nil {
		return nil, fmt.Errorf("could not load records from file: %v", err)
	}

	log.Printf("Loaded %d DHCPv4 leases from %s", len(p.Recordsv4), filename)

	for _, v := range p.Recordsv4 {
		ip, err := p.allocator4.Allocate(net.IPNet{IP: v.IP})
		if err != nil {
			return nil, fmt.Errorf("failed to re-allocate leased ip %v: %v", v.IP.String(), err)
		}
		if ip.IP.String() != v.IP.String() {
			return nil, fmt.Errorf("allocator did not re-allocate requested leased ip %v: %v", v.IP.String(), ip.String())
		}
	}

	return p.Handler4, nil
}

// setupRangeWithConfig loads configuration from YAML file for DHCPv4
func setupRangeWithConfig(configFile string) (handler.Handler4, error) {
	var (
		err error
		p   PluginState
	)

	p.allocators4ByClass = make(map[string]allocators.Allocator)

	// Read and parse YAML config
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config RangeConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	p.config = &config

	// Parse lease time
	p.LeaseTime, err = time.ParseDuration(config.LeaseTime)
	if err != nil {
		return nil, fmt.Errorf("invalid lease duration: %v", config.LeaseTime)
	}

	// Register database
	if err := p.registerBackingDB(config.Database); err != nil {
		return nil, fmt.Errorf("could not setup lease storage: %w", err)
	}

	p.Recordsv4, err = loadRecords(p.leasedb)
	if err != nil {
		return nil, fmt.Errorf("could not load records from file: %v", err)
	}

	// Setup default allocator if specified
	if config.DefaultRange != nil {
		if config.DefaultRange.Start.To4() == nil || config.DefaultRange.End.To4() == nil {
			return nil, fmt.Errorf("default range must be IPv4 addresses for DHCPv4")
		}
		p.allocator4, err = bitmap.NewIPv4Allocator(config.DefaultRange.Start, config.DefaultRange.End)
		if err != nil {
			return nil, fmt.Errorf("could not create default IPv4 allocator: %w", err)
		}
		log.Printf("Default IPv4 range: %s - %s", config.DefaultRange.Start, config.DefaultRange.End)
	}

	// Setup per-class allocators
	for _, classRange := range config.ClassRanges {
		if classRange.Range.Start.To4() == nil || classRange.Range.End.To4() == nil {
			return nil, fmt.Errorf("range for class %s must be IPv4 addresses for DHCPv4", classRange.Name)
		}
		allocator, err := bitmap.NewIPv4Allocator(classRange.Range.Start, classRange.Range.End)
		if err != nil {
			return nil, fmt.Errorf("could not create IPv4 allocator for class %s: %w", classRange.Name, err)
		}
		p.allocators4ByClass[classRange.Name] = allocator
		log.Printf("Class '%s' IPv4 range: %s - %s", classRange.Name, classRange.Range.Start, classRange.Range.End)
	}

	log.Printf("Loaded %d DHCPv4 leases from %s", len(p.Recordsv4), config.Database)

	// Re-allocate existing leases
	for _, v := range p.Recordsv4 {
		// Try to allocate from any allocator
		var allocated bool
		if p.allocator4 != nil {
			if ip, err := p.allocator4.Allocate(net.IPNet{IP: v.IP}); err == nil && ip.IP.String() == v.IP.String() {
				allocated = true
			}
		}
		if !allocated {
			for _, allocator := range p.allocators4ByClass {
				if ip, err := allocator.Allocate(net.IPNet{IP: v.IP}); err == nil && ip.IP.String() == v.IP.String() {
					allocated = true
					break
				}
			}
		}
		if !allocated {
			return nil, fmt.Errorf("failed to re-allocate leased IPv4 %v", v.IP.String())
		}
	}

	return p.Handler4, nil
}

// getAllocator4 returns the appropriate allocator for the client class
func (p *PluginState) getAllocator4(clientClass string) allocators.Allocator {
	if clientClass != "" {
		if allocator, ok := p.allocators4ByClass[clientClass]; ok {
			log.Debugf("Using class-specific allocator for '%s'", clientClass)
			return allocator
		}
	}
	return p.allocator4
}

func setupRange6(args ...string) (handler.Handler6, error) {
	var (
		err error
		p   PluginState
	)

	p.allocators6ByClass = make(map[string]allocators.Allocator)

	// Support two formats:
	// 1. Old format: database start_ip end_ip lease_time
	// 2. New format: config.yaml (single YAML config file)
	if len(args) == 1 {
		// YAML config format
		return setupRange6WithConfig(args[0])
	}

	if len(args) < 4 {
		return nil, fmt.Errorf("invalid number of arguments. Use: <config.yaml> OR <database> <start_ip> <end_ip> <lease_time>, got: %d", len(args))
	}

	// Old format - backward compatibility
	filename := args[0]
	if filename == "" {
		return nil, errors.New("file name cannot be empty")
	}
	ipRangeStart := net.ParseIP(args[1])
	if ipRangeStart.To4() != nil {
		return nil, fmt.Errorf("invalid IPv6 address: %v", args[1])
	}
	ipRangeEnd := net.ParseIP(args[2])
	if ipRangeEnd.To4() != nil {
		return nil, fmt.Errorf("invalid IPv6 address: %v", args[2])
	}

	// Verify start <= end
	if len(ipRangeStart) != net.IPv6len || len(ipRangeEnd) != net.IPv6len {
		return nil, fmt.Errorf("addresses must be 16-byte IPv6 addresses")
	}

	p.allocator6, err = bitmap.NewIPv6Allocator(ipRangeStart, ipRangeEnd)
	if err != nil {
		return nil, fmt.Errorf("could not create an IPv6 allocator: %w", err)
	}

	p.LeaseTime, err = time.ParseDuration(args[3])
	if err != nil {
		return nil, fmt.Errorf("invalid lease duration: %v", args[3])
	}

	if err := p.registerBackingDB(filename); err != nil {
		return nil, fmt.Errorf("could not setup lease storage: %w", err)
	}
	p.Recordsv6, err = loadRecords6(p.leasedb)
	if err != nil {
		return nil, fmt.Errorf("could not load IPv6 records from file: %v", err)
	}

	log.Printf("Loaded %d DHCPv6 leases from %s", len(p.Recordsv6), filename)

	for _, v := range p.Recordsv6 {
		ip, err := p.allocator6.Allocate(net.IPNet{IP: v.IP})
		if err != nil {
			return nil, fmt.Errorf("failed to re-allocate leased IPv6 %v: %v", v.IP.String(), err)
		}
		if ip.IP.String() != v.IP.String() {
			return nil, fmt.Errorf("allocator did not re-allocate requested leased IPv6 %v: %v", v.IP.String(), ip.String())
		}
	}

	return p.Handler6, nil
}

// setupRange6WithConfig loads configuration from YAML file
func setupRange6WithConfig(configFile string) (handler.Handler6, error) {
	var (
		err error
		p   PluginState
	)

	p.allocators6ByClass = make(map[string]allocators.Allocator)

	// Read and parse YAML config
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config RangeConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	p.config = &config

	// Parse lease time
	p.LeaseTime, err = time.ParseDuration(config.LeaseTime)
	if err != nil {
		return nil, fmt.Errorf("invalid lease duration: %v", config.LeaseTime)
	}

	// Register database
	if err := p.registerBackingDB(config.Database); err != nil {
		return nil, fmt.Errorf("could not setup lease storage: %w", err)
	}

	p.Recordsv6, err = loadRecords6(p.leasedb)
	if err != nil {
		return nil, fmt.Errorf("could not load IPv6 records from file: %v", err)
	}

	// Setup default allocator if specified
	if config.DefaultRange != nil {
		p.allocator6, err = bitmap.NewIPv6Allocator(config.DefaultRange.Start, config.DefaultRange.End)
		if err != nil {
			return nil, fmt.Errorf("could not create default IPv6 allocator: %w", err)
		}
		log.Printf("Default IPv6 range: %s - %s", config.DefaultRange.Start, config.DefaultRange.End)
	}

	// Setup per-class allocators
	for _, classRange := range config.ClassRanges {
		allocator, err := bitmap.NewIPv6Allocator(classRange.Range.Start, classRange.Range.End)
		if err != nil {
			return nil, fmt.Errorf("could not create IPv6 allocator for class %s: %w", classRange.Name, err)
		}
		p.allocators6ByClass[classRange.Name] = allocator
		log.Printf("Class '%s' IPv6 range: %s - %s", classRange.Name, classRange.Range.Start, classRange.Range.End)
	}

	log.Printf("Loaded %d DHCPv6 leases from %s", len(p.Recordsv6), config.Database)

	// Re-allocate existing leases
	for _, v := range p.Recordsv6 {
		// Try to allocate from any allocator
		var allocated bool
		if p.allocator6 != nil {
			if ip, err := p.allocator6.Allocate(net.IPNet{IP: v.IP}); err == nil && ip.IP.String() == v.IP.String() {
				allocated = true
			}
		}
		if !allocated {
			for _, allocator := range p.allocators6ByClass {
				if ip, err := allocator.Allocate(net.IPNet{IP: v.IP}); err == nil && ip.IP.String() == v.IP.String() {
					allocated = true
					break
				}
			}
		}
		if !allocated {
			return nil, fmt.Errorf("failed to re-allocate leased IPv6 %v", v.IP.String())
		}
	}

	return p.Handler6, nil
}

// getAllocator6 returns the appropriate allocator for the client class
func (p *PluginState) getAllocator6(clientClass string) allocators.Allocator {
	if clientClass != "" {
		if allocator, ok := p.allocators6ByClass[clientClass]; ok {
			log.Debugf("Using class-specific allocator for '%s'", clientClass)
			return allocator
		}
	}
	return p.allocator6
}
