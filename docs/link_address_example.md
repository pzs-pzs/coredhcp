# 基于 Link-Address 的地址分配

## 概述

在 DHCPv6 中继场景中，`link-address` 是中继消息中的关键字段，表示中继代理的地址（通常是网关路由器的地址）。CoreDHCP 现在支持基于 `link-address` 进行客户端分类和地址分配。

## 网络拓扑

```
                        2001:db8:1::/64 (分支机构1)
                        +-------------+
                        |  Router-1   |
                        |  2001:db8:1::1
                        +-------------+
                               |
                        (中继 DHCPv6 请求)
                               |
    +-----------+       +-------------+       +------------------+
    |  Client   |-------|  Relay      |-------|   CoreDHCP       |
    +-----------+       |  Agent       |       |   Server         |
                        +-------------+       |  2001:db8::100    |
                                              |  link-address:    |
                                              |  2001:db8:1::1    |
                                              +------------------+
```

## 配置示例

### 场景：按中继网关（分支机构）分配地址段

```yaml
# /etc/coredhcp/classes.yaml
classes:
  # 分支机构1 - 通过 link-address 识别
  - name: "branch1"
    conditions:
      link_address:
        - "2001:db8:1::1"    # Router-1 的 link-address

  # 分支机构2
  - name: "branch2"
    conditions:
      link_address:
        - "2001:db8:2::1"    # Router-2 的 link-address

  # 分支机构3
  - name: "branch3"
    conditions:
      link_address:
        - "2001:db8:3::1"    # Router-3 的 link-address

  # 总部（默认）
  - name: "headquarters"
    conditions:
      link_address:
        - "2001:db8::1"     # 总部路由器的 link-address
```

```yaml
# /etc/coredhcp/range6.yaml
database: "leases6.sqlite3"
lease_time: "3600s"

# 默认地址池（总部）
default_range:
  start: "2001:db8::100"
  end: "2001:db8::1ff"

# 分支机构地址池
class_ranges:
  - name: "branch1"
    range:
      start: "2001:db8:1:100::"
      end: "2001:db8:1:1ff::"

  - name: "branch2"
    range:
      start: "2001:db8:2:100::"
      end: "2001:db8:2:1ff::"

  - name: "branch3"
    range:
      start: "2001:db8:3:100::"
      end: "2001:db8:3:1ff::"
```

```yaml
# /etc/coredhcp/config.yaml
server6:
    listen: "[::]:547"
    plugins:
        - server_id: LL 00:de:ad:be:ef:00
        - classify: "/etc/coredhcp/classes.yaml"
        - range: "/etc/coredhcp/range6.yaml"
        - dns: 2001:4860:4860::8888
```

### 场景：按子网前缀匹配

```yaml
# /etc/coredhcp/classes.yaml
classes:
  # 所有来自 2001:db8:10::/64 网段的中继
  - name: "subnet-10"
    conditions:
      link_address:
        - "2001:db8:10::"

  # 所有来自 2001:db8:20::/64 网段的中继
  - name: "subnet-20"
    conditions:
      link_address:
        - "2001:db8:20::"
```

### 场景：组合条件

```yaml
# /etc/coredhcp/classes.yaml
classes:
  # 分支机构1 的 Android 设备
  - name: "branch1-android"
    conditions:
      link_address:
        - "2001:db8:1::1"
      vendor_class_match:
        - "android*"

  # 分支机构1 的 IoT 设备
  - name: "branch1-iot"
    conditions:
      link_address:
        - "2001:db8:1::1"
      mac_prefix:
        - "B8:27:EB"
        - "DC:4F:22"
```

## Link-Address 字段说明

### DHCPv6 Relay Message 格式

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|    MessageType = 12 (Relay-Forward)                        |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|           HopCount              |    Link-Address (16 bytes)  |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                                                               |
.                                                               .
.                  Link-Address (cont.)                        .
.                                                               .
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|           Peer-Address (16 bytes)                            |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                                                               |
.                                                               .
.                  Peer-Address (cont.)                        .
.                                                               .
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                                                               |
.                                                               .
.                 Relay Message Options                        .
.                                                               .
|                                                               |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

### 字段含义

| 字段 | 说明 |
|------|------|
| MessageType | 12 = Relay-Forward, 13 = Relay-Reply |
| HopCount | 中继跳数 |
| **Link-Address** | **中继代理的地址（本地链路地址）** |
| Peer-Address | 客户端地址（或上一跳中继的地址） |
| Options | 嵌套的原始 DHCPv6 消息 |

## Remote ID 支持

除了 `link_address`，还可以使用 `remote_id` 选项来识别中继代理：

```yaml
# /etc/coredhcp/classes.yaml
classes:
  # 通过 Remote ID 识别中继代理
  - name: "provider-relay"
    conditions:
      remote_id:
        - "00000001"  # Enterprise Number + relay-specific data

  # 组合 Link-Address 和 Remote ID
  - name: "branch1-relay"
    conditions:
      link_address:
        - "fe80::1"
      remote_id:
        - "00010001aabbccdd"
```

## 完整示例：多分支机构网络

```
                     总部 CoreDHCP (2001:db8::100)
                                |
        +-----------------------+-----------------------+
        |                       |                       |
   Branch 1                Branch 2                Branch 3
  2001:db8:1::/64         2001:db8:2::/64         2001:db8:3::/64
  Router: 2001:db8:1::1   Router: 2001:db8:2::1   Router: 2001:db8:3::1
```

### 配置文件

```yaml
# classes.yaml
classes:
  - name: "branch1"
    conditions:
      link_address: ["2001:db8:1::1"]

  - name: "branch2"
    conditions:
      link_address: ["2001:db8:2::1"]

  - name: "branch3"
    conditions:
      link_address: ["2001:db8:3::1"]
```

```yaml
# range6.yaml
database: "leases6.sqlite3"
lease_time: "7200s"

default_range:
  start: "2001:db8::100"
  end: "2001:db8::1ff"

class_ranges:
  - name: "branch1"
    range:
      start: "2001:db8:1::1000"
      end: "2001:db8:1::1fff"

  - name: "branch2"
    range:
      start: "2001:db8:2::1000"
      end: "2001:db8:2::1fff"

  - name: "branch3"
    range:
      start: "2001:db8:3::1000"
      end: "2001:db8:3::1fff"
```

### 客户端获得的地址

| 客户端位置 | Link-Address | 分配的地址段 |
|-----------|--------------|-------------|
| 分支机构1 | 2001:db8:1::1 | 2001:db8:1::1000 - 2001:db8:1::1fff |
| 分支机构2 | 2001:db8:2::1 | 2001:db8:2::1000 - 2001:db8:2::1fff |
| 分支机构3 | 2001:db8:3::1 | 2001:db8:3::1000 - 2001:db8:3::1fff |
| 总部 | 2001:db8::1 | 2001:db8::100 - 2001:db8::1ff (默认) |

## 中继代理配置

### Cisco IOS

```
! 配置 DHCPv6 中继
interface GigabitEthernet0/1
 ipv6 dhcp relay destination 2001:db8::100  ! CoreDHCP 服务器地址
 ipv6 address 2001:db8:1::1/64              ! 接口地址（将成为 link-address）
```

### Juniper JunOS

```
set interfaces ge-0/0/0 unit 0 family inet6 address 2001:db8:1::1/64
set system services dhcpv6-relay server 2001:db8::100
set system services dhcpv6-relay group relay-relay interface ge-0/0/0.0
```

### Linux (wide-dhcpv6-relay)

```bash
# /etc/wide-dhcpv6-relay.conf
server 2001:db8::100
interface eth0  # 中继接口，地址为 2001:db8:1::1
```

## 故障排查

### 查看收到的 Link-Address

启用调试日志查看中继信息：

```bash
sudo coredhcp -config config.yaml -log-level debug
```

日志中会显示：
```
Client info: DUID=..., MAC=..., LinkAddress=2001:db8:1::1
Client classified as 'branch1'
Using class-specific allocator for 'branch1'
found IPv6 address 2001:db8:1::1000 for DUID ...
```

### 常见问题

**问题1**: 客户端没有获得对应分支的地址

- 检查路由器的 link-address 是否正确配置
- 检查 classes.yaml 中的 link_address 是否与路由器地址一致
- 启用调试日志查看实际收到的 link-address

**问题2**: 所有客户端都获得默认地址

- 检查 classify 插件是否在 range 插件之前加载
- 检查 link-address 是否为空（直连场景没有 link-address）

**问题3**: 中继消息未到达服务器

```bash
# 抓包检查
sudo tcpdump -i any ip6 and port 547 -w relay.pcap
```

## 对比：Link-Address vs MAC OUI

| 方法 | 优点 | 缺点 | 适用场景 |
|------|------|------|---------|
| **Link-Address** | 与网络拓扑直接相关，易管理 | 依赖中继代理配置 | 多分支机构、多网段 |
| **MAC OUI** | 设备识别更精确 | 受设备更换影响 | 按设备类型分类 |
| **组合使用** | 灵活度高 | 配置较复杂 | 复杂网络环境 |
