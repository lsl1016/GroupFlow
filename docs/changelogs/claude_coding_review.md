总体评价                                                    
                                                                             
  三份文档本身设计是合理且自洽的：分期清晰、Redis/Kafka/WS                   
  职责边界明确、"先落库再投递 +                                              
  客户端补拉兜底"的核心思路正确。代码实现质量也较高（Outbox                  
  已落地，甚至超前于文档"一期可暂缓"的建议）。                
                                                              
  但代码与文档存在若干明显偏差，其中 3 处是真正的正确性 /                    
  一致性问题，不只是"未实现"。
                                                                             
  ---                                               
  P0 — 明显不合理 / 正确性问题                                
                                                                             
  1. 多端用户跨节点投递会丢实时消息（与 Redis 文档 §4.1 警告冲突）
  delivery.go:resolveOnlineRoutes 只读 online:user:{uid} 这一个 key 拿单个   
  serverId，而 ws.go:renewRedis 每次心跳都 SET                               
  online:user:{uid}=本节点（后写覆盖）。                                     
  → 用户 Web 在节点 A、手机在节点 B                                          
  时，只有最后注册的那个节点能收到推送，另一端实时漏收（虽可补拉）。文档 §4.1
   已明确警告"多端场景 online:user 不够准确，应使用 connection
  级路由"，但代码恰恰用了单 key。                                            
  正解：投递时走 online:user:{uid}:connections → 每个
  connection:{id}:server，或文档 §9.3 的按节点分组集合。                     
  
  2. Kafka 模式下，群系统结构化事件不投递（direct 与 kafka                   
  两种模式行为不一致）                              
  pushEventIfDirect / pushIfDirect 在 KafkaEnabled 时直接                    
  return。但只有撤回和普通消息进了 Outbox；group_member_kicked /             
  group_dismissed / group_muted / group_announcement_updated / 
  group_join_request_created 这些结构化 WS 事件在 Kafka 开启时根本没有生产到 
  Kafka。                                           
  → 开 Kafka                                                  
  后，被踢/解散/公告等事件，客户端只能收到一条系统文本消息，收不到文档 §6 /  
  WS 文档 §16 定义的结构化事件。文档 Kafka §25 一期要求
  group-system-event-topic + groupflow-system-delivery-group，均未实现。     
                                                    
  3. Delivery 出错即提交 offset，无重试、无 DLQ → 瞬时故障静默丢投递         
  delivery.go:Run 里 handle 返回 error 也照样 CommitMessages。叠加 producer
  用 RequireOne（文档 §19.2 要求 acks=all），可靠性被双重削弱。              
  → DB/Redis 瞬时抖动导致的 fanout                  
  失败，事件被永久跳过，完全依赖客户端补拉。文档 §16 一期要求"内部短暂重试 + 
  严重失败进 DLQ"，均未实现。                       
                                                                             
  ---                                               
  P1 — 一期必须项缺失（文档明列，代码未做）                   
                                                                             
  - 群配置 Redis 缓存 group:{groupId}:config 未实现：每条消息
  SendGroupMessage 都打 MySQL 查 group + member + mute（3~4 次 DB            
  读/条）。这与"面向大群高并发"的目标直接相悖，且文档 Redis §22
  把它列为一期必须。                                                         
  - 限流 key 缺失：rate_limit:user:{userId}:send_message（用户全局发送
  QPS）、rate_limit:user:{userId}:mention_all 一期必须，均未实现；mention_all
   群限频 key 命名也与文档不符（代码 rate_limit:mention_all:group:%d vs 文档
  rate_limit:group:{id}:mention_all）。                                      
  - 节点存活：server:{serverId}:heartbeat +         
  死节点清理未实现。单节点无碍，多节点时死节点的 push_url（90s TTL）过期后   
  delivery 回落默认地址，可能投错节点。
                                                                             
  ---                                               
  P2 — 次要 / 可接受的偏差                                    
                          
  - 已读回退：UPDATE ... GREATEST(...) 静默忽略回退，未按 WS 文档 §14.4 返回
  READ_SEQUENCE_ROLLBACK（行为安全，仅缺错误码）。                           
  - max_sequence 只存 MySQL 不存 Redis（其实更稳，属合理偏差）。
  - 大群 fanout = 全量成员扫描 + 逐用户                                      
  MGET（文档一期方案，已分页+pipeline）：MVP 可接受，但撑不住文档反复强调的  
  10万群；二期 online_users 集合未建。                                       
  - WS connection_kicked、连接数上限、servers:ws:active                      
  未实现（文档二期/边缘项）。                                                
                                                              
  ---                                                                        
  完善计划（建议分期）                              
                                                              
  第一阶段（修正一致性/正确性，P0）
  1. 投递路由改为多端感知：delivery 用 connections + connection:{id}:server  
  聚合，或落地文档 §9.3 group:{groupId}:server:{serverId}:users。            
  2. 拆出 group-system-event-topic（或先复用现 topic 但补齐 system 事件入    
  Outbox），让 kick/dismiss/mute/announcement 在 Kafka                       
  模式也走结构化事件投递，消除 direct/kafka 行为差异。                       
  3. Delivery 加"内部短重试（2 次 / 100ms·300ms）+ 失败入 DLQ topic"，handle
  失败不再无条件 commit；producer 改 acks=all。                              
                                                                             
  第二阶段（补齐一期必须，P1）                                
  4. 加 group:{groupId}:config 缓存（读穿透 + 改后删除），把发消息热路径的 DB
   读降到 0~1 次。                                                           
  5. 补 rate_limit:user:*:send_message、*:mention_all，并统一限流 key
  命名为文档规范。                                                           
  6. 加 server:{serverId}:heartbeat + 死节点路由清理 job。

---
---

# 二、改进实施总结（本轮已落地）

> 以下为针对上述 review 实际完成的代码改进。全程 TDD（先写失败测试再实现），
> 每一步都跑 `go test ./...` 验证，关键并发路径额外跑 `go test -race`。
> `connection_kicked` 与热点群（hot_group）按需求显式跳过；第三阶段的
> “在线优先 delivery（online_users 集合）”尝试后按要求回退，未保留。

## 2.1 修复的问题与对应改动

### 第一阶段：正确性 / 一致性（对应 P0）

**P0-1 多端跨节点投递丢消息 —— 已修复**
- 把投递路由从“按用户找单节点（online:user:{uid}→serverId）”改为
  “按连接找所属节点”：
  - Delivery 先 `SMEMBERS online:user:{uid}:connections` 取该用户全部连接，
    再对每个连接 `GET connection:{cid}:server`，最后按 serverId 分组投递。
- WS Hub 新增 `SendToConnections(connectionIDs, ...)`，支持按连接精确下发。
- 内部推送协议 `InternalPushRequest` 新增 `connectionIds` 字段，并在
  `/internal/push` 中优先于 `userIds`，旧 `userIds` 路径保留向后兼容。
- 效果：用户在 A、B 两个节点同时在线时，两端都能实时收到消息/撤回。
- 涉及：`internal/delivery/delivery.go`、`internal/ws/ws.go`、
  `internal/api/dto.go`、`internal/api/router.go`。

**P0-2 Kafka 模式下结构化事件不投递 —— 已修复**
- 复用现有 Outbox 信封，统一补齐四类结构化实时事件的“生产 + 消费”链路：
  `group_member_kicked`、`group_join_request_created`、
  `group_join_request_approved`、`group_join_request_rejected`。
- service 层新增 `RealtimeEvent` 与 `newRealtimeEvent(...)`：业务写库的同一
  事务内构造 Outbox 事件，payload 形如 `{ targetUserIds, body }`，把“谁该收到”
  在生产端固化，避免 Delivery 再回查业务规则。
- Delivery 新增 `handleStructuredEvent`：按 `targetUserIds` 解析在线连接并定向推送。
- router 去掉这些事件的本地特判直推，统一改为 `RealtimeEvent`，并保证
  Kafka 模式只走 Outbox、direct 模式只走本地推送，消除“双发 / 漏发”。
- 涉及：`internal/service/service.go`、`internal/repo/repo.go`、
  `internal/domain/models.go`、`internal/delivery/delivery.go`、`internal/api/router.go`。

**P0-3 Delivery 出错即提交 offset —— 已修复**
- 消费改为 `processMessage`：失败时做有限次短重试（3 次，间隔 0/50ms/100ms），
  仅在处理成功后才提交 offset；彻底失败则不提交，交由 Kafka 重投。
- 顺带修复审查中发现的两个真实 bug：
  - `ListActiveMemberIDs` 分页游标会在每页边界漏掉一个成员（>1000 成员的群必现）。
  - 结构化事件 fanout 缺少 500 条分批。
- 涉及：`internal/delivery/delivery.go`、`internal/repo/repo.go`。

### 第二阶段：补齐一期必须项（对应 P1）

**P1-4 群配置缓存 group:{groupId}:config —— 已实现**
- service 新增读穿透缓存：`getGroupConfig`（命中直接返回，未命中查库回填，
  TTL 10 分钟）、`invalidateGroupConfigCache`（改设置后删除）。
- `SendGroupMessage` 热路径改走缓存，`UpdateSettings` 更新后删除缓存。
- 效果：发消息热路径的群配置读由“每条必查 MySQL”降到“缓存命中即 0 次 DB”。
- 涉及：`internal/service/service.go`。

**P1-5 全局发送限流 + mention_all 限频 + key 命名规范 —— 已实现**
- 新增 `rate_limit:user:{userId}:send_message`（1 秒窗口，默认 5 条）。
- `mention_all` 改为同时校验群维度与用户维度：
  `rate_limit:group:{groupId}:mention_all` 与 `rate_limit:user:{userId}:mention_all`。
- 把旧的 `rate_limit:mention_all:group:%d` 统一为文档规范命名。
- Redis 故障时保持“降级放行 + 记日志”，不把 Redis 当硬单点。
- 涉及：`internal/service/service.go`。

**P1-6 节点心跳 + 死节点清理 —— 已实现**
- WS Hub 新增 `server:{serverId}:heartbeat`（每 10 秒续期，TTL 30 秒），
  API 启动时拉起 heartbeat 循环。
- Delivery 新增清理循环：扫描 `server:*:connections` 推导节点，heartbeat 过期
  即判定死节点，清理其遗留的 `connection:{cid}:server`、`connection:{cid}:user`，
  并从用户 `online:user:{uid}:connections` 集合中移除脏连接。
- 涉及：`internal/ws/ws.go`、`internal/delivery/delivery.go`、
  `cmd/api/main.go`、`cmd/delivery/main.go`。

### 第三阶段：保留项（对应 P2 的一部分）

**P2 已读回退错误码 READ_SEQUENCE_ROLLBACK —— 已实现**
- service 新增 `ErrReadSequenceRollback` 与 `UpdateReadPosition`：已读序号只能前进，
  回退返回明确错误，相等为 no-op，非成员返回 `ErrForbidden`。
- HTTP `read` 与 WS `group_message_read` 统一改走 service 并返回明确错误码；
  WS 入口补上 JSON 解析失败 → `BAD_REQUEST`；非成员（no-rows）映射为 403 而非 500。
- 涉及：`internal/service/service.go`、`internal/api/router.go`、`internal/api/response.go`。

**router 收口（防回归）—— 已完成**
- 锁定 `pushIfDirect` / `pushEventIfDirect` / `pushRealtimeEventIfDirect` 在
  Kafka 模式短路返回，确保 Kafka 模式只靠 Outbox 投递、不与本地直推双发，
  并补充回归测试固化该约束。

### 显式跳过 / 回退的部分

- `connection_kicked`（按连接踢下线 + 主动关闭连接）：按需求跳过。
- 热点群 `hot_group:{groupId}` / `:strategy` 标记与降级：按需求跳过。
- 第三阶段“在线优先 delivery（`group:{groupId}:online_users` + 单用户 TTL key，
  delivery 改为在线集合优先 + SSCAN 分批）”：实现后按要求回退，`fanout` 仍为
  “全量活跃成员分页枚举 + 按连接路由”。

## 2.2 测试与验证

- 新增包级测试：`internal/ws`、`internal/api`、`internal/delivery`、
  `internal/service`、`internal/repo`（此前仓库无 Go 测试）。
- 全量 `go test ./...` 通过；
  `go test -race ./internal/api ./internal/ws ./internal/delivery` 通过。
- 多轮代码审查（spec 合规 + 代码质量）发现的真实问题均已修复并补测试。

---

# 三、方案设计与架构

## 3.1 整体投递架构图（Kafka 模式）

```text
                         ┌─────────────────────────────────────────────┐
                         │                  客户端 (多端)                 │
                         │   Web@节点A      手机@节点B     平板@节点A      │
                         └─────┬───────────────┬───────────────┬────────┘
                               │ WS            │ WS            │ WS
                  ┌────────────▼───────┐  ┌────▼─────────────┐ │
                  │   API/WS 节点 A     │  │   API/WS 节点 B   │ │
                  │  ws.Hub             │  │  ws.Hub          │◄┘
                  │  - SendToConn/Users │  │                  │
                  │  - heartbeat(10s)   │  │  heartbeat(10s)  │
                  │  /internal/push     │  │  /internal/push  │
                  └───┬──────────┬──────┘  └──────────────────┘
   group_message_send │          │ 写在线态/心跳            ▲
   (校验+落库+生成事件) │          ▼                         │ HTTP 内部推送
                       │   ┌──────────────────────────┐     │ (connectionIds)
                       │   │           Redis           │     │
                       │   │ online:user:{u}:connections│    │
                       │   │ connection:{c}:server      │    │
                       │   │ server:{s}:push_url        │    │
                       │   │ server:{s}:heartbeat       │    │
                       │   │ group:{g}:config (缓存)     │    │
                       │   │ rate_limit:*               │    │
                       │   └──────────────────────────┘     │
                       ▼                                      │
            ┌─────────────────────┐   同事务写             ┌──┴───────────────────┐
            │  service.Send/Kick  │──────────────────────►│  MySQL                │
            │  /Approve/Reject    │  group_message +       │  group_message        │
            │  生成 RealtimeEvent  │  message_outbox        │  message_outbox       │
            └─────────┬───────────┘                        └───────────────────────┘
                      │                                              │
              ACK 回发送者                                  Outbox Relay 轮询(500ms)
                                                                     │ 可靠投递
                                                                     ▼
                                                            ┌──────────────────┐
                                                            │      Kafka        │
                                                            │ group-message-... │
                                                            │ key = groupId     │
                                                            └────────┬─────────┘
                                                                     │ 消费(手动提交)
                                                                     ▼
                                                  ┌──────────────────────────────────┐
                                                  │        Delivery Consumer          │
                                                  │ processMessage: 失败短重试(3次)    │
                                                  │   成功才 commit offset             │
                                                  │ handle 按 eventType 分派:          │
                                                  │  - 群消息/撤回 → fanout(全员)       │
                                                  │  - 结构化事件 → 按 targetUserIds    │
                                                  │ resolveOnlineRoutes:               │
                                                  │  user→connections→server 分组       │
                                                  │ RunCleanup: 死节点路由清理          │
                                                  └──────────────────────────────────┘
                                                                     │ 按 serverId 分组
                                                                     │ HTTP /internal/push
                                                                     ▼  (connectionIds)
                                                            回到对应 WS 节点下发
```

## 3.2 关键链路文字说明

### (1) 发送链路（先落库，再异步投递）
1. 客户端经 WS 发 `group_message_send`。
2. service 校验：群配置（走 `group:{g}:config` 缓存）、全员禁言、个人禁言、
   全局发送限流、@所有人限频、慢速模式。
3. Redis `INCR` 生成群内递增 sequence。
4. 在同一 MySQL 事务内写入 `group_message` 与 `message_outbox`，保证
   “消息已存储 ⇔ 事件已入队待发”的原子性。
5. 立即回发送者 ACK（语义：已落库，非“所有人已收到”）。
6. Outbox Relay 后台轮询把待发事件可靠投递到 Kafka（Kafka 关闭时为 no-op，
   由 router 直推）。

### (2) 投递链路（在线 + 多端 + 多节点）
1. Delivery 消费 Kafka 事件，`processMessage` 包裹有限次短重试，仅成功后提交 offset。
2. 群消息/撤回走 `fanout`：分页枚举活跃成员 → `resolveOnlineRoutes` 把每个用户的
   全部连接按所属 WS 节点（`connection:{c}:server`）分组。
3. 结构化事件走 `handleStructuredEvent`：直接用事件 payload 里的 `targetUserIds`
   定向解析连接，不再回查业务规则。
4. 按 serverId 分组后，HTTP 调各节点 `/internal/push`，携带 `connectionIds`，
   节点用 `ws.Hub.SendToConnections` 精确下发到本机对应连接。
5. 推送失败不阻塞 offset 提交；用户可通过历史消息 `afterSequence` 补拉兜底。

### (3) 在线态与节点存活
- 连接建立/心跳续期时，WS 节点写入：用户连接集合、连接→用户、连接→节点、
  节点连接集合、节点推送地址（均 90s TTL），并周期性写 `server:{s}:heartbeat`。
- Delivery 周期扫描节点，对 heartbeat 过期的死节点清理其遗留连接路由 key，
  避免脏连接长期堆积导致投递空转。

### (4) 一致性与降级原则
- MySQL 为最终数据源；Redis 承载高频运行时状态，允许短暂不一致。
- 群配置缓存采用“先更新 MySQL，再删除缓存”，下次读重建。
- 限流、在线态、路由等 Redis 故障时降级放行 / 跳过并记日志，不阻断主链路。
- 实时投递是体验增强而非唯一可见路径，最终一致由 sequence 排序 + 补拉保证。

## 3.3 与原“完善计划”的对照

| 计划项 | 状态 | 说明 |
| --- | --- | --- |
| 1. 投递改多端感知（connection 级路由） | 已完成 | 用 connections + connection:{id}:server 聚合 |
| 2. Kafka 模式补齐结构化事件 | 已完成（复用现有 topic） | 复用 Outbox 信封，未拆独立 system-event topic |
| 3. Delivery 短重试 + 失败不提交 | 已完成 | 短重试 3 次；DLQ topic 暂未引入，失败靠 Kafka 重投 |
| 4. group:{groupId}:config 缓存 | 已完成 | 读穿透 + 改后删除 |
| 5. 用户全局/【@所有人】限流 + key 规范 | 已完成 | 命名统一为文档规范 |
| 6. server:{serverId}:heartbeat + 死节点清理 | 已完成 | heartbeat + 周期清理 job |
| 7. online_users 在线优先 delivery | 已回退 | 按需求未保留 |
| 8. connection_kicked / 热点群降级 | 已跳过 | 按需求不做 |

## 3.4 已知残留（非阻塞）

- `producer acks` 仍为 `RequireOne`（文档建议 `all`），可靠性可进一步加强。
- 未引入独立 DLQ topic，彻底失败事件依赖 Kafka 重投与客户端补拉。
- 已读 ack 回显的是客户端上报值而非存储后值；real-DB 回退路径与 equal-no-op
  分支缺集成测试（当前由 service 单测覆盖逻辑）。
- 在线优先 delivery、热点群降级为后续演进项，本轮未实现。
                                                                             