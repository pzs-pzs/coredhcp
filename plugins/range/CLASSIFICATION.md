# Range Plugin - Class-Based Address Allocation

## 概述

Range 插件现在支持基于客户端分类的动态地址分配。不同类别的客户端可以从不同的 IP 地址池获取地址。

## 配置格式

### 1. 客户端分类配置 (`classes.yaml`)

```yaml
classes:
  # Android 设备
  - name: "android"
    conditions:
      vendor_class_match:
        - "android*"

  # iOS 设备
  - name: "ios"
    conditions:
      vendor_class_match:
        - "apple*"
        - "iOS*"

  # IoT 设备
  - name: "iot"
    conditions:
      mac_prefix:
        - "B8:27:EB"  # Raspberry Pi
        - "DC:4F:22"  # ESP32
        - "84:CC:A8"  # ESP32

  # 虚拟机
  - name: "virtual"
    conditions:
      mac_prefix:
        - "00:0C:29"  # VMware
        - "08:00:27"  # VirtualBox
        - "52:54:00"  # QEMU

  # 分支机构1的设备
  - name: "branch1"
    conditions:
      mac_prefix:
        - "00:11:22"  # 该分支机构的设备OUI

  # 分支机构2的设备
  - name: "branch2"
    conditions:
      mac_prefix:
        - "33:44:55"  # 该分支机构的设备OUI
```

### 2. Range 地址池配置 (`range6.yaml`)

```yaml
# 数据库文件
database: "leases6.sqlite3"

# 租约时间
lease_time: "3600s"

# 默认地址池（未分类的客户端）
default_range:
  start: "2001:db8::100"
  end: "2001:db8::1ff"

# 按分类分配地址池
class_ranges:
  - name: "android"
    range:
      start: "2001:db8:1000::100"
      end: "2001:db8:1000::1ff"

  - name: "ios"
    range:
      start: "2001:db8:2000::100"
      end: "2001:db8:2000::1ff"

  - name: "iot"
    range:
      start: "2001:db8:3000::100"
      end: "2001:db8:3000::1ff"

  - name: "virtual"
    range:
      start: "2001:db8:4000::100"
      end: "2001:db8:4000::1ff"

  - name: "branch1"
    range:
      start: "2001:db8:1:100::"
      end: "2001:db8:1:100::ffff"

  - name: "branch2"
    range:
      start: "2001:db8:2:100::"
      end: "2001:db8:2:100::ffff"
```

### 3. CoreDHCP 服务器配置

```yaml
# config.yaml
server6:
    listen: "[::]:547"
    plugins:
        # 必须先加载 classify 插件
        - server_id: LL 00:de:ad:be:ef:00
        - classify: "/etc/coredhcp/classes.yaml"
        # 然后加载 range 插件（使用 YAML 配置）
        - range: "/etc/coredhcp/range6.yaml"
        - dns: 2001:4860:4860::8888
```

## 使用示例

### 示例1：按设备类型分配

```yaml
# classes.yaml
classes:
  - name: "mobile"
    conditions:
      vendor_class_match:
        - "android*"
        - "apple*"

  - name: "desktop"
    conditions:
      vendor_class_match:
        - "windows*"

# range6.yaml
database: "leases6.sqlite3"
lease_time: "7200s"
default_range:
  start: "2001:db8::100"
  end: "2001:db8::fff"
class_ranges:
  - name: "mobile"
    range:
      start: "2001:db8:1000::100"
      end: "2001:db8:1000::fff"
  - name: "desktop"
    range:
      start: "2001:db8:2000::100"
      end: "2001:db8:2000::fff"
```

**效果：**
- Android/iOS 设备 → `2001:db8:1000::/120` 网段
- Windows 设备 → `2001:db8:2000::/120` 网段
- 其他设备 → `2001:db8::/120` 默认网段

### 示例2：多分支机构部署

```yaml
# classes.yaml
classes:
  - name: "beijing"
    conditions:
      mac_prefix:
        - "00:11:22"  # 北京办公室设备

  - name: "shanghai"
    conditions:
      mac_prefix:
        - "33:44:55"  # 上海办公室设备

  - name: "guangzhou"
    conditions:
      mac_prefix:
        - "66:77:88"  # 广州办公室设备

# range6.yaml
database: "leases6.sqlite3"
lease_time: "3600s"
default_range:
  start: "2001:db8::100"
  end: "2001:db8::1ff"
class_ranges:
  - name: "beijing"
    range:
      start: "2001:db8:1::100"
      end: "2001:db8:1::ffff"

  - name: "shanghai"
    range:
      start: "2001:db8:2::100"
      end: "2001:db8:2::ffff"

  - name: "guangzhou"
    range:
      start: "2001:db8:3::100"
      end: "2001:db8:3::ffff"
```

**效果：**
- 北京设备 → `2001:db8:1::/64` 网段
- 上海设备 → `2001:db8:2::/64` 网段
- 广州设备 → `2001:db8:3::/64` 网段

### 示例3：DHCPv4 配置

```yaml
# config.yaml
server4:
    listen: "0.0.0.0:67"
    plugins:
        - classify: "/etc/coredhcp/classes4.yaml"
        - range: "/etc/coredhcp/range4.yaml"
        - dns: 8.8.8.8

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

  - name: "servers"
    range:
      start: "192.168.20.100"
      end: "192.168.20.200"
```

## 向后兼容

旧的命令行格式仍然支持：

```yaml
# 旧格式（单个地址池）
server6:
    plugins:
        - range: "leases6.sqlite3 2001:db8::1 2001:db8::1000 3600s"
```

## 工作流程

1. **客户端发起 DHCP 请求**
   ```
   Client → Router → CoreDHCP (Solicit)
   ```

2. **Classify 插件识别客户端**
   ```
   提取: DUID, MAC, Vendor Class
   匹配: classes.yaml 中的规则
   存储: 分类结果到内存
   ```

3. **Range 插件分配地址**
   ```
   检查: 客户端分类
   选择: 对应的地址池分配器
   分配: 从指定池中分配 IP
   ```

4. **返回响应**
   ```
   CoreDHCP → Router → Client (Reply with assigned IP)
   ```

## 注意事项

1. **插件顺序**：`classify` 必须在 `range` 之前加载
2. **分类名称匹配**：`classes.yaml` 中的 name 必须与 `range6.yaml` 中的 name 完全一致
3. **默认地址池**：未匹配任何分类的客户端使用 `default_range`
4. **地址池不重叠**：确保各个地址池之间没有重叠

## 故障排查

### 客户端未获得预期地址

```bash
# 启用调试日志
coredhcp -config config.yaml -log-level debug

# 检查日志中的以下信息：
# - Client classified as 'xxx'        # 分类结果
# - Using class-specific allocator    # 使用的分配器
# - found IPv6 address ...            # 分配的地址
```

### 所有客户端都获得默认地址池

- 检查 `classify` 插件是否正确加载
- 检查分类规则是否正确匹配
- 检查 class_ranges 中的 name 是否与 classes.yaml 中的 name 一致

### 地址池耗尽

```bash
# 查看数据库中的租约
sqlite3 leases6.sqlite3 "SELECT * FROM leases6;"

# 扩大地址池范围
```
