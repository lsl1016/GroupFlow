# 聊天历史搜索（Elasticsearch 增强）设计方案

- 日期：2026-06-30
- 状态：设计已确认，待实现
- 目标：支持按**群成员 / 时间 / 文本**搜索聊天历史；前端展示搜索结果，点击某条结果直接跳转到群聊中对应位置；面向海量数据，用 Elasticsearch 增强检索性能。

## 1. 范围与关键决策

| 决策项 | 结论 |
|--------|------|
| 搜索范围 | **两者都要**：全局跨群搜索入口 + 单群内收窄搜索 |
| ES 同步方式 | **新增 Kafka 消费者**：复用现有 `group-message-topic`，独立 consumer group 异步写 ES，与发消息主链路解耦 |
| 权限边界 | **仅加群后的消息**：每个群只能搜 `sequence >= 我的 joinSequence` 的消息，与历史拉取可见性一致 |
| 系统消息 | 默认不进搜索结果（`message_type != system`） |
| 撤回/删除 | 同步更新 ES `status`，搜索固定过滤 `status=normal`，撤回即时从结果消失 |

## 2. 架构与数据流

### 现有链路（已存在）
消息发送 → MySQL 插入 + `message_outbox` 行（事务内）→ outbox relay → Kafka `group-message-topic` → delivery 消费者扇出推送。
Kafka 事件信封 `groupEvent` 的 `payload` 已包含完整消息体（content / sender / sequence / createdAt）。

### 新增组件
- **ES 索引消费者**（`cmd/es-indexer`，或并入 delivery 进程，独立 consumer group `groupflow-es-indexer`）：
  - 订阅 `group-message-topic`；
  - 处理 `group_message_created` → upsert 写入 ES；
  - 处理 `group_message_recalled` / 删除事件 → 按 `message_id` 更新 `status`；
  - 以 `message_id` 作 ES `_id`，幂等 upsert，天然去重、可重放重建。
- **Elasticsearch 集群**：docker-compose 新增 `elasticsearch`（可选 `kibana`），安装 **IK 中文分词插件**（建索引 `ik_max_word`，查询 `ik_smart`）。

### 读路径
前端 → `GET /api/v1/search/messages`（带权限过滤查 ES）→ 返回命中列表（group_id / sequence / 高亮片段）→ 点击某条 → 复用消息列表接口按 sequence 取上下文窗口跳转。

## 3. 索引设计

索引名 `group_message`（别名），底层按月 rollover：`group_message-YYYY-MM`。

| 字段 | 类型 | 用途 |
|------|------|------|
| message_id | keyword | `_id`，幂等 |
| group_id | long | 群过滤（单群 / 全局） |
| sequence | long | 排序 + 跳转定位 |
| sender_id | long | “按群成员”过滤 |
| sender_name | text + keyword | 显示 / 聚合 |
| content | text (ik) | 全文检索 + 高亮 |
| message_type | keyword | 排除 system |
| status | keyword | 排除 recalled / deleted |
| created_at | date | “按时间”范围过滤 + 排序 |

写入按 `group_id` 做 routing，单群搜索只命中对应 shard。

## 4. 后端 API 设计

### 4.1 搜索接口 `GET /api/v1/search/messages`

查询参数：
- `keyword`（可选，全文）
- `groupId`（可选；传 = 单群搜索，不传 = 全局跨群）
- `senderId`（可选，按群成员过滤）
- `startTime` / `endTime`（可选，时间范围）
- `cursor`（`search_after` 游标，基于 `[created_at, sequence]`）、`size`（默认 20，上限 50）

权限过滤（核心）：
- 用户只能搜自己 `status='normal'` 的群；
- 每个群只能搜 `sequence >= joinSequence`；
- 全局搜索：先取 `{groupId: joinSequence}` 映射 → ES 查询拼 `bool.should`，每群一个 `{group_id == X AND sequence >= joinSeq_X}` 子句；群数极多时用 `terms` + 后置过滤兜底；
- 单群搜索：校验 `IsActiveMember`，加单条 `sequence >= joinSeq` 约束；
- 固定排除：`status=normal` 且 `message_type != system`。

ES 查询：`content` 走 `match`（ik_smart），高亮返回 `<em>` 片段；排序 `created_at desc` + `search_after`。

响应：
```json
{
  "items": [{
    "messageId": "...", "groupId": 1, "groupName": "...",
    "sequence": 123, "senderId": 9, "senderName": "...",
    "contentHighlight": "...<em>关键词</em>...", "createdAt": "..."
  }],
  "nextCursor": "...", "hasMore": true
}
```

### 4.2 跳转 / 上下文接口
复用现有 `GET /groups/:groupId/messages`，扩展 `aroundSequence` 参数 → 返回该 sequence 前后各 N 条（一次 SQL `sequence BETWEEN seq-N AND seq+N`），前端定位高亮目标条。

### 4.3 新增模块
- `internal/search/`：ES client 封装 + query builder；
- service 层 `SearchMessages`；
- router 注册路由；
- `group_member.join_sequence`：加群时记录（需确认字段是否已存在，缺则新增一列）。

## 5. 前端交互（React 18 + Vite + TS + Zustand）

### 入口（两处）
- 全局搜索：顶部导航 / 侧栏搜索图标 → 打开全局搜索面板（不带 `groupId`）；
- 单群搜索：群聊页顶部搜索按钮 → 同一面板组件，预设 `groupId`。

### 搜索面板组件 `MessageSearchPanel`
- 顶部：关键词输入框（debounce 300ms）+ 筛选条：群成员下拉、时间范围选择器、（全局模式下）群下拉；
- 列表区：每条显示 `群名(全局) · 发送人 · 时间 · 高亮内容片段`，滚动到底触发 `search_after` 游标加载下一页；
- 空态 / 加载态 / 无更多。

### 点击跳转流程（核心体验）
1. 点击结果 → 取 `{groupId, sequence}`；
2. 不在该群会话页时 → 先路由切到该群；
3. 调 `GET /groups/:groupId/messages?aroundSequence=<seq>` 拉上下文窗口，**替换**当前消息列表（标记“跳转态”）；
4. `scrollIntoView({block:'center'})` 定位目标 sequence + 短暂高亮闪烁；
5. 提供“回到最新消息”悬浮按钮，恢复正常分页。

### 状态管理（Zustand）
- 新增 `searchStore`（query / filters / results / cursor / loading）；
- 会话 `messageStore` 增加“跳转模式”标志位：跳转态下暂存新推送消息，不直接 append，避免跳转窗口与实时增量互相污染。

## 6. 海量数据、一致性与上线

### 海量数据应对
- 索引按时间 rollover（`group_message-YYYY-MM` + 别名），冷数据降配 / 关副本；查询默认只打近 N 个月，带 `startTime` 时按月裁剪命中索引；
- `search_after` 游标翻页，禁用深 `from/size`；
- 写入按 `group_id` routing，单群搜索只命中对应 shard；
- 搜索接口加每用户 QPS 限流（复用现有 Redis 限流），`size` 上限 50。

### 一致性 / 撤回同步
- ES 消费者处理 `group_message_recalled` / 删除事件 → 按 `message_id` upsert 更新 `status`；
- 消费失败依赖 Kafka offset + 重试（参考 delivery 重试），`message_id` upsert 幂等，可安全重放；
- 对账：定时任务抽样比对 MySQL `MAX(id)` 与 ES doc 数，缺失触发增量回补。

### 回填（存量）
- 一次性脚本 `cmd/es-backfill`：按 `id` 游标批量扫 `group_message`，`status=normal` 用 ES `_bulk` 灌入（每批 1–5k）；建索引时临时调大 `refresh_interval` / 关副本，完成后恢复。

### 上线步骤
1. docker-compose 加 ES（+IK 插件）；加配置 `ES_ENABLED`、`ES_ADDRS`、索引名；
2. `group_member` 确认 / 补 `join_sequence` 字段（加群时落值）；
3. 部署 ES 消费者（双写一段时间，先不暴露 API）；
4. 跑 backfill；
5. 灰度开 `/search` API + 前端入口（feature flag，`ES_ENABLED=false` 时接口返回未启用、前端隐藏入口）。

## 7. 测试要点
- ES 消费者索引 / 撤回更新单测；
- 权限边界用例（加群前消息不可见）；
- search query builder 单测；
- 跳转上下文接口边界（seq 头尾）；
- 前端跳转态与实时推送互斥。
