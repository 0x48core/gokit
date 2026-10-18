// Package mysql provides a snowflake.NodeRegistry that leases node IDs from a MySQL table.
//
// The registry creates its table on first use and fills it with one row per node ID.
// Each process leases the lowest free row and renews the lease in the background; a
// crashed process's node ID becomes free again once its lease expires. All times are
// taken from the database clock, so clock differences between hosts don't matter.
//
// Tested with MySQL 8.4. The caller opens the *sql.DB, typically with
// github.com/go-sql-driver/mysql.
package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/0x48core/gokit/snowflake"
	"github.com/0x48core/gokit/snowflake/internal/lease"
)

// DefaultTable is the table name used when no WithTable option is given.
const DefaultTable = "snowflake_nodes"

var tableNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// Registry leases node IDs from a MySQL table. It is safe for concurrent use.
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

// WithTable sets the table name. It must be a plain identifier (letters, digits, underscores).
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
		return nil, fmt.Errorf("snowflake/mysql: invalid table name %q", cfg.table)
	case cfg.ttl < 3*time.Second:
		return nil, fmt.Errorf("snowflake/mysql: TTL %s is shorter than 3s", cfg.ttl)
	case cfg.owner == "" || len(cfg.owner) > 255:
		return nil, errors.New("snowflake/mysql: owner must be 1 to 255 characters")
	}

	b := &backend{db: db, table: "`" + cfg.table + "`"}
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

// expiredAt marks a node ID as free. DATETIME accepts years from 1000.
const expiredAt = "1000-01-01 00:00:00"

func (b *backend) setup(ctx context.Context) error {
	_, err := b.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+b.table+` (
		node_id    SMALLINT UNSIGNED NOT NULL PRIMARY KEY,
		owner      VARCHAR(255)      NOT NULL DEFAULT '',
		expires_at DATETIME(3)       NOT NULL DEFAULT '`+expiredAt+`'
	)`)
	if err != nil {
		return fmt.Errorf("snowflake/mysql: create table: %w", err)
	}

	var count int
	err = b.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+b.table).Scan(&count)
	if err != nil {
		return fmt.Errorf("snowflake/mysql: count node IDs: %w", err)
	}
	if count > snowflake.MaxNodeID {
		return nil
	}

	values := make([]string, snowflake.MaxNodeID+1)
	for i := range values {
		values[i] = "(" + strconv.Itoa(i) + ")"
	}
	_, err = b.db.ExecContext(ctx, `INSERT IGNORE INTO `+b.table+` (node_id) VALUES `+strings.Join(values, ","))
	if err != nil {
		return fmt.Errorf("snowflake/mysql: insert node IDs: %w", err)
	}
	return nil
}

// acquireAttempts bounds retries of the acquire transaction. On first use, processes
// seeding the table at the same time can deadlock with each other's acquire.
const acquireAttempts = 3

func (b *backend) Acquire(ctx context.Context, owner string, ttl time.Duration) (int64, error) {
	if err := b.setup(ctx); err != nil {
		return 0, err
	}

	var err error
	for range acquireAttempts {
		var nodeID int64
		nodeID, err = b.acquire(ctx, owner, ttl)
		if err == nil || errors.Is(err, snowflake.ErrNoNodeIDAvailable) || ctx.Err() != nil {
			return nodeID, err
		}
	}
	return 0, err
}

func (b *backend) acquire(ctx context.Context, owner string, ttl time.Duration) (nodeID int64, err error) {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("snowflake/mysql: begin: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Concurrent acquirers wait on the same row and then move on to the next free
	// one. SKIP LOCKED is not used: it also skips rows another process is still
	// inserting, and can then wrongly find no free node ID.
	err = tx.QueryRowContext(ctx, `SELECT node_id FROM `+b.table+`
		WHERE expires_at < UTC_TIMESTAMP(3)
		ORDER BY node_id LIMIT 1
		FOR UPDATE`).Scan(&nodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, snowflake.ErrNoNodeIDAvailable
	}
	if err != nil {
		return 0, fmt.Errorf("snowflake/mysql: select free node ID: %w", err)
	}

	_, err = tx.ExecContext(ctx, `UPDATE `+b.table+`
		SET owner = ?, expires_at = UTC_TIMESTAMP(3) + INTERVAL ? MICROSECOND
		WHERE node_id = ?`, owner, ttl.Microseconds(), nodeID)
	if err != nil {
		return 0, fmt.Errorf("snowflake/mysql: lease node ID: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return 0, fmt.Errorf("snowflake/mysql: commit: %w", err)
	}
	return nodeID, nil
}

func (b *backend) Renew(ctx context.Context, owner string, nodeID int64, ttl time.Duration) error {
	// The expires_at > now condition stops us from reviving a lease that already
	// expired, since another process may have taken the node ID in between.
	res, err := b.db.ExecContext(ctx, `UPDATE `+b.table+`
		SET expires_at = UTC_TIMESTAMP(3) + INTERVAL ? MICROSECOND
		WHERE node_id = ? AND owner = ? AND expires_at > UTC_TIMESTAMP(3)`,
		ttl.Microseconds(), nodeID, owner)
	if err != nil {
		return fmt.Errorf("snowflake/mysql: renew lease: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("snowflake/mysql: renew lease: %w", err)
	}
	if n == 0 {
		return lease.ErrNotOwner
	}
	return nil
}

func (b *backend) Release(ctx context.Context, owner string, nodeID int64) error {
	_, err := b.db.ExecContext(ctx, `UPDATE `+b.table+`
		SET owner = '', expires_at = '`+expiredAt+`'
		WHERE node_id = ? AND owner = ?`, nodeID, owner)
	if err != nil {
		return fmt.Errorf("snowflake/mysql: release lease: %w", err)
	}
	return nil
}
