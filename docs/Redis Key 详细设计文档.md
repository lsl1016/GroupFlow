# Redis Key 详细设计文档

## 1. 文档说明

### 1.1 文档目的

本文档用于说明 GroupFlow 群聊系统中的 Redis Key 设计，包括 Key 命名规范、数据结构、使用场景、读写时机、TTL 设置、清理策略、一致性处理和大群场景下的注意事项。

GroupFlow 是一个面向大群与高并发场景设计的实时群聊系统。Redis 在系统中主要承担高频状态、缓存、限流、在线状态和连接路由能力。

### 1.2 Redis 在系统中的定位

Redis 不是最终数据源。

Redis 主要用于：

1. WebSocket 在线状态。
2. WebSocket 连接路由。
3. 群消息 sequence。
4. 群最大消息 sequence。
5. 群配置缓存。
6. 群在线成员集合。
7. 按 WebSocket 节点维护群在线用户。
8. 慢速模式限流。
9. @所有人限频。
10. 群会话未读数辅助计算。
11. 热点群状态标记。
12. 投递过程中的临时状态。

最终数据仍以 MySQL 为准。

------

## 2. Redis 设计原则

### 2.1 Key 命名清晰

Redis Key 应该能直接表达业务含义。

推荐格式：

```text
业务域:{核心ID}:子对象:{子ID}
```

示例：

```text
group:{groupId}:sequence
online:user:{userId}
connection:{connectionId}:server
rate_limit:group:{groupId}:user:{userId}
```

### 2.2 避免无边界大 Key

大群系统中必须避免单个 Redis Key 无限膨胀。

重点关注：

```text
group:{groupId}:online_users
group:{groupId}:server:{serverId}:users
```

这些集合在大群中可能包含大量成员，需要结合 TTL、分片、压测和清理策略。

### 2.3 高频数据进 Redis

适合放 Redis 的数据：

1. 频繁读写。
2. 可从 MySQL 或连接状态恢复。
3. 对实时性要求高。
4. 对强一致要求不高。
5. 数据丢失后可以重建。

不适合放 Redis 的数据：

1. 群消息正文。
2. 群成员最终关系。
3. 审批记录。
4. 操作审计日志。
5. 消息撤回记录。

### 2.4 Redis 数据允许短暂不一致

Redis 中的在线状态、连接路由、群在线成员集合可能出现短暂不一致。

例如：

1. 用户异常断线，但 Redis 路由还存在。
2. WebSocket 节点宕机，部分连接 Key 未及时删除。
3. 群在线成员集合存在脏 userId。

系统应通过 TTL、心跳续期、异常清理和历史消息补拉来兜底。

### 2.5 Redis 不能替代 MySQL

Redis 中的 sequence、群配置、lastReadSequence 等数据，必要时都应该可以从 MySQL 恢复。

------

## 3. Key 分类总览

| 分类                | 作用                              |
| ------------------- | --------------------------------- |
| 在线状态 Key        | 判断用户是否在线                  |
| 连接路由 Key        | 判断用户连接在哪个 WebSocket 节点 |
| 群 sequence Key     | 生成群消息递增序号                |
| 群最大 sequence Key | 辅助计算未读数                    |
| 群配置缓存 Key      | 缓存群配置，减少 MySQL 查询       |
| 群成员缓存 Key      | 缓存普通群成员列表                |
| 群在线成员 Key      | 大群投递时筛选在线用户            |
| 节点在线用户 Key    | 统计 WS 节点在线用户              |
| 慢速模式 Key        | 控制用户发言频率                  |
| @所有人限频 Key     | 控制 @所有人频率                  |
| 热点群 Key          | 标识热点群和降级状态              |
| 投递临时 Key        | 支持投递过程中的临时状态          |
| 幂等辅助 Key        | 支持短期请求幂等或防重复提交      |

------

# 4. 在线状态 Key

## 4.1 用户在线状态

### Key

```text
online:user:{userId}
```

### 类型

```text
String
```

### Value

```text
serverId
```

### 示例

```text
online:user:1001 = ws-server-01
```

### 用途

用于快速判断用户当前是否在线，以及用户连接在哪个 WebSocket 节点。

### 写入时机

WebSocket 连接建立成功后写入。

```text
用户建立 WS 连接
  ↓
鉴权成功
  ↓
生成 connectionId
  ↓
写入 online:user:{userId}
```

### 删除时机

1. WebSocket 正常断开。
2. 心跳超时。
3. 服务端主动踢下线。
4. 连接被关闭。

### TTL

推荐：

```text
90 秒
```

### 续期策略

客户端每 20 秒发送心跳。

服务端收到心跳后续期：

```text
EXPIRE online:user:{userId} 90
```

### 注意事项

如果用户允许多端登录，单个 `online:user:{userId}` 不够准确，因为一个用户可能同时连接多个 WebSocket 节点。

多端场景应使用 connection 级别路由。

------

## 4.2 用户多端连接集合

### Key

```text
online:user:{userId}:connections
```

### 类型

```text
Set
```

### Value

```text
connectionId
```

### 示例

```text
online:user:1001:connections = {conn_001, conn_002}
```

### 用途

记录一个用户当前所有在线连接。

适用于：

1. Web 端和移动端同时在线。
2. 多浏览器标签页同时在线。
3. 同一个账号多设备登录。

### 写入时机

WebSocket 连接建立成功后：

```text
SADD online:user:{userId}:connections {connectionId}
```

### 删除时机

WebSocket 连接断开后：

```text
SREM online:user:{userId}:connections {connectionId}
```

### TTL

推荐：

```text
90 秒
```

### 续期策略

心跳续期：

```text
EXPIRE online:user:{userId}:connections 90
```

### 注意事项

Set 里的单个 connectionId 也需要有独立 Key 维护它对应的 serverId。

------

## 4.3 连接所属用户

### Key

```text
connection:{connectionId}:user
```

### 类型

```text
String
```

### Value

```text
userId
```

### 示例

```text
connection:conn_001:user = 1001
```

### 用途

通过 connectionId 反查 userId。

适用于：

1. 连接断开时清理用户连接集合。
2. 服务端排查连接归属。
3. WebSocket 节点重启前做连接清理。

### TTL

推荐：

```text
90 秒
```

------

## 4.4 连接所属 WebSocket 节点

### Key

```text
connection:{connectionId}:server
```

### 类型

```text
String
```

### Value

```text
serverId
```

### 示例

```text
connection:conn_001:server = ws-server-01
```

### 用途

用于 Delivery Service 判断某个连接应该推送到哪个 WebSocket 节点。

### 写入时机

连接建立成功后写入。

### 删除时机

连接关闭时删除。

### TTL

推荐：

```text
90 秒
```

------

# 5. WebSocket 节点状态 Key

## 5.1 节点在线用户集合

### Key

```text
server:{serverId}:users
```

### 类型

```text
Set
```

### Value

```text
userId
```

### 示例

```text
server:ws-server-01:users = {1001, 1002, 1003}
```

### 用途

记录某个 WebSocket 节点当前承载的在线用户。

用于：

1. 节点在线人数统计。
2. 节点负载观察。
3. 节点故障时清理路由。
4. 大群按节点分片投递。

### 写入时机

连接建立后：

```text
SADD server:{serverId}:users {userId}
```

### 删除时机

连接断开后：

```text
SREM server:{serverId}:users {userId}
```

### TTL

可以不设置 TTL，但推荐设置并定期续期：

```text
120 秒
```

原因：

如果节点宕机，可能无法主动删除该 Set。

------

## 5.2 节点心跳 Key

### Key

```text
server:{serverId}:heartbeat
```

### 类型

```text
String
```

### Value

```text
timestamp
```

### 示例

```text
server:ws-server-01:heartbeat = 1710000000000
```

### 用途

用于判断 WebSocket 节点是否存活。

### 写入时机

WebSocket Gateway 每隔固定时间写入。

推荐：

```text
每 10 秒写入一次
```

### TTL

推荐：

```text
30 秒
```

### 用途说明

如果某个节点 heartbeat Key 过期，可以认为该节点异常。

后续可以触发：

1. 清理该节点的用户路由。
2. 将该节点从可投递节点列表中移除。
3. 告警通知。

------

## 5.3 活跃 WebSocket 节点集合

### Key

```text
servers:ws:active
```

### 类型

```text
Set
```

### Value

```text
serverId
```

### 示例

```text
servers:ws:active = {ws-server-01, ws-server-02, ws-server-03}
```

### 用途

记录当前活跃的 WebSocket Gateway 节点。

### 写入时机

节点启动时：

```text
SADD servers:ws:active {serverId}
```

### 删除时机

节点优雅下线时：

```text
SREM servers:ws:active {serverId}
```

### 异常清理

如果节点未优雅下线，依赖 `server:{serverId}:heartbeat` 判断是否存活。

------

# 6. 群消息 sequence Key

## 6.1 群消息递增序号

### Key

```text
group:{groupId}:sequence
```

### 类型

```text
String / Counter
```

### Value

```text
当前群最新 sequence
```

### 示例

```text
group:10001:sequence = 100201
```

### 用途

用于生成群内递增消息序号。

每次发送群消息时执行：

```text
INCR group:{groupId}:sequence
```

### 写入时机

群消息发送时。

### 初始化

如果 Key 不存在，从 MySQL 查询最大 sequence：

```sql
SELECT MAX(sequence)
FROM group_message
WHERE group_id = ?;
```

然后设置：

```text
SET group:{groupId}:sequence {maxSequence}
```

再执行：

```text
INCR group:{groupId}:sequence
```

### TTL

不建议设置 TTL。

原因：

1. sequence 是群消息顺序核心状态。
2. Key 丢失后虽然可以从 MySQL 恢复，但恢复会增加复杂度。
3. 长期存在更稳定。

### 注意事项

允许 sequence 跳号。

例如：

```text
INCR 成功，但 MySQL 写入失败。
```

这会导致某个 sequence 没有对应消息。

客户端通过补拉确认后可以接受跳号。

------

## 6.2 群最大消息序号

### Key

```text
group:{groupId}:max_sequence
```

### 类型

```text
String
```

### Value

```text
当前群最大已落库消息 sequence
```

### 示例

```text
group:10001:max_sequence = 100201
```

### 用途

用于辅助计算群未读数。

```text
unreadCount = groupMaxSequence - lastReadSequence
```

### 写入时机

消息成功写入 MySQL 后更新。

推荐：

```text
SET group:{groupId}:max_sequence {sequence}
```

### TTL

不建议设置 TTL。

### 与 group:{groupId}:sequence 的区别

| Key                          | 含义                                              |
| ---------------------------- | ------------------------------------------------- |
| group:{groupId}:sequence     | Redis INCR 得到的最新序号，可能因为写库失败而跳号 |
| group:{groupId}:max_sequence | 已成功落库的最大消息序号                          |

### 注意事项

如果消息写库失败，不应更新 `max_sequence`。

------

# 7. 群配置缓存 Key

## 7.1 群配置缓存

### Key

```text
group:{groupId}:config
```

### 类型

```text
Hash 或 String JSON
```

### 推荐

初期推荐使用 String JSON，开发简单。

### Value 示例

```json
{
  "groupId": 10001,
  "name": "GroupFlow 技术交流群",
  "groupType": "large",
  "status": "normal",
  "muteAll": false,
  "slowModeSeconds": 5,
  "joinMode": "approval",
  "mentionAllRole": "admin",
  "memberCount": 12000,
  "updatedAt": "2026-06-28T10:00:00.000Z"
}
```

### 用途

用于减少发送消息、投递消息、查询群设置时对 MySQL 的访问。

常用场景：

1. 发送消息时校验群状态。
2. 发送消息时校验全员禁言。
3. 发送消息时读取慢速模式配置。
4. 投递时判断群类型。
5. 客户端进入群聊时快速读取群配置。

### 写入时机

1. 查询群详情时懒加载。
2. 创建群后写入。
3. 修改群配置后更新。
4. 开启或关闭禁言后更新。
5. 开启或关闭慢速模式后更新。

### 删除时机

群信息修改后可以直接删除缓存：

```text
DEL group:{groupId}:config
```

下次读取时重新加载。

### TTL

推荐：

```text
10 分钟 - 30 分钟
```

如果配置更新频率低，也可以设置更长。

### 一致性策略

推荐使用：

```text
先更新 MySQL，再删除 Redis 缓存
```

原因：

1. MySQL 是最终数据源。
2. 删除缓存后，下次读取会重新加载。
3. 比直接更新缓存更简单。

------

# 8. 群成员缓存 Key

## 8.1 普通群成员列表缓存

### Key

```text
group:{groupId}:members
```

### 类型

```text
Set
```

### Value

```text
userId
```

### 示例

```text
group:10001:members = {1001, 1002, 1003}
```

### 用途

普通群场景下，快速获取群成员列表，用于消息投递和权限判断辅助。

### 适用范围

适合普通群和中型群。

不建议对超大群长期缓存完整成员集合。

### TTL

推荐：

```text
5 分钟 - 30 分钟
```

### 更新时机

1. 用户加入群。
2. 用户退出群。
3. 用户被踢出。
4. 群被解散。

### 注意事项

群成员关系最终以 MySQL `group_member` 为准。

------

## 8.2 群管理员集合

### Key

```text
group:{groupId}:admins
```

### 类型

```text
Set
```

### Value

```text
userId
```

### 示例

```text
group:10001:admins = {1001, 1002}
```

### 用途

快速判断某用户是否是管理员。

适用场景：

1. 踢人权限校验。
2. 禁言权限校验。
3. 发布公告权限校验。
4. @所有人权限校验。

### TTL

推荐：

```text
10 分钟 - 30 分钟
```

### 更新时机

1. 设置管理员。
2. 取消管理员。
3. 群主变更。
4. 管理员退出群。

------

# 9. 群在线成员 Key

> **在线判定原则（重要 / 当前实现）**
>
> GroupFlow 的「在线」是**用户登录态的全局维度**，不是群维度：
>
> - **在线 = 用户已建立 WebSocket 连接（登录）**，与用户是否进入某个群无关。
> - 在线态只用用户/连接维度的 key 表达：`online:user:{userId}:connections`、
>   `connection:{connectionId}:server` 等（**均不含 groupId**）；心跳续期的也是这些
>   登录态 key。
> - 投递时不预先维护「每个群的在线集合」，而是枚举群成员后，按用户登录态过滤出在线者。
>
> 因此本章 `group:{groupId}:online_users` / `group:{groupId}:online_user:{userId}` /
> `group:{groupId}:online_servers` / `group:{groupId}:server:{serverId}:users` 这类
> **per-group（群维度）在线集合，当前不采用、也未实现**。它们作为历史设计方案保留在
> 文档中，但与「在线 = 登录态全局维度」的设计理念冲突，不应再据此实现。
>
> 若未来确需降低大群投递的成员枚举压力，推荐方向是「全局在线用户集合 ∩ 群成员」，
> 而不是为每个群维护一份在线集合。

## 9.1 群在线用户集合（历史方案 / 未采用）

### Key

```text
group:{groupId}:online_users
```

### 类型

```text
Set
```

### Value

```text
userId
```

### 示例

```text
group:10001:online_users = {1001, 1002, 1003}
```

### 用途

用于大群投递时快速获取当前在线群成员。

适合大群异步投递。

### 写入时机

有两种策略。

#### 策略一：用户连接成功后加入其所有群

```text
用户 WS 连接成功
  ↓
查询用户加入的群
  ↓
对每个群执行 SADD group:{groupId}:online_users {userId}
```

优点：

1. 投递时准确。
2. 用户在线后所有群都可以直接投递。

缺点：

1. 用户加入群很多时，连接成本高。
2. 登录瞬间 Redis 写入量大。

#### 策略二：用户进入某个群聊页面时加入

```text
用户进入群聊页面
  ↓
SADD group:{groupId}:online_users {userId}
```

优点：

1. 写入量小。
2. 更符合当前活跃群聊场景。

缺点：

1. 用户在线但没打开群，不会实时收到该群消息。
2. 更像“活跃在线成员”而不是“真实在线成员”。

### 推荐

一期可以不维护该 Key。

二期开始维护：

```text
group:{groupId}:online_users
```

但需要明确它表示的是：

```text
当前在线且可接收该群消息的用户
```

### 删除时机

1. 用户断开连接。
2. 用户退出群聊页面。
3. 用户退出群。
4. 用户被踢出群。
5. TTL 过期。

### TTL

Set 本身不容易对成员设置 TTL。

建议配合用户维度活跃 Key：

```text
group:{groupId}:online_user:{userId}
```

------

## 9.2 群用户在线状态 Key

### Key

```text
group:{groupId}:online_user:{userId}
```

### 类型

```text
String
```

### Value

```text
1
```

### 示例

```text
group:10001:online_user:1001 = 1
```

### 用途

为群在线用户提供单用户 TTL。

### TTL

推荐：

```text
90 秒
```

### 写入时机

1. 用户进入群聊页面。
2. 用户心跳续期。
3. 用户保持该群活跃时续期。

### 与 group:{groupId}:online_users 的关系

`group:{groupId}:online_users` 是集合，方便遍历。

`group:{groupId}:online_user:{userId}` 是状态 Key，方便 TTL 过期。

投递时可以：

1. 从集合取用户。
2. 批量检查这些用户的在线状态 Key。
3. 过滤已过期用户。
4. 异步清理集合中的脏 userId。

------

## 9.3 按 WebSocket 节点维护群在线用户

### Key 1：群在线节点集合

```text
group:{groupId}:online_servers
```

### 类型

```text
Set
```

### Value

```text
serverId
```

### 示例

```text
group:10001:online_servers = {ws-server-01, ws-server-02}
```

### Key 2：群在某节点上的在线用户集合

```text
group:{groupId}:server:{serverId}:users
```

### 类型

```text
Set
```

### Value

```text
userId
```

### 示例

```text
group:10001:server:ws-server-01:users = {1001, 1002, 1003}
```

### 用途

用于大群高并发投递时直接生成按 WebSocket 节点分组的投递任务。

投递时：

```text
SMEMBERS group:{groupId}:online_servers
  ↓
分别读取 group:{groupId}:server:{serverId}:users
  ↓
直接生成 PushTask
```

### 优点

1. 投递时天然按 serverId 分组。
2. 减少 RouteResolver 查询。
3. 减少大量 MGET 在线路由。
4. 更适合大群分片推送。

### 缺点

1. 维护复杂。
2. 用户连接迁移时需要更新多个 Key。
3. 节点异常时需要清理。
4. 多端连接需要更细粒度处理。

### 使用阶段

建议三期再实现。

一期和二期可以先预留接口。

------

# 10. 慢速模式与限流 Key

## 10.1 群内用户发送限流

### Key

```text
rate_limit:group:{groupId}:user:{userId}
```

### 类型

```text
String
```

### Value

```text
1
```

### 用途

控制普通成员在慢速模式下的发送频率。

### 写入方式

发送消息成功前检查：

```text
SET rate_limit:group:{groupId}:user:{userId} 1 EX {slowModeSeconds} NX
```

如果设置成功，允许发送。

如果设置失败，说明用户仍在限流窗口内，拒绝发送。

### TTL

等于群配置中的：

```text
slowModeSeconds
```

### 示例

如果慢速模式为 5 秒：

```text
SET rate_limit:group:10001:user:1001 1 EX 5 NX
```

### 注意事项

1. 群主和管理员可以豁免。
2. 普通成员必须校验。
3. 限流校验应在 Message Service 中处理。
4. Delivery Service 不负责慢速模式。

------

## 10.2 用户全局发送限流

### Key

```text
rate_limit:user:{userId}:send_message
```

### 类型

```text
String 或 Counter
```

### 用途

防止单个用户在多个群里高频发送消息。

### 示例规则

```text
每个用户每秒最多发送 5 条消息
```

### 实现方式

简单窗口：

```text
INCR rate_limit:user:{userId}:send_message
EXPIRE 1
```

如果计数超过阈值，拒绝发送。

### TTL

```text
1 秒
```

------

## 10.3 群消息 QPS 限流

### Key

```text
rate_limit:group:{groupId}:message_qps
```

### 类型

```text
Counter
```

### 用途

统计并控制单群消息 QPS。

### 实现方式

```text
INCR rate_limit:group:{groupId}:message_qps
EXPIRE 1
```

### 示例规则

```text
大群每秒最多 100 条消息
```

超过阈值时可以：

1. 拒绝普通成员消息。
2. 开启慢速模式。
3. 只允许管理员发送。
4. 标记为热点群。

------

# 11. @所有人限频 Key

## 11.1 单群 @所有人限频

### Key

```text
rate_limit:group:{groupId}:mention_all
```

### 类型

```text
String（SetNX 标记）
```

> 代码现状：当前实现使用 `SET ... NX` 做窗口标记，而非 Counter 计数。
> TTL 普通群 60 秒、大群 300 秒，由 `Service.checkMentionAll` 写入。

### 用途

限制单个群在一个时间窗口内 @所有人的次数。

### 示例规则（文档原始目标）

```text
每个群每小时最多 10 次 @所有人
```

### 实现方式（当前代码）

```text
SET rate_limit:group:{groupId}:mention_all 1 EX {60 或 300} NX
```

设置失败（key 已存在）即判定为限频命中，返回 ErrRateLimited。

### 超限处理

返回错误：

```text
MENTION_ALL_RATE_LIMITED
```

------

## 11.2 用户 @所有人限频

### Key

```text
rate_limit:user:{userId}:mention_all
```

### 类型

```text
String（SetNX 标记）
```

> 代码现状：已实现。与群维度 `rate_limit:group:{groupId}:mention_all` 在
> `Service.checkMentionAll` 中一并校验，任一命中即返回 ErrRateLimited。
> TTL 普通群 60 秒、大群 300 秒。

### 用途

限制单个用户在多个群中频繁 @所有人。

### 示例规则（文档原始目标）

```text
每个管理员每小时最多 3 次 @所有人
```

### 实现方式（当前代码）

```text
SET rate_limit:user:{userId}:mention_all 1 EX {60 或 300} NX
```

------

# 12. 已读未读辅助 Key

## 12.1 用户群已读位置缓存

### Key

```text
group:{groupId}:user:{userId}:last_read_sequence
```

### 类型

```text
String
```

### Value

```text
lastReadSequence
```

### 示例

```text
group:10001:user:1001:last_read_sequence = 100201
```

### 用途

缓存用户在群内的最后已读 sequence。

MySQL 字段：

```text
group_member.last_read_sequence
```

### 写入时机

用户上报已读位置时：

1. 更新 MySQL。
2. 更新 Redis。

### TTL

推荐：

```text
1 天 - 7 天
```

### 一致性

MySQL 为准。

如果 Redis 不存在，从 MySQL 读取并回填。

------

## 12.2 用户群未读数缓存

### Key

```text
group:{groupId}:user:{userId}:unread_count
```

### 类型

```text
String
```

### 用途

缓存用户某个群的未读数。

### 是否推荐

不强烈推荐作为核心方案。

原因：

1. 每条消息都要更新大量用户未读数，会产生写放大。
2. 大群中不可接受。
3. 通过 `max_sequence - last_read_sequence` 可以按需计算。

### 推荐策略

只在群列表展示时按需计算。

大群场景展示：

```text
99+
```

而不是精确维护每个用户未读数。

------

# 13. 热点群状态 Key

## 13.1 热点群标记

### Key

```text
hot_group:{groupId}
```

### 类型

```text
String
```

### Value

```text
1
```

### 用途

标记某个群当前处于热点状态。

### 写入时机

当监控发现以下情况时写入：

1. 群消息 QPS 超过阈值。
2. 群 fanout 过高。
3. Kafka lag 持续增长。
4. Delivery 延迟过高。
5. WebSocket 推送失败率过高。

### TTL

推荐：

```text
5 分钟
```

热点状态应该自动过期。

------

## 13.2 热点群当前策略

### Key

```text
hot_group:{groupId}:strategy
```

### 类型

```text
String JSON
```

### 示例

```json
{
  "slowModeSeconds": 5,
  "disableMentionAll": true,
  "disableReadDetail": true,
  "deliveryPriority": "normal",
  "reason": "delivery_latency_high"
}
```

### 用途

让 Message Service、Delivery Service、前端都能读取当前热点群降级策略。

### TTL

推荐：

```text
5 分钟
```

------

# 14. 投递临时状态 Key

## 14.1 消息投递状态

### Key

```text
delivery:message:{messageId}:status
```

### 类型

```text
String JSON
```

### 示例

```json
{
  "groupId": 10001,
  "messageId": "msg_100000001",
  "fanoutCount": 12000,
  "successCount": 11880,
  "failedCount": 80,
  "notFoundCount": 40,
  "durationMs": 823,
  "status": "finished"
}
```

### 用途

调试和排查投递问题。

### 是否必须

不是必须。

一期可以只打日志和指标，不存 Redis。

### TTL

推荐：

```text
10 分钟 - 1 小时
```

------

## 14.2 投递任务锁

### Key

```text
lock:delivery:message:{messageId}
```

### 类型

```text
String
```

### 用途

防止同一条消息被多个 Delivery 实例重复处理。

### 是否推荐

如果 Kafka 消费语义已经处理好，一期可以不加。

如果后续有重试扫描任务，可以使用该锁。

### 写入方式

```text
SET lock:delivery:message:{messageId} {workerId} EX 30 NX
```

### TTL

推荐：

```text
30 秒
```

------

# 15. 幂等辅助 Key

## 15.1 发送请求短期幂等缓存

### Key

```text
idempotent:message:{senderId}:{clientMessageId}
```

### 类型

```text
String JSON
```

### Value 示例

```json
{
  "groupId": 10001,
  "messageId": "msg_100000001",
  "sequence": 100201,
  "createdAt": "2026-06-28T10:00:00.000Z"
}
```

### 用途

短期缓存发送结果，减少重复请求时查 MySQL。

### 是否必须

不是必须。

核心幂等必须依赖 MySQL 唯一索引：

```text
sender_id + client_message_id
```

Redis 幂等 Key 只能作为性能优化。

### TTL

推荐：

```text
10 分钟 - 1 小时
```

------

# 16. Key TTL 总览

| Key                                              | TTL 建议        | 原因                       |
| ------------------------------------------------ | --------------- | -------------------------- |
| online:user:{userId}                             | 90 秒           | 心跳续期，异常断开自动过期 |
| online:user:{userId}:connections                 | 90 秒           | 多端连接集合自动过期       |
| connection:{connectionId}:user                   | 90 秒           | 连接异常时自动过期         |
| connection:{connectionId}:server                 | 90 秒           | 连接异常时自动过期         |
| server:{serverId}:users                          | 120 秒          | 节点异常时自动过期         |
| server:{serverId}:heartbeat                      | 30 秒           | 节点存活检测               |
| servers:ws:active                                | 不固定          | 结合 heartbeat 判断        |
| group:{groupId}:sequence                         | 不设置          | 群消息顺序核心状态         |
| group:{groupId}:max_sequence                     | 不设置          | 未读数核心辅助状态         |
| group:{groupId}:config                           | 10 - 30 分钟    | 群配置缓存                 |
| group:{groupId}:members                          | 5 - 30 分钟     | 普通群成员缓存             |
| group:{groupId}:admins                           | 10 - 30 分钟    | 权限校验缓存               |
| group:{groupId}:online_users                     | 可选            | Set 成员无独立 TTL         |
| group:{groupId}:online_user:{userId}             | 90 秒           | 群内在线状态自动过期       |
| rate_limit:group:{groupId}:user:{userId}         | slowModeSeconds | 慢速模式窗口               |
| rate_limit:user:{userId}:send_message            | 1 秒            | 用户发送 QPS 限流          |
| rate_limit:group:{groupId}:message_qps           | 1 秒            | 群消息 QPS 限流            |
| rate_limit:group:{groupId}:mention_all           | 3600 秒         | 群 @所有人限频             |
| rate_limit:user:{userId}:mention_all             | 3600 秒         | 用户 @所有人限频           |
| group:{groupId}:user:{userId}:last_read_sequence | 1 - 7 天        | 已读位置缓存               |
| hot_group:{groupId}                              | 5 分钟          | 热点状态自动恢复           |
| hot_group:{groupId}:strategy                     | 5 分钟          | 降级策略自动恢复           |
| delivery:message:{messageId}:status              | 10 - 60 分钟    | 排查临时数据               |
| lock:delivery:message:{messageId}                | 30 秒           | 防止重复处理               |
| idempotent:message:{senderId}:{clientMessageId}  | 10 - 60 分钟    | 短期幂等缓存               |

------

# 17. Redis 读写流程

## 17.1 WebSocket 连接建立

```text
用户建立 WebSocket 连接
  ↓
鉴权成功
  ↓
生成 connectionId
  ↓
写入 connection:{connectionId}:user
  ↓
写入 connection:{connectionId}:server
  ↓
SADD online:user:{userId}:connections {connectionId}
  ↓
SADD server:{serverId}:users {userId}
  ↓
写入 online:user:{userId}
```

------

## 17.2 WebSocket 心跳续期

```text
客户端发送 ping
  ↓
服务端返回 pong
  ↓
续期 online:user:{userId}
  ↓
续期 online:user:{userId}:connections
  ↓
续期 connection:{connectionId}:user
  ↓
续期 connection:{connectionId}:server
```

------

## 17.3 WebSocket 连接断开

```text
连接断开
  ↓
删除 connection:{connectionId}:user
  ↓
删除 connection:{connectionId}:server
  ↓
SREM online:user:{userId}:connections {connectionId}
  ↓
如果用户没有其他连接
      删除 online:user:{userId}
      SREM server:{serverId}:users {userId}
```

多端场景注意：

```text
只有当用户所有 connection 都断开时，才认为用户离线。
```

------

## 17.4 发送群消息

```text
收到 group_message_send
  ↓
读取 group:{groupId}:config
  ↓
校验群状态、禁言、慢速模式
  ↓
执行 rate_limit 校验
  ↓
INCR group:{groupId}:sequence
  ↓
写入 MySQL group_message
  ↓
SET group:{groupId}:max_sequence {sequence}
  ↓
发布 Kafka 事件
```

------

## 17.5 大群投递

一期：

```text
Delivery Service 消费 Kafka
  ↓
查询 MySQL group_member
  ↓
批量 MGET online:user:{userId}
  ↓
按 serverId 分组
  ↓
推送到对应 WS 节点
```

二期：

```text
Delivery Service 消费 Kafka
  ↓
SMEMBERS group:{groupId}:online_users
  ↓
批量校验 group:{groupId}:online_user:{userId}
  ↓
查询连接路由
  ↓
按 serverId 分组
  ↓
推送
```

三期：

```text
Delivery Service 消费 Kafka
  ↓
SMEMBERS group:{groupId}:online_servers
  ↓
读取 group:{groupId}:server:{serverId}:users
  ↓
直接生成按 serverId 分组的 PushTask
  ↓
批量推送
```

------

# 18. Redis 与 MySQL 一致性

## 18.1 群配置一致性

写流程：

```text
更新 MySQL chat_group
  ↓
删除 group:{groupId}:config
```

读流程：

```text
读取 group:{groupId}:config
  ↓
如果命中，直接返回
  ↓
如果未命中，查询 MySQL
  ↓
写入 Redis
```

------

## 18.2 sequence 一致性

`group:{groupId}:sequence` 用于生成新 sequence。

`group:{groupId}:max_sequence` 表示已落库最大 sequence。

如果 Redis 丢失：

```text
从 MySQL group_message 查询 MAX(sequence)
```

恢复。

------

## 18.3 lastReadSequence 一致性

MySQL 字段：

```text
group_member.last_read_sequence
```

Redis 缓存：

```text
group:{groupId}:user:{userId}:last_read_sequence
```

写流程：

```text
更新 MySQL
  ↓
更新 Redis
```

读流程：

```text
优先读 Redis
  ↓
未命中读 MySQL
  ↓
回填 Redis
```

------

## 18.4 在线状态一致性

在线状态不以 MySQL 为准。

它是运行时状态。

一致性依赖：

1. WebSocket 连接建立写入。
2. WebSocket 连接断开删除。
3. 心跳续期。
4. TTL 过期。
5. 节点心跳清理。

------

# 19. 大群场景注意事项

## 19.1 避免超大 Set 全量读取

以下操作要谨慎：

```text
SMEMBERS group:{groupId}:online_users
```

如果在线人数非常大，会造成 Redis 和 Delivery Service 压力。

优化方式：

1. 使用 SSCAN 分批读取。
2. 按 serverId 拆分集合。
3. 控制 batchSize。
4. 热点群开启慢速模式。

------

## 19.2 避免每条消息大量 MGET

一期方案中：

```text
查询所有群成员
  ↓
MGET online:user:{userId}
```

在 10 万人大群中成本较高。

后续应演进到：

```text
group:{groupId}:online_users
```

或者：

```text
group:{groupId}:server:{serverId}:users
```

------

## 19.3 避免为每个用户维护精确未读数

不要每条消息都执行：

```text
INCR group:{groupId}:user:{userId}:unread_count
```

大群中会造成严重写放大。

推荐：

```text
unreadCount = groupMaxSequence - lastReadSequence
```

------

## 19.4 避免 @所有人展开成所有用户提醒

大群中不要为每个成员写一条提醒 Key。

推荐：

1. 消息里记录 `mentionAll = true`。
2. 前端根据消息展示 @所有人。
3. 群列表可展示 @所有人提醒。
4. 不同步展开所有成员。

------

# 20. Redis Key 清理策略

## 20.1 连接断开清理

正常断开时主动清理：

```text
DEL connection:{connectionId}:user
DEL connection:{connectionId}:server
SREM online:user:{userId}:connections {connectionId}
SREM server:{serverId}:users {userId}
```

如果用户没有其他连接：

```text
DEL online:user:{userId}
```

------

## 20.2 节点异常清理

通过节点心跳发现异常：

```text
server:{serverId}:heartbeat 过期
```

触发清理：

```text
SMEMBERS server:{serverId}:users
  ↓
逐个删除 online:user:{userId}
  ↓
删除 server:{serverId}:users
  ↓
SREM servers:ws:active {serverId}
```

多端场景需要谨慎：

如果用户还有其他 connection 在其他节点，不能直接删除用户所有在线状态。

------

## 20.3 群在线集合脏数据清理

如果使用：

```text
group:{groupId}:online_users
```

需要定期清理其中已过期的用户。

流程：

```text
SSCAN group:{groupId}:online_users
  ↓
批量检查 group:{groupId}:online_user:{userId}
  ↓
不存在则 SREM
```

------

# 21. Redis 命令使用建议

## 21.1 批量读取

大群投递中，应使用批量命令：

```text
MGET
Pipeline
SSCAN
```

避免大量单条 Redis 请求。

------

## 21.2 分批处理

对于大集合，不要一次性全量处理。

推荐：

```text
SSCAN cursor COUNT 1000
```

------

## 21.3 限流使用 SET NX EX

慢速模式推荐：

```text
SET rate_limit:group:{groupId}:user:{userId} 1 EX 5 NX
```

这比先 GET 再 SET 更安全。

------

## 21.4 计数器注意 EXPIRE

使用 `INCR` 做窗口计数时，需要确保首次设置过期时间。

伪代码：

```text
count = INCR key
if count == 1:
    EXPIRE key windowSeconds
if count > limit:
    reject
```

------

# 22. 一期实现范围

一期必须实现：

1. `online:user:{userId}`
2. `online:user:{userId}:connections`
3. `connection:{connectionId}:server`
4. `connection:{connectionId}:user`
5. `server:{serverId}:users`
6. `server:{serverId}:heartbeat`
7. `group:{groupId}:sequence`
8. `group:{groupId}:max_sequence`
9. `group:{groupId}:config`
10. `rate_limit:group:{groupId}:user:{userId}`
11. `rate_limit:user:{userId}:send_message`
12. `rate_limit:group:{groupId}:mention_all`
13. `rate_limit:user:{userId}:mention_all`

一期可以暂缓：

1. `group:{groupId}:online_users`
2. `group:{groupId}:online_user:{userId}`
3. `group:{groupId}:online_servers`
4. `group:{groupId}:server:{serverId}:users`
5. `hot_group:{groupId}`
6. `hot_group:{groupId}:strategy`
7. `delivery:message:{messageId}:status`

------

# 23. 二期演进

二期建议实现：

1. 群在线用户集合。
2. 群用户在线状态 TTL Key。
3. 热点群标记。
4. 热点群策略。
5. 投递状态临时 Key。
6. lastReadSequence 缓存。
7. 群管理员集合缓存。

重点目标：

```text
减少 Delivery Service 每条消息查询 MySQL group_member 的压力。
```

------

# 24. 三期演进

三期建议实现：

1. 按 serverId 维护群在线用户集合。
2. WebSocket 节点故障自动清理。
3. 热点群自动降级策略。
4. Redis 大 Key 监控。
5. 动态 batchSize。
6. Redis Pipeline 批量优化。
7. 超大群在线用户分片集合。

超大群可以考虑分片 Key：

```text
group:{groupId}:online_users:shard:{shardId}
```

例如：

```text
group:10001:online_users:shard:0
group:10001:online_users:shard:1
group:10001:online_users:shard:2
```

分片规则：

```text
shardId = userId % shardCount
```

------

# 25. 总结

GroupFlow Redis Key 设计的核心是：

1. Redis 负责高频状态，不负责最终持久化。
2. 在线状态使用 `online:user:{userId}` 和 connection 级 Key 维护。
3. 多端连接使用 `online:user:{userId}:connections`。
4. WebSocket 节点使用 `server:{serverId}:users` 和 heartbeat 维护。
5. 群消息顺序使用 `group:{groupId}:sequence`。
6. 群最大已落库序号使用 `group:{groupId}:max_sequence`。
7. 群配置使用 `group:{groupId}:config` 缓存。
8. 慢速模式使用 `rate_limit:group:{groupId}:user:{userId}`。
9. @所有人限频使用 group 和 user 两类限频 Key。
10. 大群投递一期可以查成员后批量查在线状态。
11. 二期可以维护 `group:{groupId}:online_users`。
12. 三期可以维护 `group:{groupId}:server:{serverId}:users`，让投递天然按 WebSocket 节点分片。
13. 大群中要避免大 Key、避免全量 SMEMBERS、避免每条消息给每个用户写未读数。
14. 所有运行时状态都要有 TTL、心跳续期和异常清理策略。
15. MySQL 是最终数据源，Redis 数据丢失后应能恢复或重新注册。

------

# 26. 代码实现对齐说明（与当前实现核对）

> 本节按当前后端代码实际实现核对上文设计，标注「已实现 / 已实现但与设计有差异 /
> 未实现」三种状态，作为文档与代码之间的权威对照。

## 26.1 已实现的 Key

| Key | 类型 | TTL（代码） | 写入位置 | 读取位置 |
| --- | --- | --- | --- | --- |
| `online:user:{userId}:connections` | Set | 90s | `ws.renewRedis`（连接/心跳续期）；`ws.unregister` SREM | `delivery.resolveOnlineRoutes` SMEMBERS；死节点清理 SREM |
| `online:user:{userId}` | String=serverId | 90s | `ws.renewRedis` | **当前代码只写不读**：路由实际走 connections 集合 + connection:server，该单值 key 仅作冗余/兼容保留 |
| `connection:{connectionId}:user` | String=userId | 90s | `ws.renewRedis`；`ws.unregister` DEL | 死节点清理 GET/DEL |
| `connection:{connectionId}:server` | String=serverId | 90s | `ws.renewRedis`；`ws.unregister` DEL | `delivery.resolveOnlineRoutes` GET |
| `server:{serverId}:connections` | Set | 90s | `ws.renewRedis` | `delivery.scanActiveServerIDs` SCAN；死节点清理 SMEMBERS/DEL |
| `server:{serverId}:push_url` | String=推送地址 | 90s | `ws.renewRedis` | `delivery.pushURL` GET |
| `server:{serverId}:heartbeat` | String=毫秒时间戳 | **30s** | `ws.RunHeartbeat`（每 10s 续期） | `delivery.isServerAlive` EXISTS（死节点判定） |
| `group:{groupId}:sequence` | Counter | 不设置 | `service.nextSequence`（Lua INCR / 冷启动 SET+INCR） | 同一处 INCR 返回 |
| `group:{groupId}:config` | String JSON | **10 分钟** | `service.cacheGroupConfig`；`UpdateSettings` 后 `invalidateGroupConfigCache` DEL | `service.getGroupConfig` GET（发消息热路径） |
| `rate_limit:group:{groupId}:user:{userId}` | String SetNX | = slowModeSeconds | `service.checkSlowMode` | SetNX 成败即结果 |
| `rate_limit:group:{groupId}:mention_all` | String SetNX | 60s / 大群 300s | `service.checkMentionAll` | SetNX 成败即结果 |
| `rate_limit:user:{userId}:mention_all` | String SetNX | 60s / 大群 300s | `service.checkMentionAll` | SetNX 成败即结果 |
| `rate_limit:user:{userId}:send_message` | Counter | **1 秒窗口** | `service.checkGlobalSendRateLimit`（INCR+EXPIRE） | INCR 计数 > 5 即限流 |

## 26.2 已实现但与原设计有差异

- `@所有人` 限频实现使用 `SET NX` 标记而非 `INCR` 计数器，窗口为 60s/300s
  （不是文档示例的“每小时 N 次”计数语义）。
- `rate_limit:user:{userId}:send_message` 为 1 秒窗口、阈值 5 条的简单计数，
  不是令牌桶。
- 群配置缓存 `group:{groupId}:config` 采用 String JSON + 10 分钟 TTL，
  写策略为“先更 MySQL 再删缓存”（与 §7 设计一致）。
- `server:{serverId}:heartbeat` 实现为 10s 续期 / 30s TTL（与 §5.2 设计一致）；
  死节点判定不依赖 `servers:ws:active`，而是 `SCAN server:*:connections` 动态枚举
  节点后按 heartbeat 是否存在判活。

## 26.3 未实现 / 已回退（设计保留，代码暂未落地）

- `group:{groupId}:online_users`、`group:{groupId}:online_user:{userId}`：
  **按设计理念不采用**（群维度在线集合，与「在线 = 用户登录态全局维度」冲突）。
  当前大群投递走「全量活跃成员枚举 + 用户登录态在线过滤」。
- `group:{groupId}:online_servers`、`group:{groupId}:server:{serverId}:users`：
  **不采用**（同为群维度方案）。
- `servers:ws:active`：未实现，活跃节点通过 `SCAN server:*:connections` 动态推导。
- `group:{groupId}:user:{userId}:last_read_sequence`：未实现 Redis 缓存，
  已读位置仅落 MySQL `group_member.last_read_sequence`；已读回退由 service 层
  `UpdateReadPosition` 校验并返回 `READ_SEQUENCE_ROLLBACK`。
- `group:{groupId}:user:{userId}:unread_count`：未实现（按设计建议按需计算）。
- `hot_group:{groupId}`、`hot_group:{groupId}:strategy`：未实现。
- `delivery:message:{messageId}:status`、`lock:delivery:message:{messageId}`、
  `idempotent:message:{senderId}:{clientMessageId}`：未实现（幂等由 MySQL 唯一索引 +
  事务 Outbox 保证；Delivery 失败靠短重试与未提交 offset 兜底）。