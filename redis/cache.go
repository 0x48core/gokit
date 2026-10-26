package redis

import (
	"context"
	"time"
)

//go:generate mockgen --package=redismocks --destination=mocks/cache.go . Client,Lock

type Client interface {
	Set(ctx context.Context, caller string, key string, value any, expired time.Duration) error
	Get(ctx context.Context, caller string, key string, value any) error
	Delete(ctx context.Context, caller string, key string) error
	GetTTLByKey(ctx context.Context, caller string, key string) (time.Duration, error)
	SetNx(ctx context.Context, caller string, key string, value any, expired time.Duration) (bool, error)
	CreateRedisLock(key string, leaseTime time.Duration) Lock
	Ping(ctx context.Context) error
	Close() error
}

type Lock interface {
	Lock(ctx context.Context) error
	UnLock(ctx context.Context) error
}
