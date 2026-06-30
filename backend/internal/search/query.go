package search

import (
	"encoding/base64"
	"encoding/json"
	"errors"
)

// ErrNotMember 表示用户尝试在自己不在的群内搜索。
var ErrNotMember = errors.New("not a member of the requested group")

// GroupScope 描述某个群的可搜索范围：用户只能搜到自己加群序号（JoinSequence）之后的消息。
type GroupScope struct {
	GroupID      int64 `json:"groupId"`
	JoinSequence int64 `json:"joinSequence"`
}

// Params 是一次搜索请求的归一化参数。
type Params struct {
	Keyword   string
	GroupID   int64 // 0 = 全局跨群
	SenderID  int64 // 0 = 不限发送人
	StartTime int64 // unix 秒，0 = 不限
	EndTime   int64 // unix 秒，0 = 不限
	Size      int   // 每页条数（不含 +1 探测）
	After     []any // search_after 游标排序值
	Scopes    []GroupScope
}

// EffectiveScopes 根据用户可搜索的群集合与请求的 groupId 过滤，计算本次搜索的有效权限范围。
// requestedGroupID==0 表示全局搜索，返回用户全部群；否则收窄到该群，且校验用户确为成员。
func EffectiveScopes(userScopes []GroupScope, requestedGroupID int64) ([]GroupScope, error) {
	if requestedGroupID == 0 {
		return userScopes, nil
	}
	for _, s := range userScopes {
		if s.GroupID == requestedGroupID {
			return []GroupScope{s}, nil
		}
	}
	return nil, ErrNotMember
}

// EncodeCursor 将 ES 命中的 sort 值编码为不透明游标字符串。
func EncodeCursor(sort []any) string {
	if len(sort) == 0 {
		return ""
	}
	b, err := json.Marshal(sort)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor 解码游标字符串为 search_after 排序值；空串返回 nil。
func DecodeCursor(cursor string) ([]any, error) {
	if cursor == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, err
	}
	var sort []any
	if err := json.Unmarshal(b, &sort); err != nil {
		return nil, err
	}
	return sort, nil
}

// BuildQuery 构建发往 Elasticsearch 的查询体：权限范围（按群 + 加群序号下界）、固定排除
// （仅正常状态、排除系统消息）、可选的发送人/时间/关键词过滤，按时间倒序 + sequence 二级排序，
// 带高亮，size 取页大小 +1 以便判断 hasMore。
func BuildQuery(p Params) map[string]any {
	must := []any{}
	if p.Keyword != "" {
		must = append(must, map[string]any{
			"match": map[string]any{"content": p.Keyword},
		})
	}

	filter := []any{
		map[string]any{"term": map[string]any{"status": "normal"}},
	}
	if p.SenderID > 0 {
		filter = append(filter, map[string]any{"term": map[string]any{"sender_id": p.SenderID}})
	}
	if p.StartTime > 0 || p.EndTime > 0 {
		rng := map[string]any{}
		if p.StartTime > 0 {
			rng["gte"] = p.StartTime * 1000
		}
		if p.EndTime > 0 {
			rng["lte"] = p.EndTime * 1000
		}
		filter = append(filter, map[string]any{"range": map[string]any{"created_at": rng}})
	}

	mustNot := []any{
		map[string]any{"term": map[string]any{"message_type": "system"}},
	}

	// 权限范围：每个可搜索群一个子句 {group_id == X AND sequence >= joinSeq_X}，整体 OR。
	should := make([]any, 0, len(p.Scopes))
	for _, s := range p.Scopes {
		clauses := []any{
			map[string]any{"term": map[string]any{"group_id": s.GroupID}},
		}
		if s.JoinSequence > 0 {
			clauses = append(clauses, map[string]any{"range": map[string]any{"sequence": map[string]any{"gte": s.JoinSequence}}})
		}
		should = append(should, map[string]any{"bool": map[string]any{"must": clauses}})
	}

	boolQuery := map[string]any{
		"filter":   filter,
		"must_not": mustNot,
	}
	if len(must) > 0 {
		boolQuery["must"] = must
	}
	// should 为空表示用户没有任何可搜索的群，用永假条件兜底，避免返回全量数据。
	boolQuery["should"] = should
	boolQuery["minimum_should_match"] = 1

	size := p.Size
	if size <= 0 {
		size = 20
	}
	body := map[string]any{
		"size":  size + 1,
		"query": map[string]any{"bool": boolQuery},
		"sort": []any{
			map[string]any{"created_at": map[string]any{"order": "desc"}},
			map[string]any{"sequence": map[string]any{"order": "desc"}},
		},
		"highlight": map[string]any{
			"fields": map[string]any{
				"content": map[string]any{},
			},
		},
	}
	if len(p.After) > 0 {
		body["search_after"] = p.After
	}
	return body
}
