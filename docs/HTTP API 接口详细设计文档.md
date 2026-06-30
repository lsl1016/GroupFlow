# HTTP API 接口详细设计文档

> 本文档以 **当前后端实现为准**（`backend/internal/api`）。
> 路由定义见 `router.go`，统一响应见 `response.go`，请求/响应结构见 `dto.go`，
> 领域模型见 `internal/domain/models.go`。响应数据均由 API 层 DTO 下发，不直接暴露领域对象。

## 1. 文档说明

### 1.1 文档目的

说明 GroupFlow 群聊系统已实现的 HTTP API：认证、群管理、成员管理、加群审批、历史消息、已读位置、消息撤回、@ 提醒、群公告、禁言/慢速、群设置，以及内部服务与运维接口。

GroupFlow 面向大群与高并发场景，仅做群聊，不做单聊。

### 1.2 HTTP API 与 WebSocket 职责边界

HTTP API 负责：登录与用户信息、群与成员的管理/查询、加群审批、历史消息查询与断线补拉、已读上报、消息撤回（发起）、@ 提醒查询、群公告管理、禁言/慢速/群设置。

WebSocket 负责：群消息实时发送（`group_message_send`）、服务端 ACK（`group_message_ack`）、实时接收（`group_message_receive`）、撤回事件（`group_message_recalled`）、群事件推送、已读回执（`group_message_read`）。

> 群消息发送走 WebSocket，不走 HTTP；历史消息与断线补拉走 HTTP。

### 1.3 设计原则

1. 统一响应结构：`errNo / errMsg / traceId / data`。
2. 响应数据通过 API 层 DTO 下发，与领域模型解耦（领域字段变动不直接影响 API 契约）。
3. 所有业务接口基于 Bearer token 鉴权（`/auth/login` 除外）。
4. 群操作均校验群成员身份与角色权限。
5. 列表统一游标分页，避免大群深分页。
6. 历史消息用 `sequence` 游标，成员/公告/审批/提醒用记录自增 `id` 游标。
7. 全链路 `traceId` 贯穿 HTTP、WS、service、repo。

------

## 2. 通用约定

### 2.1 基础路径

```text
/api/v1
```

### 2.2 请求头

需要登录的接口必须携带：

```http
Authorization: Bearer {token}
Content-Type: application/json
```

WebSocket 握手不走 Authorization 头，token 放在查询参数：

```text
GET /ws?token={token}
GET /api/ws?token={token}
```

### 2.3 统一响应结构

成功：

```json
{
  "errNo": 0,
  "errMsg": "succ",
  "traceId": "trace-7f3a1c2b",
  "data": {}
}
```

失败：

```json
{
  "errNo": 20002,
  "errMsg": "group not found",
  "traceId": "trace-7f3a1c2b",
  "data": null
}
```

字段说明：

| 字段    | 类型   | 说明                                            |
| ------- | ------ | ----------------------------------------------- |
| errNo   | number | 业务错误码，`0` 表示成功；未登记的字符串码映射为 `-1` |
| errMsg  | string | 可读信息；成功固定为 `succ`                      |
| traceId | string | 链路追踪 ID，同时写回响应头 `X-Trace-Id`         |
| data    | any    | 业务数据；失败时为 `null`                        |

> 失败响应仍保留语义化 HTTP 状态码（如 400/401/403/404/500），body 中携带映射后的整数 `errNo`。错误码映射表见第 12 节。

### 2.4 TraceId 约定

服务端按优先级解析/生成 traceId：请求头 `X-Trace-Id` → `X-Request-Id` → 自动生成。解析结果写入响应头 `X-Trace-Id`，并贯穿 service/repo/redis/kafka 各层日志。

### 2.5 分页规范

所有列表使用游标分页，响应统一为 `PageDTO`：

```json
{
  "items": [],
  "nextCursor": "10086",
  "hasMore": true
}
```

| 字段       | 类型    | 说明                          |
| ---------- | ------- | ----------------------------- |
| items      | array   | 当前页数据，恒为数组（无数据为 `[]`） |
| nextCursor | string  | 下一页游标，空串表示无更多      |
| hasMore    | boolean | 是否还有更多                   |

游标取值：

- 群成员 / 公告 / 加群审批 / @ 提醒：使用对应记录的自增 `id`（通过 `cursor` 传入）。
- 历史消息：使用 `sequence`，通过 `beforeSequence` / `afterSequence` 传入（详见 7.1）。

### 2.6 时间格式

时间字段统一 ISO 8601 / RFC3339，例如：

```text
2026-06-28T10:00:00Z
```

可空时间（如 `lastMessageAt`、`leftAt`）带 `omitempty`，无值时字段省略。

### 2.7 ID 约定

| 类型            | 示例           | 说明              |
| --------------- | -------------- | ----------------- |
| userId          | 1001           | 用户 ID           |
| groupId         | 10001          | 群 ID             |
| messageId       | msg_100000001  | 业务消息 ID       |
| clientMessageId | client_msg_001 | 客户端消息 ID     |
| announcementId  | 9001           | 公告 ID           |
| requestId       | 8001           | 加群审批记录 ID   |
| mentionId       | 7001           | @ 提醒记录 ID     |

------

# 3. 认证接口

## 3.1 用户登录

```http
POST /api/v1/auth/login
```

无需鉴权。初期用用户名模拟登录，后续可扩展密码/验证码/OAuth。

### 请求体

```json
{ "username": "user_001" }
```

| 字段     | 类型   | 必填 | 说明     |
| -------- | ------ | ---- | -------- |
| username | string | 是   | 登录用户名 |

### 响应（data: LoginResponse）

```json
{
  "errNo": 0,
  "errMsg": "succ",
  "traceId": "trace-7f3a1c2b",
  "data": {
    "userId": 1001,
    "username": "user_001",
    "nickname": "张三",
    "avatar": "https://example.com/avatar.png",
    "token": "mock-token-xxx"
  }
}
```

失败：`AUTH_FAILED`（HTTP 400）。

------

## 3.2 获取当前用户信息

```http
GET /api/v1/auth/me
```

### 响应（data: UserDTO）

```json
{
  "errNo": 0,
  "errMsg": "succ",
  "traceId": "trace-7f3a1c2b",
  "data": {
    "userId": 1001,
    "username": "user_001",
    "nickname": "张三",
    "avatar": "https://example.com/avatar.png",
    "status": "normal",
    "createdAt": "2026-06-28T10:00:00Z",
    "updatedAt": "2026-06-28T10:00:00Z"
  }
}
```

失败：`USER_NOT_FOUND`（HTTP 404）。

------

# 4. 群管理接口

## 4.1 创建群

```http
POST /api/v1/groups
```

创建人自动成为群主，并写入一条系统消息。

### 请求体（CreateGroupRequest）

```json
{
  "name": "GroupFlow 技术交流群",
  "description": "讨论大群、高并发、WebSocket 和消息投递设计",
  "avatar": "https://example.com/group-avatar.png",
  "joinMode": "approval",
  "groupType": "large",
  "maxMemberCount": 100000,
  "slowModeSeconds": 5
}
```

| 字段            | 类型   | 必填 | 约束 / 说明                          |
| --------------- | ------ | ---- | ------------------------------------ |
| name            | string | 是   | 群名称                               |
| description     | string | 否   | 群简介                               |
| avatar          | string | 否   | 群头像 URL                           |
| joinMode        | string | 否   | `direct` / `approval` / `invite`     |
| groupType       | string | 否   | `normal` / `large`                   |
| maxMemberCount  | number | 否   | ≥ 1                                  |
| slowModeSeconds | number | 否   | ≥ 0，0 表示关闭慢速                  |

### 响应（data: GroupDTO）

```json
{
  "errNo": 0,
  "errMsg": "succ",
  "traceId": "trace-7f3a1c2b",
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
    "allowMemberInvite": false,
    "mentionAllRole": "admin",
    "memberCount": 1,
    "maxMemberCount": 100000,
    "maxSequence": 0,
    "lastMessageId": "",
    "lastMessageSummary": "",
    "createdAt": "2026-06-28T10:00:00Z",
    "updatedAt": "2026-06-28T10:00:00Z"
  }
}
```

失败：`CREATE_GROUP_FAILED`（HTTP 400）。

------

## 4.2 查询我的群列表

```http
GET /api/v1/groups
```

### 查询参数

| 参数   | 类型   | 必填 | 说明                  |
| ------ | ------ | ---- | --------------------- |
| cursor | number | 否   | 游标（上一页 nextCursor）|
| limit  | number | 否   | 每页数量，默认 30      |

### 响应（data: PageDTO\<GroupListItemDTO>）

`GroupListItemDTO` = `GroupDTO` 全部字段 + 当前用户维度字段：

```json
{
  "errNo": 0,
  "errMsg": "succ",
  "traceId": "trace-7f3a1c2b",
  "data": {
    "items": [
      {
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
        "allowMemberInvite": false,
        "mentionAllRole": "admin",
        "memberCount": 12000,
        "maxMemberCount": 100000,
        "maxSequence": 100201,
        "lastMessageId": "msg_100000001",
        "lastMessageSummary": "今天讨论 Kafka Topic 设计",
        "lastMessageAt": "2026-06-28T10:00:00Z",
        "createdAt": "2026-06-28T10:00:00Z",
        "updatedAt": "2026-06-28T10:00:00Z",
        "myRole": "admin",
        "lastReadSequence": 100180,
        "unreadCount": 21,
        "mentionCount": 1,
        "mentionAllUnread": false,
        "mentionSummaryText": "[有人@我]"
      }
    ],
    "nextCursor": "10001",
    "hasMore": false
  }
}
```

> 未读数：`maxSequence - lastReadSequence`；前端超过 99 展示 `99+`。大群不为每个用户维护精确 unread 计数。

------

## 4.3 查询群详情

```http
GET /api/v1/groups/{groupId}
```

仅群成员可访问，否则 `FORBIDDEN`（HTTP 403）。

### 响应（data: GroupDetailResponse）

```json
{
  "errNo": 0,
  "errMsg": "succ",
  "traceId": "trace-7f3a1c2b",
  "data": {
    "group": { "groupId": 10001, "name": "GroupFlow 技术交流群", "ownerId": 1001, "groupType": "large", "joinMode": "approval", "status": "normal", "muteAll": false, "slowModeSeconds": 5, "memberCount": 12000, "maxMemberCount": 100000, "maxSequence": 100201, "createdAt": "2026-06-28T10:00:00Z", "updatedAt": "2026-06-28T10:00:00Z" },
    "myMember": {
      "id": 1,
      "groupId": 10001,
      "userId": 1001,
      "username": "user_001",
      "nickname": "张三",
      "avatar": "https://example.com/avatar.png",
      "role": "admin",
      "status": "normal",
      "lastReadSequence": 100180,
      "joinedAt": "2026-06-28T10:00:00Z",
      "createdAt": "2026-06-28T10:00:00Z",
      "updatedAt": "2026-06-28T10:00:00Z"
    },
    "onlineUserIds": [1001, 1002, 1003]
  }
}
```

> `group` 为 `GroupDTO`（示例省略部分字段）；`myMember` 为 `MemberDTO`，非成员时为 `null`；`onlineUserIds` 为当前在线用户 ID 列表。

------

## 4.4 修改群设置

```http
PATCH /api/v1/groups/{groupId}/settings
```

权限：群主或管理员（失败 `UPDATE_SETTINGS_FAILED`，HTTP 403）。
全员禁言、慢速模式、群类型、人数上限均通过本接口修改。

### 请求体（UpdateSettingsRequest，指针字段为 null 表示不修改）

```json
{
  "muteAll": true,
  "slowModeSeconds": 10,
  "groupType": "large",
  "maxMemberCount": 100000
}
```

| 字段            | 类型    | 约束 / 说明                       |
| --------------- | ------- | --------------------------------- |
| muteAll         | boolean | 是否全员禁言，null 不变更         |
| slowModeSeconds | number  | ≥ 0，null 不变更                  |
| groupType       | string  | `normal` / `large`，null 不变更  |
| maxMemberCount  | number  | ≥ 1，null 不变更                 |

### 响应

```json
{ "errNo": 0, "errMsg": "succ", "traceId": "trace-7f3a1c2b", "data": { "updated": true } }
```

------

## 4.5 解散群

```http
DELETE /api/v1/groups/{groupId}
```

权限：仅群主（失败 `DISMISS_FAILED`，HTTP 403）。解散会写系统消息。

### 响应

```json
{ "errNo": 0, "errMsg": "succ", "traceId": "trace-7f3a1c2b", "data": { "dismissed": true } }
```

------

# 5. 群成员接口

## 5.1 查询群成员列表

```http
GET /api/v1/groups/{groupId}/members
```

仅群成员可访问。

### 查询参数

| 参数   | 类型   | 必填 | 说明                          |
| ------ | ------ | ---- | ----------------------------- |
| cursor | number | 否   | 游标，使用 `group_member.id`  |
| limit  | number | 否   | 每页数量，默认 50             |
| role   | string | 否   | `owner` / `admin` / `member` |

### 响应（data: PageDTO\<MemberDTO>）

```json
{
  "errNo": 0,
  "errMsg": "succ",
  "traceId": "trace-7f3a1c2b",
  "data": {
    "items": [
      {
        "id": 1,
        "groupId": 10001,
        "userId": 1001,
        "username": "user_001",
        "nickname": "张三",
        "avatar": "https://example.com/avatar.png",
        "role": "owner",
        "status": "normal",
        "lastReadSequence": 100201,
        "joinedAt": "2026-06-28T10:00:00Z",
        "createdAt": "2026-06-28T10:00:00Z",
        "updatedAt": "2026-06-28T10:00:00Z"
      }
    ],
    "nextCursor": "1",
    "hasMore": false
  }
}
```

> 成员在群时 `leftAt` 省略；离群/被踢后该字段为离群时间。

------

## 5.2 设置成员角色（设/取消管理员）

```http
POST /api/v1/groups/{groupId}/members/{userId}/role
```

权限：群主（失败 `SET_ROLE_FAILED`，HTTP 403）。

### 请求体（SetRoleRequest）

```json
{ "role": "admin" }
```

| 字段 | 类型   | 必填 | 约束               |
| ---- | ------ | ---- | ------------------ |
| role | string | 是   | `admin` / `member` |

### 响应

```json
{ "errNo": 0, "errMsg": "succ", "traceId": "trace-7f3a1c2b", "data": { "updated": true } }
```

------

## 5.3 踢出成员

```http
DELETE /api/v1/groups/{groupId}/members/{userId}
```

权限：群主或管理员（失败 `KICK_FAILED`，HTTP 403）。被踢用户会收到 WS 事件 `group_member_kicked`。

### 响应

```json
{ "errNo": 0, "errMsg": "succ", "traceId": "trace-7f3a1c2b", "data": { "kicked": true } }
```

------

## 5.4 禁言成员

```http
POST /api/v1/groups/{groupId}/members/{userId}/mute
```

权限：群主或管理员（失败 `MUTE_FAILED`，HTTP 403）。

### 请求体（MuteMemberRequest）

```json
{ "seconds": 3600, "reason": "刷屏" }
```

| 字段    | 类型   | 必填 | 约束 / 说明              |
| ------- | ------ | ---- | ----------------------- |
| seconds | number | 是   | ≥ 0，禁言时长（秒）      |
| reason  | string | 否   | 禁言原因                 |

### 响应

```json
{ "errNo": 0, "errMsg": "succ", "traceId": "trace-7f3a1c2b", "data": { "muted": true } }
```

------

## 5.5 解除成员禁言

```http
DELETE /api/v1/groups/{groupId}/members/{userId}/mute
```

权限：群主或管理员（失败 `UNMUTE_FAILED`，HTTP 403）。

### 响应

```json
{ "errNo": 0, "errMsg": "succ", "traceId": "trace-7f3a1c2b", "data": { "unmuted": true } }
```

------

## 5.6 退出群

```http
POST /api/v1/groups/{groupId}/leave
```

失败 `LEAVE_GROUP_FAILED`（HTTP 400）。群主需先转让或解散，不能直接退出。

### 响应

```json
{ "errNo": 0, "errMsg": "succ", "traceId": "trace-7f3a1c2b", "data": { "left": true } }
```

------

# 6. 加群与审批接口

## 6.1 加入群 / 提交加群审批

```http
POST /api/v1/groups/{groupId}/join
```

根据群 `joinMode`：`direct` 直接加入；`approval` 进入审批。失败 `JOIN_GROUP_FAILED`（HTTP 400）。

### 请求体（JoinGroupRequest）

```json
{ "reason": "希望加入讨论大群系统设计" }
```

### 响应（data: JoinGroupResponse）

直接加入：

```json
{ "errNo": 0, "errMsg": "succ", "traceId": "trace-7f3a1c2b", "data": { "joined": true, "pending": false } }
```

进入审批（同时通过 WS 向管理员推送 `group_join_request_created`）：

```json
{
  "errNo": 0,
  "errMsg": "succ",
  "traceId": "trace-7f3a1c2b",
  "data": {
    "joined": false,
    "pending": true,
    "request": {
      "requestId": 8001,
      "groupId": 10001,
      "userId": 1008,
      "username": "user_008",
      "nickname": "李四",
      "avatar": "https://example.com/avatar.png",
      "reason": "希望加入讨论大群系统设计",
      "status": "pending",
      "createdAt": "2026-06-28T11:20:00Z",
      "updatedAt": "2026-06-28T11:20:00Z"
    }
  }
}
```

------

## 6.2 查询加群审批列表

```http
GET /api/v1/groups/{groupId}/join-requests
```

权限：群主或管理员，否则 `FORBIDDEN`（HTTP 403）。

### 查询参数

| 参数   | 类型   | 必填 | 说明                                |
| ------ | ------ | ---- | ----------------------------------- |
| cursor | number | 否   | 游标                                |
| limit  | number | 否   | 每页数量，默认 30                   |
| status | string | 否   | `pending`(默认)/`approved`/`rejected` |

### 响应（data: PageDTO\<JoinRequestDTO>）

```json
{
  "errNo": 0,
  "errMsg": "succ",
  "traceId": "trace-7f3a1c2b",
  "data": {
    "items": [
      {
        "requestId": 8001,
        "groupId": 10001,
        "userId": 1008,
        "username": "user_008",
        "nickname": "李四",
        "avatar": "https://example.com/avatar.png",
        "reason": "希望加入讨论大群系统设计",
        "status": "pending",
        "createdAt": "2026-06-28T11:20:00Z",
        "updatedAt": "2026-06-28T11:20:00Z"
      }
    ],
    "nextCursor": "8001",
    "hasMore": false
  }
}
```

------

## 6.3 通过加群审批

```http
POST /api/v1/groups/{groupId}/join-requests/{requestId}/approve
```

权限：群主或管理员（失败 `APPROVE_JOIN_FAILED`，HTTP 403）。通过后写系统消息，并向申请人推送 WS 事件 `group_join_request_approved`。

### 响应（data: JoinRequestDTO）

```json
{
  "errNo": 0,
  "errMsg": "succ",
  "traceId": "trace-7f3a1c2b",
  "data": {
    "requestId": 8001,
    "groupId": 10001,
    "userId": 1008,
    "username": "user_008",
    "nickname": "李四",
    "avatar": "https://example.com/avatar.png",
    "reason": "希望加入讨论大群系统设计",
    "status": "approved",
    "operatorId": 1001,
    "createdAt": "2026-06-28T11:20:00Z",
    "updatedAt": "2026-06-28T11:25:00Z"
  }
}
```

------

## 6.4 拒绝加群审批

```http
POST /api/v1/groups/{groupId}/join-requests/{requestId}/reject
```

权限：群主或管理员（失败 `REJECT_JOIN_FAILED`，HTTP 403）。拒绝后向申请人推送 WS 事件 `group_join_request_rejected`。

### 响应（data: JoinRequestDTO）

```json
{
  "errNo": 0,
  "errMsg": "succ",
  "traceId": "trace-7f3a1c2b",
  "data": {
    "requestId": 8001, "groupId": 10001, "userId": 1008,
    "username": "user_008", "nickname": "李四", "avatar": "https://example.com/avatar.png",
    "reason": "希望加入讨论大群系统设计", "status": "rejected", "operatorId": 1001,
    "createdAt": "2026-06-28T11:20:00Z", "updatedAt": "2026-06-28T11:26:00Z"
  }
}
```

------

# 7. 历史消息接口

## 7.1 查询历史消息（游标分页 / 断线补拉）

```http
GET /api/v1/groups/{groupId}/messages
```

仅群成员可访问。群消息发送走 WebSocket，本接口仅做查询与补拉。

### 查询参数

| 参数           | 类型   | 必填 | 说明                          |
| -------------- | ------ | ---- | ----------------------------- |
| limit          | number | 否   | 默认 50                       |
| beforeSequence | number | 否   | 向上翻页：查询 `sequence <` 该值 |
| afterSequence  | number | 否   | 断线补拉：查询 `sequence >` 该值 |

示例：

```http
GET /api/v1/groups/10001/messages?beforeSequence=100201&limit=50
GET /api/v1/groups/10001/messages?afterSequence=100201&limit=100
```

### 响应（data: PageDTO\<MessageDTO>）

```json
{
  "errNo": 0,
  "errMsg": "succ",
  "traceId": "trace-7f3a1c2b",
  "data": {
    "items": [
      {
        "id": 1,
        "messageId": "msg_100000001",
        "groupId": 10001,
        "sequence": 100201,
        "senderId": 1001,
        "senderName": "张三",
        "clientMessageId": "client_msg_001",
        "messageType": "text",
        "content": "今天讨论 HTTP API 设计",
        "mentionAll": false,
        "mentionUserIds": [],
        "extra": {},
        "status": "normal",
        "createdAt": "2026-06-28T11:30:00Z",
        "updatedAt": "2026-06-28T11:30:00Z"
      }
    ],
    "nextCursor": "100180",
    "hasMore": true
  }
}
```

------

## 7.2 撤回消息

```http
POST /api/v1/groups/{groupId}/messages/{messageId}/recall
```

权限：发送者（时间窗口内）或群主/管理员（失败 `RECALL_FAILED`，HTTP 403）。撤回不占用 sequence，通过 WS 事件 `group_message_recalled` 更新客户端。

### 请求体（RecallMessageRequest）

```json
{ "reason": "发送错误" }
```

### 响应（data: RecallEventDTO）

```json
{
  "errNo": 0,
  "errMsg": "succ",
  "traceId": "trace-7f3a1c2b",
  "data": {
    "groupId": 10001,
    "messageId": "msg_100000001",
    "sequence": 100201,
    "operatorId": 1001,
    "senderId": 1001,
    "reason": "发送错误",
    "recalledAt": "2026-06-28T11:40:00Z"
  }
}
```

------

# 8. 已读与 @ 提醒接口

## 8.1 上报已读位置

```http
POST /api/v1/groups/{groupId}/read
```

失败 `READ_FAILED`（HTTP 500）。`lastReadSequence` 单调递增，不回退。

### 请求体（ReadRequest）

```json
{ "lastReadSequence": 100201 }
```

### 响应

```json
{ "errNo": 0, "errMsg": "succ", "traceId": "trace-7f3a1c2b", "data": { "lastReadSequence": 100201 } }
```

------

## 8.2 查询 @ 提醒列表

```http
GET /api/v1/groups/{groupId}/mentions
```

仅群成员可访问。

### 查询参数

| 参数       | 类型    | 必填 | 说明                          |
| ---------- | ------- | ---- | ----------------------------- |
| cursor     | number  | 否   | 游标                          |
| limit      | number  | 否   | 每页数量，默认 30             |
| unreadOnly | boolean | 否   | 默认 `true`；传 `false` 返回全部 |

### 响应（data: PageDTO\<MentionDTO>）

```json
{
  "errNo": 0,
  "errMsg": "succ",
  "traceId": "trace-7f3a1c2b",
  "data": {
    "items": [
      {
        "mentionId": 7001,
        "groupId": 10001,
        "messageId": "msg_100000001",
        "sequence": 100201,
        "userId": 1002,
        "mentionType": "user",
        "readStatus": false,
        "content": "今天讨论 HTTP API 设计",
        "senderName": "张三",
        "createdAt": "2026-06-28T12:40:00Z",
        "updatedAt": "2026-06-28T12:40:00Z"
      }
    ],
    "nextCursor": "7001",
    "hasMore": false
  }
}
```

------

## 8.3 标记 @ 提醒已读

```http
POST /api/v1/groups/{groupId}/mentions/read
```

失败 `MENTION_READ_FAILED`（HTTP 500）。标记 `sequence` 及之前的 @ 提醒为已读。

### 请求体（ReadMentionsRequest）

```json
{ "sequence": 100201 }
```

### 响应

```json
{ "errNo": 0, "errMsg": "succ", "traceId": "trace-7f3a1c2b", "data": { "read": true } }
```

------

# 9. 群公告接口

## 9.1 查询群公告列表

```http
GET /api/v1/groups/{groupId}/announcements
```

仅群成员可访问。

### 查询参数

| 参数   | 类型   | 必填 | 说明              |
| ------ | ------ | ---- | ----------------- |
| cursor | number | 否   | 游标              |
| limit  | number | 否   | 每页数量，默认 20 |

### 响应（data: PageDTO\<AnnouncementDTO>）

```json
{
  "errNo": 0,
  "errMsg": "succ",
  "traceId": "trace-7f3a1c2b",
  "data": {
    "items": [
      {
        "announcementId": 9001,
        "groupId": 10001,
        "operatorId": 1001,
        "operatorName": "张三",
        "title": "今日讨论主题",
        "content": "今天讨论大群投递和 Kafka Topic 设计",
        "pinned": true,
        "status": "normal",
        "createdAt": "2026-06-28T11:50:00Z",
        "updatedAt": "2026-06-28T11:50:00Z"
      }
    ],
    "nextCursor": "9001",
    "hasMore": false
  }
}
```

------

## 9.2 发布群公告

```http
POST /api/v1/groups/{groupId}/announcements
```

权限：群主或管理员（失败 `ANNOUNCEMENT_CREATE_FAILED`，HTTP 403）。会写系统消息。

### 请求体（CreateAnnouncementRequest）

```json
{ "title": "今日讨论主题", "content": "今天讨论大群投递和 Kafka Topic 设计", "pinned": true }
```

| 字段    | 类型    | 必填 | 说明     |
| ------- | ------- | ---- | -------- |
| title   | string  | 否   | 公告标题 |
| content | string  | 是   | 公告正文 |
| pinned  | boolean | 否   | 是否置顶 |

### 响应（data: AnnouncementDTO）

```json
{
  "errNo": 0,
  "errMsg": "succ",
  "traceId": "trace-7f3a1c2b",
  "data": {
    "announcementId": 9001, "groupId": 10001, "operatorId": 1001, "operatorName": "张三",
    "title": "今日讨论主题", "content": "今天讨论大群投递和 Kafka Topic 设计",
    "pinned": true, "status": "normal",
    "createdAt": "2026-06-28T11:50:00Z", "updatedAt": "2026-06-28T11:50:00Z"
  }
}
```

------

## 9.3 编辑群公告

```http
PUT /api/v1/groups/{groupId}/announcements/{announcementId}
```

权限：群主或管理员（失败 `ANNOUNCEMENT_UPDATE_FAILED`，HTTP 403）。

### 请求体（UpdateAnnouncementRequest，pinned 为 null 表示不修改置顶）

```json
{ "title": "更新后的标题", "content": "更新后的内容", "pinned": true }
```

### 响应（data: AnnouncementDTO）

```json
{
  "errNo": 0, "errMsg": "succ", "traceId": "trace-7f3a1c2b",
  "data": {
    "announcementId": 9001, "groupId": 10001, "operatorId": 1001, "operatorName": "张三",
    "title": "更新后的标题", "content": "更新后的内容", "pinned": true, "status": "normal",
    "createdAt": "2026-06-28T11:50:00Z", "updatedAt": "2026-06-28T12:00:00Z"
  }
}
```

------

## 9.4 删除群公告

```http
DELETE /api/v1/groups/{groupId}/announcements/{announcementId}
```

权限：群主或管理员（失败 `ANNOUNCEMENT_DELETE_FAILED`，HTTP 403）。

### 响应

```json
{ "errNo": 0, "errMsg": "succ", "traceId": "trace-7f3a1c2b", "data": { "deleted": true } }
```

------

# 10. 内部服务接口

## 10.1 WS 推送回调

```http
POST /internal/push
```

供 Delivery 服务回推 WS 消息使用，**不对外部客户端开放**（无 `/api/v1` 前缀，无鉴权中间件）。通过请求头 `X-Trace-Id` 透传链路。

### 请求体（InternalPushRequest）

```json
{
  "userIds": [1001, 1002],
  "type": "group_message_receive",
  "data": { "messageId": "msg_100000001", "groupId": 10001, "content": "..." }
}
```

| 字段    | 类型   | 说明                                       |
| ------- | ------ | ------------------------------------------ |
| userIds | array  | 目标用户 ID 列表                            |
| type    | string | WS 事件类型，缺省 `group_message_receive` |
| data    | object | 透传业务负载，原样下发                      |

### 响应

```json
{ "errNo": 0, "errMsg": "succ", "traceId": "trace-7f3a1c2b", "data": { "pushed": 2 } }
```

> `pushed` 为成功下发的连接数。

------

# 11. 系统与运维接口

| 接口                | 方法 | 鉴权 | 说明                                  |
| ------------------- | ---- | ---- | ------------------------------------- |
| `/api/v1/health`    | GET  | 否   | 健康检查，返回 `{ status, time }`     |
| `/metrics`          | GET  | 否   | Prometheus 指标                       |
| `/swagger/*any`     | GET  | 否   | Swagger UI                            |
| `/ws`、`/api/ws`    | GET  | token 查询参数 | WebSocket 握手，鉴权失败返回 `AUTH_TOKEN_INVALID` |

健康检查响应：

```json
{ "errNo": 0, "errMsg": "succ", "traceId": "trace-7f3a1c2b", "data": { "status": "ok", "time": "2026-06-28T10:00:00Z" } }
```

------

# 12. 错误码（errNo 映射）

`errNo = 0` 成功；字符串码经 `errNoMap` 映射为整数；未登记的字符串码统一为 `-1`。

| 分组          | 字符串码                   | errNo | 典型 HTTP | 说明           |
| ------------- | -------------------------- | ----- | --------- | -------------- |
| 成功          | OK                         | 0     | 200       | 成功           |
| 通用 / 鉴权   | BAD_REQUEST                | 10001 | 400       | 请求参数错误    |
|               | AUTH_FAILED                | 10002 | 400/401   | 登录失败        |
|               | AUTH_TOKEN_INVALID         | 10003 | 401       | token 无效（WS）|
|               | FORBIDDEN                  | 10004 | 403       | 无权限          |
|               | USER_NOT_FOUND             | 10005 | 404       | 用户不存在      |
|               | INTERNAL_ERROR             | 10006 | 500       | 服务内部错误    |
| 群 (200xx)    | CREATE_GROUP_FAILED        | 20001 | 400       | 创建群失败      |
|               | GROUP_NOT_FOUND            | 20002 | 404       | 群不存在        |
|               | JOIN_GROUP_FAILED          | 20003 | 400       | 加群失败        |
|               | LEAVE_GROUP_FAILED         | 20004 | 400       | 退群失败        |
|               | DISMISS_FAILED             | 20005 | 403       | 解散失败        |
|               | UPDATE_SETTINGS_FAILED     | 20006 | 403       | 修改群设置失败  |
| 成员 (210xx)  | SET_ROLE_FAILED            | 21001 | 403       | 设置角色失败    |
|               | KICK_FAILED                | 21002 | 403       | 踢人失败        |
|               | MUTE_FAILED                | 21003 | 403       | 禁言失败        |
|               | UNMUTE_FAILED              | 21004 | 403       | 解除禁言失败    |
| 消息 (220xx)  | RECALL_FAILED              | 22001 | 403       | 撤回失败        |
|               | READ_FAILED                | 22002 | 500       | 已读上报失败    |
|               | MENTION_READ_FAILED        | 22003 | 500       | @ 已读失败      |
| 公告 (230xx)  | ANNOUNCEMENT_CREATE_FAILED | 23001 | 403       | 发布公告失败    |
|               | ANNOUNCEMENT_UPDATE_FAILED | 23002 | 403       | 编辑公告失败    |
|               | ANNOUNCEMENT_DELETE_FAILED | 23003 | 403       | 删除公告失败    |
| 审批 (240xx)  | APPROVE_JOIN_FAILED        | 24001 | 403       | 通过审批失败    |
|               | REJECT_JOIN_FAILED         | 24002 | 403       | 拒绝审批失败    |

> WS 消息发送链路另有业务码（`GROUP_MUTED` / `SLOW_MODE_LIMITED` / `FORBIDDEN` / `MESSAGE_SEND_FAILED`），在 WS 帧内返回，详见 WebSocket 协议文档。

------

# 13. 权限矩阵

| 操作                         | 群主               | 管理员 | 普通成员 |
| ---------------------------- | ------------------ | ------ | -------- |
| 创建群                       | 是                 | 是     | 是       |
| 修改群设置（含全员禁言/慢速）| 是                 | 是     | 否       |
| 解散群                       | 是                 | 否     | 否       |
| 查询群详情 / 成员 / 消息     | 是                 | 是     | 是       |
| 设置 / 取消管理员            | 是                 | 否     | 否       |
| 踢人                         | 是                 | 是     | 否       |
| 单人禁言 / 解除禁言          | 是                 | 是     | 否       |
| 退出群                       | 否（需先转让/解散）| 是     | 是       |
| 查询 / 审批加群申请          | 是                 | 是     | 否       |
| 发布 / 编辑 / 删除公告       | 是                 | 是     | 否       |
| 撤回自己消息                 | 是                 | 是     | 是       |
| 撤回他人消息                 | 是                 | 是     | 否       |

------

# 14. 接口清单与实现状态

| 模块   | 方法 + 路径                                                 | Handler             |
| ------ | ---------------------------------------------------------- | ------------------- |
| 认证   | POST `/api/v1/auth/login`                                  | login               |
| 认证   | GET `/api/v1/auth/me`                                      | me                  |
| 群     | POST `/api/v1/groups`                                      | createGroup         |
| 群     | GET `/api/v1/groups`                                       | listGroups          |
| 群     | GET `/api/v1/groups/{groupId}`                             | groupDetail         |
| 群     | PATCH `/api/v1/groups/{groupId}/settings`                 | updateSettings      |
| 群     | DELETE `/api/v1/groups/{groupId}`                         | dismissGroup        |
| 成员   | GET `/api/v1/groups/{groupId}/members`                    | members             |
| 成员   | POST `/api/v1/groups/{groupId}/members/{userId}/role`     | setRole             |
| 成员   | DELETE `/api/v1/groups/{groupId}/members/{userId}`        | kickMember          |
| 成员   | POST `/api/v1/groups/{groupId}/members/{userId}/mute`     | muteMember          |
| 成员   | DELETE `/api/v1/groups/{groupId}/members/{userId}/mute`   | unmuteMember        |
| 成员   | POST `/api/v1/groups/{groupId}/leave`                     | leaveGroup          |
| 加群   | POST `/api/v1/groups/{groupId}/join`                      | joinGroup           |
| 审批   | GET `/api/v1/groups/{groupId}/join-requests`             | joinRequests        |
| 审批   | POST `/api/v1/groups/{groupId}/join-requests/{requestId}/approve` | approveJoinRequest |
| 审批   | POST `/api/v1/groups/{groupId}/join-requests/{requestId}/reject`  | rejectJoinRequest  |
| 消息   | GET `/api/v1/groups/{groupId}/messages`                  | messages            |
| 消息   | POST `/api/v1/groups/{groupId}/messages/{messageId}/recall` | recallMessage      |
| 已读   | POST `/api/v1/groups/{groupId}/read`                     | read                |
| 提醒   | GET `/api/v1/groups/{groupId}/mentions`                  | mentions            |
| 提醒   | POST `/api/v1/groups/{groupId}/mentions/read`           | readMentions        |
| 公告   | GET `/api/v1/groups/{groupId}/announcements`            | announcements       |
| 公告   | POST `/api/v1/groups/{groupId}/announcements`           | createAnnouncement  |
| 公告   | PUT `/api/v1/groups/{groupId}/announcements/{announcementId}` | updateAnnouncement |
| 公告   | DELETE `/api/v1/groups/{groupId}/announcements/{announcementId}` | deleteAnnouncement |
| 内部   | POST `/internal/push`                                    | internalPush        |
| 运维   | GET `/api/v1/health` / `/metrics` / `/swagger/*any` / `/ws` | -                |

------

# 15. 规划中（尚未实现）

以下接口为后续规划，当前代码未实现，前端不应依赖：

1. 修改群基础信息（名称/简介/头像）独立接口（当前仅 `PATCH /settings`）。
2. 查询我的成员身份 `GET /members/me`、查询群未读 `GET /unread`（信息已包含在群详情/群列表）。
3. 单条消息详情 `GET /messages/{messageId}`、已读人数 `GET /messages/{messageId}/read-count`。
4. 最新置顶公告 `GET /announcements/latest`。
5. 消息搜索、文件上传、群统计、群操作日志查询、本地删除消息。

------

# 16. 总结

1. 统一响应 `errNo / errMsg / traceId / data`，`errNo=0` 成功。
2. 响应数据由 API 层 DTO 下发，与领域模型解耦。
3. 群消息发送走 WebSocket，历史消息/补拉走 HTTP。
4. 列表统一游标分页：消息用 `sequence`，其余用记录自增 `id`。
5. 未读通过 `maxSequence - lastReadSequence` 计算，大群不维护精确未读。
6. 管理类操作严格校验群角色权限。
7. 撤回经 HTTP 发起，通过 WS（及 Kafka 直投模式）推送 `group_message_recalled`。
8. 全员禁言、慢速、群类型、人数上限统一走 `PATCH /groups/{groupId}/settings`。
9. 加群审批拆分为 approve / reject 两个独立接口。
10. 全链路 traceId 贯穿 HTTP、WS 与各数据层。
