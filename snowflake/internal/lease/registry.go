package lease

import (
	"context"
	"errors"
	"sync"
	"time"
)

// DefaultTTL is how long a node ID lease lasts without renewal.
const DefaultTTL = time.Minute

// ErrAlreadyAcquired is returned by Acquire when a node ID is already held.
var ErrAlreadyAcquired = errors.New("lease: node ID already acquired, call Release first")

// Backend stores node ID leases. Every method must be atomic: two owners must never
// hold the same node ID at once.
type Backend interface {
	// Acquire takes the lowest free or expired node ID for owner until ttl from now.
	// It returns snowflake.ErrNoNodeIDAvailable if every node ID is held.
	Acquire(ctx context.Context, owner string, ttl time.Duration) (int64, error)
	// Renew extends owner's lease on nodeID to ttl from now. It returns ErrNotOwner
	// if owner no longer holds nodeID.
	Renew(ctx context.Context, owner string, nodeID int64, ttl time.Duration) error
	// Release frees nodeID if owner still holds it.
	Release(ctx context.Context, owner string, nodeID int64) error
}

// Registry leases one node ID at a time from a Backend and keeps it renewed.
type Registry struct {
	backend Backend
	owner   string
	ttl     time.Duration

	mu     sync.Mutex
	nodeID int64
	keeper *Keeper
}

// NewRegistry creates a Registry. ttl must be positive.
func NewRegistry(backend Backend, owner string, ttl time.Duration) *Registry {
	return &Registry{backend: backend, owner: owner, ttl: ttl}
}

// Acquire leases a node ID and starts renewing it.
func (r *Registry) Acquire(ctx context.Context) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.keeper != nil {
		return 0, ErrAlreadyAcquired
	}

	acquiredAt := time.Now()
	nodeID, err := r.backend.Acquire(ctx, r.owner, r.ttl)
	if err != nil {
		return 0, err
	}

	r.nodeID = nodeID
	r.keeper = Start(acquiredAt, r.ttl, func(ctx context.Context) error {
		return r.backend.Renew(ctx, r.owner, nodeID, r.ttl)
	})
	return nodeID, nil
}

// Release stops renewing the lease and frees the node ID. It does nothing if no
// node ID is held.
func (r *Registry) Release(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.keeper == nil {
		return nil
	}
	r.keeper.Stop()
	r.keeper = nil
	return r.backend.Release(ctx, r.owner, r.nodeID)
}

// Lost is closed when the current lease is lost. It returns nil if no node ID is held.
func (r *Registry) Lost() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.keeper == nil {
		return nil
	}
	return r.keeper.Lost()
}
