package service

import (
	"context"

	"go.uber.org/zap"

	"groupflow/backend/internal/search"
)

// SearchInput 是历史消息搜索的入参。
type SearchInput struct {
	Keyword   string
	GroupID   int64 // 0 = 全局跨群
	SenderID  int64
	StartTime int64 // unix 秒
	EndTime   int64 // unix 秒
	Cursor    string
	Size      int
}

// SetSearchHooksForTest 注入搜索范围查询与 ES 执行替身，仅供测试使用。
func (s *Service) SetSearchHooksForTest(
	scopeLookup func(ctx context.Context, userID int64) ([]search.GroupScope, error),
	execute func(ctx context.Context, query map[string]any) ([]byte, error),
) {
	s.searchScopeLookup = scopeLookup
	s.searchExecute = execute
}

// SetSearcher 注入生产环境的搜索范围查询与 ES 执行实现。
func (s *Service) SetSearcher(
	scopeLookup func(ctx context.Context, userID int64) ([]search.GroupScope, error),
	execute func(ctx context.Context, query map[string]any) ([]byte, error),
) {
	s.searchScopeLookup = scopeLookup
	s.searchExecute = execute
}

// SearchEnabled 表示搜索后端是否已装配。
func (s *Service) SearchEnabled() bool {
	return s.searchScopeLookup != nil && s.searchExecute != nil
}

// SearchMessages 在用户有权限的群范围内全文搜索历史消息。
// 权限：全局搜索覆盖用户全部群；单群搜索校验用户为该群成员；每群仅可见加群序号之后的消息。
func (s *Service) SearchMessages(ctx context.Context, userID int64, in SearchInput) (search.Result, error) {
	userScopes, err := s.searchScopeLookup(ctx, userID)
	if err != nil {
		return search.Result{}, opErr(ctx, "search_scope_lookup", err, zap.Int64("userId", userID))
	}
	scopes, err := search.EffectiveScopes(userScopes, in.GroupID)
	if err != nil {
		// 请求了自己不在的群，按权限拒绝。
		return search.Result{}, ErrForbidden
	}
	after, err := search.DecodeCursor(in.Cursor)
	if err != nil {
		return search.Result{}, err
	}
	size := in.Size
	if size <= 0 || size > 50 {
		size = 20
	}
	query := search.BuildQuery(search.Params{
		Keyword:   in.Keyword,
		GroupID:   in.GroupID,
		SenderID:  in.SenderID,
		StartTime: in.StartTime,
		EndTime:   in.EndTime,
		Size:      size,
		After:     after,
		Scopes:    scopes,
	})
	raw, err := s.searchExecute(ctx, query)
	if err != nil {
		return search.Result{}, opErr(ctx, "search_execute", err, zap.Int64("userId", userID))
	}
	return search.ParseResponse(raw, size)
}
