// Package mongodb provides a snowflake.NodeRegistry that leases node IDs from a
// MongoDB collection.
//
// The registry fills its collection with one document per node ID on first use. Each
// process leases the lowest free document and renews the lease in the background; a
// crashed process's node ID becomes free again once its lease expires. All times are
// taken from the server clock ($$NOW), so clock differences between hosts don't matter.
//
// Requires MongoDB 4.2 or later.
package mongodb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/0x48core/gokit/snowflake"
	"github.com/0x48core/gokit/snowflake/internal/lease"
)

// DefaultCollection is the collection name to use if you have no preference.
const DefaultCollection = "snowflake_nodes"

// Registry leases node IDs from a MongoDB collection. It is safe for concurrent use.
type Registry struct {
	leases *lease.Registry
}

var _ snowflake.NodeRegistry = (*Registry)(nil)

type config struct {
	ttl   time.Duration
	owner string
}

// Option configures a Registry.
type Option func(*config)

// WithTTL sets how long a lease lasts without renewal. It is renewed every TTL/3.
// A shorter TTL frees a crashed process's node ID sooner, but tolerates shorter
// database outages. Defaults to one minute; must be at least 3 seconds.
func WithTTL(ttl time.Duration) Option {
	return func(c *config) {
		c.ttl = ttl
	}
}

// WithOwner sets the identifier recorded as the lease holder. Defaults to the
// hostname plus a random suffix.
func WithOwner(owner string) Option {
	return func(c *config) {
		c.owner = owner
	}
}

// New creates a Registry backed by coll, for example
// client.Database("myapp").Collection(mongodb.DefaultCollection).
func New(coll *mongo.Collection, opts ...Option) (*Registry, error) {
	cfg := config{ttl: lease.DefaultTTL, owner: lease.NewOwner()}
	for _, opt := range opts {
		opt(&cfg)
	}

	switch {
	case cfg.ttl < 3*time.Second:
		return nil, fmt.Errorf("snowflake/mongodb: TTL %s is shorter than 3s", cfg.ttl)
	case cfg.owner == "":
		return nil, errors.New("snowflake/mongodb: owner must not be empty")
	}

	b := &backend{coll: coll}
	return &Registry{leases: lease.NewRegistry(b, cfg.owner, cfg.ttl)}, nil
}

// Acquire leases the lowest free node ID and starts renewing it in the background.
// It returns snowflake.ErrNoNodeIDAvailable if all node IDs are leased.
func (r *Registry) Acquire(ctx context.Context) (int64, error) {
	return r.leases.Acquire(ctx)
}

// Release stops renewing the lease and frees the node ID.
func (r *Registry) Release(ctx context.Context) error {
	return r.leases.Release(ctx)
}

// Lost is closed if the lease could not be renewed in time or was taken over.
func (r *Registry) Lost() <-chan struct{} {
	return r.leases.Lost()
}

type backend struct {
	coll *mongo.Collection
}

type nodeDoc struct {
	NodeID    int64     `bson:"_id"`
	Owner     string    `bson:"owner"`
	ExpiresAt time.Time `bson:"expires_at"`
}

// expiredAt marks a node ID as free.
var expiredAt = time.Unix(0, 0).UTC()

func (b *backend) setup(ctx context.Context) error {
	count, err := b.coll.CountDocuments(ctx, bson.D{})
	if err != nil {
		return fmt.Errorf("snowflake/mongodb: count node IDs: %w", err)
	}
	if count > snowflake.MaxNodeID {
		return nil
	}

	docs := make([]nodeDoc, snowflake.MaxNodeID+1)
	for i := range docs {
		docs[i] = nodeDoc{NodeID: int64(i), ExpiresAt: expiredAt}
	}
	// Unordered so documents already inserted by another process are skipped.
	_, err = b.coll.InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
	if err != nil && !onlyDuplicateKeyErrors(err) {
		return fmt.Errorf("snowflake/mongodb: insert node IDs: %w", err)
	}
	return nil
}

func onlyDuplicateKeyErrors(err error) bool {
	var bwe mongo.BulkWriteException
	if !errors.As(err, &bwe) || bwe.WriteConcernError != nil {
		return false
	}
	for _, we := range bwe.WriteErrors {
		if !we.HasErrorCode(11000) {
			return false
		}
	}
	return true
}

// expiresAt is an update pipeline expression for ttl from the server's current time.
func expiresAt(ttl time.Duration) bson.D {
	return bson.D{{Key: "$add", Value: bson.A{"$$NOW", ttl.Milliseconds()}}}
}

func (b *backend) Acquire(ctx context.Context, owner string, ttl time.Duration) (int64, error) {
	if err := b.setup(ctx); err != nil {
		return 0, err
	}

	filter := bson.D{{Key: "$expr", Value: bson.D{{Key: "$lt", Value: bson.A{"$expires_at", "$$NOW"}}}}}
	update := mongo.Pipeline{{{Key: "$set", Value: bson.D{
		{Key: "owner", Value: owner},
		{Key: "expires_at", Value: expiresAt(ttl)},
	}}}}
	opts := options.FindOneAndUpdate().
		SetSort(bson.D{{Key: "_id", Value: 1}}).
		SetProjection(bson.D{{Key: "_id", Value: 1}})

	var doc nodeDoc
	err := b.coll.FindOneAndUpdate(ctx, filter, update, opts).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return 0, snowflake.ErrNoNodeIDAvailable
	}
	if err != nil {
		return 0, fmt.Errorf("snowflake/mongodb: lease node ID: %w", err)
	}
	return doc.NodeID, nil
}

func (b *backend) Renew(ctx context.Context, owner string, nodeID int64, ttl time.Duration) error {
	// Only renew a lease that hasn't expired, since another process may have taken
	// the node ID after it did.
	filter := bson.D{
		{Key: "_id", Value: nodeID},
		{Key: "owner", Value: owner},
		{Key: "$expr", Value: bson.D{{Key: "$gt", Value: bson.A{"$expires_at", "$$NOW"}}}},
	}
	update := mongo.Pipeline{{{Key: "$set", Value: bson.D{{Key: "expires_at", Value: expiresAt(ttl)}}}}}

	res, err := b.coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("snowflake/mongodb: renew lease: %w", err)
	}
	if res.MatchedCount == 0 {
		return lease.ErrNotOwner
	}
	return nil
}

func (b *backend) Release(ctx context.Context, owner string, nodeID int64) error {
	filter := bson.D{{Key: "_id", Value: nodeID}, {Key: "owner", Value: owner}}
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "owner", Value: ""},
		{Key: "expires_at", Value: expiredAt},
	}}}
	if _, err := b.coll.UpdateOne(ctx, filter, update); err != nil {
		return fmt.Errorf("snowflake/mongodb: release lease: %w", err)
	}
	return nil
}
