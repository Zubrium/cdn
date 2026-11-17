package coordinator

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"time"

	"github.com/google/uuid"
)

type InstanceStore interface {
	RegisterInstance(context.Context, string, uuid.UUID, string, time.Duration) (int64, error)
	RenewInstance(context.Context, string, uuid.UUID, int64, time.Duration) error
	ReleaseInstance(context.Context, string, uuid.UUID, int64) error
}

type InstanceConfig struct {
	SystemID          string
	Fingerprint       string
	ID                uuid.UUID
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
}

var ErrInstanceNotReady = errors.New("coordinator instance lease is not ready")

var coordinatorSystemIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

type Instance struct {
	store     InstanceStore
	cfg       InstanceConfig
	epoch     int64
	mu        sync.RWMutex
	ready     bool
	closed    bool
	closeOnce sync.Once
	closeDone chan struct{}
	closeErr  error
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	errors    chan error
}

func StartInstance(ctx context.Context, store InstanceStore, cfg InstanceConfig) (*Instance, error) {
	if ctx == nil {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if isNilInterface(store) || !coordinatorSystemIDPattern.MatchString(cfg.SystemID) || !validFingerprint(cfg.Fingerprint) ||
		cfg.LeaseDuration < 3*time.Second || cfg.LeaseDuration > time.Hour || cfg.HeartbeatInterval <= 0 ||
		cfg.HeartbeatInterval >= cfg.LeaseDuration/3 {
		return nil, ErrInvalid
	}
	if cfg.ID == uuid.Nil {
		id, err := uuid.NewRandom()
		if err != nil {
			return nil, err
		}
		cfg.ID = id
	}
	epoch, err := store.RegisterInstance(ctx, cfg.SystemID, cfg.ID, cfg.Fingerprint, cfg.LeaseDuration)
	if err != nil {
		return nil, err
	}
	if epoch <= 0 {
		return nil, ErrInvalid
	}
	instanceCtx, cancel := context.WithCancel(context.Background())
	instance := &Instance{store: store, cfg: cfg, epoch: epoch, ready: true, ctx: instanceCtx, cancel: cancel,
		done: make(chan struct{}), errors: make(chan error, 1), closeDone: make(chan struct{})}
	go instance.heartbeat()
	return instance, nil
}

func validFingerprint(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9') && !(char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func (i *Instance) ID() uuid.UUID {
	if i == nil {
		return uuid.Nil
	}
	return i.cfg.ID
}

func (i *Instance) Epoch() int64 {
	if i == nil {
		return 0
	}
	return i.epoch
}

func (i *Instance) Ready() bool {
	if i == nil {
		return false
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.ready && !i.closed
}

func (i *Instance) IsReady() bool { return i.Ready() }

func (i *Instance) Errors() <-chan error {
	if i == nil {
		ch := make(chan error)
		close(ch)
		return ch
	}
	return i.errors
}

func (i *Instance) heartbeat() {
	ticker := time.NewTicker(i.cfg.HeartbeatInterval)
	defer ticker.Stop()
	defer close(i.done)
	defer close(i.errors)
	for {
		select {
		case <-i.ctx.Done():
			return
		case <-ticker.C:
			opCtx, cancel := context.WithTimeout(i.ctx, i.cfg.HeartbeatInterval)
			err := i.store.RenewInstance(opCtx, i.cfg.SystemID, i.cfg.ID, i.epoch, i.cfg.LeaseDuration)
			cancel()
			if err != nil {
				i.mu.Lock()
				i.ready = false
				i.mu.Unlock()
				select {
				case i.errors <- ErrInstanceNotReady:
				default:
				}
				return
			}
		}
	}
}

func (i *Instance) Close(ctx context.Context) error {
	if i == nil {
		return nil
	}
	if ctx == nil {
		return ErrInvalid
	}
	i.closeOnce.Do(func() {
		i.mu.Lock()
		i.closed = true
		i.ready = false
		i.cancel()
		i.mu.Unlock()
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), i.cfg.LeaseDuration)
		go func() {
			defer releaseCancel()
			<-i.done
			err := i.store.ReleaseInstance(releaseCtx, i.cfg.SystemID, i.cfg.ID, i.epoch)
			i.mu.Lock()
			i.closeErr = err
			close(i.closeDone)
			i.mu.Unlock()
		}()
	})
	select {
	case <-i.closeDone:
		i.mu.RLock()
		defer i.mu.RUnlock()
		return i.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
