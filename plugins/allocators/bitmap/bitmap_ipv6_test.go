// Copyright 2018-present the CoreDHCP Authors. All rights reserved
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree.

package bitmap

import (
	"net"
	"testing"

	"github.com/coredhcp/coredhcp/plugins/allocators"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewIPv6Allocator(t *testing.T) {
	tests := []struct {
		name      string
		start     string
		end       string
		wantErr   bool
		expectErr string
	}{
		{
			name:    "valid range",
			start:   "2001:db8::1",
			end:     "2001:db8::10",
			wantErr: false,
		},
		{
			name:    "single address",
			start:   "2001:db8::1",
			end:     "2001:db8::1",
			wantErr: false,
		},
		{
			name:      "IPv4 addresses",
			start:     "192.168.1.1",
			end:       "192.168.1.10",
			wantErr:   true,
			expectErr: "invalid IPv6 addresses",
		},
		{
			name:    "large range",
			start:   "2001:db8::",
			end:     "2001:db8::1:0",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := net.ParseIP(tt.start)
			end := net.ParseIP(tt.end)

			alloc, err := NewIPv6Allocator(start, end)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.expectErr != "" {
					assert.Contains(t, err.Error(), tt.expectErr)
				}
				assert.Nil(t, alloc)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, alloc)
			}
		})
	}
}

func TestIPv6AllocatorAllocate(t *testing.T) {
	start := net.ParseIP("2001:db8::1")
	end := net.ParseIP("2001:db8::a")

	alloc, err := NewIPv6Allocator(start, end)
	require.NoError(t, err)
	require.NotNil(t, alloc)

	allocatedIPs := make(map[string]bool)

	// Allocate all addresses in the range (10 addresses)
	for i := 0; i < 10; i++ {
		ipNet, err := alloc.Allocate(net.IPNet{})
		assert.NoError(t, err)
		assert.NotNil(t, ipNet)

		ipStr := ipNet.IP.String()
		assert.False(t, allocatedIPs[ipStr], "IP %s should not be allocated twice", ipStr)
		allocatedIPs[ipStr] = true
	}

	// Next allocation should fail
	_, err = alloc.Allocate(net.IPNet{})
	assert.Error(t, err)
	assert.ErrorIs(t, err, allocators.ErrNoAddrAvail)
}

func TestIPv6AllocatorAllocateWithHint(t *testing.T) {
	start := net.ParseIP("2001:db8::1")
	end := net.ParseIP("2001:db8::100")

	alloc, err := NewIPv6Allocator(start, end)
	require.NoError(t, err)

	// Request a specific IP
	hint := net.IPNet{IP: net.ParseIP("2001:db8::50")}
	ipNet, err := alloc.Allocate(hint)

	assert.NoError(t, err)
	assert.Equal(t, "2001:db8::50", ipNet.IP.String())

	// Request the same IP again - should get a different one
	ipNet2, err := alloc.Allocate(hint)
	assert.NoError(t, err)
	assert.NotEqual(t, "2001:db8::50", ipNet2.IP.String())
}

func TestIPv6AllocatorFree(t *testing.T) {
	start := net.ParseIP("2001:db8::1")
	end := net.ParseIP("2001:db8::10")

	alloc, err := NewIPv6Allocator(start, end)
	require.NoError(t, err)

	// Allocate an address
	ipNet, err := alloc.Allocate(net.IPNet{})
	require.NoError(t, err)
	allocatedIP := ipNet.IP

	// Free it
	err = alloc.Free(ipNet)
	assert.NoError(t, err)

	// Should be able to allocate it again
	ipNet2, err := alloc.Allocate(net.IPNet{})
	assert.NoError(t, err)
	assert.Equal(t, allocatedIP.String(), ipNet2.IP.String())
}

func TestIPv6AllocatorDoubleFree(t *testing.T) {
	start := net.ParseIP("2001:db8::1")
	end := net.ParseIP("2001:db8::10")

	alloc, err := NewIPv6Allocator(start, end)
	require.NoError(t, err)

	// Allocate an address
	ipNet, err := alloc.Allocate(net.IPNet{})
	require.NoError(t, err)

	// Free it
	err = alloc.Free(ipNet)
	assert.NoError(t, err)

	// Try to free again
	err = alloc.Free(ipNet)
	assert.Error(t, err)

	var doubleFreeErr *allocators.ErrDoubleFree
	assert.ErrorAs(t, err, &doubleFreeErr)
}

func TestIPv6AllocatorFreeUnallocated(t *testing.T) {
	start := net.ParseIP("2001:db8::1")
	end := net.ParseIP("2001:db8::a")

	alloc, err := NewIPv6Allocator(start, end)
	require.NoError(t, err)

	// Try to free an address that was never allocated but is in range
	// This should return a double free error since the address isn't allocated
	ipNet := net.IPNet{IP: net.ParseIP("2001:db8::5")}
	err = alloc.Free(ipNet)
	assert.Error(t, err)

	var doubleFreeErr *allocators.ErrDoubleFree
	assert.ErrorAs(t, err, &doubleFreeErr)
}

func TestIPv6AllocatorOutOfRange(t *testing.T) {
	start := net.ParseIP("2001:db8::1")
	end := net.ParseIP("2001:db8::10")

	alloc, err := NewIPv6Allocator(start, end)
	require.NoError(t, err)

	// Try to free an address outside the range
	ipNet := net.IPNet{IP: net.ParseIP("2001:db8::100")}
	err = alloc.Free(ipNet)
	assert.Error(t, err)
	assert.ErrorIs(t, err, errIPv6NotInRange)
}

func TestIPv6AllocatorConcurrent(t *testing.T) {
	start := net.ParseIP("2001:db8::1")
	end := net.ParseIP("2001:db8::1000")

	alloc, err := NewIPv6Allocator(start, end)
	require.NoError(t, err)

	done := make(chan bool, 10)

	// Allocate concurrently
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 10; j++ {
				ipNet, err := alloc.Allocate(net.IPNet{})
				assert.NoError(t, err)
				_ = ipNet
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestIPv6AllocatorToIP(t *testing.T) {
	start := net.ParseIP("2001:db8::1")
	end := net.ParseIP("2001:db8::1000")

	alloc, err := NewIPv6Allocator(start, end)
	require.NoError(t, err)

	// Test offset 0
	ip := alloc.toIP(0)
	assert.Equal(t, "2001:db8::1", ip.String())

	// Test offset 1
	ip = alloc.toIP(1)
	assert.Equal(t, "2001:db8::2", ip.String())

	// Test offset that should wrap to ::1:0 (0xff from ::1)
	// 2001:db8::1 + 0xff = 2001:db8::100
	ip = alloc.toIP(0xff)
	assert.Equal(t, "2001:db8::100", ip.String())
}

func TestIPv6AllocatorToOffset(t *testing.T) {
	start := net.ParseIP("2001:db8::1")
	end := net.ParseIP("2001:db8::100")

	alloc, err := NewIPv6Allocator(start, end)
	require.NoError(t, err)

	// Test first address
	offset, err := alloc.toOffset(net.ParseIP("2001:db8::1"))
	assert.NoError(t, err)
	assert.Equal(t, uint(0), offset)

	// Test second address
	offset, err = alloc.toOffset(net.ParseIP("2001:db8::2"))
	assert.NoError(t, err)
	assert.Equal(t, uint(1), offset)

	// Test out of range
	_, err = alloc.toOffset(net.ParseIP("2001:db8::200"))
	assert.Error(t, err)
	assert.ErrorIs(t, err, errIPv6NotInRange)

	// Test IPv4 address
	_, err = alloc.toOffset(net.ParseIP("192.168.1.1"))
	assert.Error(t, err)
	assert.ErrorIs(t, err, errInvalidIPv6)
}
