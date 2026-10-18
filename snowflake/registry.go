package snowflake

import (
	"context"
	"errors"
)

// ErrNoNodeIDAvailable is returned by a NodeRegistry when all node IDs are in use.
var ErrNoNodeIDAvailable = errors.New("snowflake: no node ID available")

// NodeRegistry assigns node IDs so that no two running nodes share one.
//
// The mysql and mongodb subpackages provide registries that lease node IDs from a
// database table or collection.
type NodeRegistry interface {
	// Acquire reserves a free node ID in [0, MaxNodeID] for this process.
	Acquire(ctx context.Context) (int64, error)
	// Release gives the node ID back so another process can use it.
	Release(ctx context.Context) error
	// Lost returns a channel that is closed if the node ID can no longer be
	// guaranteed to be ours. It may return nil if that can never happen.
	Lost() <-chan struct{}
}

// NewWithRegistry acquires a node ID from registry and creates a Node with it.
// Once the registry reports the node ID as lost, Generate returns ErrLeaseLost.
//
// Call registry.Release when the Node is no longer needed.
func NewWithRegistry(ctx context.Context, registry NodeRegistry, opts ...Option) (*Node, error) {
	nodeID, err := registry.Acquire(ctx)
	if err != nil {
		return nil, err
	}

	node, err := New(nodeID, append(opts, withLeaseLost(registry.Lost()))...)
	if err != nil {
		return nil, errors.Join(err, registry.Release(ctx))
	}
	return node, nil
}
