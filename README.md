# GroupFlow / 群流 一期 MVP

这是按现有产品原型、技术架构、数据库、HTTP API、WebSocket、Redis Key、Kafka Topic、大群投递与部署文档整理并落地的 **一期 MVP 项目**。

## 一期范围覆盖

- 群列表、群详情、创建群、加入群
- 群成员游标分页、群主/管理员/普通成员角色权限
- 文本消息、系统消息、WebSocket 实时推送
- 服务端 ACK、clientMessageId 去重、group sequence
- 历史消息 beforeSequence / afterSequence 游标分页
- lastReadSequence、未读数、断线重连补拉
- 全员禁言、单人禁言、踢人、退群、解散群
- 大群模式、慢速模式
- @提醒、群公告、加群审批、消息撤回
- Swag / Swagger 在线接口文档：`/swagger/index.html`
- 初版即预留大群优化：消息只存一份、Kafka 异步投递、Delivery 批量推送、Redis 在线路由、游标分页、慢速模式限流、Repository 分表预留

## 技术栈

后端：Go 1.23、Gin、Gorilla WebSocket、MySQL、Redis、Kafka、Zap、Prometheus

前端：React 18、TypeScript、Vite、Zustand、原生 WebSocket 封装、虚拟消息列表

基础设施：Docker Compose、MySQL、Redis、Kafka、Nginx、Prometheus、Grafana

## 目录

```text
groupflow-mvp
├── backend             # Go 后端：HTTP API + WebSocket + Message Service + Delivery
├── frontend            # React 前端 MVP
├── deploy              # MySQL / Kafka / Nginx / Prometheus 配置
├── docs                # 一期 MVP 设计说明
├── scripts             # 启动、压测辅助脚本
├── docker-compose.yml
└── .env.example
```

## 快速启动

```bash
cp .env.example .env
docker compose up -d --build
```

启动后访问：

- 前端：http://localhost
- API 健康检查：http://localhost/api/v1/health
- Swagger 文档：http://localhost/swagger/index.html
- Prometheus：http://localhost:9090
- Grafana：http://localhost:3000，默认账号密码 admin / admin

如果修改了 `deploy/mysql/init.sql`，需要重置数据卷：

```bash
docker compose down -v
docker compose up -d --build
```

## 本地开发

只启动依赖：

```bash
docker compose up -d mysql redis kafka kafka-init
```

后端：

```bash
cd backend
go mod tidy
go run ./cmd/api
# 可选，Kafka 异步投递消费者
go run ./cmd/delivery
```

前端：

```bash
cd frontend
npm install
npm run dev
```

## 测试用户

初始化 SQL 预置了 5 个用户：

- user_001
- user_002
- user_003
- user_004
- user_005

登录时只填用户名即可，方便多浏览器窗口联调。

## WebSocket 协议示例

连接：

```text
ws://localhost/api/ws?token={token}&deviceId=web_001&clientType=web&protocolVersion=v1
```

发送群消息：

```json
{
  "type": "group_message_send",
  "version": "v1",
  "requestId": "req_1710000000000_abc",
  "timestamp": 1710000000000,
  "data": {
    "groupId": 10001,
    "clientMessageId": "client_msg_1710000000000_abc",
    "messageType": "text",
    "content": "大家好",
    "mentionAll": false,
    "mentionUserIds": []
  }
}
```

ACK：

```json
{
  "type": "group_message_ack",
  "requestId": "req_1710000000000_abc",
  "timestamp": 1710000000001,
  "data": {
    "messageId": "msg_xxx",
    "clientMessageId": "client_msg_1710000000000_abc",
    "groupId": 10001,
    "sequence": 12,
    "status": "success"
  }
}
```

## 新增能力说明

### @提醒

文本消息支持 `mentionAll` 和 `mentionUserIds`。`@某人` 会写入 `group_mention`，群列表返回 `mentionCount / mentionSummaryText`；大群里的 `@所有人` 不同步展开成员，只在消息上记录 `mention_all=1`，并通过 Redis 做群维度限频。

### 群公告

管理员和群主可以通过 `POST /api/v1/groups/{groupId}/announcements` 发布公告，公告会写入 `group_announcement`，同时生成系统消息推送到群内。

### 加群审批

当群 `joinMode=approval` 时，加入群会生成 `group_join_request`；群主/管理员可在右侧面板审批，也可以通过 HTTP API 操作。

### 消息撤回

撤回接口为 `POST /api/v1/groups/{groupId}/messages/{messageId}/recall`。撤回会将 `group_message.status` 更新为 `recalled`，并写入 `group_message_recall` 审计表，再通过 WebSocket 广播 `group_message_recalled`。

### 聊天历史搜索（Elasticsearch）

支持按 **关键词 / 群成员 / 时间** 搜索历史消息，支持**单群内**或**全局跨群**两种范围，搜索结果点击可
**跳转到群聊对应位置**。检索由 Elasticsearch 提供（`content` 使用 IK 中文分词）。

- 搜索接口：`GET /api/v1/search/messages?keyword=&groupId=&senderId=&startTime=&endTime=&cursor=&limit=`
  - 权限：只能搜自己所在的群，且每群仅可见 **加群之后**（`sequence >= join_sequence`）的消息；系统消息与已撤回消息不返回；`search_after` 游标分页。
- 跳转上下文：`GET /api/v1/groups/{groupId}/messages?aroundSequence={seq}` 返回目标消息前后窗口。
- 数据同步：新增独立 Kafka 消费者 `cmd/es-indexer`（独立 consumer group），订阅消息 topic 将
  `group_message_created` 写入 ES、`group_message_recalled` 更新状态，以 `messageId` 幂等 upsert，可重放重建。
- 存量回填：`cmd/es-backfill` 按主键游标批量 `_bulk` 灌入存量消息。

新增环境变量：

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `ES_ENABLED` | `false` | 是否启用搜索后端；关闭时搜索接口返回 `SEARCH_DISABLED` |
| `ES_ADDRS` | `http://localhost:9200` | Elasticsearch 节点地址（逗号分隔） |
| `ES_INDEX` | `group_message` | 消息索引名 / 别名 |
| `ES_INDEXER_CONSUMER_GROUP` | `groupflow-es-indexer` | 索引消费者的 Kafka consumer group |

> 注：`group_member` 新增 `join_sequence` 列（入群时记录群内最大序号），用于约束“仅可搜索加群后消息”。
> 修改了 `deploy/mysql/init.sql`，需 `docker compose down -v` 重置数据卷。

### Outbox 可靠投递与多节点路由

开启 Kafka（`KAFKA_ENABLED=true`）后，消息事件会与消息落库在 **同一事务** 内写入 `message_outbox`，
由 API 进程内的后台 relay 轮询 `message_outbox` 可靠地投递到 Kafka（失败指数退避重试），避免
"库写成功但 Kafka 发送失败" 导致的实时漏推。Kafka 关闭时不写 outbox，由 router 直推，行为不变。

Delivery 按 `online:user→serverId` 将消息路由到用户实际所在的 WS 节点（读取该节点广播的
`server:%s:push_url`），支持多节点水平扩容；单节点部署行为与之前一致。

新增环境变量（均有安全默认值，保持向后兼容）：

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `WS_ADVERTISE_PUSH_URL` | 同 `WS_INTERNAL_PUSH_URL` | 本 WS 节点对外暴露的内部推送地址，供 Delivery 跨节点路由 |
| `MESSAGE_SHARD_COUNT` | `1` | `group_message` 分表数（分表预留）。`1` 为单表；`>1` 按 `group_id` 哈希路由到 `group_message_NN`，需先建好物理分表 |

## Swag 文档

项目已接入 `github.com/swaggo/gin-swagger`。启动 API 后访问：

```text
http://localhost/swagger/index.html
```

如需重新生成文档：

```bash
cd backend
go install github.com/swaggo/swag/cmd/swag@latest
swag init -g cmd/api/main.go -o docs
```

## 设计取舍

一期为了保证项目完整可运行，API Service 与 Message Service 在同一个 Go 进程里；WebSocket Gateway 也与 API 同进程运行。Kafka 和 Delivery Service 已经保留并实现，默认 Docker Compose 会启用异步投递链路。后续横向扩容时，可拆出独立 API、WS Gateway、Message Service 和 Delivery Service。

