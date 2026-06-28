**GroupFlow 群聊系统分模块详细技术设计**。

定位保持不变：

```text
项目名：GroupFlow / 群流
目标：面向大群与高并发场景的实时群聊系统
范围：只关注群聊，不做单聊
核心：WebSocket、群消息、消息可靠性、大群广播、Redis 在线状态、Kafka 异步投递
```

------

# 1. 模块总览

整个系统可以拆成这些核心模块：

```text
1. 前端群聊模块
2. 接入层模块
3. WebSocket Gateway 模块
4. 用户与鉴权模块
5. 群服务模块
6. 群成员与权限模块
7. 消息服务模块
8. 消息投递模块
9. 历史消息模块
10. 已读未读模块
11. 群公告模块
12. 禁言与慢速模式模块
13. @提醒模块
14. 加群审批模块
15. 大群性能优化模块
16. Redis 缓存与在线状态模块
17. Kafka 消息队列模块
18. MySQL 持久化模块
19. 可靠性与幂等模块
20. 可观测性与压测模块
```

整体调用关系：

```text
前端
  ↓ HTTP / WebSocket
接入层 Nginx / Gateway
  ↓
HTTP API 服务 + WebSocket Gateway
  ↓
群服务 / 消息服务 / 投递服务
  ↓
Redis / MySQL / Kafka
```

------

# 2. 前端群聊模块设计

## 2.1 模块职责

前端负责群聊交互和实时消息展示。

核心职责：

```text
群列表展示
群聊窗口展示
消息发送
消息接收
消息状态展示
历史消息分页
未读数展示
@我提醒
群成员列表
群设置
群管理中心
断线重连提示
大群虚拟列表渲染
```

------

## 2.2 推荐技术

```text
React + TypeScript + Vite
Zustand 状态管理
Axios HTTP 请求
原生 WebSocket 封装
虚拟列表组件
```

如果你更熟 Vue，也可以用：

```text
Vue 3 + TypeScript + Vite + Pinia
```

但为了协议类型建模清晰，这里默认按 React + TS 讲。

------

## 2.3 前端 Store 拆分

```text
userStore
  当前用户信息、token

groupStore
  群列表、当前群详情、群设置

messageStore
  当前群消息列表、发送中消息、失败消息、未读信息

memberStore
  群成员分页数据、管理员列表、成员搜索结果

wsStore / connectionStore
  WebSocket 状态、重连次数、最后接收 sequence
```

------

## 2.4 前端消息对象设计

```ts
type MessageStatus = "sending" | "success" | "failed" | "recalled";

type GroupMessage = {
  messageId?: string;
  clientMessageId: string;
  groupId: number;
  senderId: number;
  senderName: string;
  messageType: "text" | "system" | "image" | "file";
  content: string;
  sequence?: number;
  status: MessageStatus;
  mentionAll?: boolean;
  mentionUserIds?: number[];
  createdAt?: string;
};
```

重点字段：

```text
clientMessageId：客户端生成，用于重试和去重
messageId：服务端生成，代表消息真正落库
sequence：群内递增序号，用于排序和补拉
status：前端展示状态
```

------

## 2.5 发送消息前端流程

```text
用户输入消息
  ↓
点击发送
  ↓
生成 clientMessageId
  ↓
本地插入一条 sending 消息
  ↓
通过 WebSocket 发送 group_message_send
  ↓
启动 ACK 超时定时器
  ↓
收到 ACK
  ↓
用服务端返回的 messageId、sequence 更新本地消息
  ↓
状态变成 success
```

ACK 超时：

```text
超过 5 秒未收到 ACK
  ↓
消息状态变成 failed
  ↓
用户可以点击重试
  ↓
重试仍使用相同 clientMessageId
```

------

## 2.6 前端大群优化

大群消息窗口必须做：

```text
虚拟列表渲染
历史消息游标分页
滚动位置保持
新消息悬浮提示
未读分割线
图片懒加载
消息本地缓存
长列表避免一次性 setState
```

不要这样做：

```text
一次性渲染几千条 DOM 消息
```

建议：

```text
只渲染当前可视区域附近的消息
```

------

# 3. 接入层模块设计

## 3.1 模块职责

接入层负责统一入口。

职责：

```text
HTTP 请求入口
WebSocket 请求入口
TLS 终止
负载均衡
限流
鉴权前置校验
路由转发
跨域处理
```

------

## 3.2 推荐技术

开发阶段：

```text
Nginx
```

后续可以演进：

```text
Kong
Traefik
Envoy
API Gateway
```

------

## 3.3 路由规则

```text
/api/*        -> HTTP API 服务
/ws           -> WebSocket Gateway
/metrics      -> Prometheus 指标
```

示例：

```text
https://groupflow.com/api/groups
wss://groupflow.com/ws?token=xxx
```

------

## 3.4 WebSocket 负载均衡注意点

WebSocket 是长连接，不能像普通 HTTP 一样频繁切换节点。

需要关注：

```text
长连接保持
连接超时
代理读写超时
负载均衡策略
连接数分布
```

Nginx 需要配置：

```text
proxy_http_version 1.1
proxy_set_header Upgrade $http_upgrade
proxy_set_header Connection "upgrade"
proxy_read_timeout 3600s
```

------

# 4. 用户与鉴权模块设计

## 4.1 模块职责

负责用户身份识别。

初期可以简单做：

```text
用户名登录
返回 token
WebSocket 连接携带 token
后端解析 token 得到 userId
```

------

## 4.2 登录接口

```text
POST /api/auth/login
```

请求：

```json
{
  "username": "user_001"
}
```

响应：

```json
{
  "userId": 1001,
  "username": "user_001",
  "token": "mock-token-xxx"
}
```

------

## 4.3 WebSocket 鉴权流程

```text
客户端连接 ws://host/ws?token=xxx
  ↓
WebSocket Gateway 解析 token
  ↓
校验 token 是否有效
  ↓
获取 userId
  ↓
创建 connectionId
  ↓
注册连接
```

------

## 4.4 鉴权信息上下文

后端每个请求中都需要有：

```go
type AuthContext struct {
    UserID int64
    Token  string
}
```

所有群操作都基于 `UserID` 做权限校验。

------

# 5. WebSocket Gateway 模块设计

## 5.1 模块职责

WebSocket Gateway 是实时通信入口。

职责：

```text
连接建立
连接鉴权
连接注册
心跳保活
断线清理
接收客户端消息
转发消息到业务服务
向客户端推送消息
管理本机连接
维护 Redis 全局连接路由
```

------

## 5.2 核心结构设计

```go
type Connection struct {
    ConnID     string
    UserID     int64
    ServerID   string
    DeviceID   string
    SendChan   chan []byte
    CreatedAt  time.Time
    LastPingAt time.Time
}
type Hub struct {
    ServerID    string
    Connections map[string]*Connection
    UserConns   map[int64]map[string]*Connection
}
```

说明：

```text
Connections：connectionId -> Connection
UserConns：userId -> 多个连接
```

多端登录时，一个用户可以有多个连接。

------

## 5.3 连接注册流程

```text
用户建立 WebSocket 连接
  ↓
鉴权成功
  ↓
生成 connectionId
  ↓
写入本机 Hub
  ↓
写入 Redis 在线状态
  ↓
启动读协程
  ↓
启动写协程
  ↓
启动心跳检测
```

Redis 记录：

```text
online:user:{userId} -> serverId
online:user:{userId}:connections -> connectionId set
server:{serverId}:users -> userId set
```

------

## 5.4 心跳机制

客户端定时发送：

```json
{
  "type": "ping",
  "timestamp": 1710000000000
}
```

服务端返回：

```json
{
  "type": "pong",
  "timestamp": 1710000000100
}
```

推荐规则：

```text
客户端每 20 秒 ping 一次
服务端 60 秒没收到心跳，主动断开
断开后清理本机连接和 Redis 在线状态
```

------

## 5.5 WebSocket 消息分发

统一协议：

```json
{
  "type": "group_message_send",
  "requestId": "req_001",
  "timestamp": 1710000000000,
  "data": {}
}
```

Gateway 根据 `type` 分发：

```text
group_message_send -> Message Service
group_message_read -> Read Service
ping -> heartbeat handler
```

------

# 6. 群服务模块设计

## 6.1 模块职责

群服务负责群的生命周期。

职责：

```text
创建群
修改群信息
解散群
查询群详情
群列表查询
群公告配置
大群模式配置
入群方式配置
慢速模式配置
```

------

## 6.2 群核心状态

```text
normal：正常
muted：全员禁言
dismissed：已解散
banned：系统封禁
archived：归档
```

------

## 6.3 群类型

```text
normal：普通群
large：大群
```

大群判断条件：

```text
成员数超过 500
在线人数超过 300
消息 QPS 超过阈值
管理员手动开启大群模式
```

------

## 6.4 群表设计

```sql
CREATE TABLE chat_group (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    name VARCHAR(64) NOT NULL,
    avatar VARCHAR(255),
    description VARCHAR(255),
    owner_id BIGINT NOT NULL,
    group_type VARCHAR(32) NOT NULL DEFAULT 'normal',
    join_mode VARCHAR(32) NOT NULL DEFAULT 'approval',
    mute_all TINYINT NOT NULL DEFAULT 0,
    slow_mode_seconds INT NOT NULL DEFAULT 0,
    status VARCHAR(32) NOT NULL DEFAULT 'normal',
    max_member_count INT NOT NULL DEFAULT 500,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    INDEX idx_owner_id(owner_id),
    INDEX idx_status(status)
);
```

------

## 6.5 创建群流程

```text
用户提交群名称、简介、入群方式
  ↓
校验参数
  ↓
创建 chat_group
  ↓
创建 group_member，角色 owner
  ↓
生成系统消息：创建群聊
  ↓
返回群详情
```

注意：

```text
创建群和创建群主成员关系需要放在同一个事务里
```

------

# 7. 群成员与权限模块设计

## 7.1 模块职责

负责群成员关系和权限判断。

职责：

```text
加入群
退出群
踢出成员
设置管理员
取消管理员
查询成员列表
搜索成员
成员禁言
成员状态校验
角色权限判断
```

------

## 7.2 成员表设计

```sql
CREATE TABLE group_member (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    group_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    role VARCHAR(32) NOT NULL DEFAULT 'member',
    nickname VARCHAR(64),
    status VARCHAR(32) NOT NULL DEFAULT 'normal',
    last_read_sequence BIGINT NOT NULL DEFAULT 0,
    mute_until DATETIME NULL,
    joined_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE KEY uk_group_user(group_id, user_id),
    INDEX idx_user_id(user_id),
    INDEX idx_group_role(group_id, role),
    INDEX idx_group_status(group_id, status)
);
```

------

## 7.3 成员状态

```text
normal：正常
exited：已退出
kicked：已踢出
muted：禁言中，也可以通过 mute_until 判断
```

------

## 7.4 权限模型

```go
type GroupRole string

const (
    RoleOwner  GroupRole = "owner"
    RoleAdmin  GroupRole = "admin"
    RoleMember GroupRole = "member"
)
```

权限判断示例：

```text
解散群：owner
设置管理员：owner
踢人：owner/admin
禁言：owner/admin
发消息：owner/admin/member，但需要检查禁言状态
@所有人：owner/admin
```

------

## 7.5 踢人流程

```text
管理员点击踢出成员
  ↓
服务端校验操作者是否 owner/admin
  ↓
校验目标用户是否在群内
  ↓
校验不能踢群主
  ↓
更新 group_member.status = kicked
  ↓
生成系统消息
  ↓
如果目标用户在线，推送被踢通知
```

------

## 7.6 大群成员列表设计

大群不能一次性加载所有成员。

接口：

```text
GET /api/groups/{groupId}/members?cursor=xxx&limit=50
```

支持筛选：

```text
role=admin
status=normal
keyword=张三
```

不要使用深分页：

```text
page=10000&pageSize=50
```

建议使用：

```text
id > cursor
limit 50
```

------

# 8. 消息服务模块设计

## 8.1 模块职责

消息服务负责群消息的核心处理。

职责：

```text
接收发送请求
校验发送权限
校验群状态
校验成员状态
校验禁言
校验慢速模式
生成 messageId
生成 group sequence
消息幂等
消息落库
返回 ACK
写入 Kafka
消息撤回
```

------

## 8.2 消息表设计

```sql
CREATE TABLE group_message (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    message_id VARCHAR(64) NOT NULL,
    group_id BIGINT NOT NULL,
    sender_id BIGINT NOT NULL,
    client_message_id VARCHAR(128) NOT NULL,
    message_type VARCHAR(32) NOT NULL,
    content TEXT,
    sequence BIGINT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'normal',
    mention_all TINYINT NOT NULL DEFAULT 0,
    mention_user_ids JSON,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE KEY uk_message_id(message_id),
    UNIQUE KEY uk_sender_client_msg(sender_id, client_message_id),
    UNIQUE KEY uk_group_sequence(group_id, sequence),
    INDEX idx_group_sequence(group_id, sequence),
    INDEX idx_group_created(group_id, created_at)
);
```

------

## 8.3 消息发送校验顺序

建议顺序：

```text
1. 校验 token，获取 userId
2. 校验群是否存在
3. 校验群状态是否 normal
4. 校验用户是否是群成员
5. 校验成员状态是否 normal
6. 校验全员禁言
7. 校验单人禁言
8. 校验慢速模式
9. 校验消息内容长度
10. 校验 clientMessageId 幂等
```

------

## 8.4 发送消息流程

```text
WebSocket Gateway 收到 group_message_send
  ↓
调用 Message Service
  ↓
执行权限和状态校验
  ↓
检查 senderId + clientMessageId 是否已存在
  ↓
如果已存在，直接返回原消息 ACK
  ↓
Redis INCR 生成 sequence
  ↓
生成 messageId
  ↓
写入 group_message
  ↓
返回 ACK 给发送者
  ↓
写入 Kafka group-message-topic
```

------

## 8.5 为什么先落库再 ACK？

因为 ACK 的语义是：

```text
服务端已经成功接收并持久化该消息
```

如果先 ACK 再落库，落库失败会导致客户端以为成功，但历史消息查不到。

推荐顺序：

```text
校验 -> sequence -> 落库 -> ACK -> Kafka 投递
```

------

## 8.6 消息类型

初期：

```text
text：文本消息
system：系统消息
```

后续：

```text
image：图片消息
file：文件消息
quote：引用消息
```

------

# 9. 消息投递模块设计

## 9.1 模块职责

投递模块负责把已落库的消息推送给在线群成员。

职责：

```text
消费 Kafka 消息
查询群成员
查询在线状态
过滤在线用户
按 WebSocket 节点分组
批量投递给对应 WS 节点
处理失败重试
记录投递延迟
```

------

## 9.2 为什么需要投递服务？

如果消息服务直接广播：

```text
消息发送请求会被广播耗时拖慢
大群会导致请求线程阻塞
用户发消息延迟不稳定
```

投递服务异步化后：

```text
消息服务只负责校验、落库、ACK
投递服务负责慢慢广播
Kafka 负责削峰
```

------

## 9.3 Kafka 消息结构

```json
{
  "eventId": "evt_100001",
  "eventType": "group_message_created",
  "groupId": 10001,
  "messageId": "msg_100001",
  "sequence": 100201,
  "senderId": 1001,
  "messageType": "text",
  "createdAt": "2026-06-28 10:00:00"
}
```

投递服务收到后，可以根据 `messageId` 查询完整消息，也可以在事件里携带完整消息内容。

------

## 9.4 投递流程

```text
Delivery Service 消费 Kafka
  ↓
根据 groupId 查询群成员
  ↓
查询 Redis 在线状态
  ↓
得到在线成员 userId 列表
  ↓
按 serverId 分组
  ↓
生成推送任务
  ↓
调用对应 WebSocket Gateway 内部推送接口
  ↓
WebSocket Gateway 推给本机连接
```

------

## 9.5 大群分片投递

示例：

```text
群 10001 有 100000 人
在线 12000 人
分布在 6 个 WS 节点
```

分组后：

```text
WS-1: 2300 人
WS-2: 1800 人
WS-3: 2100 人
WS-4: 1500 人
WS-5: 2500 人
WS-6: 1800 人
```

投递服务不直接推 12000 次，而是生成 6 个批量任务。

------

## 9.6 推送失败处理

推送失败场景：

```text
用户连接已断开
WebSocket 节点不可用
Redis 在线状态过期
网络异常
```

处理策略：

```text
连接断开：清理在线状态
节点不可用：记录失败指标，后续用户靠补拉恢复
消息不单独为每个用户重试到成功
```

因为群消息已经落库，用户重新进入群后可以补拉。

------

# 10. 历史消息模块设计

## 10.1 模块职责

负责用户进入群聊时加载历史消息。

职责：

```text
加载最近消息
向上滚动加载更早消息
断线后补拉遗漏消息
按 sequence 查询消息
过滤不可见消息
处理撤回消息展示
```

------

## 10.2 查询接口

加载最近消息：

```text
GET /api/groups/{groupId}/messages?limit=20
```

加载更早消息：

```text
GET /api/groups/{groupId}/messages?beforeSequence=100201&limit=20
```

补拉新消息：

```text
GET /api/groups/{groupId}/messages?afterSequence=100201&limit=100
```

------

## 10.3 游标分页 SQL

查询更早消息：

```sql
SELECT *
FROM group_message
WHERE group_id = ?
  AND sequence < ?
ORDER BY sequence DESC
LIMIT ?;
```

查询后前端需要按 sequence 升序展示。

------

## 10.4 索引要求

必须有：

```sql
INDEX idx_group_sequence(group_id, sequence)
```

这是历史消息查询的核心索引。

------

## 10.5 大群历史消息规则

大群中：

```text
不允许深分页
不允许一次性加载大量历史消息
每次最多 20 或 50 条
客户端滚动触发加载
```

------

# 11. 已读未读模块设计

## 11.1 模块职责

负责群会话未读数和读取位置。

职责：

```text
记录用户在群里的 lastReadSequence
计算未读数
上报已读位置
展示 @我提醒
进入群后清理未读
```

------

## 11.2 数据模型

直接复用 `group_member.last_read_sequence`：

```sql
last_read_sequence BIGINT NOT NULL DEFAULT 0
```

------

## 11.3 未读数计算

```text
unreadCount = groupMaxSequence - lastReadSequence
```

其中：

```text
groupMaxSequence 可以存在 Redis
lastReadSequence 存在 MySQL，也可以缓存 Redis
```

Redis Key：

```text
group:{groupId}:max_sequence
group:{groupId}:user:{userId}:last_read_sequence
```

------

## 11.4 已读上报接口

```text
POST /api/groups/{groupId}/read
```

请求：

```json
{
  "lastReadSequence": 100201
}
```

处理：

```text
校验用户是群成员
校验 lastReadSequence 不能小于旧值
更新 group_member.last_read_sequence
更新 Redis 缓存
```

注意：

```text
lastReadSequence 只能变大，不能回退
```

------

## 11.5 单条消息已读

普通群可以支持：

```text
已读 12 人，未读 3 人
```

计算方式：

```sql
SELECT COUNT(*)
FROM group_member
WHERE group_id = ?
  AND status = 'normal'
  AND last_read_sequence >= ?;
```

大群不建议展示完整已读名单。

------

# 12. 群公告模块设计

## 12.1 模块职责

负责群公告发布和展示。

职责：

```text
创建公告
编辑公告
置顶公告
查看公告
公告已读
公告系统消息
```

------

## 12.2 表设计

```sql
CREATE TABLE group_announcement (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    group_id BIGINT NOT NULL,
    creator_id BIGINT NOT NULL,
    title VARCHAR(128),
    content TEXT NOT NULL,
    pinned TINYINT NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    INDEX idx_group_id(group_id)
);
```

------

## 12.3 发布公告流程

```text
管理员发布公告
  ↓
校验权限
  ↓
写入 group_announcement
  ↓
更新群公告缓存
  ↓
生成系统消息
  ↓
推送给在线成员
```

------

## 12.4 大群公告优化

大群公告读多写少，适合缓存：

```text
group:{groupId}:announcement:latest
```

避免每次进入群都查数据库。

------

# 13. 禁言与慢速模式模块设计

## 13.1 模块职责

负责控制群发言能力。

职责：

```text
全员禁言
单人禁言
解除禁言
慢速模式
发送频率限制
@所有人限频
```

------

## 13.2 全员禁言

字段：

```sql
chat_group.mute_all
```

发送消息时校验：

```text
如果 mute_all = true
  owner/admin 可以发
  member 不能发
```

------

## 13.3 单人禁言

字段：

```sql
group_member.mute_until
```

发送消息时校验：

```text
如果 mute_until != null 且 mute_until > 当前时间
  拒绝发送
```

------

## 13.4 慢速模式

字段：

```sql
chat_group.slow_mode_seconds
```

规则：

```text
普通成员每 N 秒只能发送 1 条消息
管理员和群主可豁免，或使用更宽松限制
```

Redis Key：

```text
rate_limit:group:{groupId}:user:{userId}
```

发送时：

```text
检查 key 是否存在
  存在：拒绝，提示发送太频繁
  不存在：设置 key，过期时间为 slow_mode_seconds
```

------

## 13.5 @所有人限频

Redis Key：

```text
rate_limit:group:{groupId}:mention_all
rate_limit:user:{userId}:mention_all
```

规则示例：

```text
单个管理员每小时最多 3 次
单个群每小时最多 10 次
```

------

# 14. @提醒模块设计

## 14.1 模块职责

负责 @我、@所有人的提醒。

职责：

```text
解析 mentionUserIds
处理 mentionAll
群列表展示 @我
群列表展示 @所有人
点击跳转到对应消息
```

------

## 14.2 消息字段

```sql
mention_all TINYINT NOT NULL DEFAULT 0,
mention_user_ids JSON
```

------

## 14.3 @某人流程

```text
用户选择 @成员
  ↓
前端发送 mentionUserIds
  ↓
服务端校验被 @ 的用户是否在群内
  ↓
消息落库
  ↓
投递服务推送消息
  ↓
被 @ 用户群列表展示“有人@我”
```

------

## 14.4 @提醒存储

可以增加一张提醒表：

```sql
CREATE TABLE group_mention (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    group_id BIGINT NOT NULL,
    message_id VARCHAR(64) NOT NULL,
    user_id BIGINT NOT NULL,
    sequence BIGINT NOT NULL,
    read_status TINYINT NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    INDEX idx_user_group(user_id, group_id),
    INDEX idx_message_id(message_id)
);
```

初期也可以不单独建表，只在群会话缓存中记录。

如果要做“@我列表”，建议建表。

------

# 15. 加群审批模块设计

## 15.1 模块职责

负责用户申请加入群。

职责：

```text
提交加群申请
管理员审批
拒绝申请
重复申请限制
审批通知
审批通过后入群
```

------

## 15.2 表设计

```sql
CREATE TABLE group_join_request (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    group_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    reason VARCHAR(255),
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    reviewer_id BIGINT NULL,
    review_time DATETIME NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    INDEX idx_group_status(group_id, status),
    INDEX idx_user_id(user_id)
);
```

------

## 15.3 申请流程

```text
用户提交申请
  ↓
校验群是否存在
  ↓
校验用户是否已经是成员
  ↓
校验是否已有 pending 申请
  ↓
写入 group_join_request
  ↓
通知群主和管理员
```

------

## 15.4 审批流程

```text
管理员点击同意
  ↓
校验管理员权限
  ↓
更新申请状态 approved
  ↓
写入 group_member
  ↓
生成系统消息
  ↓
通知申请人
```

审批和入群需要事务。

------

# 16. Redis 缓存与在线状态模块设计

## 16.1 模块职责

Redis 负责高频状态。

职责：

```text
在线状态
连接路由
群 sequence
群最大 sequence
群配置缓存
群成员缓存
限流
慢速模式
```

------

## 16.2 关键 Key 设计

```text
online:user:{userId}
  value: serverId

online:user:{userId}:connections
  type: set
  value: connectionId

server:{serverId}:users
  type: set
  value: userId

group:{groupId}:sequence
  type: string
  value: 当前 sequence

group:{groupId}:max_sequence
  type: string
  value: 最大消息 sequence

group:{groupId}:config
  type: hash/json
  value: 群配置

rate_limit:group:{groupId}:user:{userId}
  type: string
  value: 1
  ttl: slow_mode_seconds
```

------

## 16.3 在线状态清理

连接断开：

```text
删除 connectionId
如果用户无其他连接
  删除 online:user:{userId}
  从 server:{serverId}:users 移除 userId
```

异常断开：

```text
依靠心跳超时清理
Redis key 设置 TTL 防止脏数据长期存在
```

------

## 16.4 Redis 使用注意点

不要把所有群成员都长期塞 Redis，尤其是超大群。

建议：

```text
普通群可以缓存成员列表
大群只缓存热点成员、配置和统计
```

------

# 17. Kafka 消息队列模块设计

## 17.1 模块职责

Kafka 负责异步事件流。

职责：

```text
群消息投递
系统消息投递
@提醒事件
后续搜索索引构建
削峰填谷
投递解耦
```

------

## 17.2 Topic 设计

```text
group-message-topic
  群普通消息投递

group-system-event-topic
  群系统事件

group-mention-topic
  @提醒事件

group-audit-topic
  审计事件，可后续加
```

------

## 17.3 分区 Key

建议使用：

```text
groupId
```

原因：

```text
同一个群的消息进入同一个分区，有利于保持消费顺序
```

注意：

```text
如果某个群特别热，会导致单分区热点
```

后续可针对超级热点群单独拆策略。

------

## 17.4 生产消息时机

推荐：

```text
消息落库成功后，再发送 Kafka
```

问题：

```text
如果落库成功，但 Kafka 发送失败，会导致实时投递缺失
```

改进方案：

```text
本地消息事件表 outbox
定时任务扫描未投递事件
重试发送 Kafka
```

初期可以先简单实现，后续再加 outbox。

------

# 18. MySQL 持久化模块设计

## 18.1 模块职责

MySQL 存储核心业务数据。

核心表：

```text
chat_group
group_member
group_message
group_announcement
group_join_request
group_mention
group_mute_record
```

------

## 18.2 消息表分表预留

初期一张表可以。

但设计上要预留：

```text
按 group_id 分表
按时间分表
按 message_id 分库
```

推荐初期逻辑仍然使用 repository 层封装，避免业务层直接写 SQL。

------

## 18.3 消息表索引重点

必须有：

```sql
UNIQUE KEY uk_message_id(message_id)
UNIQUE KEY uk_sender_client_msg(sender_id, client_message_id)
UNIQUE KEY uk_group_sequence(group_id, sequence)
INDEX idx_group_sequence(group_id, sequence)
```

这些分别支撑：

```text
message_id 查消息
clientMessageId 幂等
群消息顺序唯一
历史消息分页
```

------

## 18.4 事务边界

需要事务的场景：

```text
创建群 + 创建群主成员关系
审批通过 + 写入成员关系
解散群 + 更新群状态
消息落库 + 系统消息生成
踢人 + 系统消息生成
```

------

# 19. 可靠性与幂等模块设计

## 19.1 模块职责

保证消息不乱、不重、不丢。

核心机制：

```text
clientMessageId
server ACK
group sequence
消息落库
断线补拉
幂等约束
```

------

## 19.2 clientMessageId 幂等

客户端生成：

```text
clientMessageId = uuid
```

服务端唯一约束：

```sql
UNIQUE KEY uk_sender_client_msg(sender_id, client_message_id)
```

重试时：

```text
如果已存在
  直接返回原 messageId 和 sequence
```

------

## 19.3 group sequence 顺序

Redis 生成：

```text
INCR group:{groupId}:sequence
```

消息顺序使用：

```text
groupId + sequence
```

不用 created_at 排序。

------

## 19.4 断线补拉

客户端维护：

```text
lastReceivedSequence
```

重连后：

```text
GET /api/groups/{groupId}/messages?afterSequence=lastReceivedSequence
```

------

## 19.5 sequence 缺口检测

如果客户端收到：

```text
1001, 1002, 1005
```

说明缺：

```text
1003, 1004
```

客户端主动补拉：

```text
GET /api/groups/{groupId}/messages?afterSequence=1002&limit=100
```

------

# 20. 大群性能优化模块设计

## 20.1 模块职责

保障万人以上大群可用。

优化方向：

```text
消息广播异步化
在线用户分片推送
群成员分页
历史消息游标分页
未读数简化
已读详情限制
@所有人限频
慢速模式
虚拟列表
热点群监控
```

------

## 20.2 大群产品限制转技术规则

| 产品规则           | 技术实现                    |
| ------------------ | --------------------------- |
| 不展示完整成员列表 | 游标分页查询                |
| 不展示完整已读名单 | 只维护 lastReadSequence     |
| 离线用户不实时投递 | 消息落库，用户上线补拉      |
| @所有人限频        | Redis 限流                  |
| 慢速模式           | Redis TTL 限制              |
| 历史消息分页       | groupId + sequence 游标查询 |
| 在线人数展示       | Redis 统计或定时聚合        |

------

## 20.3 大群广播核心设计

```text
消息服务不广播
消息服务只落库、ACK、写 Kafka
投递服务消费 Kafka
投递服务查在线成员
按 serverId 分组
WebSocket 节点只推自己本机连接
```

------

## 20.4 热点群保护

热点群判断：

```text
消息 QPS 高
在线人数高
广播 fanout 高
Kafka lag 高
推送延迟高
```

保护手段：

```text
自动开启慢速模式
限制 @所有人
降低已读统计精度
延迟非核心事件
只保留最近消息实时推送
```

------

# 21. 可观测性模块设计

## 21.1 模块职责

用于观察系统运行情况。

需要覆盖：

```text
连接数
消息发送 QPS
消息 ACK 延迟
消息落库耗时
Kafka 投递延迟
WebSocket 推送成功率
Redis 延迟
MySQL 慢查询
大群 fanout 数量
```

------

## 21.2 指标设计

WebSocket：

```text
ws_online_connections
ws_connect_total
ws_disconnect_total
ws_push_total
ws_push_failed_total
ws_heartbeat_timeout_total
```

消息：

```text
group_message_send_total
group_message_send_failed_total
group_message_ack_latency_ms
group_message_persist_latency_ms
group_message_delivery_latency_ms
```

大群：

```text
large_group_message_qps
large_group_online_users
large_group_fanout_total
rate_limit_rejected_total
slow_mode_enabled_total
```

Kafka：

```text
kafka_produce_total
kafka_consume_total
kafka_consume_lag
kafka_produce_latency_ms
```

------

## 21.3 日志字段

结构化日志建议字段：

```json
{
  "level": "info",
  "traceId": "trace_xxx",
  "userId": 1001,
  "groupId": 10001,
  "messageId": "msg_100001",
  "clientMessageId": "client_msg_001",
  "sequence": 100201,
  "event": "group_message_send",
  "durationMs": 23,
  "timestamp": "2026-06-28T10:00:00Z"
}
```

符合你的日志习惯，代码里打印时可以用：

```go
logger.Infof("send group message success, groupId:%d, userId:%d, sequence:%d", groupID, userID, sequence)
```

不要用字符串拼接。

------

# 22. 压测模块设计

## 22.1 压测目标

验证：

```text
单机 WebSocket 最大连接数
群消息发送 QPS
大群广播延迟
Redis QPS
MySQL 写入能力
Kafka 消费延迟
前端长列表渲染性能
```

------

## 22.2 压测场景

### 场景一：连接压测

```text
1 万连接
5 万连接
10 万连接
```

观察：

```text
CPU
内存
连接稳定性
心跳超时数量
```

------

### 场景二：小群消息压测

```text
100 个群
每群 100 人
每秒 1000 条消息
```

观察：

```text
ACK 延迟
消息落库耗时
推送成功率
```

------

### 场景三：大群广播压测

```text
1 个大群
10 万成员
1 万在线
每秒 100 条消息
```

观察：

```text
Kafka lag
投递延迟
WebSocket 推送耗时
Redis 查询耗时
```

------

# 23. 推荐开发顺序

## 阶段一：核心群聊闭环

```text
用户登录
创建群
加入群
群列表
群详情
群成员分页
WebSocket 连接
发送文本消息
消息 ACK
消息落库
历史消息分页
```

------

## 阶段二：群管理完整能力

```text
群主 / 管理员 / 普通成员
踢人
退群
解散群
禁言
群公告
加群审批
系统消息
```

------

## 阶段三：消息可靠性

```text
clientMessageId 幂等
ACK 超时重试
断线重连
消息补拉
sequence 缺口检测
未读数
lastReadSequence
```

------

## 阶段四：大群能力

```text
Kafka
投递服务
WebSocket 多节点
Redis 连接路由
大群分片广播
慢速模式
限流
大群压测
```

------

# 24. 最终模块依赖关系

```text
用户鉴权模块
  ↓
WebSocket Gateway
  ↓
消息服务
  ↓
Redis sequence + MySQL group_message + Kafka
  ↓
投递服务
  ↓
WebSocket Gateway 推送在线用户
群服务
  ↓
群成员与权限模块
  ↓
消息服务发送前校验
群成员模块
  ↓
已读未读模块
  ↓
群列表未读数展示
Redis 在线状态模块
  ↓
投递服务
  ↓
大群分片广播
```

------

# 25. 总结

GroupFlow 的技术设计核心可以压缩成一句话：

```text
群消息只存一份，通过 groupId + sequence 保证顺序；
客户端通过 clientMessageId 和 ACK 保证发送可靠；
用户通过 lastReadSequence 维护未读位置；
在线用户通过 WebSocket 实时推送；
离线用户通过历史消息补拉；
大群通过 Kafka 异步投递、Redis 在线路由、WebSocket 节点分片推送来支撑高并发。
```

最关键的模块是这几个：

```text
WebSocket Gateway：负责长连接
Message Service：负责消息校验、落库、ACK
Delivery Service：负责大群异步广播
Redis：负责在线状态、sequence、限流
MySQL：负责群、成员、消息持久化
Kafka：负责削峰和异步投递
```

这套拆法后续可以继续往下拆成：

```text
1. 数据库详细设计
2. WebSocket 协议详细设计
3. 消息发送链路详细设计
4. 大群投递详细设计
5. Redis Key 设计
6. Kafka Topic 设计
7. 前后端接口文档
8. 开发任务拆解
```