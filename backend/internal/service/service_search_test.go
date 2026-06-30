package service

import (
	"context"
	"errors"
	"testing"

	"groupflow/backend/internal/search"
)

func TestSearchMessagesAppliesPermissionScopeAndReturnsHits(t *testing.T) {
	s := &Service{}
	var capturedQuery map[string]any
	s.SetSearchHooksForTest(
		func(ctx context.Context, userID int64) ([]search.GroupScope, error) {
			return []search.GroupScope{{GroupID: 1, JoinSequence: 10}, {GroupID: 2, JoinSequence: 0}}, nil
		},
		func(ctx context.Context, query map[string]any) ([]byte, error) {
			capturedQuery = query
			return []byte(`{"hits":{"hits":[{"_id":"m1","_source":{"message_id":"m1","group_id":1,"sequence":42,"content":"hi"},"sort":[1,42]}]}}`), nil
		},
	)

	res, err := s.SearchMessages(context.Background(), 100, SearchInput{Keyword: "hi", Size: 20})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].MessageID != "m1" {
		t.Fatalf("unexpected result: %#v", res)
	}
	if capturedQuery == nil {
		t.Fatal("expected searcher to be invoked")
	}
}

func TestSearchMessagesSingleGroupNotMemberRejected(t *testing.T) {
	s := &Service{}
	s.SetSearchHooksForTest(
		func(ctx context.Context, userID int64) ([]search.GroupScope, error) {
			return []search.GroupScope{{GroupID: 1, JoinSequence: 0}}, nil
		},
		func(ctx context.Context, query map[string]any) ([]byte, error) {
			t.Fatal("searcher must not be called when permission denied")
			return nil, nil
		},
	)

	_, err := s.SearchMessages(context.Background(), 100, SearchInput{GroupID: 99})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}
