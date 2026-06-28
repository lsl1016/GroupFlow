# HTTP API 接口详细设计文档

## 1. 文档说明

### 1.1 文档目的

本文档用于说明 GroupFlow 群聊系统中的 HTTP API 接口设计，包括认证、群管理、成员管理、历史消息、已读位置、消息撤回、群公告、加群审批、禁言、群设置和管理操作等接口。

GroupFlow 是一个面向大群与高并发场景设计的实时群聊系统，当前系统只关注群聊，不设计单聊。

### 1.2 HTTP API 与 WebSocket 的职责边界

GroupFlow 同时使用 HTTP API 和 WebSocket。

HTTP API 主要负责：

1. 用户登录。
2. 群列表查询。
3. 群创建、修改、解散。
4. 群成员分页查询。
5. 群成员管理。
6. 历史消息分页查询。
7. 断线消息补拉。
8. 已读位置上报。
9. 消息撤回。
10. 群公告管理。
11. 加群申请审批。
12. 禁言管理。
13. 群设置查询和修改。

WebSocket 主要负责：

1. 群消息实时发送。
2. 服务端 ACK。
3. 群消息实时接收。
4. 消息撤回事件推送。
5. 群事件推送。
6. 心跳保活。
7. 连接状态维护。

### 1.3 接口设计目标

HTTP API 设计需要满足以下目标：

1. 接口清晰，便于前后端联调。
2. 所有接口都能基于 token 鉴权。
3. 所有群操作都必须校验群成员身份和角色权限。
4. 历史消息使用 sequence 游标分页。
5. 群成员列表使用 id 游标分页。
6. 大群场景避免深分页和高成本查询。
7. 所有响应结构统一。
8. 错误码标准化。
9. 方便后续生成 OpenAPI 文档。
10. 方便后续拆分微服务。

------

## 2. 通用约定

## 2.1 基础路径

```text
/api/v1
```

示例：

```text
/api/v1/groups
/api/v1/groups/{groupId}/messages
```

------

## 2.2 请求头

所有需要登录的接口必须携带：

```http
Authorization: Bearer {token}
Content-Type: application/json
```

示例：

```http
Authorization: Bearer mock-token-xxx
Content-Type: application/json
```

------

## 2.3 统一响应结构

### 成功响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {},
  "requestId": "req_100001",
  "timestamp": 1710000000000
}
```

### 失败响应

```json
{
  "code": "GROUP_NOT_FOUND",
  "message": "群不存在",
  "data": null,
  "requestId": "req_100001",
  "timestamp": 1710000000000
}
```

### 字段说明

| 字段      | 类型   | 说明             |
| --------- | ------ | ---------------- |
| code      | string | 响应码           |
| message   | string | 响应描述         |
| data      | object | 响应数据         |
| requestId | string | 请求 ID          |
| timestamp | number | 服务端毫秒时间戳 |

------

## 2.4 分页规范

### 2.4.1 游标分页

大群场景禁止深分页，统一使用游标分页。

请求参数：

| 参数   | 类型   | 必填 | 说明     |
| ------ | ------ | ---- | -------- |
| cursor | string | 否   | 游标     |
| limit  | number | 否   | 每页数量 |

响应字段：

```json
{
  "items": [],
  "nextCursor": "10086",
  "hasMore": true
}
```

### 2.4.2 历史消息分页

历史消息使用 `sequence` 作为游标。

向上加载更早消息：

```text
beforeSequence=100201
```

断线补拉新消息：

```text
afterSequence=100201
```

### 2.4.3 成员分页

群成员分页使用 `memberId` 或 `group_member.id` 作为游标。

```text
cursor=100001
```

------

## 2.5 时间格式

HTTP API 响应中的时间统一使用 ISO 8601 格式。

示例：

```text
2026-06-28T10:00:00.000Z
```

------

## 2.6 ID 类型约定

| 类型            | 示例           | 说明          |
| --------------- | -------------- | ------------- |
| userId          | 1001           | 用户 ID       |
| groupId         | 10001          | 群 ID         |
| messageId       | msg_100000001  | 服务端消息 ID |
| clientMessageId | client_msg_001 | 客户端消息 ID |
| announcementId  | 9001           | 公告 ID       |
| requestId       | req_100001     | 请求 ID       |

------

# 3. 认证接口

## 3.1 用户登录

### 接口

```http
POST /api/v1/auth/login
```

### 说明

用户登录接口。初期可以使用用户名模拟登录，后续可扩展密码、验证码、OAuth 等认证方式。

### 请求体

```json
{
  "username": "user_001"
}
```

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "userId": 1001,
    "username": "user_001",
    "nickname": "张三",
    "avatar": "https://example.com/avatar.png",
    "token": "mock-token-xxx"
  },
  "requestId": "req_100001",
  "timestamp": 1710000000000
}
```

### 错误码

| 错误码         | 说明       |
| -------------- | ---------- |
| USER_NOT_FOUND | 用户不存在 |
| USER_BANNED    | 用户被封禁 |
| AUTH_FAILED    | 登录失败   |

------

## 3.2 获取当前用户信息

### 接口

```http
GET /api/v1/auth/me
```

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "userId": 1001,
    "username": "user_001",
    "nickname": "张三",
    "avatar": "https://example.com/avatar.png",
    "status": "normal"
  },
  "requestId": "req_100002",
  "timestamp": 1710000000000
}
```

------

# 4. 群管理接口

## 4.1 创建群

### 接口

```http
POST /api/v1/groups
```

### 说明

创建一个新群。创建人自动成为群主。

### 请求体

```json
{
  "name": "GroupFlow 技术交流群",
  "description": "讨论大群、高并发、WebSocket 和消息投递设计",
  "avatar": "https://example.com/group-avatar.png",
  "joinMode": "approval",
  "maxMemberCount": 500
}
```

### 请求字段

| 字段           | 类型   | 必填 | 说明       |
| -------------- | ------ | ---- | ---------- |
| name           | string | 是   | 群名称     |
| description    | string | 否   | 群简介     |
| avatar         | string | 否   | 群头像     |
| joinMode       | string | 否   | 入群方式   |
| maxMemberCount | number | 否   | 最大成员数 |

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "groupId": 10001,
    "name": "GroupFlow 技术交流群",
    "ownerId": 1001,
    "groupType": "normal",
    "joinMode": "approval",
    "status": "normal",
    "memberCount": 1,
    "createdAt": "2026-06-28T10:00:00.000Z"
  },
  "requestId": "req_100003",
  "timestamp": 1710000000000
}
```

### 业务处理

```text
1. 校验群名称。
2. 创建 chat_group。
3. 创建 group_member，角色为 owner。
4. 写入系统消息。
5. 写入群操作日志。
```

### 事务要求

创建 `chat_group`、创建群主成员关系、写系统消息需要在同一个事务内完成。

------

## 4.2 查询我的群列表

### 接口

```http
GET /api/v1/groups
```

### 查询参数

| 参数    | 类型   | 必填 | 说明       |
| ------- | ------ | ---- | ---------- |
| cursor  | string | 否   | 游标       |
| limit   | number | 否   | 每页数量   |
| keyword | string | 否   | 群名称搜索 |

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "items": [
      {
        "groupId": 10001,
        "name": "GroupFlow 技术交流群",
        "avatar": "https://example.com/group-avatar.png",
        "groupType": "large",
        "role": "admin",
        "memberCount": 12000,
        "lastMessage": {
          "messageId": "msg_100000001",
          "content": "今天讨论 Kafka Topic 设计",
          "sequence": 100201,
          "createdAt": "2026-06-28T10:00:00.000Z"
        },
        "lastReadSequence": 100180,
        "maxSequence": 100201,
        "unreadCount": 21,
        "mentionMe": true,
        "muted": false
      }
    ],
    "nextCursor": "10001",
    "hasMore": false
  },
  "requestId": "req_100004",
  "timestamp": 1710000000000
}
```

### 说明

`unreadCount` 可以通过：

```text
groupMaxSequence - lastReadSequence
```

计算。

大群未读数超过 99 时，前端展示 `99+`。

------

## 4.3 查询群详情

### 接口

```http
GET /api/v1/groups/{groupId}
```

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "groupId": 10001,
    "name": "GroupFlow 技术交流群",
    "avatar": "https://example.com/group-avatar.png",
    "description": "讨论大群、高并发、WebSocket 和消息投递设计",
    "ownerId": 1001,
    "groupType": "large",
    "joinMode": "approval",
    "status": "normal",
    "muteAll": false,
    "slowModeSeconds": 5,
    "allowMemberInvite": true,
    "mentionAllRole": "admin",
    "memberCount": 12000,
    "maxMemberCount": 100000,
    "myRole": "admin",
    "myStatus": "normal",
    "myLastReadSequence": 100180,
    "createdAt": "2026-06-28T10:00:00.000Z",
    "updatedAt": "2026-06-28T10:00:00.000Z"
  },
  "requestId": "req_100005",
  "timestamp": 1710000000000
}
```

### 权限

只有群成员可以查看群详情。

如果群设置为公开群，可根据产品策略开放部分字段。

------

## 4.4 修改群信息

### 接口

```http
PUT /api/v1/groups/{groupId}
```

### 权限

群主或管理员。

### 请求体

```json
{
  "name": "GroupFlow 高并发交流群",
  "description": "讨论大群投递、WebSocket 集群和 Kafka 削峰",
  "avatar": "https://example.com/new-avatar.png"
}
```

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "groupId": 10001,
    "updatedAt": "2026-06-28T10:20:00.000Z"
  },
  "requestId": "req_100006",
  "timestamp": 1710000000000
}
```

### 业务处理

```text
1. 校验用户是否为群主或管理员。
2. 更新 chat_group。
3. 删除 Redis group:{groupId}:config 缓存。
4. 写入群操作日志。
5. 发送 group-system-event-topic。
```

------

## 4.5 修改群设置

### 接口

```http
PUT /api/v1/groups/{groupId}/settings
```

### 权限

群主或管理员。

### 请求体

```json
{
  "joinMode": "approval",
  "allowMemberInvite": true,
  "mentionAllRole": "admin",
  "maxMemberCount": 100000
}
```

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "groupId": 10001,
    "updatedAt": "2026-06-28T10:30:00.000Z"
  },
  "requestId": "req_100007",
  "timestamp": 1710000000000
}
```

------

## 4.6 解散群

### 接口

```http
POST /api/v1/groups/{groupId}/dismiss
```

### 权限

仅群主。

### 请求体

```json
{
  "reason": "群聊已结束"
}
```

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "groupId": 10001,
    "status": "dismissed",
    "dismissedAt": "2026-06-28T10:40:00.000Z"
  },
  "requestId": "req_100008",
  "timestamp": 1710000000000
}
```

### 业务处理

```text
1. 校验群主权限。
2. 更新 chat_group.status = dismissed。
3. 写入系统消息。
4. 写入操作日志。
5. 删除群配置缓存。
6. 发送 group-system-event-topic。
```

------

# 5. 群成员接口

## 5.1 查询群成员列表

### 接口

```http
GET /api/v1/groups/{groupId}/members
```

### 查询参数

| 参数    | 类型   | 必填 | 说明                       |
| ------- | ------ | ---- | -------------------------- |
| cursor  | string | 否   | 游标，使用 group_member.id |
| limit   | number | 否   | 每页数量                   |
| role    | string | 否   | owner/admin/member         |
| keyword | string | 否   | 昵称搜索                   |

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "items": [
      {
        "memberId": 1,
        "groupId": 10001,
        "userId": 1001,
        "nickname": "张三",
        "avatar": "https://example.com/avatar.png",
        "role": "owner",
        "status": "normal",
        "muteUntil": null,
        "joinedAt": "2026-06-28T10:00:00.000Z"
      }
    ],
    "nextCursor": "1",
    "hasMore": false
  },
  "requestId": "req_100009",
  "timestamp": 1710000000000
}
```

### 设计说明

大群成员列表必须使用游标分页，不允许深分页。

------

## 5.2 查询我的群成员身份

### 接口

```http
GET /api/v1/groups/{groupId}/members/me
```

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "groupId": 10001,
    "userId": 1001,
    "role": "admin",
    "status": "normal",
    "lastReadSequence": 100201,
    "muteUntil": null,
    "joinedAt": "2026-06-28T10:00:00.000Z"
  },
  "requestId": "req_100010",
  "timestamp": 1710000000000
}
```

------

## 5.3 退出群

### 接口

```http
POST /api/v1/groups/{groupId}/members/leave
```

### 请求体

```json
{
  "reason": "用户主动退出"
}
```

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "groupId": 10001,
    "leftAt": "2026-06-28T10:50:00.000Z"
  },
  "requestId": "req_100011",
  "timestamp": 1710000000000
}
```

### 规则

1. 群主不能直接退出，需要先转让群主或解散群。
2. 普通成员和管理员可以退出群。
3. 退出后不再接收该群实时消息。

------

## 5.4 踢出成员

### 接口

```http
POST /api/v1/groups/{groupId}/members/{userId}/kick
```

### 权限

群主或管理员。

### 请求体

```json
{
  "reason": "违反群规则"
}
```

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "groupId": 10001,
    "userId": 1005,
    "kickedAt": "2026-06-28T11:00:00.000Z"
  },
  "requestId": "req_100012",
  "timestamp": 1710000000000
}
```

### 业务规则

1. 不能踢出群主。
2. 管理员不能踢出群主。
3. 普通管理员是否可以踢其他管理员由产品策略决定。
4. 被踢用户应收到 WebSocket 事件 `group_member_kicked`。

------

## 5.5 设置管理员

### 接口

```http
POST /api/v1/groups/{groupId}/members/{userId}/set-admin
```

### 权限

仅群主。

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "groupId": 10001,
    "userId": 1005,
    "role": "admin",
    "updatedAt": "2026-06-28T11:10:00.000Z"
  },
  "requestId": "req_100013",
  "timestamp": 1710000000000
}
```

------

## 5.6 取消管理员

### 接口

```http
POST /api/v1/groups/{groupId}/members/{userId}/unset-admin
```

### 权限

仅群主。

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "groupId": 10001,
    "userId": 1005,
    "role": "member",
    "updatedAt": "2026-06-28T11:15:00.000Z"
  },
  "requestId": "req_100014",
  "timestamp": 1710000000000
}
```

------

# 6. 加群申请接口

## 6.1 提交加群申请

### 接口

```http
POST /api/v1/groups/{groupId}/join-requests
```

### 请求体

```json
{
  "reason": "希望加入讨论大群系统设计"
}
```

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "requestId": 8001,
    "groupId": 10001,
    "status": "pending",
    "createdAt": "2026-06-28T11:20:00.000Z"
  },
  "requestId": "req_100015",
  "timestamp": 1710000000000
}
```

### 业务规则

1. 已经是群成员不能重复申请。
2. 已存在 pending 申请时不能重复提交。
3. 如果群 `joinMode = direct`，可以直接加入。
4. 如果群 `joinMode = approval`，进入审批流程。

------

## 6.2 查询加群申请列表

### 接口

```http
GET /api/v1/groups/{groupId}/join-requests
```

### 权限

群主或管理员。

### 查询参数

| 参数   | 类型   | 必填 | 说明                      |
| ------ | ------ | ---- | ------------------------- |
| status | string | 否   | pending/approved/rejected |
| cursor | string | 否   | 游标                      |
| limit  | number | 否   | 每页数量                  |

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "items": [
      {
        "requestId": 8001,
        "groupId": 10001,
        "userId": 1008,
        "nickname": "李四",
        "avatar": "https://example.com/avatar.png",
        "reason": "希望加入讨论大群系统设计",
        "status": "pending",
        "createdAt": "2026-06-28T11:20:00.000Z"
      }
    ],
    "nextCursor": "8001",
    "hasMore": false
  },
  "requestId": "req_100016",
  "timestamp": 1710000000000
}
```

------

## 6.3 审批加群申请

### 接口

```http
POST /api/v1/groups/{groupId}/join-requests/{joinRequestId}/review
```

### 权限

群主或管理员。

### 请求体

```json
{
  "action": "approve",
  "remark": "欢迎加入"
}
```

`action` 可选值：

```text
approve
reject
```

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "requestId": 8001,
    "groupId": 10001,
    "userId": 1008,
    "status": "approved",
    "reviewerId": 1001,
    "reviewTime": "2026-06-28T11:25:00.000Z"
  },
  "requestId": "req_100017",
  "timestamp": 1710000000000
}
```

### 事务要求

审批通过时，需要在一个事务中完成：

```text
1. 更新 group_join_request.status。
2. 插入或更新 group_member。
3. 更新 chat_group.member_count。
4. 写入系统消息。
5. 写入操作日志。
```

------

# 7. 历史消息接口

## 7.1 查询最近消息

### 接口

```http
GET /api/v1/groups/{groupId}/messages
```

### 查询参数

| 参数           | 类型   | 必填 | 说明         |
| -------------- | ------ | ---- | ------------ |
| limit          | number | 否   | 默认 20      |
| beforeSequence | number | 否   | 查询更早消息 |
| afterSequence  | number | 否   | 查询更新消息 |

### 查询最近消息示例

```http
GET /api/v1/groups/10001/messages?limit=20
```

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "groupId": 10001,
    "items": [
      {
        "messageId": "msg_100000001",
        "groupId": 10001,
        "senderId": 1001,
        "senderName": "张三",
        "senderAvatar": "https://example.com/avatar.png",
        "messageType": "text",
        "content": "今天讨论 HTTP API 设计",
        "sequence": 100201,
        "status": "normal",
        "mentionAll": false,
        "mentionUserIds": [],
        "createdAt": "2026-06-28T11:30:00.000Z"
      }
    ],
    "minSequence": 100180,
    "maxSequence": 100201,
    "hasMoreBefore": true,
    "hasMoreAfter": false
  },
  "requestId": "req_100018",
  "timestamp": 1710000000000
}
```

------

## 7.2 向上加载更早消息

### 接口

```http
GET /api/v1/groups/{groupId}/messages?beforeSequence=100201&limit=20
```

### 说明

查询 `sequence < beforeSequence` 的消息，按 sequence 倒序查询，返回前端后可按升序展示。

### 关键 SQL

```sql
SELECT *
FROM group_message
WHERE group_id = ?
  AND sequence < ?
ORDER BY sequence DESC
LIMIT ?;
```

------

## 7.3 断线后补拉消息

### 接口

```http
GET /api/v1/groups/{groupId}/messages?afterSequence=100201&limit=100
```

### 说明

查询 `sequence > afterSequence` 的消息，用于 WebSocket 断线重连后补拉。

### 关键 SQL

```sql
SELECT *
FROM group_message
WHERE group_id = ?
  AND sequence > ?
ORDER BY sequence ASC
LIMIT ?;
```

### 业务规则

1. limit 最大不超过 200。
2. 如果数据太多，前端需要多次补拉。
3. 如果 sequence 存在跳号，补拉不到缺失 sequence 时，客户端可以接受跳号。

------

## 7.4 查询单条消息详情

### 接口

```http
GET /api/v1/groups/{groupId}/messages/{messageId}
```

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "messageId": "msg_100000001",
    "groupId": 10001,
    "senderId": 1001,
    "senderName": "张三",
    "messageType": "text",
    "content": "今天讨论 HTTP API 设计",
    "sequence": 100201,
    "status": "normal",
    "mentionAll": false,
    "mentionUserIds": [],
    "createdAt": "2026-06-28T11:30:00.000Z"
  },
  "requestId": "req_100019",
  "timestamp": 1710000000000
}
```

------

# 8. 已读未读接口

## 8.1 上报已读位置

### 接口

```http
POST /api/v1/groups/{groupId}/read
```

### 请求体

```json
{
  "lastReadSequence": 100201
}
```

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "groupId": 10001,
    "lastReadSequence": 100201,
    "updatedAt": "2026-06-28T11:35:00.000Z"
  },
  "requestId": "req_100020",
  "timestamp": 1710000000000
}
```

### 业务规则

1. 用户必须是群成员。
2. `lastReadSequence` 只能变大，不能回退。
3. 更新 `group_member.last_read_sequence`。
4. 可以同步更新 Redis 缓存。

### SQL 示例

```sql
UPDATE group_member
SET last_read_sequence = ?,
    updated_at = NOW()
WHERE group_id = ?
  AND user_id = ?
  AND last_read_sequence < ?;
```

------

## 8.2 查询群未读信息

### 接口

```http
GET /api/v1/groups/{groupId}/unread
```

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "groupId": 10001,
    "lastReadSequence": 100180,
    "maxSequence": 100201,
    "unreadCount": 21,
    "mentionMe": true
  },
  "requestId": "req_100021",
  "timestamp": 1710000000000
}
```

### 设计说明

未读数通过：

```text
maxSequence - lastReadSequence
```

计算。

大群中不建议为每个用户维护精确 unread_count Key。

------

## 8.3 查询单条消息已读人数

### 接口

```http
GET /api/v1/groups/{groupId}/messages/{messageId}/read-count
```

### 适用范围

仅普通群建议使用。

大群不建议展示完整已读名单。

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "groupId": 10001,
    "messageId": "msg_100000001",
    "sequence": 100201,
    "readCount": 12,
    "unreadCount": 3
  },
  "requestId": "req_100022",
  "timestamp": 1710000000000
}
```

### 大群限制

如果群为大群，接口可以返回：

```json
{
  "code": "LARGE_GROUP_READ_DETAIL_DISABLED",
  "message": "大群不支持查看完整已读详情",
  "data": null,
  "requestId": "req_100022",
  "timestamp": 1710000000000
}
```

------

# 9. 消息管理接口

## 9.1 撤回消息

### 接口

```http
POST /api/v1/groups/{groupId}/messages/{messageId}/recall
```

### 请求体

```json
{
  "reason": "发送错误"
}
```

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "groupId": 10001,
    "messageId": "msg_100000001",
    "status": "recalled",
    "recalledAt": "2026-06-28T11:40:00.000Z"
  },
  "requestId": "req_100023",
  "timestamp": 1710000000000
}
```

### 业务规则

1. 发送者可以在允许时间窗口内撤回自己的消息。
2. 群主和管理员可以撤回成员消息。
3. 已撤回消息不能重复撤回。
4. 撤回后发送 Kafka 事件 `group-message-recall-topic`。
5. WebSocket 推送 `group_message_recalled` 事件。

------

## 9.2 删除本地消息记录

### 接口

```http
DELETE /api/v1/groups/{groupId}/messages/{messageId}/local
```

### 说明

仅删除当前用户本地视角的消息，不影响其他成员。

一期可以不实现。

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "messageId": "msg_100000001",
    "deleted": true
  },
  "requestId": "req_100024",
  "timestamp": 1710000000000
}
```

------

# 10. 群公告接口

## 10.1 查询群公告列表

### 接口

```http
GET /api/v1/groups/{groupId}/announcements
```

### 查询参数

| 参数   | 类型   | 必填 | 说明     |
| ------ | ------ | ---- | -------- |
| cursor | string | 否   | 游标     |
| limit  | number | 否   | 每页数量 |

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "items": [
      {
        "announcementId": 9001,
        "groupId": 10001,
        "title": "今日讨论主题",
        "content": "今天讨论大群投递和 Kafka Topic 设计",
        "creatorId": 1001,
        "creatorName": "张三",
        "pinned": true,
        "createdAt": "2026-06-28T11:50:00.000Z",
        "updatedAt": "2026-06-28T11:50:00.000Z"
      }
    ],
    "nextCursor": "9001",
    "hasMore": false
  },
  "requestId": "req_100025",
  "timestamp": 1710000000000
}
```

------

## 10.2 查询最新置顶公告

### 接口

```http
GET /api/v1/groups/{groupId}/announcements/latest
```

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "announcementId": 9001,
    "groupId": 10001,
    "title": "今日讨论主题",
    "content": "今天讨论大群投递和 Kafka Topic 设计",
    "pinned": true,
    "createdAt": "2026-06-28T11:50:00.000Z"
  },
  "requestId": "req_100026",
  "timestamp": 1710000000000
}
```

------

## 10.3 发布群公告

### 接口

```http
POST /api/v1/groups/{groupId}/announcements
```

### 权限

群主或管理员。

### 请求体

```json
{
  "title": "今日讨论主题",
  "content": "今天讨论大群投递和 Kafka Topic 设计",
  "pinned": true
}
```

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "announcementId": 9001,
    "groupId": 10001,
    "createdAt": "2026-06-28T11:50:00.000Z"
  },
  "requestId": "req_100027",
  "timestamp": 1710000000000
}
```

### 业务处理

```text
1. 校验管理员权限。
2. 写入 group_announcement。
3. 如果 pinned = true，更新其他公告为非置顶。
4. 删除 Redis group:{groupId}:config 或公告缓存。
5. 写入系统消息。
6. 发送 group-system-event-topic。
```

------

## 10.4 修改群公告

### 接口

```http
PUT /api/v1/groups/{groupId}/announcements/{announcementId}
```

### 权限

群主或管理员。

### 请求体

```json
{
  "title": "更新后的公告标题",
  "content": "更新后的公告内容",
  "pinned": true
}
```

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "announcementId": 9001,
    "updatedAt": "2026-06-28T12:00:00.000Z"
  },
  "requestId": "req_100028",
  "timestamp": 1710000000000
}
```

------

## 10.5 删除群公告

### 接口

```http
DELETE /api/v1/groups/{groupId}/announcements/{announcementId}
```

### 权限

群主或管理员。

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "announcementId": 9001,
    "deleted": true
  },
  "requestId": "req_100029",
  "timestamp": 1710000000000
}
```

------

# 11. 禁言与慢速模式接口

## 11.1 开启全员禁言

### 接口

```http
POST /api/v1/groups/{groupId}/mute-all
```

### 权限

群主或管理员。

### 请求体

```json
{
  "reason": "会议期间禁止发言"
}
```

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "groupId": 10001,
    "muteAll": true,
    "updatedAt": "2026-06-28T12:10:00.000Z"
  },
  "requestId": "req_100030",
  "timestamp": 1710000000000
}
```

### 业务处理

```text
1. 更新 chat_group.mute_all = 1。
2. 删除 group:{groupId}:config。
3. 写入操作日志。
4. 发送 group-system-event-topic。
```

------

## 11.2 关闭全员禁言

### 接口

```http
POST /api/v1/groups/{groupId}/unmute-all
```

### 权限

群主或管理员。

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "groupId": 10001,
    "muteAll": false,
    "updatedAt": "2026-06-28T12:20:00.000Z"
  },
  "requestId": "req_100031",
  "timestamp": 1710000000000
}
```

------

## 11.3 禁言成员

### 接口

```http
POST /api/v1/groups/{groupId}/members/{userId}/mute
```

### 权限

群主或管理员。

### 请求体

```json
{
  "muteSeconds": 3600,
  "reason": "刷屏"
}
```

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "groupId": 10001,
    "userId": 1005,
    "muteUntil": "2026-06-28T13:20:00.000Z"
  },
  "requestId": "req_100032",
  "timestamp": 1710000000000
}
```

### 业务处理

```text
1. 校验管理员权限。
2. 更新 group_member.mute_until。
3. 写入 group_mute_record。
4. 写入操作日志。
5. 发送系统事件。
```

------

## 11.4 解除成员禁言

### 接口

```http
POST /api/v1/groups/{groupId}/members/{userId}/unmute
```

### 权限

群主或管理员。

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "groupId": 10001,
    "userId": 1005,
    "muteUntil": null
  },
  "requestId": "req_100033",
  "timestamp": 1710000000000
}
```

------

## 11.5 设置慢速模式

### 接口

```http
PUT /api/v1/groups/{groupId}/slow-mode
```

### 权限

群主或管理员。

### 请求体

```json
{
  "slowModeSeconds": 5
}
```

`slowModeSeconds = 0` 表示关闭慢速模式。

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "groupId": 10001,
    "slowModeSeconds": 5,
    "updatedAt": "2026-06-28T12:30:00.000Z"
  },
  "requestId": "req_100034",
  "timestamp": 1710000000000
}
```

------

# 12. @提醒接口

## 12.1 查询我的 @提醒列表

### 接口

```http
GET /api/v1/mentions
```

### 查询参数

| 参数       | 类型   | 必填 | 说明           |
| ---------- | ------ | ---- | -------------- |
| cursor     | string | 否   | 游标           |
| limit      | number | 否   | 每页数量       |
| readStatus | number | 否   | 0 未读，1 已读 |

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "items": [
      {
        "mentionId": 7001,
        "groupId": 10001,
        "groupName": "GroupFlow 技术交流群",
        "messageId": "msg_100000001",
        "sequence": 100201,
        "senderId": 1001,
        "senderName": "张三",
        "contentPreview": "今天讨论 HTTP API 设计",
        "mentionType": "user",
        "readStatus": 0,
        "createdAt": "2026-06-28T12:40:00.000Z"
      }
    ],
    "nextCursor": "7001",
    "hasMore": false
  },
  "requestId": "req_100035",
  "timestamp": 1710000000000
}
```

------

## 12.2 标记 @提醒已读

### 接口

```http
POST /api/v1/mentions/{mentionId}/read
```

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "mentionId": 7001,
    "readStatus": 1
  },
  "requestId": "req_100036",
  "timestamp": 1710000000000
}
```

------

# 13. 搜索接口

## 13.1 搜索群消息

### 接口

```http
GET /api/v1/groups/{groupId}/messages/search
```

### 查询参数

| 参数        | 类型   | 必填 | 说明            |
| ----------- | ------ | ---- | --------------- |
| keyword     | string | 是   | 搜索关键词      |
| cursor      | string | 否   | 游标            |
| limit       | number | 否   | 每页数量        |
| messageType | string | 否   | text/file/image |

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "items": [
      {
        "messageId": "msg_100000001",
        "groupId": 10001,
        "senderId": 1001,
        "senderName": "张三",
        "messageType": "text",
        "content": "今天讨论 HTTP API 设计",
        "sequence": 100201,
        "createdAt": "2026-06-28T12:50:00.000Z"
      }
    ],
    "nextCursor": "msg_100000001",
    "hasMore": false
  },
  "requestId": "req_100037",
  "timestamp": 1710000000000
}
```

### 实现说明

一期可以不实现全文搜索。

可选方案：

1. MySQL LIKE，适合小数据量。
2. Elasticsearch / OpenSearch，适合后续扩展。
3. Kafka `group-search-index-topic` 异步构建索引。

------

# 14. 文件与图片消息接口

## 14.1 上传文件

### 接口

```http
POST /api/v1/files
```

### 请求类型

```http
multipart/form-data
```

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "fileId": "file_10001",
    "fileName": "设计文档.pdf",
    "fileSize": 1024000,
    "fileUrl": "https://example.com/files/file_10001",
    "mimeType": "application/pdf",
    "createdAt": "2026-06-28T13:00:00.000Z"
  },
  "requestId": "req_100038",
  "timestamp": 1710000000000
}
```

### 说明

发送文件消息仍然走 WebSocket `group_message_send`，但消息内容只携带文件元数据，不携带二进制内容。

------

# 15. 管理与统计接口

## 15.1 查询群统计信息

### 接口

```http
GET /api/v1/groups/{groupId}/stats
```

### 权限

群主或管理员。

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "groupId": 10001,
    "memberCount": 12000,
    "onlineCount": 3000,
    "messageCountToday": 10234,
    "messageQps": 12.5,
    "groupType": "large",
    "hotGroup": false
  },
  "requestId": "req_100039",
  "timestamp": 1710000000000
}
```

### 说明

在线人数可来自 Redis 统计。

消息量可来自 MySQL 聚合或异步统计表。

------

## 15.2 查询群操作日志

### 接口

```http
GET /api/v1/groups/{groupId}/operation-logs
```

### 权限

群主或管理员。

### 查询参数

| 参数          | 类型   | 必填 | 说明     |
| ------------- | ------ | ---- | -------- |
| cursor        | string | 否   | 游标     |
| limit         | number | 否   | 每页数量 |
| operationType | string | 否   | 操作类型 |

### 响应

```json
{
  "code": "OK",
  "message": "success",
  "data": {
    "items": [
      {
        "logId": 6001,
        "groupId": 10001,
        "operatorId": 1001,
        "operatorName": "张三",
        "targetUserId": 1005,
        "targetUserName": "李四",
        "operationType": "kick_member",
        "operationDetail": {
          "reason": "违反群规则"
        },
        "createdAt": "2026-06-28T13:10:00.000Z"
      }
    ],
    "nextCursor": "6001",
    "hasMore": false
  },
  "requestId": "req_100040",
  "timestamp": 1710000000000
}
```

------

# 16. 错误码设计

## 16.1 通用错误码

| 错误码            | 说明         |
| ----------------- | ------------ |
| OK                | 成功         |
| BAD_REQUEST       | 请求参数错误 |
| UNAUTHORIZED      | 未登录       |
| FORBIDDEN         | 无权限       |
| NOT_FOUND         | 资源不存在   |
| INTERNAL_ERROR    | 服务内部错误 |
| TOO_MANY_REQUESTS | 请求过于频繁 |

## 16.2 用户错误码

| 错误码             | 说明       |
| ------------------ | ---------- |
| USER_NOT_FOUND     | 用户不存在 |
| USER_BANNED        | 用户被封禁 |
| AUTH_TOKEN_INVALID | token 无效 |
| AUTH_TOKEN_EXPIRED | token 过期 |

## 16.3 群错误码

| 错误码                      | 说明             |
| --------------------------- | ---------------- |
| GROUP_NOT_FOUND             | 群不存在         |
| GROUP_DISMISSED             | 群已解散         |
| GROUP_BANNED                | 群被封禁         |
| GROUP_MEMBER_NOT_FOUND      | 用户不是群成员   |
| GROUP_MEMBER_KICKED         | 用户已被踢出     |
| GROUP_PERMISSION_DENIED     | 群权限不足       |
| GROUP_OWNER_CANNOT_LEAVE    | 群主不能直接退出 |
| GROUP_MEMBER_LIMIT_EXCEEDED | 群人数超过限制   |

## 16.4 消息错误码

| 错误码                           | 说明               |
| -------------------------------- | ------------------ |
| MESSAGE_NOT_FOUND                | 消息不存在         |
| MESSAGE_RECALLED                 | 消息已撤回         |
| MESSAGE_RECALL_TIMEOUT           | 消息超过可撤回时间 |
| MESSAGE_RECALL_PERMISSION_DENIED | 无权撤回该消息     |
| MESSAGE_CONTENT_EMPTY            | 消息内容为空       |
| MESSAGE_CONTENT_TOO_LONG         | 消息内容过长       |

## 16.5 禁言与限流错误码

| 错误码                   | 说明             |
| ------------------------ | ---------------- |
| GROUP_MUTE_ALL           | 群已全员禁言     |
| GROUP_MEMBER_MUTED       | 成员被禁言       |
| MESSAGE_RATE_LIMITED     | 消息发送过于频繁 |
| MENTION_ALL_RATE_LIMITED | @所有人过于频繁  |

------

# 17. 权限矩阵

| 操作         | 群主               | 管理员 | 普通成员 |
| ------------ | ------------------ | ------ | -------- |
| 创建群       | 是                 | 是     | 是       |
| 修改群信息   | 是                 | 是     | 否       |
| 解散群       | 是                 | 否     | 否       |
| 查询成员列表 | 是                 | 是     | 是       |
| 踢人         | 是                 | 是     | 否       |
| 设置管理员   | 是                 | 否     | 否       |
| 取消管理员   | 是                 | 否     | 否       |
| 退出群       | 是，需先转让或解散 | 是     | 是       |
| 发布公告     | 是                 | 是     | 否       |
| 全员禁言     | 是                 | 是     | 否       |
| 单人禁言     | 是                 | 是     | 否       |
| 设置慢速模式 | 是                 | 是     | 否       |
| 发送普通消息 | 是                 | 是     | 是       |
| @所有人      | 是                 | 视配置 | 否       |
| 撤回自己消息 | 是                 | 是     | 是       |
| 撤回他人消息 | 是                 | 是     | 否       |

------

# 18. 接口与存储关系

| 接口类型 | 主要 MySQL 表                       | Redis                           | Kafka                      |
| -------- | ----------------------------------- | ------------------------------- | -------------------------- |
| 群管理   | chat_group、group_operation_log     | group:{groupId}:config          | group-system-event-topic   |
| 成员管理 | group_member、group_operation_log   | group:{groupId}:members、admins | group-system-event-topic   |
| 消息查询 | group_message                       | group:{groupId}:max_sequence    | 无                         |
| 已读上报 | group_member                        | last_read_sequence 缓存         | 无                         |
| 消息撤回 | group_message、group_message_recall | 无                              | group-message-recall-topic |
| 公告     | group_announcement                  | 公告缓存                        | group-system-event-topic   |
| 审批     | group_join_request、group_member    | group config                    | group-system-event-topic   |
| 禁言     | group_member、group_mute_record     | group config、rate limit        | group-system-event-topic   |

------

# 19. 一期实现范围

一期建议必须实现：

1. 登录。
2. 获取当前用户。
3. 创建群。
4. 查询我的群列表。
5. 查询群详情。
6. 修改群信息。
7. 查询群成员列表。
8. 退出群。
9. 踢出成员。
10. 设置管理员。
11. 取消管理员。
12. 提交加群申请。
13. 查询加群申请。
14. 审批加群申请。
15. 查询历史消息。
16. 断线补拉消息。
17. 上报已读位置。
18. 查询群未读信息。
19. 撤回消息。
20. 查询公告。
21. 发布公告。
22. 全员禁言。
23. 单人禁言。
24. 设置慢速模式。

一期可以暂缓：

1. 消息搜索。
2. 文件上传。
3. @提醒列表。
4. 群统计。
5. 操作日志查询后台。
6. 本地删除消息。
7. 完整已读名单。

------

# 20. 总结

GroupFlow HTTP API 的核心设计是：

1. HTTP API 负责管理类、查询类和补拉类能力。
2. WebSocket 负责实时消息发送、ACK 和推送。
3. 群消息发送本身走 WebSocket，不走 HTTP。
4. 历史消息查询和断线补拉走 HTTP。
5. 群成员分页使用游标分页，避免大群深分页。
6. 历史消息分页使用 sequence 游标。
7. 未读数通过 maxSequence - lastReadSequence 计算。
8. 大群不维护每个用户的精确 unread_count。
9. 消息撤回通过 HTTP 发起，通过 Kafka 和 WebSocket 推送撤回事件。
10. 管理类接口必须校验群角色权限。
11. 修改群配置后需要清理 Redis 群配置缓存。
12. 关键管理操作需要写操作日志。
13. 重要群事件需要发布 Kafka 系统事件。
14. 一期接口应优先保证群聊闭环、大群分页、消息补拉和管理能力完整。