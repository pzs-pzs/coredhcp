# DHCP Relay (中继) 使用指南

## 网络拓扑

```
+------------------+         +------------------+         +------------------+
|                  |         |                  |         |                  |
|   DHCP Client    |-------->|  Relay Agent     |-------->|   CoreDHCP       |
|   (192.168.1.x)  |         |  (Router)        |         |   Server         |
|                  |         |  192.168.1.1     |         |  10.0.0.2        |
+------------------+         +------------------+         +------------------+
```

## 中继工作原理

1. **客户端** 发送 DHCP Discover/Request 到路由器（作为中继代理）
2. **中继代理** 将请求封装在 Relay-Forward 消息中转发给 CoreDHCP 服务器
3. **CoreDHCP** 处理请求并通过中继代理返回响应

## 关键字段

| 字段 | 说明 | 来源 |
|------|------|------|
| giaddr (Gateway IP Address) | 中继代理的IP地址 | 中继代理添加 |
| Link-Layer Address | 客户端MAC地址（中继消息中） | 中继代理添加 |
| Peer Address | 客户端链路本地地址 | 中继消息 |
| Relay Message Info | 嵌套的原始DHCP消息 | 客户端原始请求 |

## CoreDHCP 中继支持

### IPv4 中继

在 DHCPv4 中，中继代理设置 `giaddr` 字段。CoreDHCP 可以通过 `giaddr` 识别中继请求。

```yaml
server4:
    listen: "0.0.0.0:67"
    plugins:
        - classify: "/etc/coredhcp/classes.yaml"
        - range: "leases.sqlite3 192.168.1.100 192.168.1.200 3600s"
```

### IPv6 中继

在 DHCPv6 中，中继代理使用 `Relay-Forward` 消息格式。CoreDHCP 会自动解析嵌套的客户端消息。

## Classify Plugin 与中继

### 自动提取客户端信息

`classify` 插件会自动从中继消息中提取客户端信息：

```go
// handler6.go 中已实现
func (p *PluginState) extractClientInfoV6(req dhcpv6.DHCPv6, msg *dhcpv6.Message) *ClientInfo {
    // 1. 从 DUID 提取 MAC
    if info.DUID != nil {
        if mac := extractMACFromDUID(info.DUID); mac != nil {
            info.MAC = mac
        }
    }

    // 2. 尝试从中继信息提取 MAC
    if info.MAC == nil {
        if mac, err := dhcpv6.ExtractMAC(req); err == nil && mac != nil {
            info.MAC = mac
        }
    }
}
```

### 中继场景配置示例

```yaml
# /etc/coredhcp/classes.yaml
classes:
  # 按客户端MAC前缀分类（中继场景下仍然有效）
  - name: "iot-devices"
    conditions:
      mac_prefix:
        - "B8:27:EB"  # Raspberry Pi
        - "DC:4F:22"  # ESP32

  # 按厂商类别分类（从中继消息中提取）
  - name: "android-clients"
    conditions:
      vendor_class_match:
        - "android*"
```

## 路由器中继配置

### OpenWrt/LEDE 配置

```bash
# /etc/config/dhcp
config dhcp 'lan'
    option interface 'lan'
    option ignore '0'

# 配置中继到远程服务器
config dhcp 'relay'
    option interface 'lan'
    option relay_server '10.0.0.2'  # CoreDHCP 服务器地址
```

### Cisco IOS 配置

```
interface Vlan100
 ip helper-address 10.0.0.2  # CoreDHCP 服务器地址
```

### Linux isc-dhcp-relay

```bash
# /etc/default/isc-dhcp-relay
SERVERS="10.0.0.2"
INTERFACES="eth0 eth1"

# 启动中继
systemctl start isc-dhcp-relay
```

## 测试中继场景

```bash
# 1. 启动 CoreDHCP 服务器
sudo coredhcp -config config.yaml

# 2. 使用客户端工具测试
cd cmds/client
go build
sudo ./client -6
```

## 多网段部署示例

### 场景：三个分支机构通过中继连接到总部 CoreDHCP

```
分支机构1 (192.168.1.0/24) --路由器--> \
分支机构2 (192.168.2.0/24) --路由器--> 总部 CoreDHCP (10.0.0.2)
分支机构3 (192.168.3.0/24) --路由器--> /
```

```yaml
# /etc/coredhcp/branches.yaml
classes:
  - name: "branch1-clients"
    conditions:
      mac_prefix:
        - "00:11:22"  # 分支1设备OUI

  - name: "branch2-clients"
    conditions:
      mac_prefix:
        - "33:44:55"  # 分支2设备OUI
```

```bash
# leases6.txt - 为不同分支分配不同网段
# 分支1 - 2001:db8:1::/64
00:11:22:33:44:55:66:77: 2001:db8:1::100

# 分支2 - 2001:db8:2::/64  
33:44:55:66:77:88:99:aa: 2001:db8:2::100
```

## 故障排查

### 中继请求未到达服务器

```bash
# 在服务器上抓包查看
sudo tcpdump -i any port 547 -w relay.pcap
```

### 查看详细日志

```bash
# 启用调试日志
coredhcp -config config.yaml -log-level debug

# 查看提取的客户端信息
# 日志会显示：DUID、MAC地址、VendorClass、匹配的类别
```
