# DHCPv6 续约功能实现总结

## 实现概述

CoreDHCP Range 插件现已完整实现 DHCPv6 续约功能，符合 RFC 9915 标准。

## 支持的消息类型

| 消息类型 | 代码 | 处理状态 | 说明 |
|---------|------|---------|------|
| **SOLICIT** | 1 | ✅ | 新客户端分配地址 |
| **ADVERTISE** | 2 | - | 服务器响应（由其他插件处理） |
| **REQUEST** | 3 | ✅ | 确认地址分配 |
| **RENEW** | 5 | ✅ | 续租（向原始服务器） |
| **REBIND** | 6 | ✅ | 重绑定（向任意服务器） |
| **REPLY** | 7 | - | 服务器响应 |
| **RELEASE** | 8 | ✅ | 释放地址 |
| **DECLINE** | 9 | ✅ | 拒绝地址 |

## 代码结构

### Handler6 主函数

```go
func (p *PluginState) Handler6(req, resp dhcpv6.DHCPv6) (dhcpv6.DHCPv6, bool)
```

处理流程：
1. 解析内层消息
2. 验证 ClientID
3. 检查 IA_NA 选项
4. 区分消息类型
5. 调用相应处理函数

### 处理函数

#### 新客户端处理
```go
func (p *PluginState) handleNewClient6(...)
```
- 处理 SOLICIT、REQUEST
- 分配新地址
- 保存租约到数据库

#### 续约处理
```go
func (p *PluginState) handleRenewal6(...)
```
- 处理 RENEW、REBIND、REQUEST（已有租约）
- 验证请求的地址
- 延长租期（如需要）
- 添加状态码

#### 释放处理
```go
func (p *PluginState) handleRelease6(...)
```
- 处理 RELEASE、DECLINE
- 从数据库删除租约
- 释放分配器中的地址

## 续约逻辑

### 租期延长条件

```go
// 1. 租期即将过期（小于 LeaseTime）
if expiry.Before(now.Add(p.LeaseTime)) {
    extendLease = true
}
// 2. 租期剩余不足一半（推荐做法）
else if timeUntilExpiry < p.LeaseTime/2 {
    extendLease = true
}
```

### 地址验证

```go
// 检查客户端请求的地址是否与记录匹配
if addrs := iana.Options.Addresses(); len(addrs) > 0 {
    requestedAddr := addrs[0].IPv6Addr
    if !requestedAddr.Equal(record.IP) {
        log.Warnf("Client requested %s but has %s", 
            requestedAddr, record.IP)
    }
}
// 始终返回记录中的正确地址
```

### 消息类型区分

```go
switch msgType {
case dhcpv6.MessageTypeSolicit:
    log.Printf("New client (SOLICIT)")
case dhcpv6.MessageTypeRequest:
    log.Printf("New client (REQUEST)")
case dhcpv6.MessageTypeRenew:
    log.Printf("Renewal request (RENEW)")
    // 添加 Success 状态码
case dhcpv6.MessageTypeRebind:
    log.Printf("Rebind request (REBIND)")
    // 添加 Success 状态码
}
```

## 日志输出示例

### 新客户端
```
time="..." level=info msg="DUID 00010001... is new (SOLICIT), leasing new IPv6 address"
time="..." level=info msg="Allocated IPv6 address 2001:db8::10 to DUID 00010001..."
```

### 续约成功
```
time="..." level=debug msg="Received RENEW message from DUID 00010001..."
time="..." level=info msg="Extended lease for DUID 00010001...: 2001:db8::10 (msgType: RENEW, new expiry: 2026-03-22 12:32:51)"
```

### 地址不匹配警告
```
time="..." level=warning msg="Client 00010001... requested 2001:db8::20 but has lease for 2001:db8::10 (msgType: RENEW)"
```

## 测试覆盖

### TestHandler6Renew
测试 RENEW 消息处理：
- 模拟现有租约
- 发送 RENEW 请求
- 验证租期被延长

### TestHandler6RenewMismatchAddress
测试地址不匹配场景：
- 客户端请求的地址与记录不同
- 验证返回正确的地址

### TestHandler6Rebind
测试 REBIND 消息处理：
- 模拟即将过期的租约
- 发送 REBIND 请求
- 验证租期被延长

## RFC 9915 合规性

| 功能 | RFC 9915 要求 | 当前实现 |
|------|-------------|---------|
| 处理 RENEW | 必须支持 | ✅ |
| 处理 REBIND | 必须支持 | ✅ |
| 返回相同地址 | 除非地址无效 | ✅ |
| 延长有效 lifetime | 根据请求 | ✅ |
| T1/T2 处理 | 可选 | ❌ (使用服务器默认) |
| Rapid Commit | 可选 | ❌ |

## 配置示例

无需额外配置，续约功能自动工作：

```yaml
# config.yaml
server6:
    listen: "[::]:547"
    plugins:
        - server_id: LL 00:de:ad:be:ef:00
        - range: "/etc/coredhcp/range6.yaml"
```

```yaml
# range6.yaml
database: "leases6.sqlite3"
lease_time: "3600s"  # 续约时延长到此时间
default_range:
  start: "2001:db8::100"
  end: "2001:db8::1ff"
```

## 客户端行为

### 正常续约流程
```
1. 客户端获得地址，T1=1800s, T2=2880s, LeaseTime=3600s
2. T1 到达后，发送 RENEW 到原始服务器
3. 服务器延长租期
4. 客户端继续使用地址
```

### 服务器无响应（REBIND）
```
1. T2 到达，原始服务器无响应
2. 客户端发送 REBIND 到任意服务器
3. CoreDHCP 验证租约并延长
4. 客户端继续使用地址
```

## 与分类功能结合

续约功能与客户端分类完全兼容：

```
客户端分类 → 分配地址 → 定期续约
     ↓            ↓          ↓
  branch1     2001:db8:1::  延长租期
  branch2     2001:db8:2::  延长租期
```

## 关键文件

| 文件 | 说明 |
|------|------|
| `/plugins/range/handler6.go` | DHCPv6 处理逻辑 |
| `/plugins/range/plugin_test.go` | 续约功能测试 |
| `/docs/renewal_implementation.md` | 本文档 |
