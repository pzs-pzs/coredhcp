// Copyright 2018-present the CoreDHCP Authors. All rights reserved
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree.

package rangeplugin

import (
	"encoding/hex"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/coredhcp/coredhcp/plugins/allocators"
	"github.com/coredhcp/coredhcp/plugins/allocators/bitmap"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// mockAllocator is a simple mock for testing
type mockAllocator struct {
	mock.Mock
}

func (m *mockAllocator) Allocate(hint net.IPNet) (net.IPNet, error) {
	return m.Called(hint).Get(0).(net.IPNet), nil
}

func (m *mockAllocator) Free(ip net.IPNet) error {
	m.Called(ip)
	return nil
}

type mockFailingAllocator struct {
	mock.Mock
}

func (m *mockFailingAllocator) Allocate(hint net.IPNet) (net.IPNet, error) {
	args := m.Called(hint)
	return args.Get(0).(net.IPNet), args.Error(1)
}

func (m *mockFailingAllocator) Free(ip net.IPNet) error {
	args := m.Called(ip)
	return args.Error(0)
}

func TestHandler4Release(t *testing.T) {
	db, dbErr := testDBSetup()
	if dbErr != nil {
		t.Fatalf("Failed to set up test DB: %v", dbErr)
	}

	mockAlloc := &mockAllocator{}

	pl := PluginState{
		leasedb:    db,
		Recordsv4:  make(map[string]*Record),
		allocator4: mockAlloc,
	}

	loadedRecords, loadErr := loadRecords(db)
	if loadErr != nil {
		t.Fatalf("Failed to load records: %v", loadErr)
	}
	pl.Recordsv4 = loadedRecords

	// Create a DHCP RELEASE request using existing test data
	hwaddr, _ := net.ParseMAC(records[1].mac)
	req := &dhcpv4.DHCPv4{
		ClientHWAddr: hwaddr,
	}
	req.UpdateOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeRelease))

	resp := &dhcpv4.DHCPv4{}

	// Verify record exists before release
	record, exists := pl.Recordsv4[hwaddr.String()]
	assert.True(t, exists, "Record should exist before release")

	expectedIPNet := net.IPNet{IP: record.IP}
	mockAlloc.On("Free", expectedIPNet).Return(nil)

	// Call Handler4 with RELEASE message
	result, stop := pl.Handler4(req, resp)

	assert.Nil(t, result, "Should return nil response for RELEASE")
	assert.True(t, stop, "Should return true to stop processing")

	_, exists = pl.Recordsv4[hwaddr.String()]
	assert.False(t, exists, "Record should be removed from memory after release")

	parsedRecords, parseErr := loadRecords(pl.leasedb)
	if parseErr != nil {
		t.Fatalf("Failed to load records after release: %v", parseErr)
	}
	_, exists = parsedRecords[hwaddr.String()]
	assert.False(t, exists, "Record should be removed from storage after release")

	mockAlloc.AssertExpectations(t)
	mockAlloc.AssertNotCalled(t, "Allocate")
}

func TestHandler4ReleaseAllocatorError(t *testing.T) {
	db, parseErr := testDBSetup()
	if parseErr != nil {
		t.Fatalf("Failed to set up test DB: %v", parseErr)
	}

	mockAlloc := &mockFailingAllocator{}

	pl := PluginState{
		leasedb:    db,
		Recordsv4:  make(map[string]*Record),
		allocator4: mockAlloc,
	}

	loadedRecords, err := loadRecords(db)
	if err != nil {
		t.Fatalf("Failed to load records: %v", err)
	}
	pl.Recordsv4 = loadedRecords

	hwaddr, _ := net.ParseMAC(records[1].mac)
	req := &dhcpv4.DHCPv4{
		ClientHWAddr: hwaddr,
	}
	req.UpdateOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeRelease))

	resp := &dhcpv4.DHCPv4{}

	record := pl.Recordsv4[hwaddr.String()]
	expectedIPNet := net.IPNet{IP: record.IP}

	expectedError := fmt.Errorf("mock allocator free failure")
	mockAlloc.On("Free", expectedIPNet).Return(expectedError)

	// Call Handler4 - this should fail on allocator.Free()
	result, stop := pl.Handler4(req, resp)

	assert.Nil(t, result, "Should return nil on allocator failure")
	assert.True(t, stop, "Should stop processing on allocator failure")

	_, exists := pl.Recordsv4[hwaddr.String()]
	assert.False(t, exists, "Record should be removed from memory even on allocator failure")

	parsedRecords, parseErr := loadRecords(pl.leasedb)
	if parseErr != nil {
		t.Fatalf("Failed to load records after release: %v", parseErr)
	}
	_, exists = parsedRecords[hwaddr.String()]
	assert.False(t, exists, "Record should be removed from storage even on allocator failure")

	mockAlloc.AssertExpectations(t)
	mockAlloc.AssertNotCalled(t, "Allocate")
}

func TestHandler4ReleaseStorageError(t *testing.T) {
	db, parseErr := testDBSetup()
	if parseErr != nil {
		t.Fatalf("Failed to set up test DB: %v", parseErr)
	}

	mockAlloc := &mockAllocator{}

	pl := PluginState{
		leasedb:    db,
		Recordsv4:  make(map[string]*Record),
		allocator4: mockAlloc,
	}

	loadedRecords, err := loadRecords(db)
	if err != nil {
		t.Fatalf("Failed to load records: %v", err)
	}
	pl.Recordsv4 = loadedRecords

	hwaddr, _ := net.ParseMAC(records[1].mac)
	req := &dhcpv4.DHCPv4{
		ClientHWAddr: hwaddr,
	}
	req.UpdateOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeRelease))

	resp := &dhcpv4.DHCPv4{}

	// Close the database to simulate storage failure
	db.Close()

	result, stop := pl.Handler4(req, resp)

	assert.Nil(t, result, "Should return nil on storage failure")
	assert.True(t, stop, "Should stop processing on storage failure")

	_, exists := pl.Recordsv4[hwaddr.String()]
	assert.True(t, exists, "Record should still exist in memory after storage failure")

	mockAlloc.AssertNotCalled(t, "Free")
	mockAlloc.AssertNotCalled(t, "Allocate")
}

func TestLoadRecords6(t *testing.T) {
	db, err := loadDB(":memory:")
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer db.Close()

	// Insert test IPv6 records using hex encoding with valid DUID format
	testRecords := []struct {
		duid []byte
		ip   string
	}{
		{[]byte{0x00, 0x01, 0x00, 0x01, 0x00, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}, "2001:db8::1"},
		{[]byte{0x00, 0x01, 0x00, 0x01, 0x00, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x09}, "2001:db8::2"},
		{[]byte{0x00, 0x01, 0x00, 0x01, 0x00, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x0a}, "2001:db8::3"},
	}

	for _, rec := range testRecords {
		stmt, err := db.Prepare("insert into leases6(duid, ip, expiry, hostname) values (?, ?, ?, ?)")
		if err != nil {
			t.Fatalf("Failed to prepare insert: %v", err)
		}
		defer stmt.Close()
		// Use hex encoding to match what saveIPAddress6 does
		duidStr := hex.EncodeToString(rec.duid)
		if _, err := stmt.Exec(duidStr, rec.ip, 946684800, "test"); err != nil {
			t.Fatalf("Failed to insert record: %v", err)
		}
	}

	records, err := loadRecords6(db)
	if err != nil {
		t.Fatalf("Failed to load IPv6 records: %v", err)
	}

	assert.Len(t, records, 3, "Should have loaded 3 records")

	// Verify each record using hex encoding
	for _, rec := range testRecords {
		duidStr := hex.EncodeToString(rec.duid)
		record, exists := records[duidStr]
		assert.True(t, exists, "Record should exist for DUID %s", duidStr)
		if exists {
			assert.Equal(t, rec.ip, record.IP.String(), "IP should match")
		}
	}
}

func TestSaveIPAddress6(t *testing.T) {
	pl := PluginState{}
	if err := pl.registerBackingDB(":memory:"); err != nil {
		t.Fatalf("Could not setup DB: %v", err)
	}

	duid := []byte{0x00, 0x01, 0x00, 0x01, 0x00, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	record := &Record{
		IP:       net.ParseIP("2001:db8::100"),
		expires:  946684800,
		hostname: "test-host",
	}

	err := pl.saveIPAddress6(duid, record)
	assert.NoError(t, err, "Should save IPv6 lease without error")

	// Verify the record was saved
	records, err := loadRecords6(pl.leasedb)
	if err != nil {
		t.Fatalf("Failed to load records: %v", err)
	}

	// Use hex encoding to match what saveIPAddress6 does
	duidStr := hex.EncodeToString(duid)
	savedRecord, exists := records[duidStr]
	assert.True(t, exists, "Record should exist for key %s", duidStr)
	assert.NotNil(t, savedRecord, "Saved record should not be nil")
	if savedRecord != nil {
		assert.Equal(t, "2001:db8::100", savedRecord.IP.String(), "IP should match")
		assert.Equal(t, "test-host", savedRecord.hostname, "Hostname should match")
	}
}

func TestFreeIPAddress6(t *testing.T) {
	db, err := loadDB(":memory:")
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}

	duid := []byte{0x00, 0x01, 0x00, 0x01, 0x00, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	record := &Record{
		IP:       net.ParseIP("2001:db8::100"),
		expires:  946684800,
		hostname: "test-host",
	}

	// Insert a test record using hex encoding
	stmt, err := db.Prepare("insert into leases6(duid, ip, expiry, hostname) values (?, ?, ?, ?)")
	if err != nil {
		t.Fatalf("Failed to prepare insert: %v", err)
	}
	defer stmt.Close()
	duidStr := hex.EncodeToString(duid)
	if _, err := stmt.Exec(duidStr, record.IP.String(), record.expires, record.hostname); err != nil {
		t.Fatalf("Failed to insert record: %v", err)
	}

	pl := PluginState{leasedb: db}

	// Verify record exists
	records, err := loadRecords6(pl.leasedb)
	if err != nil {
		t.Fatalf("Failed to load records: %v", err)
	}
	assert.Len(t, records, 1, "Should have 1 record before deletion")

	// Free the record
	err = pl.freeIPAddress6(duid, record)
	assert.NoError(t, err, "Should free IPv6 lease without error")

	// Verify record was deleted
	records, err = loadRecords6(pl.leasedb)
	if err != nil {
		t.Fatalf("Failed to load records after deletion: %v", err)
	}
	assert.Len(t, records, 0, "Should have 0 records after deletion")
}

func TestRecordKey6(t *testing.T) {
	// Use a valid DUID format (DUID-LLT: type + hwtype + time + lladdr)
	// Type 1 = DUID-LLT, HW type 1 = Ethernet
	duid, err := dhcpv6.DUIDFromBytes([]byte{0x00, 0x01, 0x00, 0x01, 0x00, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08})
	if err != nil {
		t.Fatalf("Failed to create DUID: %v", err)
	}

	key := recordKey6(duid)
	// hex.EncodeToString produces lowercase hex with leading zeros
	// 00 01 00 01 00 02 03 04 05 06 07 08 -> 000100010002030405060708
	assert.Equal(t, "000100010002030405060708", key, "DUID should be converted to hex string with leading zeros")
}

func TestIPv6AllocatorImport(t *testing.T) {
	// This test ensures the IPv6 allocator can be imported and created
	start := net.ParseIP("2001:db8::1")
	end := net.ParseIP("2001:db8::ff")

	alloc, err := bitmap.NewIPv6Allocator(start, end)
	assert.NoError(t, err, "Should create IPv6 allocator")
	assert.NotNil(t, alloc, "Allocator should not be nil")
}

// TestHandler6Renew tests RENEW message handling for existing clients
func TestHandler6Renew(t *testing.T) {
	db, err := loadDB(":memory:")
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer db.Close()

	// Create allocator
	start := net.ParseIP("2001:db8::1")
	end := net.ParseIP("2001:db8::ff")
	allocator, err := bitmap.NewIPv6Allocator(start, end)
	if err != nil {
		t.Fatalf("Failed to create allocator: %v", err)
	}

	pl := PluginState{
		leasedb:            db,
		Recordsv6:          make(map[string]*Record),
		allocator6:         allocator,
		LeaseTime:          3600 * time.Second,
		allocators6ByClass: make(map[string]allocators.Allocator),
	}

	// Create client DUID
	duid := []byte{0x00, 0x01, 0x00, 0x01, 0x00, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	duidObj, err := dhcpv6.DUIDFromBytes(duid)
	if err != nil {
		t.Fatalf("Failed to create DUID: %v", err)
	}

	// Create an existing lease
	existingIP := net.ParseIP("2001:db8::10")
	record := &Record{
		IP:       existingIP,
		expires:  int(time.Now().Add(1800 * time.Second).Unix()), // Expires in 30 min
		hostname: "test-host",
	}

	// Save the lease
	err = pl.saveIPAddress6(duid, record)
	if err != nil {
		t.Fatalf("Failed to save lease: %v", err)
	}
	pl.Recordsv6[hex.EncodeToString(duid)] = record

	// Allocate the IP in the allocator
	_, err = allocator.Allocate(net.IPNet{IP: existingIP})
	if err != nil {
		t.Fatalf("Failed to allocate IP: %v", err)
	}

	// Create a RENEW message
	req, err := dhcpv6.NewMessage()
	if err != nil {
		t.Fatalf("Failed to create message: %v", err)
	}
	req.MessageType = dhcpv6.MessageTypeRenew
	req.AddOption(dhcpv6.OptClientID(duidObj))

	// Add IA_NA option with the existing address
	iana := &dhcpv6.OptIANA{
		IaId: [4]byte{0x01, 0x02, 0x03, 0x04},
	}
	iana.Options.Add(&dhcpv6.OptIAAddress{
		IPv6Addr:          existingIP,
		PreferredLifetime: 1800 * time.Second,
		ValidLifetime:     1800 * time.Second,
	})
	req.AddOption(iana)

	resp, err := dhcpv6.NewMessage()
	if err != nil {
		t.Fatalf("Failed to create response: %v", err)
	}

	// Handle the RENEW
	result, stop := pl.Handler6(req, resp)

	assert.NotNil(t, result, "Should return a response")
	assert.False(t, stop, "Should not stop processing")

	// Verify the lease was extended
	records, err := loadRecords6(pl.leasedb)
	if err != nil {
		t.Fatalf("Failed to load records: %v", err)
	}

	key := hex.EncodeToString(duid)
	renewedRecord, exists := records[key]
	assert.True(t, exists, "Record should exist after renewal")
	assert.NotNil(t, renewedRecord, "Record should not be nil")

	// The expiry should have been extended to approximately LeaseTime from now
	newExpiry := time.Unix(int64(renewedRecord.expires), 0)
	timeUntilNewExpiry := newExpiry.Sub(time.Now())
	assert.True(t, timeUntilNewExpiry > 3500*time.Second && timeUntilNewExpiry <= 3600*time.Second,
		"Lease should be extended to approximately LeaseTime (got %v)", timeUntilNewExpiry)
}

// TestHandler6RenewMismatchAddress tests RENEW with mismatched address
func TestHandler6RenewMismatchAddress(t *testing.T) {
	db, err := loadDB(":memory:")
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer db.Close()

	// Create allocator
	start := net.ParseIP("2001:db8::1")
	end := net.ParseIP("2001:db8::ff")
	allocator, err := bitmap.NewIPv6Allocator(start, end)
	if err != nil {
		t.Fatalf("Failed to create allocator: %v", err)
	}

	pl := PluginState{
		leasedb:            db,
		Recordsv6:          make(map[string]*Record),
		allocator6:         allocator,
		LeaseTime:          3600 * time.Second,
		allocators6ByClass: make(map[string]allocators.Allocator),
	}

	// Create client DUID
	duid := []byte{0x00, 0x01, 0x00, 0x01, 0x00, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	duidObj, err := dhcpv6.DUIDFromBytes(duid)
	if err != nil {
		t.Fatalf("Failed to create DUID: %v", err)
	}

	// Create an existing lease with different IP
	existingIP := net.ParseIP("2001:db8::10")
	record := &Record{
		IP:       existingIP,
		expires:  int(time.Now().Add(1800 * time.Second).Unix()),
		hostname: "test-host",
	}

	pl.Recordsv6[hex.EncodeToString(duid)] = record
	_, err = allocator.Allocate(net.IPNet{IP: existingIP})
	if err != nil {
		t.Fatalf("Failed to allocate IP: %v", err)
	}

	// Create a RENEW message with a different requested address
	req, err := dhcpv6.NewMessage()
	if err != nil {
		t.Fatalf("Failed to create message: %v", err)
	}
	req.MessageType = dhcpv6.MessageTypeRenew
	req.AddOption(dhcpv6.OptClientID(duidObj))

	// Add IA_NA option with DIFFERENT address
	iana := &dhcpv6.OptIANA{
		IaId: [4]byte{0x01, 0x02, 0x03, 0x04},
	}
	wrongIP := net.ParseIP("2001:db8::20")
	iana.Options.Add(&dhcpv6.OptIAAddress{
		IPv6Addr:          wrongIP,
		PreferredLifetime: 1800 * time.Second,
		ValidLifetime:     1800 * time.Second,
	})
	req.AddOption(iana)

	resp, err := dhcpv6.NewMessage()
	if err != nil {
		t.Fatalf("Failed to create response: %v", err)
	}

	// Handle the RENEW
	result, stop := pl.Handler6(req, resp)

	// Should return the correct address (from record), not the requested one
	assert.NotNil(t, result, "Should return a response")
	assert.False(t, stop, "Should not stop processing")

	// Verify response contains the correct address
	resultMsg := result.(*dhcpv6.Message)
	ianaResp := resultMsg.Options.IANA()
	assert.Len(t, ianaResp, 1, "Should have one IA_NA in response")

	addrs := ianaResp[0].Options.Addresses()
	assert.Len(t, addrs, 1, "Should have one address in response")
	assert.Equal(t, existingIP, addrs[0].IPv6Addr, "Should return the recorded address, not the requested one")
}

// TestHandler6Rebind tests REBIND message handling
func TestHandler6Rebind(t *testing.T) {
	db, err := loadDB(":memory:")
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer db.Close()

	// Create allocator
	start := net.ParseIP("2001:db8::1")
	end := net.ParseIP("2001:db8::ff")
	allocator, err := bitmap.NewIPv6Allocator(start, end)
	if err != nil {
		t.Fatalf("Failed to create allocator: %v", err)
	}

	pl := PluginState{
		leasedb:            db,
		Recordsv6:          make(map[string]*Record),
		allocator6:         allocator,
		LeaseTime:          3600 * time.Second,
		allocators6ByClass: make(map[string]allocators.Allocator),
	}

	// Create client DUID
	duid := []byte{0x00, 0x01, 0x00, 0x01, 0x00, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	duidObj, err := dhcpv6.DUIDFromBytes(duid)
	if err != nil {
		t.Fatalf("Failed to create DUID: %v", err)
	}

	// Create an existing lease that's about to expire
	existingIP := net.ParseIP("2001:db8::10")
	record := &Record{
		IP:       existingIP,
		expires:  int(time.Now().Add(100 * time.Second).Unix()), // Expires soon
		hostname: "test-host",
	}

	pl.Recordsv6[hex.EncodeToString(duid)] = record
	_, err = allocator.Allocate(net.IPNet{IP: existingIP})
	if err != nil {
		t.Fatalf("Failed to allocate IP: %v", err)
	}

	// Create a REBIND message (sent when T2 expires and client contacts any server)
	req, err := dhcpv6.NewMessage()
	if err != nil {
		t.Fatalf("Failed to create message: %v", err)
	}
	req.MessageType = dhcpv6.MessageTypeRebind
	req.AddOption(dhcpv6.OptClientID(duidObj))

	iana := &dhcpv6.OptIANA{
		IaId: [4]byte{0x01, 0x02, 0x03, 0x04},
	}
	iana.Options.Add(&dhcpv6.OptIAAddress{
		IPv6Addr:          existingIP,
		PreferredLifetime: 100 * time.Second,
		ValidLifetime:     100 * time.Second,
	})
	req.AddOption(iana)

	resp, err := dhcpv6.NewMessage()
	if err != nil {
		t.Fatalf("Failed to create response: %v", err)
	}

	// Handle the REBIND
	result, stop := pl.Handler6(req, resp)

	assert.NotNil(t, result, "Should return a response")
	assert.False(t, stop, "Should not stop processing")

	// Verify the lease was extended
	key := hex.EncodeToString(duid)
	renewedRecord := pl.Recordsv6[key]
	assert.NotNil(t, renewedRecord, "Record should exist after REBIND")

	newExpiry := time.Unix(int64(renewedRecord.expires), 0)
	assert.True(t, newExpiry.After(time.Now().Add(3500*time.Second)), "Lease should be extended to full LeaseTime")
}
