# WebSocket 协议详细设计文档

## 1. 文档说明

### 1.1 文档目的

本文档用于定义 GroupFlow 群聊系统的 WebSocket 通信协议，包括连接建立、鉴权、心跳、群消息发送、服务端 ACK、群消息推送、消息撤回、已读上报、断线重连、错误码和客户端处理策略。

GroupFlow 只关注群聊，不设计单聊。

建议配合数据库设计一起看：**数据库保证消息存储与顺序，WebSocket 协议保证实时通信与客户端状态流转**

### 1.2 协议目标

WebSocket 协议需要满足以下目标：

1. 支持群消息实时发送。
2. 支持服务端确认 ACK。
3. 支持客户端消息幂等。
4. 支持服务端群消息广播。
5. 支持消息顺序控制。
6. 支持断线重连。
7. 支持消息补拉。
8. 支持心跳保活。
9. 支持大群异步投递。
10. 支持错误码标准化。
11. 支持前后端统一消息结构。
12. 支持后续扩展图片、文件、引用消息等类型。

------

## 2. 协议设计原则

### 2.1 统一消息结构

客户端和服务端所有 WebSocket 消息都使用统一 JSON 格式。

### 2.2 所有客户端请求必须携带 requestId

客户端主动发送的业务请求必须携带 `requestId`，用于匹配服务端响应。

### 2.3 所有用户发送消息必须携带 clientMessageId

用户发送群消息时必须携带 `clientMessageId`，用于解决网络重试导致的重复消息问题。

### 2.4 服务端 ACK 只代表消息已被服务端成功处理

对于群消息发送，服务端 ACK 的语义是：

```text
消息已经通过校验，并成功持久化到数据库。
```

ACK 不代表所有群成员都已经收到消息。

### 2.5 消息顺序以 sequence 为准

群消息排序必须使用：

```text
groupId + sequence
```

不能依赖客户端时间，也不能依赖服务端 `createdAt` 排序。

### 2.6 在线实时推送，离线历史补拉

在线用户通过 WebSocket 实时接收消息。

离线用户不做实时投递，重新上线后通过历史消息接口补拉。

------

## 3. 连接地址设计

### 3.1 WebSocket 地址

开发环境：

```text
ws://localhost:8080/ws?token={token}&deviceId={deviceId}
```

生产环境：

```text
wss://groupflow.example.com/ws?token={token}&deviceId={deviceId}
```

### 3.2 参数说明

| 参数            | 必填 | 说明                               |
| --------------- | ---- | ---------------------------------- |
| token           | 是   | 用户登录后获取的访问令牌           |
| deviceId        | 否   | 设备 ID，用于区分多端连接          |
| clientType      | 否   | 客户端类型，例如 web、ios、android |
| protocolVersion | 否   | 协议版本，例如 v1                  |

示例：

```text
wss://groupflow.example.com/ws?token=abc123&deviceId=web_001&clientType=web&protocolVersion=v1
```

------

## 4. 连接建立流程

### 4.1 流程说明

```text
客户端发起 WebSocket 连接
  ↓
服务端解析 token
  ↓
服务端校验用户身份
  ↓
服务端生成 connectionId
  ↓
服务端将连接注册到本机 Hub
  ↓
服务端将在线状态写入 Redis
  ↓
服务端返回 connected 事件
```

### 4.2 连接成功事件

服务端发送：

```json
{
  "type": "connection_connected",
  "requestId": "",
  "timestamp": 1710000000000,
  "data": {
    "connectionId": "conn_100001",
    "userId": 1001,
    "serverId": "ws-server-01",
    "heartbeatIntervalSeconds": 20,
    "heartbeatTimeoutSeconds": 60,
    "protocolVersion": "v1"
  }
}
```

### 4.3 连接失败

如果 token 无效，服务端拒绝连接。

错误响应可以在关闭前发送：

```json
{
  "type": "error",
  "requestId": "",
  "timestamp": 1710000000000,
  "data": {
    "code": "AUTH_TOKEN_INVALID",
    "message": "token 无效",
    "retryable": false
  }
}
```

然后关闭连接。

------

## 5. 通用消息结构

### 5.1 基础结构

```json
{
  "type": "事件类型",
  "requestId": "请求ID",
  "timestamp": 1710000000000,
  "data": {}
}
```

### 5.2 字段说明

| 字段      | 类型   | 必填 | 说明                        |
| --------- | ------ | ---- | --------------------------- |
| type      | string | 是   | 消息类型                    |
| requestId | string | 否   | 请求 ID，用于匹配请求和响应 |
| timestamp | number | 是   | 毫秒时间戳                  |
| data      | object | 是   | 业务数据                    |

### 5.3 requestId 规则

客户端主动请求必须携带 `requestId`。

例如：

```text
req_1710000000000_abc001
```

服务端响应必须原样返回该 `requestId`。

服务端主动推送事件可以不携带 `requestId`，或传空字符串。

------

## 6. 消息类型总览

### 6.1 连接类

| type                 | 方向             | 说明               |
| -------------------- | ---------------- | ------------------ |
| connection_connected | 服务端 -> 客户端 | 连接成功           |
| connection_kicked    | 服务端 -> 客户端 | 当前连接被踢下线   |
| connection_closed    | 服务端 -> 客户端 | 服务端主动关闭连接 |

### 6.2 心跳类

| type | 方向             | 说明     |
| ---- | ---------------- | -------- |
| ping | 客户端 -> 服务端 | 心跳请求 |
| pong | 服务端 -> 客户端 | 心跳响应 |

### 6.3 群消息类

| type                   | 方向             | 说明         |
| ---------------------- | ---------------- | ------------ |
| group_message_send     | 客户端 -> 服务端 | 发送群消息   |
| group_message_ack      | 服务端 -> 客户端 | 发送消息确认 |
| group_message_receive  | 服务端 -> 客户端 | 接收群消息   |
| group_message_recalled | 服务端 -> 客户端 | 消息被撤回   |
| group_message_failed   | 服务端 -> 客户端 | 消息发送失败 |

### 6.4 已读类

| type                   | 方向             | 说明         |
| ---------------------- | ---------------- | ------------ |
| group_message_read     | 客户端 -> 服务端 | 上报已读位置 |
| group_message_read_ack | 服务端 -> 客户端 | 已读上报确认 |

### 6.5 群事件类

| type                       | 方向             | 说明           |
| -------------------------- | ---------------- | -------------- |
| group_member_joined        | 服务端 -> 客户端 | 成员加入群     |
| group_member_left          | 服务端 -> 客户端 | 成员退出群     |
| group_member_kicked        | 服务端 -> 客户端 | 成员被踢       |
| group_dismissed            | 服务端 -> 客户端 | 群被解散       |
| group_muted                | 服务端 -> 客户端 | 群开启全员禁言 |
| group_unmuted              | 服务端 -> 客户端 | 群关闭全员禁言 |
| group_announcement_updated | 服务端 -> 客户端 | 群公告更新     |

### 6.6 错误类

| type  | 方向             | 说明         |
| ----- | ---------------- | ------------ |
| error | 服务端 -> 客户端 | 通用错误响应 |

------

## 7. 心跳协议

### 7.1 心跳规则

客户端需要定时发送心跳。

推荐配置：

```text
客户端每 20 秒发送一次 ping。
服务端 60 秒内没有收到 ping，则关闭连接。
```

### 7.2 客户端 ping

```json
{
  "type": "ping",
  "requestId": "req_ping_001",
  "timestamp": 1710000000000,
  "data": {}
}
```

### 7.3 服务端 pong

```json
{
  "type": "pong",
  "requestId": "req_ping_001",
  "timestamp": 1710000000100,
  "data": {
    "serverTime": 1710000000100
  }
}
```

### 7.4 客户端处理策略

客户端需要记录最近一次收到 `pong` 的时间。

如果连续多次未收到 `pong`，客户端应认为连接不可用，并执行重连。

------

## 8. 群消息发送协议

## 8.1 客户端发送群消息

客户端发送：

```json
{
  "type": "group_message_send",
  "requestId": "req_100001",
  "timestamp": 1710000000000,
  "data": {
    "groupId": 10001,
    "clientMessageId": "client_msg_abc001",
    "messageType": "text",
    "content": "大家好，今天讨论大群广播设计。",
    "mentionAll": false,
    "mentionUserIds": [1002, 1003],
    "extra": {}
  }
}
```

### 8.2 字段说明

| 字段            | 类型     | 必填 | 说明           |
| --------------- | -------- | ---- | -------------- |
| groupId         | number   | 是   | 群 ID          |
| clientMessageId | string   | 是   | 客户端消息 ID  |
| messageType     | string   | 是   | 消息类型       |
| content         | string   | 是   | 消息内容       |
| mentionAll      | boolean  | 否   | 是否 @所有人   |
| mentionUserIds  | number[] | 否   | 被 @ 的用户 ID |
| extra           | object   | 否   | 扩展字段       |

### 8.3 clientMessageId 生成规则

客户端每次新建消息时生成一个 `clientMessageId`。

推荐格式：

```text
client_msg_{timestamp}_{random}
```

例如：

```text
client_msg_1710000000000_a8f3
```

注意：

1. 同一条消息重试时必须复用同一个 `clientMessageId`。
2. 不同消息必须使用不同的 `clientMessageId`。
3. 服务端使用 `senderId + clientMessageId` 做唯一约束。

------

## 9. 群消息 ACK 协议

### 9.1 发送成功 ACK

服务端发送：

```json
{
  "type": "group_message_ack",
  "requestId": "req_100001",
  "timestamp": 1710000000100,
  "data": {
    "groupId": 10001,
    "clientMessageId": "client_msg_abc001",
    "messageId": "msg_100000001",
    "sequence": 100201,
    "messageType": "text",
    "status": "success",
    "createdAt": "2026-06-28T10:00:00.000Z"
  }
}
```

### 9.2 ACK 语义

收到 `group_message_ack` 表示：

```text
1. 服务端已经收到消息。
2. 服务端已经完成权限校验。
3. 服务端已经生成 messageId。
4. 服务端已经生成 group sequence。
5. 服务端已经将消息写入 MySQL。
```

不表示：

```text
1. 所有群成员已经收到消息。
2. 所有群成员已经阅读消息。
3. 消息已经推送到所有 WebSocket 节点。
```

### 9.3 客户端收到 ACK 后处理

客户端需要：

```text
1. 根据 clientMessageId 找到本地 sending 消息。
2. 用 messageId、sequence、createdAt 更新本地消息。
3. 将消息状态从 sending 改为 success。
4. 清除 ACK 超时定时器。
```

------

## 10. 群消息发送失败协议

### 10.1 发送失败响应

服务端发送：

```json
{
  "type": "group_message_failed",
  "requestId": "req_100001",
  "timestamp": 1710000000100,
  "data": {
    "groupId": 10001,
    "clientMessageId": "client_msg_abc001",
    "code": "GROUP_MEMBER_MUTED",
    "message": "你当前处于禁言状态，无法发送消息",
    "retryable": false
  }
}
```

### 10.2 失败字段说明

| 字段            | 类型    | 说明          |
| --------------- | ------- | ------------- |
| groupId         | number  | 群 ID         |
| clientMessageId | string  | 客户端消息 ID |
| code            | string  | 错误码        |
| message         | string  | 错误描述      |
| retryable       | boolean | 是否可以重试  |

### 10.3 客户端处理策略

如果 `retryable = true`：

```text
消息状态改为 failed。
用户可以点击重试。
重试时继续使用相同 clientMessageId。
```

如果 `retryable = false`：

```text
消息状态改为 failed。
展示服务端错误原因。
不建议自动重试。
```

------

## 11. 服务端群消息推送协议

### 11.1 服务端推送群消息

服务端主动推送：

```json
{
  "type": "group_message_receive",
  "requestId": "",
  "timestamp": 1710000000200,
  "data": {
    "groupId": 10001,
    "messageId": "msg_100000001",
    "senderId": 1001,
    "senderName": "张三",
    "senderAvatar": "https://example.com/avatar.png",
    "messageType": "text",
    "content": "大家好，今天讨论大群广播设计。",
    "sequence": 100201,
    "status": "normal",
    "mentionAll": false,
    "mentionUserIds": [1002, 1003],
    "createdAt": "2026-06-28T10:00:00.000Z",
    "extra": {}
  }
}
```

### 11.2 客户端处理流程

收到 `group_message_receive` 后：

```text
1. 判断 groupId 是否属于当前用户的群。
2. 判断 messageId 是否已经存在，避免重复插入。
3. 根据 sequence 插入到正确位置。
4. 如果当前正在查看该群，展示消息。
5. 如果当前不在该群，更新群列表最后一条消息和未读数。
6. 如果 mentionUserIds 包含当前用户，展示“有人@我”。
7. 如果 mentionAll = true，展示“@所有人”。
```

### 11.3 发送者是否会收到 receive

推荐发送者也收到 `group_message_receive`。

原因：

1. 保持多端一致。
2. 同一个用户多个设备可以收到消息。
3. 发送端当前设备可以通过 ACK 更新消息，其他设备通过 receive 插入消息。

当前发送设备处理时需要去重：

```text
如果本地已存在相同 clientMessageId 或 messageId，则更新，不重复插入。
```

------

## 12. 消息顺序与缺口检测

### 12.1 顺序字段

服务端为每个群内消息生成递增 `sequence`。

客户端必须按照：

```text
groupId + sequence
```

排序消息。

### 12.2 客户端维护 lastReceivedSequence

每个群维护：

```text
lastReceivedSequence
```

表示客户端当前已收到的最大连续消息序号。

### 12.3 缺口检测

如果客户端当前 `lastReceivedSequence = 1002`，收到新消息 `sequence = 1005`，说明可能缺少：

```text
1003
1004
```

客户端需要触发补拉。

### 12.4 注意：sequence 可能跳号

由于 Redis sequence 生成后，MySQL 写入可能失败，所以 sequence 可能出现跳号。

因此客户端发现缺口后应调用补拉接口确认。

如果补拉不到缺失消息，客户端应接受 sequence 跳号，不应无限重试。

------

## 13. 消息补拉协议

消息补拉建议使用 HTTP 接口，而不是 WebSocket。

### 13.1 补拉遗漏消息

```text
GET /api/groups/{groupId}/messages?afterSequence=1002&limit=100
```

响应：

```json
{
  "groupId": 10001,
  "messages": [
    {
      "messageId": "msg_100000003",
      "groupId": 10001,
      "senderId": 1003,
      "messageType": "text",
      "content": "补拉消息内容",
      "sequence": 1003,
      "status": "normal",
      "createdAt": "2026-06-28T10:00:01.000Z"
    }
  ],
  "hasMore": false,
  "maxSequence": 1005
}
```

### 13.2 客户端补拉策略

```text
1. 发现 sequence 缺口。
2. 调用 afterSequence 补拉接口。
3. 将返回消息按 sequence 合并。
4. 如果仍然缺失，允许 sequence 跳号。
5. 更新 lastReceivedSequence。
```

------

## 14. 已读上报协议

### 14.1 客户端上报已读

客户端发送：

```json
{
  "type": "group_message_read",
  "requestId": "req_read_001",
  "timestamp": 1710000000300,
  "data": {
    "groupId": 10001,
    "lastReadSequence": 100201
  }
}
```

### 14.2 字段说明

| 字段             | 类型   | 必填 | 说明                     |
| ---------------- | ------ | ---- | ------------------------ |
| groupId          | number | 是   | 群 ID                    |
| lastReadSequence | number | 是   | 当前用户最后已读消息序号 |

### 14.3 服务端确认

```json
{
  "type": "group_message_read_ack",
  "requestId": "req_read_001",
  "timestamp": 1710000000350,
  "data": {
    "groupId": 10001,
    "lastReadSequence": 100201,
    "status": "success"
  }
}
```

### 14.4 服务端处理规则

服务端需要：

```text
1. 校验用户是该群成员。
2. 校验群未被封禁。
3. 校验 lastReadSequence 大于当前已读位置。
4. 更新 group_member.last_read_sequence。
5. 更新 Redis 缓存。
```

注意：

```text
lastReadSequence 只能变大，不能变小。
```

### 14.5 客户端上报频率

客户端不要每读一条消息都上报。

推荐策略：

```text
1. 用户进入群聊时上报一次。
2. 用户滚动到底部时上报一次。
3. 收到新消息并停留在当前群窗口时，延迟合并上报。
4. 页面切换或关闭前上报一次。
```

------

## 15. 消息撤回协议

### 15.1 撤回请求

撤回消息可以走 HTTP，也可以走 WebSocket。

推荐初期使用 HTTP：

```text
POST /api/groups/{groupId}/messages/{messageId}/recall
```

服务端撤回成功后，通过 WebSocket 广播撤回事件。

### 15.2 服务端推送撤回事件

```json
{
  "type": "group_message_recalled",
  "requestId": "",
  "timestamp": 1710000000400,
  "data": {
    "groupId": 10001,
    "messageId": "msg_100000001",
    "sequence": 100201,
    "operatorId": 1001,
    "senderId": 1001,
    "recalledAt": "2026-06-28T10:02:00.000Z"
  }
}
```

### 15.3 客户端处理规则

客户端收到撤回事件后：

```text
1. 根据 messageId 查找本地消息。
2. 将消息 status 改为 recalled。
3. 隐藏原始 content。
4. 展示“某某撤回了一条消息”。
```

------

## 16. 群事件推送协议

## 16.1 成员加入群

```json
{
  "type": "group_member_joined",
  "requestId": "",
  "timestamp": 1710000000500,
  "data": {
    "groupId": 10001,
    "userId": 1005,
    "nickname": "李四",
    "joinedAt": "2026-06-28T10:03:00.000Z"
  }
}
```

## 16.2 成员退出群

```json
{
  "type": "group_member_left",
  "requestId": "",
  "timestamp": 1710000000600,
  "data": {
    "groupId": 10001,
    "userId": 1005,
    "leftAt": "2026-06-28T10:05:00.000Z"
  }
}
```

## 16.3 成员被踢

```json
{
  "type": "group_member_kicked",
  "requestId": "",
  "timestamp": 1710000000700,
  "data": {
    "groupId": 10001,
    "userId": 1005,
    "operatorId": 1001,
    "kickedAt": "2026-06-28T10:06:00.000Z"
  }
}
```

如果当前用户是被踢用户，客户端需要：

```text
1. 禁用输入框。
2. 停止接收该群新消息。
3. 从群列表移除或展示“你已被移出该群”。
```

## 16.4 群解散

```json
{
  "type": "group_dismissed",
  "requestId": "",
  "timestamp": 1710000000800,
  "data": {
    "groupId": 10001,
    "operatorId": 1001,
    "dismissedAt": "2026-06-28T10:10:00.000Z"
  }
}
```

客户端处理：

```text
1. 禁用输入框。
2. 展示“该群已解散”。
3. 停止发送该群消息。
4. 历史消息仍可查看。
```

## 16.5 群公告更新

```json
{
  "type": "group_announcement_updated",
  "requestId": "",
  "timestamp": 1710000000900,
  "data": {
    "groupId": 10001,
    "announcementId": 9001,
    "title": "今日讨论主题",
    "content": "今天讨论大群广播和 WebSocket 分片推送。",
    "operatorId": 1001,
    "updatedAt": "2026-06-28T10:12:00.000Z"
  }
}
```

------

## 17. 错误协议

### 17.1 通用错误结构

```json
{
  "type": "error",
  "requestId": "req_100001",
  "timestamp": 1710000000000,
  "data": {
    "code": "GROUP_NOT_FOUND",
    "message": "群不存在",
    "retryable": false,
    "detail": {}
  }
}
```

### 17.2 字段说明

| 字段      | 类型    | 说明       |
| --------- | ------- | ---------- |
| code      | string  | 错误码     |
| message   | string  | 错误描述   |
| retryable | boolean | 是否可重试 |
| detail    | object  | 错误详情   |

------

## 18. 错误码设计

### 18.1 连接类错误码

| 错误码                    | 说明           | 是否可重试 |
| ------------------------- | -------------- | ---------- |
| AUTH_TOKEN_MISSING        | token 缺失     | 否         |
| AUTH_TOKEN_INVALID        | token 无效     | 否         |
| AUTH_TOKEN_EXPIRED        | token 过期     | 否         |
| CONNECTION_LIMIT_EXCEEDED | 连接数超过限制 | 是         |
| SERVER_BUSY               | 服务繁忙       | 是         |

### 18.2 群消息类错误码

| 错误码                   | 说明           | 是否可重试 |
| ------------------------ | -------------- | ---------- |
| GROUP_NOT_FOUND          | 群不存在       | 否         |
| GROUP_DISMISSED          | 群已解散       | 否         |
| GROUP_BANNED             | 群被封禁       | 否         |
| GROUP_MEMBER_NOT_FOUND   | 用户不是群成员 | 否         |
| GROUP_MEMBER_KICKED      | 用户已被踢出   | 否         |
| GROUP_MEMBER_MUTED       | 用户被禁言     | 否         |
| GROUP_MUTE_ALL           | 群已全员禁言   | 否         |
| MESSAGE_CONTENT_EMPTY    | 消息内容为空   | 否         |
| MESSAGE_CONTENT_TOO_LONG | 消息内容过长   | 否         |
| MESSAGE_RATE_LIMITED     | 发送过于频繁   | 是         |
| MESSAGE_DUPLICATED       | 重复消息       | 否         |
| MESSAGE_INTERNAL_ERROR   | 服务端内部错误 | 是         |

### 18.3 已读类错误码

| 错误码                 | 说明               | 是否可重试 |
| ---------------------- | ------------------ | ---------- |
| READ_SEQUENCE_INVALID  | 已读 sequence 非法 | 否         |
| READ_SEQUENCE_ROLLBACK | 已读位置不能回退   | 否         |

------

## 19. 客户端重连设计

### 19.1 重连触发条件

客户端在以下情况需要重连：

```text
1. WebSocket 连接关闭。
2. 心跳超时。
3. 网络切换。
4. 服务端返回可重试连接错误。
```

### 19.2 重连策略

推荐使用指数退避：

```text
第 1 次：1 秒后重连
第 2 次：2 秒后重连
第 3 次：4 秒后重连
第 4 次：8 秒后重连
之后：最长 30 秒间隔
```

### 19.3 重连成功后处理

```text
1. 收到 connection_connected。
2. 恢复当前用户状态。
3. 对当前打开的群执行消息补拉。
4. 根据 lastReceivedSequence 拉取遗漏消息。
5. 重新上报当前群 lastReadSequence。
```

### 19.4 重连期间发送消息

如果 WebSocket 断开，客户端发送消息时：

```text
1. 可以先插入本地 sending 消息。
2. 等连接恢复后再发送。
3. 或直接提示“网络已断开，暂时无法发送”。
```

推荐初期使用第二种方式，逻辑更简单。

------

## 20. ACK 超时与重试设计

### 20.1 ACK 超时时间

推荐：

```text
5 秒
```

### 20.2 客户端发送后状态

```text
sending
```

如果 5 秒未收到 ACK：

```text
failed
```

### 20.3 用户点击重试

重试时必须复用原来的：

```text
clientMessageId
```

原因：

```text
如果原消息已经落库但 ACK 丢失，复用 clientMessageId 可以避免重复消息。
```

------

## 21. 多端连接设计

### 21.1 多端连接规则

一个用户允许多个设备同时在线。

例如：

```text
Web 端
手机端
平板端
```

### 21.2 推送规则

服务端推送消息时，可以推送到用户所有在线连接。

Redis 中可以维护：

```text
online:user:{userId}:connections
```

### 21.3 当前设备发送消息后的处理

当前设备发送消息后：

```text
1. 先通过 ACK 更新本地消息。
2. 如果之后收到同一条 group_message_receive，需要根据 messageId 或 clientMessageId 去重。
```

其他设备收到该消息时：

```text
直接插入消息列表。
```

------

## 22. 大群场景协议策略

### 22.1 大群推送原则

大群消息协议不需要特殊改变，但服务端投递策略不同。

普通群：

```text
可以直接查在线成员并推送。
```

大群：

```text
消息服务只负责 ACK。
投递服务消费 Kafka 后异步推送。
只推在线成员。
离线成员上线后补拉。
```

### 22.2 大群客户端策略

客户端需要：

```text
1. 使用虚拟列表。
2. 不一次性渲染大量消息。
3. 收到大量消息时可以批量合并更新。
4. 未读数超过 99 时展示 99+。
5. 不请求完整已读名单。
```

### 22.3 大群消息堆积处理

如果客户端短时间收到大量消息，可以：

```text
1. 批量入队。
2. 每 100ms 合并刷新一次 UI。
3. 当前不在群窗口时，只更新最后一条消息和未读数。
```

------

## 23. 服务端处理流程

## 23.1 group_message_send 处理流程

```text
收到 group_message_send
  ↓
解析 requestId 和 data
  ↓
校验连接用户身份
  ↓
校验 groupId
  ↓
校验用户是否群成员
  ↓
校验群状态
  ↓
校验禁言状态
  ↓
校验慢速模式
  ↓
检查 senderId + clientMessageId 是否已存在
  ↓
如果已存在，返回原消息 ACK
  ↓
Redis INCR 生成 sequence
  ↓
生成 messageId
  ↓
写入 group_message
  ↓
返回 group_message_ack
  ↓
写入 Kafka 投递事件
```

## 23.2 group_message_read 处理流程

```text
收到 group_message_read
  ↓
校验用户是否群成员
  ↓
查询当前 lastReadSequence
  ↓
如果新值更大，则更新
  ↓
返回 group_message_read_ack
```

------

## 24. 前端处理流程

## 24.1 前端初始化流程

```text
用户登录成功
  ↓
获取 token
  ↓
建立 WebSocket 连接
  ↓
收到 connection_connected
  ↓
开始定时 ping
  ↓
加载群列表
  ↓
进入群聊时加载历史消息
```

## 24.2 前端发送消息流程

```text
用户点击发送
  ↓
生成 clientMessageId
  ↓
本地插入 sending 消息
  ↓
发送 group_message_send
  ↓
启动 ACK 超时计时
  ↓
收到 group_message_ack
  ↓
更新本地消息为 success
```

## 24.3 前端接收消息流程

```text
收到 group_message_receive
  ↓
根据 messageId 判断是否重复
  ↓
检查 sequence 是否连续
  ↓
不连续则触发补拉
  ↓
插入消息列表
  ↓
更新群列表最后一条消息
  ↓
更新未读数或已读位置
```

------

## 25. TypeScript 协议类型定义

### 25.1 通用消息

```ts
export type WsMessage<T = unknown> = {
  type: string;
  requestId?: string;
  timestamp: number;
  data: T;
};
```

### 25.2 发送群消息

```ts
export type GroupMessageSendData = {
  groupId: number;
  clientMessageId: string;
  messageType: "text" | "system" | "image" | "file" | "quote";
  content: string;
  mentionAll?: boolean;
  mentionUserIds?: number[];
  extra?: Record<string, unknown>;
};
```

### 25.3 消息 ACK

```ts
export type GroupMessageAckData = {
  groupId: number;
  clientMessageId: string;
  messageId: string;
  sequence: number;
  messageType: string;
  status: "success";
  createdAt: string;
};
```

### 25.4 服务端推送消息

```ts
export type GroupMessageReceiveData = {
  groupId: number;
  messageId: string;
  senderId: number;
  senderName: string;
  senderAvatar?: string;
  messageType: "text" | "system" | "image" | "file" | "quote";
  content: string;
  sequence: number;
  status: "normal" | "recalled" | "deleted";
  mentionAll?: boolean;
  mentionUserIds?: number[];
  createdAt: string;
  extra?: Record<string, unknown>;
};
```

### 25.5 错误类型

```ts
export type WsErrorData = {
  code: string;
  message: string;
  retryable: boolean;
  detail?: Record<string, unknown>;
};
```

------

## 26. Go 协议结构定义

### 26.1 通用消息结构

```go
type WSMessage struct {
    Type      string          `json:"type"`
    RequestID string          `json:"requestId,omitempty"`
    Timestamp int64           `json:"timestamp"`
    Data      json.RawMessage `json:"data"`
}
```

### 26.2 群消息发送请求

```go
type GroupMessageSendData struct {
    GroupID         int64    `json:"groupId"`
    ClientMessageID string   `json:"clientMessageId"`
    MessageType     string   `json:"messageType"`
    Content         string   `json:"content"`
    MentionAll      bool     `json:"mentionAll"`
    MentionUserIDs  []int64  `json:"mentionUserIds"`
    Extra           map[string]interface{} `json:"extra"`
}
```

### 26.3 ACK 响应

```go
type GroupMessageAckData struct {
    GroupID         int64  `json:"groupId"`
    ClientMessageID string `json:"clientMessageId"`
    MessageID       string `json:"messageId"`
    Sequence        int64  `json:"sequence"`
    MessageType     string `json:"messageType"`
    Status          string `json:"status"`
    CreatedAt       string `json:"createdAt"`
}
```

### 26.4 错误响应

```go
type WSErrorData struct {
    Code      string                 `json:"code"`
    Message   string                 `json:"message"`
    Retryable bool                   `json:"retryable"`
    Detail    map[string]interface{} `json:"detail,omitempty"`
}
```

------

## 27. 日志要求

WebSocket 服务需要记录关键日志。

### 27.1 连接日志

字段：

```text
traceId
userId
connectionId
serverId
deviceId
event
timestamp
```

### 27.2 消息日志

字段：

```text
traceId
userId
groupId
messageId
clientMessageId
sequence
event
durationMs
timestamp
```

Go 日志示例：

```go
logger.Infof("group message ack success, groupId:%d, userId:%d, sequence:%d", groupID, userID, sequence)
```

注意日志输出使用格式化占位符，不使用字符串拼接。

------

## 28. 协议版本管理

### 28.1 当前版本

```text
v1
```

### 28.2 版本传递方式

客户端连接时传递：

```text
protocolVersion=v1
```

或在消息体中增加：

```json
{
  "type": "group_message_send",
  "version": "v1",
  "requestId": "req_100001",
  "timestamp": 1710000000000,
  "data": {}
}
```

初期推荐放在连接参数中。

### 28.3 向后兼容规则

1. 新增字段必须可选。
2. 不删除已有字段。
3. 不改变已有字段含义。
4. 新增消息类型时，旧客户端可以忽略未知 type。
5. 服务端应该兼容旧版本客户端一段时间。

------

## 29. 一期必须实现的协议

一期建议实现以下协议：

```text
connection_connected
ping
pong
group_message_send
group_message_ack
group_message_failed
group_message_receive
group_message_read
group_message_read_ack
group_message_recalled
error
```

二期实现：

```text
group_member_joined
group_member_left
group_member_kicked
group_dismissed
group_muted
group_unmuted
group_announcement_updated
```

------

## 30. 总结

GroupFlow WebSocket 协议的核心是：

1. 使用统一 JSON 消息结构。
2. 客户端请求通过 requestId 匹配响应。
3. 群消息发送通过 clientMessageId 保证幂等。
4. 服务端通过 group_message_ack 告诉客户端消息已成功落库。
5. 群消息通过 group_message_receive 推送给在线成员。
6. 消息顺序通过 groupId + sequence 保证。
7. 客户端通过 sequence 缺口检测发现遗漏消息。
8. 遗漏消息通过 HTTP 历史消息接口补拉。
9. 心跳通过 ping / pong 保持连接。
10. 断线后客户端自动重连，并基于 lastReceivedSequence 补拉消息。
11. 大群场景下协议不复杂化，服务端通过 Kafka 和投递服务异步完成广播。
12. 离线用户不走 WebSocket 实时投递，上线后通过历史消息补拉。

该协议可以支撑 GroupFlow 一期群聊闭环，也为后续大群、多端同步、消息类型扩展和分布式投递预留了空间。

------

## 31. 代码实现对齐说明（与当前实现核对）

> 本节按当前后端代码实际实现核对上文协议，作为文档与代码之间的权威对照。

### 31.1 连接握手

- 连接成功后服务端立即下发 `connection_connected`，data 含
  `connectionId / userId / serverId / heartbeatIntervalSeconds(20) /
  heartbeatTimeoutSeconds(60) / protocolVersion("v1")`。
- 统一信封字段：`type / version("v1") / requestId / traceId(可选) / timestamp / data`。
  `Build` 始终写入 `version:"v1"` 与毫秒 `timestamp`。

### 31.2 当前实际处理的入站消息类型

服务端入站仅处理以下三类，其余一律回 `error` / `UNKNOWN_TYPE`：

| 入站 type | 处理 |
| --- | --- |
| `ping` | 在读循环内处理：续期 Redis 在线态，回 `pong`（含 `serverTime`） |
| `group_message_send` | 校验+落库+生成事件，回 `group_message_ack` 或 `group_message_failed` |
| `group_message_read` | 已读位置上报，含回退校验（见 31.4） |

- 协议层另有 WebSocket PING 帧（每 30s）与 PongHandler（重置 70s 读超时 + 续期）。

### 31.3 已读上报：错误以 `error` 信封返回

- `group_message_read` 入站字段：`groupId / lastReadSequence`。
- 成功 → `group_message_read_ack`（`{groupId, lastReadSequence}`）。
- 失败统一以 `type:"error"` 返回（**没有** `group_message_read_failed` 类型）：
  - JSON 解析失败 → `code:"BAD_REQUEST"`，`retryable:false`。
  - 已读回退（lastRead < 当前）→ `code:"READ_SEQUENCE_ROLLBACK"`，`retryable:false`，含 `groupId`。
  - 非群成员 / 已退群 / 已被踢 → `code:"FORBIDDEN"`，`retryable:false`。
  - 其它错误 → `code:"READ_FAILED"`，`retryable:true`。
- 相等 sequence 为幂等 no-op，直接成功。
- HTTP `POST /groups/:groupId/read` 同源映射：回退 → 400 `READ_SEQUENCE_ROLLBACK`，
  非成员 → 403 `FORBIDDEN`，其它 → 500 `READ_FAILED`。

### 31.4 发送失败错误码（当前代码）

`group_message_send` 失败时 code 取自 `codeFromErr`：
`GROUP_MUTED` / `SLOW_MODE_LIMITED` / `FORBIDDEN` / `MESSAGE_SEND_FAILED`；
JSON 解析失败为 `BAD_REQUEST`。`retryable` 由错误类型决定。

### 31.5 内部推送接口（Delivery → WS 节点）

- 实际路径为 **`POST /internal/push`**（不是文档示例的 `/internal/ws/push`）。
- 请求体字段：`userIds`（兼容）/ `connectionIds`（优先）/ `type`（缺省
  `group_message_receive`）/ `data`。`connectionIds` 非空时按连接精确下发，
  否则回落按 `userIds` 下发。
- 响应体：`{ "data": { "pushed": N } }`。
- Delivery 投递时**始终发送 `connectionIds`**（不发 userIds/targetUserIds），
  并透传 `X-Trace-Id`。

### 31.6 结构化群事件（Kafka 模式）

- 这些事件经 Outbox → Kafka → Delivery，按事件体内 `targetUserIds` 定向推送，
  WS 下发的 `type` 等于事件类型本身：
  `group_member_kicked / group_join_request_created /
  group_join_request_approved / group_join_request_rejected`。
- Kafka 关闭（direct 模式）时由 router 本地直推；Kafka 开启时 router 直推短路，
  统一走 Delivery，避免双发。

### 31.7 未实现 / 暂缺

- `connection_kicked`（按连接踢下线并主动断开）**未实现**。
- `group_member_joined/left` 等二期结构化事件未单独实现为 WS 协议事件
  （加入/退出当前以系统文本消息 `group_message_receive(system)` 形式下发）。
- 多端跨节点投递已支持：投递按 `online:user:{uid}:connections` →
  `connection:{cid}:server` 解析，按 serverId 分组后分别推送到各 WS 节点。