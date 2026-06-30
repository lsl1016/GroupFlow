CREATE DATABASE IF NOT EXISTS groupflow DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE groupflow;

CREATE TABLE IF NOT EXISTS user_account (
  id BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '用户ID',
  username VARCHAR(64) NOT NULL COMMENT '用户名',
  nickname VARCHAR(64) NOT NULL COMMENT '昵称',
  avatar VARCHAR(255) DEFAULT NULL COMMENT '头像',
  status VARCHAR(32) NOT NULL DEFAULT 'normal' COMMENT 'normal/banned/deleted',
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  UNIQUE KEY uk_username (username),
  KEY idx_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='用户表';

CREATE TABLE IF NOT EXISTS chat_group (
  id BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '群ID',
  name VARCHAR(64) NOT NULL COMMENT '群名称',
  avatar VARCHAR(255) DEFAULT NULL COMMENT '群头像',
  description VARCHAR(255) DEFAULT NULL COMMENT '群简介',
  owner_id BIGINT NOT NULL COMMENT '群主用户ID',
  group_type VARCHAR(32) NOT NULL DEFAULT 'normal' COMMENT '群类型 normal普通/large大群',
  join_mode VARCHAR(32) NOT NULL DEFAULT 'direct' COMMENT '加群方式 direct直接加入/approval需审批/invite仅邀请',
  status VARCHAR(32) NOT NULL DEFAULT 'normal' COMMENT '群状态 normal正常/dismissed已解散/banned封禁/archived归档',
  mute_all TINYINT NOT NULL DEFAULT 0 COMMENT '全员禁言 0否 1是',
  slow_mode_seconds INT NOT NULL DEFAULT 0 COMMENT '慢速模式发言间隔(秒) 0关闭',
  allow_member_invite TINYINT NOT NULL DEFAULT 1 COMMENT '是否允许成员邀请 0否 1是',
  mention_all_role VARCHAR(32) NOT NULL DEFAULT 'admin' COMMENT '允许@全体成员的最低角色',
  member_count INT NOT NULL DEFAULT 1 COMMENT '当前成员数',
  max_member_count INT NOT NULL DEFAULT 500 COMMENT '群最大成员数',
  max_sequence BIGINT NOT NULL DEFAULT 0 COMMENT '群内消息最大序号',
  last_message_id VARCHAR(64) DEFAULT NULL COMMENT '最后一条消息ID',
  last_message_summary VARCHAR(255) DEFAULT NULL COMMENT '最后一条消息摘要',
  last_message_at DATETIME DEFAULT NULL COMMENT '最后一条消息时间',
  created_at DATETIME NOT NULL COMMENT '创建时间',
  updated_at DATETIME NOT NULL COMMENT '更新时间',
  KEY idx_owner_id (owner_id),
  KEY idx_status (status),
  KEY idx_group_type (group_type),
  KEY idx_last_message_at (last_message_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='群表';

CREATE TABLE IF NOT EXISTS group_member (
  id BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '成员记录ID',
  group_id BIGINT NOT NULL COMMENT '群ID',
  user_id BIGINT NOT NULL COMMENT '用户ID',
  role VARCHAR(32) NOT NULL DEFAULT 'member' COMMENT '角色 owner群主/admin管理员/member普通成员',
  status VARCHAR(32) NOT NULL DEFAULT 'normal' COMMENT '成员状态 normal正常/left退群/kicked被踢/muted禁言/deleted删除',
  last_read_sequence BIGINT NOT NULL DEFAULT 0 COMMENT '已读消息最大序号',
  joined_at DATETIME NOT NULL COMMENT '入群时间',
  left_at DATETIME DEFAULT NULL COMMENT '退群时间',
  created_at DATETIME NOT NULL COMMENT '创建时间',
  updated_at DATETIME NOT NULL COMMENT '更新时间',
  UNIQUE KEY uk_group_user (group_id, user_id),
  KEY idx_user_status (user_id, status),
  KEY idx_group_role_id (group_id, role, id),
  KEY idx_group_status_id (group_id, status, id),
  KEY idx_group_last_read (group_id, last_read_sequence)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='群成员表';

CREATE TABLE IF NOT EXISTS group_message (
  id BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '自增主键',
  message_id VARCHAR(64) NOT NULL COMMENT '全局消息ID',
  group_id BIGINT NOT NULL COMMENT '群ID',
  sequence BIGINT NOT NULL COMMENT '群内消息序号',
  sender_id BIGINT NOT NULL DEFAULT 0 COMMENT '发送人用户ID 0为系统',
  sender_name VARCHAR(64) NOT NULL DEFAULT '系统' COMMENT '发送人昵称',
  client_message_id VARCHAR(128) NOT NULL COMMENT '客户端消息ID 用于幂等去重',
  message_type VARCHAR(32) NOT NULL DEFAULT 'text' COMMENT '消息类型 text文本/system系统',
  content TEXT NOT NULL COMMENT '消息内容',
  mention_all TINYINT NOT NULL DEFAULT 0 COMMENT '是否@全体 0否 1是',
  mention_user_ids JSON DEFAULT NULL COMMENT '被@用户ID列表',
  extra JSON DEFAULT NULL COMMENT '扩展字段',
  status VARCHAR(32) NOT NULL DEFAULT 'normal' COMMENT '消息状态 normal正常/recalled已撤回/deleted删除',
  created_at DATETIME NOT NULL COMMENT '创建时间',
  updated_at DATETIME NOT NULL COMMENT '更新时间',
  UNIQUE KEY uk_message_id (message_id),
  UNIQUE KEY uk_sender_client_msg (sender_id, client_message_id),
  UNIQUE KEY uk_group_sequence (group_id, sequence),
  KEY idx_group_sequence (group_id, sequence),
  KEY idx_sender_created (sender_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='群消息表';

CREATE TABLE IF NOT EXISTS group_mute_record (
  id BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '禁言记录ID',
  group_id BIGINT NOT NULL COMMENT '群ID',
  user_id BIGINT NOT NULL COMMENT '被禁言用户ID',
  operator_id BIGINT NOT NULL COMMENT '操作人用户ID',
  reason VARCHAR(255) DEFAULT NULL COMMENT '禁言原因',
  expire_at DATETIME DEFAULT NULL COMMENT '禁言到期时间 NULL为永久',
  status VARCHAR(32) NOT NULL DEFAULT 'active' COMMENT '状态 active生效/canceled取消/expired过期',
  created_at DATETIME NOT NULL COMMENT '创建时间',
  updated_at DATETIME NOT NULL COMMENT '更新时间',
  KEY idx_group_user_active (group_id, user_id, status),
  KEY idx_group_user_expire (group_id, user_id, expire_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='禁言记录';

CREATE TABLE IF NOT EXISTS group_join_request (
  id BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '申请ID',
  group_id BIGINT NOT NULL COMMENT '群ID',
  user_id BIGINT NOT NULL COMMENT '申请人用户ID',
  reason VARCHAR(255) DEFAULT NULL COMMENT '申请理由',
  status VARCHAR(32) NOT NULL DEFAULT 'pending' COMMENT '状态 pending待审批/approved通过/rejected拒绝',
  operator_id BIGINT DEFAULT NULL COMMENT '审批人用户ID',
  created_at DATETIME NOT NULL COMMENT '创建时间',
  updated_at DATETIME NOT NULL COMMENT '更新时间',
  KEY idx_group_status_id (group_id, status, id),
  KEY idx_user_status (user_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='加群申请';

CREATE TABLE IF NOT EXISTS group_announcement (
  id BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '公告ID',
  group_id BIGINT NOT NULL COMMENT '群ID',
  operator_id BIGINT NOT NULL COMMENT '发布人用户ID',
  title VARCHAR(128) NOT NULL COMMENT '公告标题',
  content TEXT NOT NULL COMMENT '公告内容',
  pinned TINYINT NOT NULL DEFAULT 0 COMMENT '是否置顶 0否 1是',
  status VARCHAR(32) NOT NULL DEFAULT 'normal' COMMENT '状态 normal正常/deleted删除',
  created_at DATETIME NOT NULL COMMENT '创建时间',
  updated_at DATETIME NOT NULL COMMENT '更新时间',
  KEY idx_group_status_id (group_id, status, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='群公告';


CREATE TABLE IF NOT EXISTS group_mention (
  id BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '@记录ID',
  group_id BIGINT NOT NULL COMMENT '群ID',
  message_id VARCHAR(64) NOT NULL COMMENT '消息ID',
  sequence BIGINT NOT NULL COMMENT '消息序号',
  user_id BIGINT NOT NULL COMMENT '被@用户ID',
  mention_type VARCHAR(32) NOT NULL DEFAULT 'user' COMMENT 'user/all',
  read_status TINYINT NOT NULL DEFAULT 0 COMMENT '0未读 1已读',
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  UNIQUE KEY uk_message_user (message_id, user_id),
  KEY idx_user_group_read (user_id, group_id, read_status),
  KEY idx_group_sequence (group_id, sequence)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='群@提醒表';

CREATE TABLE IF NOT EXISTS group_message_recall (
  id BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '撤回记录ID',
  group_id BIGINT NOT NULL COMMENT '群ID',
  message_id VARCHAR(64) NOT NULL COMMENT '消息ID',
  operator_id BIGINT NOT NULL COMMENT '操作人ID',
  sender_id BIGINT NOT NULL COMMENT '原发送人ID',
  reason VARCHAR(255) DEFAULT NULL COMMENT '撤回原因',
  created_at DATETIME NOT NULL,
  UNIQUE KEY uk_message_id (message_id),
  KEY idx_group_created (group_id, created_at),
  KEY idx_operator_created (operator_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='群消息撤回记录表';

CREATE TABLE IF NOT EXISTS group_operation_log (
  id BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '日志ID',
  group_id BIGINT NOT NULL COMMENT '群ID',
  operator_id BIGINT NOT NULL COMMENT '操作人用户ID',
  target_user_id BIGINT DEFAULT NULL COMMENT '操作目标用户ID',
  action VARCHAR(64) NOT NULL COMMENT '操作类型',
  detail JSON DEFAULT NULL COMMENT '操作详情',
  created_at DATETIME NOT NULL COMMENT '创建时间',
  KEY idx_group_id_id (group_id, id),
  KEY idx_operator_id (operator_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='群操作日志';

CREATE TABLE IF NOT EXISTS message_outbox (
  id BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '自增主键',
  event_id VARCHAR(64) NOT NULL COMMENT '事件ID 幂等去重',
  topic VARCHAR(128) NOT NULL COMMENT '消息主题',
  aggregate_id VARCHAR(64) NOT NULL COMMENT '聚合根ID 如群ID',
  payload JSON NOT NULL COMMENT '事件负载',
  status VARCHAR(32) NOT NULL DEFAULT 'pending' COMMENT '状态 pending待发送/sent已发送/failed失败',
  retry_count INT NOT NULL DEFAULT 0 COMMENT '重试次数',
  next_retry_at DATETIME DEFAULT NULL COMMENT '下次重试时间',
  created_at DATETIME NOT NULL COMMENT '创建时间',
  updated_at DATETIME NOT NULL COMMENT '更新时间',
  UNIQUE KEY uk_event_id (event_id),
  KEY idx_status_retry (status, next_retry_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='消息事件Outbox';

INSERT INTO user_account (id, username, nickname, avatar, status, created_at, updated_at) VALUES
(1001, 'user_001', '张三', '', 'normal', NOW(), NOW()),
(1002, 'user_002', '李四', '', 'normal', NOW(), NOW()),
(1003, 'user_003', '王五', '', 'normal', NOW(), NOW()),
(1004, 'user_004', '赵六', '', 'normal', NOW(), NOW()),
(1005, 'user_005', '阿周', '', 'normal', NOW(), NOW())
ON DUPLICATE KEY UPDATE updated_at = VALUES(updated_at);

INSERT INTO chat_group (id, name, avatar, description, owner_id, group_type, join_mode, status, mute_all, slow_mode_seconds, member_count, max_member_count, max_sequence, last_message_summary, created_at, updated_at)
VALUES (10001, 'GroupFlow 技术交流群', '', '讨论大群、高并发、WebSocket 与消息投递设计', 1001, 'normal', 'direct', 'normal', 0, 0, 3, 500, 1, '欢迎加入 GroupFlow 技术交流群', NOW(), NOW())
ON DUPLICATE KEY UPDATE updated_at = VALUES(updated_at);

INSERT INTO group_member (group_id, user_id, role, status, last_read_sequence, joined_at, created_at, updated_at) VALUES
(10001, 1001, 'owner', 'normal', 0, NOW(), NOW(), NOW()),
(10001, 1002, 'admin', 'normal', 0, NOW(), NOW(), NOW()),
(10001, 1003, 'member', 'normal', 0, NOW(), NOW(), NOW())
ON DUPLICATE KEY UPDATE updated_at = VALUES(updated_at);

INSERT INTO group_message (message_id, group_id, sequence, sender_id, sender_name, client_message_id, message_type, content, created_at, updated_at)
VALUES ('msg_seed_10001_1', 10001, 1, 0, '系统', 'seed_system_10001_1', 'system', '欢迎加入 GroupFlow 技术交流群', NOW(), NOW())
ON DUPLICATE KEY UPDATE updated_at = VALUES(updated_at);

INSERT INTO group_announcement (group_id, operator_id, title, content, pinned, status, created_at, updated_at)
VALUES (10001, 1001, '一期 MVP 公告', '欢迎体验群公告、@提醒、加群审批与消息撤回能力。', 1, 'normal', NOW(), NOW())
ON DUPLICATE KEY UPDATE updated_at = VALUES(updated_at);
