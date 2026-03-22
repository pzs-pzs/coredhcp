#!/bin/bash
# CoreDHCP DHCPv6 部署包构建脚本

set -e

echo "========================================"
echo "CoreDHCP DHCPv6 部署包构建 (Linux amd64)"
echo "========================================"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$SCRIPT_DIR/.."
DIST_DIR="$SCRIPT_DIR/dist"

# 检查 Go 环境
echo -e "${YELLOW}[1/3]${NC} 检查环境..."
if ! command -v go &> /dev/null; then
    echo "错误: 未找到 Go"
    exit 1
fi
echo -e "  ${GREEN}✓ Go $(go version | awk '{print $3}')${NC}"

# 编译
echo -e "${YELLOW}[2/3]${NC} 编译 Linux amd64 二进制..."
cd "$PROJECT_ROOT/cmds/coredhcp-generator"
go build -o coredhcp-generator 2>/dev/null || true
./coredhcp-generator --from core-plugins.txt > /dev/null

cd "$PROJECT_ROOT"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o "$DIST_DIR/coredhcp" ./cmds/coredhcp-generator/*.go
chmod +x "$DIST_DIR/coredhcp"
echo -e "  ${GREEN}✓ $DIST_DIR/coredhcp${NC}"

# 创建配置文件
echo -e "${YELLOW}[3/3]${NC} 创建配置文件..."

cat > "$DIST_DIR/config.yaml" << 'YAML'
server6:
    listen: "[::]:547"
    plugins:
        - server_id: "LL 00:de:ad:be:ef:00"
        - classify: "classes.yaml"
        - range: "range6.yaml"
        - dns: "2001:4860:4860::8888 2001:4860:4860::8844"

server4:
    listen: "0.0.0.0:67"
    plugins:
        - classify: "classes4.yaml"
        - range: "range4.yaml"
        - dns: "8.8.8.8 8.8.4.4"
YAML

cat > "$DIST_DIR/classes.yaml" << 'YAML'
classes:
  - name: "android"
    conditions:
      vendor_class_match: ["android*"]
  - name: "ios"
    conditions:
      vendor_class_match: ["apple*"]
  - name: "iot"
    conditions:
      mac_prefix: ["B8:27:EB", "DC:4F:22"]
  - name: "virtual"
    conditions:
      mac_prefix: ["00:0C:29", "08:00:27"]
  - name: "branch1"
    conditions:
      link_address: ["2001:db8:1::1"]
  - name: "branch2"
    conditions:
      link_address: ["2001:db8:2::1"]
YAML

cat > "$DIST_DIR/classes4.yaml" << 'YAML'
classes:
  - name: "iot"
    conditions:
      mac_prefix: ["B8:27:EB", "DC:4F:22"]
YAML

cat > "$DIST_DIR/range6.yaml" << 'YAML'
database: "leases6.sqlite3"
lease_time: "3600s"

default_range:
  start: "2001:db8::100"
  end: "2001:db8::1ff"

class_ranges:
  - name: "android"
    range:
      start: "2001:db8:1000::100"
      end: "2001:db8:1000::1ff"
  - name: "ios"
    range:
      start: "2001:db8:1000::200"
      end: "2001:db8:1000::2ff"
  - name: "iot"
    range:
      start: "2001:db8:2000::100"
      end: "2001:db8:2000::1ff"
  - name: "virtual"
    range:
      start: "2001:db8:3000::100"
      end: "2001:db8:3000::1ff"
  - name: "branch1"
    range:
      start: "2001:db8:1::1000"
      end: "2001:db8:1::1fff"
  - name: "branch2"
    range:
      start: "2001:db8:2::1000"
      end: "2001:db8:2::1fff"
YAML

cat > "$DIST_DIR/range4.yaml" << 'YAML'
database: "leases4.sqlite3"
lease_time: "7200s"

default_range:
  start: "192.168.1.100"
  end: "192.168.1.200"

class_ranges:
  - name: "iot"
    range:
      start: "192.168.10.100"
      end: "192.168.10.200"
YAML

cat > "$DIST_DIR/README.md" << 'README'
# CoreDHCP DHCPv6 部署包

## 运行

```bash
# 直接运行
./coredhcp -config config.yaml

# 后台运行
nohup ./coredhcp -config config.yaml &
```

## 功能

- DHCPv6 动态地址分配 (IA_NA)
- DHCPv4 动态地址分配
- 客户端分类
- 中继场景支持 (link-address)
README

# 打包
cd "$DIST_DIR"
tar czf coredhcp-dhcpv6-deploy.tar.gz \
    coredhcp config.yaml classes.yaml classes4.yaml range6.yaml range4.yaml README.md

echo ""
echo "========================================"
echo -e "${GREEN}构建完成！${NC}"
echo "========================================"
echo ""
echo "部署包: $DIST_DIR/coredhcp-dhcpv6-deploy.tar.gz"
ls -lh "$DIST_DIR/coredhcp-dhcpv6-deploy.tar.gz"
