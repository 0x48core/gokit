package mongodb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/0x48core/gokit/snowflake"
)

// Integration tests run against the server in SNOWFLAKE_MONGODB_URI, for example
// "mongodb://127.0.0.1:27017". They are skipped when it is unset.
func openCollection(t *testing.T) *mongo.Collection {
	t.Helper()
	uri := os.Getenv("SNOWFLAKE_MONGODB_URI")
	if uri == "" {
		t.Skip("SNOWFLAKE_MONGODB_URI not set")
	}

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	coll := client.Database("snowflake_test").Collection(fmt.Sprintf("nodes_%d", time.Now().UnixNano()))
	t.Cleanup(func() {
		ctx := context.Background()
		_ = coll.Drop(ctx)
		_ = client.Disconnect(ctx)
	})
	return coll
}

func newRegistry(t *testing.T, coll *mongo.Collection) *Registry {
	t.Helper()
	r, err := New(coll, WithTTL(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Release(context.Background()) })
	return r
}

func TestNewValidation(t *testing.T) {
	for _, opt := range []Option{WithTTL(time.Second), WithOwner("")} {
		if _, err := New(nil, opt); err == nil {
			t.Error("New accepted an invalid option")
		}
	}
}

func TestConcurrentAcquireGetsDistinctNodeIDs(t *testing.T) {
	coll := openCollection(t)
	ctx := context.Background()

	const n = 20
	ids := make([]int64, n)
	var wg sync.WaitGroup
	for i := range n {
		r := newRegistry(t, coll)
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
	coll := openCollection(t)
	ctx := context.Background()

	a := newRegistry(t, coll)
	idA, err := a.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Release(ctx); err != nil {
		t.Fatal(err)
	}

	b := newRegistry(t, coll)
	idB, err := b.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if idA != idB {
		t.Fatalf("after release got node ID %d, want %d", idB, idA)
	}
}

func TestExpiredLeaseIsTakenOverAndReportedLost(t *testing.T) {
	coll := openCollection(t)
	ctx := context.Background()

	a := newRegistry(t, coll)
	idA, err := a.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate A's lease expiring, for example because A was paused.
	_, err = coll.UpdateByID(ctx, idA, bson.D{{Key: "$set", Value: bson.D{{Key: "expires_at", Value: time.Now().Add(-time.Second)}}}})
	if err != nil {
		t.Fatal(err)
	}

	b := newRegistry(t, coll)
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
	coll := openCollection(t)
	ctx := context.Background()

	a := newRegistry(t, coll)
	idA, err := a.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(4 * time.Second) // longer than the TTL

	b := newRegistry(t, coll)
	idB, err := b.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if idA == idB {
		t.Fatalf("B took A's node ID %d although A kept renewing it", idA)
	}
}

func TestNoNodeIDAvailable(t *testing.T) {
	coll := openCollection(t)
	ctx := context.Background()

	a := newRegistry(t, coll)
	if _, err := a.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := coll.UpdateMany(ctx, bson.D{}, bson.D{{Key: "$set", Value: bson.D{{Key: "expires_at", Value: time.Now().Add(time.Hour)}}}})
	if err != nil {
		t.Fatal(err)
	}

	b := newRegistry(t, coll)
	if _, err := b.Acquire(ctx); !errors.Is(err, snowflake.ErrNoNodeIDAvailable) {
		t.Fatalf("Acquire error = %v, want ErrNoNodeIDAvailable", err)
	}
}

func TestNewWithRegistry(t *testing.T) {
	coll := openCollection(t)
	r := newRegistry(t, coll)

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
