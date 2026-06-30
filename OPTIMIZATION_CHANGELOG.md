# GroupFlow MVP 优化说明

本次优化基于一期 MVP 继续补齐群管理能力，并接入 Swagger / Swag 接口文档。

## 1. 后端能力

### 1.1 Swag / Swagger 文档

- 接入 `github.com/swaggo/gin-swagger`、`github.com/swaggo/files`、`github.com/swaggo/swag`。
- 后端启动后可访问：
  - `http://localhost/swagger/index.html`
  - `http://localhost/api/v1/health`
- `cmd/api/main.go` 增加 OpenAPI 基础注解。
- `internal/api/router.go` 对主要接口增加了 Swag 注解。
- `backend/docs` 内置生成后的 `docs.go / swagger.json / swagger.yaml`，本地没有安装 `swag` 命令也能打开文档。

### 1.2 @提醒

- WebSocket `group_message_send` 支持：
  - `mentionAll`
  - `mentionUserIds`
- `@某人`：写入 `group_mention` 表，用于群列表“有人@我”和提醒列表。
- `@所有人`：只在 `group_message.mention_all = 1` 标记，不同步展开全员，避免大群写放大。
- 增加接口：
  - `GET /api/v1/groups/{groupId}/mentions`
  - `POST /api/v1/groups/{groupId}/mentions/read`

### 1.3 群公告

- 增加群公告 CRUD：
  - `GET /api/v1/groups/{groupId}/announcements`
  - `POST /api/v1/groups/{groupId}/announcements`
  - `PUT /api/v1/groups/{groupId}/announcements/{announcementId}`
  - `DELETE /api/v1/groups/{groupId}/announcements/{announcementId}`
- 创建、修改、删除公告需要群主或管理员权限。
- 支持置顶公告字段 `pinned`。

### 1.4 加群审批

- `joinMode = approval` 时，用户调用加入群接口会生成 `group_join_request`，不会直接入群。
- 管理员 / 群主可以查看、通过、拒绝审批。
- 增加接口：
  - `GET /api/v1/groups/{groupId}/join-requests`
  - `POST /api/v1/groups/{groupId}/join-requests/{requestId}/approve`
  - `POST /api/v1/groups/{groupId}/join-requests/{requestId}/reject`
- 审批通过后创建成员关系、更新群人数，并推送群事件。

### 1.5 消息撤回

- 增加接口：
  - `POST /api/v1/groups/{groupId}/messages/{messageId}/recall`
- 发送者可以撤回自己的消息。
- 群主 / 管理员可以撤回他人消息。
- 撤回后：
  - `group_message.status = recalled`
  - 写入 `group_message_recall` 审计表
  - WebSocket 广播 `group_message_recalled`

## 2. 前端能力

- 聊天输入区增加：
  - `@所有人` 开关
  - 指定 `@用户ID` 输入
- 消息展示支持：
  - `@所有人` 标签
  - `@用户` 标签
  - 撤回态展示
  - 撤回按钮
- 右侧信息栏增加：
  - 群公告管理
  - 加群审批管理
- 群列表展示：
  - `有人@我`
  - `@所有人`

## 3. 数据库与基础设施

- `deploy/mysql/init.sql` 增加：
  - `group_mention`
  - `group_message_recall`
- Kafka Topic 初始化脚本增加：
  - `group-mention-topic`
  - `group-message-recall-topic`
  - `group-system-event-topic`
  - `group-audit-topic`
  - 对应 DLQ Topic
- Nginx 增加 `/swagger/` 代理。

## 4. 中文注释补充

在关键链路处补充中文注释，主要覆盖：

- `clientMessageId` 幂等处理
- Redis `group sequence` 生成
- `@所有人` 大群不展开策略
- 加群审批流转
- 消息撤回权限与审计
- 前端 WebSocket 重连补拉与 sequence gap 处理

## 5. 本地验证建议

```bash
cd groupflow-mvp-optimized
cp .env.example .env
docker compose up -d --build
```

启动后访问：

- 前端：`http://localhost`
- Swagger：`http://localhost/swagger/index.html`
- Prometheus：`http://localhost:9090`
- Grafana：`http://localhost:3000`

如果本地已有旧数据库 volume，新增表可能不会自动创建，可以先清理本地测试数据：

```bash
docker compose down -v
docker compose up -d --build
```
