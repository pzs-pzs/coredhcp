// Copyright 2018-present the CoreDHCP Authors. All rights reserved
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree.

package bitmap

// This allocator handles IPv6 address assignments for IA_NA (Identity Association for Non-temporary Addresses).
// It uses a bitmap to track individual IPv6 address allocations within a specified range.

import (
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/bits-and-blooms/bitset"
	"github.com/coredhcp/coredhcp/plugins/allocators"
)

var (
	errIPv6NotInRange = errors.New("IPv6 address outside of allowed range")
	errInvalidIPv6    = errors.New("invalid IPv6 address passed as input")
)

// IPv6Allocator allocates individual IPv6 addresses, tracking utilization with a bitmap
type IPv6Allocator struct {
	start net.IP // Start of IPv6 range
	end   net.IP // End of IPv6 range

	// bitmapSize is the number of addresses in the range (end - start + 1)
	bitmapSize uint

	// This bitset implementation isn't goroutine-safe, we protect it with a mutex for now
	// until we can swap for another concurrent implementation
	bitmap *bitset.BitSet
	l      sync.Mutex
}

// toIP converts an offset to an IPv6 address
func (a *IPv6Allocator) toIP(offset uint64) net.IP {
	if offset >= uint64(a.bitmapSize) {
		panic("BUG: offset out of bounds")
	}

	ip, err := allocators.AddPrefixes(a.start, offset, 128)
	if err != nil {
		panic(fmt.Sprintf("BUG: failed to convert offset to IP: %v", err))
	}
	return ip
}

// toOffset converts an IPv6 address to an offset from the start of the range
func (a *IPv6Allocator) toOffset(ip net.IP) (uint, error) {
	if ip.To16() == nil || ip.To4() != nil {
		return 0, errInvalidIPv6
	}

	offset, err := allocators.Offset(ip, a.start, 128)
	if err != nil {
		return 0, errIPv6NotInRange
	}

	// Verify the IP is within the range
	if offset >= uint64(a.bitmapSize) {
		return 0, errIPv6NotInRange
	}

	return uint(offset), nil
}

// Allocate reserves an IPv6 address for a client
func (a *IPv6Allocator) Allocate(hint net.IPNet) (n net.IPNet, err error) {
	n.Mask = net.CIDRMask(128, 128)

	// This is just a hint, ignore any error with it
	hintOffset, _ := a.toOffset(hint.IP)

	a.l.Lock()
	defer a.l.Unlock()

	var next uint
	// First try the exact match
	if hint.IP.To16() != nil && !a.bitmap.Test(hintOffset) {
		next = hintOffset
	} else {
		// Then any available address
		avail, ok := a.bitmap.NextClear(0)
		if !ok {
			return n, allocators.ErrNoAddrAvail
		}
		next = avail
	}

	a.bitmap.Set(next)
	n.IP = a.toIP(uint64(next))
	return
}

// Free releases the given IPv6 address
func (a *IPv6Allocator) Free(n net.IPNet) error {
	offset, err := a.toOffset(n.IP)
	if err != nil {
		return errIPv6NotInRange
	}

	a.l.Lock()
	defer a.l.Unlock()

	if !a.bitmap.Test(uint(offset)) {
		return &allocators.ErrDoubleFree{Loc: n}
	}
	a.bitmap.Clear(offset)
	return nil
}

// NewIPv6Allocator creates a new allocator suitable for giving out IPv6 addresses
func NewIPv6Allocator(start, end net.IP) (*IPv6Allocator, error) {
	if start.To4() != nil || end.To4() != nil {
		return nil, fmt.Errorf("invalid IPv6 addresses given to create the allocator: [%s,%s]", start, end)
	}

	if len(start) != net.IPv6len || len(end) != net.IPv6len {
		return nil, fmt.Errorf("addresses must be 16-byte IPv6 addresses: got %d and %d bytes", len(start), len(end))
	}

	// Calculate the size of the range
	offset, err := allocators.Offset(end, start, 128)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate range size: %w", err)
	}

	bitmapSize := offset + 1

	if bitmapSize > uint64(^uint(0)) {
		return nil, errors.New("range is too large for bitmap allocator")
	}

	alloc := IPv6Allocator{
		start:      start,
		end:        end,
		bitmapSize: uint(bitmapSize),
		bitmap:     bitset.New(uint(bitmapSize)),
	}

	return &alloc, nil
}
