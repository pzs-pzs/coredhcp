# Range 插件 - Link-Address 支持总结

## 新增功能

### 1. 基于分类的地址池分配

Range 插件现在支持根据客户端分类从不同的地址池分配 IP 地址。

#### 配置格式

**YAML 配置（推荐）**:
```yaml
# range6.yaml
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
```

**服务器配置**:
```yaml
server6:
    plugins:
        - classify: "classes.yaml"
        - range: "range6.yaml"
```

### 2. Link-Address 分类支持

Classify 插件新增 `link_address` 条件，可用于中继场景。

#### 配置示例

```yaml
# classes.yaml
classes:
  - name: "branch1"
    conditions:
      link_address: ["2001:db8:1::1"]
```

### 3. Remote ID 支持

新增 `remote_id` 条件用于识别中继代理。

```yaml
classes:
  - name: "provider-relay"
    conditions:
      remote_id: ["00000001"]
```

## 支持的分类条件

| 条件 | 说明 | 协议 |
|------|------|------|
| `link_address` | 中继代理地址 | DHCPv6 |
| `remote_id` | 中继代理 Remote ID | DHCPv6 |
| `duid_prefix` | DUID 前缀 | DHCPv6 |
| `duid_exact` | 精确 DUID | DHCPv6 |
| `mac_prefix` | MAC 前缀 | v4/v6 |
| `mac_exact` | 精确 MAC | v4/v6 |
| `vendor_class` | 精确厂商类别 | v4/v6 |
| `vendor_class_match` | 通配符厂商类别 | v4/v6 |
| `user_class` | 用户类别 | DHCPv6 |
| `arch_type` | 架构类型 | DHCPv6 |
| `interface_id` | 接口 ID | DHCPv6 |

## 使用场景

### 场景1：多分支机构

```
总部 CoreDHCP ←←← 中继 ←←← 分支1路由器 (2001:db8:1::1)
                              ←←← 分支2路由器 (2001:db8:2::1)
                              ←←← 分支3路由器 (2001:db8:3::1)
```

每个分支的客户端自动获得对应网段的 IP 地址。

### 场景2：设备类型 + 位置

```
分支1 + Android  → 2001:db8:1:200::/120
分支1 + IoT      → 2001:db8:1:300::/120
分支1 + 其他     → 2001:db8:1::/120
```

### 场景3：IPv4 类似配置

```yaml
# range4.yaml
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
```

## 文件清单

| 文件 | 说明 |
|------|------|
| `/plugins/classify/plugin.go` | 新增 link_address, remote_id 条件 |
| `/plugins/classify/handler6.go` | 提取中继消息的 link-address |
| `/plugins/range/plugin.go` | 支持多地址池配置 |
| `/plugins/range/handler6.go` | 基于分类选择地址池 |
| `/plugins/range/handler4.go` | IPv4 分类支持 |
| `/docs/link_address_example.md` | 详细文档 |
| `/docs/link_address_guide.md` | 快速参考 |

## 兼容性

- **向后兼容**: 旧的命令行格式仍然支持
  ```yaml
  plugins:
      - range: "leases6.sqlite3 2001:db8::1 2001:db8::1000 3600s"
  ```

- **渐进迁移**: 可以逐步从旧格式迁移到 YAML 配置

## 注意事项

1. **插件顺序**: `classify` 必须在 `range` 之前加载
2. **名称匹配**: classes.yaml 的 name 必须与 range6.yaml 的 class_ranges.name 一致
3. **地址池不重叠**: 确保各地址池之间没有 IP 重叠
4. **默认地址池**: 建议配置 default_range 用于未分类的客户端
