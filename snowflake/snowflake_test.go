package snowflake

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestNewInvalidNodeID(t *testing.T) {
	for _, id := range []int64{-1, MaxNodeID + 1} {
		if _, err := New(id); !errors.Is(err, ErrInvalidNodeID) {
			t.Errorf("New(%d) error = %v, want ErrInvalidNodeID", id, err)
		}
	}
}

func TestNewFutureEpoch(t *testing.T) {
	_, err := New(1, WithEpoch(time.Now().Add(time.Hour)))
	if !errors.Is(err, ErrInvalidEpoch) {
		t.Fatalf("New error = %v, want ErrInvalidEpoch", err)
	}
}

func TestGenerateParse(t *testing.T) {
	epoch := time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC)
	node, err := New(42, WithEpoch(epoch))
	if err != nil {
		t.Fatal(err)
	}

	before := time.Now().Truncate(time.Millisecond)
	id, err := node.Generate()
	if err != nil {
		t.Fatal(err)
	}
	after := time.Now()

	if id <= 0 {
		t.Fatalf("id = %d, want positive", id)
	}
	parts := Parse(id, epoch)
	if parts.NodeID != 42 {
		t.Errorf("NodeID = %d, want 42", parts.NodeID)
	}
	if parts.Sequence != 0 {
		t.Errorf("Sequence = %d, want 0", parts.Sequence)
	}
	if parts.Time.Before(before) || parts.Time.After(after) {
		t.Errorf("Time = %s, want between %s and %s", parts.Time, before, after)
	}
	if node.Parse(id) != parts {
		t.Errorf("node.Parse = %+v, want %+v", node.Parse(id), parts)
	}
}

func TestGenerateConcurrentUniqueAndIncreasing(t *testing.T) {
	node, err := New(MaxNodeID)
	if err != nil {
		t.Fatal(err)
	}

	const goroutines, perGoroutine = 8, 20000
	results := make([][]int64, goroutines)
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Go(func() {
			ids := make([]int64, perGoroutine)
			for i := range ids {
				id, err := node.Generate()
				if err != nil {
					t.Error(err)
					return
				}
				ids[i] = id
			}
			results[g] = ids
		})
	}
	wg.Wait()

	seen := make(map[int64]bool, goroutines*perGoroutine)
	for _, ids := range results {
		for i, id := range ids {
			if seen[id] {
				t.Fatalf("duplicate id %d", id)
			}
			seen[id] = true
			if i > 0 && id <= ids[i-1] {
				t.Fatalf("id %d not greater than previous %d", id, ids[i-1])
			}
		}
	}
}

func TestGenerateSequenceRollover(t *testing.T) {
	node, err := New(7)
	if err != nil {
		t.Fatal(err)
	}

	// The clock is stuck at 5ms for the first maxSequence+2 calls, then moves to 6ms.
	calls := 0
	node.now = func() time.Time {
		calls++
		if calls <= maxSequence+2 {
			return node.start.Add(5 * time.Millisecond)
		}
		return node.start.Add(6 * time.Millisecond)
	}

	for seq := int64(0); seq <= maxSequence; seq++ {
		id, err := node.Generate()
		if err != nil {
			t.Fatal(err)
		}
		if got := Parse(id, node.epoch); got.Sequence != seq || got.Time != node.epoch.Add(5*time.Millisecond) {
			t.Fatalf("id %d parts = %+v, want sequence %d at 5ms", id, got, seq)
		}
	}

	id, err := node.Generate()
	if err != nil {
		t.Fatal(err)
	}
	got := Parse(id, node.epoch)
	if got.Sequence != 0 || got.Time != node.epoch.Add(6*time.Millisecond) {
		t.Fatalf("after rollover parts = %+v, want sequence 0 at 6ms", got)
	}
}

func TestGenerateTimeOverflow(t *testing.T) {
	node, err := New(1)
	if err != nil {
		t.Fatal(err)
	}
	node.now = func() time.Time {
		return node.start.Add((maxElapsed + 1) * time.Millisecond)
	}

	if _, err := node.Generate(); !errors.Is(err, ErrTimeOverflow) {
		t.Fatalf("Generate error = %v, want ErrTimeOverflow", err)
	}
}

type fakeRegistry struct {
	nodeID   int64
	err      error
	lost     chan struct{}
	released bool
}

func (r *fakeRegistry) Acquire(context.Context) (int64, error) { return r.nodeID, r.err }
func (r *fakeRegistry) Release(context.Context) error          { r.released = true; return nil }
func (r *fakeRegistry) Lost() <-chan struct{}                  { return r.lost }

func TestNewWithRegistry(t *testing.T) {
	reg := &fakeRegistry{nodeID: 99, lost: make(chan struct{})}
	node, err := NewWithRegistry(context.Background(), reg)
	if err != nil {
		t.Fatal(err)
	}
	if node.NodeID() != 99 {
		t.Fatalf("NodeID = %d, want 99", node.NodeID())
	}

	id, err := node.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if got := node.Parse(id).NodeID; got != 99 {
		t.Fatalf("parsed NodeID = %d, want 99", got)
	}

	close(reg.lost)
	if _, err := node.Generate(); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("Generate after lost error = %v, want ErrLeaseLost", err)
	}
}

func TestNewWithRegistryAcquireError(t *testing.T) {
	reg := &fakeRegistry{err: ErrNoNodeIDAvailable}
	if _, err := NewWithRegistry(context.Background(), reg); !errors.Is(err, ErrNoNodeIDAvailable) {
		t.Fatalf("error = %v, want ErrNoNodeIDAvailable", err)
	}
}

func TestNewWithRegistryReleasesOnError(t *testing.T) {
	reg := &fakeRegistry{nodeID: MaxNodeID + 1}
	if _, err := NewWithRegistry(context.Background(), reg); !errors.Is(err, ErrInvalidNodeID) {
		t.Fatalf("error = %v, want ErrInvalidNodeID", err)
	}
	if !reg.released {
		t.Fatal("node ID was not released")
	}
}

func BenchmarkGenerate(b *testing.B) {
	node, err := New(1)
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		_, _ = node.Generate()
	}
}
