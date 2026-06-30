package search

import (
	"context"
	"testing"
)

type fakeIndexer struct {
	indexed  map[string]map[string]any
	statuses map[string]string
}

func newFakeIndexer() *fakeIndexer {
	return &fakeIndexer{indexed: map[string]map[string]any{}, statuses: map[string]string{}}
}

func (f *fakeIndexer) Index(ctx context.Context, docID string, doc map[string]any) error {
	f.indexed[docID] = doc
	return nil
}

func (f *fakeIndexer) UpdateStatus(ctx context.Context, docID, status string) error {
	f.statuses[docID] = status
	return nil
}

const createdEvent = `{
  "eventType":"group_message_created",
  "groupId":1,
  "messageId":"m1",
  "sequence":42,
  "payload":{"messageId":"m1","groupId":1,"sequence":42,"senderId":7,"senderName":"Alice","messageType":"text","content":"hello","status":"normal","createdAt":"2026-06-30T10:00:00Z"}
}`

const recalledEvent = `{"eventType":"group_message_recalled","groupId":1,"messageId":"m1","payload":{"messageId":"m1"}}`

func TestHandleEventIndexesCreatedMessage(t *testing.T) {
	idx := newFakeIndexer()
	if err := HandleEvent(context.Background(), []byte(createdEvent), idx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc, ok := idx.indexed["m1"]
	if !ok {
		t.Fatal("expected message m1 indexed")
	}
	if doc["group_id"] != int64(1) || doc["sequence"] != int64(42) || doc["sender_id"] != int64(7) {
		t.Fatalf("unexpected doc: %#v", doc)
	}
	if doc["content"] != "hello" || doc["message_type"] != "text" || doc["status"] != "normal" {
		t.Fatalf("unexpected doc content: %#v", doc)
	}
	if _, ok := doc["created_at"].(int64); !ok {
		t.Fatalf("expected created_at epoch millis int64, got %#v", doc["created_at"])
	}
}

func TestHandleEventRecallUpdatesStatus(t *testing.T) {
	idx := newFakeIndexer()
	if err := HandleEvent(context.Background(), []byte(recalledEvent), idx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idx.statuses["m1"] != "recalled" {
		t.Fatalf("expected status recalled, got %q", idx.statuses["m1"])
	}
}

func TestHandleEventUnknownTypeIgnored(t *testing.T) {
	idx := newFakeIndexer()
	if err := HandleEvent(context.Background(), []byte(`{"eventType":"group_member_kicked","groupId":1}`), idx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(idx.indexed) != 0 || len(idx.statuses) != 0 {
		t.Fatal("expected no indexing for unknown event")
	}
}
