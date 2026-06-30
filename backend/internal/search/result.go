package search

import "encoding/json"

// Hit 是返回给上层的一条搜索命中。
type Hit struct {
	MessageID  string `json:"messageId"`
	GroupID    int64  `json:"groupId"`
	Sequence   int64  `json:"sequence"`
	SenderID   int64  `json:"senderId"`
	SenderName string `json:"senderName"`
	Content    string `json:"content"`
	Highlight  string `json:"highlight"`
	CreatedAt  int64  `json:"createdAt"`
}

// Result 是一次搜索的归一化结果。
type Result struct {
	Items      []Hit  `json:"items"`
	NextCursor string `json:"nextCursor"`
	HasMore    bool   `json:"hasMore"`
}

type esResponse struct {
	Hits struct {
		Hits []struct {
			Source struct {
				MessageID   string `json:"message_id"`
				GroupID     int64  `json:"group_id"`
				Sequence    int64  `json:"sequence"`
				SenderID    int64  `json:"sender_id"`
				SenderName  string `json:"sender_name"`
				Content     string `json:"content"`
				MessageType string `json:"message_type"`
				CreatedAt   int64  `json:"created_at"`
			} `json:"_source"`
			Sort      []any               `json:"sort"`
			Highlight map[string][]string `json:"highlight"`
		} `json:"hits"`
	} `json:"hits"`
}

// ParseResponse 解析 ES 查询响应：返回的命中数比 pageSize 多 1 条时判定 hasMore 并裁剪，
// 用最后一条保留命中的 sort 值生成下一页游标；高亮缺失时回退到原始内容。
func ParseResponse(raw []byte, pageSize int) (Result, error) {
	var resp esResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Result{}, err
	}
	hits := resp.Hits.Hits
	var res Result
	res.HasMore = len(hits) > pageSize
	if res.HasMore {
		hits = hits[:pageSize]
	}
	res.Items = make([]Hit, 0, len(hits))
	var lastSort []any
	for _, h := range hits {
		hl := h.Source.Content
		if frags := h.Highlight["content"]; len(frags) > 0 {
			hl = frags[0]
		}
		res.Items = append(res.Items, Hit{
			MessageID:  h.Source.MessageID,
			GroupID:    h.Source.GroupID,
			Sequence:   h.Source.Sequence,
			SenderID:   h.Source.SenderID,
			SenderName: h.Source.SenderName,
			Content:    h.Source.Content,
			Highlight:  hl,
			CreatedAt:  h.Source.CreatedAt,
		})
		lastSort = h.Sort
	}
	if res.HasMore {
		res.NextCursor = EncodeCursor(lastSort)
	}
	return res, nil
}
