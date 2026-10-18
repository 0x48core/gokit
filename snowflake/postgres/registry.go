// Package postgres provides a snowflake.NodeRegistry that leases node IDs from a
// PostgreSQL table.
//
// The registry creates its table on first use and fills it with one row per node ID.
// Each process leases the lowest free row and renews the lease in the background; a
// crashed process's node ID becomes free again once its lease expires. All times are
// taken from the database clock, so clock differences between hosts don't matter.
//
// Requires PostgreSQL 9.5 or later (FOR UPDATE SKIP LOCKED). The caller opens the
// *sql.DB, for example with github.com/jackc/pgx/v5/stdlib.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/0x48core/gokit/snowflake"
	"github.com/0x48core/gokit/snowflake/internal/lease"
)

// DefaultTable is the table name used when no WithTable option is given.
const DefaultTable = "snowflake_nodes"

var tableNameRe = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// Registry leases node IDs from a PostgreSQL table. It is safe for concurrent use.
type Registry struct {
	leases *lease.Registry
}

var _ snowflake.NodeRegistry = (*Registry)(nil)

type config struct {
	table string
	ttl   time.Duration
	owner string
}

// Option configures a Registry.
type Option func(*config)

// WithTable sets the table name. It must be a lowercase identifier (letters, digits,
// underscores) and is created in the connection's default schema.
func WithTable(table string) Option {
	return func(c *config) {
		c.table = table
	}
}

// WithTTL sets how long a lease lasts without renewal. It is renewed every TTL/3.
// A shorter TTL frees a crashed process's node ID sooner, but tolerates shorter
// database outages. Defaults to one minute; must be at least 3 seconds.
func WithTTL(ttl time.Duration) Option {
	return func(c *config) {
		c.ttl = ttl
	}
}

// WithOwner sets the identifier recorded as the lease holder (at most 255 characters).
// Defaults to the hostname plus a random suffix.
func WithOwner(owner string) Option {
	return func(c *config) {
		c.owner = owner
	}
}

// New creates a Registry backed by db.
func New(db *sql.DB, opts ...Option) (*Registry, error) {
	cfg := config{table: DefaultTable, ttl: lease.DefaultTTL, owner: lease.NewOwner()}
	for _, opt := range opts {
		opt(&cfg)
	}

	switch {
	case !tableNameRe.MatchString(cfg.table):
		return nil, fmt.Errorf("snowflake/postgres: invalid table name %q", cfg.table)
	case cfg.ttl < 3*time.Second:
		return nil, fmt.Errorf("snowflake/postgres: TTL %s is shorter than 3s", cfg.ttl)
	case cfg.owner == "" || len(cfg.owner) > 255:
		return nil, errors.New("snowflake/postgres: owner must be 1 to 255 characters")
	}

	b := &backend{db: db, table: `"` + cfg.table + `"`}
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
	db    *sql.DB
	table string
}

func (b *backend) setup(ctx context.Context) (err error) {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("snowflake/postgres: begin: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Concurrent CREATE TABLE IF NOT EXISTS can fail in PostgreSQL, so let one
	// process at a time create and fill the table.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, b.table); err != nil {
		return fmt.Errorf("snowflake/postgres: lock: %w", err)
	}

	_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+b.table+` (
		node_id    SMALLINT     NOT NULL PRIMARY KEY,
		owner      VARCHAR(255) NOT NULL DEFAULT '',
		expires_at TIMESTAMPTZ  NOT NULL DEFAULT '-infinity'
	)`)
	if err != nil {
		return fmt.Errorf("snowflake/postgres: create table: %w", err)
	}

	_, err = tx.ExecContext(ctx, `INSERT INTO `+b.table+` (node_id)
		SELECT generate_series(0, $1) ON CONFLICT DO NOTHING`, snowflake.MaxNodeID)
	if err != nil {
		return fmt.Errorf("snowflake/postgres: insert node IDs: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("snowflake/postgres: commit: %w", err)
	}
	return nil
}

func (b *backend) Acquire(ctx context.Context, owner string, ttl time.Duration) (int64, error) {
	if err := b.setup(ctx); err != nil {
		return 0, err
	}

	// A single statement is atomic; SKIP LOCKED lets concurrent acquirers pick
	// different rows instead of waiting for each other.
	var nodeID int64
	err := b.db.QueryRowContext(ctx, `UPDATE `+b.table+`
		SET owner = $1, expires_at = now() + $2 * interval '1 microsecond'
		WHERE node_id = (
			SELECT node_id FROM `+b.table+`
			WHERE expires_at < now()
			ORDER BY node_id LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING node_id`, owner, ttl.Microseconds()).Scan(&nodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, snowflake.ErrNoNodeIDAvailable
	}
	if err != nil {
		return 0, fmt.Errorf("snowflake/postgres: lease node ID: %w", err)
	}
	return nodeID, nil
}

func (b *backend) Renew(ctx context.Context, owner string, nodeID int64, ttl time.Duration) error {
	// The expires_at > now() condition stops us from reviving a lease that already
	// expired, since another process may have taken the node ID in between.
	res, err := b.db.ExecContext(ctx, `UPDATE `+b.table+`
		SET expires_at = now() + $1 * interval '1 microsecond'
		WHERE node_id = $2 AND owner = $3 AND expires_at > now()`,
		ttl.Microseconds(), nodeID, owner)
	if err != nil {
		return fmt.Errorf("snowflake/postgres: renew lease: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("snowflake/postgres: renew lease: %w", err)
	}
	if n == 0 {
		return lease.ErrNotOwner
	}
	return nil
}

func (b *backend) Release(ctx context.Context, owner string, nodeID int64) error {
	_, err := b.db.ExecContext(ctx, `UPDATE `+b.table+`
		SET owner = '', expires_at = '-infinity'
		WHERE node_id = $1 AND owner = $2`, nodeID, owner)
	if err != nil {
		return fmt.Errorf("snowflake/postgres: release lease: %w", err)
	}
	return nil
}
