package service

import (
	"context"
	"database/sql"
	"testing"

	"go.uber.org/zap"

	"groupflow/backend/internal/config"
	"groupflow/backend/internal/domain"
)

func TestUpdateReadPositionRejectsNonMemberNoRows(t *testing.T) {
	svc := &Service{Cfg: config.Config{}, Log: zap.NewNop()}
	svc.readMemberLookup = func(ctx context.Context, groupID, userID int64) (*domain.Member, error) {
		return nil, sql.ErrNoRows
	}
	svc.readPersist = func(ctx context.Context, groupID, userID, lastRead int64) error {
		t.Fatalf("non-member must not persist read position")
		return nil
	}

	if err := svc.UpdateReadPosition(context.Background(), 10001, 1001, 120); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden for non-member, got %v", err)
	}
}

func TestUpdateReadPositionRejectsRollback(t *testing.T) {
	svc := &Service{Cfg: config.Config{}, Log: zap.NewNop()}
	svc.readMemberLookup = func(ctx context.Context, groupID, userID int64) (*domain.Member, error) {
		return &domain.Member{GroupID: groupID, UserID: userID, Status: domain.StatusNormal, LastReadSequence: 100}, nil
	}
	persisted := false
	svc.readPersist = func(ctx context.Context, groupID, userID, lastRead int64) error {
		persisted = true
		return nil
	}

	if err := svc.UpdateReadPosition(context.Background(), 10001, 1001, 50); err != ErrReadSequenceRollback {
		t.Fatalf("expected ErrReadSequenceRollback, got %v", err)
	}
	if persisted {
		t.Fatalf("rollback must not persist")
	}
}

func TestUpdateReadPositionAcceptsForwardProgress(t *testing.T) {
	svc := &Service{Cfg: config.Config{}, Log: zap.NewNop()}
	svc.readMemberLookup = func(ctx context.Context, groupID, userID int64) (*domain.Member, error) {
		return &domain.Member{GroupID: groupID, UserID: userID, Status: domain.StatusNormal, LastReadSequence: 100}, nil
	}
	var persistedTo int64
	svc.readPersist = func(ctx context.Context, groupID, userID, lastRead int64) error {
		persistedTo = lastRead
		return nil
	}

	if err := svc.UpdateReadPosition(context.Background(), 10001, 1001, 120); err != nil {
		t.Fatalf("expected forward progress to succeed, got %v", err)
	}
	if persistedTo != 120 {
		t.Fatalf("expected persist to 120, got %d", persistedTo)
	}
}

func TestUpdateReadPositionRejectsNonMember(t *testing.T) {
	svc := &Service{Cfg: config.Config{}, Log: zap.NewNop()}
	svc.readMemberLookup = func(ctx context.Context, groupID, userID int64) (*domain.Member, error) {
		return &domain.Member{GroupID: groupID, UserID: userID, Status: domain.MemberKicked, LastReadSequence: 100}, nil
	}
	svc.readPersist = func(ctx context.Context, groupID, userID, lastRead int64) error {
		t.Fatalf("kicked member must not persist read position")
		return nil
	}

	if err := svc.UpdateReadPosition(context.Background(), 10001, 1001, 120); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}
