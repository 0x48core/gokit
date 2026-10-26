package redis

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/0x48core/gokit/metrics"
	"github.com/0x48core/gokit/redis/codec"
	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/redis/go-redis/v9"
)

const metricName = "redis"

var ErrNotFound = errors.New("key not found")

type redisClient struct {
	client       redis.UniversalClient
	redisSync    *redsync.Redsync
	serializer   codec.Serializer
	metricClient metrics.Client
}

// NewClient connects to a single node when conf.Addr has one address, or to a
// cluster when it has several.
func NewClient(
	conf *Config,
	metricClient metrics.Client,
) Client {
	opts := &redis.UniversalOptions{
		Addrs:        conf.Addr,
		DB:           0,
		Username:     conf.Username,
		Password:     conf.Password,
		MaxRetries:   conf.MaxRetries,
		DialTimeout:  conf.ReadTimeout,
		ReadTimeout:  conf.ReadTimeout,
		WriteTimeout: conf.WriteTimeout,
		PoolSize:     conf.PoolSize,
		MinIdleConns: conf.MinIdleConn,
		PoolTimeout:  conf.PoolTimeout,
		ReadOnly:     conf.EnableReadSlave,
	}

	client := redis.NewUniversalClient(opts)
	return newRedisClient(client, metricClient)
}

func newRedisClient(client redis.UniversalClient, metricClient metrics.Client) *redisClient {
	return &redisClient{
		client:       client,
		redisSync:    redsync.New(goredis.NewPool(client)),
		serializer:   codec.JSONSerializer{},
		metricClient: metricClient,
	}
}

func (r *redisClient) CreateRedisLock(key string, leaseTime time.Duration) Lock {
	mutex := r.redisSync.NewMutex(key,
		redsync.WithExpiry(leaseTime),
		redsync.WithTries(defaultTryLockTime),
		redsync.WithRetryDelay(defaultTryLockDelay))
	return &lock{
		Mutex: mutex,
	}
}

func (r *redisClient) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

func (r *redisClient) Close() error {
	return r.client.Close()
}

func (r *redisClient) Set(
	ctx context.Context, caller string, key string, value any, expired time.Duration,
) error {
	defer metrics.MonitoringTimed(r.metricClient, metricName)("caller", caller)

	jsonData, err := r.serializer.Serialize(value)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to serialize value", "key", key, slog.Any("error", err))
		return err
	}

	if _, err = r.client.Set(ctx, key, jsonData, expired).Result(); err != nil {
		slog.ErrorContext(ctx, "Failed to set", "key", key, slog.Any("error", err))
		return err
	}

	return nil
}

func (r *redisClient) Delete(ctx context.Context, caller string, key string) error {
	defer metrics.MonitoringTimed(r.metricClient, metricName)("caller", caller)

	if _, err := r.client.Del(ctx, key).Result(); err != nil {
		slog.ErrorContext(ctx, "Failed to delete", "key", key, slog.Any("error", err))
		return err
	}

	return nil
}

func (r *redisClient) GetTTLByKey(ctx context.Context, caller string, key string) (time.Duration, error) {
	defer metrics.MonitoringTimed(r.metricClient, metricName)("caller", caller)
	data, err := r.client.TTL(ctx, key).Result()
	if err != nil {
		slog.ErrorContext(ctx, "Failed to get TTL", "key", key, slog.Any("error", err))
		return 0, err
	}

	return data, nil
}

func (r *redisClient) Get(ctx context.Context, caller string, key string, value any) error {
	defer metrics.MonitoringTimed(r.metricClient, metricName)("caller", caller)

	jsonData, err := r.client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return ErrNotFound
		}
		slog.ErrorContext(ctx, "Failed to get", "key", key, slog.Any("error", err))
		return err
	}

	err = r.serializer.Deserialize(jsonData, value)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to deserialize value", "key", key, slog.Any("error", err))
		return err
	}

	return nil
}

func (r *redisClient) SetNx(
	ctx context.Context,
	caller string,
	key string,
	value any,
	expired time.Duration,
) (bool, error) {
	defer metrics.MonitoringTimed(r.metricClient, metricName)("caller", caller)

	jsonData, err := r.serializer.Serialize(value)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to serialize value", "key", key, slog.Any("error", err))
		return false, err
	}

	ok, err := r.client.SetNX(ctx, key, jsonData, expired).Result()
	if err != nil {
		slog.ErrorContext(ctx, "Failed to setnx", "key", key, slog.Any("error", err))
		return false, err
	}

	return ok, nil
}
