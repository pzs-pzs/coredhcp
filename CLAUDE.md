# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

CoreDHCP is a fast, multithreaded, modular DHCP server written in Go. It supports both DHCPv4 and DHCPv6 protocols with a plugin-based architecture where almost all functionality is implemented as plugins.

## Key Architecture

### Plugin System
The entire server is built around plugins:
- All plugins are in `plugins/` directory
- Each plugin implements a `Plugin` struct with `Name`, `Setup6` (DHCPv6 setup), and `Setup4` (DHCPv4 setup) functions
- Setup functions return `Handler6` or `Handler4` functions that process packets
- Plugins are called in chain order; each handler returns `(packet, stop)` where `stop=true` ends the chain
- Example plugin: `plugins/example/plugin.go` is heavily commented and serves as the canonical reference
- Plugin registration happens in `cmds/coredhcp/main.go` via `plugins.RegisterPlugin()`

### Configuration
- Config is YAML-based, loaded via `config.Load()`
- Two separate config sections: `server6` and `server4`
- Each has `listen` (addresses to bind to) and `plugins` (ordered list)
- Config file searched in: `.`, `$XDG_CONFIG_HOME/coredhcp/`, `$HOME/.coredhcp/`, `/etc/coredhcp/`
- Example: `cmds/coredhcp/config.yml.example`

### Server Lifecycle
1. Load config
2. Register desired plugins (in main.go)
3. Call `plugins.LoadPlugins()` to instantiate handlers
4. `server.Start()` creates listeners and spawns goroutines per listener
5. `srv.Wait()` blocks until server stops

### Handler Types
- `Handler6 func(req, resp dhcpv6.DHCPv6) (dhcpv6.DHCPv6, bool)`
- `Handler4 func(req, resp *dhcpv4.DHCPv4) (*dhcpv4.DHCPv4, bool)`
- Defined in `handler/handler.go`

### Logging
Use `logger.GetLogger("prefix")` to get a logger instance. The prefix appears in log output for easy debugging.

### Main Entry Point
`cmds/coredhcp/main.go` is **generated** by `coredhcp-generator` - do NOT edit directly. To modify plugins:
1. Edit `cmds/coredhcp-generator/core-plugins.txt` to change plugin list
2. Run `coredhcp-generator` to regenerate `main.go`

## Common Development Commands

### Build
```bash
cd cmds/coredhcp && go build
```

### Run Server (requires root for raw socket access)
```bash
cd cmds/coredhcp && sudo ./coredhcp
```

### Unit Tests
```bash
# All packages
go test -v -race ./...

# Single package
go test -v -race ./plugins/...

# With coverage
go test -race -coverprofile=coverage.txt -covermode=atomic ./...
```

### Integration Tests
```bash
# Setup network namespaces for testing (requires root)
sudo ./.ci/setup-integ.sh

# Run integration tests
go test -tags=integration -race ./integ/...
```

### Lint
```bash
# Uses golangci-lint v2.5.0
golangci-lint run --timeout=5m
```

### Generate Custom Server
```bash
cd cmds/coredhcp-generator
go build
./coredhcp-generator --from core-plugins.txt [additional plugins...]
```

## Important Notes

- Requires Go 1.24+ (go.mod specifies 1.24.0, toolchain go1.24.2)
- All Go files must have the MIT license header (enforced by checklicenses in CI)
- Integration tests require `CAP_NET_ADMIN` (typically run as root)
- The test client is in `cmds/client/` for testing DHCP operations
- External plugins available at https://github.com/coredhcp/plugins

## Dependencies
- `github.com/insomniacslk/dhcp` - DHCP packet library (v4 and v6)
- `github.com/sirupsen/logrus` - Logging
- `github.com/spf13/viper` - Configuration handling
- `golang.org/x/net/{ipv4,ipv6}` - Raw socket handling
