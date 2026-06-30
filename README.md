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

