package search

import (
	"encoding/json"
	"testing"
)

func TestEffectiveScopesGlobalReturnsAllUserGroups(t *testing.T) {
	user := []GroupScope{{GroupID: 1, JoinSequence: 10}, {GroupID: 2, JoinSequence: 0}}
	got, err := EffectiveScopes(user, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected all 2 groups, got %d", len(got))
	}
}

func TestEffectiveScopesSingleGroupNarrows(t *testing.T) {
	user := []GroupScope{{GroupID: 1, JoinSequence: 10}, {GroupID: 2, JoinSequence: 5}}
	got, err := EffectiveScopes(user, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].GroupID != 2 || got[0].JoinSequence != 5 {
		t.Fatalf("expected only group 2 with joinSeq 5, got %#v", got)
	}
}

func TestEffectiveScopesSingleGroupNotMemberRejected(t *testing.T) {
	user := []GroupScope{{GroupID: 1, JoinSequence: 10}}
	if _, err := EffectiveScopes(user, 99); err != ErrNotMember {
		t.Fatalf("expected ErrNotMember, got %v", err)
	}
}

func TestEffectiveScopesNoGroupsGlobalEmpty(t *testing.T) {
	got, err := EffectiveScopes(nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty scopes, got %#v", got)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	sort := []any{float64(1719000000000), float64(42)}
	enc := EncodeCursor(sort)
	if enc == "" {
		t.Fatal("expected non-empty cursor")
	}
	dec, err := DecodeCursor(enc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dec) != 2 || dec[0] != sort[0] || dec[1] != sort[1] {
		t.Fatalf("round trip mismatch: %#v != %#v", dec, sort)
	}
}

func TestDecodeCursorEmptyIsNil(t *testing.T) {
	dec, err := DecodeCursor("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec != nil {
		t.Fatalf("expected nil for empty cursor, got %#v", dec)
	}
}

func TestBuildQueryAppliesPermissionAndFilters(t *testing.T) {
	params := Params{
		Keyword:   "hello",
		SenderID:  7,
		StartTime: 1000,
		EndTime:   2000,
		Size:      20,
		Scopes:    []GroupScope{{GroupID: 1, JoinSequence: 10}, {GroupID: 2, JoinSequence: 0}},
	}
	body := BuildQuery(params)
	raw, _ := json.Marshal(body)
	s := string(raw)

	// size is page+1 for hasMore detection
	if got := body["size"]; got != 21 {
		t.Fatalf("expected size 21, got %v", got)
	}
	// permission scope: each group must appear with its join sequence lower bound
	if !contains(s, "\"group_id\":1") || !contains(s, "\"gte\":10") {
		t.Fatalf("expected group 1 scoped with joinSeq 10, query=%s", s)
	}
	if !contains(s, "\"group_id\":2") {
		t.Fatalf("expected group 2 in scope, query=%s", s)
	}
	// fixed exclusions
	if !contains(s, "\"status\":\"normal\"") {
		t.Fatalf("expected status=normal filter, query=%s", s)
	}
	if !contains(s, "system") {
		t.Fatalf("expected system message exclusion, query=%s", s)
	}
	// sender + time + keyword + highlight + search ordering
	if !contains(s, "\"sender_id\":7") {
		t.Fatalf("expected sender filter, query=%s", s)
	}
	if !contains(s, "created_at") || !contains(s, "highlight") || !contains(s, "hello") {
		t.Fatalf("expected time/highlight/keyword, query=%s", s)
	}
}

func TestBuildQueryWithCursorAddsSearchAfter(t *testing.T) {
	params := Params{
		Size:   10,
		Scopes: []GroupScope{{GroupID: 1, JoinSequence: 0}},
		After:  []any{float64(123), float64(4)},
	}
	body := BuildQuery(params)
	if _, ok := body["search_after"]; !ok {
		t.Fatalf("expected search_after in query, got %#v", body)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (indexOf(haystack, needle) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
