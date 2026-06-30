# GroupFlow 一期 MVP 设计说明

## 1. MVP 目标

一期不做完整社交产品，只做可运行、可压测、可演进的群聊核心闭环：

1. 群列表、群详情、创建群、加入群。
2. 群成员游标分页和群主 / 管理员 / 普通成员权限。
3. 文本消息、系统消息、WebSocket 实时推送。
4. ACK、clientMessageId 去重、group sequence。
5. 历史消息 beforeSequence / afterSequence 游标分页。
6. lastReadSequence、未读数、断线补拉。
7. 全员禁言、单人禁言、踢人、退群、解散群。
8. 大群模式、慢速模式。
9. @提醒、群公告、加群审批、消息撤回。
10. Swag / Swagger 在线 API 文档。

## 2. 架构

```text
Frontend React
  | HTTP / WebSocket
Nginx
  | /api /ws
Go API + WebSocket Gateway + Message Service
  | MySQL / Redis / Kafka
Delivery Service
  | internal push
WebSocket Gateway 本机连接
```

一期为了降低部署复杂度，将 API Service、WebSocket Gateway、Message Service 放在同一个 Go 服务中；Delivery Service 独立进程消费 Kafka。后续可以拆分为多 API、多 WS、多 Delivery。

## 3. 大群优化从第一版进入模型

- 群消息只存 `group_message` 一份，不做每人一条 inbox。
- 顺序只依赖 `group_id + sequence`。
- 去重依赖 `sender_id + client_message_id` 唯一约束。
- 未读数使用 `chat_group.max_sequence - group_member.last_read_sequence`。
- 历史消息只支持 sequence 游标，不支持深分页。
- 群成员只支持 id 游标分页。
- 大群实时投递走 Kafka + Delivery Service + 批量 internal push。
- Redis 维护 `online:user:{userId}`、`online:user:{userId}:connections`、`connection:{connectionId}:server`。
- 慢速模式使用 `rate_limit:group:{groupId}:user:{userId}` TTL 限流。
- 代码通过 Repository 封装 group_message 查询，后续可按 group_id hash 分表。

## 4. HTTP API 摘要

| API | 说明 |
|---|---|
| POST /api/v1/auth/login | 用户名登录 |
| GET /api/v1/groups | 群列表，返回未读数 |
| POST /api/v1/groups | 创建群 |
| GET /api/v1/groups/{groupId} | 群详情 |
| POST /api/v1/groups/{groupId}/join | 加入群 |
| GET /api/v1/groups/{groupId}/members | 成员游标分页 |
| PATCH /api/v1/groups/{groupId}/settings | 全员禁言、大群模式、慢速模式 |
| POST /api/v1/groups/{groupId}/members/{userId}/role | 设置管理员 / 取消管理员 |
| POST /api/v1/groups/{groupId}/members/{userId}/mute | 单人禁言 |
| DELETE /api/v1/groups/{groupId}/members/{userId}/mute | 解除禁言 |
| DELETE /api/v1/groups/{groupId}/members/{userId} | 踢人 |
| POST /api/v1/groups/{groupId}/leave | 退群 |
| DELETE /api/v1/groups/{groupId} | 解散群 |
| GET /api/v1/groups/{groupId}/messages | 历史消息 / 断线补拉 |
| POST /api/v1/groups/{groupId}/read | 上报 lastReadSequence |
| GET /api/v1/groups/{groupId}/mentions | @提醒列表 |
| POST /api/v1/groups/{groupId}/mentions/read | 标记@提醒已读 |
| GET /api/v1/groups/{groupId}/announcements | 群公告列表 |
| POST /api/v1/groups/{groupId}/announcements | 发布群公告 |
| GET /api/v1/groups/{groupId}/join-requests | 加群审批列表 |
| POST /api/v1/groups/{groupId}/join-requests/{requestId}/approve | 通过加群审批 |
| POST /api/v1/groups/{groupId}/messages/{messageId}/recall | 撤回消息 |

## 5. WebSocket 协议摘要

- `connection_connected`
- `ping` / `pong`
- `group_message_send`
- `group_message_ack`
- `group_message_failed`
- `group_message_receive`
- `group_message_read`
- `group_message_read_ack`
- `group_member_kicked`
- `group_message_recalled`
- `group_join_request_approved` / `group_join_request_rejected`
- `error`

ACK 只代表消息已校验并成功落库，不代表所有成员已收到。实时推送失败由历史消息补拉兜底。

## 6. 权限模型

| 操作 | 群主 | 管理员 | 普通成员 |
|---|---:|---:|---:|
| 创建群 | 是 | - | - |
| 加入群 | 是 | 是 | 是 |
| 发送文本消息 | 是 | 是 | 是，受禁言/慢速限制 |
| 全员禁言 | 是 | 是 | 否 |
| 单人禁言 | 是 | 是 | 否 |
| 踢人 | 是 | 是 | 否 |
| 设置管理员 | 是 | 否 | 否 |
| 解散群 | 是 | 否 | 否 |
| 退群 | 群主不允许，需解散 | 是 | 是 |

## 7. 后续演进

- Outbox 扫描补偿 Kafka 发布失败。
- 多 WebSocket Gateway，Delivery 按 serverId 分片推送。
- 热点群独立 Topic 或按 groupId 子分片。
- 群消息按 group_id hash 分表 + 历史归档。
- 压测场景：1 万在线大群、ACK P95、Delivery latency、Kafka lag、Redis route query latency。
