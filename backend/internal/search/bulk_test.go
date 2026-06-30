package search

import (
	"strings"
	"testing"
	"time"

	"groupflow/backend/internal/domain"
)

func TestBuildBulkNDJSONEmitsActionAndDocLines(t *testing.T) {
	msgs := []domain.Message{
		{MessageID: "m1", GroupID: 1, Sequence: 10, Content: "hi", MessageType: "text", Status: "normal", CreatedAt: time.UnixMilli(1719000000000)},
		{MessageID: "m2", GroupID: 1, Sequence: 11, Content: "yo", MessageType: "text", Status: "normal", CreatedAt: time.UnixMilli(1719000001000)},
	}
	out := BuildBulkNDJSON("group_message", msgs)
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines (2 actions + 2 docs), got %d: %q", len(lines), out)
	}
	if !strings.Contains(lines[0], `"_index":"group_message"`) || !strings.Contains(lines[0], `"_id":"m1"`) {
		t.Fatalf("unexpected action line: %s", lines[0])
	}
	if !strings.Contains(lines[1], `"content":"hi"`) || !strings.Contains(lines[1], `"created_at":1719000000000`) {
		t.Fatalf("unexpected doc line: %s", lines[1])
	}
	if !strings.HasSuffix(string(out), "\n") {
		t.Fatal("bulk body must end with newline")
	}
}

func TestBuildBulkNDJSONEmptyReturnsEmpty(t *testing.T) {
	if out := BuildBulkNDJSON("group_message", nil); len(out) != 0 {
		t.Fatalf("expected empty body, got %q", out)
	}
}
