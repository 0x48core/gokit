# Redis

Redis cache client and distributed lock built on [go-redis](https://github.com/redis/go-redis) and
[redsync](https://github.com/go-redsync/redsync).

- One client for a single node or a cluster, depending on how many addresses you pass
- Values stored as JSON
- Each call's latency recorded in a `redis` histogram from [metrics](../metrics), tagged with the caller
- Distributed locks with a lease time
- A gomock mock of `Client` and `Lock` in [mocks](./mocks)

## Installation

```sh
go get github.com/0x48core/gokit/redis
```

Requires Go 1.25+.

## Usage

```go
client := redis.NewClient(&redis.Config{
	Addr:         []string{"localhost:6379"}, // several addresses connect to a cluster
	PoolSize:     10,
	ReadTimeout:  time.Second,
	WriteTimeout: time.Second,
}, metrics.NewClient("orders", "production"))
defer client.Close()

if err := client.Ping(ctx); err != nil {
	return err
}
```

`Config` has `mapstructure` tags, so you can load it with viper. `ReadTimeout` is also the dial timeout.

### Cache

The `caller` argument names the code path in the `caller` metric tag.

```go
err := client.Set(ctx, "GetOrder", "order:1", order, 10*time.Minute)

var order Order
err = client.Get(ctx, "GetOrder", "order:1", &order)
if errors.Is(err, redis.ErrNotFound) {
	// cache miss
}

ok, err := client.SetNx(ctx, "CreateOrder", "order:1:created", true, time.Hour) // false when the key exists
ttl, err := client.GetTTLByKey(ctx, "GetOrder", "order:1")
err = client.Delete(ctx, "GetOrder", "order:1")
```

### Lock

```go
lock := client.CreateRedisLock("order:1:lock", 10*time.Second)
if err := lock.Lock(ctx); err != nil {
	return err // held by someone else after 3 tries, 500ms apart
}
defer lock.UnLock(ctx)
```

The lock expires after the lease time, even if `UnLock` is never called. Keep the lease longer
than the work it protects.

### Mocks

```go
ctrl := gomock.NewController(t)
cache := redismocks.NewMockClient(ctrl)
cache.EXPECT().Get(gomock.Any(), "GetOrder", "order:1", gomock.Any()).Return(redis.ErrNotFound)
```

Regenerate them with `go generate ./...` after changing the interfaces.

## API

| Name | Description |
| --- | --- |
| `NewClient(conf, metricClient)` | Connects to a single node or a cluster. |
| `Set`, `Get`, `SetNx`, `Delete`, `GetTTLByKey` | Cache operations. Values are JSON-encoded. |
| `CreateRedisLock(key, leaseTime)` | Returns a `Lock` with `Lock` and `UnLock`. |
| `Ping`, `Close` | Check and close the connection. |
| `ErrNotFound` | Returned by `Get` when the key does not exist. |
| `codec.Serializer`, `codec.JSONSerializer` | The value encoding. |

## Development

```sh
make fmt               # goimports, gofumpt, gci
make lint              # golangci-lint + go vet
make test              # unit tests; Redis tests are skipped
make test-integration  # all tests against a single-node Redis in Docker
```

The Redis tests read `REDIS_ADDR` (comma-separated) and are skipped when it is unset.
