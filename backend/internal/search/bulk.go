package search

import (
	"bytes"
	"encoding/json"

	"groupflow/backend/internal/domain"
)

// BuildBulkNDJSON 将消息列表编码为 ES _bulk 接口的 NDJSON：每条消息一行 index 动作 + 一行文档，
// 以 messageId 作 _id 保证幂等。空列表返回空字节。
func BuildBulkNDJSON(index string, msgs []domain.Message) []byte {
	if len(msgs) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for i := range msgs {
		action := map[string]any{"index": map[string]any{"_index": index, "_id": msgs[i].MessageID}}
		ab, _ := json.Marshal(action)
		buf.Write(ab)
		buf.WriteByte('\n')
		db, _ := json.Marshal(MessageToDoc(&msgs[i]))
		buf.Write(db)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}
