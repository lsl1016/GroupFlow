# 一期范围实现矩阵

| 序号 | 能力 | 落地位置 |
|---:|---|---|
| 1 | 群列表 | GET /api/v1/groups，frontend 左侧群列表 |
| 2 | 群详情 | GET /api/v1/groups/{groupId}，frontend 右侧详情 |
| 3 | 创建群 | POST /api/v1/groups |
| 4 | 加入群 | POST /api/v1/groups/{groupId}/join |
| 5 | 群成员分页 | GET /api/v1/groups/{groupId}/members?cursor=&limit= |
| 6 | 群角色权限 | service.requireRole + role endpoints |
| 7 | 文本消息 | WS group_message_send messageType=text |
| 8 | 系统消息 | service.createSystemMessage |
| 9 | WebSocket 实时推送 | /ws + Hub + Delivery internal push |
| 10 | 消息 ACK | group_message_ack |
| 11 | clientMessageId 去重 | uk_sender_client_msg + FindMessageByClientID |
| 12 | group sequence | Redis INCR group:{groupId}:sequence + uk_group_sequence |
| 13 | 历史消息游标分页 | beforeSequence / afterSequence |
| 14 | lastReadSequence | group_member.last_read_sequence |
| 15 | 未读数 | chat_group.max_sequence - group_member.last_read_sequence |
| 16 | 断线重连补拉 | frontend wsClient reconnectPull + HTTP afterSequence |
| 17 | 全员禁言 | chat_group.mute_all + PATCH settings |
| 18 | 单人禁言 | group_mute_record + mute endpoints |
| 19 | 踢人 | DELETE /members/{userId} + group_member_kicked |
| 20 | 退群 | POST /leave |
| 21 | 解散群 | DELETE /groups/{groupId} |
| 22 | 大群模式 | chat_group.group_type=large，Kafka/Delivery 异步投递 |
| 23 | 慢速模式 | chat_group.slow_mode_seconds + Redis TTL 限流 |

| 24 | @提醒 | WS group_message_send mentionAll / mentionUserIds + group_mention + 群列表 @提示 |
| 25 | 群公告 | group_announcement + /announcements API + 前端右侧公告面板 |
| 26 | 加群审批 | group_join_request + /join-requests API + 前端审批列表 |
| 27 | 消息撤回 | group_message.status=recalled + group_message_recall + group_message_recalled WS 事件 |
| 28 | Swag 文档 | /swagger/index.html + backend/docs docs.go/json/yaml |
