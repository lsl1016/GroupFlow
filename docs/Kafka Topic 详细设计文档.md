# Kafka Topic 详细设计文档

## 1. 文档说明

### 1.1 文档目的

本文档用于说明 GroupFlow 群聊系统中的 Kafka Topic 设计，包括 Topic 划分、事件结构、分区策略、消费者组、消息顺序、失败重试、死信队列、Outbox、监控指标和后续扩展方案。

GroupFlow 是一个面向大群与高并发场景设计的实时群聊系统。Kafka 主要用于削峰、异步投递和服务解耦。

### 1.2 Kafka 在系统中的定位

Kafka 在 GroupFlow 中承担以下职责：

1. 群消息异步投递。
2. 系统事件异步投递。
3. @提醒事件异步处理。
4. 消息撤回事件广播。
5. 群成员变更事件广播。
6. 大群消息削峰。
7. Delivery Service 与 Message Service 解耦。
8. 后续扩展搜索索引、审计、统计分析。

### 1.3 Kafka 不负责的事情

Kafka 不负责：

1. 消息最终持久化。
2. 群消息顺序号生成。
3. 用户在线状态维护。
4. WebSocket 连接管理。
5. 未读数最终计算。
6. 消息幂等的最终约束。

这些能力分别由 MySQL、Redis、WebSocket Gateway 和业务服务完成。

------

## 2. Kafka 设计原则

### 2.1 消息先落库，再进入 Kafka

群消息发送链路中，必须先写入 MySQL，再发布 Kafka 事件。

推荐流程：

```text
Message Service 校验消息
  ↓
生成 messageId 和 sequence
  ↓
写入 MySQL group_message
  ↓
返回 ACK 给发送者
  ↓
发布 Kafka 事件
```

ACK 的语义是消息已经成功落库，不是 Kafka 投递成功。

### 2.2 Kafka 只负责异步事件流转

Kafka 事件用于驱动后续动作：

1. 投递给在线用户。
2. 处理 @提醒。
3. 推送系统事件。
4. 生成审计记录。
5. 构建搜索索引。

Kafka 不作为历史消息查询来源。

历史消息查询以 MySQL 为准。

### 2.3 同一个群尽量保证消费顺序

群消息需要按 `groupId + sequence` 排序。

Kafka 层面推荐使用 `groupId` 作为分区 Key，让同一个群的消息进入同一个分区。

### 2.4 投递失败允许补拉恢复

Kafka 消费失败或 WebSocket 推送失败时，不要求强制实时投递成功。

原因：

1. 消息已经写入 MySQL。
2. 客户端可以通过 `afterSequence` 补拉。
3. 大群场景不能对每个用户做无限重试。
4. 实时投递是提升体验，不是唯一可见路径。

### 2.5 事件结构必须可扩展

事件结构需要包含：

1. eventId
2. eventType
3. groupId
4. messageId
5. sequence
6. traceId
7. occurredAt
8. payload

后续新增字段必须兼容旧消费者。

------

## 3. Topic 总览

### 3.1 一期 Topic

一期建议实现以下 Topic：

| Topic                      | 用途         | 生产者                          | 消费者                                 |
| -------------------------- | ------------ | ------------------------------- | -------------------------------------- |
| group-message-topic        | 群消息投递   | Message Service                 | Delivery Service                       |
| group-system-event-topic   | 群系统事件   | Group Service / Message Service | Delivery Service                       |
| group-mention-topic        | @提醒事件    | Message Service                 | Mention Service / Notification Service |
| group-message-recall-topic | 消息撤回事件 | Message Service                 | Delivery Service                       |
| group-audit-topic          | 群操作审计   | Group Service / Message Service | Audit Service                          |

### 3.2 二期 Topic

二期可以增加：

| Topic                     | 用途                   |
| ------------------------- | ---------------------- |
| group-message-large-topic | 大群消息专用投递       |
| group-message-hot-topic   | 热点群消息专用投递     |
| group-search-index-topic  | 消息搜索索引构建       |
| group-stat-topic          | 群消息统计、活跃度统计 |

### 3.3 死信 Topic

建议为关键 Topic 配置死信 Topic：

| 原 Topic                   | 死信 Topic                     |
| -------------------------- | ------------------------------ |
| group-message-topic        | group-message-dlq-topic        |
| group-system-event-topic   | group-system-event-dlq-topic   |
| group-mention-topic        | group-mention-dlq-topic        |
| group-message-recall-topic | group-message-recall-dlq-topic |

------

## 4. Topic 命名规范

### 4.1 命名格式

推荐格式：

```text
业务域-事件类型-topic
```

示例：

```text
group-message-topic
group-system-event-topic
group-mention-topic
```

### 4.2 死信 Topic 命名格式

```text
原topic名去掉-topic + -dlq-topic
```

示例：

```text
group-message-dlq-topic
group-mention-dlq-topic
```

### 4.3 重试 Topic 命名格式

如果引入重试 Topic，可以使用：

```text
group-message-retry-1m-topic
group-message-retry-5m-topic
group-message-retry-30m-topic
```

------

# 5. group-message-topic 设计

## 5.1 Topic 说明

`group-message-topic` 用于承载普通群和大群的群消息投递事件。

这是 GroupFlow 最核心的 Topic。

### 5.2 生产者

```text
Message Service
```

### 5.3 消费者

```text
Delivery Service
```

### 5.4 事件触发时机

当群消息成功写入 MySQL 后，Message Service 发送事件到 Kafka。

### 5.5 分区 Key

推荐：

```text
groupId
```

原因：

1. 同一个群的消息进入同一个分区。
2. 同一个群消息消费顺序更稳定。
3. Delivery Service 更容易按 sequence 投递。
4. 客户端乱序概率更低。

### 5.6 事件结构

```json
{
  "eventId": "evt_100000001",
  "eventType": "group_message_created",
  "traceId": "trace_100000001",
  "groupId": 10001,
  "groupType": "large",
  "messageId": "msg_100000001",
  "sequence": 100201,
  "senderId": 1001,
  "occurredAt": "2026-06-28T10:00:00.000Z",
  "payload": {
    "messageId": "msg_100000001",
    "groupId": 10001,
    "senderId": 1001,
    "senderName": "张三",
    "senderAvatar": "https://example.com/avatar.png",
    "messageType": "text",
    "content": "大家好，今天讨论 Kafka Topic 设计。",
    "sequence": 100201,
    "status": "normal",
    "mentionAll": false,
    "mentionUserIds": [1002, 1003],
    "createdAt": "2026-06-28T10:00:00.000Z"
  }
}
```

### 5.7 字段说明

| 字段       | 说明             |
| ---------- | ---------------- |
| eventId    | 事件唯一 ID      |
| eventType  | 事件类型         |
| traceId    | 链路追踪 ID      |
| groupId    | 群 ID            |
| groupType  | 群类型           |
| messageId  | 消息 ID          |
| sequence   | 群内消息序号     |
| senderId   | 发送者 ID        |
| occurredAt | 事件发生时间     |
| payload    | 投递所需消息内容 |

### 5.8 是否携带完整消息内容

推荐一期携带完整消息内容。

原因：

1. Delivery Service 不需要再次查 MySQL。
2. 降低投递延迟。
3. 减少数据库读压力。
4. 大群投递链路更简单。

注意：

消息最终状态仍以 MySQL 为准。

如果消息快速撤回，需要发送撤回事件修正客户端展示。

------

# 6. group-system-event-topic 设计

## 6.1 Topic 说明

`group-system-event-topic` 用于承载群系统事件，例如成员加入、退出、被踢、群公告更新、群解散、禁言变更等。

### 6.2 生产者

```text
Group Service
Message Service
Admin Service
```

### 6.3 消费者

```text
Delivery Service
Notification Service
Audit Service
```

### 6.4 事件类型

| eventType                  | 说明           |
| -------------------------- | -------------- |
| group_member_joined        | 成员加入群     |
| group_member_left          | 成员退出群     |
| group_member_kicked        | 成员被踢       |
| group_dismissed            | 群被解散       |
| group_muted                | 群开启全员禁言 |
| group_unmuted              | 群关闭全员禁言 |
| group_announcement_updated | 群公告更新     |

### 6.5 分区 Key

推荐：

```text
groupId
```

原因：

1. 同一个群的系统事件按群有序。
2. 客户端接收同群事件更稳定。
3. 便于 Delivery Service 按群处理。

### 6.6 事件结构

```json
{
  "eventId": "evt_sys_100000001",
  "eventType": "group_member_kicked",
  "traceId": "trace_100000002",
  "groupId": 10001,
  "operatorId": 1001,
  "targetUserId": 1005,
  "occurredAt": "2026-06-28T10:10:00.000Z",
  "payload": {
    "groupId": 10001,
    "operatorId": 1001,
    "targetUserId": 1005,
    "reason": "违反群规则",
    "kickedAt": "2026-06-28T10:10:00.000Z"
  }
}
```

### 6.7 投递策略

普通系统事件可以异步投递。

但以下事件优先级更高：

1. 用户被踢。
2. 群解散。
3. 全员禁言。
4. 消息撤回。

这些事件影响用户当前操作状态，应尽快投递。

------

# 7. group-mention-topic 设计

## 7.1 Topic 说明

`group-mention-topic` 用于处理 @提醒事件。

### 7.2 生产者

```text
Message Service
```

### 7.3 消费者

```text
Mention Service
Notification Service
```

### 7.4 事件触发条件

当消息满足以下条件时产生事件：

1. `mentionAll = true`
2. `mentionUserIds` 非空

### 7.5 分区 Key

推荐：

```text
groupId
```

或者：

```text
userId
```

### 7.6 分区 Key 选择

如果重点是保持同群 @事件有序：

```text
使用 groupId
```

如果重点是按用户处理提醒：

```text
使用 userId
```

一期推荐：

```text
groupId
```

因为 @提醒来自群消息，同群顺序更重要。

### 7.7 @某人事件结构

```json
{
  "eventId": "evt_mention_100000001",
  "eventType": "group_message_mention_user",
  "traceId": "trace_100000003",
  "groupId": 10001,
  "messageId": "msg_100000001",
  "sequence": 100201,
  "senderId": 1001,
  "mentionUserIds": [1002, 1003],
  "occurredAt": "2026-06-28T10:00:00.000Z",
  "payload": {
    "contentPreview": "大家好，今天讨论 Kafka Topic 设计。",
    "messageType": "text"
  }
}
```

### 7.8 @所有人事件结构

```json
{
  "eventId": "evt_mention_all_100000001",
  "eventType": "group_message_mention_all",
  "traceId": "trace_100000004",
  "groupId": 10001,
  "messageId": "msg_100000001",
  "sequence": 100201,
  "senderId": 1001,
  "occurredAt": "2026-06-28T10:00:00.000Z",
  "payload": {
    "contentPreview": "请所有成员关注今天的公告。",
    "messageType": "text"
  }
}
```

### 7.9 大群 @所有人注意事项

大群中不要把 @所有人同步展开成所有成员提醒。

不推荐：

```text
10 万人群 @所有人
  ↓
同步写入 10 万条 group_mention
```

推荐：

1. 消息表记录 `mentionAll = true`。
2. Kafka 发送一条 `group_message_mention_all` 事件。
3. 前端在接收消息时展示 @所有人。
4. 如需提醒记录，异步生成摘要，而不是同步展开全员。

------

# 8. group-message-recall-topic 设计

## 8.1 Topic 说明

`group-message-recall-topic` 用于广播消息撤回事件。

### 8.2 生产者

```text
Message Service
```

### 8.3 消费者

```text
Delivery Service
Audit Service
Notification Service
```

### 8.4 分区 Key

推荐：

```text
groupId
```

### 8.5 事件结构

```json
{
  "eventId": "evt_recall_100000001",
  "eventType": "group_message_recalled",
  "traceId": "trace_100000005",
  "groupId": 10001,
  "messageId": "msg_100000001",
  "sequence": 100201,
  "operatorId": 1001,
  "senderId": 1001,
  "occurredAt": "2026-06-28T10:02:00.000Z",
  "payload": {
    "groupId": 10001,
    "messageId": "msg_100000001",
    "sequence": 100201,
    "operatorId": 1001,
    "senderId": 1001,
    "recalledAt": "2026-06-28T10:02:00.000Z"
  }
}
```

### 8.6 投递策略

撤回事件优先级高于普通系统事件。

客户端收到撤回事件后：

1. 根据 messageId 查找本地消息。
2. 将消息状态改为 recalled。
3. 隐藏原消息内容。
4. 展示“某某撤回了一条消息”。

------

# 9. group-audit-topic 设计

## 9.1 Topic 说明

`group-audit-topic` 用于记录群管理操作审计事件。

### 9.2 生产者

```text
Group Service
Message Service
Admin Service
```

### 9.3 消费者

```text
Audit Service
Log Service
```

### 9.4 事件类型

| eventType        | 说明       |
| ---------------- | ---------- |
| group_created    | 创建群     |
| group_updated    | 修改群     |
| group_dismissed  | 解散群     |
| member_kicked    | 踢出成员   |
| member_muted     | 禁言成员   |
| member_unmuted   | 解除禁言   |
| admin_set        | 设置管理员 |
| admin_unset      | 取消管理员 |
| message_recalled | 撤回消息   |

### 9.5 分区 Key

推荐：

```text
groupId
```

### 9.6 事件结构

```json
{
  "eventId": "evt_audit_100000001",
  "eventType": "member_kicked",
  "traceId": "trace_100000006",
  "groupId": 10001,
  "operatorId": 1001,
  "targetUserId": 1005,
  "occurredAt": "2026-06-28T10:10:00.000Z",
  "payload": {
    "reason": "违反群规则",
    "source": "group_admin_panel"
  }
}
```

### 9.7 消费策略

Audit Service 消费后写入：

```text
group_operation_log
```

或日志系统。

------

# 10. group-search-index-topic 设计

## 10.1 Topic 说明

`group-search-index-topic` 用于后续构建消息搜索索引。

一期可以不实现。

### 10.2 生产者

```text
Message Service
```

### 10.3 消费者

```text
Search Index Service
```

### 10.4 事件结构

```json
{
  "eventId": "evt_search_100000001",
  "eventType": "group_message_index",
  "traceId": "trace_100000007",
  "groupId": 10001,
  "messageId": "msg_100000001",
  "sequence": 100201,
  "senderId": 1001,
  "occurredAt": "2026-06-28T10:00:00.000Z",
  "payload": {
    "messageType": "text",
    "content": "大家好，今天讨论 Kafka Topic 设计。",
    "createdAt": "2026-06-28T10:00:00.000Z"
  }
}
```

------

# 11. 消费者组设计

## 11.1 消费者组总览

| Consumer Group                  | 消费 Topic                 | 职责           |
| ------------------------------- | -------------------------- | -------------- |
| groupflow-delivery-group        | group-message-topic        | 群消息实时投递 |
| groupflow-system-delivery-group | group-system-event-topic   | 系统事件推送   |
| groupflow-mention-group         | group-mention-topic        | @提醒处理      |
| groupflow-recall-delivery-group | group-message-recall-topic | 撤回事件推送   |
| groupflow-audit-group           | group-audit-topic          | 审计落库       |
| groupflow-search-index-group    | group-search-index-topic   | 搜索索引构建   |

### 11.2 Delivery Service 消费者组

```text
groupflow-delivery-group
```

消费：

```text
group-message-topic
```

职责：

1. 消费群消息。
2. 查询在线成员。
3. 查询连接路由。
4. 按 WebSocket 节点分片。
5. 批量推送给在线用户。

### 11.3 多消费者并行

Kafka 一个分区同一时间只能被同一消费者组中的一个消费者消费。

因此并行能力受分区数影响。

如果需要提高 Delivery Service 并行能力，需要增加 Topic 分区数。

------

# 12. 分区设计

## 12.1 分区数建议

一期开发环境：

```text
3 - 6 个分区
```

测试环境：

```text
6 - 12 个分区
```

压测环境：

```text
12 - 48 个分区
```

生产模拟：

```text
根据压测结果确定
```

### 12.2 group-message-topic 分区数

推荐初期：

```text
12
```

后续根据：

1. 消息 QPS。
2. Delivery Service 数量。
3. Kafka lag。
4. 热点群情况。

逐步调整。

### 12.3 分区 Key

`group-message-topic` 推荐：

```text
key = groupId
```

### 12.4 优点

1. 同群消息有序。
2. 简化 Delivery Service。
3. 客户端乱序更少。

### 12.5 缺点

1. 超级热点群会形成单分区热点。
2. 单个热点群无法完全利用所有分区。
3. Delivery Service 对热点群的处理可能变成瓶颈。

------

# 13. 热点群 Topic 策略

## 13.1 热点群问题

如果一个 10 万人大群每秒产生大量消息，使用 `groupId` 作为 Kafka Key 会导致：

```text
该群所有消息进入同一个分区。
该分区消费压力过高。
Kafka lag 持续增长。
Delivery Service 投递延迟升高。
```

### 13.2 解决方案一：开启慢速模式

通过 Redis 限流降低消息产生速度。

优点：

1. 简单。
2. 效果直接。
3. 保持同群消息顺序。

缺点：

1. 牺牲用户发送频率。
2. 热点群体验下降。

### 13.3 解决方案二：热点群独立 Topic

将热点群消息投递到：

```text
group-message-hot-topic
```

优点：

1. 热点群与普通群隔离。
2. 避免影响普通群投递。
3. 可以给热点 Topic 配置更多消费者资源。

缺点：

1. 路由逻辑更复杂。
2. Topic 管理成本增加。
3. 仍需要处理单群顺序问题。

### 13.4 解决方案三：投递任务拆分并行

消息仍按 `groupId` 进入 Kafka，但 Delivery Service 内部按 WebSocket 节点拆分任务并行推送。

优点：

1. 保持同群消息消费顺序。
2. 投递 fanout 可以并行。
3. 适合大群分片推送。

缺点：

1. Delivery Service 内部复杂度增加。
2. 客户端仍需处理乱序。
3. 需要控制同一群多条消息并行投递的顺序影响。

### 13.5 推荐策略

优先顺序：

```text
1. 慢速模式限制消息 QPS
2. Delivery Service 内部分片并行投递
3. 热点群独立 Topic
4. 超级热点群单独部署消费者组
```

------

# 14. 事件顺序设计

## 14.1 群消息顺序

群消息最终顺序以：

```text
groupId + sequence
```

为准。

Kafka 顺序只是辅助。

### 14.2 Kafka 中的顺序

如果使用 `groupId` 作为分区 Key，则同一群的事件在同一分区内有序。

但以下情况仍可能导致客户端接收乱序：

1. WebSocket 节点推送速度不同。
2. 客户端网络不同。
3. Delivery Service 内部分批推送。
4. 客户端处理速度不同。

### 14.3 客户端处理

客户端必须：

1. 根据 `messageId` 去重。
2. 根据 `sequence` 排序。
3. 检测 sequence 缺口。
4. 通过 HTTP 补拉遗漏消息。
5. 补拉不到时接受 sequence 跳号。

------

# 15. 消息重复设计

## 15.1 Kafka 重复消费

Kafka 消费语义可能导致消息被重复消费。

可能场景：

1. 消费成功但提交 offset 失败。
2. 消费者重平衡。
3. Delivery Service 处理超时后重试。
4. 手动重放 Topic。

### 15.2 重复投递处理

重复投递由客户端和 WebSocket 层共同兜底。

客户端必须通过：

```text
messageId
```

去重。

发送端还可以通过：

```text
clientMessageId
```

去重。

### 15.3 Delivery Service 幂等

Delivery Service 一期可以不做复杂幂等。

如果需要增强，可以增加 Redis Key：

```text
delivery:processed:{eventId}
```

TTL：

```text
10 分钟 - 1 小时
```

写入方式：

```text
SET delivery:processed:{eventId} 1 EX 3600 NX
```

如果设置失败，说明该事件已处理过，可以跳过。

------

# 16. 失败重试设计

## 16.1 失败类型

| 失败类型               | 示例                 | 是否重试     |
| ---------------------- | -------------------- | ------------ |
| Kafka 消费反序列化失败 | JSON 格式错误        | 否，进入 DLQ |
| 群配置查询失败         | Redis/MySQL 临时异常 | 是           |
| 在线状态查询失败       | Redis 临时异常       | 是           |
| WS 节点推送超时        | 网络抖动             | 是，短暂重试 |
| 单用户连接不存在       | 用户已离线           | 否           |
| SendChan 满            | 客户端消费慢         | 否或断开连接 |
| 业务数据不存在         | 群已删除、消息不存在 | 否           |

### 16.2 Delivery Service 重试

对于节点级失败，推荐短暂重试：

```text
最多重试 2 次
间隔 100ms / 300ms
```

不建议长时间阻塞 Kafka 消费线程。

### 16.3 Retry Topic

如果需要异步重试，可以增加：

```text
group-message-retry-1m-topic
group-message-retry-5m-topic
group-message-retry-30m-topic
```

### 16.4 一期建议

一期不建议做复杂 Retry Topic。

建议：

1. Delivery Service 内部短暂重试。
2. 失败后记录日志和指标。
3. 用户通过补拉恢复。
4. 严重失败事件进入 DLQ。

------

# 17. 死信队列设计

## 17.1 DLQ 目的

DLQ 用于保存无法正常消费的事件，方便后续人工排查和补偿。

### 17.2 进入 DLQ 的场景

1. 消息格式无法解析。
2. 必填字段缺失。
3. 事件类型未知。
4. 业务数据长期不存在。
5. 重试多次仍失败。
6. 消费逻辑出现不可恢复异常。

### 17.3 DLQ 消息结构

```json
{
  "dlqId": "dlq_100000001",
  "sourceTopic": "group-message-topic",
  "sourcePartition": 3,
  "sourceOffset": 102331,
  "consumerGroup": "groupflow-delivery-group",
  "eventId": "evt_100000001",
  "eventType": "group_message_created",
  "reason": "JSON_PARSE_ERROR",
  "errorMessage": "invalid character",
  "rawMessage": "{...}",
  "failedAt": "2026-06-28T10:00:00.000Z"
}
```

### 17.4 DLQ Topic

```text
group-message-dlq-topic
group-system-event-dlq-topic
group-mention-dlq-topic
group-message-recall-dlq-topic
```

### 17.5 DLQ 处理方式

1. 记录告警。
2. 后台管理页面查看。
3. 人工修正后重放。
4. 对不可恢复事件标记丢弃。

------

# 18. Outbox 设计

## 18.1 为什么需要 Outbox

基础流程中存在一个问题：

```text
MySQL 消息落库成功
  ↓
Kafka 发送失败
```

此时：

1. 发送者已经收到 ACK。
2. 消息已经在历史消息中可见。
3. 在线用户可能没有实时收到。

Outbox 用于保证 Kafka 事件最终发送。

------

## 18.2 Outbox 流程

```text
MySQL 事务中：
  1. 插入 group_message
  2. 插入 message_outbox

事务提交后：
  Outbox Worker 扫描 pending 事件
  发送 Kafka
  发送成功后更新 status = sent
```

### 18.3 Outbox 表结构

```sql
CREATE TABLE message_outbox (
    id BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '事件ID',
    event_id VARCHAR(64) NOT NULL COMMENT '事件唯一ID',
    event_type VARCHAR(64) NOT NULL COMMENT '事件类型',
    aggregate_type VARCHAR(64) NOT NULL COMMENT '聚合类型',
    aggregate_id VARCHAR(64) NOT NULL COMMENT '聚合ID',
    topic VARCHAR(128) NOT NULL COMMENT '目标Topic',
    partition_key VARCHAR(128) NOT NULL COMMENT '分区Key',
    payload JSON NOT NULL COMMENT '事件内容',

    status VARCHAR(32) NOT NULL DEFAULT 'pending' COMMENT '状态：pending/sent/failed',
    retry_count INT NOT NULL DEFAULT 0 COMMENT '重试次数',
    next_retry_at DATETIME DEFAULT NULL COMMENT '下次重试时间',

    created_at DATETIME NOT NULL COMMENT '创建时间',
    updated_at DATETIME NOT NULL COMMENT '更新时间',

    UNIQUE KEY uk_event_id (event_id),
    INDEX idx_status_retry (status, next_retry_at),
    INDEX idx_aggregate (aggregate_type, aggregate_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='消息事件Outbox表';
```

### 18.4 Outbox 状态

| 状态      | 说明                         |
| --------- | ---------------------------- |
| pending   | 等待发送                     |
| sent      | 已发送                       |
| failed    | 发送失败，等待重试或人工处理 |
| discarded | 已丢弃                       |

### 18.5 一期建议

一期可以先不实现 Outbox。

但代码层面应该抽象事件发布接口：

```go
type EventPublisher interface {
    Publish(ctx context.Context, topic string, partitionKey string, event any) error
}
```

后续可以把直接发送 Kafka 替换成写 Outbox。

------

# 19. Kafka Producer 设计

## 19.1 Producer 配置建议

配置项：

```yaml
kafka:
  producer:
    brokers:
      - localhost:9092
    acks: all
    retries: 3
    batch_size: 16384
    linger_ms: 5
    compression_type: snappy
    request_timeout_ms: 3000
```

### 19.2 acks

推荐：

```text
acks = all
```

原因：

1. 可靠性更高。
2. 避免 leader 写入成功但副本未同步导致丢事件。

### 19.3 retries

推荐：

```text
retries = 3
```

### 19.4 compression

推荐：

```text
snappy 或 lz4
```

用于降低网络传输压力。

### 19.5 发送超时

推荐：

```text
request_timeout_ms = 3000
```

发送超时后：

1. 记录日志。
2. 如果使用 Outbox，等待后续重试。
3. 如果未使用 Outbox，记录错误指标。

------

# 20. Kafka Consumer 设计

## 20.1 Consumer 配置建议

```yaml
kafka:
  consumer:
    brokers:
      - localhost:9092
    group_id: groupflow-delivery-group
    enable_auto_commit: false
    auto_offset_reset: latest
    max_poll_records: 500
    session_timeout_ms: 10000
    heartbeat_interval_ms: 3000
```

### 20.2 手动提交 Offset

推荐关闭自动提交：

```text
enable_auto_commit = false
```

处理成功后手动提交 offset。

原因：

1. 避免消息尚未处理完成就提交。
2. 出错时可以重新消费。
3. 更容易控制失败行为。

### 20.3 提交时机

推荐：

```text
事件解析成功
  ↓
核心处理完成
  ↓
必要日志和指标记录完成
  ↓
提交 offset
```

### 20.4 Delivery Service 的提交策略

对于群消息投递：

1. Kafka 消费成功。
2. Delivery Service 完成在线用户筛选。
3. 完成 WebSocket 推送任务下发。
4. 记录投递结果。
5. 提交 offset。

注意：

如果部分用户推送失败，不一定阻止 offset 提交。

原因是失败用户可以通过补拉恢复。

------

# 21. 事件版本设计

## 21.1 version 字段

建议事件中增加：

```json
{
  "version": "v1"
}
```

### 21.2 完整事件结构

```json
{
  "eventId": "evt_100000001",
  "eventType": "group_message_created",
  "version": "v1",
  "traceId": "trace_100000001",
  "groupId": 10001,
  "partitionKey": "10001",
  "occurredAt": "2026-06-28T10:00:00.000Z",
  "payload": {}
}
```

### 21.3 兼容规则

1. 新增字段必须可选。
2. 不删除已有字段。
3. 不改变已有字段含义。
4. 消费者遇到未知字段应忽略。
5. 消费者遇到未知事件类型应记录日志并跳过或进入 DLQ。

------

# 22. 消息大小控制

## 22.1 消息大小建议

Kafka 事件不应过大。

群消息事件建议控制在：

```text
小于 64KB
```

### 22.2 大文件消息

文件、图片、视频消息不应把二进制内容放入 Kafka。

推荐只放元数据：

```json
{
  "messageType": "file",
  "content": "",
  "extra": {
    "fileId": "file_10001",
    "fileName": "设计文档.pdf",
    "fileSize": 1024000,
    "fileUrl": "https://example.com/file/10001"
  }
}
```

### 22.3 文本消息长度

消息正文长度应由 Message Service 限制，例如：

```text
最多 2000 字符
```

------

# 23. 监控指标设计

## 23.1 Producer 指标

```text
kafka_produce_total
kafka_produce_failed_total
kafka_produce_latency_ms
kafka_produce_message_size_bytes
kafka_produce_retry_total
```

### 23.2 Consumer 指标

```text
kafka_consume_total
kafka_consume_failed_total
kafka_consume_latency_ms
kafka_consume_lag
kafka_consumer_rebalance_total
```

### 23.3 Topic 指标

```text
kafka_topic_group_message_lag
kafka_topic_group_message_qps
kafka_topic_group_message_bytes_in
kafka_topic_group_message_bytes_out
```

### 23.4 Delivery 相关指标

```text
delivery_message_consume_total
delivery_message_consume_failed_total
delivery_fanout_user_total
delivery_push_task_total
delivery_push_failed_total
delivery_latency_ms
```

------

# 24. 日志设计

## 24.1 Producer 日志

字段：

```json
{
  "traceId": "trace_100000001",
  "eventId": "evt_100000001",
  "topic": "group-message-topic",
  "partitionKey": "10001",
  "groupId": 10001,
  "messageId": "msg_100000001",
  "sequence": 100201,
  "event": "kafka_produce",
  "durationMs": 12
}
```

Go 日志示例：

```go
logger.Infof("kafka produce success, topic:%s, groupId:%d, messageId:%s, sequence:%d, durationMs:%d",
    topic, groupID, messageID, sequence, durationMs)
```

### 24.2 Consumer 日志

字段：

```json
{
  "traceId": "trace_100000001",
  "eventId": "evt_100000001",
  "topic": "group-message-topic",
  "partition": 3,
  "offset": 100231,
  "consumerGroup": "groupflow-delivery-group",
  "groupId": 10001,
  "messageId": "msg_100000001",
  "event": "kafka_consume",
  "durationMs": 35
}
```

Go 日志示例：

```go
logger.Infof("kafka consume success, topic:%s, partition:%d, offset:%d, groupId:%d, messageId:%s",
    topic, partition, offset, groupID, messageID)
```

注意：日志输出使用格式化占位符，不使用字符串拼接。

------

# 25. 一期实现范围

一期必须实现：

1. `group-message-topic`
2. `group-system-event-topic`
3. `group-message-recall-topic`
4. `groupflow-delivery-group`
5. `groupflow-system-delivery-group`
6. `groupflow-recall-delivery-group`
7. `groupId` 分区 Key
8. 统一事件结构
9. Producer 失败日志
10. Consumer 手动提交 offset
11. Delivery Service 消费群消息
12. 基础 Kafka lag 监控

一期可以暂缓：

1. `group-message-large-topic`
2. `group-message-hot-topic`
3. `group-search-index-topic`
4. Retry Topic
5. DLQ 管理后台
6. Outbox
7. 复杂事件版本治理

------

# 26. 二期演进

二期建议实现：

1. `group-mention-topic`
2. `group-audit-topic`
3. `group-message-dlq-topic`
4. `group-system-event-dlq-topic`
5. Outbox 表
6. Outbox Worker
7. Kafka 消费失败重试策略
8. Kafka 消息版本字段
9. Kafka 消息大小监控
10. 热点群独立消费指标

------

# 27. 三期演进

三期建议实现：

1. `group-message-large-topic`
2. `group-message-hot-topic`
3. 热点群专用消费者组。
4. 超级热点群独立资源隔离。
5. Retry Topic。
6. DLQ 重放后台。
7. 搜索索引 Topic。
8. 统计分析 Topic。
9. 动态 Topic 路由。
10. Kafka 分区扩容策略。

------

# 28. 总结

GroupFlow Kafka Topic 设计的核心是：

1. Kafka 用于异步投递、削峰和服务解耦。
2. 群消息成功落库后，才发布 Kafka 事件。
3. `group-message-topic` 是最核心的 Topic。
4. `groupId` 作为分区 Key，用于尽量保证同群消息消费顺序。
5. Delivery Service 消费群消息后，按在线用户和 WebSocket 节点进行分片推送。
6. Kafka ACK 不等于用户收到消息。
7. WebSocket 推送失败不阻塞 Kafka offset 提交，用户可以通过历史消息补拉。
8. 大群场景需要警惕单群热点分区。
9. 热点群优先通过慢速模式和内部投递分片处理。
10. 后续可以引入热点群独立 Topic、Retry Topic、DLQ 和 Outbox。
11. 事件必须包含 eventId、eventType、traceId、groupId、occurredAt 和 payload。
12. 客户端最终仍然依赖 messageId 去重、sequence 排序和历史消息补拉来保证体验一致性。

------

# 29. 代码实现对齐说明（与当前实现核对）

> 本节按当前后端代码实际实现核对上文设计，作为文档与代码之间的权威对照。

## 29.1 Topic 与消费者组

- **当前只使用单个 Topic**，由配置 `KAFKA_GROUP_MESSAGE_TOPIC` 决定，默认
  `group-message-topic`。生产者、消费者、Outbox 行的 topic 字段全部指向这一个 topic。
- 消费者组：`KAFKA_CONSUMER_GROUP`，默认 `groupflow-delivery`。
- Brokers：`KAFKA_BROKERS`，默认 `localhost:9092`。
- 开关：`KAFKA_ENABLED`，默认 `false`；关闭时不写 Outbox、不跑 relay/consumer，
  由 router 直推（direct 模式）。
- **未拆分** `group-system-event-topic` / `group-message-large-topic` /
  `group-message-hot-topic` 等：群系统/结构化事件复用同一个 topic，通过 `eventType` 区分。

## 29.2 事件信封（实际产生结构）

由 `service.outboxFor` 生成的信封 JSON 字段（顺序）：

```json
{
  "eventId": "evt_...",
  "eventType": "...",
  "traceId": "...",
  "groupId": 10001,
  "groupType": "large",
  "messageId": "msg_...",
  "sequence": 100201,
  "senderId": 1001,
  "occurredAt": "RFC3339Nano",
  "payload": { ... }
}
```

- 非消息类事件 `messageId` 为空串、`sequence` 为 0。
- 消费端 `groupEvent` 只解码 `eventId/eventType/traceId/groupId/groupType/messageId/sequence/payload`，
  `senderId` 与 `occurredAt` 产生但消费时未使用。
- 信封中**没有** `version` 字段（与 §21 建议不同，目前未引入版本字段）。

## 29.3 实际产生并消费的 eventType（生产=消费，共 6 种）

| eventType | 生产 | 消费分支 |
| --- | --- | --- |
| `group_message_created` | SendGroupMessage / createSystemMessage | fanout → WS `group_message_receive` |
| `group_message_recalled` | RecallMessage | fanout → WS `group_message_recalled` |
| `group_member_kicked` | KickMember | handleStructuredEvent（按 targetUserIds） |
| `group_join_request_created` | JoinGroup（审批模式） | handleStructuredEvent |
| `group_join_request_approved` | ApproveJoinRequest | handleStructuredEvent |
| `group_join_request_rejected` | RejectJoinRequest | handleStructuredEvent |

- 群消息/撤回走 `fanout`（枚举全量活跃成员后过滤在线连接）。
- 四类结构化事件走 `handleStructuredEvent`，payload 形如
  `{ "targetUserIds": [...], "body": <事件体> }`，只推给 targetUserIds 对应的在线连接。
- 未知 eventType：记 `ignore_unknown_event` 日志并提交（视为已处理）。

## 29.4 分区 Key

- 分区 Key = `groupId` 的字符串形式（`OutboxEvent.AggregateID`），
  生产时作为 `kafka.Message.Key`，配合 `&kafka.Hash{}` balancer 保证同群进同分区、群内有序。

## 29.5 Producer 配置（当前代码）

- `RequiredAcks = kafka.RequireOne`（**仅 leader ack**，非文档 §19.2 建议的 `all`）。
- `Balancer = &kafka.Hash{}`。
- `BatchTimeout = 10ms`。
- 未设置显式 compression / retries / request_timeout。
- Kafka 关闭时使用 `NoopProducer`（Produce 为空操作）。

## 29.6 Consumer 提交语义（重试后成功才提交）

- 使用 `FetchMessage` 手动拉取，`processMessage` 包裹处理：
  - 重试退避序列 `{0, 50ms, 100ms}`，**共 3 次尝试**。
  - **仅在处理成功后才提交 offset**；3 次都失败则不提交、记错误日志，进入下一条。
  - Reader `CommitInterval = 1s`，提交为异步刷新。
- 失败消息没有 DLQ 去向；未提交的 offset 在重启/重平衡后会被重新拉取。

## 29.7 Outbox（事务消息表）— 已实现

- 写侧：`insertOutboxTx` 在业务写入的**同一 MySQL 事务**内写 `message_outbox`
  （InsertMessage / RecallMessage / MarkMemberStatus / CreateJoinRequest /
  ApproveJoinRequest / RejectJoinRequest 均接入），保证“消息已存储 ⇔ 事件已入队”。
- relay：`RunOutboxRelay` 每 **500ms** 轮询 `drainOutbox`，仅在 Kafka 开启且 Producer 非空时运行。
- 认领：`ClaimPendingOutbox` 用 `FOR UPDATE SKIP LOCKED` 原子认领，置 `sending` 并设
  30s 租约（崩溃可被重新认领）；成功 `MarkOutboxSent`，失败 `MarkOutboxRetry`
  指数退避（`1<<retry` 秒，封顶 60s）。状态：`pending→sending→sent/failed`。

## 29.8 与原设计的主要差异（待演进项）

- 未实现独立 DLQ topic / Retry topic；失败重试由「Outbox 行 next_retry_at 退避」+
  「消费端 3 次内存重试」两层完成，无死信去向。
- Producer 仍为 `RequireOne`（可后续改 `acks=all` 提升可靠性）。
- 信封未引入 `version` 字段。
- 未拆分 system-event / large / hot 专用 topic。