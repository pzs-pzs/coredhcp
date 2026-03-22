# Link-Address 快速参考

## 什么是 Link-Address？

在 DHCPv6 中继消息中，`link-address` 字段包含中继代理（通常是路由器）的地址。当客户端通过中继请求 IP 地址时，CoreDHCP 可以使用这个地址来识别客户端来自哪个网络/分支机构。

## 快速配置

### 1. 定义分类（按中继地址）

```yaml
# classes.yaml
classes:
  - name: "branch1"
    conditions:
      link_address: ["2001:db8:1::1"]

  - name: "branch2"
    conditions:
      link_address: ["2001:db8:2::1"]
```

### 2. 配置地址池

```yaml
# range6.yaml
database: "leases6.sqlite3"
lease_time: "3600s"

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
```

### 3. 服务器配置

```yaml
# config.yaml
server6:
    plugins:
        - server_id: LL 00:de:ad:be:ef:00
        - classify: "classes.yaml"    # 先分类
        - range: "range6.yaml"         # 后分配
```

## 效果

| 客户端位置 | 中继 Link-Address | 分配的地址 |
|-----------|------------------|-----------|
| 分支1 | 2001:db8:1::1 | 2001:db8:1::1000 - 2001:db8:1::1fff |
| 分支2 | 2001:db8:2::1 | 2001:db8:2::1000 - 2001:db8:2::1fff |
| 总部（直连）| 无 | 2001:db8::100 - 2001:db8::1ff |

## 匹配规则

- **精确匹配**: `link_address: ["2001:db8:1::1"]` 只匹配该地址
- **前缀匹配**: `link_address: ["2001:db8:1::"]` 匹配该前缀的所有地址

## 路由器配置示例

### Cisco IOS
```
interface GigabitEthernet0/1
 ipv6 dhcp relay destination 2001:db8::100
 ipv6 address 2001:db8:1::1/64
```

### Juniper JunOS
```
set interfaces ge-0/0/0 unit 0 family inet6 address 2001:db8:1::1/64
set system services dhcpv6-relay server 2001:db8::100
```

## 故障排查

```bash
# 启用调试查看 link-address
sudo coredhcp -config config.yaml -log-level debug

# 日志会显示：
# Client info: LinkAddress=2001:db8:1::1
# Client classified as 'branch1'
# Using class-specific allocator for 'branch1'
```
