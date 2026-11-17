package coordinator

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeInstanceStore struct {
	mu          sync.Mutex
	registers   int
	renewals    int
	releases    int
	registerErr error
	renewErr    error
	releaseErr  error
	epoch       int64
	releaseDone chan error
}

func (s *fakeInstanceStore) RegisterInstance(ctx context.Context, _ string, _ uuid.UUID, _ string, _ time.Duration) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registers++
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return s.epoch, s.registerErr
}

func (s *fakeInstanceStore) RenewInstance(ctx context.Context, _ string, _ uuid.UUID, _ int64, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.renewals++
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.renewErr
}

func (s *fakeInstanceStore) ReleaseInstance(ctx context.Context, _ string, _ uuid.UUID, _ int64) error {
	s.mu.Lock()
	s.releases++
	releaseDone := s.releaseDone
	releaseErr := s.releaseErr
	s.mu.Unlock()
	err := ctx.Err()
	if releaseDone != nil {
		releaseDone <- err
	}
	if err != nil {
		return err
	}
	return releaseErr
}

func TestStartInstanceFailsClosedWhenHeartbeatLeaseIsLost(t *testing.T) {
	store := &fakeInstanceStore{epoch: 4, renewErr: errors.New("database secret must not leak")}
	instance, err := StartInstance(context.Background(), store, InstanceConfig{SystemID: "test-system", Fingerprint: strings.Repeat("a", 64),
		LeaseDuration: 3 * time.Second, HeartbeatInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-instance.Errors():
		if !errors.Is(got, ErrInstanceNotReady) {
			t.Fatalf("heartbeat error leaked or was not normalized: %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("failed heartbeat was not reported")
	}
	if instance.Ready() {
		t.Fatal("instance remained ready after its database lease was lost")
	}
	if err := instance.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := instance.Close(context.Background()); err != nil {
		t.Fatalf("repeated close was not idempotent: %v", err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.registers != 1 || store.renewals != 1 || store.releases != 1 {
		t.Fatalf("unexpected instance lifecycle calls: registers=%d renewals=%d releases=%d", store.registers, store.renewals, store.releases)
	}
}

func TestStartInstanceRejectsInvalidOrCancelledStartup(t *testing.T) {
	for _, config := range []InstanceConfig{
		{SystemID: "bad/system", Fingerprint: strings.Repeat("a", 64), LeaseDuration: 6 * time.Second, HeartbeatInterval: time.Second},
		{SystemID: "test", Fingerprint: strings.Repeat("a", 64), LeaseDuration: 6 * time.Second, HeartbeatInterval: 2 * time.Second},
	} {
		store := &fakeInstanceStore{epoch: 1}
		if _, err := StartInstance(context.Background(), store, config); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid instance config accepted: %+v err=%v", config, err)
		}
	}
	store := &fakeInstanceStore{epoch: 1}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := StartInstance(ctx, store, InstanceConfig{SystemID: "test", Fingerprint: strings.Repeat("a", 64),
		LeaseDuration: 6 * time.Second, HeartbeatInterval: time.Second}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled startup registered an instance: %v", err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.registers != 0 {
		t.Fatalf("cancelled startup made %d registration calls", store.registers)
	}
}

func TestStartInstanceRejectsAnInvalidStoreEpoch(t *testing.T) {
	store := &fakeInstanceStore{epoch: 0}
	_, err := StartInstance(context.Background(), store, InstanceConfig{SystemID: "test", Fingerprint: strings.Repeat("a", 64),
		LeaseDuration: 6 * time.Second, HeartbeatInterval: time.Second})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("instance accepted an invalid configuration epoch: %v", err)
	}
}

func TestInstanceCloseReleasesLeaseAfterCallerContextIsCancelled(t *testing.T) {
	store := &fakeInstanceStore{epoch: 2, releaseDone: make(chan error, 1)}
	instance, err := StartInstance(context.Background(), store, InstanceConfig{SystemID: "test", Fingerprint: strings.Repeat("b", 64),
		LeaseDuration: 6 * time.Second, HeartbeatInterval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := instance.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close did not honor the caller's canceled wait context: %v", err)
	}
	select {
	case err := <-store.releaseDone:
		if err != nil {
			t.Fatalf("lease release inherited caller cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("lease release was not attempted after Close returned")
	}
}
