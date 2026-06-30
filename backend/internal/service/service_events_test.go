package service

import (
	"context"
	"encoding/json"
	"testing"

	"groupflow/backend/internal/config"
	"groupflow/backend/internal/domain"
	"groupflow/backend/pkg/logx"
)

func TestNewRealtimeEventBuildsTargetedOutboxWhenKafkaEnabled(t *testing.T) {
	ctx := logx.WithTrace(context.Background(), logx.Trace{TraceID: "trace-test-1"})
	svc := &Service{Cfg: config.Config{KafkaEnabled: true, KafkaTopic: "group-realtime-topic"}}

	evt := svc.newRealtimeEvent(ctx, "group_member_kicked", 10001, domain.GroupLarge, 20001, []int64{30001, 30001, 30002}, map[string]any{"groupId": 10001, "userId": 30001})
	if evt == nil {
		t.Fatalf("expected realtime event")
	}
	if evt.Outbox == nil {
		t.Fatalf("expected outbox event when kafka enabled")
	}
	if evt.EventType != "group_member_kicked" {
		t.Fatalf("expected event type group_member_kicked, got %q", evt.EventType)
	}
	if len(evt.TargetUserIDs) != 2 || evt.TargetUserIDs[0] != 30001 || evt.TargetUserIDs[1] != 30002 {
		t.Fatalf("expected deduplicated target user ids, got %#v", evt.TargetUserIDs)
	}

	var envelope struct {
		EventType string `json:"eventType"`
		GroupID   int64  `json:"groupId"`
		GroupType string `json:"groupType"`
		SenderID  int64  `json:"senderId"`
		Payload   struct {
			TargetUserIDs []int64        `json:"targetUserIds"`
			Body          map[string]any `json:"body"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(evt.Outbox.Payload, &envelope); err != nil {
		t.Fatalf("unmarshal outbox payload: %v", err)
	}
	if envelope.EventType != "group_member_kicked" {
		t.Fatalf("expected outbox event type group_member_kicked, got %q", envelope.EventType)
	}
	if envelope.GroupID != 10001 || envelope.GroupType != domain.GroupLarge || envelope.SenderID != 20001 {
		t.Fatalf("unexpected outbox envelope: %#v", envelope)
	}
	if len(envelope.Payload.TargetUserIDs) != 2 || envelope.Payload.TargetUserIDs[0] != 30001 || envelope.Payload.TargetUserIDs[1] != 30002 {
		t.Fatalf("expected targeted payload user ids, got %#v", envelope.Payload.TargetUserIDs)
	}
	if got, _ := envelope.Payload.Body["userId"].(float64); got != 30001 {
		t.Fatalf("expected body userId 30001, got %#v", envelope.Payload.Body)
	}
}

func TestNewRealtimeEventOmitsOutboxWhenKafkaDisabled(t *testing.T) {
	ctx := logx.WithTrace(context.Background(), logx.Trace{TraceID: "trace-test-2"})
	svc := &Service{Cfg: config.Config{KafkaEnabled: false}}

	evt := svc.newRealtimeEvent(ctx, "group_join_request_approved", 10001, domain.GroupNormal, 20001, []int64{30001}, map[string]any{"requestId": 9001})
	if evt == nil {
		t.Fatalf("expected realtime event")
	}
	if evt.Outbox != nil {
		t.Fatalf("expected no outbox event when kafka disabled")
	}
}
