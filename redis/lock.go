package redis

import (
	"context"
	"log/slog"
	"time"

	"github.com/go-redsync/redsync/v4"
)

const (
	defaultTryLockTime  = 3
	defaultTryLockDelay = 500 * time.Millisecond
)

type lock struct {
	*redsync.Mutex
}

func (l *lock) Lock(ctx context.Context) error {
	err := l.LockContext(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to acquire lock", "key", l.Name(), slog.Any("error", err))
		return err
	}
	return nil
}

func (l *lock) UnLock(ctx context.Context) error {
	_, err := l.UnlockContext(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to release lock", "key", l.Name(), slog.Any("error", err))
		return err
	}
	return nil
}
