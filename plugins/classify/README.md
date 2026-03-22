# Classify Plugin

The `classify` plugin implements client classification based on various DHCP options and characteristics. It allows you to categorize DHCP clients into different classes and apply different behaviors based on those classifications.

## Features

- **Multiple Matching Criteria**: Classify clients by MAC address, DUID, Vendor Class, User Class, Architecture Type, and Interface ID
- **Wildcard Pattern Matching**: Support for `*` wildcards in vendor class matching
- **Dual Protocol Support**: Works with both DHCPv4 and DHCPv6
- **Shared State**: Classification results stored in-memory for access by other plugins
- **Priority-Based**: Classes evaluated in order; first match wins

## Configuration

The plugin requires a YAML configuration file defining client classes:

```yaml
classes:
  - name: "android"
    conditions:
      vendor_class_match:
        - "android*"

  - name: "ios"
    conditions:
      vendor_class_match:
        - "apple*"
      arch_type:
        - 32  # ARM architecture

  - name: "windows"
    conditions:
      vendor_class:
        - "MSFT"
        - "Microsoft"
```

## Matching Conditions

### DHCPv6 Specific

| Condition | Description | Example |
|-----------|-------------|---------|
| `duid_prefix` | Match by DUID prefix (hex) | `00010001` |
| `duid_exact` | Exact DUID match (hex) | `0001000123456789ABCD` |
| `arch_type` | Client architecture type | `182` (ARM64) |
| `interface_id` | Interface ID (hex) | `0000abcd` |
| `user_class` | User class string | `PXEClient` |
| `link_address` | Relay agent's link-address (for relay scenarios) | `2001:db8:1::1` |
| `remote_id` | Relay agent's remote ID (hex) | `00010001abcd` |

### DHCPv4 and DHCPv6

| Condition | Description | Example |
|-----------|-------------|---------|
| `mac_prefix` | MAC address prefix (OUI) | `00:00:0C` (Cisco) |
| `mac_exact` | Exact MAC address | `aa:bb:cc:dd:ee:ff` |
| `vendor_class` | Exact vendor class match | `MSFT` |
| `vendor_class_match` | Wildcard pattern match | `android*` |

## Wildcard Matching

The `vendor_class_match` condition supports `*` wildcards:

- `android*` - matches anything starting with "android"
- `*android` - matches anything ending with "android"
- `*android*` - matches anything containing "android"
- `android-*-7` - matches patterns like "android-test-7"

## Server Configuration

### DHCPv6

```yaml
server6:
    listen: "[::]:547"
    plugins:
        - server_id: LL 00:de:ad:be:ef:00
        - classify: "/etc/coredhcp/classes.yaml"
        - range: "leases6.sqlite3 2001:db8::1 2001:db8::1000 3600s"
        - dns: 2001:4860:4860::8888
```

### DHCPv4

```yaml
server4:
    listen: "0.0.0.0:67"
    plugins:
        - classify: "/etc/coredhcp/classes.yaml"
        - range: "leases4.sqlite3 192.168.1.100 192.168.1.200 3600s"
        - dns: 8.8.8.8
```

## Accessing Classification Results

Other plugins can access classification results using the exported functions:

### For DHCPv6

```go
import "github.com/coredhcp/coredhcp/plugins/classify"

// Get class name by DUID
className := classify.GetClassForDUID(duid)

// Or by DUID hex string
className := classify.GetClassForDUIDString("0001000123456789")
```

### For DHCPv4

```go
import "github.com/coredhcp/coredhcp/plugins/classify"

// Get class name by MAC address
className := classify.GetClassForMAC(macAddr)

// Or by MAC string
className := classify.GetClassForMACString("aa:bb:cc:dd:ee:ff")
```

## DHCP Relay Scenarios

The classify plugin works with DHCP relay agents (routers) that forward client requests to the CoreDHCP server.

### How It Works

When a router acts as a DHCP relay agent:

1. Client sends DHCP request to the router
2. Router encapsulates the request in a relay message and forwards to CoreDHCP
3. CoreDHCP extracts client information from the relay message

### Automatic Client Information Extraction

The classify plugin automatically extracts client information from relayed messages:

- **MAC Address**: Extracted from DUID-LL/DUID-LLT or relay link-layer address option
- **Vendor Class**: Extracted from the embedded client message
- **User Class**: Extracted from relayed options
- **DUID**: Extracted from ClientID option

### Relay Configuration Example

```yaml
# /etc/coredhcp/classes.yaml
classes:
  # Classify devices by MAC prefix even through relay
  - name: "iot-devices"
    conditions:
      mac_prefix:
        - "B8:27:EB"  # Raspberry Pi
        - "DC:4F:22"  # ESP32

  # Classify mobile devices
  - name: "mobile-clients"
    conditions:
      vendor_class_match:
        - "android*"
        - "apple*"
```

### Router Relay Configuration

**OpenWrt:**
```bash
config dhcp 'relay'
    option interface 'lan'
    option relay_server '10.0.0.2'  # CoreDHCP server IP
```

**Cisco IOS:**
```
interface Vlan100
 ip helper-address 10.0.0.2
```

### Multi-Site Deployment

For multiple sites using relays to a central CoreDHCP server:

```yaml
# Classify by device OUI to determine site
classes:
  - name: "site1-iot"
    conditions:
      mac_prefix:
        - "00:11:22"  # Site1 devices

  - name: "site2-iot"
    conditions:
      mac_prefix:
        - "33:44:55"  # Site2 devices
```

Use with the `file` plugin for static IP assignment per site:

```
# leases6.txt
# Site1 clients - 2001:db8:1::/64
00010001000001: 2001:db8:1::100

# Site2 clients - 2001:db8:2::/64
00010001000002: 2001:db8:2::100
```

For more details on relay scenarios, see [docs/relay.md](../docs/relay.md).

## Example Configurations

### Mobile Device Detection

```yaml
classes:
  - name: "android"
    conditions:
      vendor_class_match:
        - "android*"

  - name: "ios"
    conditions:
      vendor_class_match:
        - "apple*"
        - "iOS*"
```

### IoT Device Classification

```yaml
classes:
  - name: "iot-esp"
    conditions:
      mac_prefix:
        - "84:CC:A8"  # Espressif (ESP32)
        - "DC:4F:22"  # Espressif (ESP32-S2)

  - name: "iot-rpi"
    conditions:
      mac_prefix:
        - "B8:27:EB"  # Raspberry Pi
```

### Virtual Machine Detection

```yaml
classes:
  - name: "virtual"
    conditions:
      mac_prefix:
        - "00:0C:29"  # VMware
        - "00:50:56"  # VMware
        - "08:00:27"  # VirtualBox
        - "52:54:00"  # QEMU/KVM
```

### Network Equipment

```yaml
classes:
  - name: "cisco"
    conditions:
      vendor_class_match:
        - "cisco*"
      mac_prefix:
        - "00:00:0C"
        - "00:1B:D5"

  - name: "printer"
    conditions:
      vendor_class_match:
        - "hp*"
        - "canon*"
        - "epson*"
```

### PXE Boot Clients

```yaml
classes:
  - name: "pxe-client"
    conditions:
      user_class:
        - "PXEClient"
        - "pxeclient"
      arch_type:
        - 0   # Intel x86PC
        - 6   # EFI IA64
        - 7   # EFI x86-64
        - 9   # EFI x86-64
```

## Architecture Types

Common architecture type values for DHCPv6 Option 61:

| Value | Architecture |
|-------|--------------|
| 0 | Intel x86PC |
| 1 | NEC/PC98 |
| 2 | Itanium |
| 3 | DEC Alpha |
| 4 | Arc x86 |
| 5 | Intel Lean Client |
| 6 | EFI IA64 |
| 7 | EFI x86-64 |
| 9 | EFI x86-64 |
| 12 | EFI ARM64 |
| 182 | ARM64 (0xB6) |

## MAC OUI Reference

Common vendor MAC prefixes:

| Prefix | Vendor |
|--------|--------|
| 00:00:0C | Cisco |
| 00:50:56 | VMware |
| 08:00:27 | VirtualBox |
| 00:0C:29 | VMware |
| 52:54:00 | QEMU/KVM |
| B8:27:EB | Raspberry Pi |
| 84:CC:A8 | Espressif (ESP32) |
| DC:4F:22 | Espressif (ESP32-S2) |

## Behavior Notes

1. **Class Priority**: Classes are evaluated in the order they appear in the configuration file. The first matching class wins.

2. **OR Logic**: Multiple conditions within a class use OR logic - a client matches if ANY condition matches.

3. **Case Sensitivity**: Matching is generally case-insensitive for string comparisons.

4. **Partial MAC Prefix**: MAC prefixes can be partial (e.g., `02:42` for Docker) and don't require the full 6-byte address.

5. **Thread Safety**: Classification results are stored in thread-safe maps and can be accessed concurrently by multiple goroutines.
