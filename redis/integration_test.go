package redis

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/0x48core/gokit/metrics"
	"github.com/go-redsync/redsync/v4"
	"github.com/prometheus/client_golang/prometheus"
)

// Integration tests run against the comma-separated addresses in REDIS_ADDR,
// for example "127.0.0.1:36379". They are skipped when it is unset.
func newIntegrationClient(t *testing.T) Client {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("REDIS_ADDR not set")
	}

	client := NewClient(&Config{
		Addr:         strings.Split(addr, ","),
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
	}, metrics.NewClientWithRegisterer("redis", "test", prometheus.NewRegistry()))
	t.Cleanup(func() { _ = client.Close() })

	if err := client.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	return client
}

func newKey(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("gokit_test_%s_%d", t.Name(), time.Now().UnixNano())
}

func TestIntegrationSetGetDelete(t *testing.T) {
	client := newIntegrationClient(t)
	ctx := context.Background()
	key := newKey(t)

	if err := client.Set(ctx, "test", key, mockDataStruct(), time.Minute); err != nil {
		t.Fatal(err)
	}

	var got Test
	if err := client.Get(ctx, "test", key, &got); err != nil {
		t.Fatal(err)
	}
	if got != mockDataStruct() {
		t.Fatalf("got %+v, want %+v", got, mockDataStruct())
	}

	ttl, err := client.GetTTLByKey(ctx, "test", key)
	if err != nil {
		t.Fatal(err)
	}
	if ttl <= 0 || ttl > time.Minute {
		t.Fatalf("ttl = %v, want (0, 1m]", ttl)
	}

	if err := client.Delete(ctx, "test", key); err != nil {
		t.Fatal(err)
	}
	if err := client.Get(ctx, "test", key, &got); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete: err = %v, want ErrNotFound", err)
	}
}

func TestIntegrationSetNx(t *testing.T) {
	client := newIntegrationClient(t)
	ctx := context.Background()
	key := newKey(t)

	ok, err := client.SetNx(ctx, "test", key, "first", time.Minute)
	if err != nil || !ok {
		t.Fatalf("first SetNx = %v, %v; want true, nil", ok, err)
	}
	ok, err = client.SetNx(ctx, "test", key, "second", time.Minute)
	if err != nil || ok {
		t.Fatalf("second SetNx = %v, %v; want false, nil", ok, err)
	}

	var got string
	if err := client.Get(ctx, "test", key, &got); err != nil {
		t.Fatal(err)
	}
	if got != "first" {
		t.Fatalf("got %q, want %q", got, "first")
	}
}

func TestIntegrationLock(t *testing.T) {
	client := newIntegrationClient(t)
	ctx := context.Background()
	key := newKey(t)

	first := client.CreateRedisLock(key, 10*time.Second)
	if err := first.Lock(ctx); err != nil {
		t.Fatal(err)
	}

	// The second lock gives up after its retries while the first one holds it.
	second := client.CreateRedisLock(key, 10*time.Second)
	var taken *redsync.ErrTaken
	if err := second.Lock(ctx); !errors.As(err, &taken) {
		t.Fatalf("second Lock: err = %v, want ErrTaken", err)
	}

	if err := first.UnLock(ctx); err != nil {
		t.Fatal(err)
	}
	if err := second.Lock(ctx); err != nil {
		t.Fatalf("Lock after UnLock: %v", err)
	}
	if err := second.UnLock(ctx); err != nil {
		t.Fatal(err)
	}
}
