package search

import (
	"context"
	"encoding/json"

	"groupflow/backend/internal/domain"
)

const (
	eventMessageCreated  = "group_message_created"
	eventMessageRecalled = "group_message_recalled"
)

// Indexer 是 ES 写入端的最小接口，便于消费者与测试解耦。
type Indexer interface {
	Index(ctx context.Context, docID string, doc map[string]any) error
	UpdateStatus(ctx context.Context, docID, status string) error
}

type eventEnvelope struct {
	EventType string          `json:"eventType"`
	MessageID string          `json:"messageId"`
	Payload   json.RawMessage `json:"payload"`
}

// MessageToDoc 将一条消息转换为 ES 文档字段（created_at 以 epoch 毫秒存储）。
func MessageToDoc(m *domain.Message) map[string]any {
	return map[string]any{
		"message_id":   m.MessageID,
		"group_id":     m.GroupID,
		"sequence":     m.Sequence,
		"sender_id":    m.SenderID,
		"sender_name":  m.SenderName,
		"content":      m.Content,
		"message_type": m.MessageType,
		"status":       m.Status,
		"created_at":   m.CreatedAt.UnixMilli(),
	}
}

// HandleEvent 解析消息事件信封并应用到索引：创建事件写入文档，撤回事件更新状态，
// 其余事件忽略。以 messageId 作为文档 _id，保证幂等且可重放重建。
func HandleEvent(ctx context.Context, raw []byte, idx Indexer) error {
	var evt eventEnvelope
	if err := json.Unmarshal(raw, &evt); err != nil {
		return err
	}
	switch evt.EventType {
	case eventMessageCreated:
		var m domain.Message
		if err := json.Unmarshal(evt.Payload, &m); err != nil {
			return err
		}
		if m.MessageID == "" {
			return nil
		}
		return idx.Index(ctx, m.MessageID, MessageToDoc(&m))
	case eventMessageRecalled:
		if evt.MessageID == "" {
			return nil
		}
		return idx.UpdateStatus(ctx, evt.MessageID, domain.StatusRecalled)
	default:
		return nil
	}
}
