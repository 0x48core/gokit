package lease

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

const testTTL = 300 * time.Millisecond // renewals every 100ms

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestKeeperRenews(t *testing.T) {
	var renewals atomic.Int32
	k := Start(time.Now(), testTTL, func(context.Context) error {
		renewals.Add(1)
		return nil
	})

	time.Sleep(5 * testTTL / 3)
	k.Stop()

	if isClosed(k.Lost()) {
		t.Fatal("lease reported lost while renewals succeed")
	}
	if n := renewals.Load(); n < 3 {
		t.Fatalf("renewals = %d, want at least 3", n)
	}

	// No renewals after Stop.
	n := renewals.Load()
	time.Sleep(testTTL)
	if renewals.Load() != n {
		t.Fatal("renewed after Stop")
	}
}

func TestKeeperLostWhenNotOwner(t *testing.T) {
	k := Start(time.Now(), testTTL, func(context.Context) error {
		return ErrNotOwner
	})
	defer k.Stop()

	select {
	case <-k.Lost():
	case <-time.After(testTTL):
		t.Fatal("lease not reported lost after ErrNotOwner")
	}
}

func TestKeeperToleratesOneFailure(t *testing.T) {
	var calls atomic.Int32
	k := Start(time.Now(), testTTL, func(context.Context) error {
		if calls.Add(1) == 1 {
			return errors.New("temporary failure")
		}
		return nil
	})

	time.Sleep(testTTL + testTTL/2)
	k.Stop()
	if isClosed(k.Lost()) {
		t.Fatal("lease reported lost after a single failed renewal")
	}
}

func TestKeeperLostBeforeExpiry(t *testing.T) {
	acquiredAt := time.Now()
	k := Start(acquiredAt, testTTL, func(context.Context) error {
		return errors.New("database down")
	})
	defer k.Stop()

	select {
	case <-k.Lost():
		if time.Since(acquiredAt) >= testTTL {
			t.Fatal("lease reported lost only after it expired")
		}
	case <-time.After(testTTL):
		t.Fatal("lease not reported lost before expiry")
	}
}

type fakeBackend struct {
	nextID   int64
	released []int64
}

func (b *fakeBackend) Acquire(context.Context, string, time.Duration) (int64, error) {
	b.nextID++
	return b.nextID, nil
}

func (b *fakeBackend) Renew(context.Context, string, int64, time.Duration) error {
	return nil
}

func (b *fakeBackend) Release(_ context.Context, _ string, nodeID int64) error {
	b.released = append(b.released, nodeID)
	return nil
}

func TestRegistry(t *testing.T) {
	ctx := context.Background()
	b := &fakeBackend{}
	r := NewRegistry(b, "owner", testTTL)

	if r.Lost() != nil {
		t.Fatal("Lost before Acquire should be nil")
	}
	if err := r.Release(ctx); err != nil || len(b.released) != 0 {
		t.Fatalf("Release before Acquire: err = %v, released = %v", err, b.released)
	}

	id, err := r.Acquire(ctx)
	if err != nil || id != 1 {
		t.Fatalf("Acquire = %d, %v; want 1, nil", id, err)
	}
	if r.Lost() == nil {
		t.Fatal("Lost after Acquire should not be nil")
	}
	if _, err := r.Acquire(ctx); !errors.Is(err, ErrAlreadyAcquired) {
		t.Fatalf("second Acquire error = %v, want ErrAlreadyAcquired", err)
	}

	if err := r.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if len(b.released) != 1 || b.released[0] != 1 {
		t.Fatalf("released = %v, want [1]", b.released)
	}

	// The registry can acquire again after Release.
	if id, err := r.Acquire(ctx); err != nil || id != 2 {
		t.Fatalf("Acquire after Release = %d, %v; want 2, nil", id, err)
	}
	_ = r.Release(ctx)
}
