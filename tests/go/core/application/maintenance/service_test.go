package maintenance

import (
	"context"
	"errors"
	"testing"
	"time"
)

type maintenanceRepositoryStub struct {
	recoveredBefore time.Time
	prunedSessions  time.Time
	pruneSessionErr error
	order           []string
}

func (s *maintenanceRepositoryStub) RecoverPending(_ context.Context, before time.Time, _ int) error {
	s.recoveredBefore = before
	s.order = append(s.order, "recover")
	return nil
}

func (s *maintenanceRepositoryStub) PruneSessions(_ context.Context, before time.Time, _ int) (int64, error) {
	s.prunedSessions = before
	s.order = append(s.order, "sessions")
	return 0, s.pruneSessionErr
}

func (s *maintenanceRepositoryStub) PruneWorkflows(context.Context, int) (int64, error) {
	s.order = append(s.order, "workflows")
	return 0, nil
}

func TestRunPrunesExpiredSessionsBeforeWorkflowRetention(t *testing.T) {
	repository := &maintenanceRepositoryStub{}
	service, err := NewService(repository, time.Hour, 25)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	if err := service.Run(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if !repository.prunedSessions.Equal(now) {
		t.Fatalf("session prune cutoff = %s, want current time %s", repository.prunedSessions, now)
	}
	if len(repository.order) != 3 || repository.order[0] != "recover" || repository.order[1] != "sessions" || repository.order[2] != "workflows" {
		t.Fatalf("maintenance order = %v, want recover, sessions, workflows", repository.order)
	}
}

func TestRunStopsBeforeWorkflowPruningWhenSessionCleanupFails(t *testing.T) {
	wantErr := errors.New("session cleanup failed")
	repository := &maintenanceRepositoryStub{pruneSessionErr: wantErr}
	service, err := NewService(repository, time.Hour, 25)
	if err != nil {
		t.Fatal(err)
	}
	err = service.Run(context.Background(), time.Now())
	if !errors.Is(err, wantErr) {
		t.Fatalf("maintenance error = %v, want %v", err, wantErr)
	}
	if len(repository.order) != 2 || repository.order[1] != "sessions" {
		t.Fatalf("workflow pruning ran after session cleanup failed: %v", repository.order)
	}
}
