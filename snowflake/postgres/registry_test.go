package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/0x48core/gokit/snowflake"
)

// Integration tests run against the database in SNOWFLAKE_POSTGRES_DSN, for example
// "postgres://postgres:pass@127.0.0.1:5432/snowflake". They are skipped when it is unset.
func openDB(t *testing.T) (db *sql.DB, table string) {
	t.Helper()
	dsn := os.Getenv("SNOWFLAKE_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("SNOWFLAKE_POSTGRES_DSN not set")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	table = fmt.Sprintf("snowflake_test_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP TABLE IF EXISTS "` + table + `"`)
		_ = db.Close()
	})
	return db, table
}

func newRegistry(t *testing.T, db *sql.DB, table string) *Registry {
	t.Helper()
	r, err := New(db, WithTable(table), WithTTL(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Release(context.Background()) })
	return r
}

func TestNewValidation(t *testing.T) {
	for _, opt := range []Option{WithTable("bad-name"), WithTable(`x"; DROP TABLE y`), WithTable("UpperCase"), WithTTL(time.Second), WithOwner("")} {
		if _, err := New(nil, opt); err == nil {
			t.Error("New accepted an invalid option")
		}
	}
}

func TestConcurrentAcquireGetsDistinctNodeIDs(t *testing.T) {
	db, table := openDB(t)
	ctx := context.Background()

	const n = 20
	ids := make([]int64, n)
	var wg sync.WaitGroup
	for i := range n {
		r := newRegistry(t, db, table)
		wg.Go(func() {
			id, err := r.Acquire(ctx)
			if err != nil {
				t.Error(err)
			}
			ids[i] = id
		})
	}
	wg.Wait()

	slices.Sort(ids)
	for i, id := range ids {
		if id != int64(i) {
			t.Fatalf("node IDs = %v, want 0..%d", ids, n-1)
		}
	}
}

func TestReleaseFreesNodeID(t *testing.T) {
	db, table := openDB(t)
	ctx := context.Background()

	a := newRegistry(t, db, table)
	idA, err := a.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Release(ctx); err != nil {
		t.Fatal(err)
	}

	b := newRegistry(t, db, table)
	idB, err := b.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if idA != idB {
		t.Fatalf("after release got node ID %d, want %d", idB, idA)
	}
}

func TestExpiredLeaseIsTakenOverAndReportedLost(t *testing.T) {
	db, table := openDB(t)
	ctx := context.Background()

	a := newRegistry(t, db, table)
	idA, err := a.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate A's lease expiring, for example because A was paused.
	if _, err := db.Exec(`UPDATE "`+table+`" SET expires_at = now() WHERE node_id = $1`, idA); err != nil {
		t.Fatal(err)
	}

	b := newRegistry(t, db, table)
	idB, err := b.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if idA != idB {
		t.Fatalf("B got node ID %d, want A's expired %d", idB, idA)
	}

	select {
	case <-a.Lost():
	case <-time.After(3 * time.Second):
		t.Fatal("A did not report its lease as lost")
	}
	select {
	case <-b.Lost():
		t.Fatal("B reported its lease as lost")
	default:
	}
}

func TestLeaseIsRenewed(t *testing.T) {
	db, table := openDB(t)
	ctx := context.Background()

	a := newRegistry(t, db, table)
	idA, err := a.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(4 * time.Second) // longer than the TTL

	b := newRegistry(t, db, table)
	idB, err := b.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if idA == idB {
		t.Fatalf("B took A's node ID %d although A kept renewing it", idA)
	}
}

func TestNoNodeIDAvailable(t *testing.T) {
	db, table := openDB(t)
	ctx := context.Background()

	a := newRegistry(t, db, table)
	if _, err := a.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE "` + table + `" SET expires_at = now() + interval '1 hour'`); err != nil {
		t.Fatal(err)
	}

	b := newRegistry(t, db, table)
	if _, err := b.Acquire(ctx); !errors.Is(err, snowflake.ErrNoNodeIDAvailable) {
		t.Fatalf("Acquire error = %v, want ErrNoNodeIDAvailable", err)
	}
}

func TestNewWithRegistry(t *testing.T) {
	db, table := openDB(t)
	r := newRegistry(t, db, table)

	node, err := snowflake.NewWithRegistry(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	id, err := node.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if got := node.Parse(id).NodeID; got != node.NodeID() {
		t.Fatalf("parsed node ID = %d, want %d", got, node.NodeID())
	}
}
